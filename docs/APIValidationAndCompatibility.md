# API validation and client compatibility

## Purpose

This workstream rejects malformed or ambiguous requests earlier, avoids leaking
internal errors, and reduces database work without changing established client
wire formats.

## Request validation changes

### Protobuf body limits

Plain protobuf bodies are read up to 4 MiB plus one byte. A body over 4 MiB is
distinguished internally as `body_too_large` instead of being truncated and then
misreported as malformed protobuf.

Gzip protobuf endpoints retain an independent 8 MiB decompressed cap. Endpoints
whose middleware caps raw signed bodies can return HTTP 413; handlers that map
decode failures to the legacy invalid-request response retain that client shape.

### Refresh endpoints

- Forced podcast refresh requires a syntactically valid UUID before lookup or
  enqueue.
- Podcast and episode cutoffs are batch loaded and de-duplicated.
- A last-episode UUID is used only when it belongs to the requested podcast.
- Search, lookup, crawl, and enqueue failures return generic client messages but
  record the underlying error in structured server logs.
- Cache-host and pending-feed enqueue errors are no longer silently ignored.

### Social shares

An episode share must include both `episode_uuid` and `podcast_uuid`. A
podcast-only show recommendation remains valid. This removes ambiguous episode
records that downstream clients cannot resolve to a show.

### Feedback and text fields

Feedback keeps its established byte caps—10,000 bytes for the message, 512 KiB
for logs, and 1,000 bytes for smaller fields—while preserving UTF-8 boundaries.
Comment quotes and social fields use rune-aware truncation where the limit is
defined in characters.

## User impact

- Oversized requests fail deterministically instead of being partially parsed.
- Invalid forced-refresh IDs fail without producing unnecessary queue work.
- Refresh requests with a wrong-podcast cutoff receive up to 20 recent episodes
  rather than an incorrect empty/catch-up window.
- Episode shares missing their podcast are rejected as bad requests. Show-only
  recommendations continue to work.
- Client-facing search and refresh errors remain generic; internal database,
  queue, crawler, or upstream details are not exposed.
- Multi-byte feedback text is never cut into invalid UTF-8.

## Operator impact

### Log signals

New diagnostic messages include:

- `Forced refresh lookup failed`
- `Forced refresh crawl failed`
- `Forced refresh enqueue failed`
- `Podcast search failed`
- `Pending podcast enqueue failed`
- `Feed URL refresh enqueue failed`
- `Feed URL search crawl failed`
- `Podcast lookup failed`
- `Podcast lookup feed crawl failed`
- `Pending cache-host podcast enqueue failed`

These logs intentionally contain the operational cause while the response does
not. Apply normal log-access and retention controls because feed URLs and UUIDs
may appear as structured fields.

### Client compatibility

The protobuf schema comments now correctly identify
`POST /social/profile/public` and the separate `GET /u/{handle}` HTML route.
Generated Go output was regenerated from the schema; no wire field numbers or
runtime routes changed as part of that documentation correction.

Existing empty 401/403 bodies and other tested Pocket Casts response contracts
remain unchanged.

## Rollback considerations

These validation changes do not require schema rollback. Reverting them permits
ambiguous shares, cross-podcast cutoffs, silently truncated protobuf bodies, and
less useful operator diagnostics.
