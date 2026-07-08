package handlers

import (
	"errors"
	"net/http"

	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/pcerrors"
	pb "github.com/hbmartin/podcast-backend/protos/api"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// PostPodcastRatingAdd handles POST /user/podcast_rating/add: upsert the
// user's 1-5 star rating. The client only checks the response status.
func (h Handlers) PostPodcastRatingAdd(w http.ResponseWriter, r *http.Request) {
	req := &pb.PodcastRatingAddRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	if !uuidPattern.MatchString(req.PodcastUuid) || req.PodcastRating < 1 || req.PodcastRating > 5 {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "rating must be 1-5 for a valid podcast")
		return
	}

	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	err := h.Queries.UpsertPodcastRating(r.Context(), db.UpsertPodcastRatingParams{
		UserID:      user.ID,
		PodcastUuid: req.PodcastUuid,
		Rating:      int16(req.PodcastRating),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// PostPodcastRatingShow handles POST /user/podcast_rating/show: the user's
// own rating for one podcast; 404 when none exists.
func (h Handlers) PostPodcastRatingShow(w http.ResponseWriter, r *http.Request) {
	req := &pb.PodcastRatingShowRequest{}
	if err := bindProto(r, req); err != nil || req.PodcastUuid == "" {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	row, err := h.Queries.GetPodcastRating(r.Context(), db.GetPodcastRatingParams{UserID: user.ID, PodcastUuid: req.PodcastUuid})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeError(w, r, err)
		return
	}

	writeProto(w, http.StatusOK, ratingToProto(row))
}

// GetPodcastRatingList handles GET /user/podcast_rating/list: every rating
// the user has submitted.
func (h Handlers) GetPodcastRatingList(w http.ResponseWriter, r *http.Request) {
	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	rows, err := h.Queries.GetUserPodcastRatings(r.Context(), user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}

	resp := &pb.PodcastRatingsResponse{}
	for _, row := range rows {
		resp.PodcastRatings = append(resp.PodcastRatings, ratingToProto(row))
	}
	writeProto(w, http.StatusOK, resp)
}

func ratingToProto(row db.PodcastRating) *pb.PodcastRating {
	return &pb.PodcastRating{
		PodcastUuid:   row.PodcastUuid,
		ModifiedAt:    timestamppb.New(row.ModifiedAt),
		PodcastRating: uint32(row.Rating),
	}
}

// GetPodcastRatingPublic handles GET /podcast/rating/{uuid} (cache host
// role): the aggregate rating as {"total": n, "average": x}.
func (h Handlers) GetPodcastRatingPublic(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	if !uuidPattern.MatchString(uuid) {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	row, err := h.Queries.GetPodcastRatingAggregate(r.Context(), uuid)
	if err != nil {
		writeError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"total":   row.Total,
		"average": row.Average,
	})
}
