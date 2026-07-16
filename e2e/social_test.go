//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	pb "github.com/hbmartin/podcast-backend/protos/api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSocialIdentityLoop walks the full social foundation over real HTTP/DB:
// availability -> join -> duplicate-claim loses -> profile get/update ->
// visibility-filtered public read -> block hides -> unblock restores ->
// report -> erase -> tombstoned handle is never reissued (ADR-0005/6/7).
func TestSocialIdentityLoop(t *testing.T) {
	suffix := time.Now().UnixNano()
	tokenA, _ := registerUser(t, fmt.Sprintf("social-a-%d@e2e.test", suffix))
	tokenB, uuidB := registerUser(t, fmt.Sprintf("social-b-%d@e2e.test", suffix))

	handle := fmt.Sprintf("e2e_user_%d", suffix%1_000_000_000)

	// Availability: fresh handle is claimable; reserved word is not.
	avail := &pb.HandleAvailabilityResponse{}
	status := postProto(t, "/social/handle/availability", tokenA,
		&pb.HandleAvailabilityRequest{Handle: "@" + handle}, avail)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, pb.HandleStatus_HANDLE_STATUS_AVAILABLE, avail.Status)
	assert.Equal(t, handle, avail.NormalizedHandle)

	avail = &pb.HandleAvailabilityResponse{}
	status = postProto(t, "/social/handle/availability", tokenA,
		&pb.HandleAvailabilityRequest{Handle: "admin"}, avail)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, pb.HandleStatus_HANDLE_STATUS_RESERVED, avail.Status)

	// Join as A: profile created, everything private by default.
	join := &pb.JoinResponse{}
	status = postProto(t, "/social/join", tokenA, &pb.JoinRequest{
		Handle: handle, AcceptedTermsVersion: 1, DisplayName: "E2E Person A",
	}, join)
	require.Equal(t, http.StatusOK, status)
	require.NotNil(t, join.Profile)
	assert.Equal(t, handle, join.Profile.Handle)
	assert.Equal(t, pb.SocialVisibility_SOCIAL_VISIBILITY_PRIVATE, join.Profile.BioVisibility)

	// The same handle now reads taken, and B's claim of it loses.
	avail = &pb.HandleAvailabilityResponse{}
	status = postProto(t, "/social/handle/availability", tokenB,
		&pb.HandleAvailabilityRequest{Handle: handle}, avail)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, pb.HandleStatus_HANDLE_STATUS_TAKEN, avail.Status)

	status = postProto(t, "/social/join", tokenB, &pb.JoinRequest{
		Handle: handle, AcceptedTermsVersion: 1, DisplayName: "E2E Person B",
	}, nil)
	assert.Equal(t, http.StatusConflict, status)

	// Own-profile get, then update: bio set and made public.
	got := &pb.ProfileResponse{}
	status = postProto(t, "/social/profile/get", tokenA, &pb.ProfileGetRequest{}, got)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "E2E Person A", got.Profile.DisplayName)

	updated := &pb.ProfileResponse{}
	status = postProto(t, "/social/profile/update", tokenA, &pb.ProfileUpdateRequest{
		DisplayName:   "E2E Person A",
		Bio:           "hello from e2e",
		BioVisibility: pb.SocialVisibility_SOCIAL_VISIBILITY_PUBLIC,
	}, updated)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, pb.SocialVisibility_SOCIAL_VISIBILITY_PUBLIC, updated.Profile.BioVisibility)
	// Unspecified fields folded to private, handle untouched.
	assert.Equal(t, pb.SocialVisibility_SOCIAL_VISIBILITY_PRIVATE, updated.Profile.StatsVisibility)
	assert.Equal(t, handle, updated.Profile.Handle)

	// Public read as B: public bio visible, private stats absent.
	public := &pb.PublicProfileResponse{}
	status = postProto(t, "/social/profile/public", tokenB,
		&pb.PublicProfileRequest{Handle: handle}, public)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "hello from e2e", public.Bio)
	assert.False(t, public.HasStats)

	// A blocks B: B's read of A now looks like not-found (mutual invisibility).
	ack := &pb.SocialAck{}
	status = postProto(t, "/social/block", tokenA, &pb.BlockRequest{TargetUserId: uuidB}, ack)
	require.Equal(t, http.StatusOK, status)
	assert.True(t, ack.Success)

	status = postProto(t, "/social/profile/public", tokenB,
		&pb.PublicProfileRequest{Handle: handle}, nil)
	assert.Equal(t, http.StatusNotFound, status)

	// Unblock restores the read.
	ack = &pb.SocialAck{}
	status = postProto(t, "/social/unblock", tokenA, &pb.BlockRequest{TargetUserId: uuidB}, ack)
	require.Equal(t, http.StatusOK, status)
	assert.True(t, ack.Success)

	status = postProto(t, "/social/profile/public", tokenB,
		&pb.PublicProfileRequest{Handle: handle}, nil)
	assert.Equal(t, http.StatusOK, status)

	// B reports A: acknowledged into the triage queue.
	ack = &pb.SocialAck{}
	status = postProto(t, "/social/report", tokenB, &pb.ReportRequest{
		TargetUserId: join.Profile.UserId,
		Reason:       pb.ReportReason_REPORT_REASON_SPAM,
		Context:      "e2e report",
	}, ack)
	require.Equal(t, http.StatusOK, status)
	assert.True(t, ack.Success)

	// Erase A: profile gone, handle tombstoned forever.
	ack = &pb.SocialAck{}
	status = postProto(t, "/social/erase", tokenA, &pb.EraseRequest{}, ack)
	require.Equal(t, http.StatusOK, status)
	assert.True(t, ack.Success)

	status = postProto(t, "/social/profile/get", tokenA, &pb.ProfileGetRequest{}, nil)
	assert.Equal(t, http.StatusNotFound, status)

	status = postProto(t, "/social/profile/public", tokenB,
		&pb.PublicProfileRequest{Handle: handle}, nil)
	assert.Equal(t, http.StatusNotFound, status)

	avail = &pb.HandleAvailabilityResponse{}
	status = postProto(t, "/social/handle/availability", tokenB,
		&pb.HandleAvailabilityRequest{Handle: handle}, avail)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, pb.HandleStatus_HANDLE_STATUS_TOMBSTONED, avail.Status)

	// And joining with the tombstoned handle still loses.
	status = postProto(t, "/social/join", tokenB, &pb.JoinRequest{
		Handle: handle, AcceptedTermsVersion: 1, DisplayName: "E2E Person B",
	}, nil)
	assert.Equal(t, http.StatusConflict, status)
}
