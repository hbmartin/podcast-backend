-- Social identity + moderation foundation (pocket-casts-ios docs/Social.md,
-- ADR-0005/0006/0007). Opt-in public identity: a user has no row here until
-- they Join (claim a handle + accept terms). Avatars are deferred — profiles
-- are handle + display name + bio only in this slice.

-- The load-bearing integrity table. handle is the PRIMARY KEY, so a handle
-- string can only ever have one row — reissue after tombstoning is
-- structurally impossible (ADR-0005). Claims are a bare INSERT relying on the
-- PK to reject concurrent duplicates (compare-and-set, no read-then-write).
-- status: 0 = active, 1 = tombstoned (account erased; string reserved forever,
-- user_id nulled), 2 = reserved (blocklist, seeded below; never claimable).
CREATE TABLE social_handles (
    handle      CITEXT PRIMARY KEY CHECK (handle ~ '^[a-z0-9_]{3,30}$'),
    user_id     BIGINT UNIQUE REFERENCES users(id),
    status      SMALLINT NOT NULL DEFAULT 0,
    claimed_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    released_at TIMESTAMPTZ
);

-- One profile per joined user. Visibility columns hold the proto
-- SocialVisibility raw values (1 = private, 2 = public, 3 = followers_only);
-- everything defaults private per ADR-0006. display_name has no visibility —
-- it is always public once joined (it IS the addressable identity).
CREATE TABLE social_profiles (
    user_id                    BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    handle                     CITEXT NOT NULL,
    display_name               TEXT NOT NULL,
    bio                        TEXT NOT NULL DEFAULT '',
    terms_version              INT NOT NULL DEFAULT 0,
    avatar_visibility          SMALLINT NOT NULL DEFAULT 1,
    bio_visibility             SMALLINT NOT NULL DEFAULT 1,
    followed_shows_visibility  SMALLINT NOT NULL DEFAULT 1,
    top_podcasts_visibility    SMALLINT NOT NULL DEFAULT 1,
    stats_visibility           SMALLINT NOT NULL DEFAULT 1,
    history_visibility         SMALLINT NOT NULL DEFAULT 1,
    presence_visibility        SMALLINT NOT NULL DEFAULT 1,
    created_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                 TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Server-authoritative block/mute edges (docs/SocialModeration.md). kind:
-- 0 = block (mutual invisibility, enforced either-direction at the public
-- profile read), 1 = mute (one-way hide). The iOS SocialRelationship table
-- mirrors this locally for instant filtering.
CREATE TABLE social_relationships (
    user_id        BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    target_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind           SMALLINT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, target_user_id, kind)
);

CREATE INDEX social_relationships_target_idx ON social_relationships (target_user_id, kind);

-- The single triage queue: community flags and automated pre-filter hits,
-- distinguished by source (community_flag | auto_text). Worked manually
-- (admin/DB reads) at launch; dashboards/trust-weighting deferred.
-- reporter_user_id is NULL for automated sources. reason holds the proto
-- ReportReason raw value.
CREATE TABLE moderation_reports (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    target_user_id   BIGINT NOT NULL REFERENCES users(id),
    reporter_user_id BIGINT REFERENCES users(id),
    source           TEXT NOT NULL DEFAULT 'community_flag',
    reason           SMALLINT NOT NULL DEFAULT 0,
    context          TEXT NOT NULL DEFAULT '',
    state            TEXT NOT NULL DEFAULT 'open',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX moderation_reports_state_idx ON moderation_reports (state, created_at);

-- Reserved-word blocklist, seeded as permanently unclaimable rows (status 2).
INSERT INTO social_handles (handle, status) VALUES
    ('admin', 2), ('administrator', 2), ('root', 2), ('support', 2),
    ('help', 2), ('mod', 2), ('moderator', 2), ('staff', 2), ('official', 2),
    ('pocketcasts', 2), ('pocket_casts', 2), ('api', 2), ('www', 2),
    ('mail', 2), ('info', 2), ('news', 2), ('about', 2), ('terms', 2),
    ('privacy', 2), ('security', 2), ('abuse', 2), ('contact', 2),
    ('team', 2), ('system', 2), ('everyone', 2), ('anonymous', 2),
    ('user', 2), ('users', 2), ('profile', 2), ('settings', 2);
