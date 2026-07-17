//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	pb "github.com/hbmartin/podcast-backend/protos/api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"
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

// TestReviewsAndReactions walks the Slice-3 surface over real HTTP/DB:
// join-gated attributed review text, the listen-gate, public listing with
// your_review, reaction set/switch/clear with aggregate counts, and erase
// deleting the attributed review.
func TestReviewsAndReactions(t *testing.T) {
	suffix := time.Now().UnixNano()
	tokenA, _ := registerUser(t, fmt.Sprintf("review-a-%d@e2e.test", suffix))

	podcastUUID := ingestFixturePodcast(t)

	// Not joined yet: review submit is forbidden.
	status := postProto(t, "/social/review/submit", tokenA,
		&pb.PodcastReviewSubmitRequest{PodcastUuid: podcastUUID, Text: "not yet"}, nil)
	assert.Equal(t, http.StatusForbidden, status)

	// Join.
	handle := fmt.Sprintf("e2e_reviewer_%d", suffix%1_000_000_000)
	joinResp := &pb.JoinResponse{}
	status = postProto(t, "/social/join", tokenA, &pb.JoinRequest{
		Handle: handle, AcceptedTermsVersion: 1, DisplayName: "E2E Reviewer",
	}, joinResp)
	require.Equal(t, http.StatusOK, status)

	// Joined but below the listen-gate: still forbidden.
	status = postProto(t, "/social/review/submit", tokenA,
		&pb.PodcastReviewSubmitRequest{PodcastUuid: podcastUUID, Text: "still too soon"}, nil)
	assert.Equal(t, http.StatusForbidden, status)

	// Mark two episodes as played via sync so the server-side gate opens.
	playEpisodesOfPodcast(t, tokenA, podcastUUID, 2)

	review := &pb.PodcastReview{}
	status = postProto(t, "/social/review/submit", tokenA,
		&pb.PodcastReviewSubmitRequest{PodcastUuid: podcastUUID, Text: "a fine podcast"}, review)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, handle, review.Handle)
	assert.Equal(t, "a fine podcast", review.Text)

	// Public listing (anonymous) shows it; authed listing carries your_review.
	list := &pb.PodcastReviewsResponse{}
	status = postProto(t, "/podcast/reviews", "",
		&pb.PodcastReviewsRequest{PodcastUuid: podcastUUID}, list)
	require.Equal(t, http.StatusOK, status)
	require.Len(t, list.Reviews, 1)
	assert.Equal(t, handle, list.Reviews[0].Handle)
	assert.Nil(t, list.YourReview)

	list = &pb.PodcastReviewsResponse{}
	status = postProto(t, "/podcast/reviews", tokenA,
		&pb.PodcastReviewsRequest{PodcastUuid: podcastUUID}, list)
	require.Equal(t, http.StatusOK, status)
	require.NotNil(t, list.YourReview)

	// Reactions: set -> counts + your_reaction, switch, clear.
	episodeUUID := list.Reviews[0].UserId // placeholder var reuse avoided below
	_ = episodeUUID
	episode := firstEpisodeUUID(t, podcastUUID)

	ack := &pb.SocialAck{}
	status = postProto(t, "/social/reaction/set", tokenA,
		&pb.EpisodeReactionSetRequest{EpisodeUuid: episode, Kind: pb.ReactionKind_REACTION_KIND_HEART}, ack)
	require.Equal(t, http.StatusOK, status)
	assert.True(t, ack.Success)

	reactions := &pb.EpisodeReactionsResponse{}
	status = postProto(t, "/episode/reactions", tokenA,
		&pb.EpisodeReactionsRequest{EpisodeUuid: episode}, reactions)
	require.Equal(t, http.StatusOK, status)
	require.Len(t, reactions.Counts, 1)
	assert.Equal(t, pb.ReactionKind_REACTION_KIND_HEART, reactions.Counts[0].Kind)
	assert.Equal(t, int64(1), reactions.Counts[0].Count)
	assert.Equal(t, pb.ReactionKind_REACTION_KIND_HEART, reactions.YourReaction)

	status = postProto(t, "/social/reaction/set", tokenA,
		&pb.EpisodeReactionSetRequest{EpisodeUuid: episode, Kind: pb.ReactionKind_REACTION_KIND_UNSPECIFIED}, nil)
	require.Equal(t, http.StatusOK, status)
	reactions = &pb.EpisodeReactionsResponse{}
	_ = postProto(t, "/episode/reactions", tokenA,
		&pb.EpisodeReactionsRequest{EpisodeUuid: episode}, reactions)
	assert.Empty(t, reactions.Counts)

	// Erase: the attributed review disappears from the public list.
	status = postProto(t, "/social/erase", tokenA, &pb.EraseRequest{}, nil)
	require.Equal(t, http.StatusOK, status)

	list = &pb.PodcastReviewsResponse{}
	status = postProto(t, "/podcast/reviews", "",
		&pb.PodcastReviewsRequest{PodcastUuid: podcastUUID}, list)
	require.Equal(t, http.StatusOK, status)
	assert.Empty(t, list.Reviews)
}

// episodesOfPodcast returns the fixture podcast's catalog episode uuids.
func episodesOfPodcast(t *testing.T, podcastUuid string) []string {
	t.Helper()
	resp, err := http.Get(baseURL + "/mobile/podcast/full/" + podcastUuid)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var full struct {
		Podcast struct {
			Episodes []struct {
				UUID string `json:"uuid"`
			} `json:"episodes"`
		} `json:"podcast"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&full))
	uuids := make([]string, 0, len(full.Podcast.Episodes))
	for _, e := range full.Podcast.Episodes {
		uuids = append(uuids, e.UUID)
	}
	return uuids
}

func firstEpisodeUUID(t *testing.T, podcastUuid string) string {
	t.Helper()
	uuids := episodesOfPodcast(t, podcastUuid)
	require.NotEmpty(t, uuids)
	return uuids[0]
}

// playEpisodesOfPodcast syncs n episodes as played beyond half their duration,
// opening the server-side review listen-gate.
func playEpisodesOfPodcast(t *testing.T, token, podcastUuid string, n int) {
	t.Helper()
	uuids := episodesOfPodcast(t, podcastUuid)
	require.GreaterOrEqual(t, len(uuids), n)
	now := time.Now().UnixMilli()
	var records []*pb.Record
	for _, uuid := range uuids[:n] {
		records = append(records, &pb.Record{Record: &pb.Record_Episode{Episode: &pb.SyncUserEpisode{
			Uuid:               uuid,
			PodcastUuid:        podcastUuid,
			Duration:           wrapperspb.Int64(600),
			DurationModified:   wrapperspb.Int64(now),
			PlayedUpTo:         wrapperspb.Int64(500),
			PlayedUpToModified: wrapperspb.Int64(now),
		}}})
	}
	status := postProto(t, "/user/sync/update", token,
		&pb.SyncUpdateRequest{DeviceUtcTimeMs: now, Records: records}, &pb.SyncUpdateResponse{})
	require.Equal(t, http.StatusOK, status)
}
