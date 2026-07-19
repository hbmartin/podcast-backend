-- Slice 13 (ADR-0012): one Group entity, two configurations. Visibility reuses
-- the SocialVisibility wire values (1=private, 2=public). Anchors are
-- non-exclusive. member role: 1=member, 2=owner, 3=invited, 4=banned.
CREATE TABLE social_groups (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    owner_user_id BIGINT NOT NULL REFERENCES users(id),
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    visibility SMALLINT NOT NULL DEFAULT 1,
    podcast_uuid TEXT NOT NULL DEFAULT '',
    podcast_title TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX social_groups_podcast_idx ON social_groups (podcast_uuid) WHERE podcast_uuid <> '';
CREATE INDEX social_groups_owner_idx ON social_groups (owner_user_id);

CREATE TABLE social_group_members (
    group_id BIGINT NOT NULL REFERENCES social_groups(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id),
    role SMALLINT NOT NULL DEFAULT 1,
    invited_by BIGINT NULL REFERENCES users(id),
    notify_posts BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (group_id, user_id)
);
CREATE INDEX social_group_members_user_idx ON social_group_members (user_id);

CREATE TABLE social_group_posts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    group_id BIGINT NOT NULL REFERENCES social_groups(id) ON DELETE CASCADE,
    user_id BIGINT NULL REFERENCES users(id),
    parent_id BIGINT NULL,
    root_id BIGINT NULL,
    text TEXT NOT NULL,
    episode_uuid TEXT NOT NULL DEFAULT '',
    podcast_uuid TEXT NOT NULL DEFAULT '',
    episode_title TEXT NOT NULL DEFAULT '',
    podcast_title TEXT NOT NULL DEFAULT '',
    list_id BIGINT NOT NULL DEFAULT 0,
    list_title TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    edited_at TIMESTAMPTZ NULL,
    removed_at TIMESTAMPTZ NULL
);
CREATE INDEX social_group_posts_page_idx ON social_group_posts (group_id, parent_id, created_at DESC);
