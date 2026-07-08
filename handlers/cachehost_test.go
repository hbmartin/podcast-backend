package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"goapi-template/db"

	"github.com/stretchr/testify/assert"
)

// cacheMock extends refreshMock with search behavior.
type cacheMock struct {
	refreshMock
}

func (m *cacheMock) SearchPodcasts(ctx context.Context, arg db.SearchPodcastsParams) ([]db.Podcast, error) {
	var out []db.Podcast
	for _, podcast := range m.podcasts {
		if podcast.RefreshStatus == "ok" && containsFold(podcast.Title, *arg.Column1) {
			out = append(out, podcast)
		}
	}
	return out, nil
}

func (m *cacheMock) SearchEpisodesGlobal(ctx context.Context, arg db.SearchEpisodesGlobalParams) ([]db.SearchEpisodesGlobalRow, error) {
	var out []db.SearchEpisodesGlobalRow
	for uuid, episodes := range m.episodes {
		podcast := m.podcasts[uuid]
		for _, episode := range episodes {
			if containsFold(episode.Title, *arg.Column1) {
				out = append(out, db.SearchEpisodesGlobalRow{
					ID: episode.ID, Uuid: episode.Uuid, PodcastID: episode.PodcastID,
					Title: episode.Title, AudioUrl: episode.AudioUrl,
					DurationSecs: episode.DurationSecs, PublishedAt: episode.PublishedAt,
					ParentPodcastUuid: podcast.Uuid, ParentPodcastTitle: podcast.Title,
				})
			}
		}
	}
	return out, nil
}

func (m *cacheMock) SearchEpisodesInPodcast(ctx context.Context, arg db.SearchEpisodesInPodcastParams) ([]db.Episode, error) {
	var out []db.Episode
	for _, episode := range m.episodes[arg.Uuid] {
		if containsFold(episode.Title, *arg.Column2) {
			out = append(out, episode)
		}
	}
	return out, nil
}

func containsFold(haystack string, needle string) bool {
	if needle == "" {
		return true
	}
	h, n := []rune(haystack), []rune(needle)
	for i := 0; i+len(n) <= len(h); i++ {
		match := true
		for j := range n {
			a, b := h[i+j], n[j]
			if a >= 'A' && a <= 'Z' {
				a += 32
			}
			if b >= 'A' && b <= 'Z' {
				b += 32
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func cacheRouter(m *cacheMock) *http.ServeMux {
	handlers := Handlers{Queries: m, Config: testAuthConfig}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /mobile/podcast/full/{uuid}", handlers.GetPodcastFull)
	mux.HandleFunc("GET /mobile/show_notes/full/{uuid}", handlers.GetShowNotesFull)
	mux.HandleFunc("GET /mobile/episode/url/{podcastUuid}/{episodeUuid}", handlers.GetEpisodeURL)
	mux.HandleFunc("GET /mobile/podcast/findbyepisode/{podcastUuid}/{episodeUuid}", handlers.GetFindByEpisode)
	mux.HandleFunc("POST /mobile/podcast/episode/search", handlers.PostEpisodeSearchInPodcast)
	mux.HandleFunc("POST /episode/search", handlers.PostEpisodeSearch)
	mux.HandleFunc("POST /search/combined", handlers.PostCombinedSearch)
	mux.HandleFunc("GET /autocomplete/search", handlers.GetAutocompleteSearch)
	return mux
}

func newCacheMock() *cacheMock {
	m := &cacheMock{refreshMock: *newRefreshMock()}
	seedCatalog(&m.refreshMock)
	// seedCatalog leaves ContentModifiedMs zero; set it for cache validators
	podcast := m.podcasts[testPodcastUUID]
	podcast.ContentModifiedMs = 1704189600000 // 2024-01-02T10:00:00Z
	m.podcasts[testPodcastUUID] = podcast
	return m
}

type podcastFullEnvelope struct {
	Podcast struct {
		UUID     string `json:"uuid"`
		Title    string `json:"title"`
		Episodes []struct {
			UUID      string  `json:"uuid"`
			URL       string  `json:"url"`
			Duration  float64 `json:"duration"`
			Published string  `json:"published"`
			ShowNotes string  `json:"show_notes"`
		} `json:"episodes"`
	} `json:"podcast"`
}

func TestGetPodcastFull(t *testing.T) {
	m := newCacheMock()
	router := cacheRouter(m)

	code, resp, headers, err := makeRequest[podcastFullEnvelope](router, "GET", "/mobile/podcast/full/"+testPodcastUUID, nil)

	assert.NoError(t, err)
	assert.Equal(t, 200, code)
	assert.Equal(t, testPodcastUUID, resp.Podcast.UUID)
	assert.Equal(t, "Test Show", resp.Podcast.Title)
	assert.Len(t, resp.Podcast.Episodes, 2)
	assert.Equal(t, "2024-01-02T10:00:00Z", resp.Podcast.Episodes[0].Published)
	assert.NotEmpty(t, headers.Get("ETag"))
}

func TestGetPodcastFullConditional(t *testing.T) {
	m := newCacheMock()
	router := cacheRouter(m)

	// first request to learn the ETag
	req, _ := http.NewRequest("GET", "/mobile/podcast/full/"+testPodcastUUID, nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	etag := rr.Result().Header.Get("ETag")
	assert.Equal(t, 200, rr.Code)
	assert.NotEmpty(t, etag)

	// matching If-None-Match -> 304
	req, _ = http.NewRequest("GET", "/mobile/podcast/full/"+testPodcastUUID, nil)
	req.Header.Set("If-None-Match", etag)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusNotModified, rr.Code)

	// matching If-Modified-Since -> 304
	req, _ = http.NewRequest("GET", "/mobile/podcast/full/"+testPodcastUUID, nil)
	req.Header.Set("If-Modified-Since", rr.Result().Header.Get("Last-Modified"))
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusNotModified, rr.Code)
}

func TestGetPodcastFullUnknown(t *testing.T) {
	m := newCacheMock()
	router := cacheRouter(m)

	code, _, _, _ := makeRequest[string](router, "GET", "/mobile/podcast/full/99999999-9999-4999-8999-999999999999", nil)
	assert.Equal(t, http.StatusNotFound, code)
}

func TestGetPodcastFullPendingReturns404(t *testing.T) {
	m := newCacheMock()
	podcast := m.podcasts[testPodcastUUID]
	podcast.RefreshStatus = "pending"
	m.podcasts[testPodcastUUID] = podcast
	router := cacheRouter(m)

	code, _, _, _ := makeRequest[string](router, "GET", "/mobile/podcast/full/"+testPodcastUUID, nil)
	assert.Equal(t, http.StatusNotFound, code)
}

func TestGetShowNotes(t *testing.T) {
	m := newCacheMock()
	episodes := m.episodes[testPodcastUUID]
	episodes[0].ShowNotes = "<p>notes two</p>"
	m.episodes[testPodcastUUID] = episodes
	router := cacheRouter(m)

	code, resp, _, err := makeRequest[podcastFullEnvelope](router, "GET", "/mobile/show_notes/full/"+testPodcastUUID, nil)

	assert.NoError(t, err)
	assert.Equal(t, 200, code)
	assert.Len(t, resp.Podcast.Episodes, 2)
	assert.Equal(t, "<p>notes two</p>", resp.Podcast.Episodes[0].ShowNotes)
}

func TestGetEpisodeURL(t *testing.T) {
	m := newCacheMock()
	router := cacheRouter(m)

	req, _ := http.NewRequest("GET", "/mobile/episode/url/"+testPodcastUUID+"/aaaaaaaa-0000-4000-8000-000000000002", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, 200, rr.Code)
	assert.Equal(t, "https://cdn/e2.mp3", rr.Body.String())
	assert.Contains(t, rr.Result().Header.Get("Content-Type"), "text/plain")

	req, _ = http.NewRequest("GET", "/mobile/episode/url/"+testPodcastUUID+"/99999999-9999-4999-8999-999999999999", nil)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestFindByEpisode(t *testing.T) {
	m := newCacheMock()
	router := cacheRouter(m)

	code, resp, _, err := makeRequest[podcastFullEnvelope](router, "GET",
		"/mobile/podcast/findbyepisode/"+testPodcastUUID+"/aaaaaaaa-0000-4000-8000-000000000001", nil)

	assert.NoError(t, err)
	assert.Equal(t, 200, code)
	assert.Equal(t, testPodcastUUID, resp.Podcast.UUID)
	assert.Len(t, resp.Podcast.Episodes, 1)
	assert.Equal(t, "aaaaaaaa-0000-4000-8000-000000000001", resp.Podcast.Episodes[0].UUID)
}

func TestEpisodeSearchInPodcast(t *testing.T) {
	m := newCacheMock()
	router := cacheRouter(m)

	code, resp, _, err := makeRequest[struct {
		Episodes []struct {
			UUID string `json:"uuid"`
		} `json:"episodes"`
	}](router, "POST", "/mobile/podcast/episode/search", map[string]string{
		"podcastuuid": testPodcastUUID,
		"searchterm":  "two",
	})

	assert.NoError(t, err)
	assert.Equal(t, 200, code)
	assert.Len(t, resp.Episodes, 1)
	assert.Equal(t, "aaaaaaaa-0000-4000-8000-000000000002", resp.Episodes[0].UUID)
}

func TestGlobalEpisodeSearch(t *testing.T) {
	m := newCacheMock()
	router := cacheRouter(m)

	code, resp, _, err := makeRequest[struct {
		Episodes []episodeSearchResultJSON `json:"episodes"`
	}](router, "POST", "/episode/search", map[string]string{"term": "one"})

	assert.NoError(t, err)
	assert.Equal(t, 200, code)
	assert.Len(t, resp.Episodes, 1)
	assert.Equal(t, "One", resp.Episodes[0].Title)
	assert.Equal(t, testPodcastUUID, resp.Episodes[0].PodcastUuid)
	assert.Equal(t, "Test Show", resp.Episodes[0].PodcastTitle)
	assert.Equal(t, "2024-01-01T10:00:00Z", resp.Episodes[0].PublishedDate)
}

func TestCombinedSearch(t *testing.T) {
	m := newCacheMock()
	router := cacheRouter(m)

	code, resp, _, err := makeRequest[struct {
		Results []combinedSearchResultJSON `json:"results"`
	}](router, "POST", "/search/combined", map[string]string{"term": "test"})

	assert.NoError(t, err)
	assert.Equal(t, 200, code)

	var types []string
	for _, result := range resp.Results {
		types = append(types, result.Type)
	}
	assert.Contains(t, types, "podcast")
}

func TestAutocomplete(t *testing.T) {
	m := newCacheMock()
	router := cacheRouter(m)

	code, resp, _, err := makeRequest[struct {
		Results []struct {
			Type  string `json:"type"`
			Value any    `json:"value"`
		} `json:"results"`
	}](router, "GET", "/autocomplete/search?q=Test", nil)

	assert.NoError(t, err)
	assert.Equal(t, 200, code)
	assert.GreaterOrEqual(t, len(resp.Results), 2)
	assert.Equal(t, "term", resp.Results[0].Type)
	assert.Equal(t, "Test", resp.Results[0].Value)
	assert.Equal(t, "podcast", resp.Results[1].Type)
}
