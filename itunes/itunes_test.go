package itunes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

const searchFixture = `{
  "resultCount": 2,
  "results": [
    {
      "collectionId": 123,
      "collectionName": "Test Show",
      "artistName": "Test Author",
      "feedUrl": "https://example.com/feed.xml",
      "artworkUrl600": "https://example.com/art.jpg",
      "collectionExplicitness": "explicit"
    },
    {
      "collectionId": 456,
      "collectionName": "No Feed Show",
      "artistName": "Someone"
    }
  ]
}`

func TestSearchParsesAndSkipsFeedlessResults(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.String()
		w.Write([]byte(searchFixture))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	results, err := client.Search(context.Background(), "test show", 20)

	assert.NoError(t, err)
	assert.Contains(t, gotPath, "media=podcast")
	assert.Contains(t, gotPath, "term=test+show")
	assert.Len(t, results, 1, "results without a feedUrl are dropped")
	assert.Equal(t, int64(123), results[0].CollectionID)
	assert.Equal(t, "Test Show", results[0].Title)
	assert.Equal(t, "https://example.com/feed.xml", results[0].FeedURL)
	assert.True(t, results[0].Explicit)
}

func TestLookup(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/lookup", r.URL.Path)
		assert.Equal(t, "123", r.URL.Query().Get("id"))
		w.Write([]byte(searchFixture))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	result, err := client.Lookup(context.Background(), 123)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "https://example.com/feed.xml", result.FeedURL)
}

func TestUpstreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	_, err := client.Search(context.Background(), "x", 5)
	assert.Error(t, err)
}
