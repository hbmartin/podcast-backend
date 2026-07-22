package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"

	"github.com/hbmartin/podcast-backend/crawler"
	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/errs"
)

// storeStub returns not-found for podcast lookups; the embedded interface
// panics on anything else.
type storeStub struct {
	db.Store
}

type opmlStoreStub struct {
	db.Store
	mu   sync.Mutex
	seen []string
}

func (s *opmlStoreStub) GetPodcastByFeedURL(ctx context.Context, feedURL string) (db.Podcast, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, feedURL)
	return db.Podcast{Uuid: crawler.PodcastUUID(feedURL), FeedUrl: feedURL, RefreshStatus: "ok"}, nil
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

func TestHandleOpmlImportTaskSuccess(t *testing.T) {
	store := &opmlStoreStub{}
	worker := &WorkerServer{db: store, crawler: &crawler.Crawler{DB: store}}
	payload, err := json.Marshal(OpmlImportPayload{FeedURLs: []string{
		"https://example.com/one.xml", "https://example.com/two.xml",
	}})
	assert.NoError(t, err)

	err = worker.HandleOpmlImportTask(context.Background(), asynq.NewTask(TypeOpmlImport, payload))

	assert.NoError(t, err)
	assert.Equal(t, []string{"https://example.com/one.xml", "https://example.com/two.xml"}, store.seen)
}

func TestCrawlPodcastsBoundedDeduplicatesAndCapsConcurrency(t *testing.T) {
	const workerCount = 4
	var active atomic.Int32
	var peak atomic.Int32
	var calls atomic.Int32
	podcasts := make([]db.Podcast, 0, 41)
	for i := range 40 {
		podcasts = append(podcasts, db.Podcast{Uuid: fmt.Sprintf("podcast-%d", i)})
	}
	podcasts = append(podcasts, podcasts[0])

	err := crawlPodcastsBounded(context.Background(), podcasts, workerCount, func(db.Podcast) {
		calls.Add(1)
		current := active.Add(1)
		for {
			old := peak.Load()
			if current <= old || peak.CompareAndSwap(old, current) {
				break
			}
		}
		time.Sleep(time.Millisecond)
		active.Add(-1)
	})

	assert.NoError(t, err)
	assert.Equal(t, int32(40), calls.Load())
	assert.LessOrEqual(t, peak.Load(), int32(workerCount))
	assert.Greater(t, peak.Load(), int32(1))
}
