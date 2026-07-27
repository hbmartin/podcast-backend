DROP TRIGGER users_anonymize_corpus_after_delete ON users;
DROP FUNCTION anonymize_corpus_contributor;
DROP TABLE corpus_audit_events;
DROP TABLE corpus_attachment_tokens;
DROP TABLE corpus_releases;
DROP TABLE corpus_artifacts;
DROP TABLE corpus_candidates;
DROP INDEX transcript_contributions_content_idx;
ALTER TABLE transcript_contributions DROP COLUMN content_hash;
