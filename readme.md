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
| Account & auth | `user/login`, `user/register`, `user/token` (refresh_token grant with rotation), `user/forgot_password`, `user/change_email`, `user/change_password`, `user/delete_account` |
| Sync | `user/sync/update` (incremental record sync with per-field last-writer-wins), `user/last_sync_at`, `user/podcast/list`, `user/podcast/episodes`, `user/playlist/list`, `user/bookmark/list`, `starred/list` |
| Queue/history/settings | `up_next/sync`, `history/sync` (newest-100 cap), `user/named_settings/update` (per-key modifiedAt merge) |
| Real-time playback | `sync/update_episode`, `sync/update_episode_star` |
| Refresh host | `user/update`, `podcasts/refresh`, `podcasts/show`, `podcasts/search` (feed URLs crawl synchronously; text search proxies the iTunes Search API), `import/opml`, `import/export_feed_urls`, `/health.html` |
| Cache host | `mobile/podcast/full/{uuid}` (ETag/304), `mobile/show_notes/full/{uuid}`, `mobile/episode/url/{p}/{e}`, `mobile/podcast/findbyepisode/{p}/{e}`, `mobile/podcast/episode/search`, `episode/search`, `search/combined`, `podcast/rating/{uuid}` |
| Search host | `autocomplete/search` |
| Artwork | `discover/images/{size}/{uuid}.jpg` (redirect to feed art), `discover/images/metadata/{uuid}.json` (lazily computed cover colors) |
| Ratings & stats | `user/podcast_rating/add`/`show`/`list`, `user/stats/summary` |
| Discover | `discover/ios/content_v2.json`/`content_v3.json` with catalog-backed sources (trending/popular/recent/categories) |
| Sharing | `share/list` (+ `GET /l/{code}` resolution), shared `podcast/{uuid}` and `episode/{uuid}` link lookups |
| Push notifications | APNs new-episode alerts: token registration rides on `user/update` (`push_token`/`push_on`/`push_messages_on`), delivery fires from feed crawls (set `APNS_*`) |

Not implemented (yet): user file uploads, TV device auth, Sonos,
transcripts, recommendations, supporter bundles.

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
  Redis queue, swept every 5 minutes).
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
| `WEB_PORT` | listen address, default `localhost:8000` |
| `ENABLE_TASK_QUEUE` / `QUEUE_REDIS_ADDRESS` | enable background crawling (recommended) |
| `AUTH_ACCESS_TOKEN_TTL` / `AUTH_REFRESH_TOKEN_TTL` | defaults `24h` / `8760h` |
| `ITUNES_BASE_URL` | iTunes Search API base, default `https://itunes.apple.com` |
| `ALLOWED_ORIGIN`, `TLS_CERT_FILE`, `TLS_CERT_KEY_FILE` | CORS / TLS |
| `PUBLIC_BASE_URL` | base for generated links (share URLs, discover sources); set it behind a reverse proxy so client-supplied `X-Forwarded-*` headers aren't trusted |
| `RATE_LIMIT_AUTH` | per-IP requests/minute on the credential endpoints (login, register, forgot password, token), default `10`, `0` disables |
| `SHARING_CREDENTIAL` | optional; when set, `share/list` requests must carry the client's legacy SHA-1 signature |
| `APNS_KEY_FILE`, `APNS_KEY_ID`, `APNS_TEAM_ID`, `APNS_TOPIC` | set all four to enable APNs push (`.p8` auth key path, key id, team id, app bundle id) |
| `APNS_ENDPOINT` | APNs host override, e.g. `https://api.sandbox.push.apple.com` for development builds |

### Pointing the iOS client at this server

In your `pocket-casts-ios` fork, set the first-party base URLs in
`Modules/Sources/PocketCastsServer/Public/Sharing/Structs/ServerConstants.swift`
(`main()`, `api()`, `cache()`, and the `search` host) to this server's base
URL. Host roles are path-routed, so one URL serves them all.

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
