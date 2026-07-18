-- Follow graph (Slice 5; pocket-casts-ios docs/Social.md). Open/instant
-- follows by default; followees with require_follow_approval turn new follows
-- into pending requests. status: 0 = pending, 1 = active.
CREATE TABLE social_follows (
    follower_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    followee_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status           SMALLINT NOT NULL DEFAULT 1,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    approved_at      TIMESTAMPTZ,
    PRIMARY KEY (follower_user_id, followee_user_id)
);

CREATE INDEX social_follows_followee_idx ON social_follows (followee_user_id, status);

ALTER TABLE social_profiles ADD COLUMN require_follow_approval BOOLEAN NOT NULL DEFAULT false;
