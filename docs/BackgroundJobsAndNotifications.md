# Background jobs and notifications

## Purpose

This workstream removes potentially large fan-out and crawl loops from request
paths, bounds concurrency, and makes weekly digest delivery replica-safe.

## Scheduled podcast crawling

The due-podcast task now feeds a fixed pool of eight workers rather than walking
feeds serially or spawning unbounded work. Podcast UUIDs are de-duplicated within
the database batch, and cancellation stops job submission promptly.

The due-feed database batch remains capped at 200. Therefore one task holds at
most 200 podcast records in memory and performs at most eight outbound crawls at
once.

### Crawl user impact

Large refresh batches finish faster without allowing feed traffic to grow with
the batch size. Duplicate due rows no longer cause duplicate crawls.

### Crawl operator impact

Budget outbound connection, DNS, and database capacity for eight simultaneous
crawls per worker process. Horizontal worker scaling multiplies that ceiling.
Individual crawl failures log `Scheduled crawl failed` and do not abort the
remaining batch.

## Group-post notification fan-out

A new top-level group post now creates one low-priority task with a deterministic
task ID based on the post ID. Task-ID conflicts are treated as successful
de-duplication.

The worker performs one SQL query for eligible recipients. It includes members
who enabled post notifications and excludes the actor, blocked pairs, and users
who muted the actor before sending push notifications.

If the queue is disabled, the web process uses a global four-slot semaphore and
a one-minute background timeout. When all four slots are occupied, new fan-out
is dropped and logged rather than creating unbounded goroutines.

### Group notification user impact

- Posting no longer waits for every recipient lookup and push send.
- Notifications are eventually delivered by the low-priority queue.
- In queue-less overload, the post still succeeds but some group notifications
  may be omitted.

### Group notification operator impact

- Deploy workers that recognize `push:group_post_fanout` before web instances
  that enqueue it.
- Keep the low-priority queue running and monitor its latency.
- Alert on `group post fanout enqueue failed`, `group post fanout target query
  failed`, and `group post fanout dropped; concurrency limit reached`.

## Weekly digest

The scheduler checks hourly and may run Sunday at or after 17:00 UTC. Allowing
the full post-17:00 window means an instance restart at exactly 17:00 does not
skip the week.

Candidate claims are atomic:

- candidates are ordered by oldest/null `digest_sent_at`, then user ID;
- each query claims up to 500 rows with `FOR UPDATE SKIP LOCKED`;
- `digest_claimed_at` acts as a 15-minute lease;
- a user must not have been sent a digest in the last six days; and
- a user needs an active follow graph or a milestone from the last seven days.

The sweep loops through batches until no candidate remains. The sent watermark
is written only after successful delivery, or after determining that the digest
has no body. Delivery failure leaves the claim to expire and retry.

Unregistered APNs tokens are cleared. Other delivery failures are logged and
returned to the sweep so the user is not incorrectly marked sent.

### Digest user impact

- Multiple application replicas no longer send the same weekly digest
  concurrently.
- A transient push failure can retry after the 15-minute lease instead of
  suppressing the digest for a week.
- Oldest and never-sent eligible users are processed before recently sent users.

### Digest operator impact

Monitor `Digest sweep query failed`, `Digest push failed`, `digest push delivery
failed`, and `Unable to set digest watermark`. Sustained failures can produce
retries every 15 minutes during the Sunday window.

## Rollback considerations

Migration 026 supplies `digest_claimed_at`. Rolling back either the application
or column removes replica-safe claims and can restore duplicate sends. Draining
new group fan-out tasks before deploying an older worker prevents unknown-task
failures during rollback.
