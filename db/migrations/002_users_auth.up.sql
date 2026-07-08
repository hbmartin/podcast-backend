CREATE TABLE users (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    uuid          UUID NOT NULL UNIQUE,
    email         CITEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    scope         TEXT NOT NULL DEFAULT 'mobile',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ,

    -- Per-user sync tokens: int64 millis, strictly monotonic. The client
    -- stores these independently (PCLastModifiedServerDate,
    -- SJUpNextServerLastModified, SJHistoryServerLastModified).
    sync_last_modified    BIGINT NOT NULL DEFAULT 0,
    up_next_modified      BIGINT NOT NULL DEFAULT 0,
    history_modified      BIGINT NOT NULL DEFAULT 0,
    history_cleared_at_ms BIGINT NOT NULL DEFAULT 0,
    marketing_opt_in      BOOLEAN NOT NULL DEFAULT false
);

CREATE TABLE refresh_tokens (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- sha256 hex of the opaque token; the token itself is never stored
    token_hash TEXT NOT NULL UNIQUE,
    scope      TEXT NOT NULL DEFAULT 'mobile',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ
);

CREATE INDEX refresh_tokens_user_idx ON refresh_tokens(user_id);
