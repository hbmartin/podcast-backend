package handlers

import (
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"

	"github.com/hbmartin/podcast-backend/db"

	"github.com/jackc/pgx/v5"
)

// PostShareList handles POST /share/list (sharing host role): create a
// shareable podcast list. The legacy SHA-1 signature is verified only when
// SHARING_CREDENTIAL is configured — protocol compatibility, not security.
func (h Handlers) PostShareList(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Podcasts    []struct {
			UUID string `json:"uuid"`
		} `json:"podcasts"`
		Datetime string `json:"datetime"`
		H        string `json:"h"`
	}
	if err := bindJSON(r, &req); err != nil {
		writeRefreshStatus(w, "error", "invalid request")
		return
	}

	if credential := h.Config.SharingCredential; credential != "" {
		sum := sha1.Sum([]byte(req.Datetime + credential))
		expected := hex.EncodeToString(sum[:])
		if subtle.ConstantTimeCompare([]byte(expected), []byte(req.H)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"status": "error", "message": "invalid signature"})
			return
		}
	}

	uuids := make([]string, 0, len(req.Podcasts))
	for _, podcast := range req.Podcasts {
		if uuidPattern.MatchString(podcast.UUID) {
			uuids = append(uuids, podcast.UUID)
		}
	}
	if len(uuids) == 0 {
		writeRefreshStatus(w, "error", "no valid podcasts to share")
		return
	}

	var list db.SharedList
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		list, err = h.Queries.CreateSharedList(r.Context(), db.CreateSharedListParams{
			Code:         shareCode(),
			Title:        req.Title,
			Description:  req.Description,
			PodcastUuids: uuids,
		})
		if err == nil {
			break
		}
	}
	if err != nil {
		writeError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"message": "",
		"result": map[string]string{
			"share_url": h.baseURL(r) + "/l/" + list.Code,
		},
	})
}

// shareCode returns a 10-character URL-safe random code.
func shareCode() string {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	raw := make([]byte, 10)
	if _, err := rand.Read(raw); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	for i, b := range raw {
		raw[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(raw)
}

// GetSharedList handles GET /l/{code}: the list JSON the client's
// SharingServerHandler.loadList decodes.
func (h Handlers) GetSharedList(w http.ResponseWriter, r *http.Request) {
	list, err := h.Queries.GetSharedListByCode(r.Context(), r.PathValue("code"))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeError(w, r, err)
		return
	}

	known := map[string]db.Podcast{}
	if len(list.PodcastUuids) > 0 {
		podcasts, err := h.Queries.GetPodcastsByUUIDs(r.Context(), list.PodcastUuids)
		if err != nil {
			writeError(w, r, err)
			return
		}
		for _, podcast := range podcasts {
			known[podcast.Uuid] = podcast
		}
	}

	items := make([]podcastInfoJSON, 0, len(list.PodcastUuids))
	for _, uuid := range list.PodcastUuids {
		podcast, ok := known[uuid]
		if !ok {
			continue
		}
		items = append(items, podcastInfoJSON{
			Title:       podcast.Title,
			UUID:        podcast.Uuid,
			Author:      podcast.Author,
			Description: podcast.Description,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"title":       list.Title,
		"description": list.Description,
		"podcasts":    items,
	})
}

// PostSharePodcast handles POST /podcast/{uuid} (refresh host role): resolve
// a shared podcast link to a ShareListResponse-compatible payload.
func (h Handlers) PostSharePodcast(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	if !uuidPattern.MatchString(uuid) {
		writeRefreshStatus(w, "error", "invalid podcast")
		return
	}

	podcast, ok := h.loadCrawledPodcast(w, r, uuid)
	if !ok {
		return
	}

	writeRefreshOK(w, map[string]any{
		"podcast": podcastInfoJSON{
			Title:       podcast.Title,
			UUID:        podcast.Uuid,
			Author:      podcast.Author,
			Description: podcast.Description,
			Explicit:    podcast.IsExplicit,
		},
	})
}

// PostShareEpisode handles POST /episode/{uuid}: resolve a shared episode
// link, including the podcast, the episode, and an optional timestamp.
func (h Handlers) PostShareEpisode(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	if !uuidPattern.MatchString(uuid) {
		writeRefreshStatus(w, "error", "invalid episode")
		return
	}

	episode, err := h.Queries.GetEpisodeByUUID(r.Context(), uuid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeError(w, r, err)
		return
	}

	podcast, err := h.Queries.GetPodcastByID(r.Context(), episode.PodcastID)
	if err != nil {
		writeError(w, r, err)
		return
	}

	episodes := episodesToRefreshJSON([]db.Episode{episode})
	result := map[string]any{
		"podcast": podcastInfoJSON{
			Title:       podcast.Title,
			UUID:        podcast.Uuid,
			Author:      podcast.Author,
			Description: podcast.Description,
			Explicit:    podcast.IsExplicit,
		},
		"shared_episode": episodes[0],
	}
	if t := r.URL.Query().Get("t"); t != "" {
		result["time"] = t
	}

	writeRefreshOK(w, result)
}
