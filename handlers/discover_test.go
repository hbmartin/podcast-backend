package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hbmartin/podcast-backend/db"

	"github.com/stretchr/testify/assert"
)

type discoverMock struct {
	refreshMock
}

func (m *discoverMock) TopPodcastsBySubscribers(ctx context.Context, limit int32) ([]db.TopPodcastsBySubscribersRow, error) {
	var out []db.TopPodcastsBySubscribersRow
	for _, podcast := range m.podcasts {
		if podcast.RefreshStatus == "ok" {
			out = append(out, db.TopPodcastsBySubscribersRow{
				ID: podcast.ID, Uuid: podcast.Uuid, Title: podcast.Title,
				Author: podcast.Author, WebsiteUrl: podcast.WebsiteUrl,
				SubscriberCount: 3,
			})
		}
	}
	return out, nil
}

func (m *discoverMock) RecentPodcasts(ctx context.Context, limit int32) ([]db.Podcast, error) {
	var out []db.Podcast
	for _, podcast := range m.podcasts {
		if podcast.RefreshStatus == "ok" {
			out = append(out, podcast)
		}
	}
	return out, nil
}

func (m *discoverMock) DistinctCategories(ctx context.Context) ([]db.DistinctCategoriesRow, error) {
	return []db.DistinctCategoriesRow{
		{Category: "Technology", PodcastCount: 5},
		{Category: "News & Politics", PodcastCount: 2},
	}, nil
}

func (m *discoverMock) PodcastsByCategory(ctx context.Context, arg db.PodcastsByCategoryParams) ([]db.Podcast, error) {
	var out []db.Podcast
	for _, podcast := range m.podcasts {
		if podcast.Category == arg.Category {
			out = append(out, podcast)
		}
	}
	return out, nil
}

func discoverRouter(m *discoverMock) *http.ServeMux {
	handlers := Handlers{Queries: m, Config: testAuthConfig}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /discover/ios/content_v2.json", handlers.GetDiscoverContent)
	mux.HandleFunc("GET /discover/ios/content_v3.json", handlers.GetDiscoverContent)
	mux.HandleFunc("GET /discover/json/trending", handlers.GetDiscoverTrending)
	mux.HandleFunc("GET /discover/json/popular", handlers.GetDiscoverPopular)
	mux.HandleFunc("GET /discover/json/recent", handlers.GetDiscoverRecent)
	mux.HandleFunc("GET /discover/json/categories", handlers.GetDiscoverCategories)
	mux.HandleFunc("GET /discover/json/category/{name}", handlers.GetDiscoverCategory)
	return mux
}

func newDiscoverMock() *discoverMock {
	m := &discoverMock{refreshMock: *newRefreshMock()}
	seedCatalog(&m.refreshMock)
	podcast := m.podcasts[testPodcastUUID]
	podcast.Category = "Technology"
	m.podcasts[testPodcastUUID] = podcast
	return m
}

type layoutEnvelope struct {
	Layout []struct {
		ID      string   `json:"id"`
		Type    string   `json:"type"`
		Source  string   `json:"source"`
		Regions []string `json:"regions"`
	} `json:"layout"`
	Regions           map[string]struct{ Code string } `json:"regions"`
	RegionCodeToken   string                           `json:"region_code_token"`
	RegionNameToken   string                           `json:"region_name_token"`
	DefaultRegionCode string                           `json:"default_region_code"`
}

func TestDiscoverLayout(t *testing.T) {
	router := discoverRouter(newDiscoverMock())

	for _, path := range []string{"/discover/ios/content_v2.json", "/discover/ios/content_v3.json"} {
		req := httptest.NewRequest("GET", path, nil) // sets Host example.com
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		assert.Equal(t, 200, rr.Code)

		var resp layoutEnvelope
		assert.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
		assert.NotEmpty(t, resp.Layout)
		assert.Equal(t, "[regionCode]", resp.RegionCodeToken)
		assert.Equal(t, "[regionName]", resp.RegionNameToken)
		assert.Equal(t, "us", resp.DefaultRegionCode)
		assert.Contains(t, resp.Regions, "us")

		for _, item := range resp.Layout {
			assert.NotEmpty(t, item.Regions, "regions array is required by the client")
			assert.Contains(t, item.Source, "http://example.com/", "sources are absolute against the request host")
		}
	}
}

type podcastListEnvelope struct {
	Title    string                `json:"title"`
	Podcasts []discoverPodcastJSON `json:"podcasts"`
	Datetime string                `json:"datetime"`
}

func TestDiscoverSources(t *testing.T) {
	router := discoverRouter(newDiscoverMock())

	for _, path := range []string{"/discover/json/trending", "/discover/json/popular", "/discover/json/recent"} {
		code, resp, _, err := makeRequest[podcastListEnvelope](router, "GET", path, nil)

		assert.NoError(t, err)
		assert.Equal(t, 200, code)
		assert.Len(t, resp.Podcasts, 1, path)
		assert.Equal(t, testPodcastUUID, resp.Podcasts[0].UUID)
		assert.NotEmpty(t, resp.Datetime)
	}
}

func TestDiscoverCategories(t *testing.T) {
	router := discoverRouter(newDiscoverMock())

	code, resp, _, err := makeRequest[[]struct {
		ID     int    `json:"id"`
		Name   string `json:"name"`
		Source string `json:"source"`
	}](router, "GET", "/discover/json/categories", nil)

	assert.NoError(t, err)
	assert.Equal(t, 200, code)
	assert.Len(t, *resp, 2)
	assert.Equal(t, 1, (*resp)[0].ID)
	assert.Equal(t, "Technology", (*resp)[0].Name)
	assert.Contains(t, (*resp)[1].Source, "category/News%20&%20Politics")

	// category details
	code, details, _, err := makeRequest[podcastListEnvelope](router, "GET", "/discover/json/category/Technology", nil)
	assert.NoError(t, err)
	assert.Equal(t, 200, code)
	assert.Equal(t, "Technology", details.Title)
	assert.Len(t, details.Podcasts, 1)
}
