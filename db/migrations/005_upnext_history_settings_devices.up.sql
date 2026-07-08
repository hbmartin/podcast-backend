CREATE TABLE up_next_items (
    user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    episode_uuid UUID NOT NULL,
    -- TEXT rather than UUID: user files use a sentinel podcast id
    podcast_uuid TEXT NOT NULL DEFAULT '',
    title        TEXT NOT NULL DEFAULT '',
    url          TEXT NOT NULL DEFAULT '',
    published    TIMESTAMPTZ,
    position     INT NOT NULL,
    added_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, episode_uuid)
);
CREATE INDEX up_next_items_position_idx ON up_next_items(user_id, position);

CREATE TABLE history (
    user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    episode_uuid UUID NOT NULL,
    podcast_uuid TEXT NOT NULL DEFAULT '',
    title        TEXT NOT NULL DEFAULT '',
    url          TEXT NOT NULL DEFAULT '',
    published    TIMESTAMPTZ,
    -- listening interaction time in millis; doubles as the sync token
    modified_at  BIGINT NOT NULL,
    PRIMARY KEY (user_id, episode_uuid)
);
CREATE INDEX history_recent_idx ON history(user_id, modified_at DESC);

CREATE TABLE user_settings (
    user_id  BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    -- {"skipForward": {"type": "int32", "value": 30, "modified_at_ms": ...}, ...}
    -- merged per-key by modified_at_ms
    settings JSONB NOT NULL DEFAULT '{}',
    modified_at BIGINT NOT NULL DEFAULT 0
);

-- Per-device playback time stats, synced as Api_SyncUserDevice ride-along
-- records. Values are cumulative seconds reported by the device.
CREATE TABLE devices (
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id   TEXT NOT NULL,
    device_type INT NOT NULL DEFAULT 1,
    times_started_at     BIGINT NOT NULL DEFAULT 0,
    time_silence_removal BIGINT NOT NULL DEFAULT 0,
    time_variable_speed  BIGINT NOT NULL DEFAULT 0,
    time_intro_skipping  BIGINT NOT NULL DEFAULT 0,
    time_skipping        BIGINT NOT NULL DEFAULT 0,
    time_listened        BIGINT NOT NULL DEFAULT 0,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, device_id)
);
