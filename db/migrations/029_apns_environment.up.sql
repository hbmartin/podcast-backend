ALTER TABLE devices
    ADD COLUMN push_environment TEXT NOT NULL DEFAULT 'production'
    CHECK (push_environment IN ('sandbox', 'production'));
