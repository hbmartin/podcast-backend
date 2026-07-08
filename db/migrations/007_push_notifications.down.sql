DROP INDEX IF EXISTS devices_push_token_idx;
ALTER TABLE user_podcasts DROP COLUMN IF EXISTS notify_enabled;
ALTER TABLE devices DROP COLUMN IF EXISTS push_on;
ALTER TABLE devices DROP COLUMN IF EXISTS push_token;
