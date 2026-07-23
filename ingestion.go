package main

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/hbmartin/podcast-backend/crawler"
	"golang.org/x/time/rate"
)

const unknownFeedsPerUserPerHour = 20

type feedIngestionJob struct {
	feedURL string
}

type userIngestionLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

type feedIngestionDispatcher struct {
	ctx          context.Context
	jobs         chan feedIngestionJob
	allowPrivate bool
	ingest       func(context.Context, string) error

	mu       sync.Mutex
	users    map[int64]*userIngestionLimiter
	inFlight map[string]struct{}
}

func newFeedIngestionDispatcher(
	ctx context.Context,
	workers int,
	queueSize int,
	allowPrivate bool,
	ingest func(context.Context, string) error,
) *feedIngestionDispatcher {
	if workers < 1 {
		workers = 1
	}
	if queueSize < workers {
		queueSize = workers
	}
	d := &feedIngestionDispatcher{
		ctx: ctx, jobs: make(chan feedIngestionJob, queueSize),
		allowPrivate: allowPrivate, ingest: ingest,
		users: make(map[int64]*userIngestionLimiter), inFlight: make(map[string]struct{}),
	}
	for range workers {
		go d.worker()
	}
	return d
}

// Submit is the post-commit sync hook. It never blocks the request on queue or
// network work; overload and abuse are rejected before consuming workers.
func (d *feedIngestionDispatcher) Submit(userID int64, feedURL string) {
	canonical := crawler.CanonicalFeedURL(feedURL)
	if err := validateSubmittedFeedURL(canonical, d.allowPrivate); err != nil {
		if !errors.Is(d.ctx.Err(), context.Canceled) {
			slog.Warn("sync ingestion URL rejected", "user_id", userID, "error", err)
		}
		return
	}

	now := time.Now()
	d.mu.Lock()
	// A URL already being ingested is a no-op, not new work; it must not
	// consume the submitter's rate-limit budget.
	if _, exists := d.inFlight[canonical]; exists {
		d.mu.Unlock()
		return
	}
	entry := d.users[userID]
	if entry == nil {
		entry = &userIngestionLimiter{
			limiter: rate.NewLimiter(rate.Every(time.Hour/unknownFeedsPerUserPerHour), unknownFeedsPerUserPerHour),
		}
		d.users[userID] = entry
	}
	entry.lastSeen = now
	if !entry.limiter.Allow() {
		d.mu.Unlock()
		slog.Warn("sync ingestion rate limit exceeded", "user_id", userID)
		return
	}
	d.inFlight[canonical] = struct{}{}
	if len(d.users) > 10_000 {
		for id, candidate := range d.users {
			if now.Sub(candidate.lastSeen) > 2*time.Hour {
				delete(d.users, id)
			}
		}
	}
	d.mu.Unlock()

	select {
	case d.jobs <- feedIngestionJob{feedURL: canonical}:
	case <-d.ctx.Done():
		d.finish(canonical)
	default:
		d.finish(canonical)
		slog.Warn("sync ingestion queue full", "user_id", userID)
	}
}

func validateSubmittedFeedURL(feedURL string, allowPrivate bool) error {
	if allowPrivate {
		// The e2e-only mode still restricts schemes, hosts, and user info;
		// only the address-class check is relaxed in the fetcher itself.
		parsed, err := url.ParseRequestURI(feedURL)
		if err != nil {
			return err
		}
		if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil {
			return errors.New("feed URL must be HTTP(S), include a host, and omit user info")
		}
		return nil
	}
	return crawler.ValidateFeedURL(feedURL)
}

func (d *feedIngestionDispatcher) worker() {
	for {
		select {
		case <-d.ctx.Done():
			return
		case job := <-d.jobs:
			jobCtx, cancel := context.WithTimeout(d.ctx, 2*time.Minute)
			err := d.ingest(jobCtx, job.feedURL)
			cancel()
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, crawler.ErrRefreshBackoff) {
				slog.Warn("sync ingestion failed", "feed_url", job.feedURL, "error", err)
			}
			d.finish(job.feedURL)
		}
	}
}

func (d *feedIngestionDispatcher) finish(feedURL string) {
	d.mu.Lock()
	delete(d.inFlight, feedURL)
	d.mu.Unlock()
}
