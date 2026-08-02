package handlers

import (
	"net/http"
)

// GetCapabilities is the feature manifest. Feature flags and the attest mode
// are public bootstrap data; the exact server version is disclosed only to
// requests carrying a verified App Attest assertion, so anonymous scans
// cannot fingerprint the deployed build. A disabled optional feature is
// deliberately indistinguishable from one whose credentials were only
// partially configured.
func (h Handlers) GetCapabilities(w http.ResponseWriter, r *http.Request) {
	foldersAvailable := h.FoldersEnabled
	if h.FolderSuggester != nil {
		foldersAvailable = h.FolderSuggester.Available()
	}
	payload := map[string]any{
		"appAttestMode": h.AppAttestMode,
		"features": map[string]bool{
			"avatar":            h.AvatarEnabled,
			"folderSuggestions": foldersAvailable,
			"corpus":            h.CorpusEnabled,
			"person_follows":    true,
		},
	}
	if keyID, ok := r.Context().Value(attestKeyIDCtx).(string); ok && keyID != "" {
		payload["serverVersion"] = h.ServerVersion
	}
	writeJSON(w, http.StatusOK, payload)
}
