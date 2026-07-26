package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCapabilitiesReflectConfiguredFeatures(t *testing.T) {
	h := Handlers{
		ServerVersion: "2026.07.42", AppAttestMode: "log-only",
		AvatarEnabled: true, FoldersEnabled: false, CorpusEnabled: true,
	}
	recorder := httptest.NewRecorder()
	h.GetCapabilities(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/capabilities", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	var body struct {
		ServerVersion string          `json:"serverVersion"`
		AppAttestMode string          `json:"appAttestMode"`
		Features      map[string]bool `json:"features"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ServerVersion != "2026.07.42" || body.AppAttestMode != "log-only" || !body.Features["avatar"] || body.Features["folderSuggestions"] || !body.Features["corpus"] {
		t.Fatalf("unexpected manifest: %+v", body)
	}
}
