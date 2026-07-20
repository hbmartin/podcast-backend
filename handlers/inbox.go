package handlers

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/moderation"
	"github.com/hbmartin/podcast-backend/pcerrors"
	pb "github.com/hbmartin/podcast-backend/protos/api"
	"github.com/hbmartin/podcast-backend/push"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Send-to-friend + shared-item inbox (Slice 4; pocket-casts-ios docs/Social.md).
// Sender AND recipient must be joined; blocked-either-way sends fail exactly
// like a nonexistent handle (no existence leak). Inbox v1 = read/unread +
// delete; react is deferred until a sender-visible surface exists.

const (
	maxNoteLen       = 500
	maxTitleLen      = 300
	maxInboxPageSize = 50
)

// PostShareSend handles POST /social/share/send.
func (h Handlers) PostShareSend(w http.ResponseWriter, r *http.Request) {
	req := &pb.SharedItemSendRequest{}
	// A show recommendation (Slice 15) carries a podcast and no episode;
	// an episode share carries both. One of the two must be present.
	if err := bindProto(r, req); err != nil || (req.EpisodeUuid == "" && req.PodcastUuid == "") ||
		len(req.EpisodeUuid) > maxUuidFieldLen || len(req.PodcastUuid) > maxUuidFieldLen {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	note := strings.TrimSpace(req.Note)
	if utf8.RuneCountInString(note) > maxNoteLen {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "note too long")
		return
	}
	if err := moderation.CheckText(note); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "note rejected: "+err.Error())
		return
	}
	// Titles are denormalized free text bound for the recipient's inbox and
	// push — they go through the same character-level filter as the note.
	if moderation.CheckText(req.EpisodeTitle) != nil || moderation.CheckText(req.PodcastTitle) != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid title")
		return
	}

	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	// Sender must be joined (the send is attributed).
	senderProfile, err := h.Queries.GetSocialProfileByUserID(r.Context(), user.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			pcerrors.Write(w, http.StatusForbidden, pcerrors.AccessDenied, "join required to send")
			return
		}
		writeError(w, r, err)
		return
	}

	// Resolve the recipient by handle; missing, tombstoned, and
	// blocked-either-way all answer 404 identically.
	handle := normalizeHandle(req.RecipientHandle)
	recipientProfile, err := h.Queries.GetSocialProfileByHandle(r.Context(), handle)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeError(w, r, err)
		return
	}
	if recipientProfile.UserID == user.ID {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "cannot send to yourself")
		return
	}
	blocked, err := h.Queries.IsBlockedEither(r.Context(), db.IsBlockedEitherParams{
		UserID: user.ID, TargetUserID: recipientProfile.UserID,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	if blocked {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	_, err = h.Queries.InsertSharedItem(r.Context(), db.InsertSharedItemParams{
		SenderUserID:     user.ID,
		RecipientUserID:  recipientProfile.UserID,
		EpisodeUuid:      req.EpisodeUuid,
		PodcastUuid:      req.PodcastUuid,
		EpisodeTitle:     truncateRunes(req.EpisodeTitle, maxTitleLen),
		PodcastTitle:     truncateRunes(req.PodcastTitle, maxTitleLen),
		Note:             note,
		TimestampSeconds: max(req.TimestampSeconds, 0),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}

	if h.mutedBy(r, recipientProfile.UserID, user.ID) {
		writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
		return
	}
	pushData := map[string]string{"episode_uuid": req.EpisodeUuid, "podcast_uuid": req.PodcastUuid}
	if req.EpisodeUuid == "" {
		pushData["podcast_only"] = "1"
	}
	h.notifySocial(recipientProfile.UserID, push.SocialPushSharedItem,
		senderProfile.Handle, senderProfile.DisplayName, pushData)
	writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
}

// PostInbox handles POST /social/inbox: the caller's received items.
func (h Handlers) PostInbox(w http.ResponseWriter, r *http.Request) {
	req := &pb.InboxRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	limit := req.Limit
	if limit <= 0 || limit > maxInboxPageSize {
		limit = maxInboxPageSize
	}
	offset := max(req.Offset, 0)

	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}
	// Receiving requires being joined (addressable).
	if _, err := h.Queries.GetSocialProfileByUserID(r.Context(), user.ID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			pcerrors.Write(w, http.StatusForbidden, pcerrors.AccessDenied, "join required")
			return
		}
		writeError(w, r, err)
		return
	}

	rows, err := h.Queries.GetInboxItems(r.Context(), db.GetInboxItemsParams{
		RecipientUserID: user.ID, Viewer: &user.ID, Limit: limit, Offset: offset,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	total, err := h.Queries.CountInboxItems(r.Context(), db.CountInboxItemsParams{RecipientUserID: user.ID, Viewer: &user.ID})
	if err != nil {
		writeError(w, r, err)
		return
	}
	unread, err := h.Queries.CountUnreadInboxItems(r.Context(), db.CountUnreadInboxItemsParams{RecipientUserID: user.ID, Viewer: &user.ID})
	if err != nil {
		writeError(w, r, err)
		return
	}

	resp := &pb.InboxResponse{Total: total, Unread: unread}
	for _, row := range rows {
		resp.Items = append(resp.Items, &pb.SharedItem{
			Id:                row.ID,
			SenderUserId:      row.SenderUuid,
			SenderHandle:      row.SenderHandle,
			SenderDisplayName: row.SenderDisplayName,
			EpisodeUuid:       row.EpisodeUuid,
			PodcastUuid:       row.PodcastUuid,
			EpisodeTitle:      row.EpisodeTitle,
			PodcastTitle:      row.PodcastTitle,
			Note:              row.Note,
			TimestampSeconds:  row.TimestampSeconds,
			CreatedAt:         timestamppb.New(row.CreatedAt),
			Read:              row.Read,
		})
	}

	writeProto(w, http.StatusOK, resp)
}

// PostInboxRead handles POST /social/inbox/read: marks ids read (recipient-scoped).
func (h Handlers) PostInboxRead(w http.ResponseWriter, r *http.Request) {
	req := &pb.InboxMarkReadRequest{}
	if err := bindProto(r, req); err != nil || len(req.Ids) == 0 {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	if err := h.Queries.MarkInboxItemsRead(r.Context(), db.MarkInboxItemsReadParams{
		RecipientUserID: user.ID, Column2: req.Ids,
	}); err != nil {
		writeError(w, r, err)
		return
	}

	writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
}

// PostInboxDelete handles POST /social/inbox/delete (recipient-scoped, idempotent).
func (h Handlers) PostInboxDelete(w http.ResponseWriter, r *http.Request) {
	req := &pb.InboxDeleteRequest{}
	if err := bindProto(r, req); err != nil || req.Id <= 0 {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	if _, err := h.Queries.DeleteInboxItem(r.Context(), db.DeleteInboxItemParams{
		RecipientUserID: user.ID, ID: req.Id,
	}); err != nil {
		writeError(w, r, err)
		return
	}

	writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
}

func truncateRunes(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxRunes])
}
