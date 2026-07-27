# Transcript contributions and sightings

## Purpose

Transcript submissions are partly user- or installation-attributed and can be
large or expensive to process. This workstream makes validity and daily quotas
atomic under concurrency.

## Behavior changes

### Duration and cue validation

A generated transcript contribution now requires an episode duration that is:

- finite;
- not NaN;
- strictly greater than zero; and
- within 20 percent of the final parsed VTT/SRT cue time.

The cue check always runs. A missing cue or a zero, negative, infinite, or NaN
duration receives HTTP 422 with the generic invalid-submission response.

### Atomic rolling quotas

The following rolling 24-hour limits are enforced per attribution bucket:

| Submission | Limit |
| --- | ---: |
| Transcript contribution | 50 |
| Publisher transcript sighting | 200 |

The service takes a transaction-scoped PostgreSQL advisory lock derived from
the submission kind, attribution type, and attribution ID. It then counts and
inserts in the same transaction. Concurrent requests cannot race past the cap,
and a count failure fails closed rather than accepting an unmetered write.

Quota responses use HTTP 429 and `Retry-After: 3600`. The window is still a
rolling 24 hours; the header is a conservative retry hint, not a promise that
the entire quota resets in one hour.

Attribution is resolved in this order:

1. authenticated user UUID;
2. App Attest installation key; or
3. a short hash of the direct remote address for anonymous traffic.

### Relevant existing size limits

The hardening was verified against the existing limits operators need when
debugging rejections:

- compressed contribution request: 3 MiB;
- compressed sighting request: 64 KiB;
- decompressed protobuf body: 8 MiB;
- decompressed VTT: 2 MiB; and
- decompressed fingerprint: 512 KiB.

Publisher sighting URLs must remain HTTP(S), have no embedded credentials, and
must not contain query names that look like tokens, signatures, keys, sessions,
expiry values, or policies.

## User impact

- Invalid duration metadata can no longer bypass cue validation.
- Exactly 50 contributions or 200 sightings can be accepted for one attribution
  in a rolling day, even when submissions arrive concurrently.
- Quota exhaustion produces an explicit retryable 429 instead of inconsistent
  acceptance.
- Duplicate sightings retain the accepted response and do not enqueue another
  fetch.
- Anonymous users behind the same direct proxy address share a quota bucket.

## Operator impact

### Database behavior

The advisory lock is transaction-scoped and serializes only submissions with
the same kind and attribution. It does not globally serialize transcript work.
Slow count queries or a missing index would lengthen same-user submission
latency, so watch PostgreSQL transaction and lock wait times.

### Metrics

The `/metrics` endpoint exposes:

- transcript contributions by normalized engine;
- sightings by accepted/duplicate outcome; and
- transcript rejections by cause, including `duration`, `vtt`, `size`, and
  `rate_limit`.

Engine labels are normalized to a fixed set; unknown engine names become
`other` to avoid unbounded Prometheus cardinality.

### Queue-less mode

Sighting content fetches prefer the task queue. Without it, in-process fetching
is limited to eight concurrent one-minute jobs. Excess fallback work is dropped
with `dropping in-process sighting fetch; concurrency limit reached`.

## Rollback considerations

The quota lock uses existing transcript tables and does not require a new
column. Rolling it back restores a count-then-insert race and should be avoided.
The migration's recent-report index is unrelated to transcript quotas.
