package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/itunes"
	pb "github.com/hbmartin/podcast-backend/protos/api"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type episodeRecommendationStore interface {
	GetEpisodeRecommendationData(context.Context, int64) (db.EpisodeRecommendationData, error)
}

type relatedPodcastStore interface {
	GetRelatedPodcastData(context.Context, string) (db.RelatedPodcastData, error)
}

func (h Handlers) PostRecommendEpisodes(w http.ResponseWriter, r *http.Request) {
	request := &pb.BasicRequest{}
	if err := bindProto(r, request); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}
	repository, ok := h.Queries.(episodeRecommendationStore)
	if !ok {
		w.WriteHeader(http.StatusNotImplemented)
		return
	}
	data, err := repository.GetEpisodeRecommendationData(r.Context(), user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	secret := "podcast-backend-recommendations"
	if h.Config != nil && h.Config.JWTSecret != "" {
		secret = h.Config.JWTSecret
	}
	candidate := selectRecommendedEpisode(data, fmt.Sprint(user.ID), []byte(secret), time.Now().UTC())
	response := &pb.EpisodesResponse{}
	if candidate != nil {
		episode := &pb.EpisodeResponse{
			Uuid: candidate.Uuid, Url: candidate.URL, Duration: candidate.DurationSecs,
			FileType: candidate.FileType, Title: candidate.Title, Size: candidate.FileSize,
			PodcastUuid: candidate.PodcastUuid, PodcastTitle: candidate.PodcastTitle,
			Author: candidate.PodcastAuthor, EpisodeType: candidate.EpisodeType,
			EpisodeSeason: candidate.Season, EpisodeNumber: candidate.Number,
		}
		if candidate.PublishedAt != nil {
			episode.Published = timestamppb.New(*candidate.PublishedAt)
		}
		response.Total = 1
		response.Episodes = []*pb.EpisodeResponse{episode}
	}
	writeProto(w, http.StatusOK, response)
}

func selectRecommendedEpisode(data db.EpisodeRecommendationData, userSeed string, secret []byte, now time.Time) *db.RecommendationEpisode {
	affinity := map[string]float64{}
	allowExplicit := false
	if len(data.Signals) < 3 {
		for _, subscription := range data.Subscriptions {
			for _, category := range categories(subscription.Category) {
				affinity[category]++
			}
			allowExplicit = allowExplicit || subscription.Explicit
		}
	} else {
		for _, subscription := range data.Subscriptions {
			for _, category := range categories(subscription.Category) {
				affinity[category] += 1
			}
			allowExplicit = allowExplicit || subscription.Explicit
		}
		for _, signal := range data.Signals {
			age := now.Sub(signal.OccurredAt)
			if age < 0 {
				age = 0
			}
			decay := math.Exp(-math.Ln2 * age.Hours() / (90 * 24))
			for _, category := range categories(signal.Category) {
				affinity[category] += signal.Weight * decay
			}
			allowExplicit = allowExplicit || signal.Explicit
		}
	}
	seed := hmacBytes(secret, userSeed+"|"+now.Format("2006-01-02"))
	preferDiscovery := len(data.Subscriptions) >= 5 && binary.BigEndian.Uint64(seed[:8])%5 == 0
	for _, poolDiscovery := range []bool{preferDiscovery, !preferDiscovery} {
		for _, horizon := range []time.Duration{30 * 24 * time.Hour, 90 * 24 * time.Hour} {
			var best *db.RecommendationEpisode
			bestScore := math.Inf(1)
			for i := range data.Candidates {
				candidate := &data.Candidates[i]
				if candidate.Library == poolDiscovery || (candidate.Explicit && !allowExplicit) || candidate.PublishedAt == nil {
					continue
				}
				age := now.Sub(*candidate.PublishedAt)
				if age < 0 {
					age = 0
				}
				if age > horizon {
					continue
				}
				weight := 0.1
				for _, category := range categories(candidate.Category) {
					weight += affinity[category]
				}
				weight *= 1 + math.Exp(-math.Ln2*age.Hours()/(30*24))
				uBytes := hmacBytes(seed, candidate.Uuid)
				u := (float64(binary.BigEndian.Uint64(uBytes[:8])) + 1) / (float64(^uint64(0)) + 2)
				score := -math.Log(u) / weight
				if score < bestScore {
					best, bestScore = candidate, score
				}
			}
			if best != nil {
				return best
			}
		}
	}
	return nil
}

func hmacBytes(key []byte, message string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(message))
	return mac.Sum(nil)
}

var countryPattern = regexp.MustCompile(`^[A-Z]{2}$`)

func (h Handlers) GetRelatedPodcasts(w http.ResponseWriter, r *http.Request) {
	install := installationID(r)
	if install == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	allowed, retry, err := redisLimit(r.Context(), h.Redis, "related:"+install, 60, time.Minute)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if !allowed {
		writeRateLimited(w, retry)
		return
	}
	uuid := r.PathValue("uuid")
	if !uuidPattern.MatchString(uuid) {
		http.NotFound(w, r)
		return
	}
	country := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("country")))
	if !countryPattern.MatchString(country) {
		country = "US"
	}
	cacheKey := "related:cache:" + uuid + ":" + country
	if cached, err := h.Redis.Get(r.Context(), cacheKey).Bytes(); err == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "private, max-age=21600")
		_, _ = w.Write(cached)
		return
	}
	repository, ok := h.Queries.(relatedPodcastStore)
	if !ok {
		w.WriteHeader(http.StatusNotImplemented)
		return
	}
	data, err := repository.GetRelatedPodcastData(r.Context(), uuid)
	if err != nil {
		writeError(w, r, err)
		return
	}
	items := rankRelatedPodcasts(data.Source, data.Candidates, time.Now().UTC())
	seenFeeds := map[string]bool{normalizeFeedURL(data.Source.FeedURL): true}
	podcasts := make([]map[string]any, 0, 12)
	for _, item := range items {
		feed := normalizeFeedURL(item.FeedURL)
		if feed == "" || seenFeeds[feed] {
			continue
		}
		seenFeeds[feed] = true
		podcasts = append(podcasts, map[string]any{
			"uuid": item.Uuid, "title": item.Title, "author": item.Author,
			"description": item.Description, "website": item.WebsiteURL, "explicit": item.Explicit,
		})
		if len(podcasts) == 12 {
			break
		}
	}
	if len(podcasts) < 12 && h.Search != nil {
		term := strings.TrimSpace(data.Source.Title + " " + data.Source.Author)
		var upstream []itunes.Result
		if searcher, ok := h.Search.(itunes.CountrySearcher); ok {
			upstream, err = searcher.SearchCountry(r.Context(), term, country, 24)
		} else {
			upstream, err = h.Search.Search(r.Context(), term, 24)
		}
		if err == nil {
			for _, item := range upstream {
				feed := normalizeFeedURL(item.FeedURL)
				if feed == "" || seenFeeds[feed] {
					continue
				}
				seenFeeds[feed] = true
				podcasts = append(podcasts, map[string]any{
					"itunes": fmt.Sprint(item.CollectionID), "title": item.Title, "author": item.Author, "explicit": item.Explicit,
				})
				if len(podcasts) == 12 {
					break
				}
			}
		}
	}
	encoded, _ := json.Marshal(map[string]any{"podcasts": podcasts})
	_ = h.Redis.Set(r.Context(), cacheKey, encoded, 6*time.Hour).Err()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, max-age=21600")
	_, _ = w.Write(encoded)
}

func rankRelatedPodcasts(source db.RelatedPodcast, candidates []db.RelatedPodcast, now time.Time) []db.RelatedPodcast {
	maxSubscribers := int64(0)
	for _, candidate := range candidates {
		if candidate.SubscriberCount > maxSubscribers {
			maxSubscribers = candidate.SubscriberCount
		}
	}
	type scored struct {
		podcast db.RelatedPodcast
		score   float64
	}
	ranked := make([]scored, 0, len(candidates))
	for _, candidate := range candidates {
		freshness := 0.0
		if candidate.LatestPublished != nil {
			age := now.Sub(*candidate.LatestPublished)
			if age < 0 {
				age = 0
			}
			freshness = math.Exp(-math.Ln2 * age.Hours() / (365 * 24))
		}
		popularity := 0.0
		if maxSubscribers > 0 {
			popularity = math.Log1p(float64(candidate.SubscriberCount)) / math.Log1p(float64(maxSubscribers))
		}
		score := 4*jaccard(categories(source.Category), categories(candidate.Category)) +
			3*trigramSimilarity(source.Author, candidate.Author) + 2*trigramSimilarity(source.Title, candidate.Title) +
			0.5*popularity + 0.5*freshness
		ranked = append(ranked, scored{candidate, score})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return ranked[i].podcast.Uuid < ranked[j].podcast.Uuid
		}
		return ranked[i].score > ranked[j].score
	})
	result := make([]db.RelatedPodcast, len(ranked))
	for i := range ranked {
		result[i] = ranked[i].podcast
	}
	return result
}

func categories(value string) []string {
	fields := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool { return r == ',' || r == '/' || r == '|' || r == ';' })
	result := make([]string, 0, len(fields))
	for _, field := range fields {
		if trimmed := strings.TrimSpace(field); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func jaccard(a, b []string) float64 {
	left, union := map[string]bool{}, map[string]bool{}
	for _, item := range a {
		left[item], union[item] = true, true
	}
	intersection := 0
	for _, item := range b {
		if left[item] {
			intersection++
		}
		union[item] = true
	}
	if len(union) == 0 {
		return 0
	}
	return float64(intersection) / float64(len(union))
}

func trigramSimilarity(a, b string) float64 {
	grams := func(value string) map[string]bool {
		value = "  " + strings.ToLower(strings.TrimSpace(value)) + " "
		result := map[string]bool{}
		runes := []rune(value)
		for i := 0; i+2 < len(runes); i++ {
			result[string(runes[i:i+3])] = true
		}
		return result
	}
	left, right := grams(a), grams(b)
	if len(left)+len(right) == 0 {
		return 0
	}
	intersection := 0
	for gram := range left {
		if right[gram] {
			intersection++
		}
	}
	return float64(2*intersection) / float64(len(left)+len(right))
}

func normalizeFeedURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return ""
	}
	parsed.Scheme = "https"
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Fragment = ""
	parsed.RawQuery = ""
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	return parsed.String()
}
