package handlers

import (
	"crypto/tls"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRequestBaseURL(t *testing.T) {
	// plain request
	r := httptest.NewRequest("GET", "/x", nil)
	r.Host = "api.example.com"
	assert.Equal(t, "http://api.example.com", requestBaseURL(r))

	// TLS
	r = httptest.NewRequest("GET", "/x", nil)
	r.Host = "api.example.com"
	r.TLS = &tls.ConnectionState{}
	assert.Equal(t, "https://api.example.com", requestBaseURL(r))

	// forwarded headers win
	r = httptest.NewRequest("GET", "/x", nil)
	r.Host = "internal:8080"
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Host", "pods.example.com")
	assert.Equal(t, "https://pods.example.com", requestBaseURL(r))
}
