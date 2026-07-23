package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/hbmartin/podcast-backend/db"
	pb "github.com/hbmartin/podcast-backend/protos/api"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Shared-list state for socialMock (ADR-0011). Mirrors the SQL contracts
// closely enough for handler-level tests; the e2e suite covers the real
// queries.

type mockList struct {
	id          int64
	ownerID     int64
	title       string
	description string
	visibility  int16
	createdAt   time.Time
	updatedAt   time.Time
}

type mockListEntry struct {
	episodeUuid  string
	podcastUuid  string
	episodeTitle string
	podcastTitle string
	position     int32
	addedBy      *int64
	addedAt      time.Time
}

func (m *socialMock) ensureListState() {
	if m.lists == nil {
		m.lists = map[int64]*mockList{}
		m.listEntries = map[int64][]*mockListEntry{}
		m.listMembers = map[int64]map[int64]int16{}
	}
}

func (m *socialMock) CreateSocialList(ctx context.Context, arg db.CreateSocialListParams) (db.CreateSocialListRow, error) {
	m.ensureListState()
	m.listSeq++
	now := time.Now()
	m.lists[m.listSeq] = &mockList{
		id: m.listSeq, ownerID: arg.OwnerUserID, title: arg.Title,
		description: arg.Description, visibility: arg.Visibility,
		createdAt: now, updatedAt: now,
	}
	m.listMembers[m.listSeq] = map[int64]int16{}
	return db.CreateSocialListRow{ID: m.listSeq, CreatedAt: now, UpdatedAt: now}, nil
}

func (m *socialMock) UpdateSocialList(ctx context.Context, arg db.UpdateSocialListParams) (int64, error) {
	m.ensureListState()
	l, ok := m.lists[arg.ID]
	if !ok || l.ownerID != arg.OwnerUserID {
		return 0, nil
	}
	l.title, l.description, l.visibility = arg.Title, arg.Description, arg.Visibility
	l.updatedAt = time.Now()
	return 1, nil
}

func (m *socialMock) TouchSocialList(ctx context.Context, id int64) error {
	if l, ok := m.lists[id]; ok {
		l.updatedAt = time.Now()
	}
	return nil
}

func (m *socialMock) DeleteSocialList(ctx context.Context, arg db.DeleteSocialListParams) (int64, error) {
	l, ok := m.lists[arg.ID]
	if !ok || l.ownerID != arg.OwnerUserID {
		return 0, nil
	}
	delete(m.lists, arg.ID)
	delete(m.listEntries, arg.ID)
	delete(m.listMembers, arg.ID)
	return 1, nil
}

func (m *socialMock) listRow(l *mockList) db.GetSocialListRow {
	owner := m.profiles[l.ownerID]
	return db.GetSocialListRow{
		ID: l.id, OwnerUserID: l.ownerID, Title: l.title, Description: l.description,
		Visibility: l.visibility, CreatedAt: l.createdAt, UpdatedAt: l.updatedAt,
		OwnerHandle: owner.Handle, OwnerDisplayName: owner.DisplayName,
		EntryCount: int32(len(m.listEntries[l.id])),
	}
}

func (m *socialMock) GetSocialList(ctx context.Context, id int64) (db.GetSocialListRow, error) {
	m.ensureListState()
	l, ok := m.lists[id]
	if !ok {
		return db.GetSocialListRow{}, pgx.ErrNoRows
	}
	return m.listRow(l), nil
}

func (m *socialMock) GetSocialListEntries(ctx context.Context, arg db.GetSocialListEntriesParams) ([]db.GetSocialListEntriesRow, error) {
	var rows []db.GetSocialListEntriesRow
	for _, e := range m.listEntries[arg.ListID] {
		handle := ""
		if e.addedBy != nil {
			handle = m.profiles[*e.addedBy].Handle
		}
		rows = append(rows, db.GetSocialListEntriesRow{
			EpisodeUuid: e.episodeUuid, PodcastUuid: e.podcastUuid,
			EpisodeTitle: e.episodeTitle, PodcastTitle: e.podcastTitle,
			Position: e.position, AddedAt: e.addedAt, AddedByHandle: handle,
		})
	}
	return rows, nil
}

func (m *socialMock) CountSocialListEntries(ctx context.Context, listID int64) (int64, error) {
	return int64(len(m.listEntries[listID])), nil
}

func (m *socialMock) MaxSocialListPosition(ctx context.Context, listID int64) (int32, error) {
	maxPos := int32(-1)
	for _, e := range m.listEntries[listID] {
		if e.position > maxPos {
			maxPos = e.position
		}
	}
	return maxPos, nil
}

func (m *socialMock) UpsertSocialListEntry(ctx context.Context, arg db.UpsertSocialListEntryParams) error {
	m.ensureListState()
	for _, e := range m.listEntries[arg.ListID] {
		if e.episodeUuid == arg.EpisodeUuid {
			e.position = arg.Position
			return nil
		}
	}
	m.listEntries[arg.ListID] = append(m.listEntries[arg.ListID], &mockListEntry{
		episodeUuid: arg.EpisodeUuid, podcastUuid: arg.PodcastUuid,
		episodeTitle: arg.EpisodeTitle, podcastTitle: arg.PodcastTitle,
		position: arg.Position, addedBy: arg.AddedBy, addedAt: time.Now(),
	})
	return nil
}

func (m *socialMock) UpsertSocialListEntries(ctx context.Context, arg db.UpsertSocialListEntriesParams) error {
	var entries []map[string]any
	if err := json.Unmarshal(arg.Entries, &entries); err != nil {
		return err
	}
	// Postgres rejects a single INSERT ... ON CONFLICT DO UPDATE that touches
	// the same row twice; the mock must be as strict as the real batch.
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		uuid, ok := entry["episode_uuid"].(string)
		if !ok {
			return errors.New("invalid episode_uuid in social list entry batch")
		}
		if _, dup := seen[uuid]; dup {
			return errors.New("ON CONFLICT DO UPDATE command cannot affect row a second time")
		}
		seen[uuid] = struct{}{}
	}
	for _, entry := range entries {
		if err := m.UpsertSocialListEntry(ctx, db.UpsertSocialListEntryParams{
			ListID:       arg.ListID,
			EpisodeUuid:  entry["episode_uuid"].(string),
			PodcastUuid:  entry["podcast_uuid"].(string),
			EpisodeTitle: entry["episode_title"].(string),
			PodcastTitle: entry["podcast_title"].(string),
			Position:     int32(entry["position"].(float64)),
			AddedBy:      arg.AddedBy,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (m *socialMock) DeleteSocialListEntry(ctx context.Context, arg db.DeleteSocialListEntryParams) (int64, error) {
	entries := m.listEntries[arg.ListID]
	for i, e := range entries {
		if e.episodeUuid == arg.EpisodeUuid {
			m.listEntries[arg.ListID] = append(entries[:i], entries[i+1:]...)
			return 1, nil
		}
	}
	return 0, nil
}

func (m *socialMock) MoveSocialListEntry(ctx context.Context, arg db.MoveSocialListEntryParams) (int64, error) {
	for _, e := range m.listEntries[arg.ListID] {
		if e.episodeUuid == arg.EpisodeUuid {
			e.position = arg.Position
			return 1, nil
		}
	}
	return 0, nil
}

func (m *socialMock) GetSocialListMember(ctx context.Context, arg db.GetSocialListMemberParams) (int16, error) {
	if m.memberErr != nil {
		return 0, m.memberErr
	}
	m.ensureListState()
	if role, ok := m.listMembers[arg.ListID][arg.UserID]; ok {
		return role, nil
	}
	return 0, pgx.ErrNoRows
}

func (m *socialMock) UpsertSocialListMember(ctx context.Context, arg db.UpsertSocialListMemberParams) error {
	m.ensureListState()
	if m.listMembers[arg.ListID] == nil {
		m.listMembers[arg.ListID] = map[int64]int16{}
	}
	m.listMembers[arg.ListID][arg.UserID] = arg.Role
	return nil
}

func (m *socialMock) DeleteSocialListMember(ctx context.Context, arg db.DeleteSocialListMemberParams) (int64, error) {
	if _, ok := m.listMembers[arg.ListID][arg.UserID]; ok {
		delete(m.listMembers[arg.ListID], arg.UserID)
		return 1, nil
	}
	return 0, nil
}

func (m *socialMock) GetSocialListMembers(ctx context.Context, listID int64) ([]db.GetSocialListMembersRow, error) {
	var rows []db.GetSocialListMembersRow
	for userID, role := range m.listMembers[listID] {
		rows = append(rows, db.GetSocialListMembersRow{
			UserID: userID, Role: role,
			Handle: m.profiles[userID].Handle, DisplayName: m.profiles[userID].DisplayName,
		})
	}
	return rows, nil
}

func (m *socialMock) GetSocialListsForUser(ctx context.Context, ownerUserID int64) ([]db.GetSocialListsForUserRow, error) {
	m.ensureListState()
	var rows []db.GetSocialListsForUserRow
	for _, l := range m.lists {
		yourRole := int32(0)
		if l.ownerID == ownerUserID {
			yourRole = 1
		} else if role, ok := m.listMembers[l.id][ownerUserID]; ok {
			switch role {
			case listRoleCollaborator:
				yourRole = 2
			case listRoleSubscriber:
				yourRole = 3
			default:
				yourRole = 4
			}
		} else {
			continue
		}
		base := m.listRow(l)
		rows = append(rows, db.GetSocialListsForUserRow{
			ID: base.ID, OwnerUserID: base.OwnerUserID, Title: base.Title,
			Description: base.Description, Visibility: base.Visibility,
			CreatedAt: base.CreatedAt, UpdatedAt: base.UpdatedAt,
			OwnerHandle: base.OwnerHandle, OwnerDisplayName: base.OwnerDisplayName,
			EntryCount: base.EntryCount, YourRole: yourRole,
		})
	}
	return rows, nil
}

func (m *socialMock) GetSocialListInvitesForUser(ctx context.Context, userID int64) ([]db.GetSocialListInvitesForUserRow, error) {
	m.ensureListState()
	var rows []db.GetSocialListInvitesForUserRow
	for _, l := range m.lists {
		if role, ok := m.listMembers[l.id][userID]; ok && role == listRoleInvited {
			base := m.listRow(l)
			rows = append(rows, db.GetSocialListInvitesForUserRow(base))
		}
	}
	return rows, nil
}

func (m *socialMock) GetProfileSocialLists(ctx context.Context, arg db.GetProfileSocialListsParams) ([]db.GetProfileSocialListsRow, error) {
	m.ensureListState()
	allowed := map[int16]bool{}
	for _, tier := range arg.Column2 {
		allowed[tier] = true
	}
	var rows []db.GetProfileSocialListsRow
	for _, l := range m.lists {
		if l.ownerID != arg.OwnerUserID || !allowed[l.visibility] {
			continue
		}
		base := m.listRow(l)
		rows = append(rows, db.GetProfileSocialListsRow(base))
	}
	return rows, nil
}

func (m *socialMock) DeleteSocialListsForOwner(ctx context.Context, ownerUserID int64) error {
	m.ensureListState()
	for id, l := range m.lists {
		if l.ownerID == ownerUserID {
			delete(m.lists, id)
			delete(m.listEntries, id)
			delete(m.listMembers, id)
		}
	}
	return nil
}

func (m *socialMock) DeleteSocialListMembershipsForUser(ctx context.Context, userID int64) error {
	for _, members := range m.listMembers {
		delete(members, userID)
	}
	return nil
}

func (m *socialMock) ClearSocialListAttributionForUser(ctx context.Context, addedBy *int64) error {
	for _, entries := range m.listEntries {
		for _, e := range entries {
			if e.addedBy != nil && addedBy != nil && *e.addedBy == *addedBy {
				e.addedBy = nil
			}
		}
	}
	return nil
}

// ---- Router + fixtures ----

func listsRouter(m *socialMock) *http.ServeMux {
	router := commentsRouter(m)
	h := Handlers{Queries: m, Config: testAuthConfig}
	router.Handle("POST /social/list/create", mockAuthMiddleware(http.HandlerFunc(h.PostSocialListCreate)))
	router.Handle("POST /social/list/update", mockAuthMiddleware(http.HandlerFunc(h.PostSocialListUpdate)))
	router.Handle("POST /social/list/delete", mockAuthMiddleware(http.HandlerFunc(h.PostSocialListDelete)))
	router.Handle("POST /social/list/entries", mockAuthMiddleware(http.HandlerFunc(h.PostSocialListEntries)))
	router.Handle("POST /social/list/entry", mockAuthMiddleware(http.HandlerFunc(h.PostSocialListEntryOp)))
	router.Handle("POST /social/list/invite", mockAuthMiddleware(http.HandlerFunc(h.PostSocialListInvite)))
	router.Handle("POST /social/list/invite/respond", mockAuthMiddleware(http.HandlerFunc(h.PostSocialListInviteRespond)))
	router.Handle("POST /social/list/member/remove", mockAuthMiddleware(http.HandlerFunc(h.PostSocialListMemberRemove)))
	router.Handle("POST /social/list/subscribe", mockAuthMiddleware(http.HandlerFunc(h.PostSocialListSubscribe)))
	router.Handle("POST /social/lists", mockAuthMiddleware(http.HandlerFunc(h.PostSocialLists)))
	return router
}

// joinedListsMock: user 1 joined; user 2 joined as "friend".
func joinedListsMock(t *testing.T) (*socialMock, *http.ServeMux) {
	t.Helper()
	m := newSocialMock()
	m.ensureListState()
	router := listsRouter(m)
	joinAs(t, router, "list_owner")
	m.profiles[2] = db.SocialProfile{UserID: 2, Handle: "friend", DisplayName: "A Friend"}
	return m, router
}

func createList(t *testing.T, router *http.ServeMux, visibility pb.SocialVisibility) *pb.SharedList {
	t.Helper()
	list := &pb.SharedList{}
	code, _, err := makeProtoRequest(router, "/social/list/create", &pb.SharedListCreateRequest{
		Title: "Road Trip", Description: "long drives", Visibility: visibility,
		Entries: []*pb.SharedListEntry{
			{EpisodeUuid: "ep-1", PodcastUuid: "pod-1", EpisodeTitle: "First Episode"},
		},
	}, list)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, code)
	return list
}

// asUser2 flips the mock list ownership so requests (always authed as user 1)
// exercise the non-owner path: helpers plant state for user 2 directly.
func plantListOwnedBy2(m *socialMock, visibility int16) int64 {
	m.ensureListState()
	m.listSeq++
	now := time.Now()
	m.lists[m.listSeq] = &mockList{
		id: m.listSeq, ownerID: 2, title: "Theirs", visibility: visibility,
		createdAt: now, updatedAt: now,
	}
	m.listMembers[m.listSeq] = map[int64]int16{}
	return m.listSeq
}

// ---- Tests ----

func TestSocialListCreateAndVisibility(t *testing.T) {
	m, router := joinedListsMock(t)

	list := createList(t, router, pb.SocialVisibility_SOCIAL_VISIBILITY_PRIVATE)
	assert.Equal(t, pb.SharedListRole_SHARED_LIST_ROLE_OWNER, list.YourRole)
	assert.Equal(t, int32(1), list.EntryCount)

	// The owner reads it back even while private.
	entries := &pb.SharedListEntriesResponse{}
	code, _, _ := makeProtoRequest(router, "/social/list/entries",
		&pb.SharedListEntriesRequest{ListId: list.Id}, entries)
	assert.Equal(t, http.StatusOK, code)
	assert.Len(t, entries.Entries, 1)
	assert.Equal(t, "list_owner", entries.Entries[0].AddedByHandle)

	// A private list owned by someone else reads as not-found (no-leak).
	otherID := plantListOwnedBy2(m, 1)
	code, _, _ = makeProtoRequest(router, "/social/list/entries",
		&pb.SharedListEntriesRequest{ListId: otherID}, &pb.SharedListEntriesResponse{})
	assert.Equal(t, http.StatusNotFound, code)

	// Public flips it visible; followers-tier needs an active follow.
	m.lists[otherID].visibility = 2
	code, _, _ = makeProtoRequest(router, "/social/list/entries",
		&pb.SharedListEntriesRequest{ListId: otherID}, &pb.SharedListEntriesResponse{})
	assert.Equal(t, http.StatusOK, code)

	// Authorization dependencies fail closed rather than turning an unknown
	// block relationship into public access.
	m.blockErr = errors.New("block lookup unavailable")
	code, _, _ = makeProtoRequest(router, "/social/list/entries",
		&pb.SharedListEntriesRequest{ListId: otherID}, &pb.SharedListEntriesResponse{})
	assert.Equal(t, http.StatusInternalServerError, code)
	m.blockErr = nil

	m.lists[otherID].visibility = 3
	code, _, _ = makeProtoRequest(router, "/social/list/entries",
		&pb.SharedListEntriesRequest{ListId: otherID}, &pb.SharedListEntriesResponse{})
	assert.Equal(t, http.StatusNotFound, code)

	m.ensureGraphState()
	m.follows[followKey{1, 2}] = followStatusActive
	code, _, _ = makeProtoRequest(router, "/social/list/entries",
		&pb.SharedListEntriesRequest{ListId: otherID}, &pb.SharedListEntriesResponse{})
	assert.Equal(t, http.StatusOK, code)

	// A block hides even a public list.
	m.lists[otherID].visibility = 2
	m.rels[[3]int64{2, 1, 0}] = true
	code, _, _ = makeProtoRequest(router, "/social/list/entries",
		&pb.SharedListEntriesRequest{ListId: otherID}, &pb.SharedListEntriesResponse{})
	assert.Equal(t, http.StatusNotFound, code)
}

func TestSocialListCollabFlow(t *testing.T) {
	m, router := joinedListsMock(t)
	list := createList(t, router, pb.SocialVisibility_SOCIAL_VISIBILITY_PRIVATE)

	// Invite @friend; the pending invite shows on their account (planted role).
	ack := &pb.SocialAck{}
	code, _, _ := makeProtoRequest(router, "/social/list/invite",
		&pb.SharedListInviteRequest{ListId: list.Id, Handle: "friend"}, ack)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, listRoleInvited, m.listMembers[list.Id][2])

	// Accept as user 2 (planted — auth is fixed to user 1).
	m.listMembers[list.Id][2] = listRoleCollaborator

	// Collaborator adds an entry (simulate as user 2 by planting): instead,
	// verify the owner-path op works and attribution rides the entry.
	code, _, _ = makeProtoRequest(router, "/social/list/entry", &pb.SharedListEntryOpRequest{
		ListId: list.Id, Op: pb.SharedListOp_SHARED_LIST_OP_ADD,
		EpisodeUuid: "ep-2", EpisodeTitle: "Second", Position: -1,
	}, ack)
	assert.Equal(t, http.StatusOK, code)

	entries := &pb.SharedListEntriesResponse{}
	makeProtoRequest(router, "/social/list/entries", &pb.SharedListEntriesRequest{ListId: list.Id}, entries)
	assert.Len(t, entries.Entries, 2)
	assert.Equal(t, int32(1), entries.Entries[1].Position, "append lands after the snapshot")
	// Owner view carries the member list.
	require.NotNil(t, entries.List)
	require.Len(t, entries.List.Members, 1)
	assert.Equal(t, "friend", entries.List.Members[0].Handle)
	assert.Equal(t, pb.SharedListRole_SHARED_LIST_ROLE_COLLABORATOR, entries.List.Members[0].Role)

	// Kick: the member row disappears.
	code, _, _ = makeProtoRequest(router, "/social/list/member/remove",
		&pb.SharedListInviteRequest{ListId: list.Id, Handle: "friend"}, ack)
	assert.Equal(t, http.StatusOK, code)
	_, stillThere := m.listMembers[list.Id][2]
	assert.False(t, stillThere)
}

func TestSocialListCreateCollapsesDuplicateEpisodes(t *testing.T) {
	m, router := joinedListsMock(t)

	list := &pb.SharedList{}
	code, _, err := makeProtoRequest(router, "/social/list/create", &pb.SharedListCreateRequest{
		Title: "Dupes",
		Entries: []*pb.SharedListEntry{
			{EpisodeUuid: "ep-1", PodcastUuid: "pod-1", EpisodeTitle: "First Take"},
			{EpisodeUuid: "ep-2", PodcastUuid: "pod-2", EpisodeTitle: "Keeper"},
			{EpisodeUuid: "ep-1", PodcastUuid: "pod-1", EpisodeTitle: "Second Take"},
		},
	}, list)

	require.NoError(t, err)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, int32(2), list.EntryCount)
	entries := m.listEntries[list.Id]
	require.Len(t, entries, 2)
	byUuid := map[string]*mockListEntry{}
	for _, entry := range entries {
		byUuid[entry.episodeUuid] = entry
	}
	require.Contains(t, byUuid, "ep-1")
	assert.Equal(t, "Second Take", byUuid["ep-1"].episodeTitle, "the last occurrence of a duplicate episode wins")
	assert.Equal(t, int32(2), byUuid["ep-1"].position, "the surviving duplicate keeps its own request position")
}

func TestSocialListInviteResponseBlockLookupFailsClosed(t *testing.T) {
	m, router := joinedListsMock(t)
	listID := plantListOwnedBy2(m, 0)
	m.listMembers[listID][1] = listRoleInvited
	m.blockErr = assert.AnError

	code, _, _ := makeProtoRequest(router, "/social/list/invite/respond",
		&pb.SharedListInviteRespondRequest{ListId: listID, Accept: true}, &pb.SocialAck{})

	assert.Equal(t, http.StatusInternalServerError, code)
	assert.Equal(t, listRoleInvited, m.listMembers[listID][1])
}

func TestSocialListSubscribeFlow(t *testing.T) {
	m, router := joinedListsMock(t)
	otherID := plantListOwnedBy2(m, 2) // public list owned by @friend

	ack := &pb.SocialAck{}
	code, _, _ := makeProtoRequest(router, "/social/list/subscribe",
		&pb.SharedListSubscribeRequest{ListId: otherID, Subscribe: true}, ack)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, listRoleSubscriber, m.listMembers[otherID][1])

	// The subscription shows in /social/lists with the right role.
	lists := &pb.SharedListsResponse{}
	code, _, _ = makeProtoRequest(router, "/social/lists", &pb.SharedListsRequest{}, lists)
	assert.Equal(t, http.StatusOK, code)
	found := false
	for _, l := range lists.Lists {
		if l.Id == otherID {
			found = true
			assert.Equal(t, pb.SharedListRole_SHARED_LIST_ROLE_SUBSCRIBER, l.YourRole)
		}
	}
	assert.True(t, found)

	// A transient membership lookup failure is not equivalent to no row and
	// must never overwrite or silently acknowledge membership state.
	m.memberErr = errors.New("membership lookup unavailable")
	code, _, _ = makeProtoRequest(router, "/social/list/subscribe",
		&pb.SharedListSubscribeRequest{ListId: otherID, Subscribe: true}, ack)
	assert.Equal(t, http.StatusInternalServerError, code)
	m.memberErr = nil

	// Unsubscribe removes the row; a private list can't be subscribed.
	makeProtoRequest(router, "/social/list/subscribe",
		&pb.SharedListSubscribeRequest{ListId: otherID, Subscribe: false}, ack)
	_, still := m.listMembers[otherID][1]
	assert.False(t, still)

	m.lists[otherID].visibility = 1
	code, _, _ = makeProtoRequest(router, "/social/list/subscribe",
		&pb.SharedListSubscribeRequest{ListId: otherID, Subscribe: true}, ack)
	assert.Equal(t, http.StatusNotFound, code)
}

func TestSocialListEraseSemantics(t *testing.T) {
	m, router := joinedListsMock(t)
	list := createList(t, router, pb.SocialVisibility_SOCIAL_VISIBILITY_PUBLIC)

	// Plant an entry added by user 2, then erase user... the mock's erase runs
	// for the AUTHED user (1, the owner): their lists vanish entirely.
	two := int64(2)
	m.listEntries[list.Id] = append(m.listEntries[list.Id], &mockListEntry{
		episodeUuid: "ep-x", addedBy: &two, addedAt: time.Now(),
	})
	code, _, _ := makeProtoRequest(router, "/social/erase", &pb.EraseRequest{}, &pb.SocialAck{})
	assert.Equal(t, http.StatusOK, code)
	assert.Empty(t, m.lists, "owned lists die with the owner")
}
