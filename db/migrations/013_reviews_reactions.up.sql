-- Written reviews + episode reactions (Slice 3; pocket-casts-ios docs/Social.md).
-- A review's STARS live in podcast_ratings (unchanged, anonymous); this table
-- holds only the attributed public TEXT, which requires a joined account —
-- enforced by the handler (profile existence), and erased with the profile.
CREATE TABLE podcast_reviews (
    user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    podcast_uuid UUID NOT NULL,
    text         TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, podcast_uuid)
);

CREATE INDEX podcast_reviews_podcast_idx ON podcast_reviews (podcast_uuid, created_at DESC);

-- Account-recorded reactions: one per (user, episode); public display is
-- aggregate counts only. kind holds the proto ReactionKind raw value (1-5).
CREATE TABLE episode_reactions (
    user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    episode_uuid TEXT NOT NULL,
    kind         SMALLINT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, episode_uuid)
);

CREATE INDEX episode_reactions_episode_idx ON episode_reactions (episode_uuid, kind);

-- Content-level reports: what kind of thing is reported ('user' | 'review')
-- and a reference to it (review reports carry the podcast uuid).
ALTER TABLE moderation_reports
    ADD COLUMN target_type TEXT NOT NULL DEFAULT 'user',
    ADD COLUMN content_ref TEXT NOT NULL DEFAULT '';
