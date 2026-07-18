package handlers

import (
	"net/http"
	"time"

	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/moderation"
	"github.com/hbmartin/podcast-backend/pcerrors"
	pb "github.com/hbmartin/podcast-backend/protos/api"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// The episode comment tree (Slice 6, ADR-0010): one entity, fully nested,
// tombstoned deletion. Writes require a joined account; top-level comments
// carry the >=25%-played listen-gate, replies carry none; edits are allowed
// only inside a grace window and only until first reply.
const (
	commentMaxLength     = 2000
	commentEditGrace     = 5 * time.Minute
	maxCommentPageSize   = 50
	relationshipKindMute = int16(1)
)

// PostCommentSubmit handles POST /social/comment/submit.
func (h Handlers) PostCommentSubmit(w http.ResponseWriter, r *http.Request) {
	req := &pb.CommentSubmitRequest{}
	if err := bindProto(r, req); err != nil || req.EpisodeUuid == "" || req.Text == "" {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	if len(req.Text) > commentMaxLength {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "comment too long")
		return
	}
	if err := moderation.CheckText(req.Text); err != nil {
		pcerrors.Write(w, http.StatusUnprocessableEntity, pcerrors.AccessDenied, "comment rejected")
		return
	}

	user, profile, ok := h.requireJoined(w, r)
	if !ok {
		return
	}

	params := db.InsertCommentParams{
		EpisodeUuid:  req.EpisodeUuid,
		PodcastUuid:  req.PodcastUuid,
		EpisodeTitle: truncateRunes(req.EpisodeTitle, maxTitleLen),
		PodcastTitle: truncateRunes(req.PodcastTitle, maxTitleLen),
		UserID:       &user.ID,
		Text:         req.Text,
	}

	if req.ParentId > 0 {
		// Reply: parent must exist on the same episode and not be removed.
		// No listen-gate — you answer a person, not the content. Timestamps
		// belong to top-level comments only.
		if req.TimestampSeconds != nil {
			pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "replies cannot carry timestamps")
			return
		}
		parent, err := h.Queries.GetCommentByID(r.Context(), req.ParentId)
		if err != nil || parent.EpisodeUuid != req.EpisodeUuid {
			pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "parent not found")
			return
		}
		parentID := parent.ID
		params.ParentID = &parentID
		rootID := parent.ID
		if parent.RootID != nil {
			rootID = *parent.RootID
		}
		params.RootID = &rootID
	} else {
		// Top-level comment (or Moment when timestamped): the caller must
		// have played >=25% of the episode, per the reaction-gate precedent.
		playback, err := h.Queries.GetEpisodePlaybackForGate(r.Context(), db.GetEpisodePlaybackForGateParams{
			UserID: user.ID, EpisodeUuid: req.EpisodeUuid,
		})
		completed := err == nil && playback.PlayingStatus == 3
		playedEnough := err == nil && playback.Duration > 0 && playback.PlayedUpTo*4 >= playback.Duration
		if !completed && !playedEnough {
			pcerrors.Write(w, http.StatusForbidden, pcerrors.AccessDenied, "listen to more of this episode first")
			return
		}
		if req.TimestampSeconds != nil && *req.TimestampSeconds >= 0 {
			ts := *req.TimestampSeconds
			params.TimestampSeconds = &ts
		}
	}

	inserted, err := h.Queries.InsertComment(r.Context(), params)
	if err != nil {
		writeError(w, r, err)
		return
	}

	resp := &pb.SocialComment{
		Id:               inserted.ID,
		ParentId:         req.ParentId,
		UserId:           user.Uuid,
		Handle:           profile.Handle,
		DisplayName:      profile.DisplayName,
		Text:             req.Text,
		TimestampSeconds: params.TimestampSeconds,
		CreatedAt:        timestamppb.New(inserted.CreatedAt),
	}
	writeProto(w, http.StatusOK, resp)
}

// PostCommentEdit handles POST /social/comment/edit: grace-window edits only —
// the author, within commentEditGrace of posting, and only until first reply.
func (h Handlers) PostCommentEdit(w http.ResponseWriter, r *http.Request) {
	req := &pb.CommentEditRequest{}
	if err := bindProto(r, req); err != nil || req.Id <= 0 || req.Text == "" {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	if len(req.Text) > commentMaxLength {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "comment too long")
		return
	}
	if err := moderation.CheckText(req.Text); err != nil {
		pcerrors.Write(w, http.StatusUnprocessableEntity, pcerrors.AccessDenied, "comment rejected")
		return
	}

	user, _, ok := h.requireJoined(w, r)
	if !ok {
		return
	}

	comment, err := h.Queries.GetCommentByID(r.Context(), req.Id)
	if err != nil || comment.RemovedAt != nil || comment.UserID == nil || *comment.UserID != user.ID {
		pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "comment not found")
		return
	}
	if comment.HasReplies || time.Since(comment.CreatedAt) > commentEditGrace {
		pcerrors.Write(w, http.StatusConflict, pcerrors.AccessDenied, "edit window closed")
		return
	}

	if _, err := h.Queries.EditComment(r.Context(), db.EditCommentParams{
		ID: req.Id, UserID: &user.ID, Text: req.Text,
	}); err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
}

// PostCommentDelete handles POST /social/comment/delete: tombstones the
// caller's comment (text + authorship wiped, tree position kept — ADR-0010).
func (h Handlers) PostCommentDelete(w http.ResponseWriter, r *http.Request) {
	req := &pb.CommentDeleteRequest{}
	if err := bindProto(r, req); err != nil || req.Id <= 0 {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	user, _, ok := h.requireJoined(w, r)
	if !ok {
		return
	}

	affected, err := h.Queries.TombstoneComment(r.Context(), db.TombstoneCommentParams{
		ID: req.Id, UserID: &user.ID,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	if affected == 0 {
		pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "comment not found")
		return
	}
	writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
}

// PostEpisodeComments handles POST /episode/comments (optional auth): the
// top-level page of an episode's tree, newest first. Blocked-either-way and
// muted authors are excluded for authenticated viewers (their subtrees go
// with them — the block contract, ADR-0010); tombstones stay as placeholders.
func (h Handlers) PostEpisodeComments(w http.ResponseWriter, r *http.Request) {
	req := &pb.EpisodeCommentsRequest{}
	if err := bindProto(r, req); err != nil || req.EpisodeUuid == "" {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	limit := req.Limit
	if limit <= 0 || limit > maxCommentPageSize {
		limit = maxCommentPageSize
	}

	rows, err := h.Queries.GetEpisodeComments(r.Context(), db.GetEpisodeCommentsParams{
		EpisodeUuid: req.EpisodeUuid, Limit: limit, Offset: max(req.Offset, 0),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	total, err := h.Queries.CountEpisodeComments(r.Context(), req.EpisodeUuid)
	if err != nil {
		writeError(w, r, err)
		return
	}

	viewerID := h.optionalViewerID(r)
	resp := &pb.CommentsResponse{Total: int32(total)}
	for _, row := range rows {
		if h.commentAuthorHidden(r, viewerID, row.UserID) {
			continue
		}
		resp.Comments = append(resp.Comments, commentToProto(commentRow{
			ID: row.ID, UserID: row.UserID, Text: row.Text,
			TimestampSeconds: row.TimestampSeconds, CreatedAt: row.CreatedAt,
			EditedAt: row.EditedAt, RemovedAt: row.RemovedAt,
			AuthorUuid: row.AuthorUuid, Handle: row.Handle, DisplayName: row.DisplayName,
			ReplyCount: row.ReplyCount,
		}))
	}
	writeProto(w, http.StatusOK, resp)
}

// PostCommentReplies handles POST /social/comment/replies (optional auth):
// one node's direct children, oldest first — the UI expands branches on demand.
func (h Handlers) PostCommentReplies(w http.ResponseWriter, r *http.Request) {
	req := &pb.CommentRepliesRequest{}
	if err := bindProto(r, req); err != nil || req.ParentId <= 0 {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	limit := req.Limit
	if limit <= 0 || limit > maxCommentPageSize {
		limit = maxCommentPageSize
	}

	parentID := req.ParentId
	rows, err := h.Queries.GetCommentReplies(r.Context(), db.GetCommentRepliesParams{
		ParentID: &parentID, Limit: limit, Offset: max(req.Offset, 0),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	total, err := h.Queries.CountCommentReplies(r.Context(), &parentID)
	if err != nil {
		writeError(w, r, err)
		return
	}

	viewerID := h.optionalViewerID(r)
	resp := &pb.CommentsResponse{Total: int32(total)}
	for _, row := range rows {
		if h.commentAuthorHidden(r, viewerID, row.UserID) {
			continue
		}
		var authorUUID *string
		if row.AuthorUuid != nil {
			authorUUID = row.AuthorUuid
		}
		resp.Comments = append(resp.Comments, commentToProto(commentRow{
			ID: row.ID, ParentID: row.ParentID, UserID: row.UserID, Text: row.Text,
			TimestampSeconds: row.TimestampSeconds, CreatedAt: row.CreatedAt,
			EditedAt: row.EditedAt, RemovedAt: row.RemovedAt,
			AuthorUuid: authorUUID, Handle: row.Handle, DisplayName: row.DisplayName,
			ReplyCount: row.ReplyCount,
		}))
	}
	writeProto(w, http.StatusOK, resp)
}

// PostInboxReplies handles POST /social/inbox/replies: direct replies to the
// caller's comments, newest first, with the watermark-based unread count.
func (h Handlers) PostInboxReplies(w http.ResponseWriter, r *http.Request) {
	req := &pb.InboxRepliesRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	user, _, ok := h.requireJoined(w, r)
	if !ok {
		return
	}
	limit := req.Limit
	if limit <= 0 || limit > maxCommentPageSize {
		limit = maxCommentPageSize
	}

	rows, err := h.Queries.GetInboxReplies(r.Context(), db.GetInboxRepliesParams{
		UserID: &user.ID, Limit: limit, Offset: max(req.Offset, 0),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	total, err := h.Queries.CountInboxReplies(r.Context(), &user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	unread, err := h.Queries.CountUnreadInboxReplies(r.Context(), &user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}

	resp := &pb.InboxRepliesResponse{Total: int32(total), Unread: int32(unread)}
	for _, row := range rows {
		if h.commentAuthorHidden(r, user.ID, row.UserID) {
			continue
		}
		authorUUID := row.AuthorUuid
		comment := commentToProto(commentRow{
			ID: row.ID, ParentID: row.ParentID, UserID: row.UserID, Text: row.Text,
			TimestampSeconds: row.TimestampSeconds, CreatedAt: row.CreatedAt,
			EditedAt: row.EditedAt, AuthorUuid: &authorUUID,
			Handle: row.Handle, DisplayName: row.DisplayName, ReplyCount: row.ReplyCount,
		})
		comment.EpisodeUuid = row.EpisodeUuid
		comment.PodcastUuid = row.PodcastUuid
		comment.EpisodeTitle = row.EpisodeTitle
		comment.PodcastTitle = row.PodcastTitle
		resp.Replies = append(resp.Replies, comment)
	}
	writeProto(w, http.StatusOK, resp)
}

// PostInboxRepliesSeen handles POST /social/inbox/replies/seen: advances the
// caller's watermark so everything currently listed counts as read.
func (h Handlers) PostInboxRepliesSeen(w http.ResponseWriter, r *http.Request) {
	user, _, ok := h.requireJoined(w, r)
	if !ok {
		return
	}
	if err := h.Queries.SetRepliesSeen(r.Context(), user.ID); err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
}

// commentRow is the common shape of the three comment list rows.
type commentRow struct {
	ID               int64
	ParentID         *int64
	UserID           *int64
	Text             string
	TimestampSeconds *int32
	CreatedAt        time.Time
	EditedAt         *time.Time
	RemovedAt        *time.Time
	AuthorUuid       *string
	Handle           string
	DisplayName      string
	ReplyCount       int32
}

func commentToProto(row commentRow) *pb.SocialComment {
	comment := &pb.SocialComment{
		Id:         row.ID,
		CreatedAt:  timestamppb.New(row.CreatedAt),
		Edited:     row.EditedAt != nil,
		Removed:    row.RemovedAt != nil,
		ReplyCount: row.ReplyCount,
	}
	if row.ParentID != nil {
		comment.ParentId = *row.ParentID
	}
	// Tombstones ship no text and no author (ADR-0010).
	if row.RemovedAt == nil {
		comment.Text = row.Text
		comment.TimestampSeconds = row.TimestampSeconds
		comment.Handle = row.Handle
		comment.DisplayName = row.DisplayName
		if row.AuthorUuid != nil {
			comment.UserId = *row.AuthorUuid
		}
	}
	return comment
}

// optionalViewerID resolves the DB id of an optionally-authenticated caller;
// 0 when anonymous.
func (h Handlers) optionalViewerID(r *http.Request) int64 {
	ctxUser := getUser(r.Context())
	if ctxUser == nil {
		return 0
	}
	viewer, err := h.Queries.GetUserByUUID(r.Context(), ctxUser.UUID)
	if err != nil {
		return 0
	}
	return viewer.ID
}

// commentAuthorHidden applies the viewer's block (mutual) and mute (one-way)
// relationships to a comment author. Tombstones (nil author) always render.
func (h Handlers) commentAuthorHidden(r *http.Request, viewerID int64, authorID *int64) bool {
	if viewerID == 0 || authorID == nil || *authorID == viewerID {
		return false
	}
	blocked, err := h.Queries.IsBlockedEither(r.Context(), db.IsBlockedEitherParams{
		UserID: viewerID, TargetUserID: *authorID,
	})
	if err == nil && blocked {
		return true
	}
	muted, err := h.Queries.HasSocialRelationship(r.Context(), db.HasSocialRelationshipParams{
		UserID: viewerID, TargetUserID: *authorID, Kind: relationshipKindMute,
	})
	return err == nil && muted
}
