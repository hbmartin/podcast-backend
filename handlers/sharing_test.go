package handlers

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hbmartin/podcast-backend/config"
	"github.com/hbmartin/podcast-backend/db"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type sharingMock struct {
	refreshMock

	lists map[string]db.SharedList
}

func newSharingMock() *sharingMock {
	m := &sharingMock{refreshMock: *newRefreshMock(), lists: map[string]db.SharedList{}}
	seedCatalog(&m.refreshMock)
	return m
}

func (m *sharingMock) CreateSharedList(ctx context.Context, arg db.CreateSharedListParams) (db.SharedList, error) {
	list := db.SharedList{
		ID: int64(len(m.lists) + 1), Code: arg.Code, Title: arg.Title,
		Description: arg.Description, PodcastUuids: arg.PodcastUuids,
	}
	m.lists[arg.Code] = list
	return list, nil
}

func (m *sharingMock) GetPodcastByID(ctx context.Context, id int64) (db.Podcast, error) {
	for _, podcast := range m.podcasts {
		if podcast.ID == id {
			return podcast, nil
		}
	}
	return db.Podcast{}, pgx.ErrNoRows
}

func (m *sharingMock) GetSharedListByCode(ctx context.Context, code string) (db.SharedList, error) {
	if list, ok := m.lists[code]; ok {
		return list, nil
	}
	return db.SharedList{}, pgx.ErrNoRows
}

func sharingRouter(m *sharingMock, credential string) *http.ServeMux {
	cfg := &config.AuthConfiguration{
		JWTSecret:         testAuthConfig.JWTSecret,
		AccessTokenTTL:    testAuthConfig.AccessTokenTTL,
		RefreshTokenTTL:   testAuthConfig.RefreshTokenTTL,
		SharingCredential: credential,
	}
	handlers := Handlers{Queries: m, Config: cfg}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /share/list", handlers.PostShareList)
	mux.HandleFunc("GET /l/{code}", handlers.GetSharedList)
	mux.HandleFunc("POST /podcast/{uuid}", handlers.PostSharePodcast)
	mux.HandleFunc("POST /episode/{uuid}", handlers.PostShareEpisode)
	return mux
}

func TestShareListRoundTrip(t *testing.T) {
	m := newSharingMock()
	router := sharingRouter(m, "")

	var created struct {
		Status string `json:"status"`
		Result struct {
			ShareURL string `json:"share_url"`
		} `json:"result"`
	}

	// create with a valid podcast
	body := map[string]any{
		"title":       "My Favorites",
		"description": "good shows",
		"podcasts":    []map[string]string{{"uuid": testPodcastUUID}},
		"datetime":    "20240601120000",
		"h":           "ignored-without-credential",
	}
	status := postJSONHelper(t, router, "/share/list", body, &created)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "ok", created.Status)
	require.Contains(t, created.Result.ShareURL, "/l/")

	// resolve the list
	path := created.Result.ShareURL[len("http://example.com"):]
	req := httptest.NewRequest("GET", path, nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var list struct {
		Title    string            `json:"title"`
		Podcasts []podcastInfoJSON `json:"podcasts"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &list))
	assert.Equal(t, "My Favorites", list.Title)
	assert.Len(t, list.Podcasts, 1)
	assert.Equal(t, testPodcastUUID, list.Podcasts[0].UUID)
}

func postJSONHelper(t *testing.T, router *http.ServeMux, path string, body any, out any) int {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest("POST", path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if out != nil && rr.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), out))
	}
	return rr.Code
}

func TestShareListSignature(t *testing.T) {
	m := newSharingMock()
	router := sharingRouter(m, "secret-credential")

	datetime := "20240601120000"
	sum := sha1.Sum([]byte(datetime + "secret-credential"))

	// valid signature accepted
	var created struct {
		Status string `json:"status"`
	}
	status := postJSONHelper(t, router, "/share/list", map[string]any{
		"title":    "Signed",
		"podcasts": []map[string]string{{"uuid": testPodcastUUID}},
		"datetime": datetime,
		"h":        hex.EncodeToString(sum[:]),
	}, &created)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "ok", created.Status)

	// bad signature rejected
	status = postJSONHelper(t, router, "/share/list", map[string]any{
		"title":    "Forged",
		"podcasts": []map[string]string{{"uuid": testPodcastUUID}},
		"datetime": datetime,
		"h":        "deadbeef",
	}, nil)
	assert.Equal(t, http.StatusUnauthorized, status)
}

func TestShareListRejectsEmpty(t *testing.T) {
	router := sharingRouter(newSharingMock(), "")

	var resp struct {
		Status string `json:"status"`
	}
	status := postJSONHelper(t, router, "/share/list", map[string]any{
		"title":    "Empty",
		"podcasts": []map[string]string{{"uuid": "not-a-uuid"}},
	}, &resp)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "error", resp.Status)
}

func TestSharePodcastLink(t *testing.T) {
	router := sharingRouter(newSharingMock(), "")

	var resp struct {
		Status string `json:"status"`
		Result struct {
			Podcast podcastInfoJSON `json:"podcast"`
		} `json:"result"`
	}
	status := postJSONHelper(t, router, "/podcast/"+testPodcastUUID, map[string]any{}, &resp)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "ok", resp.Status)
	assert.Equal(t, "Test Show", resp.Result.Podcast.Title)
}

func TestShareEpisodeLink(t *testing.T) {
	router := sharingRouter(newSharingMock(), "")

	var resp struct {
		Status string `json:"status"`
		Result struct {
			Podcast       podcastInfoJSON    `json:"podcast"`
			SharedEpisode refreshEpisodeJSON `json:"shared_episode"`
			Time          string             `json:"time"`
		} `json:"result"`
	}
	status := postJSONHelper(t, router, "/episode/aaaaaaaa-0000-4000-8000-000000000002?t=63", map[string]any{}, &resp)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "ok", resp.Status)
	assert.Equal(t, testPodcastUUID, resp.Result.Podcast.UUID)
	assert.Equal(t, "Two", resp.Result.SharedEpisode.Title)
	assert.Equal(t, "63", resp.Result.Time)

	// unknown episode 404s
	status = postJSONHelper(t, router, "/episode/99999999-9999-4999-8999-999999999999", map[string]any{}, nil)
	assert.Equal(t, http.StatusNotFound, status)
}
