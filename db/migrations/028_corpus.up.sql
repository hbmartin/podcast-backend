ALTER TABLE transcript_contributions ADD COLUMN content_hash TEXT;
CREATE INDEX transcript_contributions_content_idx
    ON transcript_contributions (episode_uuid, content_hash) WHERE content_hash IS NOT NULL;

CREATE TABLE corpus_candidates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    episode_uuid TEXT NOT NULL,
    podcast_uuid TEXT NOT NULL,
    language TEXT NOT NULL DEFAULT 'und',
    source TEXT NOT NULL CHECK (source IN ('contribution','publisher','derived','legacy')),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','promoted','rejected')),
    content_hash TEXT NOT NULL,
    attribution TEXT NOT NULL DEFAULT 'anonymous',
    attribution_id TEXT NOT NULL DEFAULT '',
    contribution_id BIGINT UNIQUE REFERENCES transcript_contributions(id),
    sighting_id BIGINT UNIQUE REFERENCES transcript_sightings(id),
    derived_from UUID REFERENCES corpus_candidates(id),
    publisher_verified BOOLEAN NOT NULL DEFAULT false,
    provenance JSONB NOT NULL DEFAULT '{}',
    decision_reason TEXT NOT NULL DEFAULT '',
    decided_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX corpus_candidates_episode_idx ON corpus_candidates (episode_uuid, language, created_at DESC);
CREATE UNIQUE INDEX corpus_candidates_content_idx
    ON corpus_candidates (episode_uuid, language, source, content_hash)
    WHERE source <> 'legacy';

CREATE TABLE corpus_artifacts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    candidate_id UUID NOT NULL REFERENCES corpus_candidates(id),
    kind TEXT NOT NULL CHECK (kind IN ('transcript','fingerprint','summary','chapters')),
    object_key TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    media_type TEXT NOT NULL,
    format TEXT NOT NULL,
    byte_length BIGINT NOT NULL,
    language TEXT NOT NULL DEFAULT 'und',
    source TEXT NOT NULL,
    provenance JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (candidate_id, kind, content_hash)
);

CREATE TABLE corpus_releases (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    episode_uuid TEXT NOT NULL,
    language TEXT NOT NULL DEFAULT 'und',
    transcript_artifact_id UUID REFERENCES corpus_artifacts(id),
    fingerprint_artifact_id UUID REFERENCES corpus_artifacts(id),
    summary_artifact_id UUID REFERENCES corpus_artifacts(id),
    chapters_artifact_id UUID REFERENCES corpus_artifacts(id),
    active BOOLEAN NOT NULL DEFAULT true,
    reason TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL DEFAULT 'system',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX corpus_releases_active_idx ON corpus_releases (episode_uuid, language) WHERE active;
CREATE INDEX corpus_releases_history_idx ON corpus_releases (episode_uuid, language, created_at DESC);

CREATE TABLE corpus_attachment_tokens (
    candidate_id UUID PRIMARY KEY REFERENCES corpus_candidates(id),
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE corpus_audit_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor TEXT NOT NULL,
    action TEXT NOT NULL,
    candidate_id UUID REFERENCES corpus_candidates(id),
    release_id UUID REFERENCES corpus_releases(id),
    details JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX corpus_audit_events_created_idx ON corpus_audit_events (created_at DESC);

CREATE FUNCTION anonymize_corpus_contributor() RETURNS trigger AS $$
BEGIN
    IF OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL THEN
        UPDATE corpus_candidates
        SET attribution = 'anonymized', attribution_id = 'candidate:' || id::text
        WHERE attribution = 'user' AND attribution_id = OLD.uuid::text;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER users_anonymize_corpus_after_delete
AFTER UPDATE OF deleted_at ON users
FOR EACH ROW EXECUTE FUNCTION anonymize_corpus_contributor();
