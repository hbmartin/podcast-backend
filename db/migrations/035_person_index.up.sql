-- Highlights program B2 (ADR-0017): catalog-wide person identity + follows.
-- Identity is the numeric person id; canonical_name is deliberately NOT
-- unique (same-named humans can be distinct persons once external refs or
-- manual splits distinguish them). person_alias maps folded name strings to
-- persons (v1 ingest heuristic: one person per folded alias). external refs
-- (wikidata/semantic-web/publisher hrefs) are additive rows, never schema
-- changes. Follows reference person.id so identity corrections never rewrite
-- user data.
CREATE TABLE persons (
    id            BIGSERIAL PRIMARY KEY,
    canonical_name TEXT NOT NULL,
    display_name  TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX persons_canonical_idx ON persons(canonical_name);

CREATE TABLE person_aliases (
    person_id    BIGINT NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
    alias_folded TEXT NOT NULL,
    source       TEXT NOT NULL DEFAULT 'ingest',
    PRIMARY KEY (alias_folded, person_id)
);
CREATE INDEX person_aliases_person_idx ON person_aliases(person_id);

CREATE TABLE person_external_refs (
    person_id BIGINT NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
    scheme    TEXT NOT NULL,
    value     TEXT NOT NULL,
    UNIQUE (scheme, value)
);

-- One row per credited appearance; the ingest-time match source for pushes
-- and the person page's episode list.
CREATE TABLE person_appearances (
    person_id    BIGINT NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
    podcast_uuid UUID NOT NULL,
    episode_uuid UUID NOT NULL,
    role         TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (person_id, episode_uuid)
);
CREATE INDEX person_appearances_person_idx ON person_appearances(person_id, created_at DESC);

CREATE TABLE person_follows (
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    person_id  BIGINT NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, person_id)
);
CREATE INDEX person_follows_person_idx ON person_follows(person_id);
