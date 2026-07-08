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
