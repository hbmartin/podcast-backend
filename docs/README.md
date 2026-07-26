# Feedback hardening operations guide

For the current deployment topology, App Attest/authentication matrix, optional
feature activation, and immutable transcript corpus contract, start with
[Deployment and coordinated protocol](DeploymentAndProtocol.md). That document
supersedes older rollout statements in the workstream history below.

These guides describe the user and operator impact of the security, correctness,
concurrency, and operability work consolidated from pull-request feedback on PRs
1 through 17. They document the behavior in the current branch, including the
limits and failure modes that matter during rollout.

The detailed triage record remains in
[`reviews_triage/comprehensive-plan-prs-1-17.md`](../reviews_triage/comprehensive-plan-prs-1-17.md).

## Workstream index

| Workstream | Primary user impact | Primary operator concern |
| --- | --- | --- |
| [Network and feed ingestion](NetworkAndFeedIngestion.md) | Safer feed discovery with bounded, asynchronous ingestion | Public-only egress, queue saturation, and rejected URL logs |
| [Identity and account lifecycle](IdentityAndAccountLifecycle.md) | Shorter access sessions, safe refresh rotation, and all-or-nothing deletion | Client refresh readiness and the active-email uniqueness change |
| [Database integrity](DatabaseIntegrity.md) | Fewer inconsistent profiles, reports, threads, and deleted-account conflicts | Migration 026 preflight, locking, and rollback limitations |
| [Sync and catalog consistency](SyncAndCatalogConsistency.md) | Deletes and settings converge reliably across devices | Tombstone retention, post-commit ingestion, and refresh query load |
| [Social safety and moderation](SocialModeration.md) | Privacy checks fail closed and abuse controls are consistent | New 429s, database-backed quotas, and authorization error signals |
| [Transcript contributions](TranscriptContributions.md) | Invalid transcripts are rejected and quotas cannot be raced | PostgreSQL advisory locks, rolling limits, and rejection metrics |
| [Background jobs and notifications](BackgroundJobsAndNotifications.md) | Faster request paths and fewer duplicate notifications | Worker deployment order, fallback capacity, and digest leases |
| [API validation and compatibility](APIValidationAndCompatibility.md) | Malformed or ambiguous requests fail predictably | Body limits, generic client errors, and server-side diagnostic logs |
| [Operations and security tooling](OperationsAndSecurityTooling.md) | No direct user-facing protocol change | TLS health checks, CI gates, code generation, and binary policy |

## Recommended rollout order

1. Back up the production database and run the migration 026 preflight queries
   from [DatabaseIntegrity.md](DatabaseIntegrity.md) against a staging copy.
2. Confirm that production feed ingestion does not depend on an HTTP proxy or
   private-address feeds, and confirm the TLS certificate has a DNS or IP SAN.
3. Ensure clients can refresh access tokens before accepting the new one-hour
   default access-token lifetime.
4. If web and workers deploy separately, deploy queue workers before web
   instances so the new group-post fan-out task is recognized immediately.
5. Deploy the application. Startup applies migrations before serving traffic.
6. Monitor the log messages and metrics listed in each guide, particularly URL
   rejection, ingestion saturation, quota responses, digest failures, and task
   enqueue failures.

## Verification baseline

The hardening change was verified with:

```sh
go test -race ./...
go vet ./...
make security
scripts/check-no-tracked-binaries.sh
```

Migration 026 was also exercised up, down, and up again on a fresh PostgreSQL
18 database, followed by the complete `e2e` test package.

## Compatibility policy

The hardening deliberately preserves existing Pocket Casts compatibility where
feedback conflicted with a tested protocol contract. In particular, empty
401/403 response bodies, required authentication for group discovery, the
transcript query eligibility rule, and App Attest development modes were not
changed. The guides call out the areas where behavior did change.
