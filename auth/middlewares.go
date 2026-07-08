package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

type key int

// UserKey is the request-context key under which TokenAuthMiddleware stores
// the authenticated *User.
const UserKey key = 1

var allowedScopes = map[string]bool{"mobile": true, "tv": true, "sonos": true}

// TokenAuthMiddleware requires a valid Bearer access token. On success the
// authenticated *User is stored in the request context; on failure it replies
// 401. The Pocket Casts client inspects only the status code (TokenHelper
// retries once on 401, then deauths), so the body stays empty.
func TokenAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, err := userFromRequest(r)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="pocketcasts"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), UserKey, user)))
	})
}

// OptionalTokenMiddleware parses a Bearer token when present but lets the
// request through anonymously otherwise. Used by endpoints that serve both
// signed-in and signed-out clients (e.g. refresh, public cache lookups).
func OptionalTokenMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, err := userFromRequest(r); err == nil {
			r = r.WithContext(context.WithValue(r.Context(), UserKey, user))
		}
		next.ServeHTTP(w, r)
	})
}

func userFromRequest(r *http.Request) (*User, error) {
	token, err := extractToken(r)
	if err != nil {
		return nil, err
	}

	user, err := ValidateAccessToken(token)
	if err != nil {
		return nil, err
	}

	if !allowedScopes[user.Scope] {
		return nil, errInvalidScope
	}

	return user, nil
}

var (
	errInvalidScope = errors.New("token scope is not allowed")
	errNoToken      = errors.New("no bearer token provided")
)

func extractToken(r *http.Request) (string, error) {
	header := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", errNoToken
	}
	return header[len(prefix):], nil
}
