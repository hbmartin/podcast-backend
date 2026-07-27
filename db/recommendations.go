package db

import (
	"context"
	"time"
)

// PodcastSuggestionMetadata is the only catalog material permitted in folder
// suggestion prompts. UUID remains an opaque assignment token.
type PodcastSuggestionMetadata struct {
	Uuid        string
	Title       string
	Author      string
	Category    string
	Description string
}

// GetPodcastSuggestionMetadata intentionally lives outside the sqlc Querier
// interface so existing focused handler mocks do not need unrelated methods.
func (s *pgStore) GetPodcastSuggestionMetadata(ctx context.Context, uuids []string) ([]PodcastSuggestionMetadata, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT uuid::text, title, author, category, description
		FROM podcasts
		WHERE uuid = ANY($1::uuid[])
		ORDER BY uuid`, uuids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]PodcastSuggestionMetadata, 0, len(uuids))
	for rows.Next() {
		var item PodcastSuggestionMetadata
		if err := rows.Scan(&item.Uuid, &item.Title, &item.Author, &item.Category, &item.Description); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

type RecommendationSubscription struct {
	PodcastUuid string
	Category    string
	Explicit    bool
}

type RecommendationSignal struct {
	PodcastUuid string
	Category    string
	Weight      float64
	OccurredAt  time.Time
	Explicit    bool
}

type RecommendationEpisode struct {
	Uuid          string
	PodcastUuid   string
	PodcastTitle  string
	PodcastAuthor string
	Category      string
	Explicit      bool
	Library       bool
	URL           string
	Title         string
	PublishedAt   *time.Time
	DurationSecs  int32
	FileType      string
	FileSize      int64
	EpisodeType   string
	Season        int32
	Number        int32
}

type EpisodeRecommendationData struct {
	Subscriptions []RecommendationSubscription
	Signals       []RecommendationSignal
	Candidates    []RecommendationEpisode
}

func (s *pgStore) GetEpisodeRecommendationData(ctx context.Context, userID int64) (EpisodeRecommendationData, error) {
	var result EpisodeRecommendationData
	subscriptions, err := s.pool.Query(ctx, `
		SELECT p.uuid::text, p.category, p.is_explicit
		FROM user_podcasts up
		JOIN podcasts p ON p.uuid = up.podcast_uuid
		WHERE up.user_id = $1 AND up.subscribed AND NOT up.is_deleted`, userID)
	if err != nil {
		return result, err
	}
	for subscriptions.Next() {
		var item RecommendationSubscription
		if err := subscriptions.Scan(&item.PodcastUuid, &item.Category, &item.Explicit); err != nil {
			subscriptions.Close()
			return result, err
		}
		result.Subscriptions = append(result.Subscriptions, item)
	}
	if err := subscriptions.Err(); err != nil {
		subscriptions.Close()
		return result, err
	}
	subscriptions.Close()

	signals, err := s.pool.Query(ctx, `
		SELECT ue.podcast_uuid::text, p.category,
		       (CASE ue.playing_status WHEN 3 THEN 3.0 WHEN 2 THEN 2.0 ELSE 0.0 END) +
		       (CASE WHEN ue.starred THEN 3.0 ELSE 0.0 END) AS weight,
		       GREATEST(ue.playing_status_modified, ue.played_up_to_modified, ue.starred_modified),
		       p.is_explicit
		FROM user_episodes ue
		JOIN podcasts p ON p.uuid = ue.podcast_uuid
		WHERE ue.user_id = $1 AND (ue.playing_status IN (2,3) OR ue.starred OR ue.played_up_to > 0)
		UNION ALL
		SELECT h.podcast_uuid, p.category, 1.0, h.modified_at, p.is_explicit
		FROM history h
		JOIN podcasts p ON p.uuid::text = h.podcast_uuid
		WHERE h.user_id = $1`, userID)
	if err != nil {
		return result, err
	}
	for signals.Next() {
		var item RecommendationSignal
		var occurredMillis int64
		if err := signals.Scan(&item.PodcastUuid, &item.Category, &item.Weight, &occurredMillis, &item.Explicit); err != nil {
			signals.Close()
			return result, err
		}
		item.OccurredAt = time.UnixMilli(occurredMillis)
		result.Signals = append(result.Signals, item)
	}
	if err := signals.Err(); err != nil {
		signals.Close()
		return result, err
	}
	signals.Close()

	candidates, err := s.pool.Query(ctx, `
		SELECT e.uuid::text, p.uuid::text, p.title, p.author, p.category, p.is_explicit,
		       EXISTS (SELECT 1 FROM user_podcasts up WHERE up.user_id=$1 AND up.podcast_uuid=p.uuid AND up.subscribed AND NOT up.is_deleted),
		       e.audio_url, e.title, e.published_at, e.duration_secs, e.file_type, e.file_size,
		       e.episode_type, e.season, e.number
		FROM episodes e
		JOIN podcasts p ON p.id = e.podcast_id
		WHERE e.published_at >= now() - interval '90 days'
		  AND NOT EXISTS (
		      SELECT 1 FROM user_episodes ue WHERE ue.user_id=$1 AND ue.episode_uuid=e.uuid
		      AND (ue.is_deleted OR ue.playing_status IN (2,3) OR ue.played_up_to > 0 OR ue.starred))
		  AND NOT EXISTS (SELECT 1 FROM history h WHERE h.user_id=$1 AND h.episode_uuid=e.uuid)
		  AND NOT EXISTS (SELECT 1 FROM up_next_items un WHERE un.user_id=$1 AND un.episode_uuid=e.uuid)
		ORDER BY e.published_at DESC
		LIMIT 1000`, userID)
	if err != nil {
		return result, err
	}
	for candidates.Next() {
		var item RecommendationEpisode
		if err := candidates.Scan(&item.Uuid, &item.PodcastUuid, &item.PodcastTitle, &item.PodcastAuthor, &item.Category, &item.Explicit,
			&item.Library, &item.URL, &item.Title, &item.PublishedAt, &item.DurationSecs, &item.FileType, &item.FileSize,
			&item.EpisodeType, &item.Season, &item.Number); err != nil {
			candidates.Close()
			return result, err
		}
		result.Candidates = append(result.Candidates, item)
	}
	if err := candidates.Err(); err != nil {
		candidates.Close()
		return result, err
	}
	candidates.Close()
	return result, nil
}

type RelatedPodcast struct {
	Uuid            string
	FeedURL         string
	Title           string
	Author          string
	Description     string
	WebsiteURL      string
	Category        string
	Explicit        bool
	SubscriberCount int64
	LatestPublished *time.Time
}

type RelatedPodcastData struct {
	Source     RelatedPodcast
	Candidates []RelatedPodcast
}

func (s *pgStore) GetRelatedPodcastData(ctx context.Context, sourceUUID string) (RelatedPodcastData, error) {
	var result RelatedPodcastData
	err := s.pool.QueryRow(ctx, `
		SELECT uuid::text, feed_url, title, author, description, website_url, category, is_explicit,
		       (SELECT count(*) FROM user_podcasts up WHERE up.podcast_uuid=podcasts.uuid AND up.subscribed AND NOT up.is_deleted),
		       latest_episode_published
		FROM podcasts WHERE uuid=$1`, sourceUUID).Scan(
		&result.Source.Uuid, &result.Source.FeedURL, &result.Source.Title, &result.Source.Author,
		&result.Source.Description, &result.Source.WebsiteURL, &result.Source.Category, &result.Source.Explicit,
		&result.Source.SubscriberCount, &result.Source.LatestPublished)
	if err != nil {
		return result, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT p.uuid::text, p.feed_url, p.title, p.author, p.description, p.website_url, p.category, p.is_explicit,
		       count(up.user_id), p.latest_episode_published
		FROM podcasts p
		LEFT JOIN user_podcasts up ON up.podcast_uuid=p.uuid AND up.subscribed AND NOT up.is_deleted
		WHERE p.uuid <> $1
		GROUP BY p.id
		ORDER BY (similarity(p.author, $2) + similarity(p.title, $3) + CASE WHEN p.category=$4 THEN 2 ELSE 0 END) DESC,
		         count(up.user_id) DESC
		LIMIT 250`, sourceUUID, result.Source.Author, result.Source.Title, result.Source.Category)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var item RelatedPodcast
		if err := rows.Scan(&item.Uuid, &item.FeedURL, &item.Title, &item.Author, &item.Description, &item.WebsiteURL,
			&item.Category, &item.Explicit, &item.SubscriberCount, &item.LatestPublished); err != nil {
			return result, err
		}
		result.Candidates = append(result.Candidates, item)
	}
	return result, rows.Err()
}
