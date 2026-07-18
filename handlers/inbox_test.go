package handlers

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hbmartin/podcast-backend/db"
	pb "github.com/hbmartin/podcast-backend/protos/api"

	"github.com/stretchr/testify/assert"
)

// Inbox state for socialMock.

type inboxItem struct {
	id        int64
	sender    int64
	recipient int64
	req       db.InsertSharedItemParams
	read      bool
}

func (m *socialMock) ensureInboxState() {
	if m.inbox == nil {
		m.inbox = []*inboxItem{}
	}
}

func (m *socialMock) InsertSharedItem(ctx context.Context, arg db.InsertSharedItemParams) (int64, error) {
	m.ensureInboxState()
	id := int64(len(m.inbox) + 1)
	m.inbox = append(m.inbox, &inboxItem{id: id, sender: arg.SenderUserID, recipient: arg.RecipientUserID, req: arg})
	return id, nil
}

func (m *socialMock) GetInboxItems(ctx context.Context, arg db.GetInboxItemsParams) ([]db.GetInboxItemsRow, error) {
	m.ensureInboxState()
	var rows []db.GetInboxItemsRow
	for _, item := range m.inbox {
		if item.recipient != arg.RecipientUserID {
			continue
		}
		profile := m.profiles[item.sender]
		rows = append(rows, db.GetInboxItemsRow{
			ID: item.id, EpisodeUuid: item.req.EpisodeUuid, PodcastUuid: item.req.PodcastUuid,
			EpisodeTitle: item.req.EpisodeTitle, PodcastTitle: item.req.PodcastTitle,
			Note: item.req.Note, TimestampSeconds: item.req.TimestampSeconds,
			CreatedAt: time.Unix(1_750_000_000, 0), Read: item.read,
			SenderUuid: m.usersByID[item.sender].Uuid, SenderHandle: profile.Handle,
			SenderDisplayName: profile.DisplayName,
		})
	}
	return rows, nil
}

func (m *socialMock) CountInboxItems(ctx context.Context, arg db.CountInboxItemsParams) (int64, error) {
	m.ensureInboxState()
	var n int64
	for _, item := range m.inbox {
		if item.recipient == arg.RecipientUserID {
			n++
		}
	}
	return n, nil
}

func (m *socialMock) CountUnreadInboxItems(ctx context.Context, arg db.CountUnreadInboxItemsParams) (int64, error) {
	m.ensureInboxState()
	var n int64
	for _, item := range m.inbox {
		if item.recipient == arg.RecipientUserID && !item.read {
			n++
		}
	}
	return n, nil
}

func (m *socialMock) MarkInboxItemsRead(ctx context.Context, arg db.MarkInboxItemsReadParams) error {
	m.ensureInboxState()
	ids := map[int64]bool{}
	for _, id := range arg.Column2 {
		ids[id] = true
	}
	for _, item := range m.inbox {
		if item.recipient == arg.RecipientUserID && ids[item.id] {
			item.read = true
		}
	}
	return nil
}

func (m *socialMock) DeleteInboxItem(ctx context.Context, arg db.DeleteInboxItemParams) (int64, error) {
	m.ensureInboxState()
	for index, item := range m.inbox {
		if item.id == arg.ID && item.recipient == arg.RecipientUserID {
			m.inbox = append(m.inbox[:index], m.inbox[index+1:]...)
			return 1, nil
		}
	}
	return 0, nil
}

func (m *socialMock) DeleteSharedItemsForUser(ctx context.Context, userID int64) error {
	m.ensureInboxState()
	var kept []*inboxItem
	for _, item := range m.inbox {
		if item.sender != userID && item.recipient != userID {
			kept = append(kept, item)
		}
	}
	m.inbox = kept
	return nil
}

func inboxRouter(m *socialMock) *http.ServeMux {
	router := reviewsRouter(m)
	h := Handlers{Queries: m, Config: testAuthConfig}
	router.Handle("POST /social/share/send", mockAuthMiddleware(http.HandlerFunc(h.PostShareSend)))
	router.Handle("POST /social/inbox", mockAuthMiddleware(http.HandlerFunc(h.PostInbox)))
	router.Handle("POST /social/inbox/read", mockAuthMiddleware(http.HandlerFunc(h.PostInboxRead)))
	router.Handle("POST /social/inbox/delete", mockAuthMiddleware(http.HandlerFunc(h.PostInboxDelete)))
	return router
}

// joinedInboxMock: user 1 (auth) joined; user 2 joined as recipient "friend".
func joinedInboxMock(t *testing.T) (*socialMock, *http.ServeMux) {
	t.Helper()
	m := newSocialMock()
	m.ensureInboxState()
	router := inboxRouter(m)
	joinAs(t, router, "sender_one")
	m.profiles[2] = db.SocialProfile{UserID: 2, Handle: "friend", DisplayName: "A Friend"}
	return m, router
}

func sendRequest(episode string, note string) *pb.SharedItemSendRequest {
	return &pb.SharedItemSendRequest{
		RecipientHandle:  "friend",
		EpisodeUuid:      episode,
		PodcastUuid:      "bbbbbbbb-0000-0000-0000-000000000002",
		EpisodeTitle:     "An Episode",
		PodcastTitle:     "A Podcast",
		Note:             note,
		TimestampSeconds: 870,
	}
}

func TestShareSendGates(t *testing.T) {
	// Not joined: forbidden.
	m := newSocialMock()
	m.ensureInboxState()
	m.profiles[2] = db.SocialProfile{UserID: 2, Handle: "friend", DisplayName: "A Friend"}
	router := inboxRouter(m)
	code, _, _ := makeProtoRequest(router, "/social/share/send", sendRequest("e1", "hi"), nil)
	assert.Equal(t, http.StatusForbidden, code)

	// Joined but recipient unknown: 404 (no leak).
	m2, router2 := joinedInboxMock(t)
	unknown := sendRequest("e1", "hi")
	unknown.RecipientHandle = "nobody_here"
	code, _, _ = makeProtoRequest(router2, "/social/share/send", unknown, nil)
	assert.Equal(t, http.StatusNotFound, code)

	// Blocked-either-way: identical 404.
	m2.rels[[3]int64{2, 1, int64(relationshipBlock)}] = true
	code, _, _ = makeProtoRequest(router2, "/social/share/send", sendRequest("e1", "hi"), nil)
	assert.Equal(t, http.StatusNotFound, code)

	// Self-send rejected.
	delete(m2.rels, [3]int64{2, 1, int64(relationshipBlock)})
	self := sendRequest("e1", "hi")
	self.RecipientHandle = "sender_one"
	code, _, _ = makeProtoRequest(router2, "/social/share/send", self, nil)
	assert.Equal(t, http.StatusBadRequest, code)

	// Bad note rejected.
	code, _, _ = makeProtoRequest(router2, "/social/share/send", sendRequest("e1", "bad\x00note"), nil)
	assert.Equal(t, http.StatusBadRequest, code)
	code, _, _ = makeProtoRequest(router2, "/social/share/send", sendRequest("e1", strings.Repeat("x", maxNoteLen+1)), nil)
	assert.Equal(t, http.StatusBadRequest, code)
}

func TestSendInboxReadDelete(t *testing.T) {
	m, router := joinedInboxMock(t)

	ack := &pb.SocialAck{}
	code, _, err := makeProtoRequest(router, "/social/share/send", sendRequest("e1", "listen from here"), ack)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.True(t, ack.Success)
	assert.Len(t, m.inbox, 1)
	assert.Equal(t, int64(2), m.inbox[0].recipient)

	// Recipient's inbox (switch the auth subject to user 2 by remapping the
	// test UUID onto the friend's row).
	m.users[testUserUUID] = m.usersByID[2]
	inbox := &pb.InboxResponse{}
	code, _, _ = makeProtoRequest(router, "/social/inbox", &pb.InboxRequest{}, inbox)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, int64(1), inbox.Total)
	assert.Equal(t, int64(1), inbox.Unread)
	assert.Len(t, inbox.Items, 1)
	item := inbox.Items[0]
	assert.Equal(t, "sender_one", item.SenderHandle)
	assert.Equal(t, "listen from here", item.Note)
	assert.Equal(t, int32(870), item.TimestampSeconds)
	assert.False(t, item.Read)

	// Mark read.
	code, _, _ = makeProtoRequest(router, "/social/inbox/read", &pb.InboxMarkReadRequest{Ids: []int64{item.Id}}, nil)
	assert.Equal(t, http.StatusOK, code)
	inbox = &pb.InboxResponse{}
	_, _, _ = makeProtoRequest(router, "/social/inbox", &pb.InboxRequest{}, inbox)
	assert.Equal(t, int64(0), inbox.Unread)
	assert.True(t, inbox.Items[0].Read)

	// Delete.
	code, _, _ = makeProtoRequest(router, "/social/inbox/delete", &pb.InboxDeleteRequest{Id: item.Id}, nil)
	assert.Equal(t, http.StatusOK, code)
	assert.Empty(t, m.inbox)
}

func TestEraseClearsSentItems(t *testing.T) {
	m, router := joinedInboxMock(t)

	code, _, _ := makeProtoRequest(router, "/social/share/send", sendRequest("e1", "soon gone"), nil)
	assert.Equal(t, http.StatusOK, code)
	assert.Len(t, m.inbox, 1)

	// Sender erases: the item disappears from the recipient's inbox.
	code, _, _ = makeProtoRequest(router, "/social/erase", &pb.EraseRequest{}, nil)
	assert.Equal(t, http.StatusOK, code)
	assert.Empty(t, m.inbox, "sent items die with the sender's profile")
}
