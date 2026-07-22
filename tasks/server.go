package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"

	"github.com/hbmartin/podcast-backend/config"
	"github.com/hbmartin/podcast-backend/crawler"
	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/errs"
	"github.com/hbmartin/podcast-backend/push"
	"github.com/hbmartin/podcast-backend/transcripts"
)

// WorkerServer is the daemon that consumes tasks from the Redis queue and
// executes them.
type WorkerServer struct {
	srv      *asynq.Server
	db       db.Store
	crawler  *crawler.Crawler
	notifier *push.Notifier
}

func NewWorkerServer(cfg *config.Configuration, store db.Store, feedCrawler *crawler.Crawler, notifier *push.Notifier) *WorkerServer {
	srv := asynq.NewServer(
		asynq.RedisClientOpt{
			Addr:     cfg.QueueConfig.RedisAddress,
			Password: cfg.QueueConfig.RedisPassword,
			DB:       cfg.QueueConfig.RedisDb,
		},
		asynq.Config{
			Concurrency: cfg.QueueConfig.Concurrency,
			Queues: map[string]int{
				QueueCritical: 6,
				QueueDefault:  3,
				QueueLow:      1,
			},
			StrictPriority: cfg.QueueConfig.StrictPriority,
			Logger:         &slogAdapter{logger: slog.Default()},
		},
	)
	return &WorkerServer{srv: srv, db: store, crawler: feedCrawler, notifier: notifier}
}

// Start registers the task handlers and runs the worker until Shutdown is
// called. It blocks, so it is typically run in a goroutine.
func (w *WorkerServer) Start() error {
	mux := asynq.NewServeMux()

	mux.HandleFunc(TypePodcastRefresh, w.HandlePodcastRefreshTask)
	mux.HandleFunc(TypeOpmlImport, w.HandleOpmlImportTask)
	mux.HandleFunc(TypeRefreshDuePodcasts, w.HandleRefreshDuePodcastsTask)
	mux.HandleFunc(TypeNotifyNewEpisodes, w.HandleNotifyNewEpisodesTask)
	mux.HandleFunc(TypeSocialPush, w.HandleSocialPushTask)
	mux.HandleFunc(TypeSightingFetch, w.HandleSightingFetchTask)
	mux.HandleFunc(TypeGroupPostFanout, w.HandleGroupPostFanoutTask)

	return w.srv.Run(mux)
}

// Shutdown gracefully stops the worker, waiting for in-flight tasks to finish.
func (w *WorkerServer) Shutdown() {
	w.srv.Shutdown()
}

// HandlePodcastRefreshTask fetches and refreshes a single podcast feed.
func (w *WorkerServer) HandlePodcastRefreshTask(ctx context.Context, t *asynq.Task) error {
	const op errs.Op = "tasks/WorkerServer.HandlePodcastRefreshTask"

	var payload PodcastRefreshPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return errs.E(op, errs.Internal, fmt.Errorf("%w: %v", asynq.SkipRetry, err))
	}

	slog.Info("Executing Podcast Refresh", "uuid", payload.PodcastUUID, "feed_url", payload.FeedURL)

	podcast, err := w.podcastFromPayload(ctx, payload)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("Podcast refresh skipped: unknown podcast", "uuid", payload.PodcastUUID)
			return nil
		}
		return errs.E(op, err)
	}

	if err := w.crawler.Crawl(ctx, podcast); err != nil {
		// crawl failures are recorded on the podcast row; retrying the task
		// immediately would just hammer a broken feed
		slog.Warn("Podcast crawl failed", "uuid", podcast.Uuid, "error", err)
	}
	return nil
}

func (w *WorkerServer) podcastFromPayload(ctx context.Context, payload PodcastRefreshPayload) (db.Podcast, error) {
	if payload.PodcastUUID != "" {
		return w.db.GetPodcastByUUID(ctx, payload.PodcastUUID)
	}
	return w.db.GetPodcastByFeedURL(ctx, crawler.CanonicalFeedURL(payload.FeedURL))
}

// HandleOpmlImportTask imports a batch of podcast feeds for a user.
func (w *WorkerServer) HandleOpmlImportTask(ctx context.Context, t *asynq.Task) error {
	const op errs.Op = "tasks/WorkerServer.HandleOpmlImportTask"

	var payload OpmlImportPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return errs.E(op, errs.Internal, fmt.Errorf("%w: %v", asynq.SkipRetry, err))
	}

	slog.Info("Executing OPML Import batch", "count", len(payload.FeedURLs))

	for _, feedURL := range payload.FeedURLs {
		if _, err := w.crawler.EnsurePodcast(ctx, feedURL); err != nil {
			// the failure is recorded on the podcast row; poll responses
			// report it to the client
			slog.Warn("OPML feed import failed", "feed_url", feedURL, "error", err)
		}
	}
	return nil
}

// HandleRefreshDuePodcastsTask re-crawls every catalog podcast whose
// next_refresh_at has passed. Enqueued periodically from main.
func (w *WorkerServer) HandleRefreshDuePodcastsTask(ctx context.Context, t *asynq.Task) error {
	const op errs.Op = "tasks/WorkerServer.HandleRefreshDuePodcastsTask"

	due, err := w.db.GetDuePodcasts(ctx, refreshBatchSize)
	if err != nil {
		return errs.E(op, errs.Database, err)
	}

	slog.Info("Refreshing due podcasts", "count", len(due))
	return crawlPodcastsBounded(ctx, due, scheduledCrawlWorkers, func(podcast db.Podcast) {
		if err := w.crawler.Crawl(ctx, podcast); err != nil {
			slog.Warn("Scheduled crawl failed", "uuid", podcast.Uuid, "error", err)
		}
	})
}

// crawlPodcastsBounded runs one de-duplicated crawl per podcast through a
// fixed worker pool. The database batch cap bounds memory while this cap
// bounds simultaneous outbound feed requests.
func crawlPodcastsBounded(ctx context.Context, podcasts []db.Podcast, workers int, crawl func(db.Podcast)) error {
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan db.Podcast)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case podcast, ok := <-jobs:
					if !ok {
						return
					}
					crawl(podcast)
				}
			}
		}()
	}

	seen := make(map[string]struct{}, len(podcasts))
	for _, podcast := range podcasts {
		if _, exists := seen[podcast.Uuid]; exists {
			continue
		}
		seen[podcast.Uuid] = struct{}{}
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return ctx.Err()
		case jobs <- podcast:
		}
	}
	close(jobs)
	wg.Wait()
	return ctx.Err()
}

// HandleNotifyNewEpisodesTask pushes new-episode alerts to registered
// devices. A no-op when push is not configured.
func (w *WorkerServer) HandleNotifyNewEpisodesTask(ctx context.Context, t *asynq.Task) error {
	const op errs.Op = "tasks/WorkerServer.HandleNotifyNewEpisodesTask"

	if w.notifier == nil {
		return nil
	}

	var payload NotifyNewEpisodesPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return errs.E(op, errs.Internal, fmt.Errorf("%w: %v", asynq.SkipRetry, err))
	}

	slog.Info("Delivering new-episode notifications", "podcast", payload.PodcastUUID, "episodes", len(payload.EpisodeUUIDs))
	w.notifier.NotifyNewEpisodes(ctx, payload.PodcastUUID, payload.EpisodeUUIDs)
	return nil
}

// HandleSocialPushTask delivers one social push (Slice 8). A no-op when push
// is not configured.
func (w *WorkerServer) HandleSocialPushTask(ctx context.Context, t *asynq.Task) error {
	const op errs.Op = "tasks/WorkerServer.HandleSocialPushTask"

	if w.notifier == nil {
		return nil
	}

	var payload SocialPushPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return errs.E(op, errs.Internal, fmt.Errorf("%w: %v", asynq.SkipRetry, err))
	}

	w.notifier.NotifySocial(ctx, payload.TargetUserID, payload.PushType,
		payload.ActorHandle, payload.ActorDisplayName, payload.Data)
	return nil
}

func (w *WorkerServer) HandleGroupPostFanoutTask(ctx context.Context, t *asynq.Task) error {
	const op errs.Op = "tasks/WorkerServer.HandleGroupPostFanoutTask"
	if w.notifier == nil {
		return nil
	}

	var payload GroupPostFanoutPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return errs.E(op, errs.Internal, fmt.Errorf("%w: %v", asynq.SkipRetry, err))
	}
	if payload.GroupID <= 0 || payload.PostID <= 0 || payload.ActorUserID <= 0 {
		return fmt.Errorf("%w: invalid group post fanout payload", asynq.SkipRetry)
	}
	targets, err := w.db.GetGroupNotifyTargets(ctx, db.GetGroupNotifyTargetsParams{
		GroupID: payload.GroupID, UserID: payload.ActorUserID,
	})
	if err != nil {
		return errs.E(op, errs.Database, err)
	}
	data := map[string]string{
		"group_id": strconv.FormatInt(payload.GroupID, 10),
		"post_id":  strconv.FormatInt(payload.PostID, 10),
	}
	for _, target := range targets {
		w.notifier.NotifySocial(ctx, target, push.SocialPushGroupPost,
			payload.ActorHandle, payload.ActorDisplayName, data)
	}
	return nil
}

const (
	refreshBatchSize      = 200
	scheduledCrawlWorkers = 8
)

// slogAdapter bridges asynq's Logger interface to the application's slog logger.
type slogAdapter struct {
	logger *slog.Logger
}

func (l *slogAdapter) Debug(args ...interface{}) { l.log(slog.LevelDebug, args...) }
func (l *slogAdapter) Info(args ...interface{})  { l.log(slog.LevelInfo, args...) }
func (l *slogAdapter) Warn(args ...interface{})  { l.log(slog.LevelWarn, args...) }
func (l *slogAdapter) Error(args ...interface{}) { l.log(slog.LevelError, args...) }
func (l *slogAdapter) Fatal(args ...interface{}) { l.log(slog.LevelError, args...) }

func (l *slogAdapter) log(level slog.Level, args ...interface{}) {
	if len(args) == 0 {
		return
	}
	msg, ok := args[0].(string)
	if !ok || len(args) > 1 {
		l.logger.Log(context.Background(), level, "asynq", "details", args)
		return
	}
	l.logger.Log(context.Background(), level, msg)
}

// HandleSightingFetchTask fetches and stores a reported publisher transcript.
func (w *WorkerServer) HandleSightingFetchTask(ctx context.Context, t *asynq.Task) error {
	const op errs.Op = "tasks/WorkerServer.HandleSightingFetchTask"

	var payload SightingFetchPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return errs.E(op, errs.Internal, fmt.Errorf("%w: %v", asynq.SkipRetry, err))
	}
	if payload.SightingID <= 0 {
		return fmt.Errorf("%w: invalid sighting id %d", asynq.SkipRetry, payload.SightingID)
	}
	if err := transcripts.FetchAndStore(ctx, w.db, payload.SightingID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: sighting %d not found", asynq.SkipRetry, payload.SightingID)
		}
		return errs.E(op, err)
	}
	return nil
}
