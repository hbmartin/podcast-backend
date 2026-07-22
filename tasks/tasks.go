// Package tasks provides a Redis-backed background job queue built on
// github.com/hibiken/asynq. HTTP handlers enqueue tasks through the
// QueueClient; the WorkerServer consumes and executes them.
package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hibiken/asynq"

	"github.com/hbmartin/podcast-backend/errs"
)

// Task types. Each type is routed to a matching handler registered on the
// WorkerServer mux.
const (
	TypePodcastRefresh     = "podcast:refresh"
	TypeOpmlImport         = "opml:import"
	TypeRefreshDuePodcasts = "podcast:refresh_due"
	TypeNotifyNewEpisodes  = "push:new_episodes"
	TypeSocialPush         = "push:social"
	TypeSightingFetch      = "transcript:sighting_fetch"
	TypeGroupPostFanout    = "push:group_post_fanout"
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
	FeedURLs []string `json:"feed_urls"`
}

// NotifyNewEpisodesPayload carries newly published episodes to push out
// (newest first).
type NotifyNewEpisodesPayload struct {
	PodcastUUID  string   `json:"podcast_uuid"`
	EpisodeUUIDs []string `json:"episode_uuids"`
}

// SocialPushPayload carries one social event to push (Slice 8).
type SocialPushPayload struct {
	TargetUserID     int64             `json:"target_user_id"`
	PushType         int               `json:"push_type"`
	ActorHandle      string            `json:"actor_handle"`
	ActorDisplayName string            `json:"actor_display_name"`
	Data             map[string]string `json:"data,omitempty"`
}

// SightingFetchPayload identifies a transcript sighting whose publisher URL the
// server should fetch and store.
type SightingFetchPayload struct {
	SightingID int64 `json:"sighting_id"`
}

// GroupPostFanoutPayload moves potentially large public-hub notification
// fan-out off the request path.
type GroupPostFanoutPayload struct {
	GroupID          int64  `json:"group_id"`
	PostID           int64  `json:"post_id"`
	ActorUserID      int64  `json:"actor_user_id"`
	ActorHandle      string `json:"actor_handle"`
	ActorDisplayName string `json:"actor_display_name"`
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
func (qc *QueueClient) EnqueueOpmlImport(ctx context.Context, feedURLs []string) error {
	const op errs.Op = "tasks/QueueClient.EnqueueOpmlImport"

	payload, err := json.Marshal(OpmlImportPayload{FeedURLs: feedURLs})
	if err != nil {
		return errs.E(op, errs.Internal, err)
	}

	task := asynq.NewTask(TypeOpmlImport, payload)
	if err := qc.Enqueue(ctx, task, asynq.Queue(QueueLow)); err != nil {
		return errs.E(op, err)
	}
	return nil
}

// EnqueueSocialPush queues delivery of one social push event (Slice 8).
func (qc *QueueClient) EnqueueSocialPush(ctx context.Context, payload SocialPushPayload) error {
	const op errs.Op = "tasks/QueueClient.EnqueueSocialPush"

	raw, err := json.Marshal(payload)
	if err != nil {
		return errs.E(op, errs.Internal, err)
	}
	if err := qc.Enqueue(ctx, asynq.NewTask(TypeSocialPush, raw)); err != nil {
		return errs.E(op, err)
	}
	return nil
}

// EnqueueNotifyNewEpisodes queues push delivery for a podcast's newly
// published episodes.
func (qc *QueueClient) EnqueueNotifyNewEpisodes(ctx context.Context, podcastUUID string, episodeUUIDs []string) error {
	const op errs.Op = "tasks/QueueClient.EnqueueNotifyNewEpisodes"

	payload, err := json.Marshal(NotifyNewEpisodesPayload{PodcastUUID: podcastUUID, EpisodeUUIDs: episodeUUIDs})
	if err != nil {
		return errs.E(op, errs.Internal, err)
	}

	task := asynq.NewTask(TypeNotifyNewEpisodes, payload)
	if err := qc.Enqueue(ctx, task); err != nil {
		return errs.E(op, err)
	}
	return nil
}

// EnqueueRefreshDuePodcasts queues one sweep of catalog podcasts whose
// next_refresh_at has passed. asynq.TaskID de-duplicates overlapping sweeps.
func (qc *QueueClient) EnqueueRefreshDuePodcasts(ctx context.Context) error {
	const op errs.Op = "tasks/QueueClient.EnqueueRefreshDuePodcasts"

	task := asynq.NewTask(TypeRefreshDuePodcasts, nil)
	if err := qc.Enqueue(ctx, task, asynq.Queue(QueueLow), asynq.TaskID(TypeRefreshDuePodcasts)); err != nil {
		// a sweep already waiting in the queue is not an error
		if errors.Is(err, asynq.ErrTaskIDConflict) {
			return nil
		}
		return errs.E(op, err)
	}
	return nil
}

// EnqueueSightingFetch queues a background fetch of a reported publisher
// transcript (docs/TranscriptContributions.md §3).
func (qc *QueueClient) EnqueueSightingFetch(ctx context.Context, sightingID int64) error {
	const op errs.Op = "tasks/QueueClient.EnqueueSightingFetch"

	payload, err := json.Marshal(SightingFetchPayload{SightingID: sightingID})
	if err != nil {
		return errs.E(op, errs.Internal, err)
	}

	task := asynq.NewTask(TypeSightingFetch, payload)
	if err := qc.Enqueue(ctx, task, asynq.Queue(QueueLow)); err != nil {
		return errs.E(op, err)
	}
	return nil
}

func (qc *QueueClient) EnqueueGroupPostFanout(ctx context.Context, payload GroupPostFanoutPayload) error {
	const op errs.Op = "tasks/QueueClient.EnqueueGroupPostFanout"

	raw, err := json.Marshal(payload)
	if err != nil {
		return errs.E(op, errs.Internal, err)
	}
	taskID := fmt.Sprintf("%s:%d", TypeGroupPostFanout, payload.PostID)
	if err := qc.Enqueue(ctx, asynq.NewTask(TypeGroupPostFanout, raw),
		asynq.Queue(QueueLow), asynq.TaskID(taskID)); err != nil {
		if errors.Is(err, asynq.ErrTaskIDConflict) {
			return nil
		}
		return errs.E(op, err)
	}
	return nil
}
