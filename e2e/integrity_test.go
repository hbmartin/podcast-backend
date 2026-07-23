//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/hbmartin/podcast-backend/db"
	pb "github.com/hbmartin/podcast-backend/protos/api"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDigestClaimsAreOrderedAndReplicaSafe(t *testing.T) {
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	emailA := fmt.Sprintf("digest-a-%d@e2e.test", suffix)
	emailB := fmt.Sprintf("digest-b-%d@e2e.test", suffix)
	tokenA, _ := registerUser(t, emailA)
	tokenB, _ := registerUser(t, emailB)
	handleA := fmt.Sprintf("digest_a_%d", suffix%1_000_000_000)
	handleB := fmt.Sprintf("digest_b_%d", suffix%1_000_000_000)
	for _, user := range []struct{ token, handle string }{{tokenA, handleA}, {tokenB, handleB}} {
		status := postProto(t, "/social/join", user.token, &pb.JoinRequest{
			Handle: user.handle, AcceptedTermsVersion: 1, DisplayName: user.handle,
		}, &pb.JoinResponse{})
		require.Equal(t, http.StatusOK, status)
	}
	// Each account needs graph activity to be digest-eligible.
	require.Equal(t, http.StatusOK, postProto(t, "/social/follow", tokenA,
		&pb.FollowRequest{Handle: handleB}, &pb.FollowResponse{}))
	require.Equal(t, http.StatusOK, postProto(t, "/social/follow", tokenB,
		&pb.FollowRequest{Handle: handleA}, &pb.FollowResponse{}))

	pool, err := pgxpool.New(ctx, os.Getenv("E2E_DB_CONNECTION_STRING"))
	require.NoError(t, err)
	defer pool.Close()
	store := db.NewStore(pool)

	// Isolate the candidates, then make A the NULL watermark and B the older
	// non-NULL watermark. NULL must be claimed first.
	_, err = pool.Exec(ctx, "UPDATE social_profiles SET digest_claimed_at = now()")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		UPDATE social_profiles SET digest_claimed_at = NULL,
			digest_sent_at = CASE WHEN handle = $1 THEN NULL ELSE now() - interval '7 days' END
		WHERE handle IN ($1, $2)`, handleA, handleB)
	require.NoError(t, err)
	var idA, idB int64
	require.NoError(t, pool.QueryRow(ctx, "SELECT user_id FROM social_profiles WHERE handle = $1", handleA).Scan(&idA))
	require.NoError(t, pool.QueryRow(ctx, "SELECT user_id FROM social_profiles WHERE handle = $1", handleB).Scan(&idB))

	claimed, err := store.ClaimDigestCandidates(ctx, 2)
	require.NoError(t, err)
	assert.Equal(t, []int64{idA, idB}, claimed)

	// Expire both leases and race two replicas. Each user may be returned by
	// only one claimant even when both statements begin together.
	_, err = pool.Exec(ctx, "UPDATE social_profiles SET digest_claimed_at = now() - interval '16 minutes' WHERE user_id IN ($1, $2)", idA, idB)
	require.NoError(t, err)
	start := make(chan struct{})
	results := make(chan []int64, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ids, claimErr := store.ClaimDigestCandidates(ctx, 2)
			results <- ids
			errs <- claimErr
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for claimErr := range errs {
		require.NoError(t, claimErr)
	}
	seen := map[int64]int{}
	for ids := range results {
		for _, id := range ids {
			seen[id]++
		}
	}
	assert.Equal(t, 1, seen[idA])
	assert.Equal(t, 1, seen[idB])
}

func TestDeletedAccountEmailCanBeReused(t *testing.T) {
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	email := fmt.Sprintf("reuse-%d@e2e.test", suffix)
	token, oldUUID := registerUser(t, email)
	status := postProto(t, "/social/join", token, &pb.JoinRequest{
		Handle:               fmt.Sprintf("reuse_%d", suffix%1_000_000_000),
		AcceptedTermsVersion: 1,
		DisplayName:          "Disposable account",
	}, &pb.JoinResponse{})
	require.Equal(t, http.StatusOK, status)

	deleted := &pb.UserChangeResponse{}
	status = postProto(t, "/user/delete_account", token, &pb.BasicRequest{}, deleted)
	require.Equal(t, http.StatusOK, status)
	require.True(t, deleted.Success.GetValue())

	_, newUUID := registerUser(t, email)
	assert.NotEqual(t, oldUUID, newUUID)

	pool, err := pgxpool.New(ctx, os.Getenv("E2E_DB_CONNECTION_STRING"))
	require.NoError(t, err)
	defer pool.Close()
	var active, deletedCount int
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT count(*) FILTER (WHERE deleted_at IS NULL), count(*) FILTER (WHERE deleted_at IS NOT NULL) FROM users WHERE email = $1",
		email).Scan(&active, &deletedCount))
	assert.Equal(t, 1, active)
	assert.Equal(t, 1, deletedCount)
}
