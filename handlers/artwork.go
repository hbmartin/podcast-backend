package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/hbmartin/podcast-backend/artwork"
	"github.com/hbmartin/podcast-backend/db"

	"github.com/jackc/pgx/v5"
)

// artworkSizes are the thumbnail sizes the client requests
// (DiscoverServerHandler.thumbnailUrlString).
var artworkSizes = map[string]bool{
	"130": true, "140": true, "200": true, "210": true, "280": true,
	"340": true, "400": true, "420": true, "680": true, "960": true,
}

// GetDiscoverImage handles GET /discover/images/{size}/{uuid}.jpg — the URL
// Kingfisher loads all podcast artwork from. Redirects to the feed's cover
// image; sizes are accepted but not resized server-side.
func (h Handlers) GetDiscoverImage(w http.ResponseWriter, r *http.Request) {
	if !artworkSizes[r.PathValue("size")] {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	uuid := strings.TrimSuffix(r.PathValue("file"), ".jpg")
	if !uuidPattern.MatchString(uuid) {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	podcast, err := h.Queries.GetPodcastByUUID(r.Context(), uuid)
	if err != nil || podcast.ImageUrl == "" {
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		return
	}

	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.Redirect(w, r, podcast.ImageUrl, http.StatusFound)
}

// GetDiscoverImageMetadata handles GET /discover/images/metadata/{uuid}.json:
// theme colors derived from the podcast's artwork, computed lazily and cached
// on the podcast row keyed to the artwork URL.
func (h Handlers) GetDiscoverImageMetadata(w http.ResponseWriter, r *http.Request) {
	uuid := strings.TrimSuffix(r.PathValue("file"), ".json")
	if !uuidPattern.MatchString(uuid) {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	podcast, err := h.Queries.GetPodcastByUUID(r.Context(), uuid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeError(w, r, err)
		return
	}

	colors := artwork.Colors{
		Background:     podcast.BackgroundColor,
		TintForLightBg: podcast.TintForLightBg,
		TintForDarkBg:  podcast.TintForDarkBg,
	}

	if podcast.BackgroundColor == "" || podcast.ColorsSourceImageUrl != podcast.ImageUrl {
		colors = h.computeAndCacheColors(r, podcast)
	}

	w.Header().Set("Cache-Control", "public, max-age=86400")
	writeJSON(w, http.StatusOK, map[string]any{
		"colors": map[string]string{
			"background":     colors.Background,
			"tintForLightBg": colors.TintForLightBg,
			"tintForDarkBg":  colors.TintForDarkBg,
		},
	})
}

func (h Handlers) computeAndCacheColors(r *http.Request, podcast db.Podcast) artwork.Colors {
	colors := artwork.FallbackColors

	if podcast.ImageUrl != "" && h.Images != nil {
		if img, err := h.Images.FetchImage(r.Context(), podcast.ImageUrl); err == nil {
			colors = artwork.ExtractColors(img)
		} else {
			slog.Warn("Artwork color extraction failed", "podcast", podcast.Uuid, "error", err)
		}
	}

	// persist even the fallback (keyed to the current image URL) so dead
	// artwork CDNs aren't re-fetched on every request; a future image_url
	// change re-triggers the computation
	err := h.Queries.UpdatePodcastColors(r.Context(), db.UpdatePodcastColorsParams{
		ID:                   podcast.ID,
		BackgroundColor:      colors.Background,
		TintForLightBg:       colors.TintForLightBg,
		TintForDarkBg:        colors.TintForDarkBg,
		ColorsSourceImageUrl: podcast.ImageUrl,
	})
	if err != nil {
		slog.Warn("Unable to cache artwork colors", "podcast", podcast.Uuid, "error", err)
	}

	return colors
}
