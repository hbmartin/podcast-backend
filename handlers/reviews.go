package handlers

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/moderation"
	"github.com/hbmartin/podcast-backend/pcerrors"
	pb "github.com/hbmartin/podcast-backend/protos/api"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Written reviews + episode reactions (Slice 3; pocket-casts-ios docs/Social.md).
// Stars stay on the anonymous rating endpoints; the TEXT half of a review
// requires a joined account and is publicly attributed. Reactions are recorded
// per account but served as aggregate counts only.

const (
	maxReviewTextLen  = 2000
	maxReviewPageSize = 50
	// Same threshold the client's rate gate uses
	// (Constants.Values.numberOfEpisodesListenedRequiredToRate), clamped to
	// the podcast's episode count client-side; the server applies the raw
	// minimum as defense in depth.
	reviewListenGateEpisodes = 2

	reactionKindMax = int16(pb.ReactionKind_REACTION_KIND_FIRE)
)

// PostReviewSubmit handles POST /social/review/submit: upsert the caller's
// attributed review text. Requires a joined account (ADR-0005), a clean text
// pre-filter pass (ADR-0007), and the listen-gate.
func (h Handlers) PostReviewSubmit(w http.ResponseWriter, r *http.Request) {
	req := &pb.PodcastReviewSubmitRequest{}
	if err := bindProto(r, req); err != nil || !uuidPattern.MatchString(req.PodcastUuid) {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	text := strings.TrimSpace(req.Text)
	if text == "" {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "review text required")
		return
	}
	if utf8.RuneCountInString(text) > maxReviewTextLen {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "review too long")
		return
	}
	if err := moderation.CheckText(text); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "review rejected: "+err.Error())
		return
	}

	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	// Attributed public text requires a joined account.
	profile, err := h.Queries.GetSocialProfileByUserID(r.Context(), user.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			pcerrors.Write(w, http.StatusForbidden, pcerrors.AccessDenied, "join required to write reviews")
			return
		}
		writeError(w, r, err)
		return
	}

	// Listen-gate (defense in depth; the client enforces the clamped variant).
	played, err := h.Queries.CountPlayedEpisodesOfPodcast(r.Context(), db.CountPlayedEpisodesOfPodcastParams{
		UserID: user.ID, PodcastUuid: req.PodcastUuid,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	if played < reviewListenGateEpisodes {
		pcerrors.Write(w, http.StatusForbidden, pcerrors.AccessDenied, "listen to this podcast before reviewing it")
		return
	}

	row, err := h.Queries.UpsertPodcastReview(r.Context(), db.UpsertPodcastReviewParams{
		UserID: user.ID, PodcastUuid: req.PodcastUuid, Text: text,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}

	rating := int16(0)
	if own, err := h.Queries.GetPodcastRating(r.Context(), db.GetPodcastRatingParams{UserID: user.ID, PodcastUuid: req.PodcastUuid}); err == nil {
		rating = own.Rating
	}

	writeProto(w, http.StatusOK, &pb.PodcastReview{
		UserId:      user.Uuid,
		Handle:      profile.Handle,
		DisplayName: profile.DisplayName,
		Rating:      uint32(rating),
		Text:        row.Text,
		CreatedAt:   timestamppb.New(row.CreatedAt),
		UpdatedAt:   timestamppb.New(row.UpdatedAt),
	})
}

// PostReviewDelete handles POST /social/review/delete: removes the caller's
// review text for a podcast. Idempotent.
func (h Handlers) PostReviewDelete(w http.ResponseWriter, r *http.Request) {
	req := &pb.PodcastReviewDeleteRequest{}
	if err := bindProto(r, req); err != nil || !uuidPattern.MatchString(req.PodcastUuid) {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	if _, err := h.Queries.DeletePodcastReview(r.Context(), db.DeletePodcastReviewParams{
		UserID: user.ID, PodcastUuid: req.PodcastUuid,
	}); err != nil {
		writeError(w, r, err)
		return
	}

	writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
}

// PostPodcastReviews handles POST /podcast/reviews (optional auth): the public
// paginated review list, newest first. Authenticated viewers don't see reviews
// by authors they're in a block relationship with, and get your_review.
func (h Handlers) PostPodcastReviews(w http.ResponseWriter, r *http.Request) {
	req := &pb.PodcastReviewsRequest{}
	if err := bindProto(r, req); err != nil || !uuidPattern.MatchString(req.PodcastUuid) {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	limit := req.Limit
	if limit <= 0 || limit > maxReviewPageSize {
		limit = maxReviewPageSize
	}
	offset := max(req.Offset, 0)

	rows, err := h.Queries.GetPodcastReviews(r.Context(), db.GetPodcastReviewsParams{
		PodcastUuid: req.PodcastUuid, Limit: limit, Offset: offset,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	total, err := h.Queries.CountPodcastReviews(r.Context(), req.PodcastUuid)
	if err != nil {
		writeError(w, r, err)
		return
	}

	resp := &pb.PodcastReviewsResponse{Total: total}

	// Resolve the optional viewer for block filtering + your_review.
	var viewerID int64
	if ctxUser := getUser(r.Context()); ctxUser != nil {
		if viewer, err := h.Queries.GetUserByUUID(r.Context(), ctxUser.UUID); err == nil {
			viewerID = viewer.ID
			if own, err := h.Queries.GetOwnPodcastReview(r.Context(), db.GetOwnPodcastReviewParams{
				UserID: viewer.ID, PodcastUuid: req.PodcastUuid,
			}); err == nil {
				resp.YourReview = reviewToProto(db.GetPodcastReviewsRow(own))
			}
		}
	}

	for _, row := range rows {
		if viewerID != 0 && row.UserID != viewerID {
			blocked, err := h.Queries.IsBlockedEither(r.Context(), db.IsBlockedEitherParams{
				UserID: viewerID, TargetUserID: row.UserID,
			})
			if err != nil {
				writeError(w, r, err)
				return
			}
			if blocked {
				continue
			}
		}
		resp.Reviews = append(resp.Reviews, reviewToProto(row))
	}

	writeProto(w, http.StatusOK, resp)
}

func reviewToProto(row db.GetPodcastReviewsRow) *pb.PodcastReview {
	return &pb.PodcastReview{
		UserId:      row.AuthorUuid,
		Handle:      row.Handle,
		DisplayName: row.DisplayName,
		Rating:      uint32(row.Rating),
		Text:        row.Text,
		CreatedAt:   timestamppb.New(row.CreatedAt),
		UpdatedAt:   timestamppb.New(row.UpdatedAt),
	}
}

// PostReactionSet handles POST /social/reaction/set: upserts (or clears, when
// UNSPECIFIED) the caller's single reaction on an episode. Account-level — no
// join required; the public only ever sees counts.
func (h Handlers) PostReactionSet(w http.ResponseWriter, r *http.Request) {
	req := &pb.EpisodeReactionSetRequest{}
	if err := bindProto(r, req); err != nil || req.EpisodeUuid == "" {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	kind := int16(req.Kind)
	if kind < 0 || kind > reactionKindMax {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "unknown reaction")
		return
	}

	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	var err error
	if kind == 0 {
		_, err = h.Queries.DeleteEpisodeReaction(r.Context(), db.DeleteEpisodeReactionParams{
			UserID: user.ID, EpisodeUuid: req.EpisodeUuid,
		})
	} else {
		err = h.Queries.UpsertEpisodeReaction(r.Context(), db.UpsertEpisodeReactionParams{
			UserID: user.ID, EpisodeUuid: req.EpisodeUuid, Kind: kind,
		})
	}
	if err != nil {
		writeError(w, r, err)
		return
	}

	writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
}

// PostEpisodeReactions handles POST /episode/reactions (optional auth):
// aggregate counts, plus your_reaction for authenticated callers.
func (h Handlers) PostEpisodeReactions(w http.ResponseWriter, r *http.Request) {
	req := &pb.EpisodeReactionsRequest{}
	if err := bindProto(r, req); err != nil || req.EpisodeUuid == "" {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	rows, err := h.Queries.GetEpisodeReactionCounts(r.Context(), req.EpisodeUuid)
	if err != nil {
		writeError(w, r, err)
		return
	}

	resp := &pb.EpisodeReactionsResponse{}
	for _, row := range rows {
		resp.Counts = append(resp.Counts, &pb.ReactionCount{
			Kind: pb.ReactionKind(row.Kind), Count: row.Count,
		})
	}

	if ctxUser := getUser(r.Context()); ctxUser != nil {
		if viewer, err := h.Queries.GetUserByUUID(r.Context(), ctxUser.UUID); err == nil {
			if kind, err := h.Queries.GetOwnEpisodeReaction(r.Context(), db.GetOwnEpisodeReactionParams{
				UserID: viewer.ID, EpisodeUuid: req.EpisodeUuid,
			}); err == nil {
				resp.YourReaction = pb.ReactionKind(kind)
			}
		}
	}

	writeProto(w, http.StatusOK, resp)
}
