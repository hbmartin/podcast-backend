package handlers

import (
	"net/http"

	"github.com/hbmartin/podcast-backend/pcerrors"
	pb "github.com/hbmartin/podcast-backend/protos/api"
)

// PostLastSyncAt handles POST user/last_sync_at.
func (h Handlers) PostLastSyncAt(w http.ResponseWriter, r *http.Request) {
	req := &pb.EmptyRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	resp, err := h.engine().LastSyncAt(r.Context(), user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

// PostUserPodcastList handles POST user/podcast/list.
func (h Handlers) PostUserPodcastList(w http.ResponseWriter, r *http.Request) {
	req := &pb.UserPodcastListRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	resp, err := h.engine().PodcastList(r.Context(), user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

// PostUserPodcastEpisodes handles POST user/podcast/episodes.
func (h Handlers) PostUserPodcastEpisodes(w http.ResponseWriter, r *http.Request) {
	req := &pb.UuidRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	resp, err := h.engine().PodcastEpisodes(r.Context(), user.ID, req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

// PostUserPlaylistList handles POST user/playlist/list.
func (h Handlers) PostUserPlaylistList(w http.ResponseWriter, r *http.Request) {
	req := &pb.UserPlaylistListRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	resp, err := h.engine().PlaylistList(r.Context(), user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

// PostUserBookmarkList handles POST user/bookmark/list.
func (h Handlers) PostUserBookmarkList(w http.ResponseWriter, r *http.Request) {
	req := &pb.BookmarkRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	resp, err := h.engine().BookmarkList(r.Context(), user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

// PostStarredList handles POST starred/list.
func (h Handlers) PostStarredList(w http.ResponseWriter, r *http.Request) {
	req := &pb.EmptyRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	resp, err := h.engine().StarredList(r.Context(), user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}
