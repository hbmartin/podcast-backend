// Package transcripts holds the server-side background work for crowdsourced
// transcript sightings (docs/TranscriptContributions.md §3): fetching the
// publisher transcript the client reported and storing it. It is a leaf package
// so both the HTTP handlers and the asynq worker can drive it without an import
// cycle.
package transcripts

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/metrics"
)

const (
	fetchTimeout     = 20 * time.Second
	maxSightingBytes = 5 << 20 // publisher transcripts are small; cap defensively
	maxRedirects     = 5
)

var errTooManyRedirects = errors.New("too many redirects")

var httpClient = &http.Client{
	Timeout: fetchTimeout,
	CheckRedirect: func(_ *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return errTooManyRedirects
		}
		return nil
	},
}

// contentTypeAllowed mirrors the crawler's transcript MIME allow-list.
func contentTypeAllowed(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	switch ct {
	case "text/vtt", "application/json", "text/json", "application/srt",
		"application/x-subrip", "text/srt", "text/html", "text/plain":
		return true
	}
	return false
}

// FetchAndStore loads the sighting row, fetches its publisher transcript URL
// under timeouts/redirect/content-type/size limits, and stores the content
// (status "fetched"), marking "failed"/"rejected" otherwise. Errors are
// returned for the worker's retry accounting; storage-state transitions are
// always persisted first.
func FetchAndStore(ctx context.Context, store db.Store, sightingID int64) error {
	s, err := store.GetTranscriptSighting(ctx, sightingID)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.TranscriptUrl, nil)
	if err != nil {
		_ = store.MarkSightingStatus(ctx, db.MarkSightingStatusParams{ID: sightingID, Status: "rejected"})
		metrics.TranscriptSightings.WithLabelValues("rejected").Inc()
		return err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		_ = store.MarkSightingStatus(ctx, db.MarkSightingStatusParams{ID: sightingID, Status: "failed"})
		metrics.TranscriptSightings.WithLabelValues("fetch_failed").Inc()
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_ = store.MarkSightingStatus(ctx, db.MarkSightingStatusParams{ID: sightingID, Status: "failed"})
		metrics.TranscriptSightings.WithLabelValues("fetch_failed").Inc()
		return nil
	}
	ct := resp.Header.Get("Content-Type")
	if !contentTypeAllowed(ct) {
		_ = store.MarkSightingStatus(ctx, db.MarkSightingStatusParams{ID: sightingID, Status: "rejected"})
		metrics.TranscriptSightings.WithLabelValues("rejected").Inc()
		return nil
	}
	content, err := io.ReadAll(io.LimitReader(resp.Body, maxSightingBytes+1))
	if err != nil || len(content) == 0 || int64(len(content)) > maxSightingBytes {
		_ = store.MarkSightingStatus(ctx, db.MarkSightingStatusParams{ID: sightingID, Status: "rejected"})
		metrics.TranscriptSightings.WithLabelValues("rejected").Inc()
		return nil
	}

	if err := store.UpdateSightingContent(ctx, db.UpdateSightingContentParams{
		ID:          sightingID,
		Content:     content,
		ContentType: ct,
		Status:      "fetched",
	}); err != nil {
		return err
	}
	slog.Info("Stored sighted transcript", "sighting_id", sightingID, "bytes", len(content))
	metrics.TranscriptSightings.WithLabelValues("fetched").Inc()
	return nil
}
