package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func capabilitiesBody(t *testing.T, h Handlers, r *http.Request) (int, struct {
	ServerVersion string          `json:"serverVersion"`
	AppAttestMode string          `json:"appAttestMode"`
	Features      map[string]bool `json:"features"`
}) {
	t.Helper()
	recorder := httptest.NewRecorder()
	h.GetCapabilities(recorder, r)
	var body struct {
		ServerVersion string          `json:"serverVersion"`
		AppAttestMode string          `json:"appAttestMode"`
		Features      map[string]bool `json:"features"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return recorder.Code, body
}

func TestCapabilitiesReflectConfiguredFeatures(t *testing.T) {
	h := Handlers{
		ServerVersion: "2026.07.42", AppAttestMode: "log-only",
		AvatarEnabled: true, FoldersEnabled: false, CorpusEnabled: true,
	}
	code, body := capabilitiesBody(t, h, httptest.NewRequest(http.MethodGet, "/api/v1/capabilities", nil))
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if body.AppAttestMode != "log-only" || !body.Features["avatar"] || body.Features["folderSuggestions"] || !body.Features["corpus"] {
		t.Fatalf("unexpected manifest: %+v", body)
	}
	if body.ServerVersion != "" {
		t.Fatalf("serverVersion must not be disclosed to unattested requests, got %q", body.ServerVersion)
	}
}

func TestCapabilitiesDiscloseVersionToAttestedRequests(t *testing.T) {
	h := Handlers{ServerVersion: "2026.07.42", AppAttestMode: "required"}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/capabilities", nil)
	request = request.WithContext(context.WithValue(request.Context(), attestKeyIDCtx, "key-1"))
	code, body := capabilitiesBody(t, h, request)
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if body.ServerVersion != "2026.07.42" {
		t.Fatalf("attested request should receive serverVersion, got %q", body.ServerVersion)
	}
}
