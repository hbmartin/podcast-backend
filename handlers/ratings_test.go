package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hbmartin/podcast-backend/db"
	pb "github.com/hbmartin/podcast-backend/protos/api"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/proto"
)

// ratingsMock is a stateful in-memory rating store on top of QuerierMock.
type ratingsMock struct {
	QuerierMock

	ratings map[string]db.PodcastRating // by podcast uuid (single test user)
}

func newRatingsMock() *ratingsMock {
	m := &ratingsMock{ratings: map[string]db.PodcastRating{}}
	m.GetUserByUUIDResult = db.User{ID: 42, Uuid: testUserUUID, Email: "mail@test.com", CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}
	return m
}

func (m *ratingsMock) UpsertPodcastRating(ctx context.Context, arg db.UpsertPodcastRatingParams) error {
	m.ratings[arg.PodcastUuid] = db.PodcastRating{
		UserID: arg.UserID, PodcastUuid: arg.PodcastUuid, Rating: arg.Rating,
		ModifiedAt: time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC),
	}
	return nil
}

func (m *ratingsMock) GetPodcastRating(ctx context.Context, arg db.GetPodcastRatingParams) (db.PodcastRating, error) {
	if rating, ok := m.ratings[arg.PodcastUuid]; ok {
		return rating, nil
	}
	return db.PodcastRating{}, pgx.ErrNoRows
}

func (m *ratingsMock) GetUserPodcastRatings(ctx context.Context, userID int64) ([]db.PodcastRating, error) {
	var out []db.PodcastRating
	for _, rating := range m.ratings {
		out = append(out, rating)
	}
	return out, nil
}

func (m *ratingsMock) GetPodcastRatingAggregate(ctx context.Context, podcastUuid string) (db.GetPodcastRatingAggregateRow, error) {
	if rating, ok := m.ratings[podcastUuid]; ok {
		return db.GetPodcastRatingAggregateRow{Total: 1, Average: float64(rating.Rating)}, nil
	}
	return db.GetPodcastRatingAggregateRow{}, nil
}

func ratingsRouter(m *ratingsMock) *http.ServeMux {
	handlers := Handlers{Queries: m, Config: testAuthConfig}
	mux := http.NewServeMux()
	mux.Handle("POST /user/podcast_rating/add", mockAuthMiddleware(http.HandlerFunc(handlers.PostPodcastRatingAdd)))
	mux.Handle("POST /user/podcast_rating/show", mockAuthMiddleware(http.HandlerFunc(handlers.PostPodcastRatingShow)))
	mux.Handle("GET /user/podcast_rating/list", mockAuthMiddleware(http.HandlerFunc(handlers.GetPodcastRatingList)))
	mux.HandleFunc("GET /podcast/rating/{uuid}", handlers.GetPodcastRatingPublic)
	return mux
}

func TestRatingAddShowList(t *testing.T) {
	m := newRatingsMock()
	router := ratingsRouter(m)

	// add
	code, _, err := makeProtoRequest(router, "/user/podcast_rating/add",
		&pb.PodcastRatingAddRequest{PodcastUuid: testPodcastUUID, PodcastRating: 4}, nil)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, int16(4), m.ratings[testPodcastUUID].Rating)

	// upsert overwrites
	code, _, _ = makeProtoRequest(router, "/user/podcast_rating/add",
		&pb.PodcastRatingAddRequest{PodcastUuid: testPodcastUUID, PodcastRating: 5}, nil)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, int16(5), m.ratings[testPodcastUUID].Rating)

	// show
	shown := &pb.PodcastRating{}
	code, _, err = makeProtoRequest(router, "/user/podcast_rating/show",
		&pb.PodcastRatingShowRequest{PodcastUuid: testPodcastUUID}, shown)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, uint32(5), shown.PodcastRating)
	assert.Equal(t, testPodcastUUID, shown.PodcastUuid)
	assert.NotNil(t, shown.ModifiedAt)

	// list (GET with protobuf response)
	req, _ := http.NewRequest("GET", "/user/podcast_rating/list", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
	list := &pb.PodcastRatingsResponse{}
	assert.NoError(t, proto.Unmarshal(rr.Body.Bytes(), list))
	assert.Len(t, list.PodcastRatings, 1)
}

func TestRatingValidation(t *testing.T) {
	m := newRatingsMock()
	router := ratingsRouter(m)

	code, _, _ := makeProtoRequest(router, "/user/podcast_rating/add",
		&pb.PodcastRatingAddRequest{PodcastUuid: testPodcastUUID, PodcastRating: 6}, nil)
	assert.Equal(t, http.StatusBadRequest, code)

	code, _, _ = makeProtoRequest(router, "/user/podcast_rating/add",
		&pb.PodcastRatingAddRequest{PodcastUuid: "not-a-uuid", PodcastRating: 3}, nil)
	assert.Equal(t, http.StatusBadRequest, code)
}

func TestRatingShowNotFound(t *testing.T) {
	m := newRatingsMock()
	router := ratingsRouter(m)

	code, _, _ := makeProtoRequest(router, "/user/podcast_rating/show",
		&pb.PodcastRatingShowRequest{PodcastUuid: testPodcastUUID}, nil)
	assert.Equal(t, http.StatusNotFound, code)
}

func TestRatingAggregatePublic(t *testing.T) {
	m := newRatingsMock()
	m.ratings[testPodcastUUID] = db.PodcastRating{Rating: 5}
	router := ratingsRouter(m)

	code, resp, _, err := makeRequest[struct {
		Total   int64   `json:"total"`
		Average float64 `json:"average"`
	}](router, "GET", "/podcast/rating/"+testPodcastUUID, nil)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, int64(1), resp.Total)
	assert.Equal(t, float64(5), resp.Average)

	// unrated podcast: 200 with zeros
	code, resp, _, err = makeRequest[struct {
		Total   int64   `json:"total"`
		Average float64 `json:"average"`
	}](router, "GET", "/podcast/rating/99999999-9999-4999-8999-999999999999", nil)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, int64(0), resp.Total)
	assert.Equal(t, float64(0), resp.Average)
}
