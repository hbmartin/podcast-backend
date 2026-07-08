package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"

	"github.com/hbmartin/podcast-backend/errs"
)

// NewRefreshToken generates an opaque refresh token and the sha256 hex hash
// under which it is stored. The raw token is returned to the client once and
// never persisted.
func NewRefreshToken() (token string, hash string, err error) {
	const op errs.Op = "auth/NewRefreshToken"

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", errs.E(op, errs.Internal, err)
	}

	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, HashRefreshToken(token), nil
}

// HashRefreshToken returns the sha256 hex digest used as the storage key for
// a refresh token.
func HashRefreshToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
