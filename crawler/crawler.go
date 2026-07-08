package crawler

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/errs"

	"github.com/jackc/pgx/v5"
	"github.com/mmcdole/gofeed"
)

// Refresh cadence: feeds with at least one subscriber are re-crawled hourly,
// idle feeds daily. Failures back off to the idle cadence.
const (
	subscribedRefreshInterval = time.Hour
	idleRefreshInterval       = 24 * time.Hour
	maxEpisodesPerCrawl       = 5000
)

// Crawler fetches feeds and ingests them into the catalog.
type Crawler struct {
	DB      db.Store
	Fetcher Fetcher
	// OnNewEpisodes, when set, is called after a successful crawl that found
	// episodes published after the podcast's previous latest (newest first).
	// The first crawl of a feed never fires it (everything would be "new").
	OnNewEpisodes func(podcastUuid string, episodeUuids []string)
}

// EnsurePodcast makes sure a feed URL exists in the catalog and returns its
// row, crawling it synchronously when it has never been crawled. The row's
// uuid is deterministic, so concurrent callers converge on the same record.
func (c *Crawler) EnsurePodcast(ctx context.Context, feedURL string) (db.Podcast, error) {
	const op errs.Op = "crawler/Crawler.EnsurePodcast"

	canonical := CanonicalFeedURL(feedURL)
	if canonical == "" || !strings.Contains(canonical, "://") {
		return db.Podcast{}, errs.E(op, errs.Invalid, "feed url is invalid")
	}

	podcast, err := c.DB.GetPodcastByFeedURL(ctx, canonical)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return db.Podcast{}, errs.E(op, errs.Database, err)
		}
		podcast, err = c.DB.CreatePodcastPending(ctx, db.CreatePodcastPendingParams{
			Uuid:    PodcastUUID(canonical),
			FeedUrl: canonical,
		})
		if err != nil {
			return db.Podcast{}, errs.E(op, errs.Database, err)
		}
	}

	if podcast.RefreshStatus == "ok" {
		return podcast, nil
	}

	if err := c.Crawl(ctx, podcast); err != nil {
		return db.Podcast{}, err
	}

	podcast, err = c.DB.GetPodcastByUUID(ctx, podcast.Uuid)
	if err != nil {
		return db.Podcast{}, errs.E(op, errs.Database, err)
	}
	return podcast, nil
}

// Crawl fetches and ingests one podcast feed. A conditional-GET 304 only
// reschedules; parse or fetch failures record the error and back off.
func (c *Crawler) Crawl(ctx context.Context, podcast db.Podcast) error {
	const op errs.Op = "crawler/Crawler.Crawl"

	result, err := c.Fetcher.Fetch(ctx, podcast.FeedUrl, podcast.FeedEtag, podcast.FeedLastModified)
	if err != nil {
		return c.recordFailure(ctx, podcast, err)
	}

	next, dbErr := c.nextRefreshAt(ctx, podcast.Uuid)
	if dbErr != nil {
		if result.Body != nil {
			result.Body.Close()
		}
		return errs.E(op, errs.Database, dbErr)
	}

	if result.NotModified {
		return c.DB.UpdatePodcastCrawlNotModified(ctx, db.UpdatePodcastCrawlNotModifiedParams{
			ID:            podcast.ID,
			NextRefreshAt: next,
		})
	}
	defer result.Body.Close()

	feed, err := gofeed.NewParser().Parse(result.Body)
	if err != nil {
		return c.recordFailure(ctx, podcast, err)
	}

	var latestUuid *string
	var latestPublished *time.Time
	var fresh []db.UpsertEpisodeParams

	count := 0
	for _, item := range feed.Items {
		if count >= maxEpisodesPerCrawl {
			break
		}

		episode, ok := episodeFromItem(podcast, item)
		if !ok {
			continue
		}
		count++

		if err := c.DB.UpsertEpisode(ctx, episode); err != nil {
			return errs.E(op, errs.Database, err)
		}

		if episode.PublishedAt != nil && (latestPublished == nil || episode.PublishedAt.After(*latestPublished)) {
			latestPublished = episode.PublishedAt
			uuid := episode.Uuid
			latestUuid = &uuid
		}

		if podcast.LatestEpisodePublished != nil && episode.PublishedAt != nil &&
			episode.PublishedAt.After(*podcast.LatestEpisodePublished) {
			fresh = append(fresh, episode)
		}
	}

	err = c.DB.UpdatePodcastCrawlSuccess(ctx, db.UpdatePodcastCrawlSuccessParams{
		ID:                     podcast.ID,
		Title:                  feed.Title,
		Author:                 feedAuthor(feed),
		Description:            feed.Description,
		ImageUrl:               feedImage(feed),
		WebsiteUrl:             feed.Link,
		Category:               feedCategory(feed),
		Language:               feed.Language,
		MediaType:              "audio",
		ShowType:               feedShowType(feed),
		IsExplicit:             feedExplicit(feed),
		FeedEtag:               result.ETag,
		FeedLastModified:       result.LastModified,
		LatestEpisodeUuid:      latestUuid,
		LatestEpisodePublished: latestPublished,
		ContentModifiedMs:      time.Now().UnixMilli(),
		NextRefreshAt:          next,
	})
	if err != nil {
		return errs.E(op, errs.Database, err)
	}

	if c.OnNewEpisodes != nil && len(fresh) > 0 {
		sort.Slice(fresh, func(i, j int) bool {
			return fresh[i].PublishedAt.After(*fresh[j].PublishedAt)
		})
		uuids := make([]string, len(fresh))
		for i, episode := range fresh {
			uuids[i] = episode.Uuid
		}
		c.OnNewEpisodes(podcast.Uuid, uuids)
	}
	return nil
}

func (c *Crawler) recordFailure(ctx context.Context, podcast db.Podcast, cause error) error {
	const op errs.Op = "crawler/Crawler.recordFailure"

	message := cause.Error()
	if len(message) > 500 {
		message = message[:500]
	}

	err := c.DB.UpdatePodcastCrawlFailure(ctx, db.UpdatePodcastCrawlFailureParams{
		ID:            podcast.ID,
		RefreshError:  message,
		NextRefreshAt: time.Now().Add(idleRefreshInterval),
	})
	if err != nil {
		return errs.E(op, errs.Database, err)
	}
	return errs.E(op, errs.Internal, cause)
}

func (c *Crawler) nextRefreshAt(ctx context.Context, podcastUuid string) (time.Time, error) {
	subscribed, err := c.DB.PodcastHasSubscribers(ctx, podcastUuid)
	if err != nil {
		return time.Time{}, err
	}
	if subscribed {
		return time.Now().Add(subscribedRefreshInterval), nil
	}
	return time.Now().Add(idleRefreshInterval), nil
}

func episodeFromItem(podcast db.Podcast, item *gofeed.Item) (db.UpsertEpisodeParams, bool) {
	audioURL, fileType, fileSize := enclosure(item)
	if audioURL == "" {
		return db.UpsertEpisodeParams{}, false
	}

	key := item.GUID
	if key == "" {
		key = audioURL
	}

	params := db.UpsertEpisodeParams{
		Uuid:         EpisodeUUID(podcast.Uuid, key),
		PodcastID:    podcast.ID,
		Guid:         key,
		Title:        item.Title,
		AudioUrl:     audioURL,
		FileType:     fileType,
		FileSize:     fileSize,
		PublishedAt:  item.PublishedParsed,
		ShowNotes:    showNotes(item),
		DurationSecs: itunesDuration(item),
	}

	if item.ITunesExt != nil {
		params.EpisodeType = item.ITunesExt.EpisodeType
		params.Season = atoiOrZero(item.ITunesExt.Season)
		params.Number = atoiOrZero(item.ITunesExt.Episode)
		params.ImageUrl = item.ITunesExt.Image
	}
	if params.ImageUrl == "" && item.Image != nil {
		params.ImageUrl = item.Image.URL
	}

	return params, true
}

func enclosure(item *gofeed.Item) (url string, fileType string, size int64) {
	for _, enc := range item.Enclosures {
		if enc == nil || enc.URL == "" {
			continue
		}
		return enc.URL, enc.Type, atoi64OrZero(enc.Length)
	}
	return "", "", 0
}

func showNotes(item *gofeed.Item) string {
	if item.Content != "" {
		return item.Content
	}
	return item.Description
}

func itunesDuration(item *gofeed.Item) int32 {
	if item.ITunesExt == nil || item.ITunesExt.Duration == "" {
		return 0
	}
	return parseDurationSeconds(item.ITunesExt.Duration)
}

// parseDurationSeconds handles the iTunes duration formats: plain seconds,
// MM:SS, or HH:MM:SS.
func parseDurationSeconds(value string) int32 {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) > 3 {
		return 0
	}

	total := int32(0)
	for _, part := range parts {
		n := atoiOrZero(strings.TrimSpace(part))
		total = total*60 + n
	}
	return total
}

func atoiOrZero(s string) int32 {
	n := int32(0)
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int32(r-'0')
	}
	return n
}

func atoi64OrZero(s string) int64 {
	n := int64(0)
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int64(r-'0')
	}
	return n
}

func feedAuthor(feed *gofeed.Feed) string {
	if feed.ITunesExt != nil && feed.ITunesExt.Author != "" {
		return feed.ITunesExt.Author
	}
	if len(feed.Authors) > 0 && feed.Authors[0] != nil {
		return feed.Authors[0].Name
	}
	return ""
}

func feedImage(feed *gofeed.Feed) string {
	if feed.ITunesExt != nil && feed.ITunesExt.Image != "" {
		return feed.ITunesExt.Image
	}
	if feed.Image != nil {
		return feed.Image.URL
	}
	return ""
}

func feedCategory(feed *gofeed.Feed) string {
	if feed.ITunesExt != nil && len(feed.ITunesExt.Categories) > 0 && feed.ITunesExt.Categories[0] != nil {
		return feed.ITunesExt.Categories[0].Text
	}
	if len(feed.Categories) > 0 {
		return feed.Categories[0]
	}
	return ""
}

func feedShowType(feed *gofeed.Feed) string {
	if feed.ITunesExt != nil && feed.ITunesExt.Type == "serial" {
		return "serial"
	}
	return "episodic"
}

func feedExplicit(feed *gofeed.Feed) bool {
	return feed.ITunesExt != nil && strings.EqualFold(feed.ITunesExt.Explicit, "yes") ||
		feed.ITunesExt != nil && strings.EqualFold(feed.ITunesExt.Explicit, "true")
}
