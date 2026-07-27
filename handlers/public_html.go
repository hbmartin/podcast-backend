package handlers

import (
	"errors"
	"html/template"
	"net/http"

	"github.com/jackc/pgx/v5"
)

var publicItemTemplate = template.Must(template.New("item").Parse(`<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>{{.Title}}</title><meta name="description" content="{{.Description}}"><meta property="og:title" content="{{.Title}}"><meta property="og:description" content="{{.Description}}">{{if .Image}}<meta property="og:image" content="{{.Image}}">{{end}}<meta property="og:url" content="{{.URL}}"><meta property="og:type" content="{{.Type}}"></head><body><main><h1>{{.Title}}</h1>{{if .Author}}<p>By {{.Author}}</p>{{end}}<p>{{.Description}}</p><p><a href="pktc://{{.Kind}}/{{.UUID}}">Open in Pocket Casts</a></p></main></body></html>`))

func (h Handlers) GetPodcastPage(w http.ResponseWriter, r *http.Request) {
	podcast, err := h.Queries.GetPodcastByUUID(r.Context(), r.PathValue("uuid"))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.NotFound(w, r)
		} else {
			writeError(w, r, err)
		}
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = publicItemTemplate.Execute(w, map[string]any{"Title": podcast.Title, "Description": podcast.Description, "Author": podcast.Author, "Image": podcast.ImageUrl, "URL": h.baseURL(r) + "/podcast/" + podcast.Uuid, "Type": "website", "Kind": "podcast", "UUID": podcast.Uuid})
}

func (h Handlers) GetEpisodePage(w http.ResponseWriter, r *http.Request) {
	episode, err := h.Queries.GetEpisodeByUUID(r.Context(), r.PathValue("uuid"))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.NotFound(w, r)
		} else {
			writeError(w, r, err)
		}
		return
	}
	podcast, err := h.Queries.GetPodcastByID(r.Context(), episode.PodcastID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = publicItemTemplate.Execute(w, map[string]any{"Title": episode.Title, "Description": episode.ShowNotes, "Author": podcast.Title, "Image": episode.ImageUrl, "URL": h.baseURL(r) + "/episode/" + episode.Uuid, "Type": "article", "Kind": "episode", "UUID": episode.Uuid})
}

func (h Handlers) GetAASA(w http.ResponseWriter, r *http.Request) {
	if h.AssociatedAppID == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, http.StatusOK, map[string]any{"applinks": map[string]any{"apps": []string{}, "details": []any{map[string]any{"appID": h.AssociatedAppID, "paths": []string{"/l/*", "/podcast/*", "/episode/*", "/u/*"}}}}})
}
