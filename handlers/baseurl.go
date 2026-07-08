package handlers

import (
	"net/http"
	"strings"
)

// baseURL resolves the absolute base URL for links handed back to the client
// (discover source URLs, share URLs). A configured PUBLIC_BASE_URL wins so
// deployments behind proxies aren't at the mercy of client-controlled
// X-Forwarded-* headers.
func (h Handlers) baseURL(r *http.Request) string {
	if h.PublicBaseURL != "" {
		return strings.TrimRight(h.PublicBaseURL, "/")
	}
	return requestBaseURL(r)
}

// requestBaseURL derives the base URL from the request, honoring
// reverse-proxy headers.
func requestBaseURL(r *http.Request) string {
	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}

	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}

	return scheme + "://" + host
}
