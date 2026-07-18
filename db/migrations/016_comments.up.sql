-- Slice 6 (ADR-0010): one comment tree per episode, fully nested, tombstoned
-- deletion. user_id is nullable so erasure can sever authorship while the row
-- (and therefore other people's replies) survives.
CREATE TABLE episode_comments (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    episode_uuid TEXT NOT NULL,
    podcast_uuid TEXT NOT NULL DEFAULT '',
    episode_title TEXT NOT NULL DEFAULT '',
    podcast_title TEXT NOT NULL DEFAULT '',
    user_id BIGINT NULL REFERENCES users (id),
    parent_id BIGINT NULL REFERENCES episode_comments (id),
    root_id BIGINT NULL REFERENCES episode_comments (id),
    text TEXT NOT NULL,
    timestamp_seconds INT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    edited_at TIMESTAMPTZ NULL,
    removed_at TIMESTAMPTZ NULL
);

CREATE INDEX episode_comments_toplevel_idx
    ON episode_comments (episode_uuid, created_at DESC)
    WHERE parent_id IS NULL;
CREATE INDEX episode_comments_children_idx
    ON episode_comments (parent_id, created_at);
CREATE INDEX episode_comments_author_idx
    ON episode_comments (user_id, created_at DESC);

-- Watermark for the Inbox "Replies" section: replies to the caller's comments
-- created after this instant count as unread. No per-item read rows.
ALTER TABLE social_profiles
    ADD COLUMN replies_seen_at TIMESTAMPTZ NOT NULL DEFAULT now();
