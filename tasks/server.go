package tasks

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"

	"github.com/hbmartin/podcast-backend/config"
	"github.com/hbmartin/podcast-backend/crawler"
	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/errs"
	"github.com/hbmartin/podcast-backend/objectstore"
	"github.com/hbmartin/podcast-backend/push"
	"github.com/hbmartin/podcast-backend/transcripts"
	"golang.org/x/text/language"
)

// WorkerServer is the daemon that consumes tasks from the Redis queue and
// executes them.
type WorkerServer struct {
	srv      *asynq.Server
	db       db.Store
	crawler  *crawler.Crawler
	notifier *push.Notifier
	redis    *redis.Client
	objects  objectstore.Store
}

func NewWorkerServer(cfg *config.Configuration, store db.Store, feedCrawler *crawler.Crawler, notifier *push.Notifier) (*WorkerServer, error) {
	redisOpt, err := asynq.ParseRedisURI(cfg.QueueConfig.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("parse queue Redis URL: %w", err)
	}
	srv := asynq.NewServer(
		redisOpt,
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
	goRedisOpt, err := redis.ParseURL(cfg.QueueConfig.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("parse queue Redis URL: %w", err)
	}
	worker := &WorkerServer{srv: srv, db: store, crawler: feedCrawler, notifier: notifier, redis: redis.NewClient(goRedisOpt)}
	if cfg.ObjectStorageConfig.Enabled {
		worker.objects, err = objectstore.NewS3Store(context.Background(), cfg.ObjectStorageConfig.EndpointURL,
			cfg.ObjectStorageConfig.Bucket, cfg.ObjectStorageConfig.AccessKeyID,
			cfg.ObjectStorageConfig.SecretAccessKey, cfg.ObjectStorageConfig.Region)
		if err != nil {
			return nil, err
		}
	}
	return worker, nil
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
	mux.HandleFunc(TypeObjectCleanup, w.HandleObjectCleanupTask)
	mux.HandleFunc(TypeCorpusLegacyImport, w.HandleCorpusLegacyImportTask)

	return w.srv.Run(mux)
}

func (w *WorkerServer) HandleObjectCleanupTask(ctx context.Context, _ *asynq.Task) error {
	if w.objects == nil {
		return nil
	}
	items, err := w.db.ClaimObjectDeletes(ctx, 100)
	if err != nil {
		return errs.E("tasks/WorkerServer.HandleObjectCleanupTask", errs.Database, err)
	}
	for _, item := range items {
		if err := w.objects.Delete(ctx, item.ObjectKey); err != nil {
			delay := int64(30 * (1 << minInt32(item.Attempts, 8)))
			_ = w.db.RetryObjectDelete(ctx, db.RetryObjectDeleteParams{ID: item.ID, Column2: delay})
			continue
		}
		if err := w.db.CompleteObjectDelete(ctx, item.ID); err != nil {
			return err
		}
	}
	return nil
}

func minInt32(value, maximum int32) int32 {
	if value < maximum {
		return value
	}
	return maximum
}

type legacyCorpusStore interface {
	ListLegacyCorpusRows(context.Context, int64, int32) ([]db.LegacyCorpusRow, error)
	ImportLegacyCorpusCandidate(context.Context, db.ImportLegacyCorpusCandidateParams) (string, error)
}

func (w *WorkerServer) HandleCorpusLegacyImportTask(ctx context.Context, _ *asynq.Task) error {
	if w.objects == nil {
		return nil
	}
	repository, ok := w.db.(legacyCorpusStore)
	if !ok {
		return nil
	}
	var afterID int64
	for {
		rows, err := repository.ListLegacyCorpusRows(ctx, afterID, 100)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, row := range rows {
			afterID = row.ID
			vtt, err := gunzipLegacy(row.VTTBlob, 2<<20)
			if err != nil {
				return fmt.Errorf("legacy contribution %d transcript: %w", row.ID, err)
			}
			hash := sha256.Sum256(vtt)
			hashString := hex.EncodeToString(hash[:])
			key := "corpus/transcripts/" + hashString + ".vtt"
			if err := w.objects.Put(ctx, key, vtt, "text/vtt"); err != nil {
				return err
			}
			languageValue := "und"
			if value := strings.TrimSpace(row.Language); value != "" {
				if tag, parseErr := language.Parse(value); parseErr == nil {
					languageValue = tag.String()
				}
			}
			artifacts := []db.CorpusArtifactInput{{Kind: "transcript", ObjectKey: key, ContentHash: hashString, MediaType: "text/vtt", Format: "vtt", ByteLength: int64(len(vtt)), Language: languageValue, Source: "legacy"}}
			if len(row.FingerprintBlob) > 0 {
				fingerprint, err := gunzipLegacy(row.FingerprintBlob, 512<<10)
				if err != nil {
					return fmt.Errorf("legacy contribution %d fingerprint: %w", row.ID, err)
				}
				fpHash := sha256.Sum256(fingerprint)
				fpHashString := hex.EncodeToString(fpHash[:])
				fpKey := "corpus/fingerprints/" + fpHashString + ".json"
				if err := w.objects.Put(ctx, fpKey, fingerprint, "application/json"); err != nil {
					return err
				}
				artifacts = append(artifacts, db.CorpusArtifactInput{Kind: "fingerprint", ObjectKey: fpKey, ContentHash: fpHashString, MediaType: "application/json", Format: "fingerprint-compact-v2", ByteLength: int64(len(fingerprint)), Language: languageValue, Source: "legacy"})
			}
			if _, err := repository.ImportLegacyCorpusCandidate(ctx, db.ImportLegacyCorpusCandidateParams{Row: row, Language: languageValue, ContentHash: hashString, Artifacts: artifacts}); err != nil {
				return err
			}
		}
	}
}

func gunzipLegacy(data []byte, limit int64) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("decompressed artifact too large")
	}
	return body, nil
}

// Shutdown gracefully stops the worker, waiting for in-flight tasks to finish.
func (w *WorkerServer) Shutdown() {
	w.srv.Shutdown()
	_ = w.redis.Close()
}

// HandlePodcastRefreshTask fetches and refreshes a single podcast feed.
func (w *WorkerServer) HandlePodcastRefreshTask(ctx context.Context, t *asynq.Task) error {
	const op errs.Op = "tasks/WorkerServer.HandlePodcastRefreshTask"

	var payload PodcastRefreshPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return errs.E(op, errs.Internal, fmt.Errorf("%w: %v", asynq.SkipRetry, err))
	}
	if payload.JobID != "" {
		_ = w.redis.Set(ctx, "refresh-job:"+payload.JobID, "running", 15*time.Minute).Err()
		defer func() {
			completeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = w.redis.Set(completeCtx, "refresh-job:"+payload.JobID, "completed", 15*time.Minute).Err()
		}()
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
			// A feed inside its backoff window already has its failure
			// recorded and a retry scheduled; re-warning here is noise.
			if errors.Is(err, crawler.ErrRefreshBackoff) {
				continue
			}
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
	return transcripts.PromoteFetchedSighting(ctx, w.db, w.objects, payload.SightingID)
}
