# Sync and catalog consistency

## Purpose

This workstream ensures that device synchronization, catalog ingestion, history
deletion, settings cursors, and refresh cutoffs converge without holding database
transactions across network work.

## Behavior changes

### Durable history deletion

Deleting one history item now upserts a tombstone with `is_deleted=true` and a
modification token instead of physically removing the row. Sync responses return
that row with action `2`, allowing offline devices to observe the deletion.

Adding the same episode with a newer modification token clears the tombstone.
History remains capped at 100 records, including the newest tombstones needed for
convergence. Public recently-played queries exclude tombstoned rows.

### Settings cursor atomicity

Named settings and `users.sync_last_modified` now advance in the same
transaction. A failed settings write cannot publish a cursor that claims the
change committed, and a committed settings change cannot leave a stale cursor.

### Unknown podcast ingestion

Sync checks both the supplied podcast UUID and canonical feed URL before marking
a subscription as unknown. URLs are de-duplicated within the request and passed
to the ingestion dispatcher only after the per-user sync transaction commits.
Queue and HTTP work therefore cannot extend the user-row lock.

### Refresh correctness and query shape

The refresh endpoint now:

- de-duplicates valid podcast UUIDs;
- fetches podcasts and last-episode cutoffs in batches;
- uses a cutoff only when that episode belongs to the requested podcast; and
- falls back to the 20 most recent episodes for missing, invalid, or mismatched
  cutoffs instead of applying another podcast's publication date.

A valid same-podcast cutoff still permits a catch-up response of up to 100
episodes.

## User impact

- A history deletion made on one device is less likely to reappear after another
  device syncs stale state.
- Settings changes consistently advance the sync token and propagate to other
  devices.
- Subscribing to a previously unknown feed no longer makes the sync request wait
  for catalog HTTP work; title, artwork, and episodes may populate shortly after.
- A corrupt or cross-podcast last-episode UUID no longer hides legitimate new
  episodes. The client receives a bounded recent fallback instead.
- Duplicate podcast UUIDs in a refresh request no longer duplicate database work.

## Operator impact

### Storage and query load

History deletions now retain tombstone rows until normal trimming or clear-all
behavior removes them. At the existing 100-row per-user cap, storage growth is
bounded. Refresh uses two batch lookups instead of repeated per-item lookups,
reducing query count for large libraries.

### Asynchronous catalog freshness

Unknown-feed ingestion is post-commit and best effort. Monitor the dispatcher
signals in [NetworkAndFeedIngestion.md](NetworkAndFeedIngestion.md) when users
report subscriptions without catalog metadata.

### Migration dependency

History tombstones require migration 026. Public profile queries also assume the
new `is_deleted` filter. Deploy schema and application together.

## Rollback considerations

Rolling back the schema drops `history.is_deleted` and loses tombstone meaning.
Devices that have already received delete actions may later repopulate older
server history if old code is restored. A forward fix is preferable after the
new sync behavior has served traffic.
