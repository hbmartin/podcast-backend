-- name: PingDb :one
SELECT 1 as Result;

-- name: CreateUser :one
INSERT INTO users (uuid, email, password_hash, scope)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetUserByEmail :one
SELECT * FROM users
WHERE email = $1 AND deleted_at IS NULL;

-- name: GetUserByUUID :one
SELECT * FROM users
WHERE uuid = $1 AND deleted_at IS NULL;

-- name: GetUserByID :one
SELECT * FROM users
WHERE id = $1 AND deleted_at IS NULL;

-- name: UpdateUserEmail :execrows
UPDATE users SET email = $2, updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- name: UpdateUserPassword :execrows
UPDATE users SET password_hash = $2, updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- name: SoftDeleteUser :execrows
UPDATE users SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- name: CreateRefreshToken :one
INSERT INTO refresh_tokens (user_id, token_hash, scope, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetRefreshTokenByHash :one
SELECT * FROM refresh_tokens
WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now();

-- name: RevokeRefreshToken :execrows
UPDATE refresh_tokens SET revoked_at = now()
WHERE token_hash = $1 AND revoked_at IS NULL;

-- name: RevokeAllRefreshTokens :execrows
UPDATE refresh_tokens SET revoked_at = now()
WHERE user_id = $1 AND revoked_at IS NULL;

-- name: GetUserForUpdate :one
SELECT * FROM users
WHERE id = $1
FOR UPDATE;

-- name: SetUserSyncLastModified :exec
UPDATE users SET sync_last_modified = $2 WHERE id = $1;

-- name: SetUserUpNextModified :exec
UPDATE users SET up_next_modified = $2 WHERE id = $1;

-- name: SetUserHistoryModified :exec
UPDATE users SET history_modified = $2 WHERE id = $1;

-- name: SetUserHistoryCleared :exec
UPDATE users SET history_cleared_at_ms = $2, history_modified = $3 WHERE id = $1;

-- name: GetUserPodcast :one
SELECT * FROM user_podcasts WHERE user_id = $1 AND podcast_uuid = $2;

-- name: UpsertUserPodcast :exec
INSERT INTO user_podcasts (
    user_id, podcast_uuid, subscribed, is_deleted, auto_start_from,
    auto_skip_last, episodes_sort_order, folder_uuid, sort_position,
    date_added, settings, modified_at, synced_title, synced_feed_url
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
ON CONFLICT (user_id, podcast_uuid) DO UPDATE SET
    synced_title = CASE WHEN EXCLUDED.synced_title <> '' THEN EXCLUDED.synced_title ELSE user_podcasts.synced_title END,
    synced_feed_url = CASE WHEN EXCLUDED.synced_feed_url <> '' THEN EXCLUDED.synced_feed_url ELSE user_podcasts.synced_feed_url END,
    subscribed = EXCLUDED.subscribed,
    is_deleted = EXCLUDED.is_deleted,
    auto_start_from = EXCLUDED.auto_start_from,
    auto_skip_last = EXCLUDED.auto_skip_last,
    episodes_sort_order = EXCLUDED.episodes_sort_order,
    folder_uuid = EXCLUDED.folder_uuid,
    sort_position = EXCLUDED.sort_position,
    date_added = EXCLUDED.date_added,
    settings = EXCLUDED.settings,
    modified_at = EXCLUDED.modified_at;

-- name: GetUserPodcastsModifiedSince :many
SELECT * FROM user_podcasts
WHERE user_id = $1 AND modified_at > $2 AND modified_at <= $3;

-- name: GetSubscribedPodcastsWithCatalog :many
SELECT up.*,
       COALESCE(p.title, '') AS cat_title,
       COALESCE(p.author, '') AS cat_author,
       COALESCE(p.description, '') AS cat_description,
       COALESCE(p.website_url, '') AS cat_website_url,
       p.latest_episode_uuid AS cat_latest_episode_uuid,
       p.latest_episode_published AS cat_latest_episode_published
FROM user_podcasts up
LEFT JOIN podcasts p ON p.uuid = up.podcast_uuid
WHERE up.user_id = $1 AND up.subscribed AND NOT up.is_deleted;

-- name: GetUserEpisode :one
SELECT * FROM user_episodes WHERE user_id = $1 AND episode_uuid = $2;

-- name: UpsertUserEpisode :exec
INSERT INTO user_episodes (
    user_id, episode_uuid, podcast_uuid,
    playing_status, playing_status_modified,
    played_up_to, played_up_to_modified,
    starred, starred_modified,
    is_deleted, is_deleted_modified,
    duration, duration_modified,
    deselected_chapters, deselected_chapters_modified,
    modified_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
ON CONFLICT (user_id, episode_uuid) DO UPDATE SET
    podcast_uuid = EXCLUDED.podcast_uuid,
    playing_status = EXCLUDED.playing_status,
    playing_status_modified = EXCLUDED.playing_status_modified,
    played_up_to = EXCLUDED.played_up_to,
    played_up_to_modified = EXCLUDED.played_up_to_modified,
    starred = EXCLUDED.starred,
    starred_modified = EXCLUDED.starred_modified,
    is_deleted = EXCLUDED.is_deleted,
    is_deleted_modified = EXCLUDED.is_deleted_modified,
    duration = EXCLUDED.duration,
    duration_modified = EXCLUDED.duration_modified,
    deselected_chapters = EXCLUDED.deselected_chapters,
    deselected_chapters_modified = EXCLUDED.deselected_chapters_modified,
    modified_at = EXCLUDED.modified_at;

-- name: GetUserEpisodesModifiedSince :many
SELECT * FROM user_episodes
WHERE user_id = $1 AND modified_at > $2 AND modified_at <= $3;

-- name: GetUserEpisodesForPodcast :many
SELECT * FROM user_episodes
WHERE user_id = $1 AND podcast_uuid = $2;

-- name: GetStarredEpisodes :many
SELECT * FROM user_episodes
WHERE user_id = $1 AND starred;

-- name: GetFolder :one
SELECT * FROM folders WHERE user_id = $1 AND folder_uuid = $2;

-- name: UpsertFolder :exec
INSERT INTO folders (
    user_id, folder_uuid, name, color, sort_position, podcasts_sort_type,
    date_added, is_deleted, modified_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (user_id, folder_uuid) DO UPDATE SET
    name = EXCLUDED.name,
    color = EXCLUDED.color,
    sort_position = EXCLUDED.sort_position,
    podcasts_sort_type = EXCLUDED.podcasts_sort_type,
    date_added = EXCLUDED.date_added,
    is_deleted = EXCLUDED.is_deleted,
    modified_at = EXCLUDED.modified_at;

-- name: GetFoldersModifiedSince :many
SELECT * FROM folders
WHERE user_id = $1 AND modified_at > $2 AND modified_at <= $3;

-- name: GetFolders :many
SELECT * FROM folders
WHERE user_id = $1 AND NOT is_deleted;

-- name: GetPlaylist :one
SELECT * FROM playlists WHERE user_id = $1 AND uuid = $2;

-- name: UpsertPlaylist :exec
INSERT INTO playlists (
    user_id, uuid, original_uuid, title, is_deleted, all_podcasts,
    podcast_uuids, episode_uuids, audio_video, not_downloaded, downloaded,
    downloading, finished, partially_played, unplayed, starred, manual,
    sort_position, sort_type, icon_id, filter_hours, filter_duration,
    longer_than, shorter_than, show_archived, episode_order, episodes,
    modified_at, custom_query
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15,
          $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29)
ON CONFLICT (user_id, uuid) DO UPDATE SET
    original_uuid = EXCLUDED.original_uuid,
    title = EXCLUDED.title,
    is_deleted = EXCLUDED.is_deleted,
    all_podcasts = EXCLUDED.all_podcasts,
    podcast_uuids = EXCLUDED.podcast_uuids,
    episode_uuids = EXCLUDED.episode_uuids,
    audio_video = EXCLUDED.audio_video,
    not_downloaded = EXCLUDED.not_downloaded,
    downloaded = EXCLUDED.downloaded,
    downloading = EXCLUDED.downloading,
    finished = EXCLUDED.finished,
    partially_played = EXCLUDED.partially_played,
    unplayed = EXCLUDED.unplayed,
    starred = EXCLUDED.starred,
    manual = EXCLUDED.manual,
    sort_position = EXCLUDED.sort_position,
    sort_type = EXCLUDED.sort_type,
    icon_id = EXCLUDED.icon_id,
    filter_hours = EXCLUDED.filter_hours,
    filter_duration = EXCLUDED.filter_duration,
    longer_than = EXCLUDED.longer_than,
    shorter_than = EXCLUDED.shorter_than,
    show_archived = EXCLUDED.show_archived,
    episode_order = EXCLUDED.episode_order,
    episodes = EXCLUDED.episodes,
    custom_query = EXCLUDED.custom_query,
    modified_at = EXCLUDED.modified_at;

-- name: GetPlaylistsModifiedSince :many
SELECT * FROM playlists
WHERE user_id = $1 AND modified_at > $2 AND modified_at <= $3;

-- name: GetPlaylists :many
SELECT * FROM playlists
WHERE user_id = $1 AND NOT is_deleted;

-- name: GetBookmark :one
SELECT * FROM bookmarks WHERE user_id = $1 AND bookmark_uuid = $2;

-- name: UpsertBookmark :exec
INSERT INTO bookmarks (
    user_id, bookmark_uuid, podcast_uuid, episode_uuid, time_secs, title,
    title_modified, created_at, is_deleted, is_deleted_modified, modified_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (user_id, bookmark_uuid) DO UPDATE SET
    podcast_uuid = EXCLUDED.podcast_uuid,
    episode_uuid = EXCLUDED.episode_uuid,
    time_secs = EXCLUDED.time_secs,
    title = EXCLUDED.title,
    title_modified = EXCLUDED.title_modified,
    created_at = EXCLUDED.created_at,
    is_deleted = EXCLUDED.is_deleted,
    is_deleted_modified = EXCLUDED.is_deleted_modified,
    modified_at = EXCLUDED.modified_at;

-- name: GetBookmarksModifiedSince :many
SELECT * FROM bookmarks
WHERE user_id = $1 AND modified_at > $2 AND modified_at <= $3;

-- name: GetBookmarks :many
SELECT * FROM bookmarks
WHERE user_id = $1 AND NOT is_deleted;

-- name: GetBookmarksForEpisodes :many
SELECT * FROM bookmarks
WHERE user_id = $1 AND episode_uuid = ANY($2::uuid[]) AND NOT is_deleted;

-- name: UpsertDevice :exec
INSERT INTO devices (
    user_id, device_id, device_type, times_started_at, time_silence_removal,
    time_variable_speed, time_intro_skipping, time_skipping, time_listened,
    updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
ON CONFLICT (user_id, device_id) DO UPDATE SET
    device_type = EXCLUDED.device_type,
    times_started_at = EXCLUDED.times_started_at,
    time_silence_removal = EXCLUDED.time_silence_removal,
    time_variable_speed = EXCLUDED.time_variable_speed,
    time_intro_skipping = EXCLUDED.time_intro_skipping,
    time_skipping = EXCLUDED.time_skipping,
    time_listened = EXCLUDED.time_listened,
    updated_at = now();

-- name: GetUpNextItems :many
SELECT * FROM up_next_items
WHERE user_id = $1
ORDER BY position;

-- name: DeleteAllUpNextItems :exec
DELETE FROM up_next_items WHERE user_id = $1;

-- name: InsertUpNextItem :exec
INSERT INTO up_next_items (
    user_id, episode_uuid, podcast_uuid, title, url, published, position
) VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: UpsertHistoryItem :exec
INSERT INTO history (
    user_id, episode_uuid, podcast_uuid, title, url, published, modified_at
) VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (user_id, episode_uuid) DO UPDATE SET
    podcast_uuid = EXCLUDED.podcast_uuid,
    title = EXCLUDED.title,
    url = EXCLUDED.url,
    published = EXCLUDED.published,
    modified_at = EXCLUDED.modified_at
WHERE EXCLUDED.modified_at > history.modified_at;

-- name: DeleteHistoryItem :exec
DELETE FROM history WHERE user_id = $1 AND episode_uuid = $2;

-- name: DeleteHistoryBefore :exec
DELETE FROM history WHERE user_id = $1 AND modified_at <= $2;

-- name: TrimHistory :exec
DELETE FROM history
WHERE history.user_id = $1 AND history.episode_uuid NOT IN (
    SELECT h.episode_uuid FROM history h
    WHERE h.user_id = $1
    ORDER BY h.modified_at DESC
    LIMIT $2
);

-- name: GetHistory :many
SELECT * FROM history
WHERE user_id = $1
ORDER BY modified_at DESC
LIMIT $2;

-- name: GetUserSettings :one
SELECT * FROM user_settings WHERE user_id = $1;

-- name: UpsertUserSettings :exec
INSERT INTO user_settings (user_id, settings, modified_at)
VALUES ($1, $2, $3)
ON CONFLICT (user_id) DO UPDATE SET
    settings = EXCLUDED.settings,
    modified_at = EXCLUDED.modified_at;

-- name: CreatePodcastPending :one
INSERT INTO podcasts (uuid, feed_url)
VALUES ($1, $2)
ON CONFLICT (uuid) DO UPDATE SET uuid = EXCLUDED.uuid
RETURNING *;

-- name: GetPodcastByUUID :one
SELECT * FROM podcasts WHERE uuid = $1;

-- name: GetPodcastByFeedURL :one
SELECT * FROM podcasts WHERE feed_url = $1;

-- name: GetPodcastsByUUIDs :many
SELECT * FROM podcasts WHERE uuid = ANY($1::uuid[]);

-- name: GetDuePodcasts :many
SELECT * FROM podcasts
WHERE next_refresh_at <= now()
ORDER BY next_refresh_at
LIMIT $1;

-- name: PodcastHasSubscribers :one
SELECT EXISTS (
    SELECT 1 FROM user_podcasts
    WHERE podcast_uuid = $1 AND subscribed AND NOT is_deleted
) AS has_subscribers;

-- name: UpdatePodcastCrawlSuccess :exec
UPDATE podcasts SET
    title = $2,
    author = $3,
    description = $4,
    image_url = $5,
    website_url = $6,
    category = $7,
    language = $8,
    media_type = $9,
    show_type = $10,
    is_explicit = $11,
    feed_etag = $12,
    feed_last_modified = $13,
    latest_episode_uuid = $14,
    latest_episode_published = $15,
    content_modified_ms = $16,
    refresh_status = 'ok',
    refresh_error = '',
    last_refresh_at = now(),
    next_refresh_at = $17,
    updated_at = now()
WHERE id = $1;

-- name: UpdatePodcastCrawlNotModified :exec
UPDATE podcasts SET
    last_refresh_at = now(),
    next_refresh_at = $2,
    updated_at = now()
WHERE id = $1;

-- name: UpdatePodcastCrawlFailure :exec
UPDATE podcasts SET
    refresh_status = CASE WHEN refresh_status = 'ok' THEN 'ok' ELSE 'failed' END,
    refresh_error = $2,
    last_refresh_at = now(),
    next_refresh_at = $3,
    updated_at = now()
WHERE id = $1;

-- name: UpsertEpisode :exec
INSERT INTO episodes (
    uuid, podcast_id, guid, title, audio_url, file_type, file_size,
    duration_secs, published_at, episode_type, season, number, show_notes,
    image_url, transcripts, chapters_url
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
ON CONFLICT (podcast_id, guid) DO UPDATE SET
    title = EXCLUDED.title,
    audio_url = EXCLUDED.audio_url,
    file_type = EXCLUDED.file_type,
    file_size = EXCLUDED.file_size,
    duration_secs = EXCLUDED.duration_secs,
    published_at = EXCLUDED.published_at,
    episode_type = EXCLUDED.episode_type,
    season = EXCLUDED.season,
    number = EXCLUDED.number,
    show_notes = EXCLUDED.show_notes,
    image_url = EXCLUDED.image_url,
    transcripts = EXCLUDED.transcripts,
    chapters_url = EXCLUDED.chapters_url,
    updated_at = now();

-- name: GetEpisodesByPodcastID :many
SELECT * FROM episodes
WHERE podcast_id = $1
ORDER BY published_at DESC NULLS LAST
LIMIT $2;

-- name: GetEpisodeByUUID :one
SELECT * FROM episodes WHERE uuid = $1;

-- name: GetEpisodesPublishedAfter :many
SELECT * FROM episodes
WHERE podcast_id = $1 AND published_at > $2
ORDER BY published_at DESC
LIMIT $3;

-- name: GetPodcastByID :one
SELECT * FROM podcasts WHERE id = $1;

-- name: SearchPodcasts :many
SELECT * FROM podcasts
WHERE refresh_status = 'ok'
  AND (title ILIKE '%' || $1 || '%' OR author ILIKE '%' || $1 || '%')
ORDER BY similarity(title, $1) DESC
LIMIT $2;

-- name: SearchEpisodesGlobal :many
SELECT e.*, p.uuid AS parent_podcast_uuid, p.title AS parent_podcast_title
FROM episodes e
JOIN podcasts p ON p.id = e.podcast_id
WHERE e.title ILIKE '%' || $1 || '%'
ORDER BY e.published_at DESC NULLS LAST
LIMIT $2;

-- name: SearchEpisodesInPodcast :many
SELECT e.* FROM episodes e
JOIN podcasts p ON p.id = e.podcast_id
WHERE p.uuid = $1 AND e.title ILIKE '%' || $2 || '%'
ORDER BY e.published_at DESC NULLS LAST
LIMIT $3;

-- name: UpsertPodcastRating :exec
INSERT INTO podcast_ratings (user_id, podcast_uuid, rating, modified_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (user_id, podcast_uuid) DO UPDATE SET
    rating = EXCLUDED.rating,
    modified_at = now();

-- name: GetPodcastRating :one
SELECT * FROM podcast_ratings WHERE user_id = $1 AND podcast_uuid = $2;

-- name: GetUserPodcastRatings :many
SELECT * FROM podcast_ratings
WHERE user_id = $1
ORDER BY modified_at DESC;

-- name: GetPodcastRatingAggregate :one
SELECT COUNT(*)::bigint AS total, COALESCE(AVG(rating), 0)::float8 AS average
FROM podcast_ratings
WHERE podcast_uuid = $1;

-- name: GetDevice :one
SELECT * FROM devices WHERE user_id = $1 AND device_id = $2;

-- name: GetUserStatsTotals :one
SELECT COALESCE(SUM(time_silence_removal), 0)::bigint AS time_silence_removal,
       COALESCE(SUM(time_skipping), 0)::bigint AS time_skipping,
       COALESCE(SUM(time_intro_skipping), 0)::bigint AS time_intro_skipping,
       COALESCE(SUM(time_variable_speed), 0)::bigint AS time_variable_speed,
       COALESCE(SUM(time_listened), 0)::bigint AS time_listened,
       COALESCE(MIN(NULLIF(times_started_at, 0)), 0)::bigint AS earliest_started_at,
       COALESCE(MIN(created_at), now())::timestamptz AS earliest_created_at
FROM devices
WHERE user_id = $1;

-- name: UpdatePodcastColors :exec
UPDATE podcasts SET
    background_color = $2,
    tint_for_light_bg = $3,
    tint_for_dark_bg = $4,
    colors_source_image_url = $5
WHERE id = $1;

-- name: TopPodcastsBySubscribers :many
SELECT p.*, COUNT(up.user_id)::bigint AS subscriber_count
FROM podcasts p
JOIN user_podcasts up ON up.podcast_uuid = p.uuid
WHERE up.subscribed AND NOT up.is_deleted AND p.refresh_status = 'ok'
GROUP BY p.id
ORDER BY COUNT(up.user_id) DESC, p.latest_episode_published DESC NULLS LAST
LIMIT $1;

-- name: RecentPodcasts :many
SELECT * FROM podcasts
WHERE refresh_status = 'ok' AND latest_episode_published IS NOT NULL
ORDER BY latest_episode_published DESC
LIMIT $1;

-- name: DistinctCategories :many
SELECT category, COUNT(*)::bigint AS podcast_count
FROM podcasts
WHERE refresh_status = 'ok' AND category <> ''
GROUP BY category
ORDER BY COUNT(*) DESC, category;

-- name: PodcastsByCategory :many
SELECT * FROM podcasts
WHERE refresh_status = 'ok' AND category = $1
ORDER BY title
LIMIT $2;

-- name: CreateSharedList :one
INSERT INTO shared_lists (code, title, description, podcast_uuids)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetSharedListByCode :one
SELECT * FROM shared_lists WHERE code = $1;

-- name: UpsertDevicePush :exec
-- The client omits push_token unless it holds one, so an empty incoming
-- token keeps whatever was registered before.
INSERT INTO devices (user_id, device_id, push_token, push_on, updated_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (user_id, device_id) DO UPDATE SET
    push_token = CASE WHEN EXCLUDED.push_token <> '' THEN EXCLUDED.push_token ELSE devices.push_token END,
    push_on = EXCLUDED.push_on,
    updated_at = now();

-- name: SetPodcastNotifyFlags :exec
UPDATE user_podcasts
SET notify_enabled = (podcast_uuid = ANY(@notify_uuids::uuid[]))
WHERE user_id = $1 AND NOT is_deleted;

-- name: GetPushTargetsForPodcast :many
SELECT d.user_id, d.device_id, d.push_token
FROM devices d
JOIN user_podcasts up ON up.user_id = d.user_id
WHERE up.podcast_uuid = $1
  AND up.subscribed AND NOT up.is_deleted
  AND d.push_on AND d.push_token <> ''
  AND (up.notify_enabled
       OR COALESCE((up.settings->'notification'->>'value')::boolean, false));

-- name: ClearPushToken :exec
UPDATE devices SET push_token = '', updated_at = now()
WHERE push_token = $1;

-- name: InsertFeedback :exec
INSERT INTO feedback (user_id, message, subject, inbox, logs, bitdrift_session_id, device_info, app_version)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- ============================================================================
-- App Attest (docs/AppAttest.md §2)
-- ============================================================================

-- name: InsertChallenge :exec
INSERT INTO attest_challenges (challenge, expires_at)
VALUES ($1, $2);

-- name: ConsumeChallenge :one
-- Single-use: deletes the challenge and returns it only when present and
-- unexpired. No row => unknown or expired challenge.
DELETE FROM attest_challenges
WHERE challenge = $1 AND expires_at > now()
RETURNING challenge;

-- name: DeleteExpiredChallenges :exec
DELETE FROM attest_challenges WHERE expires_at <= now();

-- name: InsertAttestKey :exec
-- Idempotent enrollment: a re-enroll of an existing key_id (same Secure Enclave
-- key) is a no-op, preserving the stored monotonic counter and any revoked
-- status. key_id == base64(SHA256(public key)), so a differing key is a
-- different row.
INSERT INTO attest_keys (key_id, public_key, counter, receipt, environment)
VALUES ($1, $2, 0, $3, $4)
ON CONFLICT (key_id) DO NOTHING;

-- name: GetAttestKey :one
SELECT * FROM attest_keys WHERE key_id = $1;

-- name: AdvanceAttestCounter :execrows
-- Atomic compare-and-update (docs/AppAttest.md §2.2 step 3): accept only a
-- strictly greater counter on an active key. Zero rows affected => unknown,
-- revoked, or non-increasing counter (the caller re-reads to classify).
UPDATE attest_keys
SET counter = $2, last_used_at = now()
WHERE key_id = $1 AND status = 'active' AND counter < $2;

-- ============================================================================
-- Transcript contributions & sightings (docs/TranscriptContributions.md §4)
-- ============================================================================

-- name: InsertTranscriptContribution :exec
INSERT INTO transcript_contributions (
    episode_uuid, podcast_uuid, vtt_blob, fingerprint_blob, engine, model_id,
    language, diarized, app_version, episode_duration_seconds, created_at,
    attribution, attribution_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13);

-- name: CountRecentContributionsByAttribution :one
SELECT count(*) FROM transcript_contributions
WHERE attribution = $1 AND attribution_id = $2 AND received_at > $3;

-- name: InsertTranscriptSighting :one
-- Dedup on (episode_uuid, transcript_url). A conflict returns no row, which the
-- caller reads as "already sighted" (no fetch enqueued).
INSERT INTO transcript_sightings (
    episode_uuid, podcast_uuid, transcript_url, format, language,
    attribution, attribution_id
) VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (episode_uuid, transcript_url) DO NOTHING
RETURNING id;

-- name: CountRecentSightingsByAttribution :one
SELECT count(*) FROM transcript_sightings
WHERE attribution = $1 AND attribution_id = $2 AND received_at > $3;

-- name: GetTranscriptSighting :one
SELECT * FROM transcript_sightings WHERE id = $1;

-- name: UpdateSightingContent :exec
UPDATE transcript_sightings
SET content = $2, content_type = $3, status = $4, fetched_at = now()
WHERE id = $1;

-- name: MarkSightingStatus :exec
UPDATE transcript_sightings SET status = $2 WHERE id = $1;

-- ============================================================================
-- Social identity + moderation (pocket-casts-ios docs/Social.md, ADR-0005/6/7)
-- ============================================================================

-- name: GetHandleStatus :one
-- No row means the handle is available (pgx.ErrNoRows at the caller).
SELECT handle, user_id, status FROM social_handles WHERE handle = $1;

-- name: ClaimHandle :exec
-- A bare INSERT: the PRIMARY KEY rejects a concurrent duplicate claim and the
-- user_id UNIQUE rejects a second handle for the same account (both surface as
-- 23505). Runs inside the join transaction with CreateSocialProfile.
INSERT INTO social_handles (handle, user_id, status) VALUES ($1, $2, 0);

-- name: CreateSocialProfile :one
-- Visibility columns keep their private-by-default column defaults (ADR-0006).
INSERT INTO social_profiles (user_id, handle, display_name, terms_version)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetSocialProfileByUserID :one
SELECT * FROM social_profiles WHERE user_id = $1;

-- name: GetSocialProfileByHandle :one
-- Tombstoned/erased handles have no profile row, so this only finds live ones.
SELECT * FROM social_profiles WHERE handle = $1;

-- name: UpdateSocialProfile :one
-- The handle is immutable and deliberately absent here (ADR-0005).
UPDATE social_profiles SET
    display_name = $2,
    bio = $3,
    avatar_visibility = $4,
    bio_visibility = $5,
    followed_shows_visibility = $6,
    top_podcasts_visibility = $7,
    stats_visibility = $8,
    history_visibility = $9,
    presence_visibility = $10,
    require_follow_approval = $11,
    social_push_disabled = $12,
    hide_from_discovery = $13,
    updated_at = now()
WHERE user_id = $1
RETURNING *;

-- name: UpsertSocialRelationship :exec
-- Idempotent block/mute (kind 0 = block, 1 = mute).
INSERT INTO social_relationships (user_id, target_user_id, kind)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, target_user_id, kind) DO NOTHING;

-- name: DeleteSocialRelationship :execrows
DELETE FROM social_relationships
WHERE user_id = $1 AND target_user_id = $2 AND kind = $3;

-- name: HasSocialRelationship :one
SELECT EXISTS (
    SELECT 1 FROM social_relationships
    WHERE user_id = $1 AND target_user_id = $2 AND kind = $3
);

-- name: IsBlockedEither :one
-- Mutual invisibility: a block in either direction hides the profile
-- (docs/SocialModeration.md).
SELECT EXISTS (
    SELECT 1 FROM social_relationships
    WHERE kind = 0
      AND ((user_id = $1 AND target_user_id = $2)
        OR (user_id = $2 AND target_user_id = $1))
) AS blocked;

-- name: InsertModerationReport :exec
INSERT INTO moderation_reports (target_user_id, reporter_user_id, source, reason, context, target_type, content_ref)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: DeleteSocialProfile :execrows
DELETE FROM social_profiles WHERE user_id = $1;

-- name: TombstoneHandle :execrows
-- GDPR erase: keep the handle string forever as a non-PII reservation, drop
-- the account association (ADR-0005).
UPDATE social_handles
SET status = 1, user_id = NULL, released_at = now()
WHERE user_id = $1 AND status = 0;

-- name: DeleteRelationshipsForUser :exec
DELETE FROM social_relationships WHERE user_id = $1 OR target_user_id = $1;

-- name: GetPublicFollowedShows :many
-- Followed-shows section of a public profile. LEFT JOIN: subscriptions may
-- reference feeds the catalog hasn't ingested; they surface with empty titles.
SELECT up.podcast_uuid, COALESCE(p.title, '') AS title, COALESCE(p.author, '') AS author
FROM user_podcasts up
LEFT JOIN podcasts p ON p.uuid = up.podcast_uuid
WHERE up.user_id = $1 AND up.subscribed AND NOT up.is_deleted
ORDER BY COALESCE(p.title, '') ASC
LIMIT $2;

-- name: GetPublicTopPodcasts :many
-- Top-podcasts section: ranked by summed playback position across episodes —
-- the best per-podcast listening signal the sync data carries.
SELECT ue.podcast_uuid, COALESCE(p.title, '') AS title, COALESCE(p.author, '') AS author,
       SUM(ue.played_up_to)::bigint AS played_seconds
FROM user_episodes ue
LEFT JOIN podcasts p ON p.uuid = ue.podcast_uuid
WHERE ue.user_id = $1
GROUP BY ue.podcast_uuid, p.title, p.author
HAVING SUM(ue.played_up_to) > 0
ORDER BY played_seconds DESC
LIMIT $2;

-- name: GetPublicRecentlyPlayed :many
-- Recently-played section, straight from listening history (title is already
-- denormalized on the history row). modified_at is interaction millis.
SELECT episode_uuid, podcast_uuid, title, modified_at
FROM history
WHERE user_id = $1
ORDER BY modified_at DESC
LIMIT $2;

-- ============================================================================
-- Written reviews + episode reactions (Slice 3)
-- ============================================================================

-- name: UpsertPodcastReview :one
INSERT INTO podcast_reviews (user_id, podcast_uuid, text)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, podcast_uuid) DO UPDATE SET
    text = EXCLUDED.text,
    updated_at = now()
RETURNING *;

-- name: DeletePodcastReview :execrows
DELETE FROM podcast_reviews WHERE user_id = $1 AND podcast_uuid = $2;

-- name: DeleteReviewsForUser :exec
-- GDPR erase: attributed review text dies with the social profile.
DELETE FROM podcast_reviews WHERE user_id = $1;

-- name: GetPodcastReviews :many
-- Public review list, newest first. Joins the author's profile (text reviews
-- require a joined account, so the join always matches live authors) and the
-- author's star rating when they have one.
SELECT r.user_id, r.podcast_uuid, r.text, r.created_at, r.updated_at,
       u.uuid AS author_uuid,
       sp.handle, sp.display_name,
       COALESCE(pr.rating, 0)::smallint AS rating
FROM podcast_reviews r
JOIN users u ON u.id = r.user_id
JOIN social_profiles sp ON sp.user_id = r.user_id
LEFT JOIN podcast_ratings pr ON pr.user_id = r.user_id AND pr.podcast_uuid = r.podcast_uuid
WHERE r.podcast_uuid = $1
  AND (sqlc.narg('viewer')::bigint IS NULL OR r.user_id IS NULL OR NOT EXISTS (
    SELECT 1 FROM social_relationships sr
    WHERE (sr.kind = 0 AND ((sr.user_id = sqlc.narg('viewer') AND sr.target_user_id = r.user_id)
                         OR (sr.user_id = r.user_id AND sr.target_user_id = sqlc.narg('viewer'))))
       OR (sr.kind = 1 AND sr.user_id = sqlc.narg('viewer') AND sr.target_user_id = r.user_id)))
ORDER BY r.created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountPodcastReviews :one
SELECT count(*) FROM podcast_reviews r WHERE r.podcast_uuid = $1
  AND (sqlc.narg('viewer')::bigint IS NULL OR r.user_id IS NULL OR NOT EXISTS (
    SELECT 1 FROM social_relationships sr
    WHERE (sr.kind = 0 AND ((sr.user_id = sqlc.narg('viewer') AND sr.target_user_id = r.user_id)
                         OR (sr.user_id = r.user_id AND sr.target_user_id = sqlc.narg('viewer'))))
       OR (sr.kind = 1 AND sr.user_id = sqlc.narg('viewer') AND sr.target_user_id = r.user_id)));

-- name: GetOwnPodcastReview :one
SELECT r.user_id, r.podcast_uuid, r.text, r.created_at, r.updated_at,
       u.uuid AS author_uuid,
       sp.handle, sp.display_name,
       COALESCE(pr.rating, 0)::smallint AS rating
FROM podcast_reviews r
JOIN users u ON u.id = r.user_id
JOIN social_profiles sp ON sp.user_id = r.user_id
LEFT JOIN podcast_ratings pr ON pr.user_id = r.user_id AND pr.podcast_uuid = r.podcast_uuid
WHERE r.user_id = $1 AND r.podcast_uuid = $2;

-- name: CountPlayedEpisodesOfPodcast :one
-- Server-side listen-gate parity: episodes of this podcast the user has played
-- at least half of, mirroring the client's played-episode heuristic. Duration
-- is the client-synced per-episode value on user_episodes itself.
SELECT count(*) FROM user_episodes ue
WHERE ue.user_id = $1 AND ue.podcast_uuid = $2
  AND ue.duration > 0 AND ue.played_up_to > (ue.duration / 2);

-- name: UpsertEpisodeReaction :exec
INSERT INTO episode_reactions (user_id, episode_uuid, kind)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, episode_uuid) DO UPDATE SET
    kind = EXCLUDED.kind,
    created_at = now();

-- name: DeleteEpisodeReaction :execrows
DELETE FROM episode_reactions WHERE user_id = $1 AND episode_uuid = $2;

-- name: GetEpisodeReactionCounts :many
SELECT kind, count(*)::bigint AS count
FROM episode_reactions
WHERE episode_uuid = $1
GROUP BY kind
ORDER BY kind;

-- name: GetOwnEpisodeReaction :one
SELECT kind FROM episode_reactions WHERE user_id = $1 AND episode_uuid = $2;

-- ============================================================================
-- Send-to-friend shared items (Slice 4)
-- ============================================================================

-- name: InsertSharedItem :one
INSERT INTO shared_items (sender_user_id, recipient_user_id, episode_uuid, podcast_uuid,
                          episode_title, podcast_title, note, timestamp_seconds)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id;

-- name: GetInboxItems :many
-- The recipient's inbox, newest first, with the sender's live attribution.
SELECT si.id, si.episode_uuid, si.podcast_uuid, si.episode_title, si.podcast_title,
       si.note, si.timestamp_seconds, si.created_at, (si.read_at IS NOT NULL)::boolean AS read,
       u.uuid AS sender_uuid, sp.handle AS sender_handle, sp.display_name AS sender_display_name
FROM shared_items si
JOIN users u ON u.id = si.sender_user_id
JOIN social_profiles sp ON sp.user_id = si.sender_user_id
WHERE si.recipient_user_id = $1
  AND (sqlc.narg('viewer')::bigint IS NULL OR si.sender_user_id IS NULL OR NOT EXISTS (
    SELECT 1 FROM social_relationships sr
    WHERE (sr.kind = 0 AND ((sr.user_id = sqlc.narg('viewer') AND sr.target_user_id = si.sender_user_id)
                         OR (sr.user_id = si.sender_user_id AND sr.target_user_id = sqlc.narg('viewer'))))
       OR (sr.kind = 1 AND sr.user_id = sqlc.narg('viewer') AND sr.target_user_id = si.sender_user_id)))
ORDER BY si.created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountInboxItems :one
SELECT count(*) FROM shared_items si
JOIN social_profiles sp ON sp.user_id = si.sender_user_id
WHERE si.recipient_user_id = $1
  AND (sqlc.narg('viewer')::bigint IS NULL OR si.sender_user_id IS NULL OR NOT EXISTS (
    SELECT 1 FROM social_relationships sr
    WHERE (sr.kind = 0 AND ((sr.user_id = sqlc.narg('viewer') AND sr.target_user_id = si.sender_user_id)
                         OR (sr.user_id = si.sender_user_id AND sr.target_user_id = sqlc.narg('viewer'))))
       OR (sr.kind = 1 AND sr.user_id = sqlc.narg('viewer') AND sr.target_user_id = si.sender_user_id)));

-- name: CountUnreadInboxItems :one
SELECT count(*) FROM shared_items si
JOIN social_profiles sp ON sp.user_id = si.sender_user_id
WHERE si.recipient_user_id = $1 AND si.read_at IS NULL
  AND (sqlc.narg('viewer')::bigint IS NULL OR si.sender_user_id IS NULL OR NOT EXISTS (
    SELECT 1 FROM social_relationships sr
    WHERE (sr.kind = 0 AND ((sr.user_id = sqlc.narg('viewer') AND sr.target_user_id = si.sender_user_id)
                         OR (sr.user_id = si.sender_user_id AND sr.target_user_id = sqlc.narg('viewer'))))
       OR (sr.kind = 1 AND sr.user_id = sqlc.narg('viewer') AND sr.target_user_id = si.sender_user_id)));

-- name: MarkInboxItemsRead :exec
UPDATE shared_items SET read_at = now()
WHERE recipient_user_id = $1 AND id = ANY($2::bigint[]) AND read_at IS NULL;

-- name: DeleteInboxItem :execrows
DELETE FROM shared_items WHERE recipient_user_id = $1 AND id = $2;

-- name: DeleteSharedItemsForUser :exec
-- GDPR erase: items the erased profile sent disappear from recipients' inboxes
-- (attributed UGC); their received items go too.
DELETE FROM shared_items WHERE sender_user_id = $1 OR recipient_user_id = $1;

-- ============================================================================
-- Follow graph + activity feed (Slice 5; ADR-0009: feed derives at read time)
-- ============================================================================

-- name: UpsertFollow :execrows
-- Idempotent; re-following while pending keeps pending (no status escalation).
INSERT INTO social_follows (follower_user_id, followee_user_id, status, approved_at)
VALUES ($1, $2, $3, CASE WHEN $3::smallint = 1 THEN now() ELSE NULL END)
ON CONFLICT (follower_user_id, followee_user_id) DO NOTHING;

-- name: DeleteFollow :execrows
DELETE FROM social_follows WHERE follower_user_id = $1 AND followee_user_id = $2;

-- name: ApproveFollow :execrows
UPDATE social_follows SET status = 1, approved_at = now()
WHERE follower_user_id = $1 AND followee_user_id = $2 AND status = 0;

-- name: GetFollowState :one
SELECT status FROM social_follows WHERE follower_user_id = $1 AND followee_user_id = $2;

-- name: CountFollowers :one
SELECT count(*) FROM social_follows WHERE followee_user_id = $1 AND status = 1;

-- name: CountFollowing :one
SELECT count(*) FROM social_follows WHERE follower_user_id = $1 ;

-- name: GetFollowers :many
SELECT u.uuid AS user_uuid, sp.handle, sp.display_name, sf.status
FROM social_follows sf
JOIN users u ON u.id = sf.follower_user_id
JOIN social_profiles sp ON sp.user_id = sf.follower_user_id
WHERE sf.followee_user_id = $1 AND sf.status = 1
ORDER BY sf.created_at DESC LIMIT $2 OFFSET $3;

-- name: GetFollowing :many
SELECT u.uuid AS user_uuid, sp.handle, sp.display_name, sf.status
FROM social_follows sf
JOIN users u ON u.id = sf.followee_user_id
JOIN social_profiles sp ON sp.user_id = sf.followee_user_id
WHERE sf.follower_user_id = $1
ORDER BY sf.created_at DESC LIMIT $2 OFFSET $3;

-- name: GetPendingFollowRequests :many
SELECT u.uuid AS user_uuid, sp.handle, sp.display_name, sf.status
FROM social_follows sf
JOIN users u ON u.id = sf.follower_user_id
JOIN social_profiles sp ON sp.user_id = sf.follower_user_id
WHERE sf.followee_user_id = $1 AND sf.status = 0
ORDER BY sf.created_at DESC LIMIT $2 OFFSET $3;

-- name: DeleteFollowsForUser :exec
DELETE FROM social_follows WHERE follower_user_id = $1 OR followee_user_id = $1;

-- name: GetFeedItems :many
-- The activity feed (ADR-0009): derived at read time from the caller's ACTIVE
-- followees' existing rows. Listening-derived kinds obey the actor's
-- per-field visibility (2 = public, 3 = followers_only — the viewer IS a
-- follower here, so both pass; 1 = private never appears). Muted actors are
-- excluded (blocks can't occur: a block severs follows). Kinds mirror the
-- proto FeedItemKind values.
WITH followees AS (
    SELECT sf.followee_user_id AS uid
    FROM social_follows sf
    WHERE sf.follower_user_id = $1 AND sf.status = 1
      AND NOT EXISTS (
        SELECT 1 FROM social_relationships sr
        WHERE sr.user_id = $1 AND sr.target_user_id = sf.followee_user_id AND sr.kind = 1)
)
SELECT events.kind, events.podcast_uuid, events.podcast_title,
       events.episode_uuid, events.episode_title, events.target_handle,
       events.reaction_kind, events.review_excerpt, events.event_at,
       events.list_title, events.list_id,
       actor.handle AS actor_handle, actor.display_name AS actor_display_name,
       au.uuid AS actor_uuid
FROM (
    SELECT 1 AS kind, sp.user_id AS actor_id, '' AS podcast_uuid, '' AS podcast_title,
           '' AS episode_uuid, '' AS episode_title, '' AS target_handle,
           0 AS reaction_kind, '' AS review_excerpt, sp.created_at AS event_at,
           '' AS list_title, 0::bigint AS list_id
    FROM social_profiles sp JOIN followees f ON f.uid = sp.user_id
  UNION ALL
    SELECT 2, sf.follower_user_id, '', '', '', '',
           tp.handle::text, 0, '', COALESCE(sf.approved_at, sf.created_at),
           '', 0::bigint
    FROM social_follows sf
    JOIN followees f ON f.uid = sf.follower_user_id
    JOIN social_profiles tp ON tp.user_id = sf.followee_user_id
    WHERE sf.status = 1
      -- never surface a handle the viewer is blocked with (QA finding)
      AND NOT EXISTS (
        SELECT 1 FROM social_relationships br
        WHERE br.kind = 0
          AND ((br.user_id = $1 AND br.target_user_id = sf.followee_user_id)
            OR (br.user_id = sf.followee_user_id AND br.target_user_id = $1)))
  UNION ALL
    SELECT 3, up.user_id, up.podcast_uuid::text,
           COALESCE(NULLIF(p.title, ''), NULLIF(up.synced_title, ''), ''), '', '', '', 0, '',
           up.date_added,
           '', 0::bigint
    FROM user_podcasts up
    JOIN followees f ON f.uid = up.user_id
    JOIN social_profiles sp ON sp.user_id = up.user_id AND sp.followed_shows_visibility IN (2, 3)
    LEFT JOIN podcasts p ON p.uuid = up.podcast_uuid
    WHERE up.subscribed AND NOT up.is_deleted AND up.date_added IS NOT NULL
  UNION ALL
    SELECT 4, ue.user_id, ue.podcast_uuid::text, COALESCE(p.title, ''),
           ue.episode_uuid::text, COALESCE(e.title, ''), '', 0, '',
           to_timestamp(ue.playing_status_modified / 1000.0),
           '', 0::bigint
    FROM user_episodes ue
    JOIN followees f ON f.uid = ue.user_id
    JOIN social_profiles sp ON sp.user_id = ue.user_id AND sp.history_visibility IN (2, 3)
    LEFT JOIN episodes e ON e.uuid = ue.episode_uuid
    LEFT JOIN podcasts p ON p.uuid = ue.podcast_uuid
    WHERE ue.playing_status = 3 AND ue.playing_status_modified > 0
  UNION ALL
    SELECT 5, pr.user_id, pr.podcast_uuid::text, COALESCE(p.title, ''), '', '', '', 0,
           left(pr.text, 200), pr.updated_at,
           '', 0::bigint
    FROM podcast_reviews pr
    JOIN followees f ON f.uid = pr.user_id
    LEFT JOIN podcasts p ON p.uuid = pr.podcast_uuid
  UNION ALL
    SELECT 6, er.user_id, '', '', er.episode_uuid, COALESCE(e.title, ''), '',
           er.kind::int, '', er.created_at,
           '', 0::bigint
    FROM episode_reactions er
    JOIN followees f ON f.uid = er.user_id
    -- reactions are listening-derived: same history gate as finished-episode
    JOIN social_profiles rp ON rp.user_id = er.user_id AND rp.history_visibility IN (2, 3)
    LEFT JOIN episodes e ON e.uuid::text = er.episode_uuid
  UNION ALL
    SELECT 7, ec.user_id, ec.podcast_uuid, ec.podcast_title,
           ec.episode_uuid, ec.episode_title, '', 0, left(ec.text, 200), ec.created_at,
           '', 0::bigint
    FROM episode_comments ec
    JOIN followees f ON f.uid = ec.user_id
    WHERE ec.parent_id IS NULL AND ec.removed_at IS NULL
  UNION ALL
    SELECT 8, sl.owner_user_id, '', '', '', '', '', 0, '', sl.created_at,
           sl.title, sl.id
    FROM social_lists sl
    JOIN followees f ON f.uid = sl.owner_user_id
    WHERE sl.visibility IN (2, 3)
  UNION ALL
    -- Kind 9 (JOINED_GROUP, ADR-0012): PUBLIC group joins only — private
    -- membership never emits. Reuses the list columns; the handler maps them
    -- onto the group fields for kind 9.
    SELECT 9, m.user_id, '', '', '', '', '', 0, '', m.created_at,
           g.title, g.id
    FROM social_group_members m
    JOIN social_groups g ON g.id = m.group_id AND g.visibility = 2
    JOIN followees f ON f.uid = m.user_id
    WHERE m.role IN (1, 2)
  UNION ALL
    -- Kind 10 (MILESTONE, ADR-0013): stats-visibility gated like the
    -- heatmap. Reuses reaction_kind for the milestone kind and list_id for
    -- the tier; the handler remaps for kind 10.
    SELECT 10, sm.user_id, '', '', '', '', '', sm.kind::int, '', sm.crossed_at,
           '', sm.tier::bigint
    FROM social_milestones sm
    JOIN followees f ON f.uid = sm.user_id
    JOIN social_profiles asp ON asp.user_id = sm.user_id AND asp.stats_visibility IN (2, 3)
) events
JOIN social_profiles actor ON actor.user_id = events.actor_id
JOIN users au ON au.id = events.actor_id
WHERE (sqlc.narg('before')::timestamptz IS NULL
       OR date_trunc('milliseconds', events.event_at) < sqlc.narg('before')::timestamptz)
ORDER BY events.event_at DESC, events.actor_id, events.kind
LIMIT $2;

-- Slice 6 (ADR-0010): the episode comment tree. Tombstones (removed_at set)
-- stay in every list so other people's replies keep their context; their text
-- and author are already wiped.

-- name: InsertComment :one
INSERT INTO episode_comments (
    episode_uuid, podcast_uuid, episode_title, podcast_title,
    user_id, parent_id, root_id, text, timestamp_seconds,
    quote, quote_source, quote_segment
) VALUES (
    $1, $2, $3, $4, $5,
    sqlc.narg('parent_id'), sqlc.narg('root_id'), $6, sqlc.narg('timestamp_seconds'),
    $7, $8, $9
)
RETURNING id, created_at;

-- name: GetCommentByID :one
SELECT c.id, c.episode_uuid, c.podcast_uuid, c.episode_title, c.podcast_title,
       c.user_id, c.parent_id, c.root_id, c.text, c.timestamp_seconds,
       c.created_at, c.edited_at, c.removed_at,
       EXISTS (SELECT 1 FROM episode_comments r WHERE r.parent_id = c.id) AS has_replies
FROM episode_comments c
WHERE c.id = $1;

-- name: GetEpisodeComments :many
SELECT c.id, c.user_id, c.text, c.timestamp_seconds, c.created_at, c.edited_at,
       c.removed_at, c.quote, c.quote_source, c.quote_segment,
       u.uuid AS author_uuid, COALESCE(sp.handle, '')::text AS handle,
       COALESCE(sp.display_name, '')::text AS display_name,
       (SELECT count(*) FROM episode_comments r WHERE r.parent_id = c.id)::int AS reply_count
FROM episode_comments c
LEFT JOIN users u ON u.id = c.user_id
LEFT JOIN social_profiles sp ON sp.user_id = c.user_id
WHERE c.episode_uuid = $1 AND c.parent_id IS NULL
  AND (sqlc.narg('viewer')::bigint IS NULL OR c.user_id IS NULL OR NOT EXISTS (
    SELECT 1 FROM social_relationships sr
    WHERE (sr.kind = 0 AND ((sr.user_id = sqlc.narg('viewer') AND sr.target_user_id = c.user_id)
                         OR (sr.user_id = c.user_id AND sr.target_user_id = sqlc.narg('viewer'))))
       OR (sr.kind = 1 AND sr.user_id = sqlc.narg('viewer') AND sr.target_user_id = c.user_id)))
ORDER BY c.created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountEpisodeComments :one
SELECT count(*) FROM episode_comments c
WHERE c.episode_uuid = $1 AND c.parent_id IS NULL
  AND (sqlc.narg('viewer')::bigint IS NULL OR c.user_id IS NULL OR NOT EXISTS (
    SELECT 1 FROM social_relationships sr
    WHERE (sr.kind = 0 AND ((sr.user_id = sqlc.narg('viewer') AND sr.target_user_id = c.user_id)
                         OR (sr.user_id = c.user_id AND sr.target_user_id = sqlc.narg('viewer'))))
       OR (sr.kind = 1 AND sr.user_id = sqlc.narg('viewer') AND sr.target_user_id = c.user_id)));

-- name: GetCommentReplies :many
SELECT c.id, c.parent_id, c.user_id, c.text, c.timestamp_seconds, c.created_at,
       c.edited_at, c.removed_at, c.quote, c.quote_source, c.quote_segment,
       u.uuid AS author_uuid, COALESCE(sp.handle, '')::text AS handle,
       COALESCE(sp.display_name, '')::text AS display_name,
       (SELECT count(*) FROM episode_comments r WHERE r.parent_id = c.id)::int AS reply_count
FROM episode_comments c
LEFT JOIN users u ON u.id = c.user_id
LEFT JOIN social_profiles sp ON sp.user_id = c.user_id
WHERE c.parent_id = $1
  AND (sqlc.narg('viewer')::bigint IS NULL OR c.user_id IS NULL OR NOT EXISTS (
    SELECT 1 FROM social_relationships sr
    WHERE (sr.kind = 0 AND ((sr.user_id = sqlc.narg('viewer') AND sr.target_user_id = c.user_id)
                         OR (sr.user_id = c.user_id AND sr.target_user_id = sqlc.narg('viewer'))))
       OR (sr.kind = 1 AND sr.user_id = sqlc.narg('viewer') AND sr.target_user_id = c.user_id)))
ORDER BY c.created_at ASC
LIMIT $2 OFFSET $3;

-- name: CountCommentReplies :one
SELECT count(*) FROM episode_comments c WHERE c.parent_id = $1
  AND (sqlc.narg('viewer')::bigint IS NULL OR c.user_id IS NULL OR NOT EXISTS (
    SELECT 1 FROM social_relationships sr
    WHERE (sr.kind = 0 AND ((sr.user_id = sqlc.narg('viewer') AND sr.target_user_id = c.user_id)
                         OR (sr.user_id = c.user_id AND sr.target_user_id = sqlc.narg('viewer'))))
       OR (sr.kind = 1 AND sr.user_id = sqlc.narg('viewer') AND sr.target_user_id = c.user_id)));

-- name: EditComment :execrows
UPDATE episode_comments
SET text = $3, edited_at = now()
WHERE id = $1 AND user_id = $2 AND removed_at IS NULL;

-- name: TombstoneComment :execrows
UPDATE episode_comments
SET text = '', quote = '', quote_source = 0, quote_segment = 0,
    timestamp_seconds = NULL, user_id = NULL, edited_at = NULL, removed_at = now()
WHERE id = $1 AND user_id = $2 AND removed_at IS NULL;

-- name: TombstoneCommentsForUser :exec
UPDATE episode_comments
SET text = '', quote = '', quote_source = 0, quote_segment = 0,
    timestamp_seconds = NULL, user_id = NULL, edited_at = NULL, removed_at = now()
WHERE user_id = $1 AND removed_at IS NULL;

-- name: GetEpisodePlaybackForGate :one
SELECT played_up_to, duration, playing_status
FROM user_episodes
WHERE user_id = $1 AND episode_uuid::text = $2;

-- name: GetInboxReplies :many
SELECT c.id, c.parent_id, c.user_id, c.text, c.timestamp_seconds, c.created_at,
       c.edited_at, c.episode_uuid, c.podcast_uuid, c.episode_title, c.podcast_title,
       c.quote, c.quote_source, c.quote_segment,
       u.uuid AS author_uuid, sp.handle, sp.display_name,
       (SELECT count(*) FROM episode_comments r WHERE r.parent_id = c.id)::int AS reply_count
FROM episode_comments c
JOIN episode_comments parent ON parent.id = c.parent_id AND parent.user_id = $1
JOIN users u ON u.id = c.user_id
JOIN social_profiles sp ON sp.user_id = c.user_id
WHERE c.removed_at IS NULL AND c.user_id IS DISTINCT FROM $1
  AND (sqlc.narg('viewer')::bigint IS NULL OR c.user_id IS NULL OR NOT EXISTS (
    SELECT 1 FROM social_relationships sr
    WHERE (sr.kind = 0 AND ((sr.user_id = sqlc.narg('viewer') AND sr.target_user_id = c.user_id)
                         OR (sr.user_id = c.user_id AND sr.target_user_id = sqlc.narg('viewer'))))
       OR (sr.kind = 1 AND sr.user_id = sqlc.narg('viewer') AND sr.target_user_id = c.user_id)))
ORDER BY c.created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountInboxReplies :one
SELECT count(*)
FROM episode_comments c
JOIN episode_comments parent ON parent.id = c.parent_id AND parent.user_id = $1
WHERE c.removed_at IS NULL AND c.user_id IS DISTINCT FROM $1
  AND (sqlc.narg('viewer')::bigint IS NULL OR c.user_id IS NULL OR NOT EXISTS (
    SELECT 1 FROM social_relationships sr
    WHERE (sr.kind = 0 AND ((sr.user_id = sqlc.narg('viewer') AND sr.target_user_id = c.user_id)
                         OR (sr.user_id = c.user_id AND sr.target_user_id = sqlc.narg('viewer'))))
       OR (sr.kind = 1 AND sr.user_id = sqlc.narg('viewer') AND sr.target_user_id = c.user_id)));

-- name: CountUnreadInboxReplies :one
SELECT count(*)
FROM episode_comments c
JOIN episode_comments parent ON parent.id = c.parent_id AND parent.user_id = $1
JOIN social_profiles caller ON caller.user_id = $1
WHERE c.removed_at IS NULL AND c.user_id IS DISTINCT FROM $1
  AND c.created_at > caller.replies_seen_at
  AND (sqlc.narg('viewer')::bigint IS NULL OR c.user_id IS NULL OR NOT EXISTS (
    SELECT 1 FROM social_relationships sr
    WHERE (sr.kind = 0 AND ((sr.user_id = sqlc.narg('viewer') AND sr.target_user_id = c.user_id)
                         OR (sr.user_id = c.user_id AND sr.target_user_id = sqlc.narg('viewer'))))
       OR (sr.kind = 1 AND sr.user_id = sqlc.narg('viewer') AND sr.target_user_id = c.user_id)));

-- name: SetRepliesSeen :exec
UPDATE social_profiles SET replies_seen_at = now() WHERE user_id = $1;

-- Slice 7 (ADR-0011): shared lists — first-class multi-writer objects;
-- device playlists mirror them. added_by NULL = attribution erased.

-- name: CreateSocialList :one
INSERT INTO social_lists (owner_user_id, title, description, visibility)
VALUES ($1, $2, $3, $4)
RETURNING id, created_at, updated_at;

-- name: UpdateSocialList :execrows
UPDATE social_lists
SET title = $3, description = $4, visibility = $5, updated_at = now()
WHERE id = $1 AND owner_user_id = $2;

-- name: TouchSocialList :exec
UPDATE social_lists SET updated_at = now() WHERE id = $1;

-- name: DeleteSocialList :execrows
DELETE FROM social_lists WHERE id = $1 AND owner_user_id = $2;

-- name: GetSocialList :one
SELECT sl.id, sl.owner_user_id, sl.title, sl.description, sl.visibility,
       sl.created_at, sl.updated_at,
       op.handle AS owner_handle, op.display_name AS owner_display_name,
       (SELECT count(*) FROM social_list_entries e WHERE e.list_id = sl.id)::int AS entry_count
FROM social_lists sl
JOIN social_profiles op ON op.user_id = sl.owner_user_id
WHERE sl.id = $1;

-- name: GetSocialListsForUser :many
SELECT sl.id, sl.owner_user_id, sl.title, sl.description, sl.visibility,
       sl.created_at, sl.updated_at,
       op.handle AS owner_handle, op.display_name AS owner_display_name,
       (SELECT count(*) FROM social_list_entries e WHERE e.list_id = sl.id)::int AS entry_count,
       CASE WHEN sl.owner_user_id = $1 THEN 1
            ELSE (SELECT CASE m.role WHEN 1 THEN 2 WHEN 2 THEN 3 WHEN 0 THEN 4 ELSE 0 END
                  FROM social_list_members m WHERE m.list_id = sl.id AND m.user_id = $1)
       END::int AS your_role
FROM social_lists sl
JOIN social_profiles op ON op.user_id = sl.owner_user_id
WHERE sl.owner_user_id = $1
   OR EXISTS (SELECT 1 FROM social_list_members m WHERE m.list_id = sl.id AND m.user_id = $1)
ORDER BY sl.updated_at DESC;

-- name: GetProfileSocialLists :many
-- The owner's lists visible at the given visibility tiers (handler passes
-- (2) for anonymous/public viewers, (2,3) for active followers, all for self).
SELECT sl.id, sl.owner_user_id, sl.title, sl.description, sl.visibility,
       sl.created_at, sl.updated_at,
       op.handle AS owner_handle, op.display_name AS owner_display_name,
       (SELECT count(*) FROM social_list_entries e WHERE e.list_id = sl.id)::int AS entry_count
FROM social_lists sl
JOIN social_profiles op ON op.user_id = sl.owner_user_id
WHERE sl.owner_user_id = $1 AND sl.visibility = ANY($2::smallint[])
ORDER BY sl.updated_at DESC;

-- name: GetSocialListEntries :many
SELECT e.episode_uuid, e.podcast_uuid, e.episode_title, e.podcast_title,
       e.position, e.added_at,
       COALESCE(ap.handle, '')::text AS added_by_handle
FROM social_list_entries e
LEFT JOIN social_profiles ap ON ap.user_id = e.added_by
WHERE e.list_id = $1
ORDER BY e.position ASC, e.added_at ASC
LIMIT $2 OFFSET $3;

-- name: CountSocialListEntries :one
SELECT count(*) FROM social_list_entries WHERE list_id = $1;

-- name: MaxSocialListPosition :one
SELECT COALESCE(max(position), -1)::int FROM social_list_entries WHERE list_id = $1;

-- name: UpsertSocialListEntry :exec
INSERT INTO social_list_entries (list_id, episode_uuid, podcast_uuid,
                                 episode_title, podcast_title, position, added_by)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (list_id, episode_uuid) DO UPDATE SET
    position = EXCLUDED.position;

-- name: DeleteSocialListEntry :execrows
DELETE FROM social_list_entries WHERE list_id = $1 AND episode_uuid = $2;

-- name: MoveSocialListEntry :execrows
UPDATE social_list_entries SET position = $3
WHERE list_id = $1 AND episode_uuid = $2;

-- name: GetSocialListMember :one
SELECT role FROM social_list_members WHERE list_id = $1 AND user_id = $2;

-- name: UpsertSocialListMember :exec
INSERT INTO social_list_members (list_id, user_id, role)
VALUES ($1, $2, $3)
ON CONFLICT (list_id, user_id) DO UPDATE SET role = EXCLUDED.role;

-- name: DeleteSocialListMember :execrows
DELETE FROM social_list_members WHERE list_id = $1 AND user_id = $2;

-- name: GetSocialListMembers :many
SELECT m.user_id, m.role, sp.handle, sp.display_name
FROM social_list_members m
JOIN social_profiles sp ON sp.user_id = m.user_id
WHERE m.list_id = $1
ORDER BY m.role ASC, m.created_at ASC;

-- name: GetSocialListInvitesForUser :many
SELECT sl.id, sl.owner_user_id, sl.title, sl.description, sl.visibility,
       sl.created_at, sl.updated_at,
       op.handle AS owner_handle, op.display_name AS owner_display_name,
       (SELECT count(*) FROM social_list_entries e WHERE e.list_id = sl.id)::int AS entry_count
FROM social_list_members m
JOIN social_lists sl ON sl.id = m.list_id
JOIN social_profiles op ON op.user_id = sl.owner_user_id
WHERE m.user_id = $1 AND m.role = 0
ORDER BY m.created_at DESC;

-- name: DeleteSocialListsForOwner :exec
DELETE FROM social_lists WHERE owner_user_id = $1;

-- name: DeleteSocialListMembershipsForUser :exec
DELETE FROM social_list_members WHERE user_id = $1;

-- name: ClearSocialListAttributionForUser :exec
UPDATE social_list_entries SET added_by = NULL WHERE added_by = $1;

-- Slice 8: social push targets — every push-enabled device of one user.
-- name: GetPushTargetsForUser :many
SELECT d.user_id, d.device_id, d.push_token
FROM devices d
WHERE d.user_id = $1 AND d.push_on AND d.push_token <> '';

-- Slice 9: find people. Search and suggestions exclude hidden profiles and
-- anyone blocked either way; suggestions also exclude existing follows.

-- name: SearchSocialProfiles :many
SELECT sp.handle, sp.display_name, sp.user_id,
       COALESCE((SELECT sf.status FROM social_follows sf
                 WHERE sf.follower_user_id = $1 AND sf.followee_user_id = sp.user_id), -1)::int AS follow_status
FROM social_profiles sp
WHERE NOT sp.hide_from_discovery
  AND sp.user_id <> $1
  AND (sp.handle LIKE $2 || '%' ESCAPE '\' OR lower(sp.display_name) LIKE $2 || '%' ESCAPE '\')
  AND NOT EXISTS (
    SELECT 1 FROM social_relationships sr
    WHERE sr.kind = 0
      AND ((sr.user_id = $1 AND sr.target_user_id = sp.user_id)
        OR (sr.user_id = sp.user_id AND sr.target_user_id = $1)))
ORDER BY sp.handle
LIMIT $3;

-- name: GetSocialSuggestions :many
-- Friends-of-followed with mutual counts (count only — never names).
SELECT sp.handle, sp.display_name, sp.user_id, count(*)::int AS mutual_count
FROM social_follows first_hop
JOIN social_follows second_hop ON second_hop.follower_user_id = first_hop.followee_user_id
JOIN social_profiles sp ON sp.user_id = second_hop.followee_user_id
WHERE first_hop.follower_user_id = $1 AND first_hop.status = 1
  AND second_hop.status = 1
  AND second_hop.followee_user_id <> $1
  AND NOT sp.hide_from_discovery
  AND NOT EXISTS (
    SELECT 1 FROM social_follows mine
    WHERE mine.follower_user_id = $1 AND mine.followee_user_id = sp.user_id)
  AND NOT EXISTS (
    SELECT 1 FROM social_relationships sr
    WHERE ((sr.user_id = $1 AND sr.target_user_id = sp.user_id)
        OR (sr.user_id = sp.user_id AND sr.target_user_id = $1)))
GROUP BY sp.handle, sp.display_name, sp.user_id
ORDER BY mutual_count DESC, sp.handle
LIMIT $2;

-- name: GetDiscoverableProfileEmails :many
-- Contact matching happens in Go (salted hashes over these; fork scale keeps
-- the candidate set small). Only joined + discoverable accounts participate.
SELECT sp.user_id, sp.handle, sp.display_name, u.email
FROM social_profiles sp
JOIN users u ON u.id = sp.user_id
WHERE NOT sp.hide_from_discovery;

-- Slice 10: social discovery. Trending ranks followees' recently finished
-- episodes under each actor's HISTORY visibility; proof lists followees who
-- follow a show under each actor's FOLLOWED-SHOWS visibility. Muted actors
-- are excluded from both (blocks cannot occur across an active follow).

-- name: GetTrendingWithFriends :many
WITH followees AS (
    SELECT sf.followee_user_id AS uid
    FROM social_follows sf
    WHERE sf.follower_user_id = $1 AND sf.status = 1
      AND NOT EXISTS (
        SELECT 1 FROM social_relationships sr
        WHERE sr.user_id = $1 AND sr.target_user_id = sf.followee_user_id AND sr.kind = 1)
)
SELECT ue.podcast_uuid::text AS podcast_uuid,
       COALESCE(NULLIF(p.title, ''),
                NULLIF((SELECT max(up2.synced_title) FROM user_podcasts up2
                        WHERE up2.podcast_uuid = ue.podcast_uuid AND up2.synced_title <> ''), ''),
                '')::text AS title,
       COALESCE(p.author, '')::text AS author,
       count(DISTINCT ue.user_id)::int AS listener_count
FROM user_episodes ue
JOIN followees f ON f.uid = ue.user_id
JOIN social_profiles sp ON sp.user_id = ue.user_id AND sp.history_visibility IN (2, 3)
LEFT JOIN podcasts p ON p.uuid = ue.podcast_uuid
WHERE ue.playing_status = 3
  AND ue.playing_status_modified > (extract(epoch FROM now() - interval '30 days') * 1000)::bigint
  AND NOT EXISTS (
    SELECT 1 FROM user_podcasts mine
    WHERE mine.user_id = $1 AND mine.podcast_uuid = ue.podcast_uuid
      AND mine.subscribed AND NOT mine.is_deleted)
GROUP BY ue.podcast_uuid, p.title, p.author
ORDER BY listener_count DESC, podcast_uuid
LIMIT $2;

-- name: GetPodcastProof :many
WITH followees AS (
    SELECT sf.followee_user_id AS uid
    FROM social_follows sf
    WHERE sf.follower_user_id = $1 AND sf.status = 1
      AND NOT EXISTS (
        SELECT 1 FROM social_relationships sr
        WHERE sr.user_id = $1 AND sr.target_user_id = sf.followee_user_id AND sr.kind = 1)
)
SELECT sp.handle
FROM user_podcasts up
JOIN followees f ON f.uid = up.user_id
JOIN social_profiles sp ON sp.user_id = up.user_id
WHERE up.podcast_uuid = $2 AND up.subscribed AND NOT up.is_deleted
  -- private followed-shows never appear, not even in the count (QA finding)
  AND sp.followed_shows_visibility IN (2, 3)
ORDER BY sp.handle;

-- name: CountPendingFollowRequests :one
SELECT count(*) FROM social_follows
WHERE followee_user_id = $1 AND status = 0;

-- Account deletion must silence the account's devices (QA finding).
-- name: ClearPushStateForUser :exec
UPDATE devices SET push_token = '', push_on = false WHERE user_id = $1;

-- Groups (Slice 13, ADR-0012). Roles: 1=member, 2=owner, 3=invited, 4=banned;
-- visibility reuses SocialVisibility wire values (1=private, 2=public).

-- name: CreateSocialGroup :one
INSERT INTO social_groups (owner_user_id, title, description, visibility, podcast_uuid, podcast_title)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, created_at;

-- name: GetSocialGroup :one
SELECT g.id, g.owner_user_id, g.title, g.description, g.visibility, g.podcast_uuid, g.podcast_title, g.created_at,
       sp.handle AS owner_handle, sp.display_name AS owner_display_name,
       (SELECT count(*) FROM social_group_members c WHERE c.group_id = g.id AND c.role IN (1, 2))::int AS member_count,
       COALESCE(my.role, 0)::int AS your_role,
       COALESCE(my.notify_posts, false) AS notify_posts
FROM social_groups g
JOIN social_profiles sp ON sp.user_id = g.owner_user_id
LEFT JOIN social_group_members my ON my.group_id = g.id AND my.user_id = sqlc.narg('viewer')
WHERE g.id = $1;

-- name: UpdateSocialGroup :execrows
UPDATE social_groups
SET title = $3, description = $4, visibility = $5, updated_at = now()
WHERE id = $1 AND owner_user_id = $2;

-- name: DeleteSocialGroup :execrows
DELETE FROM social_groups WHERE id = $1 AND owner_user_id = $2;

-- name: GetGroupMember :one
SELECT role, notify_posts, created_at FROM social_group_members
WHERE group_id = $1 AND user_id = $2;

-- name: UpsertGroupMember :exec
INSERT INTO social_group_members (group_id, user_id, role, invited_by)
VALUES ($1, $2, $3, sqlc.narg('invited_by'))
ON CONFLICT (group_id, user_id) DO UPDATE SET role = EXCLUDED.role
WHERE social_group_members.role <> 4 OR EXCLUDED.role = 4;

-- name: DeleteGroupMember :execrows
DELETE FROM social_group_members WHERE group_id = $1 AND user_id = $2;

-- name: SetGroupMemberNotify :execrows
UPDATE social_group_members SET notify_posts = $3
WHERE group_id = $1 AND user_id = $2 AND role IN (1, 2);

-- name: GetGroupsForUser :many
SELECT g.id, g.owner_user_id, g.title, g.description, g.visibility, g.podcast_uuid, g.podcast_title, g.created_at,
       sp.handle AS owner_handle, sp.display_name AS owner_display_name,
       (SELECT count(*) FROM social_group_members c WHERE c.group_id = g.id AND c.role IN (1, 2))::int AS member_count,
       m.role::int AS your_role, m.notify_posts
FROM social_group_members m
JOIN social_groups g ON g.id = m.group_id
JOIN social_profiles sp ON sp.user_id = g.owner_user_id
WHERE m.user_id = $1 AND m.role = ANY($2::smallint[])
ORDER BY g.created_at DESC;

-- name: DiscoverGroups :many
SELECT g.id, g.owner_user_id, g.title, g.description, g.visibility, g.podcast_uuid, g.podcast_title, g.created_at,
       sp.handle AS owner_handle, sp.display_name AS owner_display_name,
       (SELECT count(*) FROM social_group_members c WHERE c.group_id = g.id AND c.role IN (1, 2))::int AS member_count
FROM social_groups g
JOIN social_profiles sp ON sp.user_id = g.owner_user_id
WHERE g.visibility = 2 AND (sqlc.narg('podcast_uuid')::text IS NULL OR g.podcast_uuid = sqlc.narg('podcast_uuid'))
ORDER BY member_count DESC, g.created_at DESC
LIMIT $1;

-- name: GetGroupMembers :many
SELECT sp.handle, sp.display_name, m.role::int AS role, m.created_at
FROM social_group_members m
JOIN social_profiles sp ON sp.user_id = m.user_id
WHERE m.group_id = $1 AND m.role IN (1, 2)
ORDER BY m.created_at ASC
LIMIT $2 OFFSET $3;

-- name: CountGroupMembers :one
SELECT count(*) FROM social_group_members WHERE group_id = $1 AND role IN (1, 2);

-- name: InsertGroupPost :one
INSERT INTO social_group_posts (
    group_id, user_id, parent_id, root_id, text,
    episode_uuid, podcast_uuid, episode_title, podcast_title, list_id, list_title
) VALUES (
    $1, $2, sqlc.narg('parent_id'), sqlc.narg('root_id'), $3,
    $4, $5, $6, $7, $8, $9
)
RETURNING id, created_at;

-- name: GetGroupPostByID :one
SELECT p.id, p.group_id, p.user_id, p.parent_id, p.root_id, p.text, p.created_at, p.removed_at,
       p.episode_title, p.podcast_title,
       EXISTS(SELECT 1 FROM social_group_posts r WHERE r.parent_id = p.id) AS has_replies
FROM social_group_posts p WHERE p.id = $1;

-- name: GetGroupPosts :many
SELECT p.id, p.parent_id, p.user_id, p.text, p.created_at, p.edited_at, p.removed_at,
       p.episode_uuid, p.podcast_uuid, p.episode_title, p.podcast_title, p.list_id, p.list_title,
       u.uuid AS author_uuid, sp.handle, sp.display_name,
       (SELECT count(*) FROM social_group_posts r WHERE r.parent_id = p.id)::int AS reply_count
FROM social_group_posts p
LEFT JOIN users u ON u.id = p.user_id
LEFT JOIN social_profiles sp ON sp.user_id = p.user_id
WHERE p.group_id = $1
  AND ((sqlc.narg('parent_id')::bigint IS NULL AND p.parent_id IS NULL)
       OR p.parent_id = sqlc.narg('parent_id'))
  AND (sqlc.narg('viewer')::bigint IS NULL OR p.user_id IS NULL OR NOT EXISTS (
    SELECT 1 FROM social_relationships sr
    WHERE (sr.kind = 0 AND ((sr.user_id = sqlc.narg('viewer') AND sr.target_user_id = p.user_id)
                         OR (sr.user_id = p.user_id AND sr.target_user_id = sqlc.narg('viewer'))))
       OR (sr.kind = 1 AND sr.user_id = sqlc.narg('viewer') AND sr.target_user_id = p.user_id)))
ORDER BY CASE WHEN p.parent_id IS NULL THEN p.created_at END DESC,
         CASE WHEN p.parent_id IS NOT NULL THEN p.created_at END ASC,
         p.id
LIMIT $2 OFFSET $3;

-- name: CountGroupPosts :one
SELECT count(*) FROM social_group_posts p
WHERE p.group_id = $1
  AND ((sqlc.narg('parent_id')::bigint IS NULL AND p.parent_id IS NULL)
       OR p.parent_id = sqlc.narg('parent_id'))
  AND (sqlc.narg('viewer')::bigint IS NULL OR p.user_id IS NULL OR NOT EXISTS (
    SELECT 1 FROM social_relationships sr
    WHERE (sr.kind = 0 AND ((sr.user_id = sqlc.narg('viewer') AND sr.target_user_id = p.user_id)
                         OR (sr.user_id = p.user_id AND sr.target_user_id = sqlc.narg('viewer'))))
       OR (sr.kind = 1 AND sr.user_id = sqlc.narg('viewer') AND sr.target_user_id = p.user_id)));

-- name: EditGroupPost :execrows
UPDATE social_group_posts SET text = $3, edited_at = now()
WHERE id = $1 AND user_id = $2 AND removed_at IS NULL;

-- name: TombstoneGroupPost :execrows
UPDATE social_group_posts
SET text = '', user_id = NULL, edited_at = NULL, removed_at = now(),
    episode_uuid = '', podcast_uuid = '', episode_title = '', podcast_title = '',
    list_id = 0, list_title = ''
WHERE id = $1 AND user_id = $2 AND removed_at IS NULL;

-- name: TombstoneGroupPostAsOwner :execrows
UPDATE social_group_posts p
SET text = '', user_id = NULL, edited_at = NULL, removed_at = now(),
    episode_uuid = '', podcast_uuid = '', episode_title = '', podcast_title = '',
    list_id = 0, list_title = ''
FROM social_groups g
WHERE p.id = $1 AND g.id = p.group_id AND g.owner_user_id = $2 AND p.removed_at IS NULL;

-- name: TombstoneGroupPostsForUser :exec
UPDATE social_group_posts
SET text = '', user_id = NULL, edited_at = NULL, removed_at = now(),
    episode_uuid = '', podcast_uuid = '', episode_title = '', podcast_title = '',
    list_id = 0, list_title = ''
WHERE user_id = $1 AND removed_at IS NULL;

-- name: DeleteGroupMembershipsForUser :exec
DELETE FROM social_group_members WHERE user_id = $1;

-- name: ClearGroupInviteAttributionForUser :exec
UPDATE social_group_members SET invited_by = NULL WHERE invited_by = $1;

-- name: DeleteOwnedPrivateGroups :exec
DELETE FROM social_groups WHERE owner_user_id = $1 AND visibility = 1;

-- name: GetOwnedPublicGroupIDs :many
SELECT id FROM social_groups WHERE owner_user_id = $1 AND visibility = 2;

-- name: FindGroupSuccessor :one
SELECT user_id FROM social_group_members
WHERE group_id = $1 AND role = 1 AND user_id <> $2
ORDER BY created_at ASC LIMIT 1;

-- name: TransferGroupOwner :exec
UPDATE social_groups SET owner_user_id = $2, updated_at = now() WHERE id = $1;

-- name: PromoteGroupMemberToOwner :exec
UPDATE social_group_members SET role = 2 WHERE group_id = $1 AND user_id = $2;

-- name: DeleteSocialGroupByID :exec
DELETE FROM social_groups WHERE id = $1;

-- name: GetGroupNotifyTargets :many
SELECT user_id FROM social_group_members
WHERE group_id = $1 AND role IN (1, 2) AND notify_posts AND user_id <> $2;

-- Milestones (Slice 14, ADR-0013): materialized ladder crossings. kind
-- 1=hours listened, 2=episodes finished.

-- name: GetListeningTotals :one
SELECT COALESCE(SUM(LEAST(GREATEST(played_up_to, 0),
                          CASE WHEN duration > 0 THEN duration ELSE played_up_to END)), 0)::bigint AS listened_seconds,
       COUNT(*) FILTER (WHERE playing_status = 3)::int AS episodes_finished
FROM user_episodes WHERE user_id = $1;

-- name: InsertMilestone :execrows
INSERT INTO social_milestones (user_id, kind, tier) VALUES ($1, $2, $3)
ON CONFLICT DO NOTHING;

-- name: GetMilestonesForUser :many
SELECT kind, tier, crossed_at FROM social_milestones
WHERE user_id = $1 ORDER BY crossed_at DESC, kind, tier;

-- name: GetFreshMilestones :many
SELECT kind, tier FROM social_milestones
WHERE user_id = $1 AND crossed_at > now() - interval '7 days'
ORDER BY crossed_at DESC LIMIT 3;

-- name: DeleteMilestonesForUser :exec
DELETE FROM social_milestones WHERE user_id = $1;

-- Weekly digest (push type 9): candidates are joined accounts past the
-- watermark with a graph or a fresh milestone - never filler.

-- name: GetDigestCandidates :many
SELECT sp.user_id FROM social_profiles sp
WHERE (sp.digest_sent_at IS NULL OR sp.digest_sent_at < now() - interval '6 days')
  AND (EXISTS (SELECT 1 FROM social_follows sf
               WHERE sf.follower_user_id = sp.user_id AND sf.status = 1)
       OR EXISTS (SELECT 1 FROM social_milestones sm
                  WHERE sm.user_id = sp.user_id AND sm.crossed_at > now() - interval '7 days'))
LIMIT $1;

-- name: SetDigestSent :exec
UPDATE social_profiles SET digest_sent_at = now() WHERE user_id = $1;

-- name: CountGraphHighlights :one
SELECT (
    (SELECT count(*) FROM podcast_reviews pr
     JOIN social_follows sf ON sf.followee_user_id = pr.user_id
      AND sf.follower_user_id = $1 AND sf.status = 1
     WHERE pr.updated_at > now() - interval '7 days')
  + (SELECT count(*) FROM social_lists sl
     JOIN social_follows sf ON sf.followee_user_id = sl.owner_user_id
      AND sf.follower_user_id = $1 AND sf.status = 1
     WHERE sl.created_at > now() - interval '7 days' AND sl.visibility IN (2, 3))
  + (SELECT count(*) FROM social_milestones sm
     JOIN social_follows sf ON sf.followee_user_id = sm.user_id
      AND sf.follower_user_id = $1 AND sf.status = 1
     JOIN social_profiles asp ON asp.user_id = sm.user_id
      AND asp.stats_visibility IN (2, 3)
     WHERE sm.crossed_at > now() - interval '7 days')
)::bigint;

-- Curators (Slice 15, ADR-0014): the operator-designated directory,
-- follower-ranked. Hidden-from-discovery still applies — a curator who
-- hides stays hidden (the flags compose, they don't override).

-- name: GetCurators :many
SELECT sp.handle, sp.display_name,
       CASE WHEN sp.bio_visibility = 2 THEN sp.bio ELSE '' END AS bio,
       (SELECT count(*) FROM social_follows sf
        WHERE sf.followee_user_id = sp.user_id AND sf.status = 1) AS follower_count
FROM social_profiles sp
WHERE sp.curator AND NOT sp.hide_from_discovery
  AND sp.user_id <> sqlc.narg('viewer')::bigint
  AND NOT EXISTS (
    SELECT 1 FROM social_relationships sr
    WHERE sr.kind = 0
      AND ((sr.user_id = sqlc.narg('viewer') AND sr.target_user_id = sp.user_id)
        OR (sr.user_id = sp.user_id AND sr.target_user_id = sqlc.narg('viewer'))))
ORDER BY follower_count DESC, sp.handle
LIMIT $1;
