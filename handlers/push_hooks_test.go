package handlers

import (
	"net/http"
	"testing"

	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/push"
	pb "github.com/hbmartin/podcast-backend/protos/api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Verifies the Slice-8 push seam fires with the right target/type/actor from
// the social handlers. Delivery itself is the push package's (and the e2e
// mock-APNs suite's) concern.

type recordedPush struct {
	target int64
	kind   int
	actor  string
	data   map[string]string
}

func pushRecordingRouter(m *socialMock) (*http.ServeMux, *[]recordedPush) {
	var recorded []recordedPush
	var seam SocialPushFunc = func(targetUserID int64, pushType int, actorHandle, actorDisplayName string, data map[string]string) {
		recorded = append(recorded, recordedPush{target: targetUserID, kind: pushType, actor: actorHandle, data: data})
	}
	h := Handlers{Queries: m, Config: testAuthConfig, SocialPush: &seam}
	router := http.NewServeMux()
	router.Handle("POST /social/join", mockAuthMiddleware(http.HandlerFunc(h.PostSocialJoin)))
	router.Handle("POST /social/follow", mockAuthMiddleware(http.HandlerFunc(h.PostFollow)))
	router.Handle("POST /social/follow/approve", mockAuthMiddleware(http.HandlerFunc(h.PostFollowApprove)))
	router.Handle("POST /social/comment/submit", mockAuthMiddleware(http.HandlerFunc(h.PostCommentSubmit)))
	router.Handle("POST /social/list/create", mockAuthMiddleware(http.HandlerFunc(h.PostSocialListCreate)))
	router.Handle("POST /social/list/invite", mockAuthMiddleware(http.HandlerFunc(h.PostSocialListInvite)))
	return router, &recorded
}

func TestSocialPushHooks(t *testing.T) {
	m := newSocialMock()
	m.ensureGraphState()
	m.ensureCommentState()
	m.ensureListState()
	router, recorded := pushRecordingRouter(m)
	joinAs(t, router, "push_actor")
	m.profiles[2] = db.SocialProfile{UserID: 2, Handle: "push_friend", DisplayName: "Push Friend"}

	// Open follow → NEW_FOLLOWER to the followee.
	code, _, _ := makeProtoRequest(router, "/social/follow", &pb.FollowRequest{Handle: "push_friend"}, &pb.FollowResponse{})
	require.Equal(t, http.StatusOK, code)
	require.Len(t, *recorded, 1)
	assert.Equal(t, int64(2), (*recorded)[0].target)
	assert.Equal(t, push.SocialPushNewFollower, (*recorded)[0].kind)
	assert.Equal(t, "push_actor", (*recorded)[0].actor)

	// Approval-gated follow → FOLLOW_REQUEST instead.
	profile := m.profiles[2]
	profile.RequireFollowApproval = true
	m.profiles[2] = profile
	delete(m.follows, followKey{1, 2})
	makeProtoRequest(router, "/social/follow", &pb.FollowRequest{Handle: "push_friend"}, &pb.FollowResponse{})
	require.Len(t, *recorded, 2)
	assert.Equal(t, push.SocialPushFollowRequest, (*recorded)[1].kind)

	// Approving a pending request → FOLLOW_APPROVED to the requester.
	m.follows[followKey{2, 1}] = followStatusPending
	code, _, _ = makeProtoRequest(router, "/social/follow/approve",
		&pb.FollowApprovalRequest{RequesterHandle: "push_friend", Accept: true}, &pb.SocialAck{})
	require.Equal(t, http.StatusOK, code)
	require.Len(t, *recorded, 3)
	assert.Equal(t, push.SocialPushFollowApproved, (*recorded)[2].kind)
	assert.Equal(t, int64(2), (*recorded)[2].target)

	// A reply to someone else's comment → COMMENT_REPLY to the parent author.
	m.playback[reviewKey{1, commentedEpisodeUUID}] = db.GetEpisodePlaybackForGateRow{
		PlayedUpTo: 300, Duration: 600, PlayingStatus: 2,
	}
	parentID := otherComment(m, "their seed", nil)
	code, _, _ = makeProtoRequest(router, "/social/comment/submit",
		&pb.CommentSubmitRequest{EpisodeUuid: commentedEpisodeUUID, Text: "a reply", ParentId: parentID}, &pb.SocialComment{})
	require.Equal(t, http.StatusOK, code)
	require.Len(t, *recorded, 4)
	assert.Equal(t, push.SocialPushCommentReply, (*recorded)[3].kind)
	assert.Equal(t, int64(2), (*recorded)[3].target)
	assert.NotEmpty(t, (*recorded)[3].data["comment_id"])

	// A list invite → LIST_INVITE with the list id.
	list := &pb.SharedList{}
	makeProtoRequest(router, "/social/list/create", &pb.SharedListCreateRequest{Title: "Push List"}, list)
	code, _, _ = makeProtoRequest(router, "/social/list/invite",
		&pb.SharedListInviteRequest{ListId: list.Id, Handle: "push_friend"}, &pb.SocialAck{})
	require.Equal(t, http.StatusOK, code)
	require.Len(t, *recorded, 5)
	assert.Equal(t, push.SocialPushListInvite, (*recorded)[4].kind)
	assert.NotEmpty(t, (*recorded)[4].data["list_id"])
}
