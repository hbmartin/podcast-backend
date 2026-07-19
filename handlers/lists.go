package handlers

import (
	"net/http"
	"strconv"

	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/moderation"
	"github.com/hbmartin/podcast-backend/pcerrors"
	pb "github.com/hbmartin/podcast-backend/protos/api"
	"github.com/hbmartin/podcast-backend/push"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Shared lists (Slice 7, ADR-0011): first-class multi-writer social objects;
// device playlists are read-through mirrors. Owner invites collaborators
// (Inbox accept/decline); all editors add/remove/move entries (server LWW);
// visibility reuses the ADR-0006 tiers; the list dies with its owner.
const (
	listTitleMaxLength       = 200
	listDescriptionMaxLength = 1000
	listEntriesMax           = 500
	listCollaboratorsMax     = 20
	maxListEntryPageSize     = 100

	listRoleInvited      = int16(0)
	listRoleCollaborator = int16(1)
	listRoleSubscriber   = int16(2)
)

// PostSocialListCreate handles POST /social/list/create.
func (h Handlers) PostSocialListCreate(w http.ResponseWriter, r *http.Request) {
	req := &pb.SharedListCreateRequest{}
	if err := bindProto(r, req); err != nil || req.Title == "" {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	if len(req.Title) > listTitleMaxLength || len(req.Description) > listDescriptionMaxLength ||
		len(req.Entries) > listEntriesMax {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "list too large")
		return
	}
	if moderation.CheckText(req.Title) != nil || moderation.CheckText(req.Description) != nil {
		pcerrors.Write(w, http.StatusUnprocessableEntity, pcerrors.AccessDenied, "list rejected")
		return
	}

	user, profile, ok := h.requireJoined(w, r)
	if !ok {
		return
	}

	created, err := h.Queries.CreateSocialList(r.Context(), db.CreateSocialListParams{
		OwnerUserID: user.ID,
		Title:       req.Title,
		Description: req.Description,
		Visibility:  visibilityToStored(req.Visibility),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}

	// Initial snapshot (materialize-to-share): positions follow given order.
	for index, entry := range req.Entries {
		if entry.EpisodeUuid == "" || len(entry.EpisodeUuid) > maxUuidFieldLen ||
			len(entry.PodcastUuid) > maxUuidFieldLen {
			continue
		}
		if err := h.Queries.UpsertSocialListEntry(r.Context(), db.UpsertSocialListEntryParams{
			ListID:       created.ID,
			EpisodeUuid:  entry.EpisodeUuid,
			PodcastUuid:  entry.PodcastUuid,
			EpisodeTitle: truncateRunes(entry.EpisodeTitle, maxTitleLen),
			PodcastTitle: truncateRunes(entry.PodcastTitle, maxTitleLen),
			Position:     int32(index),
			AddedBy:      &user.ID,
		}); err != nil {
			writeError(w, r, err)
			return
		}
	}

	resp := &pb.SharedList{
		Id:               created.ID,
		OwnerHandle:      profile.Handle,
		OwnerDisplayName: profile.DisplayName,
		Title:            req.Title,
		Description:      req.Description,
		Visibility:       pb.SocialVisibility(visibilityToStored(req.Visibility)),
		CreatedAt:        timestamppb.New(created.CreatedAt),
		UpdatedAt:        timestamppb.New(created.UpdatedAt),
		EntryCount:       int32(len(req.Entries)),
		YourRole:         pb.SharedListRole_SHARED_LIST_ROLE_OWNER,
	}
	writeProto(w, http.StatusOK, resp)
}

// PostSocialListUpdate handles POST /social/list/update (owner only).
func (h Handlers) PostSocialListUpdate(w http.ResponseWriter, r *http.Request) {
	req := &pb.SharedListUpdateRequest{}
	if err := bindProto(r, req); err != nil || req.ListId <= 0 || req.Title == "" {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	if len(req.Title) > listTitleMaxLength || len(req.Description) > listDescriptionMaxLength {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "list too large")
		return
	}
	if moderation.CheckText(req.Title) != nil || moderation.CheckText(req.Description) != nil {
		pcerrors.Write(w, http.StatusUnprocessableEntity, pcerrors.AccessDenied, "list rejected")
		return
	}

	user, _, ok := h.requireJoined(w, r)
	if !ok {
		return
	}

	affected, err := h.Queries.UpdateSocialList(r.Context(), db.UpdateSocialListParams{
		ID: req.ListId, OwnerUserID: user.ID,
		Title: req.Title, Description: req.Description,
		Visibility: visibilityToStored(req.Visibility),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	if affected == 0 {
		pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "list not found")
		return
	}
	writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
}

// PostSocialListDelete handles POST /social/list/delete (owner only).
func (h Handlers) PostSocialListDelete(w http.ResponseWriter, r *http.Request) {
	req := &pb.SharedListDeleteRequest{}
	if err := bindProto(r, req); err != nil || req.ListId <= 0 {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	user, _, ok := h.requireJoined(w, r)
	if !ok {
		return
	}
	affected, err := h.Queries.DeleteSocialList(r.Context(), db.DeleteSocialListParams{
		ID: req.ListId, OwnerUserID: user.ID,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	if affected == 0 {
		pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "list not found")
		return
	}
	writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
}

// PostSocialListEntries handles POST /social/list/entries (optional auth):
// the list header + an entries page, visibility- and block-gated. A hidden or
// missing list reads identically as not-found (no-leak).
func (h Handlers) PostSocialListEntries(w http.ResponseWriter, r *http.Request) {
	req := &pb.SharedListEntriesRequest{}
	if err := bindProto(r, req); err != nil || req.ListId <= 0 {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	list, err := h.Queries.GetSocialList(r.Context(), req.ListId)
	if err != nil {
		pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "list not found")
		return
	}

	viewerID := h.optionalViewerID(r)
	role := h.socialListRole(r, list.OwnerUserID, req.ListId, viewerID)
	if !h.canViewSocialList(r, list.OwnerUserID, list.Visibility, viewerID, role) {
		pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "list not found")
		return
	}

	limit := req.Limit
	if limit <= 0 || limit > maxListEntryPageSize {
		limit = maxListEntryPageSize
	}
	rows, err := h.Queries.GetSocialListEntries(r.Context(), db.GetSocialListEntriesParams{
		ListID: req.ListId, Limit: limit, Offset: max(req.Offset, 0),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	total, err := h.Queries.CountSocialListEntries(r.Context(), req.ListId)
	if err != nil {
		writeError(w, r, err)
		return
	}

	resp := &pb.SharedListEntriesResponse{
		List:  h.socialListProto(r, list, role, viewerID == list.OwnerUserID),
		Total: int32(total),
	}
	for _, row := range rows {
		resp.Entries = append(resp.Entries, &pb.SharedListEntry{
			EpisodeUuid:   row.EpisodeUuid,
			PodcastUuid:   row.PodcastUuid,
			EpisodeTitle:  row.EpisodeTitle,
			PodcastTitle:  row.PodcastTitle,
			Position:      row.Position,
			AddedByHandle: row.AddedByHandle,
			AddedAt:       timestamppb.New(row.AddedAt),
		})
	}
	writeProto(w, http.StatusOK, resp)
}

// PostSocialListEntryOp handles POST /social/list/entry: add/remove/move by
// the owner or a collaborator. Last write wins; MOVE sets the raw position.
func (h Handlers) PostSocialListEntryOp(w http.ResponseWriter, r *http.Request) {
	req := &pb.SharedListEntryOpRequest{}
	if err := bindProto(r, req); err != nil || req.ListId <= 0 || req.EpisodeUuid == "" ||
		len(req.EpisodeUuid) > maxUuidFieldLen || len(req.PodcastUuid) > maxUuidFieldLen {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	user, _, ok := h.requireJoined(w, r)
	if !ok {
		return
	}
	list, err := h.Queries.GetSocialList(r.Context(), req.ListId)
	if err != nil {
		pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "list not found")
		return
	}
	role := h.socialListRole(r, list.OwnerUserID, req.ListId, user.ID)
	if role != pb.SharedListRole_SHARED_LIST_ROLE_OWNER && role != pb.SharedListRole_SHARED_LIST_ROLE_COLLABORATOR {
		pcerrors.Write(w, http.StatusForbidden, pcerrors.AccessDenied, "not an editor")
		return
	}
	if !h.canViewSocialList(r, list.OwnerUserID, list.Visibility, user.ID, role) {
		pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "list not found")
		return
	}

	switch req.Op {
	case pb.SharedListOp_SHARED_LIST_OP_ADD:
		total, err := h.Queries.CountSocialListEntries(r.Context(), req.ListId)
		if err == nil && total >= listEntriesMax {
			pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "list full")
			return
		}
		position := req.Position
		if position < 0 {
			maxPos, err := h.Queries.MaxSocialListPosition(r.Context(), req.ListId)
			if err != nil {
				writeError(w, r, err)
				return
			}
			position = maxPos + 1
		}
		if err := h.Queries.UpsertSocialListEntry(r.Context(), db.UpsertSocialListEntryParams{
			ListID:       req.ListId,
			EpisodeUuid:  req.EpisodeUuid,
			PodcastUuid:  req.PodcastUuid,
			EpisodeTitle: truncateRunes(req.EpisodeTitle, maxTitleLen),
			PodcastTitle: truncateRunes(req.PodcastTitle, maxTitleLen),
			Position:     position,
			AddedBy:      &user.ID,
		}); err != nil {
			writeError(w, r, err)
			return
		}
	case pb.SharedListOp_SHARED_LIST_OP_REMOVE:
		if _, err := h.Queries.DeleteSocialListEntry(r.Context(), db.DeleteSocialListEntryParams{
			ListID: req.ListId, EpisodeUuid: req.EpisodeUuid,
		}); err != nil {
			writeError(w, r, err)
			return
		}
	case pb.SharedListOp_SHARED_LIST_OP_MOVE:
		if _, err := h.Queries.MoveSocialListEntry(r.Context(), db.MoveSocialListEntryParams{
			ListID: req.ListId, EpisodeUuid: req.EpisodeUuid, Position: req.Position,
		}); err != nil {
			writeError(w, r, err)
			return
		}
	default:
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "unknown op")
		return
	}

	if err := h.Queries.TouchSocialList(r.Context(), req.ListId); err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
}

// PostSocialListInvite handles POST /social/list/invite (owner only): invites
// a handle as collaborator; the invite surfaces in their Inbox. Blocked or
// missing handles fail identically (no-leak).
func (h Handlers) PostSocialListInvite(w http.ResponseWriter, r *http.Request) {
	req := &pb.SharedListInviteRequest{}
	if err := bindProto(r, req); err != nil || req.ListId <= 0 || req.Handle == "" {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	user, ownerProfile, ok := h.requireJoined(w, r)
	if !ok {
		return
	}
	list, err := h.Queries.GetSocialList(r.Context(), req.ListId)
	if err != nil || list.OwnerUserID != user.ID {
		pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "list not found")
		return
	}

	target, ok := h.resolveFollowTarget(w, r, user.ID, req.Handle)
	if !ok {
		return
	}
	if target.UserID == user.ID {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "cannot invite yourself")
		return
	}

	members, err := h.Queries.GetSocialListMembers(r.Context(), req.ListId)
	if err == nil {
		collaborators := 0
		for _, m := range members {
			if m.Role == listRoleCollaborator || m.Role == listRoleInvited {
				collaborators++
			}
			if m.UserID == target.UserID && m.Role != listRoleSubscriber {
				// Already invited or collaborating: idempotent success.
				writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
				return
			}
		}
		if collaborators >= listCollaboratorsMax {
			pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "too many collaborators")
			return
		}
	}

	if err := h.Queries.UpsertSocialListMember(r.Context(), db.UpsertSocialListMemberParams{
		ListID: req.ListId, UserID: target.UserID, Role: listRoleInvited,
	}); err != nil {
		writeError(w, r, err)
		return
	}
	if h.mutedBy(r, target.UserID, user.ID) {
		writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
		return
	}
	h.notifySocial(target.UserID, push.SocialPushListInvite,
		ownerProfile.Handle, ownerProfile.DisplayName,
		map[string]string{"list_id": strconv.FormatInt(req.ListId, 10)})
	writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
}

// PostSocialListInviteRespond handles POST /social/list/invite/respond.
func (h Handlers) PostSocialListInviteRespond(w http.ResponseWriter, r *http.Request) {
	req := &pb.SharedListInviteRespondRequest{}
	if err := bindProto(r, req); err != nil || req.ListId <= 0 {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	user, _, ok := h.requireJoined(w, r)
	if !ok {
		return
	}
	role, err := h.Queries.GetSocialListMember(r.Context(), db.GetSocialListMemberParams{
		ListID: req.ListId, UserID: user.ID,
	})
	if err != nil || role != listRoleInvited {
		pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "invite not found")
		return
	}
	if list, err := h.Queries.GetSocialList(r.Context(), req.ListId); err == nil {
		blocked, berr := h.Queries.IsBlockedEither(r.Context(), db.IsBlockedEitherParams{
			UserID: user.ID, TargetUserID: list.OwnerUserID,
		})
		if berr == nil && blocked {
			pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "invite not found")
			return
		}
	}
	if req.Accept {
		err = h.Queries.UpsertSocialListMember(r.Context(), db.UpsertSocialListMemberParams{
			ListID: req.ListId, UserID: user.ID, Role: listRoleCollaborator,
		})
	} else {
		_, err = h.Queries.DeleteSocialListMember(r.Context(), db.DeleteSocialListMemberParams{
			ListID: req.ListId, UserID: user.ID,
		})
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
}

// PostSocialListSubscribe handles POST /social/list/subscribe: follow a
// visible list read-only (or drop out — also how a collaborator leaves and
// how the owner kicks via role downgrade is NOT done here; kick = invite
// removal by the owner through the same member delete).
func (h Handlers) PostSocialListSubscribe(w http.ResponseWriter, r *http.Request) {
	req := &pb.SharedListSubscribeRequest{}
	if err := bindProto(r, req); err != nil || req.ListId <= 0 {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	user, _, ok := h.requireJoined(w, r)
	if !ok {
		return
	}
	list, err := h.Queries.GetSocialList(r.Context(), req.ListId)
	if err != nil {
		pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "list not found")
		return
	}
	if list.OwnerUserID == user.ID {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "you own this list")
		return
	}

	existing, err := h.Queries.GetSocialListMember(r.Context(), db.GetSocialListMemberParams{
		ListID: req.ListId, UserID: user.ID,
	})
	hasRow := err == nil

	if !req.Subscribe {
		// Unsubscribe / leave: any member row goes away.
		if hasRow {
			if _, err := h.Queries.DeleteSocialListMember(r.Context(), db.DeleteSocialListMemberParams{
				ListID: req.ListId, UserID: user.ID,
			}); err != nil {
				writeError(w, r, err)
				return
			}
		}
		writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
		return
	}

	if hasRow && existing != listRoleSubscriber {
		// Collaborators/invitees don't downgrade by subscribing.
		writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
		return
	}
	role := h.socialListRole(r, list.OwnerUserID, req.ListId, user.ID)
	if !h.canViewSocialList(r, list.OwnerUserID, list.Visibility, user.ID, role) {
		pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "list not found")
		return
	}
	if err := h.Queries.UpsertSocialListMember(r.Context(), db.UpsertSocialListMemberParams{
		ListID: req.ListId, UserID: user.ID, Role: listRoleSubscriber,
	}); err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
}

// PostSocialLists handles POST /social/lists: everything the caller owns,
// collaborates on, or subscribes to, plus pending invites.
func (h Handlers) PostSocialLists(w http.ResponseWriter, r *http.Request) {
	user, _, ok := h.requireJoined(w, r)
	if !ok {
		return
	}

	rows, err := h.Queries.GetSocialListsForUser(r.Context(), user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	invites, err := h.Queries.GetSocialListInvitesForUser(r.Context(), user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}

	resp := &pb.SharedListsResponse{}
	for _, row := range rows {
		if pb.SharedListRole(row.YourRole) == pb.SharedListRole_SHARED_LIST_ROLE_INVITED {
			continue // pending invites travel in the invites field
		}
		resp.Lists = append(resp.Lists, &pb.SharedList{
			Id:               row.ID,
			OwnerHandle:      row.OwnerHandle,
			OwnerDisplayName: row.OwnerDisplayName,
			Title:            row.Title,
			Description:      row.Description,
			Visibility:       pb.SocialVisibility(row.Visibility),
			CreatedAt:        timestamppb.New(row.CreatedAt),
			UpdatedAt:        timestamppb.New(row.UpdatedAt),
			EntryCount:       row.EntryCount,
			YourRole:         pb.SharedListRole(row.YourRole),
		})
	}
	for _, row := range invites {
		resp.Invites = append(resp.Invites, &pb.SharedList{
			Id:               row.ID,
			OwnerHandle:      row.OwnerHandle,
			OwnerDisplayName: row.OwnerDisplayName,
			Title:            row.Title,
			Description:      row.Description,
			Visibility:       pb.SocialVisibility(row.Visibility),
			CreatedAt:        timestamppb.New(row.CreatedAt),
			UpdatedAt:        timestamppb.New(row.UpdatedAt),
			EntryCount:       row.EntryCount,
			YourRole:         pb.SharedListRole_SHARED_LIST_ROLE_INVITED,
		})
	}
	writeProto(w, http.StatusOK, resp)
}

// PostSocialListMemberRemove handles POST /social/list/member/remove (owner
// only): kicks a collaborator, revokes an invite, or drops a subscriber.
func (h Handlers) PostSocialListMemberRemove(w http.ResponseWriter, r *http.Request) {
	req := &pb.SharedListInviteRequest{}
	if err := bindProto(r, req); err != nil || req.ListId <= 0 || req.Handle == "" {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	user, _, ok := h.requireJoined(w, r)
	if !ok {
		return
	}
	list, err := h.Queries.GetSocialList(r.Context(), req.ListId)
	if err != nil || list.OwnerUserID != user.ID {
		pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "list not found")
		return
	}
	target, err := h.Queries.GetSocialProfileByHandle(r.Context(), normalizeHandle(req.Handle))
	if err != nil {
		pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "member not found")
		return
	}
	if _, err := h.Queries.DeleteSocialListMember(r.Context(), db.DeleteSocialListMemberParams{
		ListID: req.ListId, UserID: target.UserID,
	}); err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
}

// ---- helpers ----

// socialListRole resolves the caller's role on a list (proto-space).
func (h Handlers) socialListRole(r *http.Request, ownerID, listID, viewerID int64) pb.SharedListRole {
	if viewerID == 0 {
		return pb.SharedListRole_SHARED_LIST_ROLE_NONE
	}
	if viewerID == ownerID {
		return pb.SharedListRole_SHARED_LIST_ROLE_OWNER
	}
	role, err := h.Queries.GetSocialListMember(r.Context(), db.GetSocialListMemberParams{
		ListID: listID, UserID: viewerID,
	})
	if err != nil {
		return pb.SharedListRole_SHARED_LIST_ROLE_NONE
	}
	switch role {
	case listRoleCollaborator:
		return pb.SharedListRole_SHARED_LIST_ROLE_COLLABORATOR
	case listRoleSubscriber:
		return pb.SharedListRole_SHARED_LIST_ROLE_SUBSCRIBER
	case listRoleInvited:
		return pb.SharedListRole_SHARED_LIST_ROLE_INVITED
	}
	return pb.SharedListRole_SHARED_LIST_ROLE_NONE
}

// canViewSocialList applies visibility + block gating: members always see the
// list; private = members only; public = anyone not blocked-either-way with
// the owner; followers = active followers.
func (h Handlers) canViewSocialList(r *http.Request, ownerID int64, visibility int16, viewerID int64, role pb.SharedListRole) bool {
	// A block severs everything, member roles included (QA finding) — only
	// the owner is exempt from the check against themselves.
	if viewerID != 0 && viewerID != ownerID {
		blocked, err := h.Queries.IsBlockedEither(r.Context(), db.IsBlockedEitherParams{
			UserID: viewerID, TargetUserID: ownerID,
		})
		if err == nil && blocked {
			return false
		}
	}
	if role != pb.SharedListRole_SHARED_LIST_ROLE_NONE {
		return true
	}
	switch visibility {
	case 2: // public
		return true
	case 3: // followers-only
		if viewerID == 0 {
			return false
		}
		state, err := h.Queries.GetFollowState(r.Context(), db.GetFollowStateParams{
			FollowerUserID: viewerID, FolloweeUserID: ownerID,
		})
		return err == nil && state == followStatusActive
	default: // private
		return false
	}
}

// socialListProto builds the SharedList header; members ride along only for
// the owner's view.
func (h Handlers) socialListProto(r *http.Request, list db.GetSocialListRow, role pb.SharedListRole, ownerView bool) *pb.SharedList {
	proto := &pb.SharedList{
		Id:               list.ID,
		OwnerHandle:      list.OwnerHandle,
		OwnerDisplayName: list.OwnerDisplayName,
		Title:            list.Title,
		Description:      list.Description,
		Visibility:       pb.SocialVisibility(list.Visibility),
		CreatedAt:        timestamppb.New(list.CreatedAt),
		UpdatedAt:        timestamppb.New(list.UpdatedAt),
		EntryCount:       list.EntryCount,
		YourRole:         role,
	}
	if ownerView {
		if members, err := h.Queries.GetSocialListMembers(r.Context(), list.ID); err == nil {
			for _, m := range members {
				var memberRole pb.SharedListRole
				switch m.Role {
				case listRoleCollaborator:
					memberRole = pb.SharedListRole_SHARED_LIST_ROLE_COLLABORATOR
				case listRoleSubscriber:
					memberRole = pb.SharedListRole_SHARED_LIST_ROLE_SUBSCRIBER
				default:
					memberRole = pb.SharedListRole_SHARED_LIST_ROLE_INVITED
				}
				proto.Members = append(proto.Members, &pb.SharedListMember{
					Handle: m.Handle, DisplayName: m.DisplayName, Role: memberRole,
				})
			}
		}
	}
	return proto
}
