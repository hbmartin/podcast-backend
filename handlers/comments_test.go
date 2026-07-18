package handlers

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/hbmartin/podcast-backend/db"
	pb "github.com/hbmartin/podcast-backend/protos/api"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
)

// Comment-tree state for socialMock (ADR-0010). The real tree queries are
// SQL; this mirrors their contracts closely enough for handler-level tests —
// the e2e suite covers the actual derivations.

type mockComment struct {
	id           int64
	episodeUuid  string
	podcastUuid  string
	episodeTitle string
	podcastTitle string
	userID       *int64
	parentID     *int64
	rootID       *int64
	text         string
	ts           *int32
	createdAt    time.Time
	editedAt     *time.Time
	removedAt    *time.Time
}

func (m *socialMock) ensureCommentState() {
	if m.playback == nil {
		m.playback = map[reviewKey]db.GetEpisodePlaybackForGateRow{}
	}
	if m.repliesSeenAt == nil {
		m.repliesSeenAt = map[int64]time.Time{}
	}
}

func (m *socialMock) findComment(id int64) *mockComment {
	for _, c := range m.comments {
		if c.id == id {
			return c
		}
	}
	return nil
}

func (m *socialMock) hasChildren(id int64) bool {
	for _, c := range m.comments {
		if c.parentID != nil && *c.parentID == id {
			return true
		}
	}
	return false
}

func (m *socialMock) childCount(id int64) int32 {
	var n int32
	for _, c := range m.comments {
		if c.parentID != nil && *c.parentID == id {
			n++
		}
	}
	return n
}

func (m *socialMock) InsertComment(ctx context.Context, arg db.InsertCommentParams) (db.InsertCommentRow, error) {
	m.ensureCommentState()
	m.commentSeq++
	c := &mockComment{
		id: m.commentSeq, episodeUuid: arg.EpisodeUuid, podcastUuid: arg.PodcastUuid,
		episodeTitle: arg.EpisodeTitle, podcastTitle: arg.PodcastTitle,
		userID: arg.UserID, parentID: arg.ParentID, rootID: arg.RootID,
		text: arg.Text, ts: arg.TimestampSeconds, createdAt: time.Now(),
	}
	m.comments = append(m.comments, c)
	return db.InsertCommentRow{ID: c.id, CreatedAt: c.createdAt}, nil
}

func (m *socialMock) GetCommentByID(ctx context.Context, id int64) (db.GetCommentByIDRow, error) {
	c := m.findComment(id)
	if c == nil {
		return db.GetCommentByIDRow{}, pgx.ErrNoRows
	}
	return db.GetCommentByIDRow{
		ID: c.id, EpisodeUuid: c.episodeUuid, PodcastUuid: c.podcastUuid,
		EpisodeTitle: c.episodeTitle, PodcastTitle: c.podcastTitle,
		UserID: c.userID, ParentID: c.parentID, RootID: c.rootID,
		Text: c.text, TimestampSeconds: c.ts, CreatedAt: c.createdAt,
		EditedAt: c.editedAt, RemovedAt: c.removedAt,
		HasReplies: m.hasChildren(c.id),
	}, nil
}

func (m *socialMock) authorBits(c *mockComment) (uuid *string, handle, name string) {
	if c.userID == nil {
		return nil, "", ""
	}
	if u, ok := m.usersByID[*c.userID]; ok {
		uuid = &u.Uuid
	}
	if p, ok := m.profiles[*c.userID]; ok {
		handle, name = p.Handle, p.DisplayName
	}
	return uuid, handle, name
}


// viewerHidden mirrors the SQL relationship guard: blocked either way, or
// muted by the viewer; tombstones (nil author) always visible.
func (m *socialMock) viewerHidden(viewer *int64, author *int64) bool {
	if viewer == nil || author == nil {
		return false
	}
	if m.rels[[3]int64{*viewer, *author, 0}] || m.rels[[3]int64{*author, *viewer, 0}] {
		return true
	}
	return m.rels[[3]int64{*viewer, *author, 1}]
}

func (m *socialMock) GetEpisodeComments(ctx context.Context, arg db.GetEpisodeCommentsParams) ([]db.GetEpisodeCommentsRow, error) {
	var rows []db.GetEpisodeCommentsRow
	for i := len(m.comments) - 1; i >= 0; i-- { // insertion order ≈ created ASC; reverse for DESC
		c := m.comments[i]
		if c.episodeUuid != arg.EpisodeUuid || c.parentID != nil {
			continue
		}
		if m.viewerHidden(arg.Viewer, c.userID) {
			continue
		}
		uuid, handle, name := m.authorBits(c)
		rows = append(rows, db.GetEpisodeCommentsRow{
			ID: c.id, UserID: c.userID, Text: c.text, TimestampSeconds: c.ts,
			CreatedAt: c.createdAt, EditedAt: c.editedAt, RemovedAt: c.removedAt,
			AuthorUuid: uuid, Handle: handle, DisplayName: name,
			ReplyCount: m.childCount(c.id),
		})
	}
	return rows, nil
}

func (m *socialMock) CountEpisodeComments(ctx context.Context, arg db.CountEpisodeCommentsParams) (int64, error) {
	var n int64
	for _, c := range m.comments {
		if c.episodeUuid == arg.EpisodeUuid && c.parentID == nil {
			n++
		}
	}
	return n, nil
}

func (m *socialMock) GetCommentReplies(ctx context.Context, arg db.GetCommentRepliesParams) ([]db.GetCommentRepliesRow, error) {
	var rows []db.GetCommentRepliesRow
	for _, c := range m.comments {
		if c.parentID == nil || arg.ParentID == nil || *c.parentID != *arg.ParentID {
			continue
		}
		if m.viewerHidden(arg.Viewer, c.userID) {
			continue
		}
		uuid, handle, name := m.authorBits(c)
		rows = append(rows, db.GetCommentRepliesRow{
			ID: c.id, ParentID: c.parentID, UserID: c.userID, Text: c.text,
			TimestampSeconds: c.ts, CreatedAt: c.createdAt, EditedAt: c.editedAt,
			RemovedAt: c.removedAt, AuthorUuid: uuid, Handle: handle, DisplayName: name,
			ReplyCount: m.childCount(c.id),
		})
	}
	return rows, nil
}

func (m *socialMock) CountCommentReplies(ctx context.Context, arg db.CountCommentRepliesParams) (int64, error) {
	if arg.ParentID == nil {
		return 0, nil
	}
	return int64(m.childCount(*arg.ParentID)), nil
}

func (m *socialMock) EditComment(ctx context.Context, arg db.EditCommentParams) (int64, error) {
	c := m.findComment(arg.ID)
	if c == nil || c.removedAt != nil || c.userID == nil || arg.UserID == nil || *c.userID != *arg.UserID {
		return 0, nil
	}
	now := time.Now()
	c.text, c.editedAt = arg.Text, &now
	return 1, nil
}

func (m *socialMock) TombstoneComment(ctx context.Context, arg db.TombstoneCommentParams) (int64, error) {
	c := m.findComment(arg.ID)
	if c == nil || c.removedAt != nil || c.userID == nil || arg.UserID == nil || *c.userID != *arg.UserID {
		return 0, nil
	}
	now := time.Now()
	c.text, c.userID, c.editedAt, c.removedAt = "", nil, nil, &now
	return 1, nil
}

func (m *socialMock) TombstoneCommentsForUser(ctx context.Context, userID *int64) error {
	now := time.Now()
	for _, c := range m.comments {
		if c.userID != nil && userID != nil && *c.userID == *userID && c.removedAt == nil {
			c.text, c.userID, c.editedAt, c.removedAt = "", nil, nil, &now
		}
	}
	return nil
}

func (m *socialMock) GetEpisodePlaybackForGate(ctx context.Context, arg db.GetEpisodePlaybackForGateParams) (db.GetEpisodePlaybackForGateRow, error) {
	m.ensureCommentState()
	if row, ok := m.playback[reviewKey{arg.UserID, arg.EpisodeUuid}]; ok {
		return row, nil
	}
	return db.GetEpisodePlaybackForGateRow{}, pgx.ErrNoRows
}

func (m *socialMock) inboxReplyRows(userID int64) []*mockComment {
	var rows []*mockComment
	for i := len(m.comments) - 1; i >= 0; i-- {
		c := m.comments[i]
		if c.removedAt != nil || c.parentID == nil || c.userID == nil || *c.userID == userID {
			continue
		}
		parent := m.findComment(*c.parentID)
		if parent == nil || parent.userID == nil || *parent.userID != userID {
			continue
		}
		rows = append(rows, c)
	}
	return rows
}

func (m *socialMock) GetInboxReplies(ctx context.Context, arg db.GetInboxRepliesParams) ([]db.GetInboxRepliesRow, error) {
	if arg.UserID == nil {
		return nil, nil
	}
	var rows []db.GetInboxRepliesRow
	for _, c := range m.inboxReplyRows(*arg.UserID) {
		uuid, handle, name := m.authorBits(c)
		var authorUuid string
		if uuid != nil {
			authorUuid = *uuid
		}
		rows = append(rows, db.GetInboxRepliesRow{
			ID: c.id, ParentID: c.parentID, UserID: c.userID, Text: c.text,
			TimestampSeconds: c.ts, CreatedAt: c.createdAt, EditedAt: c.editedAt,
			EpisodeUuid: c.episodeUuid, PodcastUuid: c.podcastUuid,
			EpisodeTitle: c.episodeTitle, PodcastTitle: c.podcastTitle,
			AuthorUuid: authorUuid, Handle: handle, DisplayName: name,
			ReplyCount: m.childCount(c.id),
		})
	}
	return rows, nil
}

func (m *socialMock) CountInboxReplies(ctx context.Context, arg db.CountInboxRepliesParams) (int64, error) {
	if arg.UserID == nil {
		return 0, nil
	}
	return int64(len(m.inboxReplyRows(*arg.UserID))), nil
}

func (m *socialMock) CountUnreadInboxReplies(ctx context.Context, arg db.CountUnreadInboxRepliesParams) (int64, error) {
	if arg.UserID == nil {
		return 0, nil
	}
	m.ensureCommentState()
	seen := m.repliesSeenAt[*arg.UserID]
	var n int64
	for _, c := range m.inboxReplyRows(*arg.UserID) {
		if c.createdAt.After(seen) {
			n++
		}
	}
	return n, nil
}

func (m *socialMock) SetRepliesSeen(ctx context.Context, userID int64) error {
	m.ensureCommentState()
	m.repliesSeenAt[userID] = time.Now()
	return nil
}

func (m *socialMock) HasSocialRelationship(ctx context.Context, arg db.HasSocialRelationshipParams) (bool, error) {
	return m.rels[[3]int64{arg.UserID, arg.TargetUserID, int64(arg.Kind)}], nil
}

// ---- Router + fixtures ----

func commentsRouter(m *socialMock) *http.ServeMux {
	router := graphRouter(m)
	h := Handlers{Queries: m, Config: testAuthConfig}
	router.Handle("POST /social/comment/submit", mockAuthMiddleware(http.HandlerFunc(h.PostCommentSubmit)))
	router.Handle("POST /social/comment/edit", mockAuthMiddleware(http.HandlerFunc(h.PostCommentEdit)))
	router.Handle("POST /social/comment/delete", mockAuthMiddleware(http.HandlerFunc(h.PostCommentDelete)))
	router.Handle("POST /social/comment/replies", mockAuthMiddleware(http.HandlerFunc(h.PostCommentReplies)))
	router.Handle("POST /episode/comments", mockAuthMiddleware(http.HandlerFunc(h.PostEpisodeComments)))
	router.Handle("POST /social/inbox/replies", mockAuthMiddleware(http.HandlerFunc(h.PostInboxReplies)))
	router.Handle("POST /social/inbox/replies/seen", mockAuthMiddleware(http.HandlerFunc(h.PostInboxRepliesSeen)))
	return router
}

const commentedEpisodeUUID = "eeeeeeee-1111-2222-3333-444444444444"

// joinedCommentsMock: user 1 joined + gate satisfied; user 2 joined as "friend".
func joinedCommentsMock(t *testing.T) (*socialMock, *http.ServeMux) {
	t.Helper()
	m := newSocialMock()
	m.ensureCommentState()
	router := commentsRouter(m)
	joinAs(t, router, "commenter_one")
	m.profiles[2] = db.SocialProfile{UserID: 2, Handle: "friend", DisplayName: "A Friend"}
	m.playback[reviewKey{1, commentedEpisodeUUID}] = db.GetEpisodePlaybackForGateRow{
		PlayedUpTo: 300, Duration: 600, PlayingStatus: 2,
	}
	return m, router
}

// otherComment plants a top-level comment by user 2 directly in the mock.
func otherComment(m *socialMock, text string, ts *int32) int64 {
	m.commentSeq++
	id := m.commentSeq
	userID := int64(2)
	m.comments = append(m.comments, &mockComment{
		id: id, episodeUuid: commentedEpisodeUUID, userID: &userID,
		text: text, ts: ts, createdAt: time.Now().Add(-time.Hour),
	})
	return id
}

// ---- Tests ----

func TestCommentGateAndSubmit(t *testing.T) {
	_, router := joinedCommentsMock(t)

	// Unplayed episode: the listen-gate rejects top-level comments.
	code, _, _ := makeProtoRequest(router, "/social/comment/submit",
		&pb.CommentSubmitRequest{EpisodeUuid: "eeeeeeee-9999-9999-9999-999999999999", Text: "hi"}, &pb.SocialComment{})
	assert.Equal(t, http.StatusForbidden, code)

	// Played ≥25%: a timestamped seed (a Moment) lands.
	ts := int32(95)
	resp := &pb.SocialComment{}
	code, _, err := makeProtoRequest(router, "/social/comment/submit",
		&pb.CommentSubmitRequest{EpisodeUuid: commentedEpisodeUUID, Text: "great bit here", TimestampSeconds: &ts}, resp)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "commenter_one", resp.Handle)
	assert.NotNil(t, resp.TimestampSeconds)

	// Replies skip the gate entirely — even on the ungated episode rule they
	// attach to the parent's episode; and a timestamp on a reply is rejected.
	parentID := resp.Id
	code, _, _ = makeProtoRequest(router, "/social/comment/submit",
		&pb.CommentSubmitRequest{EpisodeUuid: commentedEpisodeUUID, Text: "reply", ParentId: parentID, TimestampSeconds: &ts}, &pb.SocialComment{})
	assert.Equal(t, http.StatusBadRequest, code)

	reply := &pb.SocialComment{}
	code, _, _ = makeProtoRequest(router, "/social/comment/submit",
		&pb.CommentSubmitRequest{EpisodeUuid: commentedEpisodeUUID, Text: "reply", ParentId: parentID}, reply)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, parentID, reply.ParentId)

	// The episode list carries the seed with its reply count.
	list := &pb.CommentsResponse{}
	code, _, _ = makeProtoRequest(router, "/episode/comments",
		&pb.EpisodeCommentsRequest{EpisodeUuid: commentedEpisodeUUID}, list)
	assert.Equal(t, http.StatusOK, code)
	assert.Len(t, list.Comments, 1)
	assert.Equal(t, int32(1), list.Comments[0].ReplyCount)

	replies := &pb.CommentsResponse{}
	code, _, _ = makeProtoRequest(router, "/social/comment/replies",
		&pb.CommentRepliesRequest{ParentId: parentID}, replies)
	assert.Equal(t, http.StatusOK, code)
	assert.Len(t, replies.Comments, 1)
}

func TestCommentEditGraceWindow(t *testing.T) {
	m, router := joinedCommentsMock(t)

	resp := &pb.SocialComment{}
	makeProtoRequest(router, "/social/comment/submit",
		&pb.CommentSubmitRequest{EpisodeUuid: commentedEpisodeUUID, Text: "first thoughts"}, resp)

	// Within the grace window and unreplied: edit succeeds.
	ack := &pb.SocialAck{}
	code, _, _ := makeProtoRequest(router, "/social/comment/edit",
		&pb.CommentEditRequest{Id: resp.Id, Text: "second thoughts"}, ack)
	assert.Equal(t, http.StatusOK, code)
	assert.True(t, ack.Success)

	// A reply arrives: the window slams shut.
	makeProtoRequest(router, "/social/comment/submit",
		&pb.CommentSubmitRequest{EpisodeUuid: commentedEpisodeUUID, Text: "reply", ParentId: resp.Id}, &pb.SocialComment{})
	code, _, _ = makeProtoRequest(router, "/social/comment/edit",
		&pb.CommentEditRequest{Id: resp.Id, Text: "third thoughts"}, &pb.SocialAck{})
	assert.Equal(t, http.StatusConflict, code)

	// Expired window (backdated creation): also locked.
	expired := &pb.SocialComment{}
	makeProtoRequest(router, "/social/comment/submit",
		&pb.CommentSubmitRequest{EpisodeUuid: commentedEpisodeUUID, Text: "old"}, expired)
	m.findComment(expired.Id).createdAt = time.Now().Add(-time.Hour)
	code, _, _ = makeProtoRequest(router, "/social/comment/edit",
		&pb.CommentEditRequest{Id: expired.Id, Text: "too late"}, &pb.SocialAck{})
	assert.Equal(t, http.StatusConflict, code)
}

func TestCommentDeleteTombstones(t *testing.T) {
	m, router := joinedCommentsMock(t)

	seed := &pb.SocialComment{}
	makeProtoRequest(router, "/social/comment/submit",
		&pb.CommentSubmitRequest{EpisodeUuid: commentedEpisodeUUID, Text: "regrets"}, seed)
	makeProtoRequest(router, "/social/comment/submit",
		&pb.CommentSubmitRequest{EpisodeUuid: commentedEpisodeUUID, Text: "reply", ParentId: seed.Id}, &pb.SocialComment{})

	// Deleting someone else's comment: not found.
	otherID := otherComment(m, "not yours", nil)
	code, _, _ := makeProtoRequest(router, "/social/comment/delete",
		&pb.CommentDeleteRequest{Id: otherID}, &pb.SocialAck{})
	assert.Equal(t, http.StatusNotFound, code)

	// Own delete tombstones: placeholder stays, text and author gone, reply survives.
	code, _, _ = makeProtoRequest(router, "/social/comment/delete",
		&pb.CommentDeleteRequest{Id: seed.Id}, &pb.SocialAck{})
	assert.Equal(t, http.StatusOK, code)

	list := &pb.CommentsResponse{}
	makeProtoRequest(router, "/episode/comments", &pb.EpisodeCommentsRequest{EpisodeUuid: commentedEpisodeUUID}, list)
	var tombstone *pb.SocialComment
	for _, c := range list.Comments {
		if c.Id == seed.Id {
			tombstone = c
		}
	}
	assert.NotNil(t, tombstone)
	assert.True(t, tombstone.Removed)
	assert.Empty(t, tombstone.Text)
	assert.Empty(t, tombstone.Handle)
	assert.Equal(t, int32(1), tombstone.ReplyCount)
}

func TestCommentListHidesBlockedAndMuted(t *testing.T) {
	m, router := joinedCommentsMock(t)
	otherComment(m, "from a friend", nil)

	list := &pb.CommentsResponse{}
	makeProtoRequest(router, "/episode/comments", &pb.EpisodeCommentsRequest{EpisodeUuid: commentedEpisodeUUID}, list)
	assert.Len(t, list.Comments, 1)

	// Muted: one-way hide.
	m.rels[[3]int64{1, 2, 1}] = true
	makeProtoRequest(router, "/episode/comments", &pb.EpisodeCommentsRequest{EpisodeUuid: commentedEpisodeUUID}, list)
	assert.Empty(t, list.Comments)

	// Blocked (either direction) hides too.
	delete(m.rels, [3]int64{1, 2, 1})
	m.rels[[3]int64{2, 1, 0}] = true
	list = &pb.CommentsResponse{}
	makeProtoRequest(router, "/episode/comments", &pb.EpisodeCommentsRequest{EpisodeUuid: commentedEpisodeUUID}, list)
	assert.Empty(t, list.Comments)
}

func TestInboxRepliesWatermark(t *testing.T) {
	m, router := joinedCommentsMock(t)

	seed := &pb.SocialComment{}
	makeProtoRequest(router, "/social/comment/submit",
		&pb.CommentSubmitRequest{EpisodeUuid: commentedEpisodeUUID, Text: "mine", EpisodeTitle: "An Episode"}, seed)

	// A reply from user 2, planted directly.
	m.commentSeq++
	replyID := m.commentSeq
	otherID := int64(2)
	parentID := seed.Id
	m.comments = append(m.comments, &mockComment{
		id: replyID, episodeUuid: commentedEpisodeUUID, episodeTitle: "An Episode",
		userID: &otherID, parentID: &parentID, text: "theirs", createdAt: time.Now(),
	})
	// My own reply to my own comment must NOT appear in my inbox.
	makeProtoRequest(router, "/social/comment/submit",
		&pb.CommentSubmitRequest{EpisodeUuid: commentedEpisodeUUID, Text: "self reply", ParentId: seed.Id}, &pb.SocialComment{})

	inbox := &pb.InboxRepliesResponse{}
	code, _, _ := makeProtoRequest(router, "/social/inbox/replies", &pb.InboxRepliesRequest{}, inbox)
	assert.Equal(t, http.StatusOK, code)
	assert.Len(t, inbox.Replies, 1)
	assert.Equal(t, "friend", inbox.Replies[0].Handle)
	assert.Equal(t, "An Episode", inbox.Replies[0].EpisodeTitle)
	assert.Equal(t, int32(1), inbox.Unread)

	// Seen: the watermark advances and unread drops to zero.
	makeProtoRequest(router, "/social/inbox/replies/seen", &pb.InboxRepliesRequest{}, &pb.SocialAck{})
	makeProtoRequest(router, "/social/inbox/replies", &pb.InboxRepliesRequest{}, inbox)
	assert.Equal(t, int32(0), inbox.Unread)
	assert.Len(t, inbox.Replies, 1)
}

func TestEraseTombstonesComments(t *testing.T) {
	m, router := joinedCommentsMock(t)
	makeProtoRequest(router, "/social/comment/submit",
		&pb.CommentSubmitRequest{EpisodeUuid: commentedEpisodeUUID, Text: "to be erased"}, &pb.SocialComment{})

	code, _, _ := makeProtoRequest(router, "/social/erase", &pb.EraseRequest{}, &pb.SocialAck{})
	assert.Equal(t, http.StatusOK, code)

	assert.NotNil(t, m.comments[0].removedAt)
	assert.Empty(t, m.comments[0].text)
	assert.Nil(t, m.comments[0].userID)
}
