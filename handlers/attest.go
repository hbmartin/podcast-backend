package handlers

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"crypto/rand"

	"github.com/hbmartin/podcast-backend/attest"
	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/metrics"
	"github.com/hbmartin/podcast-backend/pcerrors"

	"github.com/jackc/pgx/v5"
)

// App Attest bootstrap endpoints (docs/AppAttest.md §1.2, §4).
const (
	attestChallengeBytes = 32
	attestChallengeTTL   = 5 * time.Minute
	maxEnrollBody        = 64 << 10 // 64 KiB (docs/AppAttest.md §4)
	maxKeyIDLen          = 256
	maxAssertionB64Len   = 16 << 10
)

// ctxKey namespaces request-context values this package sets.
type ctxKey string

// attestKeyIDCtx holds the enrolled App Attest key id after a verified
// assertion, used for install attribution when no account is present.
const attestKeyIDCtx ctxKey = "attestKeyID"

func attestKeyID(r *http.Request) string {
	if v := r.Context().Value(attestKeyIDCtx); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// GetAttestChallenge issues a single-use, short-TTL challenge (docs/AppAttest.md
// §1.2). Returns 404 when App Attest is not configured, so the always-on client
// simply sends subsequent requests unattested.
func (h Handlers) GetAttestChallenge(w http.ResponseWriter, r *http.Request) {
	if h.AttestVerifier == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	ctx := r.Context()
	// Opportunistic sweep of expired challenges (indexed by expires_at).
	_ = h.Queries.DeleteExpiredChallenges(ctx)

	buf := make([]byte, attestChallengeBytes)
	if _, err := rand.Read(buf); err != nil {
		writeError(w, r, err)
		return
	}
	if err := h.Queries.InsertChallenge(ctx, db.InsertChallengeParams{
		Challenge: buf,
		ExpiresAt: time.Now().Add(attestChallengeTTL),
	}); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"challenge": base64.StdEncoding.EncodeToString(buf)})
}

type attestEnrollRequest struct {
	KeyID       string `json:"key_id"`
	Attestation string `json:"attestation"`
	Challenge   string `json:"challenge"`
}

// PostAttestEnroll verifies an attestation object and persists the key
// (docs/AppAttest.md §1.2, §2.1). 200 on success; 400 for bad/rejected material
// (the client discards its key and re-enrolls once); 5xx for verifier-internal
// faults — never 401, or the client would discard a healthy key.
func (h Handlers) PostAttestEnroll(w http.ResponseWriter, r *http.Request) {
	if h.AttestVerifier == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	ctx := r.Context()

	var req attestEnrollRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxEnrollBody))
	if err := dec.Decode(&req); err != nil {
		metrics.AttestEnrollments.WithLabelValues("rejected").Inc()
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	challenge, err1 := base64.StdEncoding.DecodeString(req.Challenge)
	keyID, err2 := base64.StdEncoding.DecodeString(req.KeyID)
	attestation, err3 := base64.StdEncoding.DecodeString(req.Attestation)
	if err1 != nil || err2 != nil || err3 != nil || len(req.KeyID) > maxKeyIDLen {
		metrics.AttestEnrollments.WithLabelValues("rejected").Inc()
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid encoding")
		return
	}

	// Burn the challenge on first presentation; no row means unknown or expired.
	if _, err := h.Queries.ConsumeChallenge(ctx, challenge); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			metrics.AttestEnrollments.WithLabelValues("rejected").Inc()
			pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "unknown or expired challenge")
			return
		}
		metrics.AttestEnrollments.WithLabelValues("error").Inc()
		writeError(w, r, err)
		return
	}

	pub, receipt, env, err := h.AttestVerifier.VerifyAttestation(challenge, attestation, keyID)
	if err != nil {
		if errors.Is(err, attest.ErrInvalidAttestation) {
			metrics.AttestEnrollments.WithLabelValues("rejected").Inc()
			slog.Info("App Attest enrollment rejected", "error", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		metrics.AttestEnrollments.WithLabelValues("error").Inc()
		writeError(w, r, err)
		return
	}

	if err := h.Queries.InsertAttestKey(ctx, db.InsertAttestKeyParams{
		KeyID:       req.KeyID,
		PublicKey:   pub,
		Receipt:     receipt,
		Environment: env,
	}); err != nil {
		metrics.AttestEnrollments.WithLabelValues("error").Inc()
		writeError(w, r, err)
		return
	}

	metrics.AttestEnrollments.WithLabelValues("enrolled").Inc()
	w.WriteHeader(http.StatusOK)
}
