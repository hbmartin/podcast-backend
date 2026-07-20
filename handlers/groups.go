package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/moderation"
	"github.com/hbmartin/podcast-backend/pcerrors"
	pb "github.com/hbmartin/podcast-backend/protos/api"
	"github.com/hbmartin/podcast-backend/push"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Groups (Slice 13, ADR-0012): one entity, two configurations. A private
// group is an invite-only circle (any member invites, Inbox accept/decline);
// a public group is one-tap joinable and may anchor - non-exclusively - to a
// podcast. Content is deliberate posts only; group membership never grants
// follower-level visibility into members' listening.
const (
	groupTitleMaxLen   = 100
	groupDescMaxLen    = 500
	groupPostMaxLen    = 2000
	groupPrivateCap    = 100
	maxGroupPageSize   = 50
	groupPostEditGrace = 5 * time.Minute

	groupRoleMember  = int16(1)
	groupRoleOwner   = int16(2)
	groupRoleInvited = int16(3)
	groupRoleBanned  = int16(4)

	groupVisPrivate = int16(1)
	groupVisPublic  = int16(2)
)

// PostGroupCreate handles POST /social/group/create (joined required).
func (h Handlers) PostGroupCreate(w http.ResponseWriter, r *http.Request) {
	req := &pb.GroupCreateRequest{}
	if err := bindProto(r, req); err != nil || req.Title == "" {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	vis := int16(req.Visibility)
	if vis != groupVisPrivate && vis != groupVisPublic {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid visibility")
		return
	}
	if req.PodcastUuid != "" && !uuidPattern.MatchString(req.PodcastUuid) {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid podcast")
		return
	}
	if err := moderation.CheckDisplayName(req.Title); err != nil {
		pcerrors.Write(w, http.StatusUnprocessableEntity, pcerrors.AccessDenied, "title rejected")
		return
	}
	if err := moderation.CheckBio(req.Description); err != nil {
		pcerrors.Write(w, http.StatusUnprocessableEntity, pcerrors.AccessDenied, "description rejected")
		return
	}
	user, profile, ok := h.requireJoined(w, r)
	if !ok {
		return
	}

	var created db.CreateSocialGroupRow
	err := h.Queries.InTx(r.Context(), func(q db.Querier) error {
		var err error
		created, err = q.CreateSocialGroup(r.Context(), db.CreateSocialGroupParams{
			OwnerUserID:  user.ID,
			Title:        truncateRunes(req.Title, groupTitleMaxLen),
			Description:  truncateRunes(req.Description, groupDescMaxLen),
			Visibility:   vis,
			PodcastUuid:  req.PodcastUuid,
			PodcastTitle: truncateRunes(req.PodcastTitle, maxTitleLen),
		})
		if err != nil {
			return err
		}
		// The owner is also a member row: it carries tenure, the member
		// count, and the per-group notify flag.
		return q.UpsertGroupMember(r.Context(), db.UpsertGroupMemberParams{
			GroupID: created.ID, UserID: user.ID, Role: groupRoleOwner,
		})
	})
	if err != nil {
		writeError(w, r, err)
		return
	}

	writeProto(w, http.StatusOK, &pb.SocialGroup{
		Id: created.ID, OwnerHandle: profile.Handle, OwnerDisplayName: profile.DisplayName,
		Title: truncateRunes(req.Title, groupTitleMaxLen), Description: truncateRunes(req.Description, groupDescMaxLen),
		Visibility: pb.SocialVisibility(vis), PodcastUuid: req.PodcastUuid,
		PodcastTitle: truncateRunes(req.PodcastTitle, maxTitleLen),
		MemberCount:  1, YourRole: pb.GroupRole_GROUP_ROLE_OWNER,
		CreatedAt: timestamppb.New(created.CreatedAt),
	})
}

// PostGroupUpdate handles POST /social/group/update (owner only).
func (h Handlers) PostGroupUpdate(w http.ResponseWriter, r *http.Request) {
	req := &pb.GroupUpdateRequest{}
	if err := bindProto(r, req); err != nil || req.Id <= 0 || req.Title == "" {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	vis := int16(req.Visibility)
	if vis != groupVisPrivate && vis != groupVisPublic {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid visibility")
		return
	}
	if err := moderation.CheckDisplayName(req.Title); err != nil {
		pcerrors.Write(w, http.StatusUnprocessableEntity, pcerrors.AccessDenied, "title rejected")
		return
	}
	if err := moderation.CheckBio(req.Description); err != nil {
		pcerrors.Write(w, http.StatusUnprocessableEntity, pcerrors.AccessDenied, "description rejected")
		return
	}
	user, _, ok := h.requireJoined(w, r)
	if !ok {
		return
	}
	current, err := h.Queries.GetSocialGroup(r.Context(), db.GetSocialGroupParams{ID: req.Id, Viewer: &user.ID})
	if err != nil {
		pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "group not found")
		return
	}
	if current.Visibility == groupVisPrivate && vis == groupVisPublic {
		// Members joined under a privacy expectation; flipping public would
		// retroactively broadcast their joins and post history (ADR-0012).
		pcerrors.Write(w, http.StatusConflict, pcerrors.AccessDenied, "a private group cannot be made public")
		return
	}
	affected, err := h.Queries.UpdateSocialGroup(r.Context(), db.UpdateSocialGroupParams{
		ID: req.Id, OwnerUserID: user.ID,
		Title:       truncateRunes(req.Title, groupTitleMaxLen),
		Description: truncateRunes(req.Description, groupDescMaxLen),
		Visibility:  vis,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	if affected == 0 {
		pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "group not found")
		return
	}
	writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
}

// PostGroupDelete handles POST /social/group/delete (owner only).
func (h Handlers) PostGroupDelete(w http.ResponseWriter, r *http.Request) {
	req := &pb.GroupDeleteRequest{}
	if err := bindProto(r, req); err != nil || req.Id <= 0 {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	user, _, ok := h.requireJoined(w, r)
	if !ok {
		return
	}
	affected, err := h.Queries.DeleteSocialGroup(r.Context(), db.DeleteSocialGroupParams{ID: req.Id, OwnerUserID: user.ID})
	if err != nil {
		writeError(w, r, err)
		return
	}
	if affected == 0 {
		pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "group not found")
		return
	}
	writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
}

// PostGroupJoin handles POST /social/group/join: public groups only. A
// pending invite to a private group is accepted via invite/respond instead.
func (h Handlers) PostGroupJoin(w http.ResponseWriter, r *http.Request) {
	req := &pb.GroupJoinRequest{}
	if err := bindProto(r, req); err != nil || req.Id <= 0 {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	user, _, ok := h.requireJoined(w, r)
	if !ok {
		return
	}
	group, err := h.Queries.GetSocialGroup(r.Context(), db.GetSocialGroupParams{ID: req.Id, Viewer: &user.ID})
	if err != nil || group.Visibility != groupVisPublic {
		pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "group not found")
		return
	}
	switch int16(group.YourRole) {
	case groupRoleBanned:
		pcerrors.Write(w, http.StatusForbidden, pcerrors.AccessDenied, "cannot join this group")
		return
	case groupRoleMember, groupRoleOwner:
		writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
		return
	}
	if err := h.Queries.UpsertGroupMember(r.Context(), db.UpsertGroupMemberParams{
		GroupID: req.Id, UserID: user.ID, Role: groupRoleMember,
	}); err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
}

// PostGroupLeave handles POST /social/group/leave. The owner cannot leave -
// they delete the group (or erasure hands it on).
func (h Handlers) PostGroupLeave(w http.ResponseWriter, r *http.Request) {
	req := &pb.GroupLeaveRequest{}
	if err := bindProto(r, req); err != nil || req.Id <= 0 {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	user, _, ok := h.requireJoined(w, r)
	if !ok {
		return
	}
	member, err := h.Queries.GetGroupMember(r.Context(), db.GetGroupMemberParams{GroupID: req.Id, UserID: user.ID})
	if err != nil || member.Role == groupRoleBanned {
		pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "group not found")
		return
	}
	if member.Role == groupRoleOwner {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "owners delete their group instead")
		return
	}
	if _, err := h.Queries.DeleteGroupMember(r.Context(), db.DeleteGroupMemberParams{GroupID: req.Id, UserID: user.ID}); err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
}

// PostGroupInvite handles POST /social/group/invite: ANY member may invite
// (circles are peer-shaped). No-leak: blocked-either-way, banned, missing,
// and tombstoned targets all read as not-found.
func (h Handlers) PostGroupInvite(w http.ResponseWriter, r *http.Request) {
	req := &pb.GroupInviteRequest{}
	if err := bindProto(r, req); err != nil || req.GroupId <= 0 || req.Handle == "" {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
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
	group, err := h.Queries.GetSocialGroup(r.Context(), db.GetSocialGroupParams{ID: req.GroupId, Viewer: &user.ID})
	if err != nil {
		writeError(w, r, err)
		return
	}
	if group.Visibility == groupVisPrivate && int(group.MemberCount) >= groupPrivateCap {
		pcerrors.Write(w, http.StatusConflict, pcerrors.AccessDenied, "group is full")
		return
	}
	target, ok := h.resolveFollowTarget(w, r, user.ID, req.Handle)
	if !ok {
		return
	}
	existing, err := h.Queries.GetGroupMember(r.Context(), db.GetGroupMemberParams{GroupID: req.GroupId, UserID: target.UserID})
	if err == nil {
		if existing.Role == groupRoleBanned {
			pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "profile not found")
			return
		}
		writeProto(w, http.StatusOK, &pb.SocialAck{Success: true}) // already in or invited
		return
	} else if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, r, err)
		return
	}
	if err := h.Queries.UpsertGroupMember(r.Context(), db.UpsertGroupMemberParams{
		GroupID: req.GroupId, UserID: target.UserID, Role: groupRoleInvited, InvitedBy: &user.ID,
	}); err != nil {
		writeError(w, r, err)
		return
	}
	if !h.mutedBy(r, target.UserID, user.ID) {
		h.notifySocial(target.UserID, push.SocialPushGroupInvite, profile.Handle, profile.DisplayName,
			map[string]string{"group_id": strconv.FormatInt(req.GroupId, 10)})
	}
	writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
}

// PostGroupInviteRespond handles POST /social/group/invite/respond.
func (h Handlers) PostGroupInviteRespond(w http.ResponseWriter, r *http.Request) {
	req := &pb.GroupInviteRespondRequest{}
	if err := bindProto(r, req); err != nil || req.GroupId <= 0 {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	user, _, ok := h.requireJoined(w, r)
	if !ok {
		return
	}
	member, err := h.Queries.GetGroupMember(r.Context(), db.GetGroupMemberParams{GroupID: req.GroupId, UserID: user.ID})
	if err != nil || member.Role != groupRoleInvited {
		pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "invite not found")
		return
	}
	if req.Accept {
		// The cap must hold at accept, not just at invite: outstanding
		// invites don't reserve seats (QA review finding).
		group, groupErr := h.Queries.GetSocialGroup(r.Context(), db.GetSocialGroupParams{ID: req.GroupId, Viewer: &user.ID})
		if groupErr != nil {
			writeError(w, r, groupErr)
			return
		}
		if group.Visibility == groupVisPrivate && int(group.MemberCount) >= groupPrivateCap {
			pcerrors.Write(w, http.StatusConflict, pcerrors.AccessDenied, "group is full")
			return
		}
		err = h.Queries.UpsertGroupMember(r.Context(), db.UpsertGroupMemberParams{
			GroupID: req.GroupId, UserID: user.ID, Role: groupRoleMember,
		})
	} else {
		_, err = h.Queries.DeleteGroupMember(r.Context(), db.DeleteGroupMemberParams{GroupID: req.GroupId, UserID: user.ID})
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
}

// PostGroupKick handles POST /social/group/kick (owner only; ban blocks
// rejoin and re-invite).
func (h Handlers) PostGroupKick(w http.ResponseWriter, r *http.Request) {
	req := &pb.GroupKickRequest{}
	if err := bindProto(r, req); err != nil || req.GroupId <= 0 || req.Handle == "" {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	user, _, ok := h.requireJoined(w, r)
	if !ok {
		return
	}
	member, err := h.Queries.GetGroupMember(r.Context(), db.GetGroupMemberParams{GroupID: req.GroupId, UserID: user.ID})
	if err != nil || member.Role != groupRoleOwner {
		pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "group not found")
		return
	}
	target, err := h.Queries.GetSocialProfileByHandle(r.Context(), normalizeHandle(req.Handle))
	if err != nil || target.UserID == user.ID {
		pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "member not found")
		return
	}
	if req.Ban {
		err = h.Queries.UpsertGroupMember(r.Context(), db.UpsertGroupMemberParams{
			GroupID: req.GroupId, UserID: target.UserID, Role: groupRoleBanned,
		})
	} else {
		_, err = h.Queries.DeleteGroupMember(r.Context(), db.DeleteGroupMemberParams{GroupID: req.GroupId, UserID: target.UserID})
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
}

// PostGroupAlert handles POST /social/group/alert: the per-group opt-in
// new-post notification flag (default off - quiet groups).
func (h Handlers) PostGroupAlert(w http.ResponseWriter, r *http.Request) {
	req := &pb.GroupAlertRequest{}
	if err := bindProto(r, req); err != nil || req.GroupId <= 0 {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	user, _, ok := h.requireJoined(w, r)
	if !ok {
		return
	}
	affected, err := h.Queries.SetGroupMemberNotify(r.Context(), db.SetGroupMemberNotifyParams{
		GroupID: req.GroupId, UserID: user.ID, NotifyPosts: req.Enabled,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	if affected == 0 {
		pcerrors.Write(w, http.StatusNotFound, pcerrors.AccessDenied, "group not found")
		return
	}
	writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
}

// PostGroups handles POST /social/groups: the caller's groups + pending
// invites (Inbox-surfaced).
func (h Handlers) PostGroups(w http.ResponseWriter, r *http.Request) {
	user, _, ok := h.requireJoined(w, r)
	if !ok {
		return
	}
	mine, err := h.Queries.GetGroupsForUser(r.Context(), db.GetGroupsForUserParams{
		UserID: user.ID, Column2: []int16{groupRoleMember, groupRoleOwner},
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	invited, err := h.Queries.GetGroupsForUser(r.Context(), db.GetGroupsForUserParams{
		UserID: user.ID, Column2: []int16{groupRoleInvited},
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	resp := &pb.GroupsResponse{}
	for _, g := range mine {
		resp.Groups = append(resp.Groups, memberGroupToProto(g))
	}
	for _, g := range invited {
		resp.Invites = append(resp.Invites, memberGroupToProto(g))
	}
	writeProto(w, http.StatusOK, resp)
}

// PostGroupDiscover handles POST /social/group/discover (joined): public
// groups by size.
func (h Handlers) PostGroupDiscover(w http.ResponseWriter, r *http.Request) {
	req := &pb.GroupDiscoverRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	if _, _, ok := h.requireJoined(w, r); !ok {
		return
	}
	limit := req.Limit
	if limit <= 0 || limit > 20 {
		limit = 20
	}
	rows, err := h.Queries.DiscoverGroups(r.Context(), db.DiscoverGroupsParams{Limit: limit})
	if err != nil {
		writeError(w, r, err)
		return
	}
	resp := &pb.GroupDiscoverResponse{}
	for _, g := range rows {
		resp.Groups = append(resp.Groups, discoverGroupToProto(g))
	}
	writeProto(w, http.StatusOK, resp)
}

// PostGroupsForPodcast handles POST /social/group/for-podcast (optional
// auth): the show's fandom hubs, member-count ordered. Anchors are
// non-exclusive by design (ADR-0012).
func (h Handlers) PostGroupsForPodcast(w http.ResponseWriter, r *http.Request) {
	req := &pb.GroupsForPodcastRequest{}
	if err := bindProto(r, req); err != nil || !uuidPattern.MatchString(req.PodcastUuid) {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	rows, err := h.Queries.DiscoverGroups(r.Context(), db.DiscoverGroupsParams{Limit: 20, PodcastUuid: &req.PodcastUuid})
	if err != nil {
		writeError(w, r, err)
		return
	}
	resp := &pb.GroupDiscoverResponse{}
	for _, g := range rows {
		resp.Groups = append(resp.Groups, discoverGroupToProto(g))
	}
	writeProto(w, http.StatusOK, resp)
}

// PostGroupMembers handles POST /social/group/members: member-visible for
// private groups; any joined viewer for public ones.
func (h Handlers) PostGroupMembers(w http.ResponseWriter, r *http.Request) {
	req := &pb.GroupMembersRequest{}
	if err := bindProto(r, req); err != nil || req.GroupId <= 0 {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	user, _, ok := h.requireJoined(w, r)
	if !ok {
		return
	}
	group, err := h.Queries.GetSocialGroup(r.Context(), db.GetSocialGroupParams{ID: req.GroupId, Viewer: &user.ID})
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
	rows, err := h.Queries.GetGroupMembers(r.Context(), db.GetGroupMembersParams{
		GroupID: req.GroupId, Limit: limit, Offset: max(req.Offset, 0),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	total, err := h.Queries.CountGroupMembers(r.Context(), req.GroupId)
	if err != nil {
		writeError(w, r, err)
		return
	}
	resp := &pb.GroupMembersResponse{Total: int32(total)}
	for _, m := range rows {
		resp.Members = append(resp.Members, &pb.GroupMember{
			Handle: m.Handle, DisplayName: m.DisplayName,
			Role: pb.GroupRole(m.Role), JoinedAt: timestamppb.New(m.CreatedAt),
		})
	}
	writeProto(w, http.StatusOK, resp)
}

// memberGroupToProto maps a GetGroupsForUser row.
func memberGroupToProto(g db.GetGroupsForUserRow) *pb.SocialGroup {
	return &pb.SocialGroup{
		Id: g.ID, OwnerHandle: g.OwnerHandle, OwnerDisplayName: g.OwnerDisplayName,
		Title: g.Title, Description: g.Description,
		Visibility: pb.SocialVisibility(g.Visibility), PodcastUuid: g.PodcastUuid,
		PodcastTitle: g.PodcastTitle, MemberCount: g.MemberCount,
		YourRole: pb.GroupRole(g.YourRole), NotifyPosts: g.NotifyPosts,
		CreatedAt: timestamppb.New(g.CreatedAt),
	}
}

// discoverGroupToProto maps a DiscoverGroups row (no viewer role).
func discoverGroupToProto(g db.DiscoverGroupsRow) *pb.SocialGroup {
	return &pb.SocialGroup{
		Id: g.ID, OwnerHandle: g.OwnerHandle, OwnerDisplayName: g.OwnerDisplayName,
		Title: g.Title, Description: g.Description,
		Visibility: pb.SocialVisibility(g.Visibility), PodcastUuid: g.PodcastUuid,
		PodcastTitle: g.PodcastTitle, MemberCount: g.MemberCount,
		CreatedAt: timestamppb.New(g.CreatedAt),
	}
}
