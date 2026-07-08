ALTER TABLE devices DROP COLUMN created_at;
ALTER TABLE podcasts
    DROP COLUMN background_color,
    DROP COLUMN tint_for_light_bg,
    DROP COLUMN tint_for_dark_bg,
    DROP COLUMN colors_source_image_url;
DROP TABLE shared_lists;
DROP TABLE podcast_ratings;
