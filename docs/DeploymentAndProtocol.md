# Deployment and coordinated protocol

This document is the operational contract shared with the companion iOS
release. It supersedes earlier write-only transcript and per-host assumptions.

## Runtime and providers

The single multi-architecture image selects `web`, `worker`,
`refresh-scheduler`, or `digest-scheduler` with `PROCESS_ROLE`. `all` is only a
local compatibility role and is rejected in production. Only web migrates;
other roles wait up to five minutes for the expected schema. Railway runs all
four roles with scheduler loops. Render uses web, worker, and two one-shot cron
jobs from `render.yaml`.

Production requires PostgreSQL, persistent no-eviction Redis/Valkey,
`PUBLIC_BASE_URL`, `ADMIN_TOKEN`, and an explicit role. `PUBLIC_BASE_URL` is a
custom root HTTPS origin. Public HTML requests on a provider host redirect to
it; API and artifact requests on the wrong host receive 421. Provider-native
database backups should be enabled; restore into a new database, verify schema
and readiness, then repoint every role together.

Images publish publicly at `ghcr.io/hbmartin/podcast-backend` for amd64 and
arm64 with immutable calendar and full-commit tags, provenance, SPDX SBOM, and
keyless Cosign signing. Promotion verifies all supply-chain evidence (Cosign
signature plus GitHub provenance and SPDX SBOM attestations) and pins provider
services by digest. The Railway template itself must be repinned by a human in
the template editor. Rollback re-verifies the same evidence, redeploys an
earlier digest, and never runs down-migrations.

Current provider costs must be checked immediately before deployment using the
official [Railway pricing](https://docs.railway.com/pricing/plans) and [Render
pricing](https://render.com/pricing) pages. Railway should have $20 and $30
spend alerts without a hard stop.

## App Attest and authentication

The signed request input is exactly:

```text
v1\n<METHOD>\n<normalized escaped path>\n<sorted RFC3986 query>\n<lowercase SHA-256 of exact body bytes>
```

Duplicate query keys are preserved and encoded key/value pairs are sorted.
Ambiguous noncanonical paths are rejected. Authenticated APIs, including login,
registration, and token issuance, require App Attest according to
`APP_ATTEST_MODE`. Optional-auth routes require it when Bearer auth is supplied;
anonymous native compute/data routes require it; public catalog, HTML, sharing
resolution, artwork, AASA, and liveness do not. Refresh exchange accepts Bearer
or App Attest; possession-only token revocation requires neither.

Login/register/reset require the ordinary installation identifier. Access
tokens last one hour. Refresh tokens rotate inside a device-bound, 90-day
sliding family; replay revokes the family. Password changes, reset, and account
deletion revoke all families. Reset codes are admin-generated, single-use,
hashed, and valid for 15 minutes; no email provider is involved.

## Optional features

Complete S3-compatible object-storage configuration enables the private corpus.
Adding complete Google Vision credentials enables avatars. Gemini credentials
enable AI folder suggestions. Partial sets remain disabled, report false in
`/api/v1/capabilities`, and return 404. Avatar scanning rejects nudity/racy
SafeSearch results at `LIKELY` or `VERY_LIKELY`; it is not represented as CSAM
detection. APNs accepts only a base64 key and stores sandbox/production alongside
each device token.

## Corpus and admin

Corpus candidates, artifacts, provenance, decisions, release snapshots, and
audit history are immutable. One active release exists per language, but its
transcript, fingerprint, summary, and chapters may be selected independently.
Publisher artifacts auto-promote only when their exact URL is currently declared
by the cataloged feed. Admin edits create derived candidates and rollback
activates an old snapshot.

Contribution writes are idempotent by content hash. A transcript response
returns a candidate ID, SHA-256, and single-use candidate-scoped attachment
token; summary and chapters attach later with that token. Reads resolve language
from `Accept-Language`, podcast language, then `und`, and expose only backend
capability URLs in typed manifests. They require App Attest and use private,
no-cache ETag revalidation.

`/admin` uses the constant-time admin token to issue a Redis-backed secure,
HttpOnly, SameSite=Strict session. Same-origin, CSRF, CSP, session expiry, and
auditing protect listing, preview, promotion, rejection, derived edits,
rollback, and reset-code creation.
