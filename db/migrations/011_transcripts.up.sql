-- Crowdsourced transcript contributions and sightings
-- (docs/TranscriptContributions.md §4). v1 is write-only: the backend ingests
-- and stores; nothing is served back yet. Every contribution is kept (no
-- arbitration). Each row is attributed to exactly one of an account user
-- (valid Bearer) or an App Attest install key_id. episode/podcast identifiers
-- are stored as TEXT because they may be catalog UUIDs or deterministic
-- local-feed identities (indistinguishable by design).
CREATE TABLE transcript_contributions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    episode_uuid TEXT NOT NULL,
    podcast_uuid TEXT NOT NULL,
    vtt_blob BYTEA NOT NULL,
    fingerprint_blob BYTEA NOT NULL,
    engine TEXT NOT NULL DEFAULT '',
    model_id TEXT NOT NULL DEFAULT '',
    language TEXT NOT NULL DEFAULT '',
    diarized BOOLEAN NOT NULL DEFAULT false,
    app_version TEXT NOT NULL DEFAULT '',
    episode_duration_seconds DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    attribution TEXT NOT NULL,
    attribution_id TEXT NOT NULL
);

CREATE INDEX transcript_contributions_episode_idx ON transcript_contributions (episode_uuid);
CREATE INDEX transcript_contributions_attribution_idx ON transcript_contributions (attribution, attribution_id);

-- Sightings of publisher-provided transcripts. The server fetches the content
-- itself (background job); dedup is per (episode_uuid, transcript_url).
CREATE TABLE transcript_sightings (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    episode_uuid TEXT NOT NULL,
    podcast_uuid TEXT NOT NULL,
    transcript_url TEXT NOT NULL,
    format TEXT NOT NULL DEFAULT '',
    language TEXT NOT NULL DEFAULT '',
    attribution TEXT NOT NULL,
    attribution_id TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    content BYTEA,
    content_type TEXT NOT NULL DEFAULT '',
    fetched_at TIMESTAMPTZ,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (episode_uuid, transcript_url)
);
