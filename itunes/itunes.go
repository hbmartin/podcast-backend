// Package itunes proxies podcast text search to the iTunes Search API.
package itunes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"goapi-template/errs"
)

// Result is one podcast from the search upstream.
type Result struct {
	CollectionID int64
	Title        string
	Author       string
	FeedURL      string
	ArtworkURL   string
	Explicit     bool
}

// Searcher finds podcasts by text term. Tests point Client.BaseURL at an
// httptest server; nothing in the test suite dials Apple.
type Searcher interface {
	Search(ctx context.Context, term string, limit int) ([]Result, error)
	Lookup(ctx context.Context, collectionID int64) (*Result, error)
}

type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

func NewClient(baseURL string) *Client {
	if baseURL == "" {
		baseURL = "https://itunes.apple.com"
	}
	return &Client{
		BaseURL:    baseURL,
		HTTPClient: &http.Client{Timeout: 15 * time.Second},
	}
}

type itunesResponse struct {
	Results []itunesResult `json:"results"`
}

type itunesResult struct {
	CollectionID       int64  `json:"collectionId"`
	CollectionName     string `json:"collectionName"`
	ArtistName         string `json:"artistName"`
	FeedURL            string `json:"feedUrl"`
	ArtworkURL600      string `json:"artworkUrl600"`
	CollectionExplicit string `json:"collectionExplicitness"`
}

func (c *Client) Search(ctx context.Context, term string, limit int) ([]Result, error) {
	const op errs.Op = "itunes/Client.Search"

	query := url.Values{
		"media": {"podcast"},
		"term":  {term},
		"limit": {fmt.Sprint(limit)},
	}
	return c.call(ctx, op, c.BaseURL+"/search?"+query.Encode())
}

func (c *Client) Lookup(ctx context.Context, collectionID int64) (*Result, error) {
	const op errs.Op = "itunes/Client.Lookup"

	results, err := c.call(ctx, op, fmt.Sprintf("%s/lookup?id=%d", c.BaseURL, collectionID))
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, nil
	}
	return &results[0], nil
}

func (c *Client) call(ctx context.Context, op errs.Op, callURL string) ([]Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, callURL, nil)
	if err != nil {
		return nil, errs.E(op, errs.Internal, err)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, errs.E(op, errs.Internal, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errs.E(op, errs.Internal, errs.Code("itunes_upstream_error"),
			fmt.Errorf("itunes returned status %d", resp.StatusCode))
	}

	parsed := itunesResponse{}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, errs.E(op, errs.Internal, err)
	}

	results := make([]Result, 0, len(parsed.Results))
	for _, item := range parsed.Results {
		if item.FeedURL == "" {
			continue
		}
		results = append(results, Result{
			CollectionID: item.CollectionID,
			Title:        item.CollectionName,
			Author:       item.ArtistName,
			FeedURL:      item.FeedURL,
			ArtworkURL:   item.ArtworkURL600,
			Explicit:     item.CollectionExplicit == "explicit",
		})
	}
	return results, nil
}
