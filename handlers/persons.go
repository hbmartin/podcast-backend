package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/hbmartin/podcast-backend/crawler"
	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/pcerrors"
	pb "github.com/hbmartin/podcast-backend/protos/api"

	"github.com/jackc/pgx/v5"
)

// Person index + follows (Highlights B2, ADR-0017). A person follow is
// PRIVATE account state — it requires a signed-in account but deliberately
// not a joined social profile: the follow's only effect is the push when the
// person appears on a newly ingested episode.

const maxPersonSearchResults = 25
const maxPersonAppearances = 100

func personSummary(p db.Person) *pb.PersonSummary {
	return &pb.PersonSummary{Id: p.ID, DisplayName: p.DisplayName}
}

// PostPersonGet returns the person page: identity, appearances, follow state.
func (h Handlers) PostPersonGet(w http.ResponseWriter, r *http.Request) {
	req := &pb.PersonGetRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	person, err := h.Queries.GetPerson(r.Context(), req.Id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			w.WriteHeader(http.StatusNotFound)
		} else {
			writeError(w, r, err)
		}
		return
	}

	appearances, err := h.Queries.GetPersonAppearances(r.Context(), db.GetPersonAppearancesParams{
		PersonID: person.ID, Limit: maxPersonAppearances,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}

	followed, err := h.Queries.GetFollowedPersons(r.Context(), user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	following := false
	for _, f := range followed {
		if f.ID == person.ID {
			following = true
			break
		}
	}

	response := &pb.PersonGetResponse{Person: personSummary(person), Following: following}
	for _, appearance := range appearances {
		response.Appearances = append(response.Appearances, &pb.PersonAppearance{
			PodcastUuid: appearance.PodcastUuid,
			EpisodeUuid: appearance.EpisodeUuid,
			Role:        appearance.Role,
		})
	}
	writeProto(w, http.StatusOK, response)
}

// PostPersonSearch: folded-alias prefix search (the same folding rule the
// clients use, so a search for "José" finds "jose …" aliases).
func (h Handlers) PostPersonSearch(w http.ResponseWriter, r *http.Request) {
	req := &pb.PersonSearchRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	if _, ok := h.currentDbUser(w, r); !ok {
		return
	}

	query := crawler.FoldPersonName(req.Query)
	if strings.TrimSpace(query) == "" {
		writeProto(w, http.StatusOK, &pb.PersonListResponse{})
		return
	}

	persons, err := h.Queries.SearchPersons(r.Context(), db.SearchPersonsParams{
		Column1: &query, Limit: maxPersonSearchResults,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}

	response := &pb.PersonListResponse{}
	for _, person := range persons {
		response.Persons = append(response.Persons, personSummary(person))
	}
	writeProto(w, http.StatusOK, response)
}

// PostPersonFollow / PostPersonUnfollow: idempotent private follow edges.
func (h Handlers) PostPersonFollow(w http.ResponseWriter, r *http.Request) {
	req := &pb.PersonFollowRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}
	if _, err := h.Queries.GetPerson(r.Context(), req.PersonId); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			w.WriteHeader(http.StatusNotFound)
		} else {
			writeError(w, r, err)
		}
		return
	}
	if err := h.Queries.FollowPerson(r.Context(), db.FollowPersonParams{
		UserID: user.ID, PersonID: req.PersonId,
	}); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h Handlers) PostPersonUnfollow(w http.ResponseWriter, r *http.Request) {
	req := &pb.PersonFollowRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}
	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}
	if err := h.Queries.UnfollowPerson(r.Context(), db.UnfollowPersonParams{
		UserID: user.ID, PersonID: req.PersonId,
	}); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// PostPersonFollows lists the caller's followed persons.
func (h Handlers) PostPersonFollows(w http.ResponseWriter, r *http.Request) {
	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}
	followed, err := h.Queries.GetFollowedPersons(r.Context(), user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	response := &pb.PersonListResponse{}
	for _, person := range followed {
		response.Persons = append(response.Persons, personSummary(person))
	}
	writeProto(w, http.StatusOK, response)
}
