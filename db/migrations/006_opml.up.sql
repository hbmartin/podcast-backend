-- OPML import batches: the client uploads feed URLs in chunks and polls by
-- re-POSTing; resolution state lives here.
CREATE TABLE opml_imports (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE opml_import_items (
    import_id    BIGINT NOT NULL REFERENCES opml_imports(id) ON DELETE CASCADE,
    feed_url     TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending', -- pending|resolved|failed
    podcast_uuid UUID,
    PRIMARY KEY (import_id, feed_url)
);
