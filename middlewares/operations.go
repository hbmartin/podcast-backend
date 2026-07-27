package middlewares

import (
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"
)

// BearerSecret protects operational endpoints without leaking whether a
// deployment omitted the secret. It intentionally does not share JWT auth.
func BearerSecret(secret string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if secret == "" {
			http.NotFound(w, r)
			return
		}
		const prefix = "Bearer "
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, prefix) || subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(header, prefix)), []byte(secret)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// CanonicalHost enforces PUBLIC_BASE_URL while leaving the provider's
// process-only liveness route usable before custom DNS is attached.
func CanonicalHost(publicBaseURL string, next http.Handler) http.Handler {
	if publicBaseURL == "" {
		return next
	}
	origin, err := url.Parse(publicBaseURL)
	if err != nil {
		return next // startup validation owns malformed configuration
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/livez" || strings.EqualFold(r.Host, origin.Host) {
			next.ServeHTTP(w, r)
			return
		}
		if (r.Method == http.MethodGet || r.Method == http.MethodHead) && isPublicHTMLPath(r.URL.Path) {
			target := *r.URL
			target.Scheme = origin.Scheme
			target.Host = origin.Host
			http.Redirect(w, r, target.String(), http.StatusPermanentRedirect)
			return
		}
		w.WriteHeader(http.StatusMisdirectedRequest)
	})
}

func isPublicHTMLPath(path string) bool {
	return path == "/health.html" ||
		path == "/apple-app-site-association" ||
		path == "/.well-known/apple-app-site-association" ||
		strings.HasPrefix(path, "/u/") ||
		strings.HasPrefix(path, "/podcast/") ||
		strings.HasPrefix(path, "/episode/")
}
