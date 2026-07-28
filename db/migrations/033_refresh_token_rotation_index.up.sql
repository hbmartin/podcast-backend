-- A standalone migration lets golang-migrate execute this outside an explicit
-- transaction, which PostgreSQL requires for concurrent index creation.
CREATE INDEX CONCURRENTLY IF NOT EXISTS refresh_tokens_rotated_from_idx
    ON refresh_tokens(rotated_from_id)
    WHERE rotated_from_id IS NOT NULL;
