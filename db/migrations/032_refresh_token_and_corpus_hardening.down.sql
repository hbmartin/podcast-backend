-- Attachment-token expiry is a security boundary and is intentionally not
-- relaxed on rollback. Restore only the former FK deletion behavior.
ALTER TABLE refresh_tokens
    DROP CONSTRAINT IF EXISTS refresh_tokens_rotated_from_id_fkey;
ALTER TABLE refresh_tokens
    ADD CONSTRAINT refresh_tokens_rotated_from_id_fkey
    FOREIGN KEY (rotated_from_id) REFERENCES refresh_tokens(id);
