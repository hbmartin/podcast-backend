package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/pcerrors"
	pb "github.com/hbmartin/podcast-backend/protos/api"
	"github.com/hbmartin/podcast-backend/push"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Follow graph + activity feed (Slice 5; pocket-casts-ios docs/Social.md,
// ADR-0009). Follows are open by default; followees with
// require_follow_approval turn new follows into pending requests. Blocked-
// either-way answers 404 like a missing handle. The feed derives at read time
// from followees' rows — no events table.

const (
	followStatusPending int16 = 0
	followStatusActive  int16 = 1

	maxFollowPageSize = 100
	maxFeedPageSize   = 50
)

// requireJoined resolves the caller to their joined profile, answering 403
// when the account hasn't joined.
func (h Handlers) requireJoined(w http.ResponseWriter, r *http.Request) (db.User, db.SocialProfile, bool) {
	user, ok := h.currentDbUser(w, r)
	if !ok {
		return db.User{}, db.SocialProfile{}, false
	}
	profile, err := h.Queries.GetSocialProfileByUserID(r.Context(), user.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			pcerrors.Write(w, http.StatusForbidden, pcerrors.AccessDenied, "join required")
		} else {
			writeError(w, r, err)
		}
		return db.User{}, db.SocialProfile{}, false
	}
	return user, profile, true
}

// resolveFollowTarget resolves a handle to a followable profile, applying the
// no-leak rule (missing, tombstoned and blocked-either-way all 404) and
// rejecting self-targets.
func (h Handlers) resolveFollowTarget(w http.ResponseWriter, r *http.Request, callerID int64, rawHandle string) (db.SocialProfile, bool) {
	target, err := h.Queries.GetSocialProfileByHandle(r.Context(), normalizeHandle(rawHandle))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			w.WriteHeader(http.StatusNotFound)
		} else {
			writeError(w, r, err)
		}
		return db.SocialProfile{}, false
	}
	if target.UserID == callerID {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "cannot target yourself")
		return db.SocialProfile{}, false
	}
	blocked, err := h.Queries.IsBlockedEither(r.Context(), db.IsBlockedEitherParams{
		UserID: callerID, TargetUserID: target.UserID,
	})
	if err != nil {
		writeError(w, r, err)
		return db.SocialProfile{}, false
	}
	if blocked {
		w.WriteHeader(http.StatusNotFound)
		return db.SocialProfile{}, false
	}
	return target, true
}

// PostFollow handles POST /social/follow.
func (h Handlers) PostFollow(w http.ResponseWriter, r *http.Request) {
	req := &pb.FollowRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	user, profile, ok := h.requireJoined(w, r)
	if !ok {
		return
	}
	target, ok := h.resolveFollowTarget(w, r, user.ID, req.Handle)
	if !ok {
		return
	}

	status := followStatusActive
	if target.RequireFollowApproval {
		status = followStatusPending
	}
	inserted, err := h.Queries.UpsertFollow(r.Context(), db.UpsertFollowParams{
		FollowerUserID: user.ID, FolloweeUserID: target.UserID, Status: status,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}

	// Report the actual stored state (an existing row wins over this request).
	stored, err := h.Queries.GetFollowState(r.Context(), db.GetFollowStateParams{
		FollowerUserID: user.ID, FolloweeUserID: target.UserID,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	state := pb.FollowState_FOLLOW_STATE_PENDING
	pushType := push.SocialPushFollowRequest
	if stored == followStatusActive {
		state = pb.FollowState_FOLLOW_STATE_ACTIVE
		pushType = push.SocialPushNewFollower
	}
	// Only a NEW row notifies: idempotent re-follows must not re-push, and
	// the target's mute suppresses the push outright (QA findings).
	if inserted > 0 && !h.mutedBy(r, target.UserID, user.ID) {
		h.notifySocial(target.UserID, pushType, profile.Handle, profile.DisplayName, nil)
	}
	writeProto(w, http.StatusOK, &pb.FollowResponse{State: state})
}

// PostUnfollow handles POST /social/unfollow (also cancels pending). Idempotent.
func (h Handlers) PostUnfollow(w http.ResponseWriter, r *http.Request) {
	req := &pb.UnfollowRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	user, _, ok := h.requireJoined(w, r)
	if !ok {
		return
	}
	target, err := h.Queries.GetSocialProfileByHandle(r.Context(), normalizeHandle(req.Handle))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeError(w, r, err)
		return
	}

	if _, err := h.Queries.DeleteFollow(r.Context(), db.DeleteFollowParams{
		FollowerUserID: user.ID, FolloweeUserID: target.UserID,
	}); err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
}

// PostFollowList handles POST /social/follows: either side of the caller's graph.
func (h Handlers) PostFollowList(w http.ResponseWriter, r *http.Request) {
	req := &pb.FollowListRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	limit := req.Limit
	if limit <= 0 || limit > maxFollowPageSize {
		limit = maxFollowPageSize
	}
	offset := max(req.Offset, 0)

	user, _, ok := h.requireJoined(w, r)
	if !ok {
		return
	}

	resp := &pb.FollowListResponse{}
	if req.Followers {
		rows, err := h.Queries.GetFollowers(r.Context(), db.GetFollowersParams{
			FolloweeUserID: user.ID, Limit: limit, Offset: offset,
		})
		if err != nil {
			writeError(w, r, err)
			return
		}
		for _, row := range rows {
			resp.Entries = append(resp.Entries, followEntry(row.UserUuid, row.Handle, row.DisplayName, row.Status))
		}
		if resp.Total, err = h.Queries.CountFollowers(r.Context(), user.ID); err != nil {
			writeError(w, r, err)
			return
		}
	} else {
		rows, err := h.Queries.GetFollowing(r.Context(), db.GetFollowingParams{
			FollowerUserID: user.ID, Limit: limit, Offset: offset,
		})
		if err != nil {
			writeError(w, r, err)
			return
		}
		for _, row := range rows {
			resp.Entries = append(resp.Entries, followEntry(row.UserUuid, row.Handle, row.DisplayName, row.Status))
		}
		if resp.Total, err = h.Queries.CountFollowing(r.Context(), user.ID); err != nil {
			writeError(w, r, err)
			return
		}
	}
	writeProto(w, http.StatusOK, resp)
}

// PostFollowRequests handles POST /social/follow/requests: pending requests
// awaiting the caller's approval.
func (h Handlers) PostFollowRequests(w http.ResponseWriter, r *http.Request) {
	req := &pb.FollowRequestsRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	limit := req.Limit
	if limit <= 0 || limit > maxFollowPageSize {
		limit = maxFollowPageSize
	}

	user, _, ok := h.requireJoined(w, r)
	if !ok {
		return
	}

	rows, err := h.Queries.GetPendingFollowRequests(r.Context(), db.GetPendingFollowRequestsParams{
		FolloweeUserID: user.ID, Limit: limit, Offset: max(req.Offset, 0),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	pendingTotal, err := h.Queries.CountPendingFollowRequests(r.Context(), user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	resp := &pb.FollowListResponse{Total: pendingTotal}
	for _, row := range rows {
		resp.Entries = append(resp.Entries, followEntry(row.UserUuid, row.Handle, row.DisplayName, row.Status))
	}
	writeProto(w, http.StatusOK, resp)
}

// PostFollowApprove handles POST /social/follow/approve: accept keeps the
// follow (now active); decline removes it. Idempotent either way.
func (h Handlers) PostFollowApprove(w http.ResponseWriter, r *http.Request) {
	req := &pb.FollowApprovalRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	user, profile, ok := h.requireJoined(w, r)
	if !ok {
		return
	}
	requester, err := h.Queries.GetSocialProfileByHandle(r.Context(), normalizeHandle(req.RequesterHandle))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeError(w, r, err)
		return
	}

	var affected int64
	if req.Accept {
		affected, err = h.Queries.ApproveFollow(r.Context(), db.ApproveFollowParams{
			FollowerUserID: requester.UserID, FolloweeUserID: user.ID,
		})
	} else {
		affected, err = h.Queries.DeleteFollow(r.Context(), db.DeleteFollowParams{
			FollowerUserID: requester.UserID, FolloweeUserID: user.ID,
		})
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	// Only a real pending→active transition notifies — approving a
	// nonexistent request must never manufacture a push (QA finding).
	if req.Accept && affected > 0 {
		h.notifySocial(requester.UserID, push.SocialPushFollowApproved, profile.Handle, profile.DisplayName, nil)
	}
	writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
}

// PostFeed handles POST /social/feed (ADR-0009: derived at read time).
func (h Handlers) PostFeed(w http.ResponseWriter, r *http.Request) {
	req := &pb.FeedRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	limit := req.Limit
	if limit <= 0 || limit > maxFeedPageSize {
		limit = maxFeedPageSize
	}
	var before *time.Time
	if req.BeforeUnixMs > 0 {
		t := time.UnixMilli(req.BeforeUnixMs)
		before = &t
	}

	user, _, ok := h.requireJoined(w, r)
	if !ok {
		return
	}

	rows, err := h.Queries.GetFeedItems(r.Context(), db.GetFeedItemsParams{
		UserID: user.ID, Limit: limit, Before: before,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}

	resp := &pb.FeedResponse{}
	for _, row := range rows {
		item := &pb.FeedItem{
			Kind:             pb.FeedItemKind(row.Kind),
			ActorHandle:      row.ActorHandle,
			ActorDisplayName: row.ActorDisplayName,
			ActorUserId:      row.ActorUuid,
			PodcastUuid:      row.PodcastUuid,
			PodcastTitle:     row.PodcastTitle,
			EpisodeUuid:      row.EpisodeUuid,
			EpisodeTitle:     row.EpisodeTitle,
			TargetHandle:     row.TargetHandle,
			ReactionKind:     pb.ReactionKind(row.ReactionKind),
			ReviewExcerpt:    row.ReviewExcerpt,
			ListTitle:        row.ListTitle,
			ListId:           row.ListID,
			EventAt:          timestamppb.New(row.EventAt),
		}
		// Kind 9 rides the list columns in SQL (see GetFeedItems arm 9);
		// remap onto the dedicated group fields here.
		if item.Kind == pb.FeedItemKind_FEED_ITEM_KIND_JOINED_GROUP {
			item.GroupId, item.GroupTitle = item.ListId, item.ListTitle
			item.ListId, item.ListTitle = 0, ""
		}
		resp.Items = append(resp.Items, item)
	}
	writeProto(w, http.StatusOK, resp)
}

func followEntry(uuid, handle, displayName string, status int16) *pb.FollowEntry {
	state := pb.FollowState_FOLLOW_STATE_PENDING
	if status == followStatusActive {
		state = pb.FollowState_FOLLOW_STATE_ACTIVE
	}
	return &pb.FollowEntry{Handle: handle, DisplayName: displayName, UserId: uuid, State: state}
}
