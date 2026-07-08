package handlers

import (
	"net/http"
	"net/url"
	"time"

	"github.com/hbmartin/podcast-backend/db"
)

// discoverDateFormat matches the datetime fields in discover payloads.
const discoverDateFormat = "20060102150405"

// discoverPodcastJSON mirrors the client's DiscoverPodcast CodingKeys.
type discoverPodcastJSON struct {
	Title       string `json:"title"`
	Author      string `json:"author"`
	Description string `json:"description,omitempty"`
	UUID        string `json:"uuid"`
	Website     string `json:"website,omitempty"`
	Itunes      string `json:"itunes,omitempty"`
	Explicit    bool   `json:"explicit"`
}

type discoverItemJSON struct {
	ID               string   `json:"id"`
	Title            string   `json:"title"`
	Type             string   `json:"type"`
	SummaryStyle     string   `json:"summary_style,omitempty"`
	ExpandedStyle    string   `json:"expanded_style,omitempty"`
	SummaryItemCount int      `json:"summary_item_count,omitempty"`
	Source           string   `json:"source"`
	Regions          []string `json:"regions"`
}

type discoverRegionJSON struct {
	Name string `json:"name"`
	Code string `json:"code"`
	Flag string `json:"flag"`
}

func podcastToDiscoverJSON(p db.Podcast) discoverPodcastJSON {
	return discoverPodcastJSON{
		Title:       p.Title,
		Author:      p.Author,
		Description: p.Description,
		UUID:        p.Uuid,
		Website:     p.WebsiteUrl,
		Explicit:    p.IsExplicit,
	}
}

// GetDiscoverContent serves GET /discover/ios/content_v2.json and
// content_v3.json: the discover tab layout, with source URLs pointing back
// at this server.
func (h Handlers) GetDiscoverContent(w http.ResponseWriter, r *http.Request) {
	base := h.baseURL(r)
	regions := []string{"us"}

	layout := []discoverItemJSON{
		{ID: "featured", Title: "Featured", Type: "podcast_list", SummaryStyle: "carousel",
			ExpandedStyle: "plain_list", Source: base + "/discover/json/popular", Regions: regions},
		{ID: "trending", Title: "Trending", Type: "podcast_list", SummaryStyle: "small_list",
			ExpandedStyle: "descriptive_list", Source: base + "/discover/json/trending", Regions: regions},
		{ID: "popular", Title: "Popular", Type: "podcast_list", SummaryStyle: "large_list",
			ExpandedStyle: "plain_list", Source: base + "/discover/json/popular", Regions: regions},
		{ID: "recent", Title: "New Episodes", Type: "podcast_list", SummaryStyle: "small_list",
			ExpandedStyle: "descriptive_list", Source: base + "/discover/json/recent", Regions: regions},
		{ID: "categories", Title: "Browse by Category", Type: "categories", SummaryStyle: "pills",
			Source: base + "/discover/json/categories", Regions: regions},
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"layout": layout,
		"regions": map[string]discoverRegionJSON{
			"us": {Name: "United States", Code: "us", Flag: "us"},
		},
		"region_code_token":   "[regionCode]",
		"region_name_token":   "[regionName]",
		"default_region_code": "us",
	})
}

func (h Handlers) writePodcastList(w http.ResponseWriter, r *http.Request, title string, podcasts []db.Podcast) {
	items := make([]discoverPodcastJSON, 0, len(podcasts))
	for _, podcast := range podcasts {
		items = append(items, podcastToDiscoverJSON(podcast))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"title":       title,
		"description": "",
		"podcasts":    items,
		"datetime":    time.Now().UTC().Format(discoverDateFormat),
	})
}

// GetDiscoverTrending handles GET /discover/json/trending.
func (h Handlers) GetDiscoverTrending(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Queries.TopPodcastsBySubscribers(r.Context(), 20)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.writePodcastList(w, r, "Trending", topRowsToPodcasts(rows))
}

// GetDiscoverPopular handles GET /discover/json/popular.
func (h Handlers) GetDiscoverPopular(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Queries.TopPodcastsBySubscribers(r.Context(), 50)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.writePodcastList(w, r, "Popular", topRowsToPodcasts(rows))
}

// GetDiscoverRecent handles GET /discover/json/recent.
func (h Handlers) GetDiscoverRecent(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Queries.RecentPodcasts(r.Context(), 20)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.writePodcastList(w, r, "New Episodes", rows)
}

func topRowsToPodcasts(rows []db.TopPodcastsBySubscribersRow) []db.Podcast {
	podcasts := make([]db.Podcast, 0, len(rows))
	for _, row := range rows {
		podcasts = append(podcasts, db.Podcast{
			ID: row.ID, Uuid: row.Uuid, FeedUrl: row.FeedUrl, Title: row.Title,
			Author: row.Author, Description: row.Description, ImageUrl: row.ImageUrl,
			WebsiteUrl: row.WebsiteUrl, Category: row.Category, IsExplicit: row.IsExplicit,
		})
	}
	return podcasts
}

// GetDiscoverCategories handles GET /discover/json/categories.
func (h Handlers) GetDiscoverCategories(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Queries.DistinctCategories(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}

	base := h.baseURL(r)
	type categoryJSON struct {
		ID     int    `json:"id"`
		Name   string `json:"name"`
		Source string `json:"source"`
	}
	categories := make([]categoryJSON, 0, len(rows))
	for i, row := range rows {
		categories = append(categories, categoryJSON{
			ID:     i + 1,
			Name:   row.Category,
			Source: base + "/discover/json/category/" + url.PathEscape(row.Category),
		})
	}
	writeJSON(w, http.StatusOK, categories)
}

// GetDiscoverCategory handles GET /discover/json/category/{name}: the
// DiscoverCategoryDetails payload for one category.
func (h Handlers) GetDiscoverCategory(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	rows, err := h.Queries.PodcastsByCategory(r.Context(), db.PodcastsByCategoryParams{Category: name, Limit: 50})
	if err != nil {
		writeError(w, r, err)
		return
	}

	items := make([]discoverPodcastJSON, 0, len(rows))
	for _, podcast := range rows {
		items = append(items, podcastToDiscoverJSON(podcast))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"title":       name,
		"description": "",
		"podcasts":    items,
	})
}
