package handlers

import (
	"net/http"
)

// GetCapabilities is the attested, installation-scoped feature manifest. A
// disabled optional feature is deliberately indistinguishable from one whose
// credentials were only partially configured.
func (h Handlers) GetCapabilities(w http.ResponseWriter, _ *http.Request) {
	foldersAvailable := h.FoldersEnabled
	if h.FolderSuggester != nil {
		foldersAvailable = h.FolderSuggester.Available()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"serverVersion": h.ServerVersion,
		"appAttestMode": h.AppAttestMode,
		"features": map[string]bool{
			"avatar":            h.AvatarEnabled,
			"folderSuggestions": foldersAvailable,
			"corpus":            h.CorpusEnabled,
		},
	})
}
