# Database integrity and migration 026

## Purpose

Migration `026_integrity_hardening` moves application assumptions into
PostgreSQL constraints and adds the state required for durable deletes and
replica-safe digest delivery.

## Schema changes

- **Feedback:** an index on `feedback(user_id)` prevents account deletion and
  foreign-key maintenance from scanning the entire table.
- **Accounts:** a partial unique index on active email addresses lets a deleted
  account's email register again without permitting two active owners.
- **Social handles:** lowercase normalization, a case-sensitive format check,
  and composite handle/user uniqueness keep handles canonical and owned by the
  correct account.
- **Social profiles:** a composite foreign key prevents a profile from claiming
  another account's handle.
- **Reports:** a reason constraint blocks unsupported values, while a
  recent-reporter index supports the rolling quota.
- **Group posts:** parent/root self-referencing foreign keys with
  `ON DELETE SET NULL` prevent dangling thread pointers.
- **History:** the non-null `is_deleted` flag persists listening-history
  tombstones for cross-device synchronization.
- **Weekly digest:** nullable `digest_claimed_at` leases prevent multiple
  replicas from claiming the same digest concurrently.

## Preflight failures

Migration 026 deliberately stops before adding constraints if it finds:

- a social profile whose `(handle, user_id)` does not match a handle record;
- a moderation report reason outside the supported range; or
- a group post whose `parent_id` or `root_id` points to a missing post.

Run these read-only checks before the production migration:

```sql
SELECT sp.user_id, sp.handle
FROM social_profiles sp
LEFT JOIN social_handles sh
  ON sh.handle = sp.handle AND sh.user_id = sp.user_id
WHERE sh.handle IS NULL;

SELECT id, source, reason
FROM moderation_reports
WHERE NOT (
  (source = 'community_flag' AND reason BETWEEN 1 AND 6)
  OR (source <> 'community_flag' AND reason BETWEEN 0 AND 6)
);

SELECT post.id, post.parent_id, post.root_id
FROM social_group_posts post
LEFT JOIN social_group_posts parent ON parent.id = post.parent_id
LEFT JOIN social_group_posts root ON root.id = post.root_id
WHERE (post.parent_id IS NOT NULL AND parent.id IS NULL)
   OR (post.root_id IS NOT NULL AND root.id IS NULL);
```

Any returned row requires an explicit data-repair decision. Do not bypass the
preflight by editing a previously deployed migration.

## User impact

- Active-account email conflicts, handle ownership mismatches, invalid report
  reasons, and dangling group thread pointers fail at the write boundary.
- Existing handle spelling is normalized to lowercase.
- Deleted listening-history items and digest claims gain durable storage.
- Normal requests should not change shape because sqlc output was regenerated
  against the new schema and queries.

## Operator impact

### Deployment

- Take a database backup before applying the migration.
- Run the preflight on a recent production copy and repair every returned row.
- Schedule for a period where brief table locks are acceptable. The migration
  updates handle tables and changes constraints/indexes.
- Application startup runs migrations before serving, so a preflight exception
  prevents the instance from becoming ready rather than serving on a partial
  schema.

### Validation after migration

Confirm that migration version 26 is clean, then verify:

```sql
SELECT indexname FROM pg_indexes
WHERE indexname IN (
  'feedback_user_idx',
  'users_active_email_unique',
  'moderation_reports_reporter_recent_idx'
);

SELECT column_name FROM information_schema.columns
WHERE table_name = 'history' AND column_name = 'is_deleted';

SELECT column_name FROM information_schema.columns
WHERE table_name = 'social_profiles' AND column_name = 'digest_claimed_at';
```

## Rollback considerations

The down migration is structurally complete but can be lossy or blocked:

- history tombstone state is dropped;
- digest leases are dropped;
- the old ineffective CITEXT handle check is restored; and
- restoring global email uniqueness fails if an email has been reused after an
  older account was deleted.

Prefer a forward repair over rollback once production writes have used the new
columns or active-email semantics.
