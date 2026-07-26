# Network and feed ingestion

## Purpose

Feed URLs can originate from search, OPML, and synchronized client data. This
workstream makes those inputs safe to fetch and bounds the work they can create.

## Behavior changes

### Public-network-only feed fetching

Production feed fetching now:

- accepts only `http` and `https` URLs;
- rejects URLs with embedded user information or no hostname;
- rejects loopback, private, link-local, carrier-grade NAT, documentation,
  multicast, reserved, NAT64, 6to4, and other non-public IPv4/IPv6 ranges;
- resolves and validates every DNS answer immediately before dialing;
- repeats validation for every redirect and permits at most 10 redirects;
- disables proxy use so resolution and connection checks cannot be bypassed;
- uses a 10-second dial timeout and a 30-second total HTTP timeout; and
- rejects a feed body larger than 20 MiB before parsing it.

The validation at dial time is important: validating only the URL string would
still permit a hostname to resolve to an internal address or change answers
between validation and connection.

### Failed-feed backoff

`EnsurePodcast` no longer retries a feed marked `failed` until its stored
`next_refresh_at` is reached. A client lookup during that window receives the
existing catalog state instead of causing another outbound request.

### Sync-driven ingestion dispatcher

Unknown feed URLs discovered during sync are collected inside the transaction
but dispatched only after commit. The dispatcher has:

- 4 workers;
- a 128-job in-memory queue;
- a two-minute timeout per ingestion job;
- global in-flight de-duplication by canonical feed URL; and
- a per-user token bucket averaging 20 unknown feeds per hour, with a burst of
  up to 20.

When the external task queue is enabled, a worker enqueues OPML ingestion. When
it is disabled, the same bounded dispatcher calls the crawler directly.

## User impact

- A malicious or accidental internal URL can no longer make the service connect
  to loopback, private infrastructure, cloud metadata, or reserved networks.
- A new valid public feed may become available asynchronously rather than during
  the sync request. Existing poll behavior remains the client contract.
- A burst of more than 20 previously unknown feeds from one account may be
  delayed or discarded from the immediate dispatcher. A later sync can submit
  them again after capacity refills.
- Oversized feeds and feeds that depend on private network access now fail.
- Failed feeds honor their retry schedule, reducing repeated latency and load.

## Operator impact

### Network assumptions

- Feed egress must have direct public DNS and public HTTP(S) access. Environment
  proxy settings are intentionally ignored for crawler traffic.
- Private feeds are unsupported in production. The only escape hatch requires
  both `ENV=e2e` and `ALLOW_PRIVATE_FEED_URLS=true`; it exists for local E2E
  fixtures and must not be enabled in a deployed environment.
- A hostname with a mixture of public and non-public DNS answers is rejected in
  full rather than trying only the public answers.

### Capacity and failure signals

Monitor these structured log messages:

- `sync ingestion URL rejected`
- `sync ingestion rate limit exceeded`
- `sync ingestion queue full`
- `sync ingestion failed`

Crawler outcomes continue to update the crawl metrics with `ok`, `failed`, and
`not_modified` labels. A queue-full or rate-limit event is non-blocking for the
sync request, so logs and catalog freshness are the primary operational signals.

### Rollout checklist

1. Inventory feeds with private literal addresses or internal-only DNS.
2. Confirm no production deployment requires an outbound HTTP proxy for feeds.
3. Alert on sustained ingestion rejection or queue-full logs.
4. Confirm the worker/task queue is healthy if asynchronous ingestion is used.

## Rollback considerations

This workstream has no dedicated schema change. Rolling back restores the old
network behavior, including its SSRF exposure and unbounded sync dispatch, so a
rollback should be treated as a security exception rather than a neutral
operational change.
