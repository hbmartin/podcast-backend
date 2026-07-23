package handlers

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/hbmartin/podcast-backend/crawler"
	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/itunes"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
)

// refreshMock extends QuerierMock with stateful catalog behavior.
type refreshMock struct {
	QuerierMock

	podcasts map[string]db.Podcast // by uuid
	episodes map[string][]db.Episode
	byUUID   map[string]db.Episode
}

func newRefreshMock() *refreshMock {
	return &refreshMock{
		podcasts: map[string]db.Podcast{},
		episodes: map[string][]db.Episode{},
		byUUID:   map[string]db.Episode{},
	}
}

func (m *refreshMock) GetPodcastsByUUIDs(ctx context.Context, uuids []string) ([]db.Podcast, error) {
	var out []db.Podcast
	for _, uuid := range uuids {
		if podcast, ok := m.podcasts[uuid]; ok {
			out = append(out, podcast)
		}
	}
	return out, nil
}

func (m *refreshMock) GetPodcastByUUID(ctx context.Context, uuid string) (db.Podcast, error) {
	if podcast, ok := m.podcasts[uuid]; ok {
		return podcast, nil
	}
	return db.Podcast{}, pgx.ErrNoRows
}

func (m *refreshMock) GetPodcastByFeedURL(ctx context.Context, feedURL string) (db.Podcast, error) {
	for _, podcast := range m.podcasts {
		if podcast.FeedUrl == feedURL {
			return podcast, nil
		}
	}
	return db.Podcast{}, pgx.ErrNoRows
}

func (m *refreshMock) CreatePodcastPending(ctx context.Context, arg db.CreatePodcastPendingParams) (db.Podcast, error) {
	if podcast, ok := m.podcasts[arg.Uuid]; ok {
		return podcast, nil
	}
	podcast := db.Podcast{ID: int64(len(m.podcasts) + 1), Uuid: arg.Uuid, FeedUrl: arg.FeedUrl, RefreshStatus: "pending"}
	m.podcasts[arg.Uuid] = podcast
	return podcast, nil
}

func (m *refreshMock) GetEpisodeByUUID(ctx context.Context, uuid string) (db.Episode, error) {
	if episode, ok := m.byUUID[uuid]; ok {
		return episode, nil
	}
	return db.Episode{}, pgx.ErrNoRows
}

func (m *refreshMock) GetEpisodesByUUIDs(ctx context.Context, uuids []string) ([]db.Episode, error) {
	var out []db.Episode
	for _, uuid := range uuids {
		if episode, ok := m.byUUID[uuid]; ok {
			out = append(out, episode)
		}
	}
	return out, nil
}

func (m *refreshMock) GetEpisodesByPodcastID(ctx context.Context, arg db.GetEpisodesByPodcastIDParams) ([]db.Episode, error) {
	rows := m.episodes[podcastUUIDByID(m.podcasts, arg.PodcastID)]
	if int32(len(rows)) > arg.Limit {
		rows = rows[:arg.Limit]
	}
	return rows, nil
}

func (m *refreshMock) GetEpisodesPublishedAfter(ctx context.Context, arg db.GetEpisodesPublishedAfterParams) ([]db.Episode, error) {
	var out []db.Episode
	for _, episode := range m.episodes[podcastUUIDByID(m.podcasts, arg.PodcastID)] {
		if episode.PublishedAt != nil && episode.PublishedAt.After(*arg.PublishedAt) {
			out = append(out, episode)
		}
	}
	return out, nil
}

func podcastUUIDByID(podcasts map[string]db.Podcast, id int64) string {
	for uuid, podcast := range podcasts {
		if podcast.ID == id {
			return uuid
		}
	}
	return ""
}

const testPodcastUUID = "0f0e0d0c-0b0a-4988-8776-655443322110"

func seedCatalog(m *refreshMock) {
	t1 := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2024, 1, 2, 10, 0, 0, 0, time.UTC)
	ep1 := db.Episode{ID: 1, Uuid: "aaaaaaaa-0000-4000-8000-000000000001", PodcastID: 1, Title: "One", AudioUrl: "https://cdn/e1.mp3", PublishedAt: &t1, DurationSecs: 600}
	ep2 := db.Episode{ID: 2, Uuid: "aaaaaaaa-0000-4000-8000-000000000002", PodcastID: 1, Title: "Two", AudioUrl: "https://cdn/e2.mp3", PublishedAt: &t2, DurationSecs: 900}

	m.podcasts[testPodcastUUID] = db.Podcast{ID: 1, Uuid: testPodcastUUID, FeedUrl: "https://example.com/feed.xml", RefreshStatus: "ok", Title: "Test Show"}
	m.episodes[testPodcastUUID] = []db.Episode{ep2, ep1} // newest first
	m.byUUID[ep1.Uuid] = ep1
	m.byUUID[ep2.Uuid] = ep2
}

func refreshRouter(m *refreshMock, searcher itunes.Searcher) *http.ServeMux {
	handlers := Handlers{
		Queries: m,
		Config:  testAuthConfig,
		Search:  searcher,
		Crawler: &crawler.Crawler{DB: m, Fetcher: nil},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /user/update", handlers.PostRefreshUserUpdate)
	mux.HandleFunc("POST /podcasts/search", handlers.PostPodcastsSearch)
	mux.HandleFunc("POST /import/opml", handlers.PostImportOpml)
	mux.HandleFunc("POST /import/export_feed_urls", handlers.PostExportFeedUrls)
	return mux
}

type refreshEnvelope struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Result  struct {
		PodcastUpdates map[string][]refreshEpisodeJSON `json:"podcast_updates"`
		SearchResults  []podcastInfoJSON               `json:"search_results"`
		Uuids          []string                        `json:"uuids"`
		PollUuids      []string                        `json:"poll_uuids"`
		Failed         int                             `json:"failed"`
	} `json:"result"`
}

func TestRefreshUserUpdateNewEpisodes(t *testing.T) {
	m := newRefreshMock()
	seedCatalog(m)
	router := refreshRouter(m, nil)

	// client knows episode One; expects episode Two back
	code, resp, _, err := makeRequest[refreshEnvelope](router, "POST", "/user/update", map[string]string{
		"podcasts":      testPodcastUUID,
		"last_episodes": "aaaaaaaa-0000-4000-8000-000000000001",
	})

	assert.NoError(t, err)
	assert.Equal(t, 200, code)
	assert.Equal(t, "ok", resp.Status)
	updates := resp.Result.PodcastUpdates[testPodcastUUID]
	assert.Len(t, updates, 1)
	assert.Equal(t, "aaaaaaaa-0000-4000-8000-000000000002", updates[0].UUID)
	assert.Equal(t, "https://cdn/e2.mp3", updates[0].URL)
	assert.Equal(t, "2024-01-02 10:00:00", updates[0].PublishedAt)
}

func TestRefreshUserUpdateUpToDate(t *testing.T) {
	m := newRefreshMock()
	seedCatalog(m)
	router := refreshRouter(m, nil)

	code, resp, _, err := makeRequest[refreshEnvelope](router, "POST", "/user/update", map[string]string{
		"podcasts":      testPodcastUUID,
		"last_episodes": "aaaaaaaa-0000-4000-8000-000000000002",
	})

	assert.NoError(t, err)
	assert.Equal(t, 200, code)
	assert.Empty(t, resp.Result.PodcastUpdates)
}

func TestRefreshUserUpdateRejectsCrossPodcastCutoff(t *testing.T) {
	m := newRefreshMock()
	seedCatalog(m)
	otherPodcast := "11111111-2222-4333-8444-555555555555"
	m.podcasts[otherPodcast] = db.Podcast{ID: 2, Uuid: otherPodcast, RefreshStatus: "ok"}
	future := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	foreignEpisode := db.Episode{ID: 3, Uuid: "cccccccc-0000-4000-8000-000000000003", PodcastID: 2, PublishedAt: &future}
	m.byUUID[foreignEpisode.Uuid] = foreignEpisode
	router := refreshRouter(m, nil)

	code, resp, _, err := makeRequest[refreshEnvelope](router, "POST", "/user/update", map[string]string{
		"podcasts": testPodcastUUID, "last_episodes": foreignEpisode.Uuid,
	})

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.Len(t, resp.Result.PodcastUpdates[testPodcastUUID], 2,
		"a cutoff belonging to another podcast must fall back to recent episodes")
}

func TestRefreshUserUpdateUnknownPodcastSkipped(t *testing.T) {
	m := newRefreshMock()
	router := refreshRouter(m, nil)

	code, resp, _, err := makeRequest[refreshEnvelope](router, "POST", "/user/update", map[string]string{
		"podcasts":      "11111111-2222-4333-8444-555555555555",
		"last_episodes": "",
	})

	assert.NoError(t, err)
	assert.Equal(t, 200, code)
	assert.Equal(t, "ok", resp.Status)
	assert.Empty(t, resp.Result.PodcastUpdates)
}

type fakeSearcher struct {
	results []itunes.Result
}

func (s *fakeSearcher) Search(ctx context.Context, term string, limit int) ([]itunes.Result, error) {
	return s.results, nil
}

func (s *fakeSearcher) Lookup(ctx context.Context, id int64) (*itunes.Result, error) {
	if len(s.results) == 0 {
		return nil, nil
	}
	return &s.results[0], nil
}

func TestPodcastsSearchByText(t *testing.T) {
	m := newRefreshMock()
	searcher := &fakeSearcher{results: []itunes.Result{{
		CollectionID: 99, Title: "Found Show", Author: "Author", FeedURL: "https://example.com/found.xml",
	}}}
	router := refreshRouter(m, searcher)

	code, resp, _, err := makeRequest[refreshEnvelope](router, "POST", "/podcasts/search", map[string]string{"q": "found"})

	assert.NoError(t, err)
	assert.Equal(t, 200, code)
	assert.Equal(t, "ok", resp.Status)
	assert.Len(t, resp.Result.SearchResults, 1)
	result := resp.Result.SearchResults[0]
	assert.Equal(t, "Found Show", result.Title)
	assert.Equal(t, crawler.PodcastUUID("https://example.com/found.xml"), result.UUID)
	assert.Equal(t, int64(99), result.CollectionID)

	// lazily inserted into the catalog as pending
	_, ok := m.podcasts[result.UUID]
	assert.True(t, ok)
}

func TestPodcastsSearchByURLReportsBackoffAsError(t *testing.T) {
	m := newRefreshMock()
	feedURL := crawler.CanonicalFeedURL("https://example.com/broken.xml")
	m.podcasts["broken-uuid"] = db.Podcast{
		ID: 9, Uuid: "broken-uuid", FeedUrl: feedURL,
		RefreshStatus: "failed", NextRefreshAt: time.Now().Add(time.Hour),
	}
	// The nil Fetcher would panic on any outbound fetch: the backoff window
	// must be answered from the stored row alone, and as an error rather
	// than an empty success payload.
	router := refreshRouter(m, nil)

	code, resp, _, err := makeRequest[refreshEnvelope](router, "POST", "/podcasts/search",
		map[string]string{"q": "https://example.com/broken.xml"})

	assert.NoError(t, err)
	assert.Equal(t, 200, code)
	assert.Equal(t, "error", resp.Status)
	assert.Equal(t, "feed could not be loaded", resp.Message)
}

func TestImportOpmlChunkAndPoll(t *testing.T) {
	m := newRefreshMock()
	seedCatalog(m)
	router := refreshRouter(m, nil)

	// one known-ok feed (already in catalog via its canonical URL) and one new
	code, resp, _, err := makeRequest[refreshEnvelope](router, "POST", "/import/opml", map[string]any{
		"urls": []string{"https://example.com/feed.xml", "https://example.com/new.xml", "::bad::"},
	})

	assert.NoError(t, err)
	assert.Equal(t, 200, code)
	assert.Equal(t, "ok", resp.Status)

	// the known feed resolves immediately... but only if its uuid matches the
	// deterministic uuid; the seeded catalog row uses a fixed uuid, so the
	// known feed is found via GetPodcastByFeedURL inside CreatePodcastPending?
	// CreatePodcastPending is keyed by uuid: the deterministic uuid differs
	// from the seeded fixture uuid, so this behaves as a new pending feed.
	assert.Equal(t, 1, resp.Result.Failed, "invalid URL counts as failed")
	assert.NotEmpty(t, resp.Result.PollUuids)

	// mark one pending podcast as crawled, then poll
	pollUuid := resp.Result.PollUuids[0]
	podcast := m.podcasts[pollUuid]
	podcast.RefreshStatus = "ok"
	m.podcasts[pollUuid] = podcast

	code, resp, _, err = makeRequest[refreshEnvelope](router, "POST", "/import/opml", map[string]any{
		"poll_uuids": []string{pollUuid},
	})

	assert.NoError(t, err)
	assert.Equal(t, 200, code)
	assert.Contains(t, resp.Result.Uuids, pollUuid)
	assert.Empty(t, resp.Result.PollUuids)
}

func TestExportFeedUrls(t *testing.T) {
	m := newRefreshMock()
	seedCatalog(m)
	router := refreshRouter(m, nil)

	code, resp, _, err := makeRequest[struct {
		Status string            `json:"status"`
		Result map[string]string `json:"result"`
	}](router, "POST", "/import/export_feed_urls", map[string]any{
		"uuids": []string{testPodcastUUID},
	})

	assert.NoError(t, err)
	assert.Equal(t, 200, code)
	assert.Equal(t, "ok", resp.Status)
	assert.Equal(t, "https://example.com/feed.xml", resp.Result[testPodcastUUID])
}
