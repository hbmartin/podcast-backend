package handlers

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/hbmartin/podcast-backend/db"
	pb "github.com/hbmartin/podcast-backend/protos/api"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
)

// Reviews + reactions state for socialMock (same package; the identity state
// lives in social_test.go).

type reviewKey struct {
	userID      int64
	podcastUuid string
}

func (m *socialMock) ensureReviewState() {
	if m.reviews == nil {
		m.reviews = map[reviewKey]db.PodcastReview{}
		m.ratings = map[reviewKey]int16{}
		m.playedCounts = map[reviewKey]int64{}
		m.reactions = map[reviewKey]int16{}
	}
}

func (m *socialMock) UpsertPodcastReview(ctx context.Context, arg db.UpsertPodcastReviewParams) (db.PodcastReview, error) {
	m.ensureReviewState()
	row := db.PodcastReview{UserID: arg.UserID, PodcastUuid: arg.PodcastUuid, Text: arg.Text}
	m.reviews[reviewKey{arg.UserID, arg.PodcastUuid}] = row
	return row, nil
}

func (m *socialMock) DeletePodcastReview(ctx context.Context, arg db.DeletePodcastReviewParams) (int64, error) {
	m.ensureReviewState()
	key := reviewKey{arg.UserID, arg.PodcastUuid}
	if _, ok := m.reviews[key]; ok {
		delete(m.reviews, key)
		return 1, nil
	}
	return 0, nil
}

func (m *socialMock) DeleteReviewsForUser(ctx context.Context, userID int64) error {
	m.ensureReviewState()
	for key := range m.reviews {
		if key.userID == userID {
			delete(m.reviews, key)
		}
	}
	return nil
}

func (m *socialMock) reviewRow(key reviewKey) (db.GetPodcastReviewsRow, bool) {
	review, ok := m.reviews[key]
	if !ok {
		return db.GetPodcastReviewsRow{}, false
	}
	profile := m.profiles[key.userID]
	return db.GetPodcastReviewsRow{
		UserID:      key.userID,
		PodcastUuid: key.podcastUuid,
		Text:        review.Text,
		AuthorUuid:  m.usersByID[key.userID].Uuid,
		Handle:      profile.Handle,
		DisplayName: profile.DisplayName,
		Rating:      m.ratings[key],
	}, true
}

func (m *socialMock) GetPodcastReviews(ctx context.Context, arg db.GetPodcastReviewsParams) ([]db.GetPodcastReviewsRow, error) {
	m.ensureReviewState()
	var rows []db.GetPodcastReviewsRow
	for key := range m.reviews {
		if key.podcastUuid == arg.PodcastUuid {
			if row, ok := m.reviewRow(key); ok {
				rows = append(rows, row)
			}
		}
	}
	return rows, nil
}

func (m *socialMock) CountPodcastReviews(ctx context.Context, podcastUuid string) (int64, error) {
	m.ensureReviewState()
	var n int64
	for key := range m.reviews {
		if key.podcastUuid == podcastUuid {
			n++
		}
	}
	return n, nil
}

func (m *socialMock) GetOwnPodcastReview(ctx context.Context, arg db.GetOwnPodcastReviewParams) (db.GetOwnPodcastReviewRow, error) {
	m.ensureReviewState()
	if row, ok := m.reviewRow(reviewKey{arg.UserID, arg.PodcastUuid}); ok {
		return db.GetOwnPodcastReviewRow(row), nil
	}
	return db.GetOwnPodcastReviewRow{}, pgx.ErrNoRows
}

func (m *socialMock) GetPodcastRating(ctx context.Context, arg db.GetPodcastRatingParams) (db.PodcastRating, error) {
	m.ensureReviewState()
	if rating, ok := m.ratings[reviewKey{arg.UserID, arg.PodcastUuid}]; ok {
		return db.PodcastRating{UserID: arg.UserID, PodcastUuid: arg.PodcastUuid, Rating: rating}, nil
	}
	return db.PodcastRating{}, pgx.ErrNoRows
}

func (m *socialMock) CountPlayedEpisodesOfPodcast(ctx context.Context, arg db.CountPlayedEpisodesOfPodcastParams) (int64, error) {
	m.ensureReviewState()
	return m.playedCounts[reviewKey{arg.UserID, arg.PodcastUuid}], nil
}

func (m *socialMock) UpsertEpisodeReaction(ctx context.Context, arg db.UpsertEpisodeReactionParams) error {
	m.ensureReviewState()
	m.reactions[reviewKey{arg.UserID, arg.EpisodeUuid}] = arg.Kind
	return nil
}

func (m *socialMock) DeleteEpisodeReaction(ctx context.Context, arg db.DeleteEpisodeReactionParams) (int64, error) {
	m.ensureReviewState()
	key := reviewKey{arg.UserID, arg.EpisodeUuid}
	if _, ok := m.reactions[key]; ok {
		delete(m.reactions, key)
		return 1, nil
	}
	return 0, nil
}

func (m *socialMock) GetEpisodeReactionCounts(ctx context.Context, episodeUuid string) ([]db.GetEpisodeReactionCountsRow, error) {
	m.ensureReviewState()
	counts := map[int16]int64{}
	for key, kind := range m.reactions {
		if key.podcastUuid == episodeUuid {
			counts[kind]++
		}
	}
	var rows []db.GetEpisodeReactionCountsRow
	for kind, count := range counts {
		rows = append(rows, db.GetEpisodeReactionCountsRow{Kind: kind, Count: count})
	}
	return rows, nil
}

func (m *socialMock) GetOwnEpisodeReaction(ctx context.Context, arg db.GetOwnEpisodeReactionParams) (int16, error) {
	m.ensureReviewState()
	if kind, ok := m.reactions[reviewKey{arg.UserID, arg.EpisodeUuid}]; ok {
		return kind, nil
	}
	return 0, pgx.ErrNoRows
}

const reviewedPodcastUUID = "bbbbbbbb-0000-0000-0000-000000000001"

func reviewsRouter(m *socialMock) *http.ServeMux {
	router := socialRouter(m)
	h := Handlers{Queries: m, Config: testAuthConfig}
	router.Handle("POST /social/review/submit", mockAuthMiddleware(http.HandlerFunc(h.PostReviewSubmit)))
	router.Handle("POST /social/review/delete", mockAuthMiddleware(http.HandlerFunc(h.PostReviewDelete)))
	router.Handle("POST /podcast/reviews", mockAuthMiddleware(http.HandlerFunc(h.PostPodcastReviews)))
	router.HandleFunc("POST /anon/podcast/reviews", h.PostPodcastReviews)
	router.Handle("POST /social/reaction/set", mockAuthMiddleware(http.HandlerFunc(h.PostReactionSet)))
	router.Handle("POST /episode/reactions", mockAuthMiddleware(http.HandlerFunc(h.PostEpisodeReactions)))
	return router
}

// joinedReviewsMock: user 1 joined + past the listen gate.
func joinedReviewsMock(t *testing.T, router func(*socialMock) *http.ServeMux) (*socialMock, *http.ServeMux) {
	t.Helper()
	m := newSocialMock()
	m.ensureReviewState()
	m.playedCounts[reviewKey{1, reviewedPodcastUUID}] = 5
	r := router(m)
	joinAs(t, r, "reviewer")
	return m, r
}

func TestReviewSubmitRequiresJoin(t *testing.T) {
	m := newSocialMock()
	m.ensureReviewState()
	m.playedCounts[reviewKey{1, reviewedPodcastUUID}] = 5
	router := reviewsRouter(m)

	code, _, _ := makeProtoRequest(router, "/social/review/submit",
		&pb.PodcastReviewSubmitRequest{PodcastUuid: reviewedPodcastUUID, Text: "great show"}, nil)
	assert.Equal(t, http.StatusForbidden, code)
}

func TestReviewSubmitListenGate(t *testing.T) {
	m := newSocialMock()
	m.ensureReviewState()
	router := reviewsRouter(m)
	joinAs(t, router, "eager_reviewer")

	// Only 1 played episode: below the gate.
	m.playedCounts[reviewKey{1, reviewedPodcastUUID}] = 1
	code, _, _ := makeProtoRequest(router, "/social/review/submit",
		&pb.PodcastReviewSubmitRequest{PodcastUuid: reviewedPodcastUUID, Text: "too soon"}, nil)
	assert.Equal(t, http.StatusForbidden, code)
}

func TestReviewSubmitValidation(t *testing.T) {
	_, router := joinedReviewsMock(t, reviewsRouter)

	// Empty text.
	code, _, _ := makeProtoRequest(router, "/social/review/submit",
		&pb.PodcastReviewSubmitRequest{PodcastUuid: reviewedPodcastUUID, Text: "   "}, nil)
	assert.Equal(t, http.StatusBadRequest, code)

	// Control characters.
	code, _, _ = makeProtoRequest(router, "/social/review/submit",
		&pb.PodcastReviewSubmitRequest{PodcastUuid: reviewedPodcastUUID, Text: "bad\x00text"}, nil)
	assert.Equal(t, http.StatusBadRequest, code)

	// Too long.
	code, _, _ = makeProtoRequest(router, "/social/review/submit",
		&pb.PodcastReviewSubmitRequest{PodcastUuid: reviewedPodcastUUID, Text: strings.Repeat("x", maxReviewTextLen+1)}, nil)
	assert.Equal(t, http.StatusBadRequest, code)
}

func TestReviewSubmitListAndDelete(t *testing.T) {
	m, router := joinedReviewsMock(t, reviewsRouter)
	m.ratings[reviewKey{1, reviewedPodcastUUID}] = 4

	// Submit echoes the attributed review.
	review := &pb.PodcastReview{}
	code, _, err := makeProtoRequest(router, "/social/review/submit",
		&pb.PodcastReviewSubmitRequest{PodcastUuid: reviewedPodcastUUID, Text: "a considered opinion"}, review)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "reviewer", review.Handle)
	assert.Equal(t, uint32(4), review.Rating)
	assert.Equal(t, "a considered opinion", review.Text)

	// Listed publicly with attribution + your_review for the author.
	list := &pb.PodcastReviewsResponse{}
	code, _, _ = makeProtoRequest(router, "/podcast/reviews",
		&pb.PodcastReviewsRequest{PodcastUuid: reviewedPodcastUUID}, list)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, int64(1), list.Total)
	assert.Len(t, list.Reviews, 1)
	assert.Equal(t, "reviewer", list.Reviews[0].Handle)
	assert.NotNil(t, list.YourReview)

	// Anonymous list works too.
	list = &pb.PodcastReviewsResponse{}
	code, _, _ = makeProtoRequest(router, "/anon/podcast/reviews",
		&pb.PodcastReviewsRequest{PodcastUuid: reviewedPodcastUUID}, list)
	assert.Equal(t, http.StatusOK, code)
	assert.Len(t, list.Reviews, 1)
	assert.Nil(t, list.YourReview)

	// Delete: ack + gone.
	ack := &pb.SocialAck{}
	code, _, _ = makeProtoRequest(router, "/social/review/delete",
		&pb.PodcastReviewDeleteRequest{PodcastUuid: reviewedPodcastUUID}, ack)
	assert.Equal(t, http.StatusOK, code)
	assert.True(t, ack.Success)
	assert.Empty(t, m.reviews)
}

func TestReviewListFiltersBlockedAuthors(t *testing.T) {
	m, router := joinedReviewsMock(t, reviewsRouter)

	// User 2 (other) also has a review; viewer (user 1) blocks them.
	m.profiles[2] = db.SocialProfile{UserID: 2, Handle: "blocked_author", DisplayName: "Blocked"}
	m.reviews[reviewKey{2, reviewedPodcastUUID}] = db.PodcastReview{UserID: 2, PodcastUuid: reviewedPodcastUUID, Text: "their take"}
	m.rels[[3]int64{1, 2, int64(relationshipBlock)}] = true

	list := &pb.PodcastReviewsResponse{}
	code, _, _ := makeProtoRequest(router, "/podcast/reviews",
		&pb.PodcastReviewsRequest{PodcastUuid: reviewedPodcastUUID}, list)
	assert.Equal(t, http.StatusOK, code)
	assert.Empty(t, list.Reviews, "blocked author's review hidden from the viewer")

	// Anonymous viewers still see it.
	list = &pb.PodcastReviewsResponse{}
	code, _, _ = makeProtoRequest(router, "/anon/podcast/reviews",
		&pb.PodcastReviewsRequest{PodcastUuid: reviewedPodcastUUID}, list)
	assert.Equal(t, http.StatusOK, code)
	assert.Len(t, list.Reviews, 1)
}

func TestEraseDeletesReviews(t *testing.T) {
	m, router := joinedReviewsMock(t, reviewsRouter)

	code, _, _ := makeProtoRequest(router, "/social/review/submit",
		&pb.PodcastReviewSubmitRequest{PodcastUuid: reviewedPodcastUUID, Text: "soon to vanish"}, nil)
	assert.Equal(t, http.StatusOK, code)
	assert.Len(t, m.reviews, 1)

	code, _, _ = makeProtoRequest(router, "/social/erase", &pb.EraseRequest{}, nil)
	assert.Equal(t, http.StatusOK, code)
	assert.Empty(t, m.reviews, "attributed review text dies with the profile")
}

func TestReactions(t *testing.T) {
	m := newSocialMock()
	m.ensureReviewState()
	router := reviewsRouter(m)
	episode := "cccccccc-0000-0000-0000-000000000001"

	// Set (no join required — account-level).
	ack := &pb.SocialAck{}
	code, _, _ := makeProtoRequest(router, "/social/reaction/set",
		&pb.EpisodeReactionSetRequest{EpisodeUuid: episode, Kind: pb.ReactionKind_REACTION_KIND_MIND_BLOWN}, ack)
	assert.Equal(t, http.StatusOK, code)
	assert.True(t, ack.Success)

	counts := &pb.EpisodeReactionsResponse{}
	code, _, _ = makeProtoRequest(router, "/episode/reactions",
		&pb.EpisodeReactionsRequest{EpisodeUuid: episode}, counts)
	assert.Equal(t, http.StatusOK, code)
	assert.Len(t, counts.Counts, 1)
	assert.Equal(t, pb.ReactionKind_REACTION_KIND_MIND_BLOWN, counts.Counts[0].Kind)
	assert.Equal(t, int64(1), counts.Counts[0].Count)
	assert.Equal(t, pb.ReactionKind_REACTION_KIND_MIND_BLOWN, counts.YourReaction)

	// Switch replaces (one per user per episode).
	code, _, _ = makeProtoRequest(router, "/social/reaction/set",
		&pb.EpisodeReactionSetRequest{EpisodeUuid: episode, Kind: pb.ReactionKind_REACTION_KIND_FIRE}, nil)
	assert.Equal(t, http.StatusOK, code)
	counts = &pb.EpisodeReactionsResponse{}
	_, _, _ = makeProtoRequest(router, "/episode/reactions",
		&pb.EpisodeReactionsRequest{EpisodeUuid: episode}, counts)
	assert.Len(t, counts.Counts, 1)
	assert.Equal(t, pb.ReactionKind_REACTION_KIND_FIRE, counts.Counts[0].Kind)

	// Clear via UNSPECIFIED.
	code, _, _ = makeProtoRequest(router, "/social/reaction/set",
		&pb.EpisodeReactionSetRequest{EpisodeUuid: episode, Kind: pb.ReactionKind_REACTION_KIND_UNSPECIFIED}, nil)
	assert.Equal(t, http.StatusOK, code)
	counts = &pb.EpisodeReactionsResponse{}
	_, _, _ = makeProtoRequest(router, "/episode/reactions",
		&pb.EpisodeReactionsRequest{EpisodeUuid: episode}, counts)
	assert.Empty(t, counts.Counts)
	assert.Equal(t, pb.ReactionKind_REACTION_KIND_UNSPECIFIED, counts.YourReaction)

	// Unknown kind rejected.
	code, _, _ = makeProtoRequest(router, "/social/reaction/set",
		&pb.EpisodeReactionSetRequest{EpisodeUuid: episode, Kind: pb.ReactionKind(99)}, nil)
	assert.Equal(t, http.StatusBadRequest, code)
}
