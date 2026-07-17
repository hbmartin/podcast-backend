package handlers

import (
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

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

// Server-side caps on public-profile section sizes.
const (
	maxFollowedShows  = 50
	maxTopPodcasts    = 10
	maxRecentlyPlayed = 20
)

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

	resp, status, err := h.buildPublicProfile(r, req.Handle)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if status != http.StatusOK {
		w.WriteHeader(status)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

// buildPublicProfile resolves a handle to its visibility-filtered public
// profile for the requesting viewer. Returns (nil, 404, nil) for every kind of
// miss — unknown, tombstoned, or blocked-either-way — so callers can't leak
// which it was. Shared by the protobuf endpoint and the HTML page.
func (h Handlers) buildPublicProfile(r *http.Request, rawHandle string) (*pb.PublicProfileResponse, int, error) {
	handle := normalizeHandle(rawHandle)
	if !handlePattern.MatchString(handle) {
		return nil, http.StatusNotFound, nil
	}

	profile, err := h.Queries.GetSocialProfileByHandle(r.Context(), handle)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, http.StatusNotFound, nil
		}
		return nil, 0, err
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
					return nil, 0, err
				}
				if blocked {
					return nil, http.StatusNotFound, nil
				}
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, 0, err
		}
	}

	owner, err := h.Queries.GetUserByID(r.Context(), profile.UserID)
	if err != nil {
		return nil, 0, err
	}

	resp := &pb.PublicProfileResponse{
		UserId:      owner.Uuid,
		Handle:      profile.Handle,
		DisplayName: profile.DisplayName,
		CreatedAt:   timestamppb.New(profile.CreatedAt),
	}
	// Phase 1 exposes only the public tier to non-owners; followers-only
	// unlocks with the Phase-2 graph (ADR-0006). Owners see their own fields.
	visible := func(stored int16) bool {
		return isOwner || pb.SocialVisibility(stored) == pb.SocialVisibility_SOCIAL_VISIBILITY_PUBLIC
	}
	if visible(profile.BioVisibility) {
		resp.Bio = profile.Bio
	}
	// avatar_url stays empty: avatars are deferred from this slice.

	if visible(profile.FollowedShowsVisibility) {
		rows, err := h.Queries.GetPublicFollowedShows(r.Context(), db.GetPublicFollowedShowsParams{
			UserID: profile.UserID, Limit: maxFollowedShows,
		})
		if err != nil {
			return nil, 0, err
		}
		for _, row := range rows {
			resp.FollowedShows = append(resp.FollowedShows, &pb.SocialProfilePodcast{
				Uuid: row.PodcastUuid, Title: row.Title, Author: row.Author,
			})
		}
	}

	if visible(profile.TopPodcastsVisibility) {
		rows, err := h.Queries.GetPublicTopPodcasts(r.Context(), db.GetPublicTopPodcastsParams{
			UserID: profile.UserID, Limit: maxTopPodcasts,
		})
		if err != nil {
			return nil, 0, err
		}
		for _, row := range rows {
			resp.TopPodcasts = append(resp.TopPodcasts, &pb.SocialProfilePodcast{
				Uuid: row.PodcastUuid, Title: row.Title, Author: row.Author, PlayedSeconds: row.PlayedSeconds,
			})
		}
	}

	if resp.HasStats = visible(profile.StatsVisibility); resp.HasStats {
		totals, err := h.Queries.GetUserStatsTotals(r.Context(), profile.UserID)
		if err != nil {
			return nil, 0, err
		}
		since := totals.EarliestCreatedAt
		if totals.EarliestStartedAt > 0 {
			since = time.Unix(totals.EarliestStartedAt, 0)
		}
		resp.Stats = &pb.SocialProfileStats{
			TimeListenedSeconds: totals.TimeListened,
			ListeningSince:      timestamppb.New(since),
		}
	}

	if visible(profile.HistoryVisibility) {
		rows, err := h.Queries.GetPublicRecentlyPlayed(r.Context(), db.GetPublicRecentlyPlayedParams{
			UserID: profile.UserID, Limit: maxRecentlyPlayed,
		})
		if err != nil {
			return nil, 0, err
		}
		for _, row := range rows {
			resp.RecentlyPlayed = append(resp.RecentlyPlayed, &pb.SocialProfileEpisode{
				Uuid: row.EpisodeUuid, PodcastUuid: row.PodcastUuid, Title: row.Title,
				PlayedAt: timestamppb.New(time.UnixMilli(row.ModifiedAt)),
			})
		}
	}

	return resp, http.StatusOK, nil
}

// profilePageTemplate renders the minimal web Profile Link page
// (<PUBLIC_BASE_URL>/u/<handle>, ADR-0008 in the iOS repo). html/template
// escapes every interpolation.
var profilePageTemplate = template.Must(template.New("profile").Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>@{{.Handle}} — Pocket Casts</title>
<style>body{font-family:-apple-system,system-ui,sans-serif;max-width:40rem;margin:2rem auto;padding:0 1rem;color:#222}h1{margin-bottom:0}.handle{color:#666}.bio{margin:1rem 0}h2{font-size:1rem;margin-top:1.5rem}li{margin:.2rem 0}.muted{color:#888;font-size:.9rem}</style>
</head><body>
<h1>{{.DisplayName}}</h1>
<p class="handle">@{{.Handle}}</p>
{{if .Bio}}<p class="bio">{{.Bio}}</p>{{end}}
{{if .Stats}}<p class="muted">{{.HoursListened}} hours listened · listening since {{.Since}}</p>{{end}}
{{if .FollowedShows}}<h2>Followed shows</h2><ul>{{range .FollowedShows}}<li>{{.Title}}{{if .Author}} <span class="muted">— {{.Author}}</span>{{end}}</li>{{end}}</ul>{{end}}
{{if .TopPodcasts}}<h2>Top podcasts</h2><ul>{{range .TopPodcasts}}<li>{{.Title}}{{if .Author}} <span class="muted">— {{.Author}}</span>{{end}}</li>{{end}}</ul>{{end}}
{{if .RecentlyPlayed}}<h2>Recently played</h2><ul>{{range .RecentlyPlayed}}<li>{{.Title}}</li>{{end}}</ul>{{end}}
<p><a href="thcast://profile/{{.Handle}}">Open in app</a></p>
</body></html>
`))

// GetPublicProfilePage handles GET /u/{handle}: the anonymous web view of a
// public profile — the same visibility-filtered read rendered as minimal HTML.
func (h Handlers) GetPublicProfilePage(w http.ResponseWriter, r *http.Request) {
	resp, status, err := h.buildPublicProfile(r, r.PathValue("handle"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	if status != http.StatusOK {
		http.NotFound(w, r)
		return
	}

	data := struct {
		*pb.PublicProfileResponse
		HoursListened int64
		Since         string
	}{PublicProfileResponse: resp}
	if resp.Stats != nil {
		data.HoursListened = resp.Stats.TimeListenedSeconds / 3600
		data.Since = resp.Stats.ListeningSince.AsTime().Format("January 2006")
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if err := profilePageTemplate.Execute(w, data); err != nil {
		slog.Warn("Profile page render failed", "handle", resp.Handle, "error", err)
	}
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
	targetType := req.TargetType
	if targetType == "" {
		targetType = "user"
	}

	err = h.Queries.InsertModerationReport(r.Context(), db.InsertModerationReportParams{
		TargetUserID:   target.ID,
		ReporterUserID: &user.ID,
		Source:         "community_flag",
		Reason:         int16(req.Reason),
		Context:        context,
		TargetType:     targetType,
		ContentRef:     req.ContentRef,
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
// nulled (ADR-0005) — drops every block/mute edge touching the user, and
// deletes their attributed review text (which only exists because they
// joined). Account-level reactions survive until account hard-delete.
// Also invoked from account deletion (PostDeleteAccount).
func (h Handlers) socialErase(r *http.Request, userID int64) error {
	return h.Queries.InTx(r.Context(), func(q db.Querier) error {
		if _, err := q.DeleteSocialProfile(r.Context(), userID); err != nil {
			return err
		}
		if _, err := q.TombstoneHandle(r.Context(), &userID); err != nil {
			return err
		}
		if err := q.DeleteReviewsForUser(r.Context(), userID); err != nil {
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
