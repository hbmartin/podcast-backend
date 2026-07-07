// Package tasks provides a Redis-backed background job queue built on
// github.com/hibiken/asynq. HTTP handlers enqueue tasks through the
// QueueClient; the WorkerServer consumes and executes them.
package tasks

import (
	"context"
	"encoding/json"

	"github.com/hibiken/asynq"

	"goapi-template/errs"
)

// Task types. Each type is routed to a matching handler registered on the
// WorkerServer mux.
const (
	TypePodcastRefresh = "podcast:refresh"
	TypeOpmlImport     = "opml:import"
)

// Queue names, in priority order.
const (
	QueueCritical = "critical"
	QueueDefault  = "default"
	QueueLow      = "low"
)

// PodcastRefreshPayload carries the data needed to refresh a podcast feed.
type PodcastRefreshPayload struct {
	PodcastUUID string `json:"podcast_uuid"`
	FeedURL     string `json:"feed_url"`
}

// OpmlImportPayload carries the data needed to import an OPML feed list.
type OpmlImportPayload struct {
	UserUUID string   `json:"user_uuid"`
	FeedURLs []string `json:"feed_urls"`
}

// QueueClient enqueues background tasks onto the Redis queue.
type QueueClient struct {
	client *asynq.Client
}

func NewQueueClient(redisAddr string, redisPwd string, redisDb int) *QueueClient {
	return &QueueClient{
		client: asynq.NewClient(asynq.RedisClientOpt{
			Addr:     redisAddr,
			Password: redisPwd,
			DB:       redisDb,
		}),
	}
}

func (qc *QueueClient) Close() error {
	if qc == nil || qc.client == nil {
		return nil
	}
	return qc.client.Close()
}

// Enqueue places a raw task on the queue. Prefer the typed helpers below.
func (qc *QueueClient) Enqueue(ctx context.Context, task *asynq.Task, opts ...asynq.Option) error {
	const op errs.Op = "tasks/QueueClient.Enqueue"

	if qc == nil || qc.client == nil {
		return errs.E(op, errs.Internal, "task queue is not enabled")
	}

	if _, err := qc.client.EnqueueContext(ctx, task, opts...); err != nil {
		return errs.E(op, errs.Internal, err)
	}
	return nil
}

// EnqueuePodcastRefresh queues a background refresh of a single podcast feed.
func (qc *QueueClient) EnqueuePodcastRefresh(ctx context.Context, uuid string, url string) error {
	const op errs.Op = "tasks/QueueClient.EnqueuePodcastRefresh"

	payload, err := json.Marshal(PodcastRefreshPayload{PodcastUUID: uuid, FeedURL: url})
	if err != nil {
		return errs.E(op, errs.Internal, err)
	}

	task := asynq.NewTask(TypePodcastRefresh, payload)
	if err := qc.Enqueue(ctx, task); err != nil {
		return errs.E(op, err)
	}
	return nil
}

// EnqueueOpmlImport queues a background import of a batch of feed URLs.
func (qc *QueueClient) EnqueueOpmlImport(ctx context.Context, userUUID string, feedURLs []string) error {
	const op errs.Op = "tasks/QueueClient.EnqueueOpmlImport"

	payload, err := json.Marshal(OpmlImportPayload{UserUUID: userUUID, FeedURLs: feedURLs})
	if err != nil {
		return errs.E(op, errs.Internal, err)
	}

	task := asynq.NewTask(TypeOpmlImport, payload)
	if err := qc.Enqueue(ctx, task, asynq.Queue(QueueLow)); err != nil {
		return errs.E(op, err)
	}
	return nil
}
