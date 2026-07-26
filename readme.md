# podcast-backend

A self-hosted, open-source re-implementation of the Pocket Casts backend API,
built to serve the open-source Pocket Casts iOS client
([hbmartin/pocket-casts-ios](https://github.com/hbmartin/pocket-casts-ios)).

One Go binary provides every first-party host role the client talks to —
`api`, `refresh`, `cache`, and `search` — behind a single base URL. Wire
formats match the client exactly: Protocol Buffers on the api-host endpoints
(reconstructed from the client's generated `api.pb.swift`, field number for
field number) and JSON matching the client's `Codable`/dictionary decoders
everywhere else.

## What's implemented

| Area | Endpoints |
|---|---|
| Account & auth | `user/login`, `user/register`, `user/token` (device-bound refresh-token families), `user/token/revoke`, `user/reset_password`, `user/change_email`, `user/change_password`, `user/delete_account` |
| Sync | `user/sync/update` (incremental record sync with per-field last-writer-wins), `user/last_sync_at`, `user/podcast/list`, `user/podcast/episodes`, `user/playlist/list`, `user/bookmark/list`, `starred/list` |
| Queue/history/settings | `up_next/sync`, `history/sync` (newest-100 cap), `user/named_settings/update` (per-key modifiedAt merge) |
| Real-time playback | `sync/update_episode`, `sync/update_episode_star` |
| Refresh host | `user/update`, `podcasts/refresh`, `podcasts/show`, `podcasts/search` (feed URLs crawl synchronously; text search proxies the iTunes Search API), `import/opml`, `import/export_feed_urls`, `/health.html` |
| Cache host | `mobile/podcast/full/{uuid}` (ETag/304), `mobile/show_notes/full/{uuid}` (show notes, episode art, Podcasting 2.0 transcripts + chapters URL), `mobile/episode/url/{p}/{e}`, `mobile/podcast/findbyepisode/{p}/{e}`, `mobile/podcast/episode/search`, `episode/search`, `search/combined`, `podcast/rating/{uuid}` |
| Search host | `autocomplete/search` |
| Artwork | Feed artwork plus the finite `discover/images/artwork/{light|dark}/{280|960}/{1..8}.png` default-artwork set |
| Ratings & stats | `user/podcast_rating/add`/`show`/`list`, `user/stats/summary` |
| Discover | `discover/ios/content_v2.json`/`content_v3.json` with catalog-backed sources (trending/popular/recent/categories) |
| Sharing | Bearer+App-Attest `share/list`, public resolution, podcast/episode HTML, Open Graph metadata, and AASA |
| Push notifications | APNs new-episode alerts with the sandbox/production environment stored per device token |
| App security (App Attest) | `attest/challenge`, `attest/enroll`, and canonical method/path/query/body assertions across protected native routes, with monotonic-counter replay defense |
| Product APIs | Durable podcast refresh jobs, capabilities, AI/local folder suggestions, episode and related-podcast recommendations, and social avatar lifecycle |
| Transcript corpus | Idempotent contributions, publisher sightings, immutable candidates/artifacts/releases, manifest-driven reads, and audited admin promotion/rollback |

Intentionally unsupported: user file hosting, TV device auth, Sonos exchange,
subscriptions/IAP, supporter bundles, promotions, paid recommendations, and web
audio playback.

## Architecture

- **Go 1.25**, stdlib `net/http` routing, [sqlc](https://sqlc.dev) + pgx/v5
  over **PostgreSQL**, migrations run automatically at startup
  (golang-migrate).
- **Auth**: bcrypt password hashing, server-minted HS256 access tokens
  (`AUTH_JWT_SECRET`), opaque rotating refresh tokens stored as sha256
  hashes. Error responses use the client's `{"errorMessageId": ...}`
  envelope (`login_email_taken`, `invalid_grant`, ...).
- **Sync engine** (`syncsvc`): each user has monotonic int64-millis sync
  tokens (main / Up Next / history, mirroring the client's three stored
  tokens). Mutations run in a transaction holding a row lock on the user;
  responses echo all records with `modified_at > lastModified`, which the
  client imports idempotently. Episode state and bookmark title/archive
  merge per-field by the client's device-time `*Modified` tokens.
- **Catalog** (`crawler`): podcasts and episodes have *deterministic* UUIDs
  — `uuidv5(namespace, canonical feed URL)` and `uuidv5(podcast uuid, item
  guid)` — so any instance derives identical ids for the same feeds, and
  OPML import polling needs no server-side state. Feeds are fetched with
  conditional GETs and parsed with gofeed; subscribed feeds re-crawl hourly,
  idle ones daily (background jobs on an [asynq](https://github.com/hibiken/asynq)
  Redis queue, swept every 5 minutes). The feed client ignores
  `HTTP(S)_PROXY` by design — a proxy would resolve and dial destinations
  outside the SSRF guard — so feed traffic needs direct egress.
  - Limitation: a bare unknown podcast uuid arriving via sync cannot be
    reverse-resolved to a feed URL. The subscription still syncs across
    devices; catalog data fills in once the server learns the URL (search,
    OPML, another client action).
- **Search**: catalog search uses Postgres `pg_trgm`; text podcast search
  proxies the iTunes Search API (`ITUNES_BASE_URL` to override).

## Running

```bash
export AUTH_JWT_SECRET=$(openssl rand -hex 32)
export POSTGRES_PASSWORD=change-me REDIS_PASSWORD=change-me
docker compose up      # app + postgres + redis
```

Configuration:

| Variable | Meaning |
|---|---|
| `DB_CONNECTION_STRING` | Postgres URL, e.g. `postgres://user:pass@host:5432/podcasts?sslmode=disable` (required) |
| `AUTH_JWT_SECRET` | ≥32 bytes; signs access tokens (required) |
| `PROCESS_ROLE` / `SCHEDULER_MODE` | `web`, `worker`, `refresh-scheduler`, `digest-scheduler`, or local-only `all`; schedulers use `loop` or `once` |
| `WEB_PORT` / `PORT` | listen-address precedence; defaults to `:8000` |
| `QUEUE_REDIS_URL` | required `redis://` or TLS `rediss://` URL for queues, rate limits, sessions, and coordination |
| `AUTH_ACCESS_TOKEN_TTL` / `AUTH_REFRESH_TOKEN_TTL` | defaults `1h` / `2160h` (90-day sliding refresh lifetime) |
| `ITUNES_BASE_URL` | iTunes Search API base, default `https://itunes.apple.com` |
| `PUBLIC_BASE_URL` | custom root HTTPS origin used for links and host enforcement; required in production |
| `ALLOWED_ORIGINS` / `TRUST_PROXY_HEADERS` | explicit CORS origins and provider proxy-header trust; CORS defaults to same-origin |
| `METRICS_TOKEN` / `ADMIN_TOKEN` | protected operations tokens; production requires `ADMIN_TOKEN` |
| `APNS_KEY_BASE64`, `APNS_KEY_ID`, `APNS_TEAM_ID`, `APNS_TOPIC` | complete set enables APNs; one key serves the device token's recorded sandbox or production environment |
| `APP_ATTEST_TEAM_ID` | Apple Developer team id; setting it enables App Attest (challenge/enroll/verify). Unset ⇒ App Attest off, endpoints accept unattested requests |
| `APP_ATTEST_BUNDLE_ID` | app bundle id for the App Attest App ID (`TEAMID.BUNDLEID`), default `au.com.shiftyjelly.podcasts` |
| `APP_ATTEST_ALLOW_DEV` | `true` also accepts the development-environment attestation (`appattestdevelop`); production/TestFlight attest under production regardless. Default `false` |
| `APP_ATTEST_MODE` | `log-only` or `required`; templates start at `log-only` |
| `OBJECT_STORAGE_S3_URL`, `OBJECT_STORAGE_BUCKET`, `OBJECT_STORAGE_ACCESS_KEY_ID`, `OBJECT_STORAGE_SECRET_ACCESS_KEY` | complete set enables the private transcript/avatar object store |
| `GOOGLE_VISION_CREDENTIALS_BASE64` | with object storage, enables avatar upload and nudity/racy SafeSearch filtering |
| `GEMINI_API_KEY` / `GEMINI_MODEL` | enables folder suggestions; the model defaults to the pinned application value |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | set to enable OpenTelemetry tracing (OTLP/HTTP export; standard `OTEL_*` vars respected, service name defaults to `podcast-backend`) |

Operations: public `GET /livez` is process-only liveness and `/health.html` is a
minimal status page. `GET /readyz`, legacy `/health`, and `/metrics` require
`Authorization: Bearer $METRICS_TOKEN`; they return 404 when no token is set.
`podcast-backend -health` is a self-contained container health probe and is
wired as the image's role-aware `HEALTHCHECK`. Tracing samples 100% by default — set
`OTEL_TRACES_SAMPLER`/`OTEL_TRACES_SAMPLER_ARG` (e.g. `traceidratio` / `0.1`)
for high-traffic deployments.

### Pointing the iOS client at this server

Inject the custom root origin through the iOS xcconfig/Info.plist. The client
persists that build origin on first launch and blocks networking after a build
origin change until reinstall; host roles are path-routed through the one
origin. See [Deployment and protocol](docs/DeploymentAndProtocol.md).

## Development

```bash
make test    # unit tests (no network, no database needed)
make lint    # go vet + staticcheck
make proto   # regenerate protos/api from protos/api.proto (needs protoc)
make sqlc    # regenerate db/ from db/queries.sql
make e2e     # end-to-end suite, needs Postgres:
             #   E2E_DB_CONNECTION_STRING=postgres://... make e2e
```

The e2e suite builds the real binary, runs migrations against your Postgres,
and drives the full client loop over HTTP: register → login → two-device
sync convergence → Up Next/history/settings → feed ingestion from a fixture
RSS server → cache-host reads with 304 revalidation.

### Wire-compatibility notes

`protos/api.proto` is reconstructed from the iOS client's generated
SwiftProtobuf code. Field numbers are load-bearing: golden wire-format tests
in `protos/api/wire_test.go` pin the known-tricky ones (field gaps, wrapper
types). If you regenerate the client or add messages, verify against
`api.pb.swift` and extend those tests.
