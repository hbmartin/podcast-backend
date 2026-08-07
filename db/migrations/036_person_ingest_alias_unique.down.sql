-- The dedupe merge is not reversible; only the constraint is dropped.
DROP INDEX IF EXISTS person_aliases_ingest_alias_unique;
