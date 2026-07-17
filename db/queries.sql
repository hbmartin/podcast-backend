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
    date_added, settings, modified_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (user_id, podcast_uuid) DO UPDATE SET
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
    modified_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15,
          $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28)
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
ORDER BY r.created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountPodcastReviews :one
SELECT count(*) FROM podcast_reviews WHERE podcast_uuid = $1;

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
