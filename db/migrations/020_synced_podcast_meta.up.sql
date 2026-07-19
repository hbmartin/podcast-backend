-- Slice 11: client-synced podcast metadata — the title renders while (or if
-- ever) the feed crawl lands; the URL lets the server ingest unknown feeds.
ALTER TABLE user_podcasts ADD COLUMN synced_title TEXT NOT NULL DEFAULT '';
ALTER TABLE user_podcasts ADD COLUMN synced_feed_url TEXT NOT NULL DEFAULT '';
