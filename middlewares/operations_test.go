package middlewares

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBearerSecret(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	resp := httptest.NewRecorder()
	BearerSecret("", next).ServeHTTP(resp, req)
	assert.Equal(t, http.StatusNotFound, resp.Code)

	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	resp = httptest.NewRecorder()
	BearerSecret("secret", next).ServeHTTP(resp, req)
	assert.Equal(t, http.StatusUnauthorized, resp.Code)

	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp = httptest.NewRecorder()
	BearerSecret("secret", next).ServeHTTP(resp, req)
	assert.Equal(t, http.StatusNoContent, resp.Code)
}

func TestCanonicalHost(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := CanonicalHost("https://pods.example.com", next)

	for _, path := range []string{"/livez", "/api/v1/capabilities"} {
		req := httptest.NewRequest(http.MethodGet, "https://pods.example.com"+path, nil)
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		assert.Equal(t, http.StatusNoContent, resp.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "https://provider.example/u/alice?x=1", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	assert.Equal(t, http.StatusPermanentRedirect, resp.Code)
	assert.Equal(t, "https://pods.example.com/u/alice?x=1", resp.Header().Get("Location"))

	req = httptest.NewRequest(http.MethodGet, "https://provider.example/api/v1/capabilities", nil)
	resp = httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	assert.Equal(t, http.StatusMisdirectedRequest, resp.Code)
}
