// Package crawler ingests podcast RSS feeds into the catalog.
package crawler

import (
	"net/url"
	"strings"

	"github.com/google/uuid"
)

// NsPodcast is the fixed UUIDv5 namespace for this server's podcast ids.
// Podcast uuids are uuid5(NsPodcast, canonical feed URL) and episode uuids
// are uuid5(podcast uuid, guid), so every instance of this server derives
// identical ids for the same feeds — no central registry needed.
var NsPodcast = uuid.MustParse("f0e1d2c3-b4a5-4697-8879-6a5b4c3d2e1f")

// CanonicalFeedURL normalizes a feed URL for identity purposes: lowercase
// scheme and host, no fragment, no trailing slash.
func CanonicalFeedURL(feedURL string) string {
	trimmed := strings.TrimSpace(feedURL)
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return trimmed
	}

	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Fragment = ""
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	return parsed.String()
}

// PodcastUUID derives the deterministic catalog uuid for a feed URL.
func PodcastUUID(feedURL string) string {
	return uuid.NewSHA1(NsPodcast, []byte(CanonicalFeedURL(feedURL))).String()
}

// EpisodeUUID derives the deterministic uuid for an episode. key is the
// feed item's guid, falling back to the enclosure URL when absent.
func EpisodeUUID(podcastUUID string, key string) string {
	ns, err := uuid.Parse(podcastUUID)
	if err != nil {
		ns = uuid.NewSHA1(NsPodcast, []byte(podcastUUID))
	}
	return uuid.NewSHA1(ns, []byte(key)).String()
}
