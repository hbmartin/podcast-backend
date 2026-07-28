-- Migration 027 shipped before its rotation-chain FK was changed to
-- ON DELETE SET NULL. Recreate it here so databases that already applied 027
-- receive the same behavior as fresh installs.
ALTER TABLE refresh_tokens
    DROP CONSTRAINT IF EXISTS refresh_tokens_rotated_from_id_fkey;
ALTER TABLE refresh_tokens
    ADD CONSTRAINT refresh_tokens_rotated_from_id_fkey
    FOREIGN KEY (rotated_from_id) REFERENCES refresh_tokens(id)
    ON DELETE SET NULL NOT VALID;
ALTER TABLE refresh_tokens
    VALIDATE CONSTRAINT refresh_tokens_rotated_from_id_fkey;

-- Older builds issued unbounded attachment capabilities. Preserve the
-- intended issuance-relative lifetime while expiring every legacy token that
-- is already more than fifteen minutes old.
UPDATE corpus_attachment_tokens
SET expires_at = created_at + interval '15 minutes'
WHERE expires_at = 'infinity'::timestamptz;
