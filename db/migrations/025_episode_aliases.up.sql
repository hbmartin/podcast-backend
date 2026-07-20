-- Slice 16 (ADR-0015): the device-scheme <-> catalog episode uuid bridge.
-- Both schemes are pure functions of the feed item's guid, so the crawler
-- derives and writes the pair at ingest; social endpoints resolve through it.
CREATE TABLE episode_aliases (
    device_uuid TEXT PRIMARY KEY,
    catalog_uuid TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX episode_aliases_catalog_idx ON episode_aliases (catalog_uuid);
