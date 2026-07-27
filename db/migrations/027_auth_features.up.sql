-- Coordinated pre-release auth migration: legacy refresh tokens deliberately
-- do not survive because older clients lack mandatory device binding.
DELETE FROM refresh_tokens;

ALTER TABLE refresh_tokens
    ADD COLUMN family_id UUID NOT NULL,
    ADD COLUMN device_id TEXT NOT NULL,
    ADD COLUMN rotated_from_id BIGINT REFERENCES refresh_tokens(id),
    ADD COLUMN last_used_at TIMESTAMPTZ;
CREATE INDEX refresh_tokens_family_idx ON refresh_tokens(family_id);

CREATE TABLE password_reset_codes (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash  TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX password_reset_codes_user_idx ON password_reset_codes(user_id, created_at DESC);

ALTER TABLE social_profiles ADD COLUMN avatar_url TEXT NOT NULL DEFAULT '';

CREATE TABLE social_avatars (
    user_id         BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    capability_hash TEXT NOT NULL UNIQUE,
    version         UUID NOT NULL UNIQUE,
    object_key      TEXT NOT NULL UNIQUE,
    content_hash    TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ
);

CREATE TABLE object_delete_outbox (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    object_key  TEXT NOT NULL UNIQUE,
    reason      TEXT NOT NULL,
    attempts    INT NOT NULL DEFAULT 0,
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX object_delete_outbox_pending_idx
    ON object_delete_outbox(available_at, id) WHERE completed_at IS NULL;
