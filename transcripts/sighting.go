// Package transcripts holds the server-side background work for crowdsourced
// transcript sightings (docs/TranscriptContributions.md §3): fetching the
// publisher transcript the client reported and storing it. It is a leaf package
// so both the HTTP handlers and the asynq worker can drive it without an import
// cycle.
package transcripts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
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

var (
	errTooManyRedirects = errors.New("too many redirects")
	// ErrRetryable marks a transient sighting-fetch failure so the asynq worker
	// retries it, distinct from a permanent rejection which returns nil.
	ErrRetryable = errors.New("retryable sighting fetch failure")
)

// blockedIP reports whether an address must not be fetched: loopback, private
// (RFC1918 / IPv6 ULA), link-local (incl. the 169.254.169.254 cloud-metadata
// endpoint), unspecified, or multicast. This is the SSRF guard.
func blockedIP(ip net.IP) bool {
	return ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() ||
		ip.IsInterfaceLocalMulticast()
}

// safeDialContext resolves the host and refuses to connect to any non-public
// address, then dials the resolved IP directly so a later DNS rebind cannot
// point a validated hostname at an internal address. Because the same transport
// handles redirects, every redirect target is re-validated here too.
func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	var lastErr error = fmt.Errorf("no dialable address for %q", host)
	for _, ip := range ips {
		if blockedIP(ip.IP) {
			return nil, fmt.Errorf("refusing to fetch non-public address %s", ip.IP)
		}
	}
	for _, ip := range ips {
		conn, derr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if derr == nil {
			return conn, nil
		}
		lastErr = derr
	}
	return nil, lastErr
}

var httpClient = &http.Client{
	Timeout: fetchTimeout,
	CheckRedirect: func(_ *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return errTooManyRedirects
		}
		return nil
	},
	Transport: &http.Transport{
		DialContext:           safeDialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		DisableKeepAlives:     true,
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
// under SSRF/timeout/redirect/content-type/size limits, and stores the content
// (status "fetched"). Transient failures (network errors, 429, 5xx, status
// writes) return an error wrapping ErrRetryable so the worker retries; permanent
// rejections (non-public target, bad content type, oversize) mark the row and
// return nil.
func FetchAndStore(ctx context.Context, store db.Store, sightingID int64) error {
	s, err := store.GetTranscriptSighting(ctx, sightingID)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.TranscriptUrl, nil)
	if err != nil {
		return rejectSighting(ctx, store, sightingID, "rejected")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		// Transport errors include the SSRF guard rejecting a non-public target;
		// those are permanent, but network blips are transient. Retrying a
		// blocked address is harmless (it stays blocked), so treat all as
		// retryable and let the worker's bounded retries give up.
		metrics.TranscriptSightings.WithLabelValues("fetch_failed").Inc()
		if serr := store.MarkSightingStatus(ctx, db.MarkSightingStatusParams{ID: sightingID, Status: "failed"}); serr != nil {
			return serr
		}
		return fmt.Errorf("%w: %v", ErrRetryable, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
		// proceed
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		metrics.TranscriptSightings.WithLabelValues("fetch_failed").Inc()
		if serr := store.MarkSightingStatus(ctx, db.MarkSightingStatusParams{ID: sightingID, Status: "failed"}); serr != nil {
			return serr
		}
		return fmt.Errorf("%w: publisher returned %d", ErrRetryable, resp.StatusCode)
	default:
		return rejectSighting(ctx, store, sightingID, "rejected")
	}

	ct := resp.Header.Get("Content-Type")
	if !contentTypeAllowed(ct) {
		return rejectSighting(ctx, store, sightingID, "rejected")
	}
	content, err := io.ReadAll(io.LimitReader(resp.Body, maxSightingBytes+1))
	if err != nil {
		// A partial read is a transient network failure: retry.
		metrics.TranscriptSightings.WithLabelValues("fetch_failed").Inc()
		if serr := store.MarkSightingStatus(ctx, db.MarkSightingStatusParams{ID: sightingID, Status: "failed"}); serr != nil {
			return serr
		}
		return fmt.Errorf("%w: %v", ErrRetryable, err)
	}
	if len(content) == 0 || int64(len(content)) > maxSightingBytes {
		return rejectSighting(ctx, store, sightingID, "rejected")
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

func rejectSighting(ctx context.Context, store db.Store, sightingID int64, status string) error {
	metrics.TranscriptSightings.WithLabelValues("rejected").Inc()
	return store.MarkSightingStatus(ctx, db.MarkSightingStatusParams{ID: sightingID, Status: status})
}
