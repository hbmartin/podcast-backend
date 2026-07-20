-- Slice 12: transcript-pinned comments. The quote is self-contained render
-- truth; source/segment are advisory (transcripts regenerate).
ALTER TABLE episode_comments ADD COLUMN quote TEXT NOT NULL DEFAULT '';
ALTER TABLE episode_comments ADD COLUMN quote_source INT NOT NULL DEFAULT 0;
ALTER TABLE episode_comments ADD COLUMN quote_segment INT NOT NULL DEFAULT 0;
