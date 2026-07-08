-- Per-user podcast ratings (1-5 stars).
CREATE TABLE podcast_ratings (
    user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    podcast_uuid UUID NOT NULL,
    rating       SMALLINT NOT NULL CHECK (rating BETWEEN 1 AND 5),
    modified_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, podcast_uuid)
);
CREATE INDEX podcast_ratings_podcast_idx ON podcast_ratings(podcast_uuid);

-- Shareable curated podcast lists (share_url = <base>/l/<code>).
CREATE TABLE shared_lists (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code          TEXT NOT NULL UNIQUE,
    title         TEXT NOT NULL DEFAULT '',
    description   TEXT NOT NULL DEFAULT '',
    podcast_uuids UUID[] NOT NULL DEFAULT '{}',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Cached artwork colors served at discover/images/metadata/{uuid}.json.
-- colors_source_image_url records which artwork the colors were computed
-- from; a crawl changing image_url implicitly invalidates the cache.
ALTER TABLE podcasts
    ADD COLUMN background_color        TEXT NOT NULL DEFAULT '',
    ADD COLUMN tint_for_light_bg       TEXT NOT NULL DEFAULT '',
    ADD COLUMN tint_for_dark_bg        TEXT NOT NULL DEFAULT '',
    ADD COLUMN colors_source_image_url TEXT NOT NULL DEFAULT '';

-- Anchor for the stats "since" timestamp when a device never reported one.
ALTER TABLE devices ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT now();
