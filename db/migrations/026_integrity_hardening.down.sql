ALTER TABLE social_profiles DROP COLUMN digest_claimed_at;

ALTER TABLE history DROP COLUMN is_deleted;

ALTER TABLE social_group_posts
    DROP CONSTRAINT social_group_posts_root_fk,
    DROP CONSTRAINT social_group_posts_parent_fk;

DROP INDEX moderation_reports_reporter_recent_idx;
ALTER TABLE moderation_reports DROP CONSTRAINT moderation_reports_reason_check;

ALTER TABLE social_profiles DROP CONSTRAINT social_profiles_handle_owner_fk;
ALTER TABLE social_handles
    DROP CONSTRAINT social_handles_handle_user_unique,
    DROP CONSTRAINT social_handles_handle_check;
ALTER TABLE social_handles
    ADD CONSTRAINT social_handles_handle_check
    CHECK (handle ~ '^[a-z0-9_]{3,30}$');

-- Conditionally irreversible: once a deleted account's email has been reused
-- (the exact behavior the up migration enables), re-adding the blanket
-- uniqueness constraint fails on the duplicate rows. Resolve or purge the
-- soft-deleted duplicates before rolling back.
DROP INDEX users_active_email_unique;
ALTER TABLE users ADD CONSTRAINT users_email_key UNIQUE (email);

DROP INDEX feedback_user_idx;
