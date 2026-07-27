DROP TABLE IF EXISTS object_delete_outbox;
DROP TABLE IF EXISTS social_avatars;
ALTER TABLE social_profiles DROP COLUMN IF EXISTS avatar_url;
DROP TABLE IF EXISTS password_reset_codes;
DROP INDEX IF EXISTS refresh_tokens_family_idx;
ALTER TABLE refresh_tokens
    DROP COLUMN IF EXISTS last_used_at,
    DROP COLUMN IF EXISTS rotated_from_id,
    DROP COLUMN IF EXISTS device_id,
    DROP COLUMN IF EXISTS family_id;
