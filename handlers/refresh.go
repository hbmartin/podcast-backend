package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/hbmartin/podcast-backend/crawler"
	"github.com/hbmartin/podcast-backend/db"
)

// jsonDateFormat matches the client's DateFormatHelper json format.
const jsonDateFormat = "2006-01-02 15:04:05"

// refreshEpisodeJSON mirrors the client's RefreshEpisode CodingKeys exactly.
type refreshEpisodeJSON struct {
	Title          string `json:"title"`
	UUID           string `json:"uuid"`
	URL            string `json:"url"`
	Description    string `json:"description,omitempty"`
	FileType       string `json:"fileType,omitempty"`
	SizeInBytes    int64  `json:"sizeInBytes,omitempty"`
	DurationInSecs int32  `json:"durationInSecs,omitempty"`
	EpType         string `json:"epType,omitempty"`
	EpSeason       int64  `json:"epSeason,omitempty"`
	EpNumber       int64  `json:"epNumber,omitempty"`
	PublishedAt    string `json:"publishedAt,omitempty"`
}

// podcastInfoJSON mirrors the client's PodcastInfo CodingKeys.
type podcastInfoJSON struct {
	Title        string `json:"title,omitempty"`
	UUID         string `json:"uuid,omitempty"`
	Author       string `json:"author,omitempty"`
	Description  string `json:"description,omitempty"`
	CollectionID int64  `json:"collection_id,omitempty"`
	Explicit     bool   `json:"explicit,omitempty"`
}

func writeRefreshOK(w http.ResponseWriter, result any) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"message": "",
		"result":  result,
	})
}

func writeRefreshStatus(w http.ResponseWriter, status string, message string) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  status,
		"message": message,
	})
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// PostRefreshUserUpdate handles POST user/update, the primary refresh call:
// for each subscribed podcast uuid, return episodes newer than the client's
// latest-known episode uuid. It doubles as push-notification registration:
// signed-in clients piggyback their APNs token, the global toggle, and a
// positional per-podcast bit-string on the same body.
func (h Handlers) PostRefreshUserUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Podcasts     string `json:"podcasts"`
		LastEpisodes string `json:"last_episodes"`
		Device       string `json:"device"`
		PushToken    string `json:"push_token"`
		PushOn       string `json:"push_on"`
		// one '0'/'1' per entry of Podcasts, no separators
		PushMessagesOn string `json:"push_messages_on"`
	}
	if err := bindJSON(r, &req); err != nil {
		writeRefreshStatus(w, "error", "invalid request")
		return
	}

	uuids := splitCSV(req.Podcasts)
	lastEpisodes := splitCSV(req.LastEpisodes)

	h.persistPushState(r, req.Device, req.PushToken, req.PushOn, req.PushMessagesOn, uuids)

	valid := make([]string, 0, len(uuids))
	seenPodcasts := map[string]struct{}{}
	for _, uuid := range uuids {
		if uuidPattern.MatchString(uuid) {
			if _, seen := seenPodcasts[uuid]; seen {
				continue
			}
			seenPodcasts[uuid] = struct{}{}
			valid = append(valid, uuid)
		}
	}

	known := map[string]db.Podcast{}
	if len(valid) > 0 {
		podcasts, err := h.Queries.GetPodcastsByUUIDs(r.Context(), valid)
		if err != nil {
			writeError(w, r, err)
			return
		}
		for _, podcast := range podcasts {
			known[podcast.Uuid] = podcast
		}
	}

	cutoffs := map[string]db.Episode{}
	cutoffUUIDs := make([]string, 0, len(lastEpisodes))
	seenCutoffs := map[string]struct{}{}
	for index, uuid := range lastEpisodes {
		if index >= len(uuids) {
			break
		}
		if _, exists := known[uuids[index]]; !exists {
			continue
		}
		if uuidPattern.MatchString(uuid) {
			if _, seen := seenCutoffs[uuid]; !seen {
				seenCutoffs[uuid] = struct{}{}
				cutoffUUIDs = append(cutoffUUIDs, uuid)
			}
		}
	}
	if len(cutoffUUIDs) > 0 {
		episodes, err := h.Queries.GetEpisodesByUUIDs(r.Context(), cutoffUUIDs)
		if err != nil {
			writeError(w, r, err)
			return
		}
		for _, episode := range episodes {
			cutoffs[episode.Uuid] = episode
		}
	}

	updates := map[string][]refreshEpisodeJSON{}
	for i, uuid := range uuids {
		podcast, ok := known[uuid]
		if !ok {
			continue
		}

		lastEpisode := ""
		if i < len(lastEpisodes) {
			lastEpisode = lastEpisodes[i]
		}

		cutoff, hasCutoff := cutoffs[lastEpisode]
		episodes, err := h.newEpisodesSince(r.Context(), podcast, cutoff, hasCutoff)
		if err != nil {
			writeError(w, r, err)
			return
		}
		if len(episodes) > 0 {
			updates[uuid] = episodes
		}
	}

	writeRefreshOK(w, map[string]any{"podcast_updates": updates})
}

// newEpisodesSince returns catalog episodes published after a verified cutoff
// from the same podcast (all recent episodes when it is absent or mismatched).
func (h Handlers) newEpisodesSince(ctx context.Context, podcast db.Podcast, last db.Episode, hasCutoff bool) ([]refreshEpisodeJSON, error) {
	const recentLimit = 20
	const catchUpLimit = 100

	var rows []db.Episode
	var err error

	if hasCutoff && last.PodcastID == podcast.ID && last.PublishedAt != nil {
		rows, err = h.Queries.GetEpisodesPublishedAfter(ctx, db.GetEpisodesPublishedAfterParams{
			PodcastID:   podcast.ID,
			PublishedAt: last.PublishedAt,
			Limit:       catchUpLimit,
		})
		if err != nil {
			return nil, err
		}
		return episodesToRefreshJSON(rows), nil
	}

	rows, err = h.Queries.GetEpisodesByPodcastID(ctx, db.GetEpisodesByPodcastIDParams{PodcastID: podcast.ID, Limit: recentLimit})
	if err != nil {
		return nil, err
	}
	return episodesToRefreshJSON(rows), nil
}

func episodesToRefreshJSON(rows []db.Episode) []refreshEpisodeJSON {
	episodes := make([]refreshEpisodeJSON, 0, len(rows))
	for _, row := range rows {
		episode := refreshEpisodeJSON{
			Title:          row.Title,
			UUID:           row.Uuid,
			URL:            row.AudioUrl,
			FileType:       row.FileType,
			SizeInBytes:    row.FileSize,
			DurationInSecs: row.DurationSecs,
			EpType:         row.EpisodeType,
			EpSeason:       int64(row.Season),
			EpNumber:       int64(row.Number),
		}
		if row.PublishedAt != nil {
			episode.PublishedAt = row.PublishedAt.UTC().Format(jsonDateFormat)
		}
		episodes = append(episodes, episode)
	}
	return episodes
}

// PostPodcastsRefresh handles POST podcasts/refresh: force a single feed
// re-crawl. Queued when the task queue is enabled, synchronous otherwise.
func (h Handlers) PostPodcastsRefresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PodcastUuid string `json:"podcast_uuid"`
	}
	if err := bindJSON(r, &req); err != nil || !uuidPattern.MatchString(req.PodcastUuid) {
		writeRefreshStatus(w, "error", "podcast_uuid is required")
		return
	}

	if h.Queue == nil {
		podcast, err := h.Queries.GetPodcastByUUID(r.Context(), req.PodcastUuid)
		if err != nil {
			slog.Warn("Forced refresh lookup failed", "podcast_uuid", req.PodcastUuid, "error", err)
			writeRefreshStatus(w, "error", "unknown podcast")
			return
		}
		if err := h.Crawler.Crawl(r.Context(), podcast); err != nil {
			slog.Warn("Forced refresh crawl failed", "podcast_uuid", req.PodcastUuid, "error", err)
			writeRefreshStatus(w, "error", "crawl failed")
			return
		}
		writeRefreshStatus(w, "ok", "")
		return
	}

	if err := h.Queue.EnqueuePodcastRefresh(r.Context(), req.PodcastUuid, ""); err != nil {
		slog.Warn("Forced refresh enqueue failed", "podcast_uuid", req.PodcastUuid, "error", err)
		writeRefreshStatus(w, "error", "unable to queue refresh")
		return
	}

	writeRefreshStatus(w, "ok", "")
}

// PostPodcastsSearch handles POST podcasts/search. URL terms are crawled
// synchronously (status "poll" while a slow feed is still being ingested);
// text terms proxy the iTunes Search API.
func (h Handlers) PostPodcastsSearch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Q string `json:"q"`
	}
	if err := bindJSON(r, &req); err != nil {
		writeRefreshStatus(w, "error", "invalid request")
		return
	}

	term := strings.TrimSpace(req.Q)
	if term == "" {
		writeRefreshOK(w, map[string]any{"search_results": []podcastInfoJSON{}})
		return
	}

	if strings.HasPrefix(term, "http://") || strings.HasPrefix(term, "https://") {
		h.searchByFeedURL(w, r, term)
		return
	}

	results, err := h.Search.Search(r.Context(), term, 20)
	if err != nil {
		slog.Warn("Podcast search failed", "error", err)
		writeRefreshStatus(w, "error", "search unavailable")
		return
	}

	infos := make([]podcastInfoJSON, 0, len(results))
	for _, result := range results {
		podcast, err := h.Queries.CreatePodcastPending(r.Context(), db.CreatePodcastPendingParams{
			Uuid:    crawler.PodcastUUID(result.FeedURL),
			FeedUrl: crawler.CanonicalFeedURL(result.FeedURL),
		})
		if err != nil {
			writeError(w, r, err)
			return
		}
		if podcast.RefreshStatus == "pending" && h.Queue != nil {
			// fill the catalog in the background so the follow-up cache-host
			// full-podcast call succeeds
			if err := h.Queue.EnqueuePodcastRefresh(r.Context(), podcast.Uuid, podcast.FeedUrl); err != nil {
				slog.Warn("Pending podcast enqueue failed", "podcast_uuid", podcast.Uuid, "error", err)
			}
		}

		infos = append(infos, podcastInfoJSON{
			Title:        result.Title,
			UUID:         podcast.Uuid,
			Author:       result.Author,
			CollectionID: result.CollectionID,
			Explicit:     result.Explicit,
		})
	}

	writeRefreshOK(w, map[string]any{"search_results": infos})
}

func (h Handlers) searchByFeedURL(w http.ResponseWriter, r *http.Request, feedURL string) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	podcast, err := h.Crawler.EnsurePodcast(ctx, feedURL)
	if err != nil {
		if errors.Is(err, crawler.ErrRefreshBackoff) {
			// Known-bad feed inside its retry window: report the recorded
			// failure without triggering another outbound fetch.
			writeRefreshStatus(w, "error", "feed could not be loaded")
			return
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			// slow feed: let the client's poll/backoff loop retry while a
			// background crawl finishes
			if h.Queue != nil {
				if enqueueErr := h.Queue.EnqueuePodcastRefresh(r.Context(), "", feedURL); enqueueErr != nil {
					slog.Warn("Feed URL refresh enqueue failed", "feed_url", feedURL, "error", enqueueErr)
				}
			}
			writeRefreshStatus(w, "poll", "")
			return
		}
		slog.Warn("Feed URL search crawl failed", "feed_url", feedURL, "error", err)
		writeRefreshStatus(w, "error", "feed could not be loaded")
		return
	}

	writeRefreshOK(w, map[string]any{
		"podcast": podcastInfoJSON{
			Title:       podcast.Title,
			UUID:        podcast.Uuid,
			Author:      podcast.Author,
			Description: podcast.Description,
			Explicit:    podcast.IsExplicit,
		},
	})
}

// PostPodcastsShow handles POST podcasts/show: resolve an iTunes collection
// id to a catalog podcast.
func (h Handlers) PostPodcastsShow(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if err := bindJSON(r, &req); err != nil || req.ID == 0 {
		writeRefreshStatus(w, "error", "id is required")
		return
	}

	result, err := h.Search.Lookup(r.Context(), req.ID)
	if err != nil || result == nil {
		slog.Warn("Podcast lookup failed", "collection_id", req.ID, "error", err)
		writeRefreshStatus(w, "error", "podcast not found")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	podcast, err := h.Crawler.EnsurePodcast(ctx, result.FeedURL)
	if err != nil {
		slog.Warn("Podcast lookup feed crawl failed", "collection_id", req.ID, "error", err)
		writeRefreshStatus(w, "error", "feed could not be loaded")
		return
	}

	writeRefreshOK(w, map[string]any{
		"podcast": podcastInfoJSON{
			Title:        podcast.Title,
			UUID:         podcast.Uuid,
			Author:       podcast.Author,
			Description:  podcast.Description,
			CollectionID: req.ID,
			Explicit:     podcast.IsExplicit,
		},
	})
}

// PostImportOpml handles POST import/opml. The client sends feed URLs in
// chunks and then polls with poll_uuids; resolution state lives entirely in
// the catalog (uuids are deterministic), so polling is stateless.
func (h Handlers) PostImportOpml(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Urls      []string `json:"urls"`
		PollUuids []string `json:"poll_uuids"`
	}
	if err := bindJSON(r, &req); err != nil {
		writeRefreshStatus(w, "error", "invalid request")
		return
	}

	resolved := []string{}
	poll := []string{}
	failed := 0

	var toCrawl []string
	for _, feedURL := range req.Urls {
		canonical := crawler.CanonicalFeedURL(feedURL)
		if !strings.Contains(canonical, "://") {
			failed++
			continue
		}

		podcast, err := h.Queries.CreatePodcastPending(r.Context(), db.CreatePodcastPendingParams{
			Uuid:    crawler.PodcastUUID(canonical),
			FeedUrl: canonical,
		})
		if err != nil {
			failed++
			continue
		}

		switch podcast.RefreshStatus {
		case "ok":
			resolved = append(resolved, podcast.Uuid)
		case "failed":
			failed++
		default:
			poll = append(poll, podcast.Uuid)
			toCrawl = append(toCrawl, canonical)
		}
	}

	if len(toCrawl) > 0 && h.Queue != nil {
		if err := h.Queue.EnqueueOpmlImport(r.Context(), toCrawl); err != nil {
			writeRefreshStatus(w, "error", "unable to queue import")
			return
		}
	}

	for _, uuid := range req.PollUuids {
		if !uuidPattern.MatchString(uuid) {
			failed++
			continue
		}
		podcast, err := h.Queries.GetPodcastByUUID(r.Context(), uuid)
		if err != nil {
			failed++
			continue
		}
		switch podcast.RefreshStatus {
		case "ok":
			resolved = append(resolved, podcast.Uuid)
		case "failed":
			failed++
		default:
			poll = append(poll, podcast.Uuid)
		}
	}

	writeRefreshOK(w, map[string]any{
		"uuids":      resolved,
		"poll_uuids": poll,
		"failed":     failed,
	})
}

// PostExportFeedUrls handles POST import/export_feed_urls: map catalog uuids
// back to their feed URLs.
func (h Handlers) PostExportFeedUrls(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Uuids []string `json:"uuids"`
	}
	if err := bindJSON(r, &req); err != nil {
		writeRefreshStatus(w, "error", "invalid request")
		return
	}

	valid := make([]string, 0, len(req.Uuids))
	for _, uuid := range req.Uuids {
		if uuidPattern.MatchString(uuid) {
			valid = append(valid, uuid)
		}
	}

	result := map[string]string{}
	if len(valid) > 0 {
		podcasts, err := h.Queries.GetPodcastsByUUIDs(r.Context(), valid)
		if err != nil {
			writeError(w, r, err)
			return
		}
		for _, podcast := range podcasts {
			result[podcast.Uuid] = podcast.FeedUrl
		}
	}

	writeRefreshOK(w, result)
}

func splitCSV(csv string) []string {
	if csv == "" {
		return nil
	}
	return strings.Split(csv, ",")
}
