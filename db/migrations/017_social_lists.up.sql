-- Slice 7 (ADR-0011): shared lists are first-class multi-writer social
-- objects; participants' device playlists are read-through mirrors. added_by
-- is nullable so erasure can wipe attribution while entries survive.
CREATE TABLE social_lists (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    owner_user_id BIGINT NOT NULL REFERENCES users (id),
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    visibility SMALLINT NOT NULL DEFAULT 1, -- 1=private 2=public 3=followers (SocialVisibility raw)
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX social_lists_owner_idx ON social_lists (owner_user_id);

CREATE TABLE social_list_entries (
    list_id BIGINT NOT NULL REFERENCES social_lists (id) ON DELETE CASCADE,
    episode_uuid TEXT NOT NULL,
    podcast_uuid TEXT NOT NULL DEFAULT '',
    episode_title TEXT NOT NULL DEFAULT '',
    podcast_title TEXT NOT NULL DEFAULT '',
    position INT NOT NULL DEFAULT 0,
    added_by BIGINT NULL REFERENCES users (id),
    added_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (list_id, episode_uuid)
);

CREATE TABLE social_list_members (
    list_id BIGINT NOT NULL REFERENCES social_lists (id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users (id),
    role SMALLINT NOT NULL, -- 0=invited 1=collaborator 2=subscriber
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (list_id, user_id)
);

CREATE INDEX social_list_members_user_idx ON social_list_members (user_id);

-- The custom-playlist sync overturn: the query envelope finally has a home
-- server-side (SyncUserPlaylist.custom_query = 1001).
ALTER TABLE playlists
    ADD COLUMN custom_query TEXT NOT NULL DEFAULT '';
