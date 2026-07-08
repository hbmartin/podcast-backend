//go:build e2e

// Package e2e drives the compiled server end-to-end over real HTTP with a
// real Postgres database: register -> login -> sync -> second-device
// convergence -> catalog ingest via a fixture feed -> cache-host reads.
//
// Requirements: a reachable Postgres (set E2E_DB_CONNECTION_STRING) and the
// go toolchain. Run via `make e2e`.
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "github.com/hbmartin/podcast-backend/protos/api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

var (
	baseURL    string
	feedServer *httptest.Server
)

func TestMain(m *testing.M) {
	connString := os.Getenv("E2E_DB_CONNECTION_STRING")
	if connString == "" {
		fmt.Println("E2E_DB_CONNECTION_STRING not set; skipping e2e suite")
		os.Exit(0)
	}

	// serve the crawler fixture feed for catalog ingestion, rewriting its
	// artwork URL to a tiny PNG this server also hosts so color extraction
	// exercises the real fetch path
	feedServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/art.png" {
			w.Header().Set("Content-Type", "image/png")
			w.Write(artworkPNG())
			return
		}
		raw, err := os.ReadFile(filepath.Join("..", "crawler", "testdata", "feed.xml"))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		feed := strings.ReplaceAll(string(raw), "https://example.com/art.jpg", feedServer.URL+"/art.png")
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write([]byte(feed))
	}))
	defer feedServer.Close()

	binary := filepath.Join(os.TempDir(), "podcast-backend-e2e")
	build := exec.Command("go", "build", "-o", binary, "..")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Println("build failed:", err)
		os.Exit(1)
	}

	port := "127.0.0.1:8091"
	baseURL = "http://" + port

	server := exec.Command(binary)
	server.Dir = ".." // migrations are read from db/migrations relative to cwd
	server.Env = append(os.Environ(),
		"ENV=e2e",
		"WEB_PORT="+port,
		"DB_CONNECTION_STRING="+connString,
		"AUTH_JWT_SECRET=e2e-secret-e2e-secret-e2e-secret-32",
		"ENABLE_TASK_QUEUE=false",
		"ENABLE_SWAGGER=false",
	)
	server.Stdout, server.Stderr = os.Stdout, os.Stderr
	if err := server.Start(); err != nil {
		fmt.Println("server start failed:", err)
		os.Exit(1)
	}
	defer server.Process.Kill()

	if !waitForHealth(baseURL + "/health") {
		fmt.Println("server did not become healthy")
		server.Process.Kill()
		os.Exit(1)
	}

	code := m.Run()
	server.Process.Kill()
	os.Exit(code)
}

// artworkPNG renders a solid dark-red 10x10 cover image.
func artworkPNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			img.Set(x, y, color.RGBA{R: 200, G: 20, B: 20, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func waitForHealth(url string) bool {
	for i := 0; i < 50; i++ {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

// postProto sends a protobuf message and decodes the protobuf response.
func postProto(t *testing.T, path string, token string, req proto.Message, resp proto.Message) int {
	t.Helper()

	body, err := proto.Marshal(req)
	require.NoError(t, err)

	httpReq, err := http.NewRequest("POST", baseURL+path, bytes.NewReader(body))
	require.NoError(t, err)
	httpReq.Header.Set("Content-Type", "application/octet-stream")
	if token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}

	httpResp, err := http.DefaultClient.Do(httpReq)
	require.NoError(t, err)
	defer httpResp.Body.Close()

	raw, err := io.ReadAll(httpResp.Body)
	require.NoError(t, err)

	if httpResp.StatusCode == http.StatusOK && resp != nil {
		require.NoError(t, proto.Unmarshal(raw, resp))
	}
	return httpResp.StatusCode
}

func postJSON(t *testing.T, path string, token string, req any, resp any) int {
	t.Helper()

	body, err := json.Marshal(req)
	require.NoError(t, err)

	httpReq, err := http.NewRequest("POST", baseURL+path, bytes.NewReader(body))
	require.NoError(t, err)
	httpReq.Header.Set("Content-Type", "application/json")
	if token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}

	httpResp, err := http.DefaultClient.Do(httpReq)
	require.NoError(t, err)
	defer httpResp.Body.Close()

	raw, err := io.ReadAll(httpResp.Body)
	require.NoError(t, err)
	if resp != nil {
		require.NoError(t, json.Unmarshal(raw, resp))
	}
	return httpResp.StatusCode
}

func registerUser(t *testing.T, email string) (token string, uuid string) {
	t.Helper()

	resp := &pb.RegisterResponse{}
	status := postProto(t, "/user/register", "", &pb.RegisterRequest{
		Email: email, Password: "e2e-password", Scope: "mobile",
	}, resp)
	require.Equal(t, http.StatusOK, status)
	require.True(t, resp.Success.GetValue())
	return resp.Token, resp.Uuid
}

func login(t *testing.T, email string) string {
	t.Helper()

	resp := &pb.UserLoginResponse{}
	status := postProto(t, "/user/login", "", &pb.UserLoginRequest{
		Email: email, Password: "e2e-password", Scope: "mobile",
	}, resp)
	require.Equal(t, http.StatusOK, status)
	require.NotEmpty(t, resp.Token)
	return resp.Token
}

func TestAccountAndSyncConvergence(t *testing.T) {
	email := fmt.Sprintf("sync-%d@e2e.test", time.Now().UnixNano())
	tokenA, userUuid := registerUser(t, email)
	assert.NotEmpty(t, userUuid)

	// device A subscribes to a podcast and reports playback progress
	sync := &pb.SyncUpdateResponse{}
	status := postProto(t, "/user/sync/update", tokenA, &pb.SyncUpdateRequest{
		DeviceUtcTimeMs: time.Now().UnixMilli(),
		Records: []*pb.Record{
			{Record: &pb.Record_Podcast{Podcast: &pb.SyncUserPodcast{
				Uuid:       "6a09813e-84ba-4f4c-b70c-620ae7dcbfc9",
				Subscribed: wrapperspb.Bool(true),
			}}},
			{Record: &pb.Record_Episode{Episode: &pb.SyncUserEpisode{
				Uuid:               "5b1fdc19-e2e6-4b34-a3f1-72b0d5b83b41",
				PodcastUuid:        "6a09813e-84ba-4f4c-b70c-620ae7dcbfc9",
				PlayedUpTo:         wrapperspb.Int64(120),
				PlayedUpToModified: wrapperspb.Int64(time.Now().UnixMilli()),
				Starred:            wrapperspb.Bool(true),
				StarredModified:    wrapperspb.Int64(time.Now().UnixMilli()),
			}}},
		},
	}, sync)
	require.Equal(t, http.StatusOK, status)
	assert.Greater(t, sync.LastModified, int64(0))

	// second device logs in and does an initial incremental sync from zero
	tokenB := login(t, email)
	syncB := &pb.SyncUpdateResponse{}
	status = postProto(t, "/user/sync/update", tokenB, &pb.SyncUpdateRequest{LastModified: 0}, syncB)
	require.Equal(t, http.StatusOK, status)

	var sawPodcast, sawEpisode bool
	for _, record := range syncB.Records {
		if p := record.GetPodcast(); p != nil && p.Uuid == "6a09813e-84ba-4f4c-b70c-620ae7dcbfc9" {
			sawPodcast = p.Subscribed.GetValue()
		}
		if e := record.GetEpisode(); e != nil && e.Uuid == "5b1fdc19-e2e6-4b34-a3f1-72b0d5b83b41" {
			sawEpisode = e.PlayedUpTo.GetValue() == 120 && e.Starred.GetValue()
		}
	}
	assert.True(t, sawPodcast, "device B sees the subscription")
	assert.True(t, sawEpisode, "device B sees playback state")

	// starred list agrees
	starred := &pb.StarredEpisodesResponse{}
	status = postProto(t, "/starred/list", tokenB, &pb.EmptyRequest{}, starred)
	require.Equal(t, http.StatusOK, status)
	assert.Len(t, starred.Episodes, 1)

	// last_sync_at reflects the token
	lastSync := &pb.UserLastSyncAtResponse{}
	status = postProto(t, "/user/last_sync_at", tokenB, &pb.EmptyRequest{}, lastSync)
	require.Equal(t, http.StatusOK, status)
	assert.GreaterOrEqual(t, lastSync.LastSyncAtMs, sync.LastModified)
}

func TestUpNextAndHistory(t *testing.T) {
	email := fmt.Sprintf("upnext-%d@e2e.test", time.Now().UnixNano())
	token, _ := registerUser(t, email)

	upNext := &pb.UpNextResponse{}
	status := postProto(t, "/up_next/sync", token, &pb.UpNextSyncRequest{
		DeviceTime: time.Now().UnixMilli(),
		UpNext: &pb.UpNextChanges{Changes: []*pb.UpNextChanges_Change{
			{Uuid: "11111111-1111-4111-8111-111111111111", Action: 3, Modified: 1, Title: "First"},
			{Uuid: "22222222-2222-4222-8222-222222222222", Action: 1, Modified: 2, Title: "Now"},
		}},
	}, upNext)
	require.Equal(t, http.StatusOK, status)
	require.Len(t, upNext.Episodes, 2)
	assert.Equal(t, "22222222-2222-4222-8222-222222222222", upNext.Episodes[0].Uuid)

	history := &pb.HistoryResponse{}
	status = postProto(t, "/history/sync", token, &pb.HistorySyncRequest{
		DeviceTime: time.Now().UnixMilli(),
		Changes: []*pb.HistoryChange{
			{Action: 1, Episode: "33333333-3333-4333-8333-333333333333", Title: "Heard", ModifiedAt: time.Now().UnixMilli()},
		},
	}, history)
	require.Equal(t, http.StatusOK, status)
	assert.Len(t, history.Changes, 1)
	assert.Equal(t, "Heard", history.Changes[0].Title)
}

func TestSettingsRoundTrip(t *testing.T) {
	email := fmt.Sprintf("settings-%d@e2e.test", time.Now().UnixNano())
	token, _ := registerUser(t, email)

	resp := &pb.NamedSettingsResponse{}
	status := postProto(t, "/user/named_settings/update", token, &pb.NamedSettingsRequest{
		M: "iPhone",
		ChangedSettings: &pb.ChangeableSettings{
			SkipForward: &pb.Int32Setting{
				Value:      wrapperspb.Int32(45),
				Changed:    wrapperspb.Bool(true),
				ModifiedAt: nowProto(),
			},
		},
	}, resp)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, int32(45), resp.SkipForward.Value.GetValue())
}

func TestCatalogIngestAndCacheHost(t *testing.T) {
	email := fmt.Sprintf("catalog-%d@e2e.test", time.Now().UnixNano())
	registerUser(t, email)

	feedURL := feedServer.URL + "/feed.xml"

	// search by URL crawls the feed synchronously
	var search struct {
		Status string `json:"status"`
		Result struct {
			Podcast struct {
				UUID  string `json:"uuid"`
				Title string `json:"title"`
			} `json:"podcast"`
		} `json:"result"`
	}
	status := postJSON(t, "/podcasts/search", "", map[string]string{"q": feedURL}, &search)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "ok", search.Status)
	require.NotEmpty(t, search.Result.Podcast.UUID)
	assert.Equal(t, "Test Show", search.Result.Podcast.Title)

	podcastUuid := search.Result.Podcast.UUID

	// cache host serves the full podcast with episodes and validators
	resp, err := http.Get(baseURL + "/mobile/podcast/full/" + podcastUuid)
	require.NoError(t, err)
	var full struct {
		Podcast struct {
			UUID     string `json:"uuid"`
			Episodes []struct {
				UUID string `json:"uuid"`
				URL  string `json:"url"`
			} `json:"episodes"`
		} `json:"podcast"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&full))
	resp.Body.Close()
	etag := resp.Header.Get("ETag")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, full.Podcast.Episodes, 2)
	require.NotEmpty(t, etag)

	// conditional request replies 304
	req, _ := http.NewRequest("GET", baseURL+"/mobile/podcast/full/"+podcastUuid, nil)
	req.Header.Set("If-None-Match", etag)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusNotModified, resp.StatusCode)

	// episode URL lookup returns the enclosure URL as plain text
	episodeUuid := full.Podcast.Episodes[0].UUID
	resp, err = http.Get(baseURL + "/mobile/episode/url/" + podcastUuid + "/" + episodeUuid)
	require.NoError(t, err)
	urlBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, string(urlBytes), "https://cdn.example.com/")

	// refresh user/update reports the new episodes for this podcast
	var refresh struct {
		Status string `json:"status"`
		Result struct {
			PodcastUpdates map[string][]struct {
				UUID string `json:"uuid"`
			} `json:"podcast_updates"`
		} `json:"result"`
	}
	status = postJSON(t, "/user/update", "", map[string]string{
		"podcasts":      podcastUuid,
		"last_episodes": "",
	}, &refresh)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "ok", refresh.Status)
	assert.Len(t, refresh.Result.PodcastUpdates[podcastUuid], 2)

	// combined search finds the ingested podcast
	var combined struct {
		Results []struct {
			Type  string `json:"type"`
			Title string `json:"title"`
		} `json:"results"`
	}
	status = postJSON(t, "/search/combined", "", map[string]string{"term": "Test Show"}, &combined)
	require.Equal(t, http.StatusOK, status)
	found := false
	for _, result := range combined.Results {
		if result.Type == "podcast" && result.Title == "Test Show" {
			found = true
		}
	}
	assert.True(t, found)
}

func TestAuthFailures(t *testing.T) {
	// no token
	status := postProto(t, "/user/sync/update", "", &pb.SyncUpdateRequest{}, nil)
	assert.Equal(t, http.StatusUnauthorized, status)

	// wrong password produces the client error envelope
	email := fmt.Sprintf("auth-%d@e2e.test", time.Now().UnixNano())
	registerUser(t, email)

	body, _ := proto.Marshal(&pb.UserLoginRequest{Email: email, Password: "wrong"})
	resp, err := http.Post(baseURL+"/user/login", "application/octet-stream", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	var envelope struct {
		ErrorMessageID string `json:"errorMessageId"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&envelope))
	assert.Equal(t, "login_password_incorrect", envelope.ErrorMessageID)
}

func nowProto() *timestamppb.Timestamp {
	return timestamppb.New(time.Now())
}

// ingestFixturePodcast ensures the fixture feed is in the catalog and
// returns its uuid (deterministic, so repeat calls converge).
func ingestFixturePodcast(t *testing.T) string {
	t.Helper()

	var search struct {
		Status string `json:"status"`
		Result struct {
			Podcast struct {
				UUID string `json:"uuid"`
			} `json:"podcast"`
		} `json:"result"`
	}
	status := postJSON(t, "/podcasts/search", "", map[string]string{"q": feedServer.URL + "/feed.xml"}, &search)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "ok", search.Status)
	require.NotEmpty(t, search.Result.Podcast.UUID)
	return search.Result.Podcast.UUID
}

func TestArtworkAndColors(t *testing.T) {
	podcastUuid := ingestFixturePodcast(t)

	// artwork redirects to the feed's cover image
	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(baseURL + "/discover/images/280/" + podcastUuid + ".jpg")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Equal(t, feedServer.URL+"/art.png", resp.Header.Get("Location"))

	// color metadata computed from the artwork
	resp, err = http.Get(baseURL + "/discover/images/metadata/" + podcastUuid + ".json")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var envelope struct {
		Colors struct {
			Background     string `json:"background"`
			TintForLightBg string `json:"tintForLightBg"`
			TintForDarkBg  string `json:"tintForDarkBg"`
		} `json:"colors"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&envelope))
	assert.Equal(t, "#C81414", envelope.Colors.Background)
	assert.Regexp(t, `^#[0-9A-F]{6}$`, envelope.Colors.TintForLightBg)
	assert.Regexp(t, `^#[0-9A-F]{6}$`, envelope.Colors.TintForDarkBg)
}
