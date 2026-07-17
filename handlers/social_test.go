package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hbmartin/podcast-backend/db"
	pb "github.com/hbmartin/podcast-backend/protos/api"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
)

// socialMock is a stateful in-memory implementation of the social queries so
// handler tests can walk the whole join → read → block → erase loop
// coherently. Everything else falls through to QuerierMock (panics if
// unstubbed).
type socialMock struct {
	QuerierMock

	users     map[string]db.User // uuid -> user (auth subject + targets)
	usersByID map[int64]db.User
	handles   map[string]*handleRow
	profiles  map[int64]db.SocialProfile
	rels      map[[3]int64]bool // (user, target, kind)
	reports   []db.InsertModerationReportParams

	// Reviews + reactions state (methods in reviews_test.go; lazily built by
	// ensureReviewState). reviewKey doubles as (user, episode) for reactions.
	reviews      map[reviewKey]db.PodcastReview
	ratings      map[reviewKey]int16
	playedCounts map[reviewKey]int64
	reactions    map[reviewKey]int16

	// Inbox state (methods in inbox_test.go).
	inbox []*inboxItem

	// Graph state (methods in graph_test.go).
	follows  map[followKey]int16
	feedRows []db.GetFeedItemsRow
}

type handleRow struct {
	userID *int64
	status int16
}

func newSocialMock() *socialMock {
	m := &socialMock{
		users:     map[string]db.User{},
		usersByID: map[int64]db.User{},
		handles:   map[string]*handleRow{},
		profiles:  map[int64]db.SocialProfile{},
		rels:      map[[3]int64]bool{},
	}
	m.addUser(db.User{ID: 1, Uuid: testUserUUID, Email: "mail@test.com"})
	m.addUser(db.User{ID: 2, Uuid: otherUserUUID, Email: "other@test.com"})
	return m
}

func (m *socialMock) addUser(u db.User) {
	m.users[u.Uuid] = u
	m.usersByID[u.ID] = u
}

func (m *socialMock) InTx(ctx context.Context, fn func(db.Querier) error) error {
	return fn(m)
}

func (m *socialMock) GetUserByUUID(ctx context.Context, uuid string) (db.User, error) {
	if u, ok := m.users[strings.ToLower(uuid)]; ok {
		return u, nil
	}
	return db.User{}, pgx.ErrNoRows
}

func (m *socialMock) GetUserByID(ctx context.Context, id int64) (db.User, error) {
	if u, ok := m.usersByID[id]; ok {
		return u, nil
	}
	return db.User{}, pgx.ErrNoRows
}

func (m *socialMock) GetHandleStatus(ctx context.Context, handle string) (db.GetHandleStatusRow, error) {
	row, ok := m.handles[handle]
	if !ok {
		return db.GetHandleStatusRow{}, pgx.ErrNoRows
	}
	return db.GetHandleStatusRow{Handle: handle, UserID: row.userID, Status: row.status}, nil
}

func (m *socialMock) ClaimHandle(ctx context.Context, arg db.ClaimHandleParams) error {
	if _, exists := m.handles[arg.Handle]; exists {
		return &pgconn.PgError{Code: "23505"}
	}
	for _, row := range m.handles {
		if row.userID != nil && arg.UserID != nil && *row.userID == *arg.UserID {
			return &pgconn.PgError{Code: "23505"}
		}
	}
	m.handles[arg.Handle] = &handleRow{userID: arg.UserID, status: handleStatusActive}
	return nil
}

func (m *socialMock) CreateSocialProfile(ctx context.Context, arg db.CreateSocialProfileParams) (db.SocialProfile, error) {
	profile := db.SocialProfile{
		UserID:                  arg.UserID,
		Handle:                  arg.Handle,
		DisplayName:             arg.DisplayName,
		TermsVersion:            arg.TermsVersion,
		AvatarVisibility:        1,
		BioVisibility:           1,
		FollowedShowsVisibility: 1,
		TopPodcastsVisibility:   1,
		StatsVisibility:         1,
		HistoryVisibility:       1,
		PresenceVisibility:      1,
	}
	m.profiles[arg.UserID] = profile
	return profile, nil
}

func (m *socialMock) GetSocialProfileByUserID(ctx context.Context, userID int64) (db.SocialProfile, error) {
	if p, ok := m.profiles[userID]; ok {
		return p, nil
	}
	return db.SocialProfile{}, pgx.ErrNoRows
}

func (m *socialMock) GetSocialProfileByHandle(ctx context.Context, handle string) (db.SocialProfile, error) {
	for _, p := range m.profiles {
		if p.Handle == handle {
			return p, nil
		}
	}
	return db.SocialProfile{}, pgx.ErrNoRows
}

func (m *socialMock) UpdateSocialProfile(ctx context.Context, arg db.UpdateSocialProfileParams) (db.SocialProfile, error) {
	p, ok := m.profiles[arg.UserID]
	if !ok {
		return db.SocialProfile{}, pgx.ErrNoRows
	}
	p.DisplayName = arg.DisplayName
	p.Bio = arg.Bio
	p.AvatarVisibility = arg.AvatarVisibility
	p.BioVisibility = arg.BioVisibility
	p.FollowedShowsVisibility = arg.FollowedShowsVisibility
	p.TopPodcastsVisibility = arg.TopPodcastsVisibility
	p.StatsVisibility = arg.StatsVisibility
	p.HistoryVisibility = arg.HistoryVisibility
	p.PresenceVisibility = arg.PresenceVisibility
	m.profiles[arg.UserID] = p
	return p, nil
}

func (m *socialMock) UpsertSocialRelationship(ctx context.Context, arg db.UpsertSocialRelationshipParams) error {
	m.rels[[3]int64{arg.UserID, arg.TargetUserID, int64(arg.Kind)}] = true
	return nil
}

func (m *socialMock) DeleteSocialRelationship(ctx context.Context, arg db.DeleteSocialRelationshipParams) (int64, error) {
	key := [3]int64{arg.UserID, arg.TargetUserID, int64(arg.Kind)}
	if m.rels[key] {
		delete(m.rels, key)
		return 1, nil
	}
	return 0, nil
}

func (m *socialMock) IsBlockedEither(ctx context.Context, arg db.IsBlockedEitherParams) (bool, error) {
	return m.rels[[3]int64{arg.UserID, arg.TargetUserID, int64(relationshipBlock)}] ||
		m.rels[[3]int64{arg.TargetUserID, arg.UserID, int64(relationshipBlock)}], nil
}

func (m *socialMock) InsertModerationReport(ctx context.Context, arg db.InsertModerationReportParams) error {
	m.reports = append(m.reports, arg)
	return nil
}

func (m *socialMock) DeleteSocialProfile(ctx context.Context, userID int64) (int64, error) {
	if _, ok := m.profiles[userID]; ok {
		delete(m.profiles, userID)
		return 1, nil
	}
	return 0, nil
}

func (m *socialMock) TombstoneHandle(ctx context.Context, userID *int64) (int64, error) {
	var n int64
	for _, row := range m.handles {
		if row.userID != nil && userID != nil && *row.userID == *userID && row.status == handleStatusActive {
			row.status = handleStatusTombstoned
			row.userID = nil
			n++
		}
	}
	return n, nil
}

func (m *socialMock) DeleteRelationshipsForUser(ctx context.Context, userID int64) error {
	for key := range m.rels {
		if key[0] == userID || key[1] == userID {
			delete(m.rels, key)
		}
	}
	return nil
}

// Section queries: canned rows so visibility gating is observable.

func (m *socialMock) GetPublicFollowedShows(ctx context.Context, arg db.GetPublicFollowedShowsParams) ([]db.GetPublicFollowedShowsRow, error) {
	return []db.GetPublicFollowedShowsRow{{PodcastUuid: "aaaaaaaa-0000-0000-0000-000000000001", Title: "Followed Show", Author: "An Author"}}, nil
}

func (m *socialMock) GetPublicTopPodcasts(ctx context.Context, arg db.GetPublicTopPodcastsParams) ([]db.GetPublicTopPodcastsRow, error) {
	return []db.GetPublicTopPodcastsRow{{PodcastUuid: "aaaaaaaa-0000-0000-0000-000000000002", Title: "Top Podcast", Author: "An Author", PlayedSeconds: 3600}}, nil
}

func (m *socialMock) GetPublicRecentlyPlayed(ctx context.Context, arg db.GetPublicRecentlyPlayedParams) ([]db.GetPublicRecentlyPlayedRow, error) {
	return []db.GetPublicRecentlyPlayedRow{{EpisodeUuid: "aaaaaaaa-0000-0000-0000-000000000003", PodcastUuid: "aaaaaaaa-0000-0000-0000-000000000002", Title: "Recent Episode", ModifiedAt: 1700000000000}}, nil
}

func (m *socialMock) GetUserStatsTotals(ctx context.Context, userID int64) (db.GetUserStatsTotalsRow, error) {
	return db.GetUserStatsTotalsRow{TimeListened: 7200, EarliestStartedAt: 1600000000}, nil
}

const otherUserUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

// socialRouter mirrors main.go's social routes: authed routes behind the
// mock auth middleware, availability plain, and the public read registered
// twice — authed (viewer = testUserUUID) and anonymous.
func socialRouter(m *socialMock) *http.ServeMux {
	router := http.NewServeMux()
	h := Handlers{Queries: m, Config: testAuthConfig}

	router.HandleFunc("POST /social/handle/availability", h.PostSocialHandleAvailability)
	router.Handle("POST /social/join", mockAuthMiddleware(http.HandlerFunc(h.PostSocialJoin)))
	router.Handle("POST /social/profile/get", mockAuthMiddleware(http.HandlerFunc(h.PostSocialProfileGet)))
	router.Handle("POST /social/profile/update", mockAuthMiddleware(http.HandlerFunc(h.PostSocialProfileUpdate)))
	router.Handle("POST /social/profile/public", mockAuthMiddleware(http.HandlerFunc(h.PostSocialProfilePublic)))
	router.HandleFunc("POST /anon/social/profile/public", h.PostSocialProfilePublic)
	router.Handle("POST /social/block", mockAuthMiddleware(http.HandlerFunc(h.PostSocialBlock)))
	router.Handle("POST /social/unblock", mockAuthMiddleware(http.HandlerFunc(h.PostSocialUnblock)))
	router.Handle("POST /social/mute", mockAuthMiddleware(http.HandlerFunc(h.PostSocialMute)))
	router.Handle("POST /social/unmute", mockAuthMiddleware(http.HandlerFunc(h.PostSocialUnmute)))
	router.Handle("POST /social/report", mockAuthMiddleware(http.HandlerFunc(h.PostSocialReport)))
	router.Handle("POST /social/erase", mockAuthMiddleware(http.HandlerFunc(h.PostSocialErase)))
	router.Handle("POST /user/delete_account", mockAuthMiddleware(http.HandlerFunc(h.PostDeleteAccount)))

	return router
}

func joinAs(t *testing.T, router *http.ServeMux, handle string) *pb.SocialProfile {
	t.Helper()
	resp := &pb.JoinResponse{}
	code, _, err := makeProtoRequest(router, "/social/join",
		&pb.JoinRequest{Handle: handle, AcceptedTermsVersion: 1, DisplayName: "Test Person"}, resp)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	return resp.Profile
}

func TestHandleAvailabilityStates(t *testing.T) {
	m := newSocialMock()
	m.handles["taken_one"] = &handleRow{status: handleStatusActive}
	m.handles["ghost"] = &handleRow{status: handleStatusTombstoned}
	m.handles["admin"] = &handleRow{status: handleStatusReserved}
	router := socialRouter(m)

	cases := []struct {
		raw        string
		status     pb.HandleStatus
		normalized string
	}{
		{"fresh_name", pb.HandleStatus_HANDLE_STATUS_AVAILABLE, "fresh_name"},
		{"  @Fresh_Name ", pb.HandleStatus_HANDLE_STATUS_AVAILABLE, "fresh_name"},
		{"taken_one", pb.HandleStatus_HANDLE_STATUS_TAKEN, "taken_one"},
		{"ghost", pb.HandleStatus_HANDLE_STATUS_TOMBSTONED, "ghost"},
		{"admin", pb.HandleStatus_HANDLE_STATUS_RESERVED, "admin"},
		{"ab", pb.HandleStatus_HANDLE_STATUS_INVALID, "ab"},
		{"has space", pb.HandleStatus_HANDLE_STATUS_INVALID, "has space"},
		{strings.Repeat("x", 31), pb.HandleStatus_HANDLE_STATUS_INVALID, strings.Repeat("x", 31)},
	}
	for _, c := range cases {
		resp := &pb.HandleAvailabilityResponse{}
		code, _, err := makeProtoRequest(router, "/social/handle/availability",
			&pb.HandleAvailabilityRequest{Handle: c.raw}, resp)
		assert.NoError(t, err, c.raw)
		assert.Equal(t, http.StatusOK, code, c.raw)
		assert.Equal(t, c.status, resp.Status, c.raw)
		assert.Equal(t, c.normalized, resp.NormalizedHandle, c.raw)
	}
}

func TestJoinHappyPath(t *testing.T) {
	m := newSocialMock()
	router := socialRouter(m)

	profile := joinAs(t, router, "@Cool_Person")

	assert.Equal(t, "cool_person", profile.Handle)
	assert.Equal(t, "Test Person", profile.DisplayName)
	assert.Equal(t, testUserUUID, profile.UserId)
	assert.Equal(t, int32(1), profile.TermsVersion)
	// Everything defaults private (ADR-0006).
	assert.Equal(t, pb.SocialVisibility_SOCIAL_VISIBILITY_PRIVATE, profile.BioVisibility)
	assert.Equal(t, pb.SocialVisibility_SOCIAL_VISIBILITY_PRIVATE, profile.StatsVisibility)
	assert.Empty(t, profile.AvatarUrl)
	assert.NotNil(t, m.handles["cool_person"])
	assert.Equal(t, handleStatusActive, m.handles["cool_person"].status)
}

func TestJoinDuplicateHandleLoses(t *testing.T) {
	m := newSocialMock()
	m.handles["wanted"] = &handleRow{status: handleStatusActive}
	router := socialRouter(m)

	code, _, _ := makeProtoRequest(router, "/social/join",
		&pb.JoinRequest{Handle: "wanted", AcceptedTermsVersion: 1, DisplayName: "Me"}, nil)
	assert.Equal(t, http.StatusConflict, code)
}

func TestJoinTwiceRejected(t *testing.T) {
	m := newSocialMock()
	router := socialRouter(m)
	joinAs(t, router, "first_handle")

	code, _, _ := makeProtoRequest(router, "/social/join",
		&pb.JoinRequest{Handle: "second_handle", AcceptedTermsVersion: 1, DisplayName: "Me"}, nil)
	assert.Equal(t, http.StatusConflict, code)
}

func TestJoinValidation(t *testing.T) {
	m := newSocialMock()
	router := socialRouter(m)

	// No terms acceptance.
	code, _, _ := makeProtoRequest(router, "/social/join",
		&pb.JoinRequest{Handle: "valid_name", DisplayName: "Me"}, nil)
	assert.Equal(t, http.StatusBadRequest, code)

	// Empty display name.
	code, _, _ = makeProtoRequest(router, "/social/join",
		&pb.JoinRequest{Handle: "valid_name", AcceptedTermsVersion: 1, DisplayName: "   "}, nil)
	assert.Equal(t, http.StatusBadRequest, code)

	// Control characters in display name.
	code, _, _ = makeProtoRequest(router, "/social/join",
		&pb.JoinRequest{Handle: "valid_name", AcceptedTermsVersion: 1, DisplayName: "bad\x00name"}, nil)
	assert.Equal(t, http.StatusBadRequest, code)

	// Invalid handle.
	code, _, _ = makeProtoRequest(router, "/social/join",
		&pb.JoinRequest{Handle: "No Spaces Allowed", AcceptedTermsVersion: 1, DisplayName: "Me"}, nil)
	assert.Equal(t, http.StatusBadRequest, code)
}

func TestProfileGet(t *testing.T) {
	m := newSocialMock()
	router := socialRouter(m)

	// Not joined yet: 404.
	code, _, _ := makeProtoRequest(router, "/social/profile/get", &pb.ProfileGetRequest{}, nil)
	assert.Equal(t, http.StatusNotFound, code)

	joinAs(t, router, "reader")

	resp := &pb.ProfileResponse{}
	code, _, err := makeProtoRequest(router, "/social/profile/get", &pb.ProfileGetRequest{}, resp)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "reader", resp.Profile.Handle)
}

func TestProfileUpdate(t *testing.T) {
	m := newSocialMock()
	router := socialRouter(m)
	joinAs(t, router, "editor")

	resp := &pb.ProfileResponse{}
	code, _, err := makeProtoRequest(router, "/social/profile/update", &pb.ProfileUpdateRequest{
		DisplayName:     "New Name",
		Bio:             "A bio.",
		BioVisibility:   pb.SocialVisibility_SOCIAL_VISIBILITY_PUBLIC,
		StatsVisibility: pb.SocialVisibility_SOCIAL_VISIBILITY_FOLLOWERS_ONLY,
		// Everything else unspecified (0) must fold to private.
	}, resp)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "New Name", resp.Profile.DisplayName)
	assert.Equal(t, "A bio.", resp.Profile.Bio)
	assert.Equal(t, pb.SocialVisibility_SOCIAL_VISIBILITY_PUBLIC, resp.Profile.BioVisibility)
	assert.Equal(t, pb.SocialVisibility_SOCIAL_VISIBILITY_FOLLOWERS_ONLY, resp.Profile.StatsVisibility)
	assert.Equal(t, pb.SocialVisibility_SOCIAL_VISIBILITY_PRIVATE, resp.Profile.HistoryVisibility)
	// Handle is immutable — unchanged by update.
	assert.Equal(t, "editor", resp.Profile.Handle)
}

func TestPublicProfileVisibility(t *testing.T) {
	m := newSocialMock()
	// Other user (ID 2) owns the profile; the authed viewer is user 1.
	ownerID := int64(2)
	m.handles["target"] = &handleRow{userID: &ownerID, status: handleStatusActive}
	m.profiles[2] = db.SocialProfile{
		UserID: 2, Handle: "target", DisplayName: "Target", Bio: "secret bio",
		BioVisibility: 1, StatsVisibility: 1,
	}
	router := socialRouter(m)

	// Private bio hidden from a non-owner viewer.
	resp := &pb.PublicProfileResponse{}
	code, _, err := makeProtoRequest(router, "/social/profile/public",
		&pb.PublicProfileRequest{Handle: "target"}, resp)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "Target", resp.DisplayName)
	assert.Equal(t, otherUserUUID, resp.UserId)
	assert.Empty(t, resp.Bio)
	assert.False(t, resp.HasStats)

	// Private sections are absent too.
	assert.Empty(t, resp.FollowedShows)
	assert.Empty(t, resp.TopPodcasts)
	assert.Nil(t, resp.Stats)
	assert.Empty(t, resp.RecentlyPlayed)

	// Public bio shown, including to anonymous viewers.
	p := m.profiles[2]
	p.BioVisibility = 2
	p.StatsVisibility = 2
	p.FollowedShowsVisibility = 2
	p.TopPodcastsVisibility = 2
	p.HistoryVisibility = 2
	m.profiles[2] = p

	resp = &pb.PublicProfileResponse{}
	code, _, _ = makeProtoRequest(router, "/anon/social/profile/public",
		&pb.PublicProfileRequest{Handle: "target"}, resp)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "secret bio", resp.Bio)
	assert.True(t, resp.HasStats)

	// Public sections populated from the section queries.
	assert.Len(t, resp.FollowedShows, 1)
	assert.Equal(t, "Followed Show", resp.FollowedShows[0].Title)
	assert.Len(t, resp.TopPodcasts, 1)
	assert.Equal(t, int64(3600), resp.TopPodcasts[0].PlayedSeconds)
	assert.NotNil(t, resp.Stats)
	assert.Equal(t, int64(7200), resp.Stats.TimeListenedSeconds)
	assert.Len(t, resp.RecentlyPlayed, 1)
	assert.Equal(t, "Recent Episode", resp.RecentlyPlayed[0].Title)

	// Unknown handle: 404.
	code, _, _ = makeProtoRequest(router, "/social/profile/public",
		&pb.PublicProfileRequest{Handle: "nobody_here"}, nil)
	assert.Equal(t, http.StatusNotFound, code)
}

func TestPublicProfileBlockedLooksLikeMissing(t *testing.T) {
	m := newSocialMock()
	m.profiles[2] = db.SocialProfile{UserID: 2, Handle: "target", DisplayName: "Target", BioVisibility: 2}
	router := socialRouter(m)

	// Block in the target→viewer direction hides the profile from the viewer.
	m.rels[[3]int64{2, 1, int64(relationshipBlock)}] = true

	code, _, _ := makeProtoRequest(router, "/social/profile/public",
		&pb.PublicProfileRequest{Handle: "target"}, nil)
	assert.Equal(t, http.StatusNotFound, code)

	// Anonymous viewers are unaffected by the block edge.
	code, _, _ = makeProtoRequest(router, "/anon/social/profile/public",
		&pb.PublicProfileRequest{Handle: "target"}, nil)
	assert.Equal(t, http.StatusOK, code)
}

func TestBlockMuteAcks(t *testing.T) {
	m := newSocialMock()
	router := socialRouter(m)

	ack := &pb.SocialAck{}
	code, _, err := makeProtoRequest(router, "/social/block",
		&pb.BlockRequest{TargetUserId: otherUserUUID}, ack)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.True(t, ack.Success)
	assert.True(t, m.rels[[3]int64{1, 2, int64(relationshipBlock)}])

	ack = &pb.SocialAck{}
	code, _, _ = makeProtoRequest(router, "/social/unblock",
		&pb.BlockRequest{TargetUserId: otherUserUUID}, ack)
	assert.Equal(t, http.StatusOK, code)
	assert.True(t, ack.Success)
	assert.False(t, m.rels[[3]int64{1, 2, int64(relationshipBlock)}])

	ack = &pb.SocialAck{}
	code, _, _ = makeProtoRequest(router, "/social/mute",
		&pb.MuteRequest{TargetUserId: otherUserUUID}, ack)
	assert.Equal(t, http.StatusOK, code)
	assert.True(t, ack.Success)
	assert.True(t, m.rels[[3]int64{1, 2, int64(relationshipMute)}])

	// Self-target rejected.
	code, _, _ = makeProtoRequest(router, "/social/block",
		&pb.BlockRequest{TargetUserId: testUserUUID}, nil)
	assert.Equal(t, http.StatusBadRequest, code)

	// Unknown target: 404.
	code, _, _ = makeProtoRequest(router, "/social/block",
		&pb.BlockRequest{TargetUserId: "99999999-9999-9999-9999-999999999999"}, nil)
	assert.Equal(t, http.StatusNotFound, code)
}

func TestReport(t *testing.T) {
	m := newSocialMock()
	router := socialRouter(m)

	longContext := strings.Repeat("x", maxReportContextLen+50)
	ack := &pb.SocialAck{}
	code, _, err := makeProtoRequest(router, "/social/report", &pb.ReportRequest{
		TargetUserId: otherUserUUID,
		Reason:       pb.ReportReason_REPORT_REASON_HARASSMENT,
		Context:      longContext,
	}, ack)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.True(t, ack.Success)

	assert.Len(t, m.reports, 1)
	report := m.reports[0]
	assert.Equal(t, int64(2), report.TargetUserID)
	assert.Equal(t, int64(1), *report.ReporterUserID)
	assert.Equal(t, "community_flag", report.Source)
	assert.Equal(t, int16(pb.ReportReason_REPORT_REASON_HARASSMENT), report.Reason)
	assert.Len(t, report.Context, maxReportContextLen)
}

func TestEraseTombstonesHandle(t *testing.T) {
	m := newSocialMock()
	router := socialRouter(m)
	joinAs(t, router, "leaving")
	m.rels[[3]int64{1, 2, int64(relationshipBlock)}] = true

	ack := &pb.SocialAck{}
	code, _, err := makeProtoRequest(router, "/social/erase", &pb.EraseRequest{}, ack)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.True(t, ack.Success)

	// Profile gone, handle tombstoned with the account link nulled, edges gone.
	assert.Empty(t, m.profiles)
	assert.Equal(t, handleStatusTombstoned, m.handles["leaving"].status)
	assert.Nil(t, m.handles["leaving"].userID)
	assert.Empty(t, m.rels)

	// The tombstoned handle is never claimable again.
	avail := &pb.HandleAvailabilityResponse{}
	code, _, _ = makeProtoRequest(router, "/social/handle/availability",
		&pb.HandleAvailabilityRequest{Handle: "leaving"}, avail)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, pb.HandleStatus_HANDLE_STATUS_TOMBSTONED, avail.Status)

	// Erase is idempotent for a no-longer/never-joined account.
	ack = &pb.SocialAck{}
	code, _, _ = makeProtoRequest(router, "/social/erase", &pb.EraseRequest{}, ack)
	assert.Equal(t, http.StatusOK, code)
	assert.True(t, ack.Success)
}

func TestPublicProfilePage(t *testing.T) {
	m := newSocialMock()
	m.profiles[2] = db.SocialProfile{
		UserID: 2, Handle: "webby", DisplayName: "Web <Person>", Bio: "bio & things",
		BioVisibility: 2, FollowedShowsVisibility: 2,
	}
	router := http.NewServeMux()
	h := Handlers{Queries: m, Config: testAuthConfig}
	router.HandleFunc("GET /u/{handle}", h.GetPublicProfilePage)

	req, _ := http.NewRequest("GET", "/u/webby", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Header().Get("Content-Type"), "text/html")
	body := rr.Body.String()
	// html/template escapes interpolations.
	assert.Contains(t, body, "Web &lt;Person&gt;")
	assert.Contains(t, body, "bio &amp; things")
	assert.Contains(t, body, "@webby")
	assert.Contains(t, body, "Followed Show")
	assert.Contains(t, body, "thcast://profile/webby")
	// Hidden sections absent.
	assert.NotContains(t, body, "Top podcasts")

	// Unknown handle: HTML 404.
	req, _ = http.NewRequest("GET", "/u/nobody_home", nil)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestDeleteAccountErasesSocial(t *testing.T) {
	m := newSocialMock()
	router := socialRouter(m)
	joinAs(t, router, "deleting")

	code, _, _ := makeProtoRequest(router, "/user/delete_account", &pb.BasicRequest{}, nil)
	assert.Equal(t, http.StatusOK, code)

	assert.Empty(t, m.profiles)
	assert.Equal(t, handleStatusTombstoned, m.handles["deleting"].status)
	assert.Equal(t, int64(1), m.SoftDeletedUserID)
}
