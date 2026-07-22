package handlers

import (
	"github.com/jackc/pgx/v5"
	"net/http"
	"strconv"
	"time"

	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/moderation"
	"github.com/hbmartin/podcast-backend/pcerrors"
	pb "github.com/hbmartin/podcast-backend/protos/api"
	"github.com/hbmartin/podcast-backend/push"
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
	// Cap for uuid-shaped identifier fields (QA finding: unbounded storage).
	maxUuidFieldLen = 64
	// Slice 12: transcript quote cap (runes).
	quoteMaxLength = 300
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

	boundedQuote := truncateRunes(req.Quote, quoteMaxLength)
	if boundedQuote != "" {
		// A quote always implies a Moment: top-level + timestamped. The
		// quote is UGC-adjacent (it can be hand-edited client-side), so it
		// goes through the same filter as the text.
		if req.ParentId > 0 || req.TimestampSeconds == nil || *req.TimestampSeconds < 0 {
			pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "quotes require a timestamp")
			return
		}
		if err := moderation.CheckText(boundedQuote); err != nil {
			pcerrors.Write(w, http.StatusUnprocessableEntity, pcerrors.AccessDenied, "quote rejected")
			return
		}
	}

	user, profile, ok := h.requireJoined(w, r)
	if !ok {
		return
	}

	// ADR-0015: store canonically so device- and catalog-keyed viewers see
	// one thread. The listen-gate below still checks the RAW uuid too —
	// playback rows sync under the device's uuid.
	canonicalUuid := h.canonicalEpisodeUuid(r, req.EpisodeUuid)

	params := db.InsertCommentParams{
		EpisodeUuid:  canonicalUuid,
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
		if err != nil || parent.EpisodeUuid != canonicalUuid || parent.RemovedAt != nil {
			pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "parent not found")
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
				pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "parent not found")
				return
			}
		}
		parentID := parent.ID
		params.ParentID = &parentID
		rootID := parent.ID
		if parent.RootID != nil {
			rootID = *parent.RootID
		}
		params.RootID = &rootID
		// Replies inherit the seed's denormalized context so inbox rows
		// render titles without the client re-sending them.
		if params.PodcastUuid == "" {
			params.PodcastUuid = parent.PodcastUuid
		}
		if params.EpisodeTitle == "" {
			params.EpisodeTitle = parent.EpisodeTitle
		}
		if params.PodcastTitle == "" {
			params.PodcastTitle = parent.PodcastTitle
		}
	} else {
		// Top-level comment (or Moment when timestamped): the caller must
		// have played >=25% of the episode, per the reaction-gate precedent.
		// The gate checks every identity the episode is known under: the
		// submitted uuid, the canonical form, and any device aliases —
		// playback rows sync under whichever scheme the device uses.
		gateUuids := []string{req.EpisodeUuid}
		if canonicalUuid != req.EpisodeUuid {
			gateUuids = append(gateUuids, canonicalUuid)
		}
		if aliases, aliasErr := h.Queries.ReverseEpisodeAliases(r.Context(), canonicalUuid); aliasErr == nil {
			gateUuids = append(gateUuids, aliases...)
		}
		var playback db.GetEpisodePlaybackForGateRow
		err := pgx.ErrNoRows
		for _, gateUuid := range gateUuids {
			playback, err = h.Queries.GetEpisodePlaybackForGate(r.Context(), db.GetEpisodePlaybackForGateParams{
				UserID: user.ID, EpisodeUuid: gateUuid,
			})
			if err == nil {
				break
			}
		}
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
		params.Quote = boundedQuote
		params.QuoteSource = max(req.QuoteSource, 0)
		params.QuoteSegment = max(req.QuoteSegment, 0)
		if params.Quote == "" {
			params.QuoteSource, params.QuoteSegment = 0, 0
		}
	}

	inserted, err := h.Queries.InsertComment(r.Context(), params)
	if err != nil {
		writeError(w, r, err)
		return
	}

	// A reply notifies the parent's author (Slice 8) — never yourself, and
	// tombstoned parents have no author to notify.
	if req.ParentId > 0 {
		if parent, err := h.Queries.GetCommentByID(r.Context(), req.ParentId); err == nil &&
			parent.UserID != nil && *parent.UserID != user.ID &&
			!h.mutedBy(r, *parent.UserID, user.ID) {
			focusID := parent.ID
			if parent.RootID != nil {
				focusID = *parent.RootID
			}
			h.notifySocial(*parent.UserID, push.SocialPushCommentReply,
				profile.Handle, profile.DisplayName,
				map[string]string{
					"episode_uuid": req.EpisodeUuid,
					"podcast_uuid": params.PodcastUuid,
					"comment_id":   strconv.FormatInt(focusID, 10),
				})
		}
	}

	resp := &pb.SocialComment{
		Id:               inserted.ID,
		ParentId:         req.ParentId,
		UserId:           user.Uuid,
		Handle:           profile.Handle,
		DisplayName:      profile.DisplayName,
		Text:             req.Text,
		TimestampSeconds: params.TimestampSeconds,
		Quote:            params.Quote,
		QuoteSource:      params.QuoteSource,
		QuoteSegment:     params.QuoteSegment,
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

	viewerID := h.optionalViewerID(r)
	episodeUuid := h.canonicalEpisodeUuid(r, req.EpisodeUuid)
	rows, err := h.Queries.GetEpisodeComments(r.Context(), db.GetEpisodeCommentsParams{
		EpisodeUuid: episodeUuid, Limit: limit, Offset: max(req.Offset, 0),
		Viewer: viewerRef(viewerID),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	total, err := h.Queries.CountEpisodeComments(r.Context(), db.CountEpisodeCommentsParams{
		EpisodeUuid: episodeUuid, Viewer: viewerRef(viewerID),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}

	resp := &pb.CommentsResponse{Total: int32(total)}
	for _, row := range rows {
		resp.Comments = append(resp.Comments, commentToProto(commentRow{
			ID: row.ID, UserID: row.UserID, Text: row.Text,
			TimestampSeconds: row.TimestampSeconds, CreatedAt: row.CreatedAt,
			EditedAt: row.EditedAt, RemovedAt: row.RemovedAt,
			AuthorUuid: row.AuthorUuid, Handle: row.Handle, DisplayName: row.DisplayName,
			ReplyCount: row.ReplyCount,
			Quote:      row.Quote, QuoteSource: row.QuoteSource, QuoteSegment: row.QuoteSegment,
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
	viewerID := h.optionalViewerID(r)
	rows, err := h.Queries.GetCommentReplies(r.Context(), db.GetCommentRepliesParams{
		ParentID: &parentID, Limit: limit, Offset: max(req.Offset, 0),
		Viewer: viewerRef(viewerID),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	total, err := h.Queries.CountCommentReplies(r.Context(), db.CountCommentRepliesParams{
		ParentID: &parentID, Viewer: viewerRef(viewerID),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}

	resp := &pb.CommentsResponse{Total: int32(total)}
	for _, row := range rows {
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
			Quote:      row.Quote, QuoteSource: row.QuoteSource, QuoteSegment: row.QuoteSegment,
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
		UserID: &user.ID, Limit: limit, Offset: max(req.Offset, 0), Viewer: &user.ID,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	total, err := h.Queries.CountInboxReplies(r.Context(), db.CountInboxRepliesParams{
		UserID: &user.ID, Viewer: &user.ID,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	unread, err := h.Queries.CountUnreadInboxReplies(r.Context(), db.CountUnreadInboxRepliesParams{
		UserID: &user.ID, Viewer: &user.ID,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}

	resp := &pb.InboxRepliesResponse{Total: int32(total), Unread: int32(unread)}
	for _, row := range rows {
		authorUUID := row.AuthorUuid
		comment := commentToProto(commentRow{
			ID: row.ID, ParentID: row.ParentID, UserID: row.UserID, Text: row.Text,
			TimestampSeconds: row.TimestampSeconds, CreatedAt: row.CreatedAt,
			EditedAt: row.EditedAt, AuthorUuid: &authorUUID,
			Handle: row.Handle, DisplayName: row.DisplayName, ReplyCount: row.ReplyCount,
			Quote: row.Quote, QuoteSource: row.QuoteSource, QuoteSegment: row.QuoteSegment,
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
	Quote            string
	QuoteSource      int32
	QuoteSegment     int32
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
		comment.Quote = row.Quote
		comment.QuoteSource = row.QuoteSource
		comment.QuoteSegment = row.QuoteSegment
		comment.Handle = row.Handle
		comment.DisplayName = row.DisplayName
		if row.AuthorUuid != nil {
			comment.UserId = *row.AuthorUuid
		}
	}
	return comment
}

// canonicalEpisodeUuid resolves a device-scheme episode uuid to the catalog
// uuid when the alias bridge knows it (ADR-0015); unknown uuids pass through
// so uncataloged episodes keep working device-keyed.
func (h Handlers) canonicalEpisodeUuid(r *http.Request, episodeUuid string) string {
	if episodeUuid == "" {
		return episodeUuid
	}
	if canonical, err := h.Queries.ResolveEpisodeAlias(r.Context(), episodeUuid); err == nil && canonical != "" {
		return canonical
	}
	return episodeUuid
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

// viewerRef converts an optional viewer id to the SQL narg form.
func viewerRef(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}

// mutedBy reports whether `owner` has muted `actor` (push suppression).
func (h Handlers) mutedBy(r *http.Request, owner, actor int64) bool {
	muted, err := h.Queries.HasSocialRelationship(r.Context(), db.HasSocialRelationshipParams{
		UserID: owner, TargetUserID: actor, Kind: relationshipKindMute,
	})
	return err == nil && muted
}
