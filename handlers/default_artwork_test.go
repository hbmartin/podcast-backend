package handlers

import (
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func defaultArtworkRouter() *http.ServeMux {
	router := http.NewServeMux()
	router.HandleFunc("GET /discover/images/artwork/{theme}/{size}/{file}", Handlers{}.GetDefaultArtwork)
	return router
}

func TestDefaultArtworkAllFiniteVariants(t *testing.T) {
	router := defaultArtworkRouter()
	for _, theme := range []string{"light", "dark"} {
		for _, size := range []string{"280", "960"} {
			for variant := '1'; variant <= '8'; variant++ {
				path := "/discover/images/artwork/" + theme + "/" + size + "/" + string(variant) + ".png"
				recorder := httptest.NewRecorder()
				router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
				if recorder.Code != http.StatusOK {
					t.Fatalf("%s: status = %d", path, recorder.Code)
				}
				if !strings.HasPrefix(recorder.Header().Get("ETag"), `"`) || !strings.Contains(recorder.Header().Get("Cache-Control"), "immutable") {
					t.Fatalf("%s: missing immutable caching headers", path)
				}
				img, err := png.Decode(strings.NewReader(recorder.Body.String()))
				if err != nil {
					t.Fatalf("%s: %v", path, err)
				}
				if got := img.Bounds().Dx(); got != mustAtoi(size) {
					t.Fatalf("%s: width = %d", path, got)
				}
			}
		}
	}
}

func TestDefaultArtworkETagAndInvalidVariants(t *testing.T) {
	router := defaultArtworkRouter()
	first := httptest.NewRecorder()
	router.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/discover/images/artwork/light/280/2.png", nil))

	request := httptest.NewRequest(http.MethodGet, "/discover/images/artwork/light/280/2.png", nil)
	request.Header.Set("If-None-Match", first.Header().Get("ETag"))
	notModified := httptest.NewRecorder()
	router.ServeHTTP(notModified, request)
	if notModified.Code != http.StatusNotModified {
		t.Fatalf("conditional status = %d", notModified.Code)
	}

	for _, path := range []string{
		"/discover/images/artwork/classic/280/1.png",
		"/discover/images/artwork/light/281/1.png",
		"/discover/images/artwork/light/280/0.png",
		"/discover/images/artwork/light/280/9.png",
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d", path, recorder.Code)
		}
	}
}

func mustAtoi(value string) int {
	if value == "960" {
		return 960
	}
	return 280
}
