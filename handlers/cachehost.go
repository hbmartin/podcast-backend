package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/hbmartin/podcast-backend/crawler"
	"github.com/hbmartin/podcast-backend/db"

	"github.com/jackc/pgx/v5"
)

// isoDateFormat matches the ISO8601 format the client's cache-host decoders
// use ("yyyy-MM-dd'T'HH:mm:ss'Z'").
const isoDateFormat = "2006-01-02T15:04:05Z"

// podcastFullEpisodeJSON is one episode inside mobile/podcast/full — keys per
// Episode+fromDictionary.swift.
type podcastFullEpisodeJSON struct {
	UUID      string  `json:"uuid"`
	Title     string  `json:"title"`
	URL       string  `json:"url"`
	FileType  string  `json:"file_type,omitempty"`
	FileSize  int64   `json:"file_size,omitempty"`
	Duration  float64 `json:"duration,omitempty"`
	Published string  `json:"published,omitempty"`
	Number    int64   `json:"number,omitempty"`
	Season    int64   `json:"season,omitempty"`
	Type      string  `json:"type,omitempty"`
}

// podcastFullJSON is the "podcast" object — keys per
// Podcast+fromDictionary.swift.
type podcastFullJSON struct {
	UUID            string                   `json:"uuid"`
	Title           string                   `json:"title"`
	Author          string                   `json:"author"`
	URL             string                   `json:"url"`
	Description     string                   `json:"description"`
	DescriptionHTML string                   `json:"description_html,omitempty"`
	Category        string                   `json:"category,omitempty"`
	ShowType        string                   `json:"show_type,omitempty"`
	Explicit        bool                     `json:"explicit"`
	Episodes        []podcastFullEpisodeJSON `json:"episodes"`
}

// serveCached writes payload with cache validators derived from the
// podcast's content_modified_ms and replies 304 to matching conditional
// requests.
func serveCached(w http.ResponseWriter, r *http.Request, contentModifiedMs int64, payload any) {
	etag := fmt.Sprintf(`"%d"`, contentModifiedMs)
	lastModified := time.UnixMilli(contentModifiedMs).UTC().Format(http.TimeFormat)

	w.Header().Set("ETag", etag)
	w.Header().Set("Last-Modified", lastModified)

	if match := r.Header.Get("If-None-Match"); match != "" && match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if since := r.Header.Get("If-Modified-Since"); since != "" {
		if sinceTime, err := http.ParseTime(since); err == nil && !time.UnixMilli(contentModifiedMs).UTC().Truncate(time.Second).After(sinceTime) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}

	writeJSON(w, http.StatusOK, payload)
}

// loadCrawledPodcast fetches a catalog podcast for the cache-host endpoints.
// Pending podcasts get a background crawl kick and a 404 so the client's
// retry loop finds them once ingested.
func (h Handlers) loadCrawledPodcast(w http.ResponseWriter, r *http.Request, uuid string) (db.Podcast, bool) {
	podcast, err := h.Queries.GetPodcastByUUID(r.Context(), uuid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			w.WriteHeader(http.StatusNotFound)
			return db.Podcast{}, false
		}
		writeError(w, r, err)
		return db.Podcast{}, false
	}

	if podcast.RefreshStatus != "ok" {
		if h.Queue != nil {
			if err := h.Queue.EnqueuePodcastRefresh(r.Context(), podcast.Uuid, podcast.FeedUrl); err != nil {
				slog.Warn("Pending cache-host podcast enqueue failed", "podcast_uuid", podcast.Uuid, "error", err)
			}
		}
		w.WriteHeader(http.StatusNotFound)
		return db.Podcast{}, false
	}

	return podcast, true
}

const allEpisodesLimit = 10000

// GetPodcastFull handles GET mobile/podcast/full/{uuid}.
func (h Handlers) GetPodcastFull(w http.ResponseWriter, r *http.Request) {
	podcast, ok := h.loadCrawledPodcast(w, r, r.PathValue("uuid"))
	if !ok {
		return
	}

	episodes, err := h.Queries.GetEpisodesByPodcastID(r.Context(), db.GetEpisodesByPodcastIDParams{PodcastID: podcast.ID, Limit: allEpisodesLimit})
	if err != nil {
		writeError(w, r, err)
		return
	}

	payload := podcastFullJSON{
		UUID:        podcast.Uuid,
		Title:       podcast.Title,
		Author:      podcast.Author,
		URL:         podcast.WebsiteUrl,
		Description: podcast.Description,
		Category:    podcast.Category,
		ShowType:    podcast.ShowType,
		Explicit:    podcast.IsExplicit,
		Episodes:    make([]podcastFullEpisodeJSON, 0, len(episodes)),
	}
	for _, episode := range episodes {
		payload.Episodes = append(payload.Episodes, episodeToFullJSON(episode))
	}

	serveCached(w, r, podcast.ContentModifiedMs, map[string]any{"podcast": payload})
}

func episodeToFullJSON(episode db.Episode) podcastFullEpisodeJSON {
	out := podcastFullEpisodeJSON{
		UUID:     episode.Uuid,
		Title:    episode.Title,
		URL:      episode.AudioUrl,
		FileType: episode.FileType,
		FileSize: episode.FileSize,
		Duration: float64(episode.DurationSecs),
		Number:   int64(episode.Number),
		Season:   int64(episode.Season),
		Type:     episode.EpisodeType,
	}
	if episode.PublishedAt != nil {
		out.Published = episode.PublishedAt.UTC().Format(isoDateFormat)
	}
	return out
}

// GetShowNotesFull handles GET mobile/show_notes/full/{uuid}.
func (h Handlers) GetShowNotesFull(w http.ResponseWriter, r *http.Request) {
	podcast, ok := h.loadCrawledPodcast(w, r, r.PathValue("uuid"))
	if !ok {
		return
	}

	episodes, err := h.Queries.GetEpisodesByPodcastID(r.Context(), db.GetEpisodesByPodcastIDParams{PodcastID: podcast.ID, Limit: allEpisodesLimit})
	if err != nil {
		writeError(w, r, err)
		return
	}

	// Episode.Metadata as the client decodes it (convertFromSnakeCase):
	// `transcripts` is the one non-optional field, so it must always be
	// present -- even empty -- or the whole per-episode decode fails.
	type showNotesEpisode struct {
		UUID        string               `json:"uuid"`
		ShowNotes   string               `json:"show_notes"`
		Image       string               `json:"image,omitempty"`
		Transcripts []crawler.Transcript `json:"transcripts"`
		ChaptersURL string               `json:"chapters_url,omitempty"`
	}
	notes := make([]showNotesEpisode, 0, len(episodes))
	for _, episode := range episodes {
		transcripts := []crawler.Transcript{}
		if len(episode.Transcripts) > 0 {
			if err := json.Unmarshal(episode.Transcripts, &transcripts); err != nil {
				slog.Warn("show_notes: undecodable transcripts column", "episode", episode.Uuid, "error", err)
				transcripts = []crawler.Transcript{}
			}
			if transcripts == nil {
				// a literal JSON null would serialize back as null, which the
				// client's non-optional transcripts field rejects
				transcripts = []crawler.Transcript{}
			}
		}
		notes = append(notes, showNotesEpisode{
			UUID:        episode.Uuid,
			ShowNotes:   episode.ShowNotes,
			Image:       episode.ImageUrl,
			Transcripts: transcripts,
			ChaptersURL: episode.ChaptersUrl,
		})
	}

	serveCached(w, r, podcast.ContentModifiedMs, map[string]any{
		"podcast": map[string]any{
			"uuid":     podcast.Uuid,
			"episodes": notes,
		},
	})
}

// GetEpisodeURL handles GET mobile/episode/url/{podcastUuid}/{episodeUuid}:
// plain-text playable URL lookup.
func (h Handlers) GetEpisodeURL(w http.ResponseWriter, r *http.Request) {
	episode, err := h.Queries.GetEpisodeByUUID(r.Context(), r.PathValue("episodeUuid"))
	if err != nil || episode.AudioUrl == "" {
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(episode.AudioUrl))
}

// GetFindByEpisode handles GET mobile/podcast/findbyepisode/{podcastUuid}/{episodeUuid}:
// the podcast/full envelope scoped to a single episode, used when the client
// adds a podcast from a shared episode link.
func (h Handlers) GetFindByEpisode(w http.ResponseWriter, r *http.Request) {
	podcast, ok := h.loadCrawledPodcast(w, r, r.PathValue("podcastUuid"))
	if !ok {
		return
	}

	episode, err := h.Queries.GetEpisodeByUUID(r.Context(), r.PathValue("episodeUuid"))
	if err != nil || episode.PodcastID != podcast.ID {
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		return
	}

	payload := podcastFullJSON{
		UUID:        podcast.Uuid,
		Title:       podcast.Title,
		Author:      podcast.Author,
		URL:         podcast.WebsiteUrl,
		Description: podcast.Description,
		Category:    podcast.Category,
		ShowType:    podcast.ShowType,
		Explicit:    podcast.IsExplicit,
		Episodes:    []podcastFullEpisodeJSON{episodeToFullJSON(episode)},
	}

	serveCached(w, r, podcast.ContentModifiedMs, map[string]any{"podcast": payload})
}

// PostEpisodeSearchInPodcast handles POST mobile/podcast/episode/search:
// find episode uuids within one podcast.
func (h Handlers) PostEpisodeSearchInPodcast(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PodcastUuid string `json:"podcastuuid"`
		SearchTerm  string `json:"searchterm"`
	}
	if err := bindJSON(r, &req); err != nil || req.PodcastUuid == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	episodes, err := h.Queries.SearchEpisodesInPodcast(r.Context(), db.SearchEpisodesInPodcastParams{
		Uuid:    req.PodcastUuid,
		Column2: &req.SearchTerm,
		Limit:   100,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}

	type match struct {
		UUID string `json:"uuid"`
	}
	matches := make([]match, 0, len(episodes))
	for _, episode := range episodes {
		matches = append(matches, match{UUID: episode.Uuid})
	}
	writeJSON(w, http.StatusOK, map[string]any{"episodes": matches})
}

// episodeSearchResultJSON matches EpisodeSearchEnvelope (snake_case keys,
// ISO8601 Z dates).
type episodeSearchResultJSON struct {
	UUID          string  `json:"uuid"`
	Title         string  `json:"title"`
	PublishedDate string  `json:"published_date"`
	Duration      float64 `json:"duration"`
	PodcastUuid   string  `json:"podcast_uuid"`
	PodcastTitle  string  `json:"podcast_title"`
}

// PostEpisodeSearch handles POST episode/search: global episode search over
// the catalog.
func (h Handlers) PostEpisodeSearch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Term string `json:"term"`
	}
	if err := bindJSON(r, &req); err != nil || req.Term == "" {
		writeJSON(w, http.StatusOK, map[string]any{"episodes": []episodeSearchResultJSON{}})
		return
	}

	rows, err := h.Queries.SearchEpisodesGlobal(r.Context(), db.SearchEpisodesGlobalParams{Column1: &req.Term, Limit: 30})
	if err != nil {
		writeError(w, r, err)
		return
	}

	episodes := make([]episodeSearchResultJSON, 0, len(rows))
	for _, row := range rows {
		episode := episodeSearchResultJSON{
			UUID:         row.Uuid,
			Title:        row.Title,
			Duration:     float64(row.DurationSecs),
			PodcastUuid:  row.ParentPodcastUuid,
			PodcastTitle: row.ParentPodcastTitle,
		}
		if row.PublishedAt != nil {
			episode.PublishedDate = row.PublishedAt.UTC().Format(isoDateFormat)
		}
		episodes = append(episodes, episode)
	}

	writeJSON(w, http.StatusOK, map[string]any{"episodes": episodes})
}

// combinedSearchResultJSON matches CombinedSearchResult (snake_case keys).
type combinedSearchResultJSON struct {
	Type          string  `json:"type"`
	UUID          string  `json:"uuid"`
	Title         string  `json:"title"`
	PublishedDate string  `json:"published_date,omitempty"`
	Duration      float64 `json:"duration,omitempty"`
	PodcastUuid   string  `json:"podcast_uuid,omitempty"`
	PodcastTitle  string  `json:"podcast_title,omitempty"`
	Author        string  `json:"author,omitempty"`
	Explicit      bool    `json:"explicit,omitempty"`
}

// PostCombinedSearch handles POST search/combined: podcasts and episodes in
// one response.
func (h Handlers) PostCombinedSearch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Term string `json:"term"`
	}
	if err := bindJSON(r, &req); err != nil || req.Term == "" {
		writeJSON(w, http.StatusOK, map[string]any{"results": []combinedSearchResultJSON{}})
		return
	}

	results := []combinedSearchResultJSON{}

	podcasts, err := h.Queries.SearchPodcasts(r.Context(), db.SearchPodcastsParams{Column1: &req.Term, Limit: 10})
	if err != nil {
		writeError(w, r, err)
		return
	}
	for _, podcast := range podcasts {
		results = append(results, combinedSearchResultJSON{
			Type:     "podcast",
			UUID:     podcast.Uuid,
			Title:    podcast.Title,
			Author:   podcast.Author,
			Explicit: podcast.IsExplicit,
		})
	}

	episodes, err := h.Queries.SearchEpisodesGlobal(r.Context(), db.SearchEpisodesGlobalParams{Column1: &req.Term, Limit: 20})
	if err != nil {
		writeError(w, r, err)
		return
	}
	for _, row := range episodes {
		result := combinedSearchResultJSON{
			Type:         "episode",
			UUID:         row.Uuid,
			Title:        row.Title,
			Duration:     float64(row.DurationSecs),
			PodcastUuid:  row.ParentPodcastUuid,
			PodcastTitle: row.ParentPodcastTitle,
		}
		if row.PublishedAt != nil {
			result.PublishedDate = row.PublishedAt.UTC().Format(isoDateFormat)
		}
		results = append(results, result)
	}

	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// GetAutocompleteSearch handles GET autocomplete/search?q=: predictive
// results (the raw term plus matching catalog podcasts).
func (h Handlers) GetAutocompleteSearch(w http.ResponseWriter, r *http.Request) {
	term := r.URL.Query().Get("q")

	type result struct {
		Type  string `json:"type"`
		Value any    `json:"value"`
	}
	results := []result{}

	if term != "" {
		results = append(results, result{Type: "term", Value: term})

		podcasts, err := h.Queries.SearchPodcasts(r.Context(), db.SearchPodcastsParams{Column1: &term, Limit: 5})
		if err != nil {
			writeError(w, r, err)
			return
		}
		for _, podcast := range podcasts {
			results = append(results, result{Type: "podcast", Value: map[string]any{
				"uuid":     podcast.Uuid,
				"title":    podcast.Title,
				"author":   podcast.Author,
				"explicit": podcast.IsExplicit,
			}})
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}
