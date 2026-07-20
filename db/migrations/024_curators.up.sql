-- Slice 15 (ADR-0014): operator-designated curators. Set only via admin/DB
-- (the ADR-0005 operator tier); no self-serve path exists.
ALTER TABLE social_profiles ADD COLUMN curator BOOLEAN NOT NULL DEFAULT false;
