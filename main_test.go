package main

import (
	"context"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/push"
)

type digestStoreStub struct {
	db.Store
	claimErr error
	claims   int
}

func (s *digestStoreStub) ClaimDigestCandidates(context.Context, int32) ([]int64, error) {
	s.claims++
	return nil, s.claimErr
}

func TestProbeURL(t *testing.T) {
	assert.Equal(t, "http://127.0.0.1:8000/livez", probeURL("", false), "default port")
	assert.Equal(t, "http://127.0.0.1:8000/livez", probeURL(":8000", false))
	assert.Equal(t, "http://127.0.0.1:9000/livez", probeURL("0.0.0.0:9000", false), "bind-all host rewritten to loopback")
	assert.Equal(t, "http://127.0.0.1:8000/livez", probeURL("localhost:8000", false))
	assert.Equal(t, "http://127.0.0.1:7000/livez", probeURL("7000", false), "bare port")
	assert.Equal(t, "https://127.0.0.1:8443/livez", probeURL(":8443", true), "TLS-serving instances probed over https")
}

func TestFeedIngestionDispatcherDeduplicatesAndRateLimits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls atomic.Int32
	dispatcher := newFeedIngestionDispatcher(ctx, 1, 32, false, func(context.Context, string) error {
		calls.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		return nil
	})

	dispatcher.Submit(42, "https://example.com/feed.xml")
	<-started
	dispatcher.Submit(42, "https://example.com/feed.xml")
	for i := 0; i < unknownFeedsPerUserPerHour+5; i++ {
		dispatcher.Submit(99, fmt.Sprintf("https://example.com/feed-%d.xml", i))
	}

	dispatcher.mu.Lock()
	pending := len(dispatcher.inFlight)
	dispatcher.mu.Unlock()
	assert.Equal(t, unknownFeedsPerUserPerHour+1, pending, "one deduplicated job plus the per-user burst")

	assert.Equal(t, int32(1), calls.Load(), "the duplicate URL was not dispatched while the original was in flight")
	close(release)
	assert.Eventually(t, func() bool { return calls.Load() > 1 }, time.Second, 10*time.Millisecond)
}

func TestFeedIngestionDispatcherDuplicateDoesNotConsumeQuota(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	release := make(chan struct{})
	defer close(release)
	dispatcher := newFeedIngestionDispatcher(ctx, 1, 64, false, func(context.Context, string) error {
		<-release
		return nil
	})

	// The first submission spends one token; replaying the in-flight URL,
	// however often, must not spend any more of the burst budget.
	dispatcher.Submit(7, "https://example.com/dup.xml")
	for range unknownFeedsPerUserPerHour * 2 {
		dispatcher.Submit(7, "https://example.com/dup.xml")
	}
	for i := range unknownFeedsPerUserPerHour - 1 {
		dispatcher.Submit(7, fmt.Sprintf("https://example.com/fresh-%d.xml", i))
	}

	dispatcher.mu.Lock()
	pending := len(dispatcher.inFlight)
	dispatcher.mu.Unlock()
	assert.Equal(t, unknownFeedsPerUserPerHour, pending,
		"every fresh URL after the duplicates must still fit in the burst budget")
}

func TestHealthProbeClientVerifiesConfiguredCertificateAndRejectsRedirects(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/health", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	certPath := filepath.Join(t.TempDir(), "server.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	assert.NoError(t, os.WriteFile(certPath, certPEM, 0o600))

	client, err := healthProbeClient(certPath)
	assert.NoError(t, err)
	resp, err := client.Get(server.URL + "/health")
	if assert.NoError(t, err) {
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
	}

	_, err = client.Get(server.URL + "/redirect")
	assert.ErrorContains(t, err, "redirects are not allowed")
}

func TestShouldRunDigestAfterSundayWindowOpens(t *testing.T) {
	sunday := time.Date(2026, time.July, 26, 17, 30, 0, 0, time.UTC)
	assert.True(t, shouldRunDigest(sunday))
	assert.True(t, shouldRunDigest(sunday.Add(5*time.Hour)))
	assert.False(t, shouldRunDigest(sunday.Add(-time.Hour)))
	assert.False(t, shouldRunDigest(sunday.Add(7*time.Hour)))
}

func TestRunDigestSchedulerOnceReturnsFailureAndReleasesOwnedLock(t *testing.T) {
	server, err := miniredis.Run()
	require.NoError(t, err)
	defer server.Close()
	redisClient := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer redisClient.Close()

	sentinel := errors.New("digest query unavailable")
	sunday := time.Date(2026, time.July, 26, 17, 30, 0, 0, time.UTC)
	err = runDigestSchedulerOnce(context.Background(), &digestStoreStub{claimErr: sentinel}, &push.Notifier{}, redisClient, sunday)

	assert.ErrorIs(t, err, sentinel)
	assert.False(t, server.Exists("scheduler:digest:2026-30:lock"))
}

func TestRunDigestSchedulerOnceWritesCompletionMarker(t *testing.T) {
	server, err := miniredis.Run()
	require.NoError(t, err)
	defer server.Close()
	redisClient := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer redisClient.Close()
	store := &digestStoreStub{}
	sunday := time.Date(2026, time.July, 26, 17, 30, 0, 0, time.UTC)

	require.NoError(t, runDigestSchedulerOnce(context.Background(), store, &push.Notifier{}, redisClient, sunday))
	require.NoError(t, runDigestSchedulerOnce(context.Background(), store, &push.Notifier{}, redisClient, sunday.Add(time.Hour)))

	assert.Equal(t, 1, store.claims, "the completion marker makes later invocations no-ops")
	assert.True(t, server.Exists("scheduler:digest:2026-30"))
}

func TestReleaseDigestLockDoesNotDeleteNewOwner(t *testing.T) {
	server, err := miniredis.Run()
	require.NoError(t, err)
	defer server.Close()
	redisClient := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer redisClient.Close()
	server.Set("digest-lock", "new-owner")

	result, err := releaseDigestLock.Run(context.Background(), redisClient, []string{"digest-lock"}, "old-owner").Int()

	require.NoError(t, err)
	assert.Equal(t, 0, result)
	owner, err := server.Get("digest-lock")
	require.NoError(t, err)
	assert.Equal(t, "new-owner", owner)
}
