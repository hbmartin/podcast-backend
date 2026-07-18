-- Slice 8: per-type social push preferences. Bitmask of DISABLED
-- SocialPushType values (bit n = type n+1 off); default 0 = all on.
ALTER TABLE social_profiles
    ADD COLUMN social_push_disabled BIGINT NOT NULL DEFAULT 0;
