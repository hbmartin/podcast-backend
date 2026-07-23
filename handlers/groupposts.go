package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/metrics"
	"github.com/hbmartin/podcast-backend/moderation"
	"github.com/hbmartin/podcast-backend/pcerrors"
	pb "github.com/hbmartin/podcast-backend/protos/api"
	"github.com/hbmartin/podcast-backend/push"
	"github.com/hbmartin/podcast-backend/tasks"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Group posts (Slice 13, ADR-0012): deliberate shares into a group - an
// episode, a shared list, or plain text - with threaded replies carrying the
// comment-tree semantics (tombstones, grace-window edit, filters, block
// invisibility).

// PostGroupPostSubmit handles POST /social/group/post/submit (member only).
func (h Handlers) PostGroupPostSubmit(w http.ResponseWriter, r *http.Request) {
	req := &pb.GroupPostRequest{}
	if err := bindProto(r, req); err != nil || req.GroupId <= 0 || req.Text == "" {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	if len(req.Text) > groupPostMaxLen {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "post too long")
		return
	}
	if err := moderation.CheckText(req.Text); err != nil {
		pcerrors.Write(w, http.StatusUnprocessableEntity, pcerrors.AccessDenied, "post rejected")
		return
	}
	if req.EpisodeUuid != "" && len(req.EpisodeUuid) > maxUuidFieldLen {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid episode")
		return
	}
	user, profile, ok := h.requireJoined(w, r)
	if !ok {
		return
	}
	member, err := h.Queries.GetGroupMember(r.Context(), db.GetGroupMemberParams{GroupID: req.GroupId, UserID: user.ID})
	if err != nil || (member.Role != groupRoleMember && member.Role != groupRoleOwner) {
		pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "group not found")
		return
	}

	params := db.InsertGroupPostParams{
		GroupID: req.GroupId, UserID: &user.ID, Text: req.Text,
		EpisodeUuid: req.EpisodeUuid, PodcastUuid: truncateRunes(req.PodcastUuid, maxUuidFieldLen),
		EpisodeTitle: truncateRunes(req.EpisodeTitle, maxTitleLen),
		PodcastTitle: truncateRunes(req.PodcastTitle, maxTitleLen),
		ListID:       req.ListId, ListTitle: truncateRunes(req.ListTitle, maxTitleLen),
	}

	var parentAuthor *int64
	if req.ParentId > 0 {
		parent, err := h.Queries.GetGroupPostByID(r.Context(), req.ParentId)
		if err != nil || parent.GroupID != req.GroupId || parent.RemovedAt != nil {
			pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "post not found")
			return
		}
		if parent.UserID != nil && *parent.UserID != user.ID {
			blocked, err := h.Queries.IsBlockedEither(r.Context(), db.IsBlockedEitherParams{
				UserID: user.ID, TargetUserID: *parent.UserID,
			})
			if err != nil {
				writeError(w, r, err)
				return
			}
			if blocked {
				pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "post not found")
				return
			}
			parentAuthor = parent.UserID
		}
		parentID := parent.ID
		params.ParentID = &parentID
		rootID := parent.ID
		if parent.RootID != nil {
			rootID = *parent.RootID
		}
		params.RootID = &rootID
	}

	inserted, err := h.Queries.InsertGroupPost(r.Context(), params)
	if err != nil {
		writeError(w, r, err)
		return
	}

	groupData := map[string]string{
		"group_id": strconv.FormatInt(req.GroupId, 10),
		"post_id":  strconv.FormatInt(inserted.ID, 10),
	}
	if parentAuthor != nil && !h.mutedBy(r, *parentAuthor, user.ID) {
		// Replies ride the existing reply notification machinery (type 5),
		// with group payload keys for the deep link.
		h.notifySocial(*parentAuthor, push.SocialPushCommentReply, profile.Handle, profile.DisplayName, groupData)
	}
	if req.ParentId == 0 {
		h.dispatchGroupPostFanout(tasks.GroupPostFanoutPayload{
			GroupID: req.GroupId, PostID: inserted.ID, ActorUserID: user.ID,
			ActorHandle: profile.Handle, ActorDisplayName: profile.DisplayName,
		})
	}

	writeProto(w, http.StatusOK, &pb.GroupPost{
		Id: inserted.ID, GroupId: req.GroupId, ParentId: req.ParentId,
		UserId: user.Uuid, Handle: profile.Handle, DisplayName: profile.DisplayName,
		Text: req.Text, EpisodeUuid: params.EpisodeUuid, PodcastUuid: params.PodcastUuid,
		EpisodeTitle: params.EpisodeTitle, PodcastTitle: params.PodcastTitle,
		ListId: params.ListID, ListTitle: params.ListTitle,
		CreatedAt: timestamppb.New(inserted.CreatedAt),
	})
}

var directGroupFanoutSem = make(chan struct{}, 4)

func (h Handlers) dispatchGroupPostFanout(payload tasks.GroupPostFanoutPayload) {
	if h.Queue != nil {
		if err := h.Queue.EnqueueGroupPostFanout(context.Background(), payload); err != nil {
			slog.Warn("group post fanout enqueue failed", "post_id", payload.PostID, "error", err)
		}
		return
	}
	select {
	case directGroupFanoutSem <- struct{}{}:
	default:
		metrics.GroupFanoutDropped.Inc()
		slog.Warn("group post fanout dropped; concurrency limit reached", "post_id", payload.PostID)
		return
	}
	// nosemgrep: go.request-loop-unbounded-goroutine -- fallback is globally bounded by directGroupFanoutSem.
	go func() {
		defer func() { <-directGroupFanoutSem }()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		targets, err := h.Queries.GetGroupNotifyTargets(ctx, db.GetGroupNotifyTargetsParams{
			GroupID: payload.GroupID, UserID: payload.ActorUserID,
		})
		if err != nil {
			slog.Warn("group post fanout target query failed", "post_id", payload.PostID, "error", err)
			return
		}
		data := map[string]string{
			"group_id": strconv.FormatInt(payload.GroupID, 10),
			"post_id":  strconv.FormatInt(payload.PostID, 10),
		}
		for _, target := range targets {
			h.notifySocial(target, push.SocialPushGroupPost,
				payload.ActorHandle, payload.ActorDisplayName, data)
		}
	}()
}

// PostGroupPosts handles POST /social/group/posts: private groups are
// member-only (no-leak 404); public group feeds are readable by any caller
// (optional auth), consistent with public shared lists.
func (h Handlers) PostGroupPosts(w http.ResponseWriter, r *http.Request) {
	req := &pb.GroupPostsRequest{}
	if err := bindProto(r, req); err != nil || req.GroupId <= 0 {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	viewerID := h.optionalViewerID(r)
	group, err := h.Queries.GetSocialGroup(r.Context(), db.GetSocialGroupParams{ID: req.GroupId, Viewer: viewerRef(viewerID)})
	if err != nil {
		pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "group not found")
		return
	}
	role := int16(group.YourRole)
	if group.Visibility != groupVisPublic && role != groupRoleMember && role != groupRoleOwner {
		pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "group not found")
		return
	}
	limit := req.Limit
	if limit <= 0 || limit > maxGroupPageSize {
		limit = maxGroupPageSize
	}
	var parentRef *int64
	if req.ParentId > 0 {
		parentRef = &req.ParentId
	}
	rows, err := h.Queries.GetGroupPosts(r.Context(), db.GetGroupPostsParams{
		GroupID: req.GroupId, ParentID: parentRef, Viewer: viewerRef(viewerID),
		Limit: limit, Offset: max(req.Offset, 0),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	total, err := h.Queries.CountGroupPosts(r.Context(), db.CountGroupPostsParams{
		GroupID: req.GroupId, ParentID: parentRef, Viewer: viewerRef(viewerID),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}

	resp := &pb.GroupPostsResponse{Total: int32(total)}
	if req.ParentId == 0 {
		resp.Group = &pb.SocialGroup{
			Id: group.ID, OwnerHandle: group.OwnerHandle, OwnerDisplayName: group.OwnerDisplayName,
			Title: group.Title, Description: group.Description,
			Visibility: pb.SocialVisibility(group.Visibility), PodcastUuid: group.PodcastUuid,
			PodcastTitle: group.PodcastTitle, MemberCount: group.MemberCount,
			YourRole: pb.GroupRole(group.YourRole), NotifyPosts: group.NotifyPosts,
			CreatedAt: timestamppb.New(group.CreatedAt),
		}
	}
	for _, row := range rows {
		post := &pb.GroupPost{
			Id: row.ID, GroupId: req.GroupId,
			CreatedAt: timestamppb.New(row.CreatedAt),
			Edited:    row.EditedAt != nil, Removed: row.RemovedAt != nil,
			ReplyCount: row.ReplyCount,
		}
		if row.ParentID != nil {
			post.ParentId = *row.ParentID
		}
		// Tombstones ship no text, author, or attachments (ADR-0010 rules).
		if row.RemovedAt == nil {
			post.Text = row.Text
			post.EpisodeUuid = row.EpisodeUuid
			post.PodcastUuid = row.PodcastUuid
			post.EpisodeTitle = row.EpisodeTitle
			post.PodcastTitle = row.PodcastTitle
			post.ListId = row.ListID
			post.ListTitle = row.ListTitle
			if row.Handle.Valid {
				post.Handle = row.Handle.String
			}
			if row.DisplayName != nil {
				post.DisplayName = *row.DisplayName
			}
			if row.AuthorUuid != nil {
				post.UserId = *row.AuthorUuid
			}
		}
		resp.Posts = append(resp.Posts, post)
	}
	writeProto(w, http.StatusOK, resp)
}

// PostGroupPostEdit handles POST /social/group/post/edit: author only,
// inside the grace window, until first reply.
func (h Handlers) PostGroupPostEdit(w http.ResponseWriter, r *http.Request) {
	req := &pb.GroupPostEditRequest{}
	if err := bindProto(r, req); err != nil || req.Id <= 0 || req.Text == "" {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	if len(req.Text) > groupPostMaxLen {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "post too long")
		return
	}
	if err := moderation.CheckText(req.Text); err != nil {
		pcerrors.Write(w, http.StatusUnprocessableEntity, pcerrors.AccessDenied, "post rejected")
		return
	}
	user, _, ok := h.requireJoined(w, r)
	if !ok {
		return
	}
	post, err := h.Queries.GetGroupPostByID(r.Context(), req.Id)
	if err != nil || post.RemovedAt != nil || post.UserID == nil || *post.UserID != user.ID {
		pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "post not found")
		return
	}
	if post.HasReplies || time.Since(post.CreatedAt) > groupPostEditGrace {
		pcerrors.Write(w, http.StatusConflict, pcerrors.AccessDenied, "edit window closed")
		return
	}
	if _, err := h.Queries.EditGroupPost(r.Context(), db.EditGroupPostParams{
		ID: req.Id, UserID: &user.ID, Text: req.Text,
	}); err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
}

// PostGroupPostDelete handles POST /social/group/post/delete: the author
// tombstones their own post; the group owner tombstones anyone's.
func (h Handlers) PostGroupPostDelete(w http.ResponseWriter, r *http.Request) {
	req := &pb.GroupPostDeleteRequest{}
	if err := bindProto(r, req); err != nil || req.Id <= 0 {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	user, _, ok := h.requireJoined(w, r)
	if !ok {
		return
	}
	affected, err := h.Queries.TombstoneGroupPost(r.Context(), db.TombstoneGroupPostParams{ID: req.Id, UserID: &user.ID})
	if err != nil {
		writeError(w, r, err)
		return
	}
	if affected == 0 {
		affected, err = h.Queries.TombstoneGroupPostAsOwner(r.Context(), db.TombstoneGroupPostAsOwnerParams{
			ID: req.Id, OwnerUserID: user.ID,
		})
		if err != nil {
			writeError(w, r, err)
			return
		}
	}
	if affected == 0 {
		pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "post not found")
		return
	}
	writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
}
