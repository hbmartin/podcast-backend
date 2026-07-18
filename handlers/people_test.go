package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"

	"github.com/hbmartin/podcast-backend/db"
	pb "github.com/hbmartin/podcast-backend/protos/api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Find-people state for socialMock (Slice 9). Search/suggestion SQL is
// mirrored just enough for handler-level tests; the e2e suite covers the
// real queries.

func (m *socialMock) SearchSocialProfiles(ctx context.Context, arg db.SearchSocialProfilesParams) ([]db.SearchSocialProfilesRow, error) {
	var rows []db.SearchSocialProfilesRow
	prefix := ""
	if arg.Column2 != nil {
		prefix = *arg.Column2
	}
	for _, p := range m.profiles {
		if p.UserID == arg.FollowerUserID || p.HideFromDiscovery {
			continue
		}
		if !strings.HasPrefix(p.Handle, prefix) && !strings.HasPrefix(strings.ToLower(p.DisplayName), prefix) {
			continue
		}
		if m.rels[[3]int64{arg.FollowerUserID, p.UserID, 0}] || m.rels[[3]int64{p.UserID, arg.FollowerUserID, 0}] {
			continue
		}
		status := int32(-1)
		if s, ok := m.follows[followKey{arg.FollowerUserID, p.UserID}]; ok {
			status = int32(s)
		}
		rows = append(rows, db.SearchSocialProfilesRow{
			Handle: p.Handle, DisplayName: p.DisplayName, UserID: p.UserID, FollowStatus: status,
		})
	}
	return rows, nil
}

func (m *socialMock) GetSocialSuggestions(ctx context.Context, arg db.GetSocialSuggestionsParams) ([]db.GetSocialSuggestionsRow, error) {
	mutuals := map[int64]int32{}
	for edge, status := range m.follows {
		if edge.follower != arg.FollowerUserID || status != followStatusActive {
			continue
		}
		for second, secondStatus := range m.follows {
			if second.follower != edge.followee || secondStatus != followStatusActive {
				continue
			}
			candidate := second.followee
			if candidate == arg.FollowerUserID {
				continue
			}
			if _, direct := m.follows[followKey{arg.FollowerUserID, candidate}]; direct {
				continue
			}
			if m.profiles[candidate].HideFromDiscovery {
				continue
			}
			mutuals[candidate]++
		}
	}
	var rows []db.GetSocialSuggestionsRow
	for userID, count := range mutuals {
		rows = append(rows, db.GetSocialSuggestionsRow{
			Handle: m.profiles[userID].Handle, DisplayName: m.profiles[userID].DisplayName,
			UserID: userID, MutualCount: count,
		})
	}
	return rows, nil
}

func (m *socialMock) GetDiscoverableProfileEmails(ctx context.Context) ([]db.GetDiscoverableProfileEmailsRow, error) {
	var rows []db.GetDiscoverableProfileEmailsRow
	for _, p := range m.profiles {
		if p.HideFromDiscovery {
			continue
		}
		rows = append(rows, db.GetDiscoverableProfileEmailsRow{
			UserID: p.UserID, Handle: p.Handle, DisplayName: p.DisplayName,
			Email: m.usersByID[p.UserID].Email,
		})
	}
	return rows, nil
}

func peopleRouter(m *socialMock) *http.ServeMux {
	router := listsRouter(m)
	h := Handlers{Queries: m, Config: testAuthConfig}
	router.Handle("POST /social/search", mockAuthMiddleware(http.HandlerFunc(h.PostSocialSearch)))
	router.Handle("POST /social/suggestions", mockAuthMiddleware(http.HandlerFunc(h.PostSocialSuggestions)))
	router.Handle("POST /social/contacts/salt", mockAuthMiddleware(http.HandlerFunc(h.PostContactsSalt)))
	router.Handle("POST /social/contacts/match", mockAuthMiddleware(http.HandlerFunc(h.PostContactsMatch)))
	return router
}

func TestPeopleSearchAndDiscoverability(t *testing.T) {
	m := newSocialMock()
	m.ensureGraphState()
	router := peopleRouter(m)
	joinAs(t, router, "searcher_one")
	m.profiles[2] = db.SocialProfile{UserID: 2, Handle: "findable_friend", DisplayName: "Findable Friend"}

	resp := &pb.SocialSearchResponse{}
	code, _, _ := makeProtoRequest(router, "/social/search", &pb.SocialSearchRequest{Query: "@Find"}, resp)
	require.Equal(t, http.StatusOK, code)
	require.Len(t, resp.Profiles, 1)
	assert.Equal(t, "findable_friend", resp.Profiles[0].Handle)

	// Hidden profiles vanish from search.
	profile := m.profiles[2]
	profile.HideFromDiscovery = true
	m.profiles[2] = profile
	resp = &pb.SocialSearchResponse{}
	makeProtoRequest(router, "/social/search", &pb.SocialSearchRequest{Query: "find"}, resp)
	assert.Empty(t, resp.Profiles)

	// Blocked either way never surfaces.
	profile.HideFromDiscovery = false
	m.profiles[2] = profile
	m.rels[[3]int64{2, 1, 0}] = true
	resp = &pb.SocialSearchResponse{}
	makeProtoRequest(router, "/social/search", &pb.SocialSearchRequest{Query: "find"}, resp)
	assert.Empty(t, resp.Profiles)
}

func TestPeopleSuggestionsCountOnly(t *testing.T) {
	m := newSocialMock()
	m.ensureGraphState()
	router := peopleRouter(m)
	joinAs(t, router, "suggestee")
	m.profiles[2] = db.SocialProfile{UserID: 2, Handle: "hop_friend", DisplayName: "Hop"}
	m.addUser(db.User{ID: 3, Uuid: "cccccccc-3333-3333-3333-333333333333", Email: "three@test.com"})
	m.profiles[3] = db.SocialProfile{UserID: 3, Handle: "suggested_person", DisplayName: "Suggested"}
	m.follows[followKey{1, 2}] = followStatusActive
	m.follows[followKey{2, 3}] = followStatusActive

	resp := &pb.SocialSuggestionsResponse{}
	code, _, _ := makeProtoRequest(router, "/social/suggestions", &pb.SocialSuggestionsRequest{}, resp)
	require.Equal(t, http.StatusOK, code)
	require.Len(t, resp.Profiles, 1)
	assert.Equal(t, "suggested_person", resp.Profiles[0].Handle)
	assert.Equal(t, int32(1), resp.Profiles[0].MutualCount)
}

func TestContactsMatchTransient(t *testing.T) {
	m := newSocialMock()
	router := peopleRouter(m)
	joinAs(t, router, "matcher")
	m.profiles[2] = db.SocialProfile{UserID: 2, Handle: "in_contacts", DisplayName: "In Contacts"}

	saltResp := &pb.ContactsSaltResponse{}
	code, _, _ := makeProtoRequest(router, "/social/contacts/salt", &pb.SocialSearchRequest{}, saltResp)
	require.Equal(t, http.StatusOK, code)
	require.NotEmpty(t, saltResp.Salt)

	// User 2's account email, hashed the way the client would.
	sum := sha256.Sum256([]byte(saltResp.Salt + "other@test.com"))
	emailHash := hex.EncodeToString(sum[:])
	phoneSum := sha256.Sum256([]byte(saltResp.Salt + "+15551234567"))

	match := &pb.ContactsMatchResponse{}
	code, _, _ = makeProtoRequest(router, "/social/contacts/match", &pb.ContactsMatchRequest{
		Hashes: []*pb.ContactHash{
			{Kind: pb.ContactHashKind_CONTACT_HASH_KIND_EMAIL, Hash: emailHash},
			{Kind: pb.ContactHashKind_CONTACT_HASH_KIND_PHONE, Hash: hex.EncodeToString(phoneSum[:])},
		},
	}, match)
	require.Equal(t, http.StatusOK, code)
	require.Len(t, match.Profiles, 1, "the email matches; the phone hash is wire-ready but ignored")
	assert.Equal(t, "in_contacts", match.Profiles[0].Handle)

	// A hidden profile never matches.
	profile := m.profiles[2]
	profile.HideFromDiscovery = true
	m.profiles[2] = profile
	match = &pb.ContactsMatchResponse{}
	makeProtoRequest(router, "/social/contacts/match", &pb.ContactsMatchRequest{
		Hashes: []*pb.ContactHash{{Kind: pb.ContactHashKind_CONTACT_HASH_KIND_EMAIL, Hash: emailHash}},
	}, match)
	assert.Empty(t, match.Profiles)
}
