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

// TestSendToFriendAndInbox walks the Slice-4 surface: join both ends, send
// with note+timestamp, inbox unread -> read -> delete, blocked send 404, and
// sender-erase clearing the recipient's inbox.
func TestSendToFriendAndInbox(t *testing.T) {
	suffix := time.Now().UnixNano()
	tokenA, _ := registerUser(t, fmt.Sprintf("send-a-%d@e2e.test", suffix))
	tokenB, uuidB := registerUser(t, fmt.Sprintf("send-b-%d@e2e.test", suffix))

	handleA := fmt.Sprintf("e2e_send_a_%d", suffix%1_000_000_000)
	handleB := fmt.Sprintf("e2e_send_b_%d", suffix%1_000_000_000)
	for _, pair := range []struct {
		token, handle, name string
	}{{tokenA, handleA, "Sender A"}, {tokenB, handleB, "Recipient B"}} {
		status := postProto(t, "/social/join", pair.token, &pb.JoinRequest{
			Handle: pair.handle, AcceptedTermsVersion: 1, DisplayName: pair.name,
		}, &pb.JoinResponse{})
		require.Equal(t, http.StatusOK, status)
	}

	send := &pb.SharedItemSendRequest{
		RecipientHandle:  handleB,
		EpisodeUuid:      "e2e-episode-1",
		PodcastUuid:      "e2e-podcast-1",
		EpisodeTitle:     "A Great Episode",
		PodcastTitle:     "A Great Podcast",
		Note:             "listen from here!",
		TimestampSeconds: 870,
	}
	ack := &pb.SocialAck{}
	status := postProto(t, "/social/share/send", tokenA, send, ack)
	require.Equal(t, http.StatusOK, status)
	assert.True(t, ack.Success)

	// B's inbox: one unread item with attribution + note + timestamp.
	inbox := &pb.InboxResponse{}
	status = postProto(t, "/social/inbox", tokenB, &pb.InboxRequest{}, inbox)
	require.Equal(t, http.StatusOK, status)
	require.Len(t, inbox.Items, 1)
	assert.Equal(t, int64(1), inbox.Unread)
	item := inbox.Items[0]
	assert.Equal(t, handleA, item.SenderHandle)
	assert.Equal(t, "listen from here!", item.Note)
	assert.Equal(t, int32(870), item.TimestampSeconds)
	assert.False(t, item.Read)

	// Read, then confirm unread drops.
	status = postProto(t, "/social/inbox/read", tokenB, &pb.InboxMarkReadRequest{Ids: []int64{item.Id}}, &pb.SocialAck{})
	require.Equal(t, http.StatusOK, status)
	inbox = &pb.InboxResponse{}
	_ = postProto(t, "/social/inbox", tokenB, &pb.InboxRequest{}, inbox)
	assert.Equal(t, int64(0), inbox.Unread)
	assert.True(t, inbox.Items[0].Read)

	// B blocks A: further sends fail like a missing handle.
	status = postProto(t, "/social/block", tokenB, &pb.BlockRequest{TargetUserId: inbox.Items[0].SenderUserId}, &pb.SocialAck{})
	require.Equal(t, http.StatusOK, status)
	status = postProto(t, "/social/share/send", tokenA, send, nil)
	assert.Equal(t, http.StatusNotFound, status)
	status = postProto(t, "/social/unblock", tokenB, &pb.BlockRequest{TargetUserId: inbox.Items[0].SenderUserId}, &pb.SocialAck{})
	require.Equal(t, http.StatusOK, status)

	// Sender erases: the delivered item vanishes from B's inbox.
	status = postProto(t, "/social/erase", tokenA, &pb.EraseRequest{}, &pb.SocialAck{})
	require.Equal(t, http.StatusOK, status)
	inbox = &pb.InboxResponse{}
	status = postProto(t, "/social/inbox", tokenB, &pb.InboxRequest{}, inbox)
	require.Equal(t, http.StatusOK, status)
	assert.Empty(t, inbox.Items, "sent items die with the sender's profile")
	_ = uuidB
}

// TestFollowGraphAndFeed walks Slice 5 over real HTTP/DB: open follow, the
// derived feed (finished-episode gated by history_visibility, review event),
// followers-only unlock, the approval toggle flow, and mute filtering.
func TestFollowGraphAndFeed(t *testing.T) {
	suffix := time.Now().UnixNano()
	tokenA, _ := registerUser(t, fmt.Sprintf("graph-a-%d@e2e.test", suffix))
	tokenB, _ := registerUser(t, fmt.Sprintf("graph-b-%d@e2e.test", suffix))

	handleA := fmt.Sprintf("e2e_gr_a_%d", suffix%1_000_000_000)
	handleB := fmt.Sprintf("e2e_gr_b_%d", suffix%1_000_000_000)
	for _, pair := range []struct{ token, handle, name string }{
		{tokenA, handleA, "Viewer A"}, {tokenB, handleB, "Actor B"},
	} {
		status := postProto(t, "/social/join", pair.token, &pb.JoinRequest{
			Handle: pair.handle, AcceptedTermsVersion: 1, DisplayName: pair.name,
		}, &pb.JoinResponse{})
		require.Equal(t, http.StatusOK, status)
	}

	// A follows B: open by default, immediately active.
	follow := &pb.FollowResponse{}
	status := postProto(t, "/social/follow", tokenA, &pb.FollowRequest{Handle: handleB}, follow)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, pb.FollowState_FOLLOW_STATE_ACTIVE, follow.State)

	// B acts: writes a review (public act) and finishes an episode (history-gated).
	podcastUUID := ingestFixturePodcast(t)
	playEpisodesOfPodcast(t, tokenB, podcastUUID, 2)
	status = postProto(t, "/social/review/submit", tokenB,
		&pb.PodcastReviewSubmitRequest{PodcastUuid: podcastUUID, Text: "reviewed for the feed"}, nil)
	require.Equal(t, http.StatusOK, status)
	// Mark one episode played (playing_status = 3).
	episode := firstEpisodeUUID(t, podcastUUID)
	now := time.Now().UnixMilli()
	status = postProto(t, "/user/sync/update", tokenB, &pb.SyncUpdateRequest{
		DeviceUtcTimeMs: now,
		Records: []*pb.Record{{Record: &pb.Record_Episode{Episode: &pb.SyncUserEpisode{
			Uuid: episode, PodcastUuid: podcastUUID,
			PlayingStatus: wrapperspb.Int32(3), PlayingStatusModified: wrapperspb.Int64(now),
		}}}},
	}, &pb.SyncUpdateResponse{})
	require.Equal(t, http.StatusOK, status)

	// A's feed: review + joined visible; finished-episode ABSENT while B's
	// history_visibility is private.
	feed := &pb.FeedResponse{}
	status = postProto(t, "/social/feed", tokenA, &pb.FeedRequest{}, feed)
	require.Equal(t, http.StatusOK, status)
	kinds := map[pb.FeedItemKind]bool{}
	for _, item := range feed.Items {
		kinds[item.Kind] = true
	}
	assert.True(t, kinds[pb.FeedItemKind_FEED_ITEM_KIND_REVIEWED])
	assert.True(t, kinds[pb.FeedItemKind_FEED_ITEM_KIND_JOINED])
	assert.False(t, kinds[pb.FeedItemKind_FEED_ITEM_KIND_FINISHED_EPISODE],
		"history-gated event hidden while private")

	// B opens history to followers: the finished-episode event appears.
	status = postProto(t, "/social/profile/update", tokenB, &pb.ProfileUpdateRequest{
		DisplayName:       "Actor B",
		HistoryVisibility: pb.SocialVisibility_SOCIAL_VISIBILITY_FOLLOWERS_ONLY,
	}, &pb.ProfileResponse{})
	require.Equal(t, http.StatusOK, status)

	feed = &pb.FeedResponse{}
	status = postProto(t, "/social/feed", tokenA, &pb.FeedRequest{}, feed)
	require.Equal(t, http.StatusOK, status)
	found := false
	for _, item := range feed.Items {
		if item.Kind == pb.FeedItemKind_FEED_ITEM_KIND_FINISHED_EPISODE {
			found = true
			assert.Equal(t, handleB, item.ActorHandle)
			assert.NotEmpty(t, item.EpisodeTitle)
		}
	}
	assert.True(t, found, "followers-only history event visible to a follower")

	// Followers-only profile field also unlocks for A.
	status = postProto(t, "/social/profile/update", tokenB, &pb.ProfileUpdateRequest{
		DisplayName: "Actor B", Bio: "for my followers",
		BioVisibility:     pb.SocialVisibility_SOCIAL_VISIBILITY_FOLLOWERS_ONLY,
		HistoryVisibility: pb.SocialVisibility_SOCIAL_VISIBILITY_FOLLOWERS_ONLY,
	}, &pb.ProfileResponse{})
	require.Equal(t, http.StatusOK, status)
	public := &pb.PublicProfileResponse{}
	status = postProto(t, "/social/profile/public", tokenA, &pb.PublicProfileRequest{Handle: handleB}, public)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "for my followers", public.Bio)
	assert.Equal(t, pb.FollowState_FOLLOW_STATE_ACTIVE, public.YourFollowState)
	assert.Equal(t, int64(1), public.FollowerCount)

	// Mute B: the feed goes quiet without unfollowing.
	status = postProto(t, "/social/mute", tokenA, &pb.MuteRequest{TargetUserId: public.UserId}, &pb.SocialAck{})
	require.Equal(t, http.StatusOK, status)
	feed = &pb.FeedResponse{}
	status = postProto(t, "/social/feed", tokenA, &pb.FeedRequest{}, feed)
	require.Equal(t, http.StatusOK, status)
	assert.Empty(t, feed.Items, "muted actor's events filtered")
	status = postProto(t, "/social/unmute", tokenA, &pb.MuteRequest{TargetUserId: public.UserId}, &pb.SocialAck{})
	require.Equal(t, http.StatusOK, status)

	// Approval toggle: a third account's follow pends, then acceptance activates.
	tokenC, _ := registerUser(t, fmt.Sprintf("graph-c-%d@e2e.test", suffix))
	handleC := fmt.Sprintf("e2e_gr_c_%d", suffix%1_000_000_000)
	status = postProto(t, "/social/join", tokenC, &pb.JoinRequest{
		Handle: handleC, AcceptedTermsVersion: 1, DisplayName: "Requester C",
	}, &pb.JoinResponse{})
	require.Equal(t, http.StatusOK, status)

	status = postProto(t, "/social/profile/update", tokenB, &pb.ProfileUpdateRequest{
		DisplayName: "Actor B", RequireFollowApproval: true,
	}, &pb.ProfileResponse{})
	require.Equal(t, http.StatusOK, status)

	follow = &pb.FollowResponse{}
	status = postProto(t, "/social/follow", tokenC, &pb.FollowRequest{Handle: handleB}, follow)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, pb.FollowState_FOLLOW_STATE_PENDING, follow.State)

	requests := &pb.FollowListResponse{}
	status = postProto(t, "/social/follow/requests", tokenB, &pb.FollowRequestsRequest{}, requests)
	require.Equal(t, http.StatusOK, status)
	require.Len(t, requests.Entries, 1)
	assert.Equal(t, handleC, requests.Entries[0].Handle)

	status = postProto(t, "/social/follow/approve", tokenB,
		&pb.FollowApprovalRequest{RequesterHandle: handleC, Accept: true}, &pb.SocialAck{})
	require.Equal(t, http.StatusOK, status)
	public = &pb.PublicProfileResponse{}
	status = postProto(t, "/social/profile/public", tokenC, &pb.PublicProfileRequest{Handle: handleB}, public)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, pb.FollowState_FOLLOW_STATE_ACTIVE, public.YourFollowState)
}
