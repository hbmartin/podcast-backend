package crawler

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"goapi-template/db"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
)

// catalogFake is an in-memory db.Store covering the catalog queries the
// crawler uses.
type catalogFake struct {
	db.Store

	nextID      int64
	podcasts    map[string]*db.Podcast // by uuid
	episodes    map[string]db.UpsertEpisodeParams
	subscribers map[string]bool
}

func newCatalogFake() *catalogFake {
	return &catalogFake{
		podcasts:    map[string]*db.Podcast{},
		episodes:    map[string]db.UpsertEpisodeParams{},
		subscribers: map[string]bool{},
	}
}

func (f *catalogFake) CreatePodcastPending(ctx context.Context, arg db.CreatePodcastPendingParams) (db.Podcast, error) {
	if existing, ok := f.podcasts[arg.Uuid]; ok {
		return *existing, nil
	}
	f.nextID++
	podcast := &db.Podcast{ID: f.nextID, Uuid: arg.Uuid, FeedUrl: arg.FeedUrl, RefreshStatus: "pending"}
	f.podcasts[arg.Uuid] = podcast
	return *podcast, nil
}

func (f *catalogFake) GetPodcastByUUID(ctx context.Context, uuid string) (db.Podcast, error) {
	if podcast, ok := f.podcasts[uuid]; ok {
		return *podcast, nil
	}
	return db.Podcast{}, pgx.ErrNoRows
}

func (f *catalogFake) GetPodcastByFeedURL(ctx context.Context, feedURL string) (db.Podcast, error) {
	for _, podcast := range f.podcasts {
		if podcast.FeedUrl == feedURL {
			return *podcast, nil
		}
	}
	return db.Podcast{}, pgx.ErrNoRows
}

func (f *catalogFake) PodcastHasSubscribers(ctx context.Context, podcastUuid string) (bool, error) {
	return f.subscribers[podcastUuid], nil
}

func (f *catalogFake) UpsertEpisode(ctx context.Context, arg db.UpsertEpisodeParams) error {
	f.episodes[arg.Uuid] = arg
	return nil
}

func (f *catalogFake) UpdatePodcastCrawlSuccess(ctx context.Context, arg db.UpdatePodcastCrawlSuccessParams) error {
	for _, podcast := range f.podcasts {
		if podcast.ID == arg.ID {
			podcast.Title = arg.Title
			podcast.Author = arg.Author
			podcast.Description = arg.Description
			podcast.ImageUrl = arg.ImageUrl
			podcast.WebsiteUrl = arg.WebsiteUrl
			podcast.Category = arg.Category
			podcast.Language = arg.Language
			podcast.ShowType = arg.ShowType
			podcast.IsExplicit = arg.IsExplicit
			podcast.FeedEtag = arg.FeedEtag
			podcast.FeedLastModified = arg.FeedLastModified
			podcast.LatestEpisodeUuid = arg.LatestEpisodeUuid
			podcast.LatestEpisodePublished = arg.LatestEpisodePublished
			podcast.ContentModifiedMs = arg.ContentModifiedMs
			podcast.NextRefreshAt = arg.NextRefreshAt
			podcast.RefreshStatus = "ok"
			podcast.RefreshError = ""
		}
	}
	return nil
}

func (f *catalogFake) UpdatePodcastCrawlNotModified(ctx context.Context, arg db.UpdatePodcastCrawlNotModifiedParams) error {
	for _, podcast := range f.podcasts {
		if podcast.ID == arg.ID {
			podcast.NextRefreshAt = arg.NextRefreshAt
		}
	}
	return nil
}

func (f *catalogFake) UpdatePodcastCrawlFailure(ctx context.Context, arg db.UpdatePodcastCrawlFailureParams) error {
	for _, podcast := range f.podcasts {
		if podcast.ID == arg.ID {
			if podcast.RefreshStatus != "ok" {
				podcast.RefreshStatus = "failed"
			}
			podcast.RefreshError = arg.RefreshError
			podcast.NextRefreshAt = arg.NextRefreshAt
		}
	}
	return nil
}

// fixtureFetcher serves canned feed documents.
type fixtureFetcher struct {
	file        string
	notModified bool
	err         error
	gotETag     string
	gotLastMod  string
}

func (f *fixtureFetcher) Fetch(ctx context.Context, url string, etag string, lastModified string) (*FetchResult, error) {
	f.gotETag = etag
	f.gotLastMod = lastModified
	if f.err != nil {
		return nil, f.err
	}
	if f.notModified {
		return &FetchResult{NotModified: true}, nil
	}
	body, err := os.Open(f.file)
	if err != nil {
		return nil, err
	}
	return &FetchResult{Body: body, ETag: `"etag-1"`, LastModified: "Mon, 01 Jan 2024 10:00:00 GMT"}, nil
}

var _ io.ReadCloser = (*os.File)(nil)

func TestUUIDDeterminism(t *testing.T) {
	a := PodcastUUID("https://Example.com/feed.xml")
	b := PodcastUUID("https://example.com/feed.xml/")
	c := PodcastUUID("https://example.com/other.xml")

	assert.Equal(t, a, b, "host case and trailing slash are canonicalized")
	assert.NotEqual(t, a, c)

	e1 := EpisodeUUID(a, "guid-1")
	e2 := EpisodeUUID(a, "guid-1")
	e3 := EpisodeUUID(a, "guid-2")
	assert.Equal(t, e1, e2)
	assert.NotEqual(t, e1, e3)
}

func TestEnsurePodcastCrawlsAndParses(t *testing.T) {
	store := newCatalogFake()
	c := &Crawler{DB: store, Fetcher: &fixtureFetcher{file: "testdata/feed.xml"}}

	podcast, err := c.EnsurePodcast(context.Background(), "https://example.com/feed.xml")
	assert.NoError(t, err)

	assert.Equal(t, "ok", podcast.RefreshStatus)
	assert.Equal(t, "Test Show", podcast.Title)
	assert.Equal(t, "Test Author", podcast.Author)
	assert.Equal(t, "serial", podcast.ShowType)
	assert.True(t, podcast.IsExplicit)
	assert.Equal(t, `"etag-1"`, podcast.FeedEtag)
	assert.Greater(t, podcast.ContentModifiedMs, int64(0))

	// two of three items have enclosures
	assert.Len(t, store.episodes, 2)

	ep2uuid := EpisodeUUID(podcast.Uuid, "ep-guid-2")
	episode, ok := store.episodes[ep2uuid]
	assert.True(t, ok)
	assert.Equal(t, "Episode Two", episode.Title)
	assert.Equal(t, "https://cdn.example.com/ep2.mp3", episode.AudioUrl)
	assert.Equal(t, int64(123456), episode.FileSize)
	assert.Equal(t, int32(3723), episode.DurationSecs, "1:02:03 in seconds")
	assert.Equal(t, "full", episode.EpisodeType)
	assert.Equal(t, int32(1), episode.Season)
	assert.Equal(t, int32(2), episode.Number)
	assert.Equal(t, "<p>Show notes for two</p>", episode.ShowNotes)

	// latest episode is the newest by pubDate
	assert.Equal(t, ep2uuid, *podcast.LatestEpisodeUuid)

	// idempotent: ensure again returns without recrawl
	again, err := c.EnsurePodcast(context.Background(), "https://example.com/feed.xml")
	assert.NoError(t, err)
	assert.Equal(t, podcast.Uuid, again.Uuid)
}

func TestCrawlGuidFallsBackToEnclosureURL(t *testing.T) {
	store := newCatalogFake()
	c := &Crawler{DB: store, Fetcher: &fixtureFetcher{file: "testdata/no_guids.xml"}}

	podcast, err := c.EnsurePodcast(context.Background(), "https://example.com/noguid.xml")
	assert.NoError(t, err)

	expected := EpisodeUUID(podcast.Uuid, "https://cdn.example.com/only.mp3")
	_, ok := store.episodes[expected]
	assert.True(t, ok)
}

func TestCrawlNotModifiedOnlyReschedules(t *testing.T) {
	store := newCatalogFake()
	fetcher := &fixtureFetcher{file: "testdata/feed.xml"}
	c := &Crawler{DB: store, Fetcher: fetcher}

	podcast, err := c.EnsurePodcast(context.Background(), "https://example.com/feed.xml")
	assert.NoError(t, err)

	fetcher.notModified = true
	before := len(store.episodes)
	assert.NoError(t, c.Crawl(context.Background(), podcast))
	assert.Equal(t, before, len(store.episodes))
	// conditional headers were sent
	assert.Equal(t, `"etag-1"`, fetcher.gotETag)
}

func TestCrawlFailureRecordsAndBacksOff(t *testing.T) {
	store := newCatalogFake()
	c := &Crawler{DB: store, Fetcher: &fixtureFetcher{err: errors.New("connection refused")}}

	_, err := c.EnsurePodcast(context.Background(), "https://example.com/down.xml")
	assert.Error(t, err)

	uuid := PodcastUUID("https://example.com/down.xml")
	podcast := store.podcasts[uuid]
	assert.Equal(t, "failed", podcast.RefreshStatus)
	assert.Contains(t, podcast.RefreshError, "connection refused")
	assert.True(t, podcast.NextRefreshAt.After(time.Now().Add(time.Hour)))
}

func TestSubscribedRefreshCadence(t *testing.T) {
	store := newCatalogFake()
	fetcher := &fixtureFetcher{file: "testdata/feed.xml"}
	c := &Crawler{DB: store, Fetcher: fetcher}

	uuid := PodcastUUID("https://example.com/feed.xml")
	store.subscribers[uuid] = true

	podcast, err := c.EnsurePodcast(context.Background(), "https://example.com/feed.xml")
	assert.NoError(t, err)

	// subscribed feeds re-crawl within ~an hour, not a day
	assert.True(t, podcast.NextRefreshAt.Before(time.Now().Add(2*time.Hour)))
}

func TestEnsurePodcastRejectsInvalidURL(t *testing.T) {
	c := &Crawler{DB: newCatalogFake(), Fetcher: &fixtureFetcher{}}

	_, err := c.EnsurePodcast(context.Background(), "not a url")
	assert.Error(t, err)
}
