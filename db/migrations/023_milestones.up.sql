-- Slice 14 (ADR-0013): materialized milestone crossings. kind 1=hours
-- listened, 2=episodes finished; the row is a fact about the past, written
-- once (ON CONFLICT DO NOTHING at the detection site).
CREATE TABLE social_milestones (
    user_id BIGINT NOT NULL REFERENCES users(id),
    kind SMALLINT NOT NULL,
    tier INT NOT NULL,
    crossed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, kind, tier)
);
CREATE INDEX social_milestones_crossed_idx ON social_milestones (user_id, crossed_at DESC);

-- The weekly-digest sent watermark (never send twice for one week).
ALTER TABLE social_profiles ADD COLUMN digest_sent_at TIMESTAMPTZ NULL;
