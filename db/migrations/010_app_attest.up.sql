-- App Attest (Apple DeviceCheck) device attestation for fork-owned anonymous
-- endpoints (docs/AppAttest.md §2). attest_keys holds one enrolled key per
-- install (key_id == base64(SHA256(public key))); the counter is advanced
-- atomically on every accepted assertion to defeat replay. attest_challenges
-- is a single-use, short-TTL nonce store issued by GET /attest/challenge.
CREATE TABLE attest_keys (
    key_id TEXT PRIMARY KEY,
    public_key BYTEA NOT NULL,
    counter BIGINT NOT NULL DEFAULT 0,
    receipt BYTEA,
    environment TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ
);

CREATE TABLE attest_challenges (
    challenge BYTEA PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX attest_challenges_expires_at_idx ON attest_challenges (expires_at);
