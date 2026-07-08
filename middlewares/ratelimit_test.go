package middlewares

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestRateLimiterBlocksAfterBurst(t *testing.T) {
	rl := NewRateLimiter(10, false) // burst 20
	handler := rl.Handler(okHandler())

	var lastCode int
	blockedAt := -1
	for i := 0; i < 25; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/user/login", nil)
		r.RemoteAddr = "203.0.113.7:1234"
		handler.ServeHTTP(w, r)
		lastCode = w.Code
		if w.Code == http.StatusTooManyRequests && blockedAt == -1 {
			blockedAt = i
			assert.JSONEq(t, `{"errorMessageId":"rate_limited","errorMessage":"too many requests"}`, w.Body.String())
		}
	}

	assert.Equal(t, http.StatusTooManyRequests, lastCode)
	assert.Equal(t, 20, blockedAt, "burst of 2x limit allowed through")
}

func TestRateLimiterIsolatesClients(t *testing.T) {
	rl := NewRateLimiter(1, false) // burst 2
	handler := rl.Handler(okHandler())

	send := func(addr string) int {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/user/login", nil)
		r.RemoteAddr = addr
		handler.ServeHTTP(w, r)
		return w.Code
	}

	assert.Equal(t, http.StatusOK, send("203.0.113.7:1"))
	assert.Equal(t, http.StatusOK, send("203.0.113.7:2"))
	assert.Equal(t, http.StatusTooManyRequests, send("203.0.113.7:3"), "same IP, different port")
	assert.Equal(t, http.StatusOK, send("203.0.113.8:1"), "other IP unaffected")
}

func TestRateLimiterForwardedForOnlyWhenTrusted(t *testing.T) {
	send := func(rl *RateLimiter, xff string) int {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/user/login", nil)
		r.RemoteAddr = "10.0.0.1:1"
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		rl.Handler(okHandler()).ServeHTTP(w, r)
		return w.Code
	}

	// untrusted: spoofed XFF cannot dodge the limit on the real address
	rl := NewRateLimiter(1, false)
	assert.Equal(t, http.StatusOK, send(rl, "1.1.1.1"))
	assert.Equal(t, http.StatusOK, send(rl, "2.2.2.2"))
	assert.Equal(t, http.StatusTooManyRequests, send(rl, "3.3.3.3"))

	// trusted proxy: the leftmost XFF entry is the client identity
	rl = NewRateLimiter(1, true)
	assert.Equal(t, http.StatusOK, send(rl, "1.1.1.1, 10.0.0.1"))
	assert.Equal(t, http.StatusOK, send(rl, "1.1.1.1"))
	assert.Equal(t, http.StatusTooManyRequests, send(rl, "1.1.1.1"))
	assert.Equal(t, http.StatusOK, send(rl, "2.2.2.2"))
}

func TestRateLimiterDisabled(t *testing.T) {
	rl := NewRateLimiter(0, false)
	assert.Nil(t, rl)

	handler := rl.Handler(okHandler())
	for i := 0; i < 100; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/user/login", nil)
		r.RemoteAddr = "203.0.113.7:1"
		handler.ServeHTTP(w, r)
		assert.Equal(t, http.StatusOK, w.Code)
	}
}
