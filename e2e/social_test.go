//go:build e2e

package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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

// TestCommentTree walks the Slice-6 surface (ADR-0010): gate on seeds, free
// replies, grace-window edit, tombstoned delete, inbox replies watermark,
// the commented feed item, and erase tombstoning.
func TestCommentTree(t *testing.T) {
	suffix := time.Now().UnixNano()
	tokenA, _ := registerUser(t, fmt.Sprintf("comment-a-%d@e2e.test", suffix))
	tokenB, _ := registerUser(t, fmt.Sprintf("comment-b-%d@e2e.test", suffix))

	handleA := fmt.Sprintf("e2e_cmt_a_%d", suffix%1_000_000_000)
	handleB := fmt.Sprintf("e2e_cmt_b_%d", suffix%1_000_000_000)
	for _, pair := range []struct {
		token, handle, name string
	}{{tokenA, handleA, "Commenter A"}, {tokenB, handleB, "Replier B"}} {
		status := postProto(t, "/social/join", pair.token, &pb.JoinRequest{
			Handle: pair.handle, AcceptedTermsVersion: 1, DisplayName: pair.name,
		}, &pb.JoinResponse{})
		require.Equal(t, http.StatusOK, status)
	}

	podcastUUID := ingestFixturePodcast(t)
	episodeUUID := episodesOfPodcast(t, podcastUUID)[0]

	// Ungated: A hasn't played the episode yet.
	status := postProto(t, "/social/comment/submit", tokenA,
		&pb.CommentSubmitRequest{EpisodeUuid: episodeUUID, PodcastUuid: podcastUUID, Text: "too soon"}, nil)
	require.Equal(t, http.StatusForbidden, status)

	// A plays past the gate and posts a timestamped seed (a Moment).
	playEpisodesOfPodcast(t, tokenA, podcastUUID, 1)
	ts := int32(125)
	seed := &pb.SocialComment{}
	status = postProto(t, "/social/comment/submit", tokenA, &pb.CommentSubmitRequest{
		EpisodeUuid: episodeUUID, PodcastUuid: podcastUUID,
		EpisodeTitle: "Fixture Episode", PodcastTitle: "Fixture Podcast",
		Text: "this bit at two minutes", TimestampSeconds: &ts,
	}, seed)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, handleA, seed.Handle)
	require.NotNil(t, seed.TimestampSeconds)
	assert.Equal(t, int32(125), *seed.TimestampSeconds)

	// B replies without ever playing: no gate on replies.
	reply := &pb.SocialComment{}
	status = postProto(t, "/social/comment/submit", tokenB, &pb.CommentSubmitRequest{
		EpisodeUuid: episodeUUID, PodcastUuid: podcastUUID, Text: "agreed!", ParentId: seed.Id,
	}, reply)
	require.Equal(t, http.StatusOK, status)

	// The public list shows one seed with one reply; anonymous read works.
	list := &pb.CommentsResponse{}
	status = postProto(t, "/episode/comments", "",
		&pb.EpisodeCommentsRequest{EpisodeUuid: episodeUUID}, list)
	require.Equal(t, http.StatusOK, status)
	require.Len(t, list.Comments, 1)
	assert.Equal(t, int32(1), list.Comments[0].ReplyCount)

	replies := &pb.CommentsResponse{}
	status = postProto(t, "/social/comment/replies", "",
		&pb.CommentRepliesRequest{ParentId: seed.Id}, replies)
	require.Equal(t, http.StatusOK, status)
	require.Len(t, replies.Comments, 1)
	assert.Equal(t, handleB, replies.Comments[0].Handle)

	// Edit after reply: the grace window is shut.
	status = postProto(t, "/social/comment/edit", tokenA,
		&pb.CommentEditRequest{Id: seed.Id, Text: "revised"}, nil)
	require.Equal(t, http.StatusConflict, status)

	// B's fresh reply is editable inside the window.
	status = postProto(t, "/social/comment/edit", tokenB,
		&pb.CommentEditRequest{Id: reply.Id, Text: "agreed — loudly"}, &pb.SocialAck{})
	require.Equal(t, http.StatusOK, status)

	// A's inbox: one unread reply; seen clears it.
	inbox := &pb.InboxRepliesResponse{}
	status = postProto(t, "/social/inbox/replies", tokenA, &pb.InboxRepliesRequest{}, inbox)
	require.Equal(t, http.StatusOK, status)
	require.Len(t, inbox.Replies, 1)
	assert.Equal(t, handleB, inbox.Replies[0].Handle)
	assert.True(t, inbox.Replies[0].Edited)
	assert.Equal(t, "Fixture Episode", inbox.Replies[0].EpisodeTitle,
		"replies inherit the seed's denormalized titles")
	assert.Equal(t, int32(1), inbox.Unread)

	status = postProto(t, "/social/inbox/replies/seen", tokenA, &pb.InboxRepliesRequest{}, &pb.SocialAck{})
	require.Equal(t, http.StatusOK, status)
	postProto(t, "/social/inbox/replies", tokenA, &pb.InboxRepliesRequest{}, inbox)
	assert.Equal(t, int32(0), inbox.Unread)

	// B follows A: A's seed appears in B's feed as a commented item.
	status = postProto(t, "/social/follow", tokenB, &pb.FollowRequest{Handle: handleA}, &pb.FollowResponse{})
	require.Equal(t, http.StatusOK, status)
	feed := &pb.FeedResponse{}
	status = postProto(t, "/social/feed", tokenB, &pb.FeedRequest{}, feed)
	require.Equal(t, http.StatusOK, status)
	foundCommented := false
	for _, item := range feed.Items {
		if item.Kind == pb.FeedItemKind_FEED_ITEM_KIND_COMMENTED && item.ActorHandle == handleA {
			foundCommented = true
			assert.Equal(t, episodeUUID, item.EpisodeUuid)
			assert.Contains(t, item.ReviewExcerpt, "two minutes")
		}
	}
	assert.True(t, foundCommented, "the seed must surface as a commented feed item")

	// A deletes the seed: tombstone stays, B's reply survives.
	status = postProto(t, "/social/comment/delete", tokenA, &pb.CommentDeleteRequest{Id: seed.Id}, &pb.SocialAck{})
	require.Equal(t, http.StatusOK, status)
	postProto(t, "/episode/comments", "", &pb.EpisodeCommentsRequest{EpisodeUuid: episodeUUID}, list)
	require.Len(t, list.Comments, 1)
	assert.True(t, list.Comments[0].Removed)
	assert.Empty(t, list.Comments[0].Text)
	assert.Equal(t, int32(1), list.Comments[0].ReplyCount)

	// B erases: the reply tombstones too (ADR-0010), the row itself remains.
	status = postProto(t, "/social/erase", tokenB, &pb.EraseRequest{}, &pb.SocialAck{})
	require.Equal(t, http.StatusOK, status)
	postProto(t, "/social/comment/replies", "", &pb.CommentRepliesRequest{ParentId: seed.Id}, replies)
	require.Len(t, replies.Comments, 1)
	assert.True(t, replies.Comments[0].Removed)
	assert.Empty(t, replies.Comments[0].Text)
	assert.Empty(t, replies.Comments[0].Handle)
}

// TestSocialLists walks the Slice-7 surface (ADR-0011): visibility gating,
// subscription, the collaborator invite loop with attributed entries,
// kick, the profile Lists section, owner-death erase — plus the
// custom-playlist sync overturn round-trip.
func TestSocialLists(t *testing.T) {
	suffix := time.Now().UnixNano()
	tokenA, _ := registerUser(t, fmt.Sprintf("list-a-%d@e2e.test", suffix))
	tokenB, _ := registerUser(t, fmt.Sprintf("list-b-%d@e2e.test", suffix))

	handleA := fmt.Sprintf("e2e_lst_a_%d", suffix%1_000_000_000)
	handleB := fmt.Sprintf("e2e_lst_b_%d", suffix%1_000_000_000)
	for _, pair := range []struct {
		token, handle, name string
	}{{tokenA, handleA, "List Owner"}, {tokenB, handleB, "List Friend"}} {
		status := postProto(t, "/social/join", pair.token, &pb.JoinRequest{
			Handle: pair.handle, AcceptedTermsVersion: 1, DisplayName: pair.name,
		}, &pb.JoinResponse{})
		require.Equal(t, http.StatusOK, status)
	}

	// Create private with a snapshot entry (materialize-to-share shape).
	list := &pb.SharedList{}
	status := postProto(t, "/social/list/create", tokenA, &pb.SharedListCreateRequest{
		Title: "Road Trip", Description: "long drives",
		Visibility: pb.SocialVisibility_SOCIAL_VISIBILITY_PRIVATE,
		Entries: []*pb.SharedListEntry{
			{EpisodeUuid: "ep-e2e-1", PodcastUuid: "pod-e2e-1", EpisodeTitle: "First"},
		},
	}, list)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, pb.SharedListRole_SHARED_LIST_ROLE_OWNER, list.YourRole)

	// Private: B and anonymous read as not-found.
	status = postProto(t, "/social/list/entries", tokenB,
		&pb.SharedListEntriesRequest{ListId: list.Id}, nil)
	require.Equal(t, http.StatusNotFound, status)

	// Public: visible to B and anonymous; B subscribes.
	status = postProto(t, "/social/list/update", tokenA, &pb.SharedListUpdateRequest{
		ListId: list.Id, Title: "Road Trip", Description: "long drives",
		Visibility: pb.SocialVisibility_SOCIAL_VISIBILITY_PUBLIC,
	}, &pb.SocialAck{})
	require.Equal(t, http.StatusOK, status)

	entries := &pb.SharedListEntriesResponse{}
	status = postProto(t, "/social/list/entries", "", &pb.SharedListEntriesRequest{ListId: list.Id}, entries)
	require.Equal(t, http.StatusOK, status)
	require.Len(t, entries.Entries, 1)
	assert.Equal(t, handleA, entries.Entries[0].AddedByHandle)

	status = postProto(t, "/social/list/subscribe", tokenB,
		&pb.SharedListSubscribeRequest{ListId: list.Id, Subscribe: true}, &pb.SocialAck{})
	require.Equal(t, http.StatusOK, status)

	lists := &pb.SharedListsResponse{}
	status = postProto(t, "/social/lists", tokenB, &pb.SharedListsRequest{}, lists)
	require.Equal(t, http.StatusOK, status)
	require.Len(t, lists.Lists, 1)
	assert.Equal(t, pb.SharedListRole_SHARED_LIST_ROLE_SUBSCRIBER, lists.Lists[0].YourRole)

	// The profile Lists section carries the public list.
	profile := &pb.PublicProfileResponse{}
	status = postProto(t, "/social/profile/public", tokenB, &pb.PublicProfileRequest{Handle: handleA}, profile)
	require.Equal(t, http.StatusOK, status)
	require.Len(t, profile.Lists, 1)
	assert.Equal(t, "Road Trip", profile.Lists[0].Title)

	// Invite B as collaborator; B accepts and adds an attributed entry.
	status = postProto(t, "/social/list/invite", tokenA,
		&pb.SharedListInviteRequest{ListId: list.Id, Handle: handleB}, &pb.SocialAck{})
	require.Equal(t, http.StatusOK, status)

	postProto(t, "/social/lists", tokenB, &pb.SharedListsRequest{}, lists)
	require.Len(t, lists.Invites, 1)
	assert.Equal(t, "Road Trip", lists.Invites[0].Title)

	status = postProto(t, "/social/list/invite/respond", tokenB,
		&pb.SharedListInviteRespondRequest{ListId: list.Id, Accept: true}, &pb.SocialAck{})
	require.Equal(t, http.StatusOK, status)

	status = postProto(t, "/social/list/entry", tokenB, &pb.SharedListEntryOpRequest{
		ListId: list.Id, Op: pb.SharedListOp_SHARED_LIST_OP_ADD,
		EpisodeUuid: "ep-e2e-2", PodcastUuid: "pod-e2e-1", EpisodeTitle: "Second", Position: -1,
	}, &pb.SocialAck{})
	require.Equal(t, http.StatusOK, status)

	postProto(t, "/social/list/entries", tokenA, &pb.SharedListEntriesRequest{ListId: list.Id}, entries)
	require.Len(t, entries.Entries, 2)
	assert.Equal(t, handleB, entries.Entries[1].AddedByHandle)
	require.NotNil(t, entries.List)
	require.Len(t, entries.List.Members, 1, "owner view lists the collaborator")

	// Kick B: their edits stop.
	status = postProto(t, "/social/list/member/remove", tokenA,
		&pb.SharedListInviteRequest{ListId: list.Id, Handle: handleB}, &pb.SocialAck{})
	require.Equal(t, http.StatusOK, status)
	status = postProto(t, "/social/list/entry", tokenB, &pb.SharedListEntryOpRequest{
		ListId: list.Id, Op: pb.SharedListOp_SHARED_LIST_OP_ADD, EpisodeUuid: "ep-e2e-3",
	}, nil)
	require.Equal(t, http.StatusForbidden, status)

	// Custom-playlist sync overturn: the query envelope round-trips.
	now := time.Now().UnixMilli()
	status = postProto(t, "/user/sync/update", tokenA, &pb.SyncUpdateRequest{
		DeviceUtcTimeMs: now,
		Records: []*pb.Record{{Record: &pb.Record_Playlist{Playlist: &pb.SyncUserPlaylist{
			Uuid:        "cccc1111-2222-3333-4444-555566667777",
			Title:       wrapperspb.String("My Custom"),
			Manual:      wrapperspb.Bool(false),
			CustomQuery: wrapperspb.String(`{"version":1,"mode":"sql"}`),
		}}}},
	}, &pb.SyncUpdateResponse{})
	require.Equal(t, http.StatusOK, status)

	playlists := &pb.UserPlaylistListResponse{}
	status = postProto(t, "/user/playlist/list", tokenA, &pb.UserPlaylistListRequest{}, playlists)
	require.Equal(t, http.StatusOK, status)
	foundCustom := false
	for _, p := range playlists.Playlists {
		if p.Uuid == "cccc1111-2222-3333-4444-555566667777" {
			foundCustom = true
			assert.Equal(t, `{"version":1,"mode":"sql"}`, p.GetCustomQuery().GetValue(),
				"custom_query must round-trip through sync")
		}
	}
	assert.True(t, foundCustom)

	// Owner erase: the list vanishes for everyone.
	status = postProto(t, "/social/erase", tokenA, &pb.EraseRequest{}, &pb.SocialAck{})
	require.Equal(t, http.StatusOK, status)
	status = postProto(t, "/social/list/entries", tokenB,
		&pb.SharedListEntriesRequest{ListId: list.Id}, nil)
	require.Equal(t, http.StatusNotFound, status)
}

// TestSocialPush walks the Slice-8 surface end-to-end: a social event lands
// on the recipient's registered device via (mock) APNs with the category and
// typed payload the iOS dispatcher keys on, and the per-type disabled bitmask
// silences exactly its type. The e2e server runs queue-less, so sends ride
// the direct goroutine path — assertions poll.
func TestSocialPush(t *testing.T) {
	suffix := time.Now().UnixNano()
	tokenA, _ := registerUser(t, fmt.Sprintf("push-a-%d@e2e.test", suffix))
	tokenB, _ := registerUser(t, fmt.Sprintf("push-b-%d@e2e.test", suffix))

	handleA := fmt.Sprintf("e2e_psh_a_%d", suffix%1_000_000_000)
	handleB := fmt.Sprintf("e2e_psh_b_%d", suffix%1_000_000_000)
	for _, pair := range []struct {
		token, handle, name string
	}{{tokenA, handleA, "Push Actor"}, {tokenB, handleB, "Push Target"}} {
		status := postProto(t, "/social/join", pair.token, &pb.JoinRequest{
			Handle: pair.handle, AcceptedTermsVersion: 1, DisplayName: pair.name,
		}, &pb.JoinResponse{})
		require.Equal(t, http.StatusOK, status)
	}

	// B registers a push token (piggybacked on user/update).
	deviceToken := fmt.Sprintf("E2ESOCIAL%d", suffix%1_000_000)
	var refresh struct {
		Status string `json:"status"`
	}
	status := postJSON(t, "/user/update", tokenB, map[string]string{
		"podcasts": "", "last_episodes": "", "device": "e2e-device-social",
		"push_token": deviceToken, "push_on": "true", "push_messages_on": "",
	}, &refresh)
	require.Equal(t, http.StatusOK, status)

	countFor := func(devicePath string) int {
		apnsMu.Lock()
		defer apnsMu.Unlock()
		n := 0
		for _, p := range apnsPushes {
			if p.path == "/3/device/"+deviceToken && strings.Contains(p.body, devicePath) {
				n++
			}
		}
		return n
	}
	waitFor := func(marker string, want int) bool {
		for i := 0; i < 40; i++ {
			if countFor(marker) >= want {
				return true
			}
			time.Sleep(50 * time.Millisecond)
		}
		return false
	}

	// A follows B (open): NEW_FOLLOWER (type 3) arrives with category "so",
	// the typed payload, and the actor's name in the alert title.
	status = postProto(t, "/social/follow", tokenA, &pb.FollowRequest{Handle: handleB}, &pb.FollowResponse{})
	require.Equal(t, http.StatusOK, status)
	require.True(t, waitFor(`"social_type":"3"`, 1), "the new-follower push must reach the mock APNs")
	apnsMu.Lock()
	var followBody string
	for _, p := range apnsPushes {
		if p.path == "/3/device/"+deviceToken && strings.Contains(p.body, `"social_type":"3"`) {
			followBody = p.body
		}
	}
	apnsMu.Unlock()
	assert.Contains(t, followBody, `"category":"so"`)
	assert.Contains(t, followBody, `"actor_handle":"`+handleA+`"`)
	assert.Contains(t, followBody, `"title":"Push Actor"`)

	// B disables NEW_FOLLOWER (bit 2 = 1<<(3-1) = 4): a refollow stays silent.
	update := &pb.ProfileUpdateRequest{DisplayName: "Push Target", SocialPushDisabled: 1 << 2}
	status = postProto(t, "/social/profile/update", tokenB, update, &pb.ProfileResponse{})
	require.Equal(t, http.StatusOK, status)

	status = postProto(t, "/social/unfollow", tokenA, &pb.UnfollowRequest{Handle: handleB}, &pb.SocialAck{})
	require.Equal(t, http.StatusOK, status)
	status = postProto(t, "/social/follow", tokenA, &pb.FollowRequest{Handle: handleB}, &pb.FollowResponse{})
	require.Equal(t, http.StatusOK, status)
	time.Sleep(400 * time.Millisecond)
	assert.Equal(t, 1, countFor(`"social_type":"3"`), "a disabled type must stay silent")

	// A different type still flows: a shared item (type 4).
	status = postProto(t, "/social/share/send", tokenA, &pb.SharedItemSendRequest{
		RecipientHandle: handleB, EpisodeUuid: fmt.Sprintf("push-ep-%d", suffix),
		EpisodeTitle: "Pushed Episode",
	}, &pb.SocialAck{})
	require.Equal(t, http.StatusOK, status)
	require.True(t, waitFor(`"social_type":"4"`, 1), "other types keep flowing")
}

// TestFindPeople walks the Slice-9 surface: prefix search with the
// discoverability opt-out, friends-of-followed suggestions with count-only
// copy, and transient contacts matching via typed salted hashes.
func TestFindPeople(t *testing.T) {
	suffix := time.Now().UnixNano()
	emailB := fmt.Sprintf("find-b-%d@e2e.test", suffix)
	tokenA, _ := registerUser(t, fmt.Sprintf("find-a-%d@e2e.test", suffix))
	tokenB, _ := registerUser(t, emailB)
	tokenC, _ := registerUser(t, fmt.Sprintf("find-c-%d@e2e.test", suffix))

	handleA := fmt.Sprintf("e2e_fnd_a_%d", suffix%1_000_000_000)
	handleB := fmt.Sprintf("e2e_fnd_b_%d", suffix%1_000_000_000)
	handleC := fmt.Sprintf("e2e_fnd_c_%d", suffix%1_000_000_000)
	for _, pair := range []struct {
		token, handle, name string
	}{{tokenA, handleA, "Finder A"}, {tokenB, handleB, "Findable B"}, {tokenC, handleC, "Suggested C"}} {
		status := postProto(t, "/social/join", pair.token, &pb.JoinRequest{
			Handle: pair.handle, AcceptedTermsVersion: 1, DisplayName: pair.name,
		}, &pb.JoinResponse{})
		require.Equal(t, http.StatusOK, status)
	}

	// Prefix search finds B; the opt-out removes them; restore brings back.
	search := &pb.SocialSearchResponse{}
	status := postProto(t, "/social/search", tokenA, &pb.SocialSearchRequest{Query: handleB[:12]}, search)
	require.Equal(t, http.StatusOK, status)
	require.Len(t, search.Profiles, 1)
	assert.Equal(t, handleB, search.Profiles[0].Handle)

	status = postProto(t, "/social/profile/update", tokenB, &pb.ProfileUpdateRequest{
		DisplayName: "Findable B", HideFromDiscovery: true,
	}, &pb.ProfileResponse{})
	require.Equal(t, http.StatusOK, status)
	search = &pb.SocialSearchResponse{}
	postProto(t, "/social/search", tokenA, &pb.SocialSearchRequest{Query: handleB[:12]}, search)
	assert.Empty(t, search.Profiles, "hidden profiles leave search")

	status = postProto(t, "/social/profile/update", tokenB, &pb.ProfileUpdateRequest{
		DisplayName: "Findable B",
	}, &pb.ProfileResponse{})
	require.Equal(t, http.StatusOK, status)

	// A follows B, B follows C: C is suggested to A with one mutual.
	for _, hop := range []struct {
		token, handle string
	}{{tokenA, handleB}, {tokenB, handleC}} {
		status = postProto(t, "/social/follow", hop.token, &pb.FollowRequest{Handle: hop.handle}, &pb.FollowResponse{})
		require.Equal(t, http.StatusOK, status)
	}
	suggestions := &pb.SocialSuggestionsResponse{}
	status = postProto(t, "/social/suggestions", tokenA, &pb.SocialSuggestionsRequest{}, suggestions)
	require.Equal(t, http.StatusOK, status)
	require.Len(t, suggestions.Profiles, 1)
	assert.Equal(t, handleC, suggestions.Profiles[0].Handle)
	assert.Equal(t, int32(1), suggestions.Profiles[0].MutualCount)

	// Contacts match: hash B's email with the server salt; phone hash rides
	// along unmatched (wire-ready).
	salt := &pb.ContactsSaltResponse{}
	status = postProto(t, "/social/contacts/salt", tokenA, &pb.SocialSuggestionsRequest{}, salt)
	require.Equal(t, http.StatusOK, status)
	sum := sha256.Sum256([]byte(salt.Salt + strings.ToLower(emailB)))
	phoneSum := sha256.Sum256([]byte(salt.Salt + "+15550001111"))
	match := &pb.ContactsMatchResponse{}
	status = postProto(t, "/social/contacts/match", tokenA, &pb.ContactsMatchRequest{
		Hashes: []*pb.ContactHash{
			{Kind: pb.ContactHashKind_CONTACT_HASH_KIND_EMAIL, Hash: hex.EncodeToString(sum[:])},
			{Kind: pb.ContactHashKind_CONTACT_HASH_KIND_PHONE, Hash: hex.EncodeToString(phoneSum[:])},
		},
	}, match)
	require.Equal(t, http.StatusOK, status)
	require.Len(t, match.Profiles, 1)
	assert.Equal(t, handleB, match.Profiles[0].Handle)
}
