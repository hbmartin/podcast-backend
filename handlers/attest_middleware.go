package handlers

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"

	"github.com/hbmartin/podcast-backend/attest"
	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/metrics"
	"github.com/hbmartin/podcast-backend/pcerrors"
)

// AttestVerify wraps a handler with App Attest assertion verification
// (docs/AppAttest.md §2.2, §2.3). It always caps and buffers the request body to
// maxBody (the assertion signs the exact wire bytes, and the handler re-reads
// the buffer) and then applies the endpoint's enforcement mode:
//
//   - ModeOff / no verifier configured: pass through.
//   - ModeLogOnly: verify a present assertion (logging failures) but accept
//     unattested requests, so Simulator/dev/older builds keep working.
//   - ModeRequired: reject unattested (401 invalid_attestation), invalid
//     signatures/keys (401 invalid_attestation), and non-increasing counters
//     (409 stale_attestation).
//
// On a verified assertion the enrolled key id is stored in the request context
// for install attribution. Verifier-internal faults return 5xx, never 401.
func (h Handlers) AttestVerify(mode attest.Mode, maxBody int64, endpoint string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, ok := readCappedBody(w, r, maxBody)
		if !ok {
			return
		}
		if h.AttestVerifier == nil || mode == attest.ModeOff {
			next(w, r)
			return
		}
		ctx := r.Context()

		keyID := r.Header.Get(attest.HeaderKeyID)
		assertionB64 := r.Header.Get(attest.HeaderAssertion)

		// deny records the verdict and either blocks (required) or logs and
		// allows the request (log-only).
		deny := func(outcome, envelopeID string, status int) {
			metrics.AttestAssertions.WithLabelValues(endpoint, outcome).Inc()
			if mode == attest.ModeRequired {
				pcerrors.Write(w, status, envelopeID, "")
				return
			}
			slog.Info("App Attest log-only: accepted despite verdict", "endpoint", endpoint, "outcome", outcome)
			next(w, r)
		}

		if keyID == "" || assertionB64 == "" {
			metrics.AttestAssertions.WithLabelValues(endpoint, "unattested").Inc()
			if mode == attest.ModeRequired {
				pcerrors.Write(w, http.StatusUnauthorized, pcerrors.InvalidAttestation, "")
				return
			}
			next(w, r)
			return
		}
		if len(keyID) > maxKeyIDLen || len(assertionB64) > maxAssertionB64Len {
			w.WriteHeader(http.StatusRequestHeaderFieldsTooLarge)
			return
		}
		assertion, err := base64.StdEncoding.DecodeString(assertionB64)
		if err != nil {
			deny("invalid_key", pcerrors.InvalidAttestation, http.StatusUnauthorized)
			return
		}

		key, err := h.Queries.GetAttestKey(ctx, keyID)
		if err != nil || key.Status != "active" {
			deny("invalid_key", pcerrors.InvalidAttestation, http.StatusUnauthorized)
			return
		}
		counter, verr := h.AttestVerifier.VerifyAssertion(key.PublicKey, assertion, body)
		if verr != nil {
			if errors.Is(verr, attest.ErrInvalidAttestation) {
				deny("bad_signature", pcerrors.InvalidAttestation, http.StatusUnauthorized)
				return
			}
			metrics.AttestAssertions.WithLabelValues(endpoint, "error").Inc()
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		// Atomic monotonic counter update (docs/AppAttest.md §2.2 step 3).
		rows, aerr := h.Queries.AdvanceAttestCounter(ctx, db.AdvanceAttestCounterParams{
			KeyID:   keyID,
			Counter: int64(counter),
		})
		if aerr != nil {
			metrics.AttestAssertions.WithLabelValues(endpoint, "error").Inc()
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if rows == 0 {
			// Distinguish a revoked/unknown key from a merely non-increasing
			// counter (out-of-order concurrent requests).
			if k2, e2 := h.Queries.GetAttestKey(ctx, keyID); e2 != nil || k2.Status != "active" {
				deny("invalid_key", pcerrors.InvalidAttestation, http.StatusUnauthorized)
				return
			}
			deny("stale", pcerrors.StaleAttestation, http.StatusConflict)
			return
		}

		metrics.AttestAssertions.WithLabelValues(endpoint, "ok").Inc()
		next(w, r.WithContext(context.WithValue(ctx, attestKeyIDCtx, keyID)))
	}
}
