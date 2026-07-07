-- Extensions used across the schema: citext for case-insensitive emails,
-- pg_trgm for fuzzy podcast/episode title search.
CREATE EXTENSION IF NOT EXISTS citext;
CREATE EXTENSION IF NOT EXISTS pg_trgm;
