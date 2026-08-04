ALTER TABLE bookmarks
    DROP COLUMN IF EXISTS excerpt,
    DROP COLUMN IF EXISTS end_time_secs,
    DROP COLUMN IF EXISTS trim_modified,
    DROP COLUMN IF EXISTS tags,
    DROP COLUMN IF EXISTS tags_modified;
