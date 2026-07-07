package tasks

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"

	"goapi-template/errs"
)

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

func TestHandlePodcastRefreshTask(t *testing.T) {
	payload, err := json.Marshal(PodcastRefreshPayload{
		PodcastUUID: "6a09813e-84ba-4f4c-b70c-620ae7dcbfc9",
		FeedURL:     "https://example.com/feed.xml",
	})
	assert.Nil(t, err)

	worker := &WorkerServer{}
	task := asynq.NewTask(TypePodcastRefresh, payload)

	assert.Nil(t, worker.HandlePodcastRefreshTask(context.Background(), task))
}

func TestHandlePodcastRefreshTaskBadPayload(t *testing.T) {
	worker := &WorkerServer{}
	task := asynq.NewTask(TypePodcastRefresh, []byte("not json"))

	err := worker.HandlePodcastRefreshTask(context.Background(), task)

	assert.NotNil(t, err)
	assert.True(t, errs.KindIs(err, errs.Internal))
	assert.Equal(t, []string{"tasks/WorkerServer.HandlePodcastRefreshTask"}, errs.OpStack(err))
}

func TestHandleOpmlImportTask(t *testing.T) {
	payload, err := json.Marshal(OpmlImportPayload{
		UserUUID: "9b1fdc19-e2e6-4b34-a3f1-72b0d5b83b40",
		FeedURLs: []string{"https://example.com/a.xml", "https://example.com/b.xml"},
	})
	assert.Nil(t, err)

	worker := &WorkerServer{}
	task := asynq.NewTask(TypeOpmlImport, payload)

	assert.Nil(t, worker.HandleOpmlImportTask(context.Background(), task))
}

func TestHandleOpmlImportTaskBadPayload(t *testing.T) {
	worker := &WorkerServer{}
	task := asynq.NewTask(TypeOpmlImport, []byte("not json"))

	err := worker.HandleOpmlImportTask(context.Background(), task)

	assert.NotNil(t, err)
	assert.True(t, errs.KindIs(err, errs.Internal))
}
