package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	imagecolor "image/color"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hbmartin/podcast-backend/db"

	"github.com/stretchr/testify/assert"
)

// artworkMock extends refreshMock with color persistence.
type artworkMock struct {
	refreshMock

	savedColors *db.UpdatePodcastColorsParams
}

func (m *artworkMock) UpdatePodcastColors(ctx context.Context, arg db.UpdatePodcastColorsParams) error {
	m.savedColors = &arg
	for uuid, podcast := range m.podcasts {
		if podcast.ID == arg.ID {
			podcast.BackgroundColor = arg.BackgroundColor
			podcast.TintForLightBg = arg.TintForLightBg
			podcast.TintForDarkBg = arg.TintForDarkBg
			podcast.ColorsSourceImageUrl = arg.ColorsSourceImageUrl
			m.podcasts[uuid] = podcast
		}
	}
	return nil
}

type fakeImageFetcher struct {
	img     image.Image
	err     error
	fetches int
}

func (f *fakeImageFetcher) FetchImage(ctx context.Context, url string) (image.Image, error) {
	f.fetches++
	return f.img, f.err
}

func solidTestImage() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			img.Set(x, y, imagecolor.RGBA{R: 200, G: 20, B: 20, A: 255})
		}
	}
	return img
}

func artworkRouter(m *artworkMock, fetcher *fakeImageFetcher) *http.ServeMux {
	handlers := Handlers{Queries: m, Config: testAuthConfig, Images: fetcher}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /discover/images/metadata/{file}", handlers.GetDiscoverImageMetadata)
	mux.HandleFunc("GET /discover/images/{size}/{file}", handlers.GetDiscoverImage)
	return mux
}

func newArtworkMock() *artworkMock {
	m := &artworkMock{refreshMock: *newRefreshMock()}
	seedCatalog(&m.refreshMock)
	podcast := m.podcasts[testPodcastUUID]
	podcast.ImageUrl = "https://cdn.example.com/cover.jpg"
	m.podcasts[testPodcastUUID] = podcast
	return m
}

func TestDiscoverImageRedirect(t *testing.T) {
	m := newArtworkMock()
	router := artworkRouter(m, &fakeImageFetcher{})

	req, _ := http.NewRequest("GET", "/discover/images/280/"+testPodcastUUID+".jpg", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusFound, rr.Code)
	assert.Equal(t, "https://cdn.example.com/cover.jpg", rr.Result().Header.Get("Location"))
	assert.Contains(t, rr.Result().Header.Get("Cache-Control"), "max-age")
}

func TestDiscoverImageInvalid(t *testing.T) {
	m := newArtworkMock()
	router := artworkRouter(m, &fakeImageFetcher{})

	// invalid size
	req, _ := http.NewRequest("GET", "/discover/images/123/"+testPodcastUUID+".jpg", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusNotFound, rr.Code)

	// unknown podcast
	req, _ = http.NewRequest("GET", "/discover/images/280/99999999-9999-4999-8999-999999999999.jpg", nil)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusNotFound, rr.Code)

	// non-uuid file name
	req, _ = http.NewRequest("GET", "/discover/images/280/evil.jpg", nil)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

type colorsEnvelope struct {
	Colors struct {
		Background     string `json:"background"`
		TintForLightBg string `json:"tintForLightBg"`
		TintForDarkBg  string `json:"tintForDarkBg"`
	} `json:"colors"`
}

func TestMetadataLazyComputeAndCache(t *testing.T) {
	m := newArtworkMock()
	fetcher := &fakeImageFetcher{img: solidTestImage()}
	router := artworkRouter(m, fetcher)

	req, _ := http.NewRequest("GET", "/discover/images/metadata/"+testPodcastUUID+".json", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	var envelope colorsEnvelope
	assert.NoError(t, json.Unmarshal(rr.Body.Bytes(), &envelope))
	assert.Equal(t, "#C81414", envelope.Colors.Background)
	assert.Len(t, envelope.Colors.TintForLightBg, 7)
	assert.Equal(t, 1, fetcher.fetches)
	assert.NotNil(t, m.savedColors)
	assert.Equal(t, "https://cdn.example.com/cover.jpg", m.savedColors.ColorsSourceImageUrl)

	// second request hits the cached row, no refetch
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, 1, fetcher.fetches)
}

func TestMetadataRecomputesWhenImageChanges(t *testing.T) {
	m := newArtworkMock()
	podcast := m.podcasts[testPodcastUUID]
	podcast.BackgroundColor = "#111111"
	podcast.TintForLightBg = "#111111"
	podcast.TintForDarkBg = "#111111"
	podcast.ColorsSourceImageUrl = "https://cdn.example.com/OLD.jpg" // stale
	m.podcasts[testPodcastUUID] = podcast

	fetcher := &fakeImageFetcher{img: solidTestImage()}
	router := artworkRouter(m, fetcher)

	req, _ := http.NewRequest("GET", "/discover/images/metadata/"+testPodcastUUID+".json", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	var envelope colorsEnvelope
	assert.NoError(t, json.Unmarshal(rr.Body.Bytes(), &envelope))
	assert.Equal(t, "#C81414", envelope.Colors.Background, "stale colors recomputed")
	assert.Equal(t, 1, fetcher.fetches)
}

func TestMetadataFallbackOnFetchFailure(t *testing.T) {
	m := newArtworkMock()
	fetcher := &fakeImageFetcher{err: errors.New("cdn down")}
	router := artworkRouter(m, fetcher)

	req, _ := http.NewRequest("GET", "/discover/images/metadata/"+testPodcastUUID+".json", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	var envelope colorsEnvelope
	assert.NoError(t, json.Unmarshal(rr.Body.Bytes(), &envelope))
	assert.Equal(t, "#3D3D3D", envelope.Colors.Background)
	// fallback persisted so the dead CDN isn't hammered
	assert.NotNil(t, m.savedColors)

	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	assert.Equal(t, 1, fetcher.fetches)
}

func TestMetadataUnknownPodcast(t *testing.T) {
	m := newArtworkMock()
	router := artworkRouter(m, &fakeImageFetcher{})

	req, _ := http.NewRequest("GET", "/discover/images/metadata/99999999-9999-4999-8999-999999999999.json", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusNotFound, rr.Code)
}
