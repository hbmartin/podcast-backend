-- Forward-only integrity hardening collected from PR feedback triage.

CREATE INDEX feedback_user_idx ON feedback (user_id);

ALTER TABLE users DROP CONSTRAINT users_email_key;
CREATE UNIQUE INDEX users_active_email_unique
    ON users (email)
    WHERE deleted_at IS NULL;

-- Normalize the stored spelling before replacing the ineffective
-- case-insensitive CITEXT regex with a text regex.
UPDATE social_profiles SET handle = lower(handle::text);
UPDATE social_handles SET handle = lower(handle::text);
ALTER TABLE social_handles DROP CONSTRAINT social_handles_handle_check;
ALTER TABLE social_handles
    ADD CONSTRAINT social_handles_handle_check
    CHECK (handle::text ~ '^[a-z0-9_]{3,30}$');
ALTER TABLE social_handles
    ADD CONSTRAINT social_handles_handle_user_unique UNIQUE (handle, user_id);

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM social_profiles sp
        LEFT JOIN social_handles sh
          ON sh.handle = sp.handle AND sh.user_id = sp.user_id
        WHERE sh.handle IS NULL
    ) THEN
        RAISE EXCEPTION 'social profile handle ownership mismatch';
    END IF;
END
$$;

ALTER TABLE social_profiles
    ADD CONSTRAINT social_profiles_handle_owner_fk
    FOREIGN KEY (handle, user_id)
    REFERENCES social_handles (handle, user_id);

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM moderation_reports
        WHERE NOT (
            (source = 'community_flag' AND reason BETWEEN 1 AND 6)
            OR (source <> 'community_flag' AND reason BETWEEN 0 AND 6)
        )
    ) THEN
        RAISE EXCEPTION 'unsupported moderation report reason exists';
    END IF;
END
$$;

ALTER TABLE moderation_reports
    ADD CONSTRAINT moderation_reports_reason_check
    CHECK (
        (source = 'community_flag' AND reason BETWEEN 1 AND 6)
        OR (source <> 'community_flag' AND reason BETWEEN 0 AND 6)
    );
CREATE INDEX moderation_reports_reporter_recent_idx
    ON moderation_reports (reporter_user_id, created_at DESC)
    WHERE reporter_user_id IS NOT NULL;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM social_group_posts post
        LEFT JOIN social_group_posts parent ON parent.id = post.parent_id
        LEFT JOIN social_group_posts root ON root.id = post.root_id
        WHERE (post.parent_id IS NOT NULL AND parent.id IS NULL)
           OR (post.root_id IS NOT NULL AND root.id IS NULL)
    ) THEN
        RAISE EXCEPTION 'dangling social group post tree pointer exists';
    END IF;
END
$$;

ALTER TABLE social_group_posts
    ADD CONSTRAINT social_group_posts_parent_fk
    FOREIGN KEY (parent_id) REFERENCES social_group_posts (id) ON DELETE SET NULL,
    ADD CONSTRAINT social_group_posts_root_fk
    FOREIGN KEY (root_id) REFERENCES social_group_posts (id) ON DELETE SET NULL;

ALTER TABLE history
    ADD COLUMN is_deleted BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE social_profiles
    ADD COLUMN digest_claimed_at TIMESTAMPTZ NULL;
