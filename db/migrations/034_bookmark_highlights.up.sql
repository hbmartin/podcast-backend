-- Highlights program B1 (ADR-0016): smart-highlight enrichment plus
-- user-authored trim and tags on bookmarks. excerpt/end_time were wire-defined
-- (fork fields 1001/1002) but never persisted; trim_modified marks the window
-- user-edited (beats machine enrichment in merges), tags merge as a whole set
-- LWW by tags_modified. Zero timestamps mean "never set", matching the
-- title_modified/is_deleted_modified convention.
ALTER TABLE bookmarks
    ADD COLUMN excerpt TEXT NOT NULL DEFAULT '',
    ADD COLUMN end_time_secs DOUBLE PRECISION NOT NULL DEFAULT 0,
    ADD COLUMN trim_modified BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN tags TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN tags_modified BIGINT NOT NULL DEFAULT 0;
