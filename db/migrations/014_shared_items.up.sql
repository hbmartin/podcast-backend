-- Send-to-friend shared items (Slice 4; pocket-casts-ios docs/Social.md).
-- Sender and recipient must both be joined (handler-enforced); items sent by
-- an erased profile are deleted with it (attributed UGC). Titles are
-- denormalized at send time so inbox rendering never needs catalog joins.
CREATE TABLE shared_items (
    id                BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    sender_user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    recipient_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    episode_uuid      TEXT NOT NULL,
    podcast_uuid      TEXT NOT NULL DEFAULT '',
    episode_title     TEXT NOT NULL DEFAULT '',
    podcast_title     TEXT NOT NULL DEFAULT '',
    note              TEXT NOT NULL DEFAULT '',
    timestamp_seconds INT NOT NULL DEFAULT 0,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    read_at           TIMESTAMPTZ
);

CREATE INDEX shared_items_recipient_idx ON shared_items (recipient_user_id, read_at, created_at DESC);
CREATE INDEX shared_items_sender_idx ON shared_items (sender_user_id);
