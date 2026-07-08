-- Podcast catalog: feeds the server crawls. UUIDs are deterministic
-- (UUIDv5 of the canonical feed URL / episode guid) so any server instance
-- derives the same ids for the same feeds.
CREATE TABLE podcasts (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    uuid           UUID NOT NULL UNIQUE,
    feed_url       TEXT NOT NULL UNIQUE,
    title          TEXT NOT NULL DEFAULT '',
    author         TEXT NOT NULL DEFAULT '',
    description    TEXT NOT NULL DEFAULT '',
    image_url      TEXT NOT NULL DEFAULT '',
    website_url    TEXT NOT NULL DEFAULT '',
    category       TEXT NOT NULL DEFAULT '',
    language       TEXT NOT NULL DEFAULT '',
    media_type     TEXT NOT NULL DEFAULT 'audio',
    show_type      TEXT NOT NULL DEFAULT 'episodic',
    is_explicit    BOOLEAN NOT NULL DEFAULT false,

    refresh_status TEXT NOT NULL DEFAULT 'pending', -- pending|ok|failed
    refresh_error  TEXT NOT NULL DEFAULT '',
    feed_etag      TEXT NOT NULL DEFAULT '',
    feed_last_modified TEXT NOT NULL DEFAULT '',
    last_refresh_at TIMESTAMPTZ,
    next_refresh_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    latest_episode_uuid UUID,
    latest_episode_published TIMESTAMPTZ,
    -- bumped on every content change; drives ETag/If-Modified-Since on the
    -- cache-host endpoints
    content_modified_ms BIGINT NOT NULL DEFAULT 0,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX podcasts_next_refresh_idx ON podcasts(next_refresh_at);
CREATE INDEX podcasts_title_trgm_idx ON podcasts USING gin (title gin_trgm_ops);

CREATE TABLE episodes (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    uuid          UUID NOT NULL UNIQUE,
    podcast_id    BIGINT NOT NULL REFERENCES podcasts(id) ON DELETE CASCADE,
    guid          TEXT NOT NULL,
    title         TEXT NOT NULL DEFAULT '',
    audio_url     TEXT NOT NULL DEFAULT '',
    file_type     TEXT NOT NULL DEFAULT '',
    file_size     BIGINT NOT NULL DEFAULT 0,
    duration_secs INT NOT NULL DEFAULT 0,
    published_at  TIMESTAMPTZ,
    episode_type  TEXT NOT NULL DEFAULT '',   -- full|trailer|bonus
    season        INT NOT NULL DEFAULT 0,
    number        INT NOT NULL DEFAULT 0,
    show_notes    TEXT NOT NULL DEFAULT '',
    image_url     TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (podcast_id, guid)
);
CREATE INDEX episodes_podcast_published_idx ON episodes(podcast_id, published_at DESC);
CREATE INDEX episodes_title_trgm_idx ON episodes USING gin (title gin_trgm_ops);
