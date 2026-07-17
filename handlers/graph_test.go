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

// Follow-graph state for socialMock (feed rows are canned via feedRows —
// the real derivation is SQL, covered by the e2e suite).

type followKey struct{ follower, followee int64 }

func (m *socialMock) ensureGraphState() {
	if m.follows == nil {
		m.follows = map[followKey]int16{}
	}
}

func (m *socialMock) UpsertFollow(ctx context.Context, arg db.UpsertFollowParams) error {
	m.ensureGraphState()
	key := followKey{arg.FollowerUserID, arg.FolloweeUserID}
	if _, exists := m.follows[key]; !exists {
		m.follows[key] = arg.Status
	}
	return nil
}

func (m *socialMock) DeleteFollow(ctx context.Context, arg db.DeleteFollowParams) (int64, error) {
	m.ensureGraphState()
	key := followKey{arg.FollowerUserID, arg.FolloweeUserID}
	if _, ok := m.follows[key]; ok {
		delete(m.follows, key)
		return 1, nil
	}
	return 0, nil
}

func (m *socialMock) ApproveFollow(ctx context.Context, arg db.ApproveFollowParams) (int64, error) {
	m.ensureGraphState()
	key := followKey{arg.FollowerUserID, arg.FolloweeUserID}
	if status, ok := m.follows[key]; ok && status == followStatusPending {
		m.follows[key] = followStatusActive
		return 1, nil
	}
	return 0, nil
}

func (m *socialMock) GetFollowState(ctx context.Context, arg db.GetFollowStateParams) (int16, error) {
	m.ensureGraphState()
	if status, ok := m.follows[followKey{arg.FollowerUserID, arg.FolloweeUserID}]; ok {
		return status, nil
	}
	return 0, pgx.ErrNoRows
}

func (m *socialMock) CountFollowers(ctx context.Context, followeeUserID int64) (int64, error) {
	m.ensureGraphState()
	var n int64
	for key, status := range m.follows {
		if key.followee == followeeUserID && status == followStatusActive {
			n++
		}
	}
	return n, nil
}

func (m *socialMock) CountFollowing(ctx context.Context, followerUserID int64) (int64, error) {
	m.ensureGraphState()
	var n int64
	for key, status := range m.follows {
		if key.follower == followerUserID && status == followStatusActive {
			n++
		}
	}
	return n, nil
}

func (m *socialMock) followRows(match func(followKey, int16) (int64, bool)) []db.GetFollowersRow {
	var rows []db.GetFollowersRow
	for key, status := range m.follows {
		if uid, ok := match(key, status); ok {
			profile := m.profiles[uid]
			rows = append(rows, db.GetFollowersRow{
				UserUuid: m.usersByID[uid].Uuid, Handle: profile.Handle,
				DisplayName: profile.DisplayName, Status: status,
			})
		}
	}
	return rows
}

func (m *socialMock) GetFollowers(ctx context.Context, arg db.GetFollowersParams) ([]db.GetFollowersRow, error) {
	m.ensureGraphState()
	return m.followRows(func(key followKey, status int16) (int64, bool) {
		return key.follower, key.followee == arg.FolloweeUserID && status == followStatusActive
	}), nil
}

func (m *socialMock) GetFollowing(ctx context.Context, arg db.GetFollowingParams) ([]db.GetFollowingRow, error) {
	m.ensureGraphState()
	var rows []db.GetFollowingRow
	for _, row := range m.followRows(func(key followKey, status int16) (int64, bool) {
		return key.followee, key.follower == arg.FollowerUserID
	}) {
		rows = append(rows, db.GetFollowingRow(row))
	}
	return rows, nil
}

func (m *socialMock) GetPendingFollowRequests(ctx context.Context, arg db.GetPendingFollowRequestsParams) ([]db.GetPendingFollowRequestsRow, error) {
	m.ensureGraphState()
	var rows []db.GetPendingFollowRequestsRow
	for _, row := range m.followRows(func(key followKey, status int16) (int64, bool) {
		return key.follower, key.followee == arg.FolloweeUserID && status == followStatusPending
	}) {
		rows = append(rows, db.GetPendingFollowRequestsRow(row))
	}
	return rows, nil
}

func (m *socialMock) DeleteFollowsForUser(ctx context.Context, userID int64) error {
	m.ensureGraphState()
	for key := range m.follows {
		if key.follower == userID || key.followee == userID {
			delete(m.follows, key)
		}
	}
	return nil
}

func (m *socialMock) GetFeedItems(ctx context.Context, arg db.GetFeedItemsParams) ([]db.GetFeedItemsRow, error) {
	return m.feedRows, nil
}

func graphRouter(m *socialMock) *http.ServeMux {
	router := inboxRouter(m)
	h := Handlers{Queries: m, Config: testAuthConfig}
	router.Handle("POST /social/follow", mockAuthMiddleware(http.HandlerFunc(h.PostFollow)))
	router.Handle("POST /social/unfollow", mockAuthMiddleware(http.HandlerFunc(h.PostUnfollow)))
	router.Handle("POST /social/follows", mockAuthMiddleware(http.HandlerFunc(h.PostFollowList)))
	router.Handle("POST /social/follow/requests", mockAuthMiddleware(http.HandlerFunc(h.PostFollowRequests)))
	router.Handle("POST /social/follow/approve", mockAuthMiddleware(http.HandlerFunc(h.PostFollowApprove)))
	router.Handle("POST /social/feed", mockAuthMiddleware(http.HandlerFunc(h.PostFeed)))
	return router
}

// joinedGraphMock: user 1 joined; user 2 joined as "friend".
func joinedGraphMock(t *testing.T) (*socialMock, *http.ServeMux) {
	t.Helper()
	m := newSocialMock()
	m.ensureGraphState()
	router := graphRouter(m)
	joinAs(t, router, "follower_one")
	m.profiles[2] = db.SocialProfile{UserID: 2, Handle: "friend", DisplayName: "A Friend"}
	return m, router
}

func TestFollowOpenAndUnfollow(t *testing.T) {
	m, router := joinedGraphMock(t)

	resp := &pb.FollowResponse{}
	code, _, err := makeProtoRequest(router, "/social/follow", &pb.FollowRequest{Handle: "friend"}, resp)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, pb.FollowState_FOLLOW_STATE_ACTIVE, resp.State)
	assert.Equal(t, followStatusActive, m.follows[followKey{1, 2}])

	// Following list shows the friend.
	list := &pb.FollowListResponse{}
	code, _, _ = makeProtoRequest(router, "/social/follows", &pb.FollowListRequest{Followers: false}, list)
	assert.Equal(t, http.StatusOK, code)
	assert.Len(t, list.Entries, 1)
	assert.Equal(t, "friend", list.Entries[0].Handle)
	assert.Equal(t, int64(1), list.Total)

	// Unfollow: idempotent ack + row gone.
	ack := &pb.SocialAck{}
	code, _, _ = makeProtoRequest(router, "/social/unfollow", &pb.UnfollowRequest{Handle: "friend"}, ack)
	assert.Equal(t, http.StatusOK, code)
	assert.True(t, ack.Success)
	assert.Empty(t, m.follows)
}

func TestFollowApprovalFlow(t *testing.T) {
	m, router := joinedGraphMock(t)
	profile := m.profiles[2]
	profile.RequireFollowApproval = true
	m.profiles[2] = profile

	// Follow becomes a pending request.
	resp := &pb.FollowResponse{}
	code, _, _ := makeProtoRequest(router, "/social/follow", &pb.FollowRequest{Handle: "friend"}, resp)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, pb.FollowState_FOLLOW_STATE_PENDING, resp.State)
	assert.Equal(t, followStatusPending, m.follows[followKey{1, 2}])

	// The friend (auth remapped to user 2) sees + accepts the request.
	m.users[testUserUUID] = m.usersByID[2]
	requests := &pb.FollowListResponse{}
	code, _, _ = makeProtoRequest(router, "/social/follow/requests", &pb.FollowRequestsRequest{}, requests)
	assert.Equal(t, http.StatusOK, code)
	assert.Len(t, requests.Entries, 1)
	assert.Equal(t, "follower_one", requests.Entries[0].Handle)

	code, _, _ = makeProtoRequest(router, "/social/follow/approve",
		&pb.FollowApprovalRequest{RequesterHandle: "follower_one", Accept: true}, nil)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, followStatusActive, m.follows[followKey{1, 2}])
}

func TestFollowBlockedNoLeak(t *testing.T) {
	m, router := joinedGraphMock(t)
	m.rels[[3]int64{2, 1, int64(relationshipBlock)}] = true

	code, _, _ := makeProtoRequest(router, "/social/follow", &pb.FollowRequest{Handle: "friend"}, nil)
	assert.Equal(t, http.StatusNotFound, code, "blocked-either-way reads as missing")

	// Unknown handle: identical 404.
	code, _, _ = makeProtoRequest(router, "/social/follow", &pb.FollowRequest{Handle: "nobody_here"}, nil)
	assert.Equal(t, http.StatusNotFound, code)
}

func TestBlockSeversFollows(t *testing.T) {
	m, router := joinedGraphMock(t)
	m.follows[followKey{1, 2}] = followStatusActive
	m.follows[followKey{2, 1}] = followStatusActive

	code, _, _ := makeProtoRequest(router, "/social/block", &pb.BlockRequest{TargetUserId: otherUserUUID}, nil)
	assert.Equal(t, http.StatusOK, code)
	assert.Empty(t, m.follows, "block severs the graph both directions")
}

func TestFollowerSeesFollowersOnlyFields(t *testing.T) {
	m, router := joinedGraphMock(t)
	profile := m.profiles[2]
	profile.Bio = "followers-only bio"
	profile.BioVisibility = 3 // followers_only
	m.profiles[2] = profile

	// Not following: hidden.
	public := &pb.PublicProfileResponse{}
	code, _, _ := makeProtoRequest(router, "/social/profile/public", &pb.PublicProfileRequest{Handle: "friend"}, public)
	assert.Equal(t, http.StatusOK, code)
	assert.Empty(t, public.Bio)
	assert.Equal(t, pb.FollowState_FOLLOW_STATE_NONE, public.YourFollowState)

	// Active follower: visible, with state + counts.
	m.follows[followKey{1, 2}] = followStatusActive
	public = &pb.PublicProfileResponse{}
	code, _, _ = makeProtoRequest(router, "/social/profile/public", &pb.PublicProfileRequest{Handle: "friend"}, public)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "followers-only bio", public.Bio)
	assert.Equal(t, pb.FollowState_FOLLOW_STATE_ACTIVE, public.YourFollowState)
	assert.Equal(t, int64(1), public.FollowerCount)
}

func TestFeedPlumbing(t *testing.T) {
	m, router := joinedGraphMock(t)
	m.feedRows = []db.GetFeedItemsRow{{
		Kind: 4, ActorHandle: "friend", ActorDisplayName: "A Friend", ActorUuid: otherUserUUID,
		PodcastUuid: "p1", PodcastTitle: "A Podcast", EpisodeUuid: "e1", EpisodeTitle: "An Episode",
		EventAt: time.Unix(1_750_000_000, 0),
	}}

	feed := &pb.FeedResponse{}
	code, _, err := makeProtoRequest(router, "/social/feed", &pb.FeedRequest{}, feed)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.Len(t, feed.Items, 1)
	item := feed.Items[0]
	assert.Equal(t, pb.FeedItemKind_FEED_ITEM_KIND_FINISHED_EPISODE, item.Kind)
	assert.Equal(t, "friend", item.ActorHandle)
	assert.Equal(t, "An Episode", item.EpisodeTitle)
}

func TestEraseClearsFollows(t *testing.T) {
	m, router := joinedGraphMock(t)
	m.follows[followKey{1, 2}] = followStatusActive
	m.follows[followKey{2, 1}] = followStatusActive

	code, _, _ := makeProtoRequest(router, "/social/erase", &pb.EraseRequest{}, nil)
	assert.Equal(t, http.StatusOK, code)
	assert.Empty(t, m.follows)
}
