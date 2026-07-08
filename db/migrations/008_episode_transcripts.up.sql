-- Podcasting 2.0 episode metadata surfaced through the show-notes endpoint:
-- publisher transcripts ([{url, type, language}] as the client decodes them)
-- and the podcast:chapters URL (Podcast Index chapters JSON).
ALTER TABLE episodes ADD COLUMN transcripts JSONB NOT NULL DEFAULT '[]';
ALTER TABLE episodes ADD COLUMN chapters_url TEXT NOT NULL DEFAULT '';
