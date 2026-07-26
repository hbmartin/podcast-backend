package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hbmartin/podcast-backend/attest"
	"github.com/redis/go-redis/v9"
)

var distributedFixedWindowScript = redis.NewScript(`
local current = redis.call('INCR', KEYS[1])
if current == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[2]) end
local ttl = redis.call('PTTL', KEYS[1])
if current > tonumber(ARGV[1]) then return {0, ttl} end
return {1, ttl}`)

func redisLimit(ctx context.Context, client *redis.Client, key string, limit int64, window time.Duration) (bool, time.Duration, error) {
	if client == nil {
		return false, 0, fmt.Errorf("redis rate limiter unavailable")
	}
	result, err := distributedFixedWindowScript.Run(ctx, client, []string{"rate:" + key}, limit, window.Milliseconds()).Int64Slice()
	if err != nil || len(result) != 2 {
		return false, 0, err
	}
	return result[0] == 1, time.Duration(result[1]) * time.Millisecond, nil
}

func writeRateLimited(w http.ResponseWriter, retry time.Duration) {
	seconds := int64((retry + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", fmt.Sprint(seconds))
	w.WriteHeader(http.StatusTooManyRequests)
}

func installationID(r *http.Request) string {
	if keyID, ok := r.Context().Value(attestKeyIDCtx).(string); ok && keyID != "" {
		return keyID
	}
	if id := strings.TrimSpace(r.Header.Get("X-Installation-ID")); id != "" {
		return id
	}
	return strings.TrimSpace(r.Header.Get(attest.HeaderKeyID))
}
