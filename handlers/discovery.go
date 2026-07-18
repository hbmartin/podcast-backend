package handlers

import (
	"net/http"

	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/pcerrors"
	pb "github.com/hbmartin/podcast-backend/protos/api"
)

// Social discovery (Slice 10, docs/Social.md): the trending row ranks
// followees' recently finished episodes under each actor's HISTORY
// visibility; podcast proof names followees under their FOLLOWED-SHOWS
// visibility (named only when their list is already visible to the caller).
const (
	maxTrendingResults = 10
	maxProofNames      = 3
)

// PostSocialTrending handles POST /social/trending (joined required).
func (h Handlers) PostSocialTrending(w http.ResponseWriter, r *http.Request) {
	req := &pb.SocialTrendingRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	user, _, ok := h.requireJoined(w, r)
	if !ok {
		return
	}

	limit := req.Limit
	if limit <= 0 || limit > maxTrendingResults {
		limit = maxTrendingResults
	}
	rows, err := h.Queries.GetTrendingWithFriends(r.Context(), db.GetTrendingWithFriendsParams{
		UserID: user.ID, Limit: limit,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}

	resp := &pb.SocialTrendingResponse{}
	for _, row := range rows {
		resp.Podcasts = append(resp.Podcasts, &pb.TrendingPodcast{
			PodcastUuid:   row.PodcastUuid,
			Title:         row.Title,
			Author:        row.Author,
			ListenerCount: row.ListenerCount,
		})
	}
	writeProto(w, http.StatusOK, resp)
}

// PostPodcastProof handles POST /social/podcast/proof (joined required).
func (h Handlers) PostPodcastProof(w http.ResponseWriter, r *http.Request) {
	req := &pb.PodcastProofRequest{}
	if err := bindProto(r, req); err != nil || !uuidPattern.MatchString(req.PodcastUuid) {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	user, _, ok := h.requireJoined(w, r)
	if !ok {
		return
	}

	rows, err := h.Queries.GetPodcastProof(r.Context(), db.GetPodcastProofParams{
		FollowerUserID: user.ID, PodcastUuid: req.PodcastUuid,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}

	// Every row is already visibility-filtered in SQL — private lists never
	// reach the count at all (QA finding).
	resp := &pb.PodcastProofResponse{TotalCount: int32(len(rows))}
	for _, handle := range rows {
		if len(resp.VisibleHandles) < maxProofNames {
			resp.VisibleHandles = append(resp.VisibleHandles, handle)
		}
	}
	writeProto(w, http.StatusOK, resp)
}
