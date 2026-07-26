# Identity and account lifecycle

## Purpose

This workstream shortens credential exposure, prevents refresh-token races,
enforces bcrypt's real input limit, and makes account deletion atomic.

## Behavior changes

### Access and refresh tokens

- The default access-token lifetime is now one hour instead of 24 hours.
- `AUTH_ACCESS_TOKEN_TTL` still overrides the default with any positive Go
  duration such as `30m` or `2h`.
- Refresh-token lookup takes a row lock. Lookup, access-token minting, old-token
  revocation, and replacement-token creation occur in one transaction.
- Rotation succeeds only when exactly one live old token was revoked. Concurrent
  attempts cannot both mint a valid replacement.
- User mutation locks exclude accounts with `deleted_at` set.

### Password length

Registration and password changes reject passwords shorter than six characters
or longer than 72 UTF-8 bytes. The upper limit matches bcrypt's input boundary;
it prevents two longer passwords with the same first 72 bytes from being treated
as equivalent.

### Account deletion

Account deletion now performs all of the following in one database transaction:

1. clear push state;
2. erase social profile data and related edges;
3. tombstone the social handle;
4. soft-delete the user; and
5. revoke every live refresh token.

If any step fails, every step rolls back. The user cannot be left deleted but
still receiving pushes, or partially visible in social data.

Deleted accounts no longer reserve their email address because migration 026
enforces uniqueness only for rows where `deleted_at IS NULL`.

## User impact

- Signed-in clients need to refresh access tokens more frequently. A healthy
  client with a refresh token should see no interactive login change.
- Reusing the same refresh token concurrently now produces one successful
  rotation; the losing request receives the existing invalid-grant behavior.
- Very long passwords that were previously silently truncated by bcrypt are now
  rejected explicitly.
- Account deletion is all-or-nothing. After successful deletion, push tokens and
  refresh tokens are unusable and the email address can register a new account.
- Social handles remain tombstoned after deletion and cannot be claimed by a new
  account. This preserves identity and anti-impersonation behavior.

## Operator impact

### Configuration

Review `AUTH_ACCESS_TOKEN_TTL`. Leaving it unset now means `1h`. Operators may
temporarily set a longer positive duration during client migration, but doing so
extends the usefulness of a stolen access token.

### Monitoring

After rollout, watch for:

- an increase in token refresh volume;
- spikes in invalid-grant responses that could indicate a client reusing a
  rotated refresh token;
- registration failures around the 72-byte password boundary; and
- account-deletion transaction errors.

### Database dependency

Email reuse depends on `users_active_email_unique` from migration 026. The new
application should not serve traffic against a schema that lacks that migration.

## Rollback considerations

Rolling back migration 026 after a deleted email has been reused can fail when
the old global `UNIQUE (email)` constraint is restored. Audit duplicate email
values across active and deleted rows before running the down migration. Token
and deletion transaction changes themselves do not require data conversion.
