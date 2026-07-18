-- Slice 9: inverted discoverability — true hides the profile from people
-- search and suggestions (default: discoverable).
ALTER TABLE social_profiles
    ADD COLUMN hide_from_discovery BOOLEAN NOT NULL DEFAULT false;
