-- User library rows are keyed by uuid (not catalog FK) so subscriptions to
-- podcasts the server has never crawled still sync between devices.
-- modified_at columns carry the per-user sync token (int64 millis).

CREATE TABLE user_podcasts (
    user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    podcast_uuid UUID NOT NULL,
    subscribed   BOOLEAN NOT NULL DEFAULT true,
    is_deleted   BOOLEAN NOT NULL DEFAULT false,
    auto_start_from INT,
    auto_skip_last  INT,
    episodes_sort_order INT,
    folder_uuid  UUID,
    sort_position INT,
    date_added   TIMESTAMPTZ,
    -- Api_PodcastSettings serialized as {"name": {"type": ..., "value": ...,
    -- "modified_at_ms": ...}} merged per-key by modified_at_ms
    settings     JSONB NOT NULL DEFAULT '{}',
    modified_at  BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, podcast_uuid)
);
CREATE INDEX user_podcasts_modified_idx ON user_podcasts(user_id, modified_at);

CREATE TABLE user_episodes (
    user_id       BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    episode_uuid  UUID NOT NULL,
    podcast_uuid  UUID NOT NULL,
    -- per-field last-writer-wins pairs, matching Api_SyncUserEpisode
    playing_status          INT NOT NULL DEFAULT 1,
    playing_status_modified BIGINT NOT NULL DEFAULT 0,
    played_up_to            BIGINT NOT NULL DEFAULT 0,
    played_up_to_modified   BIGINT NOT NULL DEFAULT 0,
    starred                 BOOLEAN NOT NULL DEFAULT false,
    starred_modified        BIGINT NOT NULL DEFAULT 0,
    is_deleted              BOOLEAN NOT NULL DEFAULT false, -- archived
    is_deleted_modified     BIGINT NOT NULL DEFAULT 0,
    duration                BIGINT NOT NULL DEFAULT 0,
    duration_modified       BIGINT NOT NULL DEFAULT 0,
    deselected_chapters          TEXT NOT NULL DEFAULT '',
    deselected_chapters_modified BIGINT NOT NULL DEFAULT 0,
    modified_at   BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, episode_uuid)
);
CREATE INDEX user_episodes_modified_idx ON user_episodes(user_id, modified_at);
CREATE INDEX user_episodes_starred_idx ON user_episodes(user_id) WHERE starred;
CREATE INDEX user_episodes_podcast_idx ON user_episodes(user_id, podcast_uuid);

CREATE TABLE folders (
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    folder_uuid UUID NOT NULL,
    name        TEXT NOT NULL DEFAULT '',
    color       INT NOT NULL DEFAULT 0,
    sort_position INT NOT NULL DEFAULT 0,
    podcasts_sort_type INT NOT NULL DEFAULT 0,
    date_added  TIMESTAMPTZ,
    is_deleted  BOOLEAN NOT NULL DEFAULT false,
    modified_at BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, folder_uuid)
);
CREATE INDEX folders_modified_idx ON folders(user_id, modified_at);

-- Filters ("playlists"); column set mirrors Api_SyncUserPlaylist fields 1-26.
CREATE TABLE playlists (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    uuid    UUID NOT NULL,
    original_uuid TEXT NOT NULL DEFAULT '',
    title   TEXT NOT NULL DEFAULT '',
    is_deleted BOOLEAN NOT NULL DEFAULT false,
    all_podcasts BOOLEAN,
    podcast_uuids TEXT,
    episode_uuids TEXT,
    audio_video INT,
    not_downloaded BOOLEAN,
    downloaded BOOLEAN,
    downloading BOOLEAN,
    finished BOOLEAN,
    partially_played BOOLEAN,
    unplayed BOOLEAN,
    starred BOOLEAN,
    manual BOOLEAN,
    sort_position INT,
    sort_type INT,
    icon_id INT,
    filter_hours INT,
    filter_duration BOOLEAN,
    longer_than INT,
    shorter_than INT,
    show_archived BOOLEAN,
    episode_order TEXT[] NOT NULL DEFAULT '{}',
    -- manual playlist episodes as serialized Api_SyncPlaylistEpisode list
    episodes JSONB NOT NULL DEFAULT '[]',
    modified_at BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, uuid)
);
CREATE INDEX playlists_modified_idx ON playlists(user_id, modified_at);

CREATE TABLE bookmarks (
    user_id       BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    bookmark_uuid UUID NOT NULL,
    podcast_uuid  UUID NOT NULL,
    episode_uuid  UUID NOT NULL,
    time_secs     INT NOT NULL DEFAULT 0,
    title         TEXT NOT NULL DEFAULT '',
    title_modified BIGINT NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    is_deleted    BOOLEAN NOT NULL DEFAULT false,
    is_deleted_modified BIGINT NOT NULL DEFAULT 0,
    modified_at   BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, bookmark_uuid)
);
CREATE INDEX bookmarks_modified_idx ON bookmarks(user_id, modified_at);
CREATE INDEX bookmarks_episode_idx ON bookmarks(user_id, episode_uuid);
