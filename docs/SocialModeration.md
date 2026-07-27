# Social safety and moderation

## Purpose

This workstream makes social visibility decisions fail closed, makes compound
writes atomic, and prevents moderation controls from being bypassed through
concurrency, unsupported values, or invisible Unicode.

## Behavior changes

### Fail-closed visibility and reply checks

Shared-list access now separates "not a member" from "membership lookup failed."
Only PostgreSQL's no-row result means no membership; database errors stop the
request. Block and follower lookups are handled the same way.

The same fail-closed rule applies when:

- replying to a social comment;
- replying to a group post;
- accepting or rejecting a shared-list invitation; and
- resolving member, follower, or block state for list access.

A confirmed privacy denial retains the existing not-found response so callers
cannot distinguish blocked content from missing content. An infrastructure
failure becomes a server error instead of granting access.

### Atomic shared-list creation

The list row and its valid initial entries are now created in one transaction.
Entries are validated and bounded before the transaction, serialized as one
JSON batch, and inserted in one database operation. A failed entry batch no
longer leaves an empty list behind.

Invalid initial entries are omitted. The returned entry count reflects the
entries that were accepted, not the raw request length.

### Community-report controls

Community reports now accept only the six supported reason values. The handler
and migration 026 enforce the same range.

Each account can create at most 20 reports in a rolling 24-hour window. The
count and insert share a transaction guarded by a PostgreSQL advisory lock, so
concurrent requests cannot exceed the limit. The 21st request receives HTTP
429 with the existing rate-limited error shape.

### Text and quote handling

- Comment quotes are truncated to 300 Unicode code points before moderation.
  The exact moderated value is the value stored.
- Feedback fields retain their byte limits but truncate only at valid UTF-8
  boundaries, preventing malformed PostgreSQL text.
- Public text rejects bidi overrides and isolates, directional marks, soft
  hyphens, zero-width spaces, BOMs, word joiners, and related invisible format
  controls used for spoofing or filter evasion.
- Zero-width joiner and zero-width non-joiner remain allowed for legitimate
  emoji and writing-system use.

### Contact matching

Contact discovery now excludes the caller and blocked relationships in one SQL
query. The server no longer performs one block lookup per discoverable profile.
Uploaded hashes remain transient and are not stored.

## User impact

- Private or blocked content is never exposed because a relationship lookup
  failed. During a database problem, a user may see a temporary server error
  rather than content.
- Shared lists no longer appear partially created after a database failure.
- Heavy reporters receive a deterministic 429 after 20 reports in 24 hours.
- Unsupported report reasons receive a bad-request response.
- Quotes and feedback containing multi-byte characters remain valid after
  truncation.
- Text containing spoofing-oriented invisible characters is rejected, while
  ordinary multilingual text and joined emoji continue to work.
- Contact matching produces the same visible matches with fewer database
  round trips and never returns a blocked account.

## Operator impact

### Signals to monitor

- A rise in server errors from list or reply authorization may indicate a
  database availability problem that older code would have hidden.
- HTTP 429 volume on `/social/report` indicates reporter abuse or a client retry
  loop. The limit is stored in code as 20 per rolling 24 hours.
- PostgreSQL contention on the moderation-report advisory-lock key should be
  brief because the transaction performs only a count and insert.

### Data and migration dependencies

Migration 026 adds the report reason constraint and recent-reporter index. Its
preflight rejects unsupported historical reasons. Repair those rows explicitly
before deployment; silently coercing reasons would damage moderation evidence.

### Privacy model

Blocked or otherwise invisible resources still use not-found responses by
design. Do not replace them with descriptive forbidden responses in proxies or
API gateways, because that would restore an enumeration channel.

## Rollback considerations

Removing fail-closed checks reintroduces privacy exposure during database
errors. Rolling back the report quota can allow concurrent abuse. The migration
down path removes the reason constraint and quota index; prefer a forward repair
if operational trouble is caused by specific data or lock contention.
