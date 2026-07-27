ALTER TABLE devices
    ADD COLUMN push_environment TEXT NOT NULL DEFAULT 'production';
-- NOT VALID keeps this migration's ACCESS EXCLUSIVE window free of a
-- full-table validation scan; migration 030 validates the constraint under a
-- lock that does not block writes.
ALTER TABLE devices
    ADD CONSTRAINT devices_push_environment_check
    CHECK (push_environment IN ('sandbox', 'production')) NOT VALID;
