package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/pcerrors"
	pb "github.com/hbmartin/podcast-backend/protos/api"
)

// Find people (Slice 9, docs/Social.md): prefix search over discoverable
// joined profiles, friends-of-followed suggestions with count-only copy, and
// transient contact matching over typed salted hashes. Search requires a
// signed-in account (scrape deterrence); suggestions require the caller's
// graph (joined); contact matching stores nothing and never notifies matches.
const (
	maxSearchResults      = 25
	maxSuggestionResults  = 10
	maxContactHashUploads = 2000
)

// contactsSalt is deterministic per server so clients can hash offline within
// a session; deriving it from the JWT secret avoids new configuration.
func (h Handlers) contactsSalt() string {
	sum := sha256.Sum256([]byte("contacts-salt:" + h.Config.JWTSecret))
	return hex.EncodeToString(sum[:16])
}

// PostSocialSearch handles POST /social/search.
func (h Handlers) PostSocialSearch(w http.ResponseWriter, r *http.Request) {
	req := &pb.SocialSearchRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	query := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(req.Query, "@")))
	// Escape LIKE metacharacters: '%' must not enumerate the directory and
	// '_' is a legal handle character, not a wildcard (QA finding).
	query = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(query)
	if query == "" {
		writeProto(w, http.StatusOK, &pb.SocialSearchResponse{})
		return
	}

	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	limit := req.Limit
	if limit <= 0 || limit > maxSearchResults {
		limit = maxSearchResults
	}
	rows, err := h.Queries.SearchSocialProfiles(r.Context(), db.SearchSocialProfilesParams{
		FollowerUserID: user.ID, Column2: &query, Limit: limit,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}

	resp := &pb.SocialSearchResponse{}
	for _, row := range rows {
		state := pb.FollowState_FOLLOW_STATE_NONE
		switch row.FollowStatus {
		case int32(followStatusActive):
			state = pb.FollowState_FOLLOW_STATE_ACTIVE
		case int32(followStatusPending):
			state = pb.FollowState_FOLLOW_STATE_PENDING
		}
		resp.Profiles = append(resp.Profiles, &pb.ProfileSummary{
			Handle: row.Handle, DisplayName: row.DisplayName, YourFollowState: state,
		})
	}
	writeProto(w, http.StatusOK, resp)
}

// PostSocialSuggestions handles POST /social/suggestions (joined required).
func (h Handlers) PostSocialSuggestions(w http.ResponseWriter, r *http.Request) {
	req := &pb.SocialSuggestionsRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	user, _, ok := h.requireJoined(w, r)
	if !ok {
		return
	}

	limit := req.Limit
	if limit <= 0 || limit > maxSuggestionResults {
		limit = maxSuggestionResults
	}
	rows, err := h.Queries.GetSocialSuggestions(r.Context(), db.GetSocialSuggestionsParams{
		FollowerUserID: user.ID, Limit: limit,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}

	resp := &pb.SocialSuggestionsResponse{}
	for _, row := range rows {
		resp.Profiles = append(resp.Profiles, &pb.ProfileSummary{
			Handle: row.Handle, DisplayName: row.DisplayName, MutualCount: row.MutualCount,
		})
	}
	writeProto(w, http.StatusOK, resp)
}

// PostContactsSalt handles POST /social/contacts/salt.
func (h Handlers) PostContactsSalt(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.currentDbUser(w, r); !ok {
		return
	}
	writeProto(w, http.StatusOK, &pb.ContactsSaltResponse{Salt: h.contactsSalt()})
}

// PostContactsMatch handles POST /social/contacts/match: transient — hashes
// are matched in memory against discoverable accounts and discarded. Phone
// hashes are accepted but unmatched until accounts ever carry phone numbers.
func (h Handlers) PostContactsMatch(w http.ResponseWriter, r *http.Request) {
	req := &pb.ContactsMatchRequest{}
	if err := bindProto(r, req); err != nil || len(req.Hashes) == 0 {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	if len(req.Hashes) > maxContactHashUploads {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "too many hashes")
		return
	}
	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	wanted := make(map[string]bool, len(req.Hashes))
	for _, contactHash := range req.Hashes {
		if contactHash.Kind == pb.ContactHashKind_CONTACT_HASH_KIND_EMAIL {
			wanted[strings.ToLower(contactHash.Hash)] = true
		}
	}
	if len(wanted) == 0 {
		writeProto(w, http.StatusOK, &pb.ContactsMatchResponse{})
		return
	}

	candidates, err := h.Queries.GetDiscoverableProfileEmails(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}

	salt := h.contactsSalt()
	resp := &pb.ContactsMatchResponse{}
	for _, candidate := range candidates {
		if candidate.UserID == user.ID {
			continue
		}
		sum := sha256.Sum256([]byte(salt + strings.ToLower(strings.TrimSpace(candidate.Email))))
		if !wanted[hex.EncodeToString(sum[:])] {
			continue
		}
		blocked, err := h.Queries.IsBlockedEither(r.Context(), db.IsBlockedEitherParams{
			UserID: user.ID, TargetUserID: candidate.UserID,
		})
		if err == nil && blocked {
			continue
		}
		resp.Profiles = append(resp.Profiles, &pb.ProfileSummary{
			Handle: candidate.Handle, DisplayName: candidate.DisplayName,
		})
	}
	writeProto(w, http.StatusOK, resp)
}

// PostCurators handles POST /social/curators (joined required): the
// operator-designated directory, follower-ranked (Slice 15, ADR-0014).
// Designation itself has no endpoint — it is an admin/DB act.
func (h Handlers) PostCurators(w http.ResponseWriter, r *http.Request) {
	req := &pb.CuratorsRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	if _, _, ok := h.requireJoined(w, r); !ok {
		return
	}
	limit := req.Limit
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	rows, err := h.Queries.GetCurators(r.Context(), limit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	resp := &pb.CuratorsResponse{}
	for _, row := range rows {
		resp.Curators = append(resp.Curators, &pb.CuratorEntry{
			Handle: row.Handle, DisplayName: row.DisplayName,
			Bio: row.Bio, FollowerCount: row.FollowerCount,
		})
	}
	writeProto(w, http.StatusOK, resp)
}
