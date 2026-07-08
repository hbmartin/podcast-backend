// Package syncsvc implements the Pocket Casts sync engine: incremental
// record sync, Up Next queue, listening history, and named settings.
package syncsvc

import "time"

// NextToken returns the next per-user sync token: current wall-clock millis,
// guaranteed strictly greater than the previous token so incremental reads
// (modified_at > since) never miss a write even within the same millisecond.
func NextToken(prev int64) int64 {
	now := time.Now().UnixMilli()
	if now <= prev {
		return prev + 1
	}
	return now
}
