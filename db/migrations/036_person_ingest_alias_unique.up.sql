-- ADR-0017 hardening: enforce the v1 "one person per folded alias" ingest
-- heuristic at the schema level. Concurrent crawls could both miss
-- FindPersonByAlias and create duplicate persons for the same folded name;
-- the partial index below makes the loser's insert fail so ingest can retry
-- against the winner. Scoped to source='ingest' so future manual splits
-- (same alias deliberately owned by several persons) remain possible.

-- First fold any duplicates the race already created into the lowest person
-- id per alias — the row FindPersonByAlias (ORDER BY p.id LIMIT 1) already
-- returns, so surviving behavior is unchanged. v1 ingest creates exactly one
-- alias per person, so a loser for one alias cannot be a winner for another.
WITH shadowed AS (
    SELECT a.person_id AS loser_id, MIN(b.person_id) AS winner_id
    FROM person_aliases a
    JOIN person_aliases b
      ON b.alias_folded = a.alias_folded AND b.source = 'ingest'
    WHERE a.source = 'ingest'
    GROUP BY a.person_id
    HAVING a.person_id > MIN(b.person_id)
)
INSERT INTO person_appearances (person_id, podcast_uuid, episode_uuid, role, created_at)
SELECT s.winner_id, pa.podcast_uuid, pa.episode_uuid, pa.role, pa.created_at
FROM person_appearances pa
JOIN shadowed s ON s.loser_id = pa.person_id
ON CONFLICT (person_id, episode_uuid) DO NOTHING;

WITH shadowed AS (
    SELECT a.person_id AS loser_id, MIN(b.person_id) AS winner_id
    FROM person_aliases a
    JOIN person_aliases b
      ON b.alias_folded = a.alias_folded AND b.source = 'ingest'
    WHERE a.source = 'ingest'
    GROUP BY a.person_id
    HAVING a.person_id > MIN(b.person_id)
)
INSERT INTO person_follows (user_id, person_id, created_at)
SELECT pf.user_id, s.winner_id, pf.created_at
FROM person_follows pf
JOIN shadowed s ON s.loser_id = pf.person_id
ON CONFLICT (user_id, person_id) DO NOTHING;

WITH shadowed AS (
    SELECT a.person_id AS loser_id, MIN(b.person_id) AS winner_id
    FROM person_aliases a
    JOIN person_aliases b
      ON b.alias_folded = a.alias_folded AND b.source = 'ingest'
    WHERE a.source = 'ingest'
    GROUP BY a.person_id
    HAVING a.person_id > MIN(b.person_id)
)
DELETE FROM persons WHERE id IN (SELECT loser_id FROM shadowed);

CREATE UNIQUE INDEX person_aliases_ingest_alias_unique
    ON person_aliases (alias_folded) WHERE source = 'ingest';
