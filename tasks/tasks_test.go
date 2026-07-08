package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"

	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/errs"
)

// storeStub returns not-found for podcast lookups; the embedded interface
// panics on anything else.
type storeStub struct {
	db.Store
}

func (s *storeStub) GetPodcastByUUID(ctx context.Context, uuid string) (db.Podcast, error) {
	return db.Podcast{}, pgx.ErrNoRows
}

func (s *storeStub) GetPodcastByFeedURL(ctx context.Context, feedURL string) (db.Podcast, error) {
	return db.Podcast{}, pgx.ErrNoRows
}

func TestEnqueueWithoutClientReturnsInternalError(t *testing.T) {
	var qc *QueueClient

	err := qc.Enqueue(context.Background(), asynq.NewTask(TypePodcastRefresh, nil))

	assert.NotNil(t, err)
	assert.True(t, errs.KindIs(err, errs.Internal))
}

func TestCloseWithoutClientIsSafe(t *testing.T) {
	var qc *QueueClient

	assert.Nil(t, qc.Close())
}

func TestHandlePodcastRefreshTaskUnknownPodcast(t *testing.T) {
	payload, err := json.Marshal(PodcastRefreshPayload{
		PodcastUUID: "6a09813e-84ba-4f4c-b70c-620ae7dcbfc9",
		FeedURL:     "https://example.com/feed.xml",
	})
	assert.Nil(t, err)

	// unknown podcasts are skipped without retrying
	worker := &WorkerServer{db: &storeStub{}}
	task := asynq.NewTask(TypePodcastRefresh, payload)

	assert.Nil(t, worker.HandlePodcastRefreshTask(context.Background(), task))
}

func TestHandlePodcastRefreshTaskBadPayload(t *testing.T) {
	worker := &WorkerServer{}
	task := asynq.NewTask(TypePodcastRefresh, []byte("not json"))

	err := worker.HandlePodcastRefreshTask(context.Background(), task)

	assert.NotNil(t, err)
	assert.True(t, errs.KindIs(err, errs.Internal))
	assert.True(t, errors.Is(err, asynq.SkipRetry))
	assert.Equal(t, []string{"tasks/WorkerServer.HandlePodcastRefreshTask"}, errs.OpStack(err))
}

func TestHandleOpmlImportTaskBadPayload(t *testing.T) {
	worker := &WorkerServer{}
	task := asynq.NewTask(TypeOpmlImport, []byte("not json"))

	err := worker.HandleOpmlImportTask(context.Background(), task)

	assert.NotNil(t, err)
	assert.True(t, errs.KindIs(err, errs.Internal))
	assert.True(t, errors.Is(err, asynq.SkipRetry))
}
