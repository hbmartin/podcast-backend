package handlers

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/metrics"
	"github.com/hbmartin/podcast-backend/pcerrors"
	pb "github.com/hbmartin/podcast-backend/protos/api"
	"github.com/hbmartin/podcast-backend/transcripts"
)

// Compressed body caps (docs/AppAttest.md §4) enforced by the attest middleware.
const (
	MaxContributeBody = 3 << 20  // 3 MiB
	MaxSightingBody   = 64 << 10 // 64 KiB
	maxVTTBytes       = 2 << 20  // decompressed VTT cap (docs/TranscriptContributions.md §4)
	maxFingerprint    = 512 << 10
	durationTolerance = 0.20 // cue span must be within ±20% of episode duration
)

// Per-attribution daily rate limits (docs/TranscriptContributions.md §5).
const (
	contributionDailyLimit = 50
	sightingDailyLimit     = 200
	rateLimitWindow        = 24 * time.Hour
	rateLimitRetryAfter    = "3600"
)

// tokenishQueryName flags query parameters that likely carry a signed/expiring
// token, so a sighting URL bearing one is rejected (docs/TranscriptContributions.md §1).
var tokenishQueryName = regexp.MustCompile(`(?i)(token|sig|signature|key|auth|session|expires|policy)`)

// vttCueLine matches a WebVTT/SRT cue timing line and captures the end time.
var vttCueLine = regexp.MustCompile(`-->\s*((?:\d{1,2}:)?\d{1,2}:\d{2}[.,]\d{1,3})`)

// PostTranscriptContribute ingests a crowdsourced generated transcript
// (docs/TranscriptContributions.md §3, §4). The attest middleware has already
// verified any assertion and capped the compressed body.
func (h Handlers) PostTranscriptContribute(w http.ResponseWriter, r *http.Request) {
	req := &pb.TranscriptContributionRequest{}
	if err := bindProtoGzip(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	if req.EpisodeUuid == "" || req.PodcastUuid == "" {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "episode and podcast are required")
		return
	}

	// Validate the VTT (decompress, non-empty, cue span within tolerance).
	if len(req.Vtt) > maxVTTBytes {
		reject(w, "size")
		return
	}
	vtt, err := gunzipCapped(req.Vtt, maxVTTBytes)
	if err != nil || len(bytes.TrimSpace(vtt)) == 0 {
		reject(w, "vtt")
		return
	}
	d := req.EpisodeDurationSeconds
	if math.IsNaN(d) || math.IsInf(d, 0) || d < 0 {
		reject(w, "duration")
		return
	}
	end, ok := maxCueEndSeconds(vtt)
	if !ok {
		reject(w, "vtt")
		return
	}
	if d > 0 && (end < d*(1-durationTolerance) || end > d*(1+durationTolerance)) {
		reject(w, "duration")
		return
	}

	// Validate the fingerprint (decompress, decodes as JSON, non-empty).
	if len(req.Fingerprint) > maxFingerprint {
		reject(w, "size")
		return
	}
	fp, err := gunzipCapped(req.Fingerprint, maxFingerprint)
	if err != nil || !json.Valid(fp) || len(bytes.TrimSpace(fp)) < 2 {
		reject(w, "fingerprint")
		return
	}

	attribution, attributionID := attribution(r)
	if !h.withinRate(r.Context(), w, "contribution", attribution, attributionID, contributionDailyLimit) {
		return
	}

	var createdAt *time.Time
	if req.CreatedAt != nil {
		t := req.CreatedAt.AsTime()
		createdAt = &t
	}

	if err := h.Queries.InsertTranscriptContribution(r.Context(), db.InsertTranscriptContributionParams{
		EpisodeUuid:            req.EpisodeUuid,
		PodcastUuid:            req.PodcastUuid,
		VttBlob:                req.Vtt,
		FingerprintBlob:        req.Fingerprint,
		Engine:                 req.Engine,
		ModelID:                req.ModelId,
		Language:               req.Language,
		Diarized:               req.Diarized,
		AppVersion:             req.AppVersion,
		EpisodeDurationSeconds: req.EpisodeDurationSeconds,
		CreatedAt:              createdAt,
		Attribution:            attribution,
		AttributionID:          attributionID,
	}); err != nil {
		writeError(w, r, err)
		return
	}

	metrics.TranscriptContributions.WithLabelValues(normalizeEngineLabel(req.Engine)).Inc()
	w.WriteHeader(http.StatusOK)
}

// PostTranscriptSighting records a report of a publisher-provided transcript and
// schedules a server-side fetch of its content (docs/TranscriptContributions.md §3).
func (h Handlers) PostTranscriptSighting(w http.ResponseWriter, r *http.Request) {
	req := &pb.TranscriptSightingRequest{}
	if err := bindProtoGzip(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	if req.EpisodeUuid == "" || req.PodcastUuid == "" || !isTokenFreeURL(req.TranscriptUrl) {
		reject(w, "url")
		return
	}

	attribution, attributionID := attribution(r)
	if !h.withinRate(r.Context(), w, "sighting", attribution, attributionID, sightingDailyLimit) {
		return
	}

	id, err := h.Queries.InsertTranscriptSighting(r.Context(), db.InsertTranscriptSightingParams{
		EpisodeUuid:   req.EpisodeUuid,
		PodcastUuid:   req.PodcastUuid,
		TranscriptUrl: req.TranscriptUrl,
		Format:        req.Format,
		Language:      req.Language,
		Attribution:   attribution,
		AttributionID: attributionID,
	})
	if err != nil {
		// Only the ON CONFLICT DO NOTHING no-row result is a duplicate; real
		// database failures must not be masked as a successful sighting.
		if errors.Is(err, pgx.ErrNoRows) {
			metrics.TranscriptSightings.WithLabelValues("duplicate").Inc()
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeError(w, r, err)
		return
	}

	metrics.TranscriptSightings.WithLabelValues("accepted").Inc()
	h.scheduleSightingFetch(id)
	w.WriteHeader(http.StatusAccepted)
}

// scheduleSightingFetch enqueues the publisher fetch on the task queue, falling
// back to a bounded in-process goroutine when the queue is disabled (mirroring
// the push-delivery pattern).
// directFetchSem bounds concurrent in-process sighting fetches used when the
// task queue is disabled, so a burst cannot exhaust goroutines/DB connections.
var directFetchSem = make(chan struct{}, 8)

func (h Handlers) scheduleSightingFetch(sightingID int64) {
	if h.Queue != nil {
		if err := h.Queue.EnqueueSightingFetch(context.Background(), sightingID); err == nil {
			return
		}
	}
	select {
	case directFetchSem <- struct{}{}:
	default:
		slog.Warn("dropping in-process sighting fetch; concurrency limit reached", "sighting_id", sightingID)
		return
	}
	store := h.Queries
	go func() {
		defer func() { <-directFetchSem }()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = transcripts.FetchAndStore(ctx, store, sightingID)
	}()
}

// normalizeEngineLabel bounds the Prometheus engine label to a fixed set so an
// attacker-controlled engine string cannot create unbounded time series.
func normalizeEngineLabel(engine string) string {
	switch engine {
	case "whisperkit", "applespeech", "assemblyai", "openai", "elevenlabs", "gemini", "deepgram", "publisher":
		return engine
	case "":
		return "unknown"
	default:
		return "other"
	}
}

func reject(w http.ResponseWriter, cause string) {
	metrics.TranscriptRejections.WithLabelValues(cause).Inc()
	pcerrors.Write(w, http.StatusUnprocessableEntity, pcerrors.AccessDenied, "invalid transcript submission")
}

// attribution resolves the storage attribution: the account (valid Bearer) when
// present, otherwise the App Attest install key, otherwise anonymous.
func attribution(r *http.Request) (string, string) {
	if user := getUser(r.Context()); user != nil {
		return "user", user.UUID
	}
	if keyID := attestKeyID(r); keyID != "" {
		return "install", keyID
	}
	return "anonymous", ""
}

func (h Handlers) withinRate(ctx context.Context, w http.ResponseWriter, kind, attribution, attributionID string, limit int64) bool {
	var count int64
	var err error
	cutoff := time.Now().Add(-rateLimitWindow)
	if kind == "sighting" {
		count, err = h.Queries.CountRecentSightingsByAttribution(ctx, db.CountRecentSightingsByAttributionParams{
			Attribution: attribution, AttributionID: attributionID, ReceivedAt: cutoff,
		})
	} else {
		count, err = h.Queries.CountRecentContributionsByAttribution(ctx, db.CountRecentContributionsByAttributionParams{
			Attribution: attribution, AttributionID: attributionID, ReceivedAt: cutoff,
		})
	}
	if err == nil && count >= limit {
		metrics.TranscriptRejections.WithLabelValues("rate_limit").Inc()
		w.Header().Set("Retry-After", rateLimitRetryAfter)
		pcerrors.Write(w, http.StatusTooManyRequests, pcerrors.RateLimited, "rate limit exceeded")
		return false
	}
	return true
}

// isTokenFreeURL re-validates a sighting URL server-side: http(s), no userinfo,
// and no query parameter that looks like a signed/expiring token
// (docs/TranscriptContributions.md §1).
func isTokenFreeURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return false
	}
	if u.User != nil {
		return false
	}
	// Reject obvious non-public IP literals early (the fetcher's dialer also
	// blocks resolved private addresses as defense in depth).
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
			ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
			return false
		}
	}
	for name, values := range u.Query() {
		if tokenishQueryName.MatchString(name) {
			return false
		}
		for _, v := range values {
			if len(v) >= 16 {
				return false
			}
		}
	}
	return true
}

func gunzipCapped(data []byte, limit int) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	out, err := io.ReadAll(io.LimitReader(zr, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(out) > limit {
		return nil, errors.New("decompressed body exceeds limit")
	}
	return out, nil
}

// maxCueEndSeconds returns the latest cue end time in a WebVTT/SRT body, and
// whether at least one cue was found.
func maxCueEndSeconds(vtt []byte) (float64, bool) {
	matches := vttCueLine.FindAllSubmatch(vtt, -1)
	if len(matches) == 0 {
		return 0, false
	}
	var max float64
	for _, m := range matches {
		if s, ok := parseCueTimestamp(string(m[1])); ok && s > max {
			max = s
		}
	}
	return max, true
}

func parseCueTimestamp(s string) (float64, bool) {
	s = strings.Replace(strings.TrimSpace(s), ",", ".", 1)
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	var total float64
	for _, p := range parts {
		v, err := strconv.ParseFloat(p, 64)
		if err != nil {
			return 0, false
		}
		total = total*60 + v
	}
	return total, true
}
