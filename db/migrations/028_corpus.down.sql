DROP TRIGGER IF EXISTS users_anonymize_corpus_after_delete ON users;
DROP FUNCTION IF EXISTS anonymize_corpus_contributor;
DROP TABLE IF EXISTS corpus_audit_events;
DROP TABLE IF EXISTS corpus_attachment_tokens;
DROP TABLE IF EXISTS corpus_releases;
DROP TABLE IF EXISTS corpus_artifacts;
DROP TABLE IF EXISTS corpus_candidates;
DROP INDEX IF EXISTS transcript_contributions_content_idx;
ALTER TABLE transcript_contributions DROP COLUMN IF EXISTS content_hash;
