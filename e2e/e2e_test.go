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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
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
	"sync"
	"sync/atomic"
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

	// push fixtures: a fake APNs endpoint recording deliveries, and a toggle
	// that makes the fixture feed publish one extra (newer) episode
	apnsMu              sync.Mutex
	apnsPushes          []apnsPush
	includeExtraEpisode atomic.Bool
)

type apnsPush struct {
	path string
	body string
}

const extraEpisodeItem = `    <item>
      <title>Breaking Episode</title>
      <guid>ep-guid-push</guid>
      <pubDate>Fri, 05 Jan 2024 10:00:00 +0000</pubDate>
      <description>Fresh off the press</description>
      <enclosure url="https://cdn.example.com/ep-push.mp3" length="200" type="audio/mpeg"/>
    </item>
`

// apnsKeyFile writes a throwaway EC P-256 key in Apple's .p8 layout so the
// server's APNs client can sign provider tokens against the fixture endpoint.
func apnsKeyFile() (string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", err
	}
	path := filepath.Join(os.TempDir(), "podcast-backend-e2e-apns.p8")
	return path, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600)
}

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
		if includeExtraEpisode.Load() {
			feed = strings.Replace(feed, "    <item>", extraEpisodeItem+"    <item>", 1)
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write([]byte(feed))
	}))
	defer feedServer.Close()

	apnsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		apnsMu.Lock()
		apnsPushes = append(apnsPushes, apnsPush{path: r.URL.Path, body: string(body)})
		apnsMu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer apnsServer.Close()

	apnsKey, err := apnsKeyFile()
	if err != nil {
		fmt.Println("apns key generation failed:", err)
		os.Exit(1)
	}

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
		// keep the auth rate limiter on its enabled code path without ever
		// tripping during the suite's rapid-fire logins
		"RATE_LIMIT_AUTH=1000",
		// push notifications against the fixture APNs endpoint (queue off,
		// so delivery runs in-process)
		"APNS_KEY_FILE="+apnsKey,
		"APNS_KEY_ID=E2EKEY",
		"APNS_TEAM_ID=E2ETEAM",
		"APNS_TOPIC=com.example.e2e",
		"APNS_ENDPOINT="+apnsServer.URL,
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

	// show notes carry the full episode metadata: transcripts (required key,
	// even when empty), episode image, and the podcast:chapters URL
	resp, err = http.Get(baseURL + "/mobile/show_notes/full/" + podcastUuid)
	require.NoError(t, err)
	var notes struct {
		Podcast struct {
			Episodes []struct {
				UUID        string `json:"uuid"`
				ShowNotes   string `json:"show_notes"`
				Transcripts []struct {
					URL      string `json:"url"`
					Type     string `json:"type"`
					Language string `json:"language"`
				} `json:"transcripts"`
				ChaptersURL string `json:"chapters_url"`
			} `json:"episodes"`
		} `json:"podcast"`
	}
	rawNotes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, json.Unmarshal(rawNotes, &notes))
	require.Len(t, notes.Podcast.Episodes, 2)
	assert.Contains(t, string(rawNotes), `"transcripts"`)
	var withTranscript bool
	for _, episode := range notes.Podcast.Episodes {
		require.NotNil(t, episode.Transcripts, "transcripts key must decode on every episode")
		if len(episode.Transcripts) > 0 {
			withTranscript = true
			assert.Equal(t, "https://cdn.example.com/ep2.vtt", episode.Transcripts[0].URL)
			assert.Equal(t, "text/vtt", episode.Transcripts[0].Type)
			assert.Equal(t, "en", episode.Transcripts[0].Language)
			assert.Equal(t, "https://cdn.example.com/ep2-chapters.json", episode.ChaptersURL)
		}
	}
	assert.True(t, withTranscript, "fixture feed's podcast:transcript tag was ingested")

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

func TestRatingsAndStats(t *testing.T) {
	email := fmt.Sprintf("ratings-%d@e2e.test", time.Now().UnixNano())
	token, _ := registerUser(t, email)
	podcastUuid := ingestFixturePodcast(t)

	// rate the podcast
	status := postProto(t, "/user/podcast_rating/add", token,
		&pb.PodcastRatingAddRequest{PodcastUuid: podcastUuid, PodcastRating: 5}, nil)
	require.Equal(t, http.StatusOK, status)

	// own rating round-trips
	shown := &pb.PodcastRating{}
	status = postProto(t, "/user/podcast_rating/show", token,
		&pb.PodcastRatingShowRequest{PodcastUuid: podcastUuid}, shown)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, uint32(5), shown.PodcastRating)

	// aggregate rating is public JSON on the cache host role
	resp, err := http.Get(baseURL + "/podcast/rating/" + podcastUuid)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var aggregate struct {
		Total   int64   `json:"total"`
		Average float64 `json:"average"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&aggregate))
	assert.Equal(t, int64(1), aggregate.Total)
	assert.Equal(t, float64(5), aggregate.Average)

	// device stats ride along in sync; summary reflects them
	syncResp := &pb.SyncUpdateResponse{}
	status = postProto(t, "/user/sync/update", token, &pb.SyncUpdateRequest{
		DeviceUtcTimeMs: time.Now().UnixMilli(),
		Records: []*pb.Record{
			{Record: &pb.Record_Device{Device: &pb.SyncUserDevice{
				DeviceId:       wrapperspb.String("e2e-device"),
				DeviceType:     wrapperspb.Int32(1),
				TimeListened:   wrapperspb.Int64(3600),
				TimeSkipping:   wrapperspb.Int64(42),
				TimesStartedAt: wrapperspb.Int64(1700000000),
			}}},
		},
	}, syncResp)
	require.Equal(t, http.StatusOK, status)

	stats := &pb.StatsResponse{}
	status = postProto(t, "/user/stats/summary", token,
		&pb.StatsRequest{DeviceId: "", DeviceType: 1}, stats)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, int64(3600), stats.TimeListened)
	assert.Equal(t, int64(42), stats.TimeSkipping)
	assert.Equal(t, int64(1700000000), stats.TimesStartedAt.Seconds)
}

func TestDiscoverLayoutAndSources(t *testing.T) {
	email := fmt.Sprintf("discover-%d@e2e.test", time.Now().UnixNano())
	token, _ := registerUser(t, email)
	podcastUuid := ingestFixturePodcast(t)

	// subscribe so the fixture podcast has a subscriber for popularity
	status := postProto(t, "/user/sync/update", token, &pb.SyncUpdateRequest{
		Records: []*pb.Record{{Record: &pb.Record_Podcast{Podcast: &pb.SyncUserPodcast{
			Uuid: podcastUuid, Subscribed: wrapperspb.Bool(true),
		}}}},
	}, &pb.SyncUpdateResponse{})
	require.Equal(t, http.StatusOK, status)

	// layout parses and points at this server
	resp, err := http.Get(baseURL + "/discover/ios/content_v2.json")
	require.NoError(t, err)
	var layout struct {
		Layout []struct {
			Source string `json:"source"`
			Type   string `json:"type"`
		} `json:"layout"`
		DefaultRegionCode string `json:"default_region_code"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&layout))
	resp.Body.Close()
	require.NotEmpty(t, layout.Layout)
	assert.Equal(t, "us", layout.DefaultRegionCode)

	// first podcast_list source contains the fixture podcast
	var sourceURL string
	for _, item := range layout.Layout {
		if item.Type == "podcast_list" {
			sourceURL = item.Source
			break
		}
	}
	require.NotEmpty(t, sourceURL)

	resp, err = http.Get(sourceURL)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var list struct {
		Podcasts []struct {
			UUID string `json:"uuid"`
		} `json:"podcasts"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&list))
	found := false
	for _, podcast := range list.Podcasts {
		if podcast.UUID == podcastUuid {
			found = true
		}
	}
	assert.True(t, found, "subscribed fixture podcast appears in discover source")
}

func TestShareListAndLinks(t *testing.T) {
	podcastUuid := ingestFixturePodcast(t)

	// create a shared list
	var created struct {
		Status string `json:"status"`
		Result struct {
			ShareURL string `json:"share_url"`
		} `json:"result"`
	}
	status := postJSON(t, "/share/list", "", map[string]any{
		"title":       "E2E Favorites",
		"description": "from the e2e suite",
		"podcasts":    []map[string]string{{"uuid": podcastUuid}},
		"datetime":    "20240601120000",
		"h":           "unverified",
	}, &created)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "ok", created.Status)
	require.NotEmpty(t, created.Result.ShareURL)

	// resolve it
	resp, err := http.Get(created.Result.ShareURL)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var list struct {
		Title    string `json:"title"`
		Podcasts []struct {
			UUID string `json:"uuid"`
		} `json:"podcasts"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&list))
	assert.Equal(t, "E2E Favorites", list.Title)
	require.Len(t, list.Podcasts, 1)
	assert.Equal(t, podcastUuid, list.Podcasts[0].UUID)

	// shared podcast link resolves
	var shared struct {
		Status string `json:"status"`
		Result struct {
			Podcast struct {
				Title string `json:"title"`
			} `json:"podcast"`
		} `json:"result"`
	}
	status = postJSON(t, "/podcast/"+podcastUuid, "", map[string]any{}, &shared)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "ok", shared.Status)
	assert.Equal(t, "Test Show", shared.Result.Podcast.Title)
}

// TestObservability covers the ops surface: /health dependency reporting,
// Prometheus /metrics output, and the binary's -health container probe.
func TestObservability(t *testing.T) {
	// /health reports the DB dependency (queue is off in e2e)
	resp, err := http.Get(baseURL + "/health")
	require.NoError(t, err)
	var health struct {
		Healthy      bool `json:"healthy"`
		Dependencies []struct {
			Name    string `json:"name"`
			Healthy bool   `json:"healthy"`
		} `json:"dependencies"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&health))
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, health.Healthy)
	require.Len(t, health.Dependencies, 1)
	assert.Equal(t, "DB", health.Dependencies[0].Name)

	// /metrics serves Prometheus text including our HTTP histogram
	resp, err = http.Get(baseURL + "/metrics")
	require.NoError(t, err)
	metricsBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, string(metricsBody), "podcast_backend_http_request_duration_seconds")
	assert.Contains(t, string(metricsBody), `route="GET /health"`)

	// the binary doubles as the container HEALTHCHECK probe
	probe := exec.Command(filepath.Join(os.TempDir(), "podcast-backend-e2e"), "-health")
	probe.Env = append(os.Environ(), "WEB_PORT=127.0.0.1:8091")
	out, err := probe.CombinedOutput()
	assert.NoError(t, err, "health probe should exit 0: %s", out)
}

// TestPushNotifications registers a device's APNs token on the refresh call,
// publishes a new episode in the fixture feed, forces a re-crawl, and asserts
// the fixture APNs endpoint received the alert. It must run after the tests
// that ingest the fixture podcast's baseline episodes (source order).
func TestPushNotifications(t *testing.T) {
	podcastUuid := ingestFixturePodcast(t)
	token, _ := registerUser(t, fmt.Sprintf("push-%d@e2e.test", time.Now().UnixNano()))

	// subscribe (push targets are subscribed podcasts only)
	sync := &pb.SyncUpdateResponse{}
	status0 := postProto(t, "/user/sync/update", token, &pb.SyncUpdateRequest{
		DeviceUtcTimeMs: time.Now().UnixMilli(),
		Records: []*pb.Record{{Record: &pb.Record_Podcast{Podcast: &pb.SyncUserPodcast{
			Uuid:       podcastUuid,
			Subscribed: wrapperspb.Bool(true),
		}}}},
	}, sync)
	require.Equal(t, http.StatusOK, status0)

	// push registration piggybacks on user/update
	var refresh struct {
		Status string `json:"status"`
	}
	status := postJSON(t, "/user/update", token, map[string]string{
		"podcasts":         podcastUuid,
		"last_episodes":    "",
		"device":           "e2e-device-push",
		"push_token":       "E2EPUSHTOKEN00",
		"push_on":          "true",
		"push_messages_on": "1",
	}, &refresh)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "ok", refresh.Status)

	// the feed publishes a new episode; force a re-crawl (synchronous, since
	// the e2e server runs without the task queue)
	includeExtraEpisode.Store(true)
	t.Cleanup(func() { includeExtraEpisode.Store(false) })

	var crawl struct {
		Status string `json:"status"`
	}
	status = postJSON(t, "/podcasts/refresh", "", map[string]string{"podcast_uuid": podcastUuid}, &crawl)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "ok", crawl.Status)

	// delivery runs in a background goroutine; wait for it
	var got []apnsPush
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		apnsMu.Lock()
		got = append([]apnsPush(nil), apnsPushes...)
		apnsMu.Unlock()
		if len(got) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	require.NotEmpty(t, got, "fixture APNs endpoint received no delivery")
	assert.Equal(t, "/3/device/E2EPUSHTOKEN00", got[0].path)
	assert.Contains(t, got[0].body, `"title":"Test Show"`)
	assert.Contains(t, got[0].body, `"body":"Breaking Episode"`)
	assert.Len(t, got, 1, "exactly one new episode, one registered device")
}
