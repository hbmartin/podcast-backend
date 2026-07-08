-- Push notification state. The APNs token arrives on the legacy refresh call
-- (POST user/update) rather than a dedicated registration endpoint, keyed by
-- the client's device identifier.
ALTER TABLE devices ADD COLUMN push_token TEXT NOT NULL DEFAULT '';
ALTER TABLE devices ADD COLUMN push_on BOOLEAN NOT NULL DEFAULT false;

-- Per-podcast notification toggle from the refresh call's positional
-- push_messages_on bit-string. The protobuf sync channel stores the same
-- toggle inside settings->'notification'; delivery honors either.
ALTER TABLE user_podcasts ADD COLUMN notify_enabled BOOLEAN NOT NULL DEFAULT false;

CREATE INDEX devices_push_token_idx ON devices (user_id) WHERE push_token <> '';
