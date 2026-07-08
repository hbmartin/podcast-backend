package middlewares

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/hbmartin/podcast-backend/pcerrors"
)

// RateLimiter applies a per-client-IP token bucket. It fronts the credential
// endpoints (login, register, forgot password, token refresh) to slow
// brute-force attempts; other routes stay unlimited.
type RateLimiter struct {
	perMinute int
	burst     int
	// trustProxy enables X-Forwarded-For as the client identity. Only safe
	// when a reverse proxy always overwrites the header.
	trustProxy bool

	mu      sync.Mutex
	clients map[string]*rateClient
}

type rateClient struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// sweepThreshold bounds the client map: past this size, stale entries are
// evicted on the next new-client insert.
const sweepThreshold = 10_000

// NewRateLimiter returns a limiter allowing perMinute requests (burst 2x)
// per client IP, or nil (a no-op) when perMinute <= 0.
func NewRateLimiter(perMinute int, trustProxy bool) *RateLimiter {
	if perMinute <= 0 {
		return nil
	}
	return &RateLimiter{
		perMinute:  perMinute,
		burst:      perMinute * 2,
		trustProxy: trustProxy,
		clients:    map[string]*rateClient{},
	}
}

// Handler wraps next with the limit. A nil receiver passes through, so a
// disabled limiter costs nothing at the call site.
func (rl *RateLimiter) Handler(next http.Handler) http.Handler {
	if rl == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.allow(rl.clientIP(r)) {
			pcerrors.Write(w, http.StatusTooManyRequests, pcerrors.RateLimited, "too many requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (rl *RateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	c, ok := rl.clients[ip]
	if !ok {
		if len(rl.clients) >= sweepThreshold {
			for k, v := range rl.clients {
				if now.Sub(v.lastSeen) > 10*time.Minute {
					delete(rl.clients, k)
				}
			}
		}
		c = &rateClient{limiter: rate.NewLimiter(rate.Limit(rl.perMinute)/60, rl.burst)}
		rl.clients[ip] = c
	}
	c.lastSeen = now

	return c.limiter.Allow()
}

func (rl *RateLimiter) clientIP(r *http.Request) string {
	if rl.trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// leftmost address is the originating client
			if i := strings.IndexByte(xff, ','); i >= 0 {
				xff = xff[:i]
			}
			return strings.TrimSpace(xff)
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
