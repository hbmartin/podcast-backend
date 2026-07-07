package tasks

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/hibiken/asynq"

	"goapi-template/config"
	"goapi-template/db"
	"goapi-template/errs"
)

// WorkerServer is the daemon that consumes tasks from the Redis queue and
// executes them.
type WorkerServer struct {
	srv *asynq.Server
	db  db.Querier
}

func NewWorkerServer(cfg *config.Configuration, querier db.Querier) *WorkerServer {
	srv := asynq.NewServer(
		asynq.RedisClientOpt{
			Addr:     cfg.QueueConfig.RedisAddress,
			Password: cfg.QueueConfig.RedisPassword,
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
	return &WorkerServer{srv: srv, db: querier}
}

// Start registers the task handlers and runs the worker until Shutdown is
// called. It blocks, so it is typically run in a goroutine.
func (w *WorkerServer) Start() error {
	mux := asynq.NewServeMux()

	mux.HandleFunc(TypePodcastRefresh, w.HandlePodcastRefreshTask)
	mux.HandleFunc(TypeOpmlImport, w.HandleOpmlImportTask)

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
		return errs.E(op, errs.Internal, err)
	}

	slog.Info("Executing Podcast Refresh", "uuid", payload.PodcastUUID, "feed_url", payload.FeedURL)

	// Example DB updates:
	// _, err := w.db.UpdatePodcastRefreshTime(ctx, payload.PodcastUUID)

	return nil
}

// HandleOpmlImportTask imports a batch of podcast feeds for a user.
func (w *WorkerServer) HandleOpmlImportTask(ctx context.Context, t *asynq.Task) error {
	const op errs.Op = "tasks/WorkerServer.HandleOpmlImportTask"

	var payload OpmlImportPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return errs.E(op, errs.Internal, err)
	}

	slog.Info("Executing OPML Import batch", "user_uuid", payload.UserUUID, "count", len(payload.FeedURLs))
	return nil
}

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
