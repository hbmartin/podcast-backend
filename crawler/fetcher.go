package crawler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// FetchResult is one conditional GET of a feed.
type FetchResult struct {
	// Body is nil when NotModified is true.
	Body         io.ReadCloser
	ETag         string
	LastModified string
	NotModified  bool
}

// Fetcher retrieves feed documents. The production implementation dials out;
// tests supply fixtures — nothing in the test suite performs network IO.
type Fetcher interface {
	Fetch(ctx context.Context, url string, etag string, lastModified string) (*FetchResult, error)
}

// HTTPFetcher fetches feeds over HTTP with conditional-request support.
type HTTPFetcher struct {
	Client    *http.Client
	UserAgent string
}

func NewHTTPFetcher() *HTTPFetcher {
	return &HTTPFetcher{
		Client:    &http.Client{Timeout: 30 * time.Second},
		UserAgent: "podcast-backend/1.0 (+https://github.com/hbmartin/podcast-backend)",
	}
}

func (f *HTTPFetcher) Fetch(ctx context.Context, url string, etag string, lastModified string) (*FetchResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", f.UserAgent)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if lastModified != "" {
		req.Header.Set("If-Modified-Since", lastModified)
	}

	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusNotModified {
		resp.Body.Close()
		return &FetchResult{NotModified: true}, nil
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("feed fetch returned status %d", resp.StatusCode)
	}

	return &FetchResult{
		Body:         resp.Body,
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
	}, nil
}
