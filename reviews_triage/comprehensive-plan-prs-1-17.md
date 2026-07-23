# Comprehensive PR Feedback Triage Plan: PRs #1–#17

Generated July 22, 2026 against `main` at `7abb060`.

## Scope and result

Seventeen dedicated agents fetched the GitHub feedback payload for one PR each, treated that payload as the source of truth, checked historical comments against the current repository, and persisted every classification to `triage_decisions.db`.

| Classification | Count | Interpretation |
| --- | ---: | --- |
| Already fixed | 54 | The underlying issue is absent from current `main`, usually because a later commit repaired it. |
| High-level | 86 | Review rollups, coverage reports, quota/rate-limit notices, service-status messages, or broad guidance without a concrete current change. |
| Should be fixed | 56 | Concrete records that de-duplicate into the workstreams below. |
| Should not be fixed | 19 | Suggestions that conflict with an intentional compatibility/security contract, are unsupported scanner heuristics, or add churn without a demonstrated defect. |
| **Total** | **215** | All stored feedback decisions across PRs #1–#17. |

## Implementation status

All 56 records classified **Should be fixed** have now been implemented and
de-duplicated into the workstreams below. The implementation includes the
forward-only `026_integrity_hardening` migration, focused regressions for the
security and concurrency boundaries, repository-specific Semgrep rules, pinned
Go security tools, and CI enforcement. The 54 **Already fixed** records were
revalidated against the resulting tree; the 19 intentionally rejected records
remain unchanged for the compatibility and evidence reasons documented below.

Verification completed on July 22, 2026 includes the full unit suite, the race
suite, PostgreSQL 18 migration/end-to-end coverage, zero-finding repository
Semgrep and gosec G402 scans, and a zero-reachable-vulnerability `govulncheck`
after upgrading `golang.org/x/text` to v0.39.0.

## Prioritized implementation plan

### P0: critical security, correctness, and atomicity

Implement these first, preferably as small reviewable changes with failure-injection or adversarial tests.

1. **Secure sync-driven feed ingestion (PR #17).** Centralize feed fetching behind a crawler-level validator that permits only HTTP(S), rejects loopback/private/link-local/reserved IPv4 and IPv6, revalidates every redirect and dialed DNS result, caps response bodies, and rate-limits unknown-feed submissions per user. Add redirect, DNS-rebinding, alternate-IP-notation, IPv6, oversized-body, and quota tests.
2. **Fail closed in list authorization (PR #16).** Change `canViewSocialList` to return `(allowed, error)` and propagate block-lookup failures instead of allowing access. Add failure-injection tests for public lists, members, and blocked relationships.
3. **Make account deletion atomic (PR #14).** Refactor social erasure to accept `db.Querier`, then perform push cleanup, social erasure, soft deletion, and token revocation inside one outer `Store.InTx`. Add rollback tests at every mutation boundary.
4. **Honor failed-feed backoff (PR #6).** Check `NextRefreshAt` before `EnsurePodcast` calls `Crawl`; do not synchronously retry during the backoff window. Add a regression test that freezes time and proves no outbound request occurs.
5. **Preserve UTF-8 during feedback byte truncation (PR #10).** Replace raw byte slicing with boundary-safe truncation, retaining the storage byte cap. Add table-driven tests for 2-, 3-, and 4-byte runes crossing the boundary and assert `utf8.ValidString`.

### P1: transactional and persistent data invariants

Complete these after the P0 helper/API shapes settle.

- **Authentication and account state (PR #6):** make refresh-token revoke-and-replace transactional; exclude soft-deleted users at the mutation boundary; shorten the default access-token lifetime while retaining the environment override; reject passwords beyond bcrypt's 72-byte limit.
- **Transcript quotas and validity (PR #12):** require finite, strictly positive episode duration; replace count-then-insert quota checks with one attribution-scoped transactional operation or advisory lock; add concurrent PostgreSQL tests proving the 50-contribution and 200-sighting limits cannot be exceeded.
- **Social-list writes (PR #16):** treat only `pgx.ErrNoRows` as absent membership; propagate other database errors; create lists and initial entries in one transaction; batch the entries rather than inserting sequentially.
- **Weekly digest claims (PR #17):** prevent duplicate delivery across replicas with atomic per-user claims, leases, and `FOR UPDATE SKIP LOCKED` (or an equivalent single-coordinator design); mark sent only after successful delivery and recover expired claims.
- **Sync state (PR #6):** persist history-deletion tombstones and advance `users.sync_last_modified` in the same transaction as settings changes.

### P1: forward-only database integrity migrations

Do not edit migrations that may already be deployed. Add new numbered migrations, preflight/audit existing rows, regenerate sqlc output from `db/queries.sql`, and add migration/e2e fixtures.

- Index `feedback(user_id)` for the `ON DELETE SET NULL` path (PR #10).
- Add active-user email uniqueness compatible with soft deletion (PR #6).
- Enforce `social_profiles(handle,user_id)` ownership through a composite key/FK to `social_handles`; replace the ineffective CITEXT lowercase check with a case-sensitive `handle::text` check (PR #14).
- Constrain moderation report reasons to supported values in both PostgreSQL and the handler (PR #14).
- Exclude tombstoned `user_episodes` from public rankings and regenerate the query code (PR #14).
- Add self-referencing foreign keys for group-post `parent_id` and `root_id` (PR #17).

### P1: bounded background and request work

- **Unknown-feed ingestion (PR #17):** collect URLs during sync, commit first, then dispatch de-duplicated work through a bounded worker/semaphore plus singleflight. Never perform Redis or HTTP work while holding `GetUserForUpdate`'s transaction.
- **Scheduled crawling (PR #6):** use bounded concurrency and de-duplication for due feeds; cap feed bodies before parsing.
- **Group-post notifications (PR #17):** enqueue one fan-out job after creation, and batch recipient visibility/block/mute filtering in SQL instead of doing per-recipient work on the request path.
- **Contact matching (PR #16):** push blocked-pair exclusion into `GetDiscoverableProfileEmails` with `NOT EXISTS`; remove per-profile `IsBlockedEither` calls.
- **Refresh endpoint (PR #6):** batch per-podcast cutoff lookups and validate that `lastEpisodeUuid` belongs to the requested podcast before using it.

### P1: API abuse resistance and validation

- Truncate comment quotes before moderation and store that exact bounded value (PR #17).
- Reject episode shares that omit the required podcast identifier while preserving podcast-only show recommendations (PR #17).
- Reject dangerous bidi overrides/isolates and selected invisible format characters in shared text validation without blanket-rejecting legitimate ZWJ/ZWNJ use (PR #14).
- Add a durable per-account moderation-report quota backed by a shared store/database, with concurrency, 429, and window-rollover tests (PR #14).
- Detect oversized protobuf bodies explicitly, validate forced-refresh UUIDs, and keep generic client errors while logging underlying search/lookup/crawl failures (PR #6).

### P2: operability, reproducibility, and coverage

- Remove the tracked 42 MB debug ELF `main`, add anchored `/main` ignore coverage, and add a Git-aware CI check for tracked executable signatures and unexpectedly large binaries. Treat history rewriting as a separate coordinated decision (PR #9).
- Replace blanket `InsecureSkipVerify` in the loopback HTTPS liveness probe with verification against `TLS_CERT_FILE` or a pinned certificate/public key; reject redirects and test mismatches (PR #9).
- Run the Sunday digest sweep at or after 17:00 UTC, order candidates deterministically by oldest/null watermark then user ID, and log non-unregistered APNs failures (PR #17).
- Log pending-feed enqueue failures and restore OPML success-path coverage (PR #6).
- Pin `protoc-gen-go` and `sqlc` versions and correct stale social protobuf route comments before regenerating output (PRs #6 and #14).

## Prevention rules

Dependency-cruiser is not recommended: this is a Go repository, and the high-risk findings are data-flow, transaction, and network-boundary problems rather than JavaScript/TypeScript module-boundary violations.

Add a small repository-specific Semgrep policy and native Go checks:

- Flag `http.Get`, `http.DefaultClient.Do`, or unguarded `http.Client` use for user-controlled feed URLs outside the approved safe fetcher.
- Flag crawler clients without redirect validation and a guarded `DialContext`; also enable `gosec` G402 for blanket `InsecureSkipVerify`.
- Flag security predicates such as `IsBlockedEither` whose error branch can result in authorization success.
- Flag direct string byte slicing in truncation/byte-cap helpers unless a UTF-8 boundary is established.
- Flag `EnsurePodcast` crawl paths that omit the `NextRefreshAt` guard.
- Flag raw handler calls to quota-count queries after the atomic quota repository operation exists.
- Flag queue/network calls inside `InTx` callbacks and goroutines spawned from request-controlled loops without an explicit limiter.
- Flag `moderation.CheckText(req.<field>)` when a bounded stored value should be moderated instead.
- Retain the raw-error-to-client-response rule suggested by PR #2, even though the original leaks are fixed.
- Use `govulncheck`/Socket for dependency risk and a Git-aware file-signature script for committed binaries; neither concern is well modeled by Semgrep.

## Already-fixed rollup

The 54 fixed records include bounded shutdown and startup cancellation, safer task retry classification, Redis isolation, fail-closed error masking, nested error-kind checks, secret-length validation, transcript normalization, safer metrics/transcript fetch behavior, SSRF-resistant transcript URL fetching, transactional group creation, group-capacity rechecks, safe successor lookup, push-type bounds checks, Unicode-safe report-context truncation, and other later QA repairs. Focused package tests run by the agents and the final full `go test ./...` pass support these classifications.

## High-level rollup

The 86 high-level records are mostly automated review quota/rate-limit notices, contributor dashboards without repository evidence, coverage snapshots without a required threshold, walkthroughs, empty review wrappers, merge/deploy status, and duplicate review summaries. They do not add implementation work beyond the concrete findings above.

## Recommendations intentionally not accepted

- Preserve empty 401/403 bodies because Pocket Casts compatibility depends on the tested status/header contract (PR #2).
- Preserve the 16-character transcript query eligibility check and required authentication on for-podcast group discovery because both are explicit client/security contracts (PRs #12 and #17).
- Preserve anonymous attribution in App Attest `off`/`log-only` modes because those modes intentionally support Simulator, development, and older clients; anonymous quotas remain isolated by hashed client address (PR #12).
- Preserve separate asynq/go-redis setup and one-time `OTEL_SERVICE_NAME` seeding; the libraries' ownership and schema behavior make these intentional (PR #9).
- Do not act on heuristic/archive warnings for checksum-pinned transitive `reflect2`/`json-iterator` dependencies without a disclosed vulnerability (PR #6), or on Socket's administrative ignore command (PR #1).
- Defer linter-only cleanup and speculative helper/refactor suggestions that do not change behavior or address a demonstrated defect (PR #6).

## Verification and delivery order

For each implementation batch: add a failing regression test first, make the smallest scoped change, run focused tests plus `go test -race ./...`, run migration/e2e tests where PostgreSQL behavior is involved, run Semgrep/gosec/govulncheck, and regenerate sqlc/protobuf output only from source definitions. Land P0 changes separately, then transactional/schema work, then bounded-background-work changes, then P2 cleanup.
