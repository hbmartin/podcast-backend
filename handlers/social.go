package handlers

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/moderation"
	"github.com/hbmartin/podcast-backend/pcerrors"
	pb "github.com/hbmartin/podcast-backend/protos/api"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Social identity + moderation endpoints (pocket-casts-ios docs/Social.md,
// docs/SocialModeration.md, ADR-0005/0006/0007). All bodies are protobuf
// (application/octet-stream). The iOS client decodes a SocialAck proto body
// from the moderation endpoints — a bare 200 is not enough. Avatars are
// deferred: avatar_url is always empty in this slice.

// handlePattern is the shared charset rule (ADR-0005); the server is
// authoritative and the DB CHECK constraint backstops it.
var handlePattern = regexp.MustCompile(`^[a-z0-9_]{3,30}$`)

const (
	handleStatusActive     int16 = 0
	handleStatusTombstoned int16 = 1
	handleStatusReserved   int16 = 2

	relationshipBlock int16 = 0
	relationshipMute  int16 = 1

	maxReportContextLen = 1000
)

// normalizeHandle lowercases and strips whitespace and a leading '@' so the
// stored form is always the canonical one.
func normalizeHandle(raw string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(raw), "@"))
}

// PostSocialHandleAvailability handles POST /social/handle/availability: the
// rate-limited typeahead check. No row means available; existing rows map by
// status. Never claims anything.
func (h Handlers) PostSocialHandleAvailability(w http.ResponseWriter, r *http.Request) {
	req := &pb.HandleAvailabilityRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	handle := normalizeHandle(req.Handle)
	resp := &pb.HandleAvailabilityResponse{NormalizedHandle: handle}

	if !handlePattern.MatchString(handle) {
		resp.Status = pb.HandleStatus_HANDLE_STATUS_INVALID
		writeProto(w, http.StatusOK, resp)
		return
	}

	row, err := h.Queries.GetHandleStatus(r.Context(), handle)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			resp.Status = pb.HandleStatus_HANDLE_STATUS_AVAILABLE
			writeProto(w, http.StatusOK, resp)
			return
		}
		writeError(w, r, err)
		return
	}

	switch row.Status {
	case handleStatusTombstoned:
		resp.Status = pb.HandleStatus_HANDLE_STATUS_TOMBSTONED
	case handleStatusReserved:
		resp.Status = pb.HandleStatus_HANDLE_STATUS_RESERVED
	default:
		resp.Status = pb.HandleStatus_HANDLE_STATUS_TAKEN
	}
	writeProto(w, http.StatusOK, resp)
}

// PostSocialJoin handles POST /social/join: the one-time opt-in. Claims the
// handle and creates the profile in one transaction; the handles PK is the
// compare-and-set, so a concurrent duplicate claim loses cleanly (23505).
// Every visibility starts private (column defaults) per ADR-0006.
func (h Handlers) PostSocialJoin(w http.ResponseWriter, r *http.Request) {
	req := &pb.JoinRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	handle := normalizeHandle(req.Handle)
	if !handlePattern.MatchString(handle) {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid handle")
		return
	}
	if req.AcceptedTermsVersion <= 0 {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "terms must be accepted")
		return
	}
	displayName := strings.TrimSpace(req.DisplayName)
	if err := moderation.CheckDisplayName(displayName); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "display name rejected: "+err.Error())
		return
	}

	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	if _, err := h.Queries.GetSocialProfileByUserID(r.Context(), user.ID); err == nil {
		pcerrors.Write(w, http.StatusConflict, pcerrors.AccessDenied, "already joined")
		return
	} else if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, r, err)
		return
	}

	var profile db.SocialProfile
	err := h.Queries.InTx(r.Context(), func(q db.Querier) error {
		if err := q.ClaimHandle(r.Context(), db.ClaimHandleParams{Handle: handle, UserID: &user.ID}); err != nil {
			return err
		}
		created, err := q.CreateSocialProfile(r.Context(), db.CreateSocialProfileParams{
			UserID:       user.ID,
			Handle:       handle,
			DisplayName:  displayName,
			TermsVersion: req.AcceptedTermsVersion,
		})
		if err != nil {
			return err
		}
		profile = created
		return nil
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// Taken, reserved, tombstoned, or this account already holds a
			// handle — all surface as a unique/PK violation on the claim.
			pcerrors.Write(w, http.StatusConflict, pcerrors.AccessDenied, "handle unavailable")
			return
		}
		writeError(w, r, err)
		return
	}

	writeProto(w, http.StatusOK, &pb.JoinResponse{Profile: profileToProto(profile, user.Uuid)})
}

// PostSocialProfileGet handles POST /social/profile/get: the caller's own
// profile with every visibility tier; 404 when the account hasn't joined.
func (h Handlers) PostSocialProfileGet(w http.ResponseWriter, r *http.Request) {
	req := &pb.ProfileGetRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	profile, err := h.Queries.GetSocialProfileByUserID(r.Context(), user.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeError(w, r, err)
		return
	}

	writeProto(w, http.StatusOK, &pb.ProfileResponse{Profile: profileToProto(profile, user.Uuid)})
}

// PostSocialProfileUpdate handles POST /social/profile/update: display name,
// bio and per-field visibility. The handle is immutable and never accepted
// here (ADR-0005). Name/bio re-run the text pre-filter.
func (h Handlers) PostSocialProfileUpdate(w http.ResponseWriter, r *http.Request) {
	req := &pb.ProfileUpdateRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	displayName := strings.TrimSpace(req.DisplayName)
	if err := moderation.CheckDisplayName(displayName); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "display name rejected: "+err.Error())
		return
	}
	if err := moderation.CheckBio(req.Bio); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "bio rejected: "+err.Error())
		return
	}

	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	profile, err := h.Queries.UpdateSocialProfile(r.Context(), db.UpdateSocialProfileParams{
		UserID:                  user.ID,
		DisplayName:             displayName,
		Bio:                     req.Bio,
		AvatarVisibility:        visibilityToStored(req.AvatarVisibility),
		BioVisibility:           visibilityToStored(req.BioVisibility),
		FollowedShowsVisibility: visibilityToStored(req.FollowedShowsVisibility),
		TopPodcastsVisibility:   visibilityToStored(req.TopPodcastsVisibility),
		StatsVisibility:         visibilityToStored(req.StatsVisibility),
		HistoryVisibility:       visibilityToStored(req.HistoryVisibility),
		PresenceVisibility:      visibilityToStored(req.PresenceVisibility),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeError(w, r, err)
		return
	}

	writeProto(w, http.StatusOK, &pb.ProfileResponse{Profile: profileToProto(profile, user.Uuid)})
}

// PostSocialProfilePublic handles POST /social/profile/public (optional auth):
// the visibility-filtered public read. Blocked-either-way, unjoined, and
// tombstoned all produce the same 404 shape, so a blocked viewer can't
// distinguish removal from blocking (docs/SocialModeration.md).
func (h Handlers) PostSocialProfilePublic(w http.ResponseWriter, r *http.Request) {
	req := &pb.PublicProfileRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	handle := normalizeHandle(req.Handle)
	if !handlePattern.MatchString(handle) {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	profile, err := h.Queries.GetSocialProfileByHandle(r.Context(), handle)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeError(w, r, err)
		return
	}

	// Resolve the (optional) viewer for the block check and the owner case.
	isOwner := false
	if ctxUser := getUser(r.Context()); ctxUser != nil {
		viewer, err := h.Queries.GetUserByUUID(r.Context(), ctxUser.UUID)
		if err == nil {
			if viewer.ID == profile.UserID {
				isOwner = true
			} else {
				blocked, err := h.Queries.IsBlockedEither(r.Context(), db.IsBlockedEitherParams{
					UserID:       viewer.ID,
					TargetUserID: profile.UserID,
				})
				if err != nil {
					writeError(w, r, err)
					return
				}
				if blocked {
					w.WriteHeader(http.StatusNotFound)
					return
				}
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			writeError(w, r, err)
			return
		}
	}

	owner, err := h.Queries.GetUserByID(r.Context(), profile.UserID)
	if err != nil {
		writeError(w, r, err)
		return
	}

	resp := &pb.PublicProfileResponse{
		UserId:      owner.Uuid,
		Handle:      profile.Handle,
		DisplayName: profile.DisplayName,
		CreatedAt:   timestamppb.New(profile.CreatedAt),
	}
	// Phase 1 exposes only the public tier to non-owners; followers-only
	// unlocks with the Phase-2 graph (ADR-0006). Owners see their own fields.
	if isOwner || pb.SocialVisibility(profile.BioVisibility) == pb.SocialVisibility_SOCIAL_VISIBILITY_PUBLIC {
		resp.Bio = profile.Bio
	}
	resp.HasStats = isOwner || pb.SocialVisibility(profile.StatsVisibility) == pb.SocialVisibility_SOCIAL_VISIBILITY_PUBLIC
	// avatar_url stays empty: avatars are deferred from this slice.

	writeProto(w, http.StatusOK, resp)
}

// PostSocialBlock / PostSocialUnblock — mutual invisibility while blocked.
func (h Handlers) PostSocialBlock(w http.ResponseWriter, r *http.Request) {
	h.setRelationship(w, r, relationshipBlock, true)
}

func (h Handlers) PostSocialUnblock(w http.ResponseWriter, r *http.Request) {
	h.setRelationship(w, r, relationshipBlock, false)
}

// PostSocialMute / PostSocialUnmute — one-way hide; the target is never told.
func (h Handlers) PostSocialMute(w http.ResponseWriter, r *http.Request) {
	h.setRelationship(w, r, relationshipMute, true)
}

func (h Handlers) PostSocialUnmute(w http.ResponseWriter, r *http.Request) {
	h.setRelationship(w, r, relationshipMute, false)
}

// setRelationship implements the four block/mute mutations. BlockRequest and
// MuteRequest share the same single-field wire shape, so both decode as
// BlockRequest here. Idempotent in both directions.
func (h Handlers) setRelationship(w http.ResponseWriter, r *http.Request, kind int16, add bool) {
	req := &pb.BlockRequest{}
	if err := bindProto(r, req); err != nil || !uuidPattern.MatchString(req.TargetUserId) {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	target, err := h.Queries.GetUserByUUID(r.Context(), strings.ToLower(req.TargetUserId))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeError(w, r, err)
		return
	}
	if target.ID == user.ID {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "cannot target yourself")
		return
	}

	if add {
		err = h.Queries.UpsertSocialRelationship(r.Context(), db.UpsertSocialRelationshipParams{
			UserID: user.ID, TargetUserID: target.ID, Kind: kind,
		})
	} else {
		_, err = h.Queries.DeleteSocialRelationship(r.Context(), db.DeleteSocialRelationshipParams{
			UserID: user.ID, TargetUserID: target.ID, Kind: kind,
		})
	}
	if err != nil {
		writeError(w, r, err)
		return
	}

	writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
}

// PostSocialReport handles POST /social/report: a community flag into the
// single triage queue (source community_flag), worked manually at launch.
func (h Handlers) PostSocialReport(w http.ResponseWriter, r *http.Request) {
	req := &pb.ReportRequest{}
	if err := bindProto(r, req); err != nil || !uuidPattern.MatchString(req.TargetUserId) {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	target, err := h.Queries.GetUserByUUID(r.Context(), strings.ToLower(req.TargetUserId))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeError(w, r, err)
		return
	}
	if target.ID == user.ID {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "cannot target yourself")
		return
	}

	context := req.Context
	if len(context) > maxReportContextLen {
		context = context[:maxReportContextLen]
	}

	err = h.Queries.InsertModerationReport(r.Context(), db.InsertModerationReportParams{
		TargetUserID:   target.ID,
		ReporterUserID: &user.ID,
		Source:         "community_flag",
		Reason:         int16(req.Reason),
		Context:        context,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}

	writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
}

// PostSocialErase handles POST /social/erase: GDPR erasure of the social
// profile. Idempotent — erasing a never-joined account succeeds.
func (h Handlers) PostSocialErase(w http.ResponseWriter, r *http.Request) {
	req := &pb.EraseRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	if err := h.socialErase(r, user.ID); err != nil {
		writeError(w, r, err)
		return
	}

	writeProto(w, http.StatusOK, &pb.SocialAck{Success: true})
}

// socialErase deletes the profile row (PII), tombstones the handle — the
// string survives forever as a non-PII reservation with the account link
// nulled (ADR-0005) — and drops every block/mute edge touching the user.
// Also invoked from account deletion (PostDeleteAccount).
func (h Handlers) socialErase(r *http.Request, userID int64) error {
	return h.Queries.InTx(r.Context(), func(q db.Querier) error {
		if _, err := q.DeleteSocialProfile(r.Context(), userID); err != nil {
			return err
		}
		if _, err := q.TombstoneHandle(r.Context(), &userID); err != nil {
			return err
		}
		return q.DeleteRelationshipsForUser(r.Context(), userID)
	})
}

// visibilityToStored maps a wire SocialVisibility onto the stored raw value,
// folding UNSPECIFIED/unknown to private (fail toward hidden, ADR-0006).
func visibilityToStored(v pb.SocialVisibility) int16 {
	switch v {
	case pb.SocialVisibility_SOCIAL_VISIBILITY_PUBLIC,
		pb.SocialVisibility_SOCIAL_VISIBILITY_FOLLOWERS_ONLY:
		return int16(v)
	default:
		return int16(pb.SocialVisibility_SOCIAL_VISIBILITY_PRIVATE)
	}
}

func profileToProto(row db.SocialProfile, userUuid string) *pb.SocialProfile {
	return &pb.SocialProfile{
		UserId:                  userUuid,
		Handle:                  row.Handle,
		DisplayName:             row.DisplayName,
		Bio:                     row.Bio,
		CreatedAt:               timestamppb.New(row.CreatedAt),
		TermsVersion:            row.TermsVersion,
		AvatarVisibility:        pb.SocialVisibility(row.AvatarVisibility),
		BioVisibility:           pb.SocialVisibility(row.BioVisibility),
		FollowedShowsVisibility: pb.SocialVisibility(row.FollowedShowsVisibility),
		TopPodcastsVisibility:   pb.SocialVisibility(row.TopPodcastsVisibility),
		StatsVisibility:         pb.SocialVisibility(row.StatsVisibility),
		HistoryVisibility:       pb.SocialVisibility(row.HistoryVisibility),
		PresenceVisibility:      pb.SocialVisibility(row.PresenceVisibility),
	}
}
