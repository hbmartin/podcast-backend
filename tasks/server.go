package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"

	"goapi-template/config"
	"goapi-template/crawler"
	"goapi-template/db"
	"goapi-template/errs"
)

// WorkerServer is the daemon that consumes tasks from the Redis queue and
// executes them.
type WorkerServer struct {
	srv     *asynq.Server
	db      db.Store
	crawler *crawler.Crawler
}

func NewWorkerServer(cfg *config.Configuration, store db.Store, feedCrawler *crawler.Crawler) *WorkerServer {
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
	return &WorkerServer{srv: srv, db: store, crawler: feedCrawler}
}

// Start registers the task handlers and runs the worker until Shutdown is
// called. It blocks, so it is typically run in a goroutine.
func (w *WorkerServer) Start() error {
	mux := asynq.NewServeMux()

	mux.HandleFunc(TypePodcastRefresh, w.HandlePodcastRefreshTask)
	mux.HandleFunc(TypeOpmlImport, w.HandleOpmlImportTask)
	mux.HandleFunc(TypeRefreshDuePodcasts, w.HandleRefreshDuePodcastsTask)

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
	for _, podcast := range due {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := w.crawler.Crawl(ctx, podcast); err != nil {
			slog.Warn("Scheduled crawl failed", "uuid", podcast.Uuid, "error", err)
		}
	}
	return nil
}

const refreshBatchSize = 200

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
