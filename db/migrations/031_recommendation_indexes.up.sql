-- The episode-recommendation candidate query walks the newest episodes
-- globally (ORDER BY published_at DESC LIMIT ...); the existing
-- (podcast_id, published_at) index cannot serve that ordering.
CREATE INDEX IF NOT EXISTS episodes_published_at_idx
    ON episodes (published_at DESC);
