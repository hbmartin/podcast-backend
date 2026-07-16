package handlers

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/hbmartin/podcast-backend/attest"
	"github.com/hbmartin/podcast-backend/db"
)

// Real assertion vector (App ID 35MFYY2JY5.co.chiff.attestation-test); the
// signed clientData is {"challenge":"assertion-test"}.
const (
	mwAppID          = "35MFYY2JY5.co.chiff.attestation-test"
	mwKeyID          = "AcP/pnpoNVPIJYZOvmIvWzDvmxkFoQCE4Uu7Nk6WiAA="
	mwPubKeyHex      = "0437c404fa2bbf8fbcf4ee7080573d5fa80c4f6cc3a22f7db43af92c394e7cd1c880c95ab422972625e8e673af1bda2b096654e9b602895601f925bb5941c53082"
	mwAssertionB64U  = "omlzaWduYXR1cmVYRzBFAiEAyC5S3pcvtSpmTfNSd8aJRJCQ6PbN7Dnv_oPkZNMLeIwCIBmxCHXKYyGswzp_LwOxoL18puHooxudXWqDgtTvRomdcWF1dGhlbnRpY2F0b3JEYXRhWCV87ytV2nJBCLqRJ5b2df8AvnHVLa4mj6aI00ym0n9wdEAAAAAD"
	mwSignedBodyB64U = "eyJjaGFsbGVuZ2UiOiJhc3NlcnRpb24tdGVzdCJ9"
)

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
	v, err := attest.NewVerifier(mwAppID, true)
	if err != nil {
		t.Fatal(err)
	}
	return Handlers{Queries: store, AttestVerifier: v}
}

func mwRequest(t *testing.T, body []byte, withHeaders bool) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/transcripts/contribute", strings.NewReader(string(body)))
	if withHeaders {
		cbor, _ := base64.RawURLEncoding.DecodeString(mwAssertionB64U)
		req.Header.Set(attest.HeaderKeyID, mwKeyID)
		req.Header.Set(attest.HeaderAssertion, base64.StdEncoding.EncodeToString(cbor))
	}
	return req
}

func signedBody(t *testing.T) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(mwSignedBodyB64U)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newActiveStore() *mwFakeStore {
	pub, _ := hex.DecodeString(mwPubKeyHex)
	return &mwFakeStore{status: "active", pub: pub}
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
