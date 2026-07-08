package handlers

import (
	"net/http"

	"github.com/hbmartin/podcast-backend/pcerrors"
	pb "github.com/hbmartin/podcast-backend/protos/api"
	"github.com/hbmartin/podcast-backend/syncsvc"
)

func (h Handlers) engine() *syncsvc.Engine {
	return &syncsvc.Engine{DB: h.Queries}
}

// PostSyncUpdate handles POST user/sync/update, the incremental sync endpoint.
func (h Handlers) PostSyncUpdate(w http.ResponseWriter, r *http.Request) {
	req := &pb.SyncUpdateRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	resp, err := h.engine().ApplyUpdate(r.Context(), user.ID, req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

// PostUpNextSync handles POST up_next/sync.
func (h Handlers) PostUpNextSync(w http.ResponseWriter, r *http.Request) {
	req := &pb.UpNextSyncRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	resp, err := h.engine().SyncUpNext(r.Context(), user.ID, req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

// PostHistorySync handles POST history/sync.
func (h Handlers) PostHistorySync(w http.ResponseWriter, r *http.Request) {
	req := &pb.HistorySyncRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	resp, err := h.engine().SyncHistory(r.Context(), user.ID, req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

// PostNamedSettingsUpdate handles POST user/named_settings/update.
func (h Handlers) PostNamedSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	req := &pb.NamedSettingsRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	resp, err := h.engine().UpdateSettings(r.Context(), user.ID, req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

// PostUpdateEpisode handles POST sync/update_episode (real-time playback
// position). The client ignores the response body on success.
func (h Handlers) PostUpdateEpisode(w http.ResponseWriter, r *http.Request) {
	req := &pb.UpdateEpisodeRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	if err := h.engine().UpdateEpisode(r.Context(), user.ID, req); err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, &pb.UpdateEpisodeResponse{})
}

// PostUpdateEpisodeStar handles POST sync/update_episode_star.
func (h Handlers) PostUpdateEpisodeStar(w http.ResponseWriter, r *http.Request) {
	req := &pb.UpdateEpisodeStarRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	if err := h.engine().UpdateEpisodeStar(r.Context(), user.ID, req); err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, &pb.UpdateEpisodeStarResponse{})
}
