package handlers

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/hbmartin/podcast-backend/attest"
	"github.com/hbmartin/podcast-backend/db"
)

const (
	mwKeyID = "test-key"
)

type mwFakeVerifier struct{ expected []byte }

func (v *mwFakeVerifier) VerifyAttestation(_, _, _ []byte) ([]byte, []byte, string, error) {
	return nil, nil, "", errors.New("not used")
}

func (v *mwFakeVerifier) VerifyAssertion(_, _, clientData []byte) (uint32, error) {
	if !slices.Equal(v.expected, clientData) {
		return 0, attest.ErrInvalidAttestation
	}
	return 1, nil
}

// mwFakeStore implements just the attest queries AttestVerify calls.
type mwFakeStore struct {
	db.Store
	missing bool
	status  string
	pub     []byte
	counter int64
}

func (f *mwFakeStore) GetAttestKey(_ context.Context, keyID string) (db.AttestKey, error) {
	if f.missing {
		return db.AttestKey{}, pgx.ErrNoRows
	}
	return db.AttestKey{KeyID: keyID, PublicKey: f.pub, Counter: f.counter, Status: f.status}, nil
}

func (f *mwFakeStore) AdvanceAttestCounter(_ context.Context, arg db.AdvanceAttestCounterParams) (int64, error) {
	if f.status == "active" && arg.Counter > f.counter {
		f.counter = arg.Counter
		return 1, nil
	}
	return 0, nil
}

func mwHandlers(t *testing.T, store db.Store) Handlers {
	t.Helper()
	req := mwRequest(t, signedBody(t), false)
	expected, err := attest.CanonicalRequest(req, signedBody(t))
	if err != nil {
		t.Fatal(err)
	}
	return Handlers{Queries: store, AttestVerifier: &mwFakeVerifier{expected: expected}}
}

func mwRequest(t *testing.T, body []byte, withHeaders bool) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/transcripts/contribute", strings.NewReader(string(body)))
	if withHeaders {
		req.Header.Set(attest.HeaderKeyID, mwKeyID)
		req.Header.Set(attest.HeaderAssertion, base64.StdEncoding.EncodeToString([]byte("assertion")))
	}
	return req
}

func signedBody(t *testing.T) []byte {
	t.Helper()
	return []byte(`{"challenge":"assertion-test"}`)
}

func newActiveStore() *mwFakeStore {
	return &mwFakeStore{status: "active", pub: []byte("public-key")}
}

func run(t *testing.T, h Handlers, mode attest.Mode, req *http.Request) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	called := false
	next := func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(http.StatusOK) }
	rec := httptest.NewRecorder()
	h.AttestVerify(mode, MaxContributeBody, "test", next).ServeHTTP(rec, req)
	return rec, called
}

func TestAttestVerify_ValidAssertionRequired(t *testing.T) {
	h := mwHandlers(t, newActiveStore())
	rec, called := run(t, h, attest.ModeRequired, mwRequest(t, signedBody(t), true))
	if !called || rec.Code != http.StatusOK {
		t.Fatalf("valid assertion should pass: called=%v code=%d", called, rec.Code)
	}
}

func TestAttestVerify_TamperedBodyRejected(t *testing.T) {
	h := mwHandlers(t, newActiveStore())
	rec, called := run(t, h, attest.ModeRequired, mwRequest(t, []byte("tampered"), true))
	if called || rec.Code != http.StatusUnauthorized {
		t.Fatalf("tampered body should 401: called=%v code=%d", called, rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid_attestation") {
		t.Fatalf("expected invalid_attestation envelope, got %s", rec.Body.String())
	}
}

func TestAttestVerify_UnattestedRequired(t *testing.T) {
	h := mwHandlers(t, newActiveStore())
	rec, called := run(t, h, attest.ModeRequired, mwRequest(t, signedBody(t), false))
	if called || rec.Code != http.StatusUnauthorized {
		t.Fatalf("unattested+required should 401: called=%v code=%d", called, rec.Code)
	}
}

func TestAttestVerify_UnattestedLogOnlyAllowed(t *testing.T) {
	h := mwHandlers(t, newActiveStore())
	rec, called := run(t, h, attest.ModeLogOnly, mwRequest(t, signedBody(t), false))
	if !called || rec.Code != http.StatusOK {
		t.Fatalf("unattested+log-only should pass: called=%v code=%d", called, rec.Code)
	}
}

func TestAttestVerify_OversizedHeaderHonorsMode(t *testing.T) {
	tests := []struct {
		name       string
		mode       attest.Mode
		wantCalled bool
		wantCode   int
	}{
		{name: "required", mode: attest.ModeRequired, wantCode: http.StatusRequestHeaderFieldsTooLarge},
		{name: "log-only", mode: attest.ModeLogOnly, wantCalled: true, wantCode: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := mwHandlers(t, newActiveStore())
			req := mwRequest(t, signedBody(t), true)
			req.Header.Set(attest.HeaderAssertion, strings.Repeat("a", maxAssertionB64Len+1))

			rec, called := run(t, h, tt.mode, req)
			if called != tt.wantCalled || rec.Code != tt.wantCode {
				t.Fatalf("oversized header: called=%v code=%d, want called=%v code=%d", called, rec.Code, tt.wantCalled, tt.wantCode)
			}
		})
	}
}

func TestAttestVerify_ReplayIsStale(t *testing.T) {
	store := newActiveStore()
	h := mwHandlers(t, store)
	// First use advances the counter; a replay of the same assertion is stale.
	if _, called := run(t, h, attest.ModeRequired, mwRequest(t, signedBody(t), true)); !called {
		t.Fatal("first assertion should pass")
	}
	rec, called := run(t, h, attest.ModeRequired, mwRequest(t, signedBody(t), true))
	if called || rec.Code != http.StatusConflict {
		t.Fatalf("replay should 409 stale: called=%v code=%d", called, rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "stale_attestation") {
		t.Fatalf("expected stale_attestation envelope, got %s", rec.Body.String())
	}
}

func TestAttestVerify_UnknownKeyRejected(t *testing.T) {
	store := newActiveStore()
	store.missing = true
	h := mwHandlers(t, store)
	rec, called := run(t, h, attest.ModeRequired, mwRequest(t, signedBody(t), true))
	if called || rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown key should 401: called=%v code=%d", called, rec.Code)
	}
}

func TestAttestVerify_OversizeBody413(t *testing.T) {
	h := mwHandlers(t, newActiveStore())
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(strings.Repeat("a", 100)))
	called := false
	next := func(w http.ResponseWriter, r *http.Request) { called = true }
	rec := httptest.NewRecorder()
	h.AttestVerify(attest.ModeLogOnly, 10, "test", next).ServeHTTP(rec, req)
	if called || rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize should 413: called=%v code=%d", called, rec.Code)
	}
}
