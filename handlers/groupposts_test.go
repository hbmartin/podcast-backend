package handlers

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/hbmartin/podcast-backend/db"
	pb "github.com/hbmartin/podcast-backend/protos/api"
	"github.com/stretchr/testify/assert"
)

type groupPostBlockMock struct {
	*socialMock
	parent db.GetGroupPostByIDRow
}

func (m *groupPostBlockMock) GetGroupMember(context.Context, db.GetGroupMemberParams) (db.GetGroupMemberRow, error) {
	return db.GetGroupMemberRow{Role: groupRoleMember}, nil
}

func (m *groupPostBlockMock) GetGroupPostByID(context.Context, int64) (db.GetGroupPostByIDRow, error) {
	return m.parent, nil
}

func TestGroupPostReplyBlockLookupFailsClosed(t *testing.T) {
	social := newSocialMock()
	social.profiles[1] = db.SocialProfile{UserID: 1, Handle: "writer"}
	social.blockErr = assert.AnError
	parentUserID := int64(2)
	m := &groupPostBlockMock{
		socialMock: social,
		parent: db.GetGroupPostByIDRow{
			ID: 9, GroupID: 4, UserID: &parentUserID, CreatedAt: time.Now(),
		},
	}
	h := Handlers{Queries: m, Config: testAuthConfig}
	router := http.NewServeMux()
	router.Handle("POST /social/group/post/submit", mockAuthMiddleware(http.HandlerFunc(h.PostGroupPostSubmit)))

	code, _, _ := makeProtoRequest(router, "/social/group/post/submit",
		&pb.GroupPostRequest{GroupId: 4, ParentId: 9, Text: "reply"}, &pb.GroupPost{})

	assert.Equal(t, http.StatusInternalServerError, code)
}
