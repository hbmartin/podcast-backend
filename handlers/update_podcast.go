package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hbmartin/podcast-backend/middlewares"
)

const refreshJobTTL = 15 * time.Minute

var fixedWindowScript = `
local count = redis.call('INCR', KEYS[1])
if count == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[1]) end
local ttl = redis.call('PTTL', KEYS[1])
return {count, ttl}
`

func (h Handlers) allowRedisWindow(r *http.Request, key string, limit int, window time.Duration) (bool, int) {
	if h.Redis == nil {
		return false, 1
	}
	result, err := h.Redis.Eval(r.Context(), fixedWindowScript, []string{key}, window.Milliseconds()).Int64Slice()
	if err != nil || len(result) != 2 {
		return false, 1
	}
	retry := int((result[1] + 999) / 1000)
	if retry < 1 {
		retry = 1
	}
	return result[0] <= int64(limit), retry
}

func (h Handlers) rateIdentity(r *http.Request) string {
	if keyID := attestKeyID(r); keyID != "" {
		return "attest:" + keyID
	}
	return "ip:" + middlewares.ClientIP(r, h.TrustProxyHeaders)
}

// GetUpdatePodcast implements the client's 202 + Location polling contract.
func (h Handlers) GetUpdatePodcast(w http.ResponseWriter, r *http.Request) {
	podcastUUID := strings.TrimSpace(r.URL.Query().Get("podcast_uuid"))
	lastEpisodeUUID := strings.TrimSpace(r.URL.Query().Get("last_episode_uuid"))
	if !uuidPattern.MatchString(podcastUUID) || (lastEpisodeUUID != "" && !uuidPattern.MatchString(lastEpisodeUUID)) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if h.Queue == nil || h.Redis == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	allowed, retry := h.allowRedisWindow(r, "rate:refresh:"+h.rateIdentity(r), 10, time.Minute)
	if !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}

	podcast, err := h.Queries.GetPodcastByUUID(r.Context(), podcastUUID)
	if err == nil && podcast.LatestEpisodeUuid != nil && *podcast.LatestEpisodeUuid != "" && *podcast.LatestEpisodeUuid != lastEpisodeUUID {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		return
	}

	sum := sha256.Sum256([]byte(podcastUUID + "\x00" + lastEpisodeUUID))
	jobID := hex.EncodeToString(sum[:])
	cooldownKey := "refresh-cooldown:" + podcastUUID
	claimed, redisErr := h.Redis.SetNX(r.Context(), cooldownKey, jobID, refreshJobTTL).Result()
	if redisErr != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if !claimed {
		if existing, getErr := h.Redis.Get(r.Context(), cooldownKey).Result(); getErr == nil && existing != "" {
			jobID = existing
		}
	}
	statusKey := "refresh-job:" + jobID
	if claimed {
		_ = h.Redis.Set(r.Context(), statusKey, "queued", refreshJobTTL).Err()
		if err != nil {
			_ = h.Redis.Set(r.Context(), statusKey, "completed", refreshJobTTL).Err()
		} else if enqueueErr := h.Queue.EnqueuePodcastRefreshJob(r.Context(), podcast.Uuid, podcast.FeedUrl, jobID); enqueueErr != nil {
			_ = h.Redis.Del(r.Context(), cooldownKey, statusKey).Err()
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
	}
	h.writeRefreshPollingResponse(w, r, jobID)
}

func (h Handlers) GetUpdatePodcastJob(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	if len(jobID) != 64 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if _, err := hex.DecodeString(jobID); err != nil || h.Redis == nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	status, err := h.Redis.Get(r.Context(), "refresh-job:"+jobID).Result()
	w.Header().Set("Cache-Control", "no-store")
	// Both terminal statuses end polling with 200; "failed" means the crawl
	// ran and did not succeed (the podcast row records the failure detail).
	if err != nil || status == "completed" || status == "failed" {
		w.WriteHeader(http.StatusOK)
		return
	}
	h.writeRefreshPollingResponse(w, r, jobID)
}

func (h Handlers) writeRefreshPollingResponse(w http.ResponseWriter, r *http.Request, jobID string) {
	w.Header().Set("Location", h.baseURL(r)+"/api/v1/update_podcast/jobs/"+jobID)
	w.Header().Set("Retry-After", "2")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusAccepted)
}
