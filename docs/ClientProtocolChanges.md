# Client-visible changes from the PR #22 review fixes

This guide is written for the coordinated iOS client
(hbmartin/pocket-casts-ios#327). It lists every behavior change from the
post-review hardening pass that a client can observe on the wire, with the
required client action for each. Server-internal fixes (queue durability,
migration locking, CI/CD supply-chain repairs) are summarized at the end for
operators; they need no client work.

No protobuf schema changed in this pass. All wire-format definitions in
`protos/api.proto` are exactly as reviewed.

## Changes that need client attention

### 1. `GET /api/v1/capabilities`: `serverVersion` is now conditional

`serverVersion` is included only when the request carries a **verified App
Attest assertion**. Unattested requests (including all requests while the
server runs in `log-only` mode and the client omits assertion headers) receive
only `appAttestMode` and `features`.

**Client action:** treat `serverVersion` as optional in the response model. If
the client gates features on `serverVersion`, send the capabilities request
through the same attested-request path as other API calls.

### 2. `POST /user/token`: attestation is verified on every path

Previously a request that carried an `Authorization` header skipped App Attest
verification entirely. Now the assertion is verified on both the
with-Authorization and without-Authorization paths. In `required` mode a
refresh call without valid attest headers fails with `401
invalid_attestation`.

**Client action:** attach the App Attest assertion headers to `/user/token`
requests unconditionally, even when also sending a Bearer token.

### 3. `POST /user/token`: wrong `device` now revokes the token family

Presenting a live refresh token with a `device` value that does not match the
token's binding is treated as a theft signal, the same as reusing a rotated
token: the whole family is revoked and the response is `400 invalid_grant`.
Previously the token stayed usable, which allowed unlimited device-id
guessing.

**Client action:** none for correct clients (the device id sent at
login/register must be the one sent on refresh — already the contract). Be
aware that a device-id mismatch is now unrecoverable: the user must sign in
interactively again. Restore-from-backup flows that regenerate the install
identifier must go through login, not token refresh.

### 4. Podcast refresh polling: failures now terminate honestly

`GET /api/v1/update_podcast/jobs/{id}` keeps its wire contract (`202` while
running, `200` when done), but a refresh whose crawl failed now records a
`failed` terminal status instead of reporting `completed`. Both terminal
states end polling with `200`.

**Client action:** none required (the polling loop is unchanged). Do not
assume `200` means new episodes are present; re-fetch the podcast state, which
also carries the recorded crawl failure, as before.

### 5. Push registration: `push_environment` is sticky

In the `POST /user/update` registration fields, an omitted or unrecognized
`push_environment` now **preserves the stored environment** instead of
silently overwriting it to `production`. New device rows still default to
`production`.

**Client action:** keep sending `push_environment` on every registration ping
(`sandbox` from development builds, `production` otherwise). The change makes
an occasional omission harmless instead of breaking sandbox push delivery.

### 6. Transcript contribution attachment tokens expire in 15 minutes

`attachment_token` in `TranscriptContributionResponse` was previously valid
forever; it now expires 15 minutes after issuance, and it can only be
refreshed by the **same contributor** re-submitting the same content. A
different account submitting identical content no longer replaces (or gains)
the attach token for the existing candidate; its returned token simply will
not authorize attachment.

**Client action:** upload artifacts and attach metadata promptly after the
contribution response. On `4xx` from the attach endpoint after a delay,
re-submit the contribution to obtain a fresh token.

### 7. Rate-limit identity for unattested requests is the client IP

For `POST /podcast/suggest_folders`, corpus reads, and
`GET /recommendations/podcast/{uuid}`, unattested requests are now rate
limited per client IP. The `X-Installation-ID` header no longer influences
rate limiting (it was spoofable). Attested requests keep the finer
per-installation limits keyed by the App Attest key id.

**Client action:** none for attested builds. Development/simulator builds
sharing a NAT egress may see `429` sooner than before; the `Retry-After`
header is authoritative.

### 8. `features.avatar` reflects real availability

The capabilities flag is now `true` only when both image moderation and object
storage are configured server-side, matching whether `POST /social/avatar`
will actually accept uploads. Previously the flag could be `true` while
uploads returned errors.

**Client action:** none; the flag is now trustworthy.

## Rollout facts unchanged from the PR description

- Migration 027 still purges all legacy refresh tokens: every user signs in
  again once after the upgrade.
- The schema version the non-web roles gate on is now **33** (four new
  migrations: push-environment constraint validation, concurrent recommendation
  and refresh-token indexes, refresh-token FK repair, and legacy attachment-token
  expiry). This is transparent to clients.

## Server-side fixes with no client impact (operator summary)

Full operator detail lives in
[Deployment and coordinated protocol](DeploymentAndProtocol.md).

- **Deploy pipeline:** promotion and rollback now verify the Cosign signature
  plus GitHub provenance/SPDX SBOM attestations (via `gh attestation verify`);
  rollback verifies before deploying, rolls providers back before the git pin,
  and fails fast when no live provider is configured. Publish refuses to
  overwrite an existing immutable version tag. All actions are SHA-pinned and
  CI test failures fail the build again.
- **Configuration:** `METRICS_TOKEN` (≥32 bytes) is now required in
  production, matching `ADMIN_TOKEN`; a missing value previously 404'd
  `/readyz`, `/health`, and `/metrics` with no startup error.
- **Durability:** the weekly digest sweep retries after a mid-sweep crash
  instead of skipping the week; a corrupt legacy transcript row is skipped
  (and logged) instead of stalling the corpus import forever; object-store
  cleanup uses a real lease; every queue task now has an execution timeout;
  APNs 429/5xx responses are retried with backoff only when the notification
  carries a stable collapse identifier.
- **Privacy:** legacy corpus rows contributed by a since-deleted account are
  imported as `anonymized`, closing the gap where the deletion trigger ran
  before the import.
- **SSRF:** transcript-sighting fetches now share the crawler's full IP
  blocklist (adds CGNAT, benchmark, and translation ranges).
- **Performance:** episode-recommendation candidates are bounded (newest 1000
  within 90 days) and served by a new `episodes(published_at DESC)` index.
- **Alerting:** the digest-overdue Grafana rule can actually fire now, and all
  rules declare explicit no-data/error states.
