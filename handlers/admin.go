package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hbmartin/podcast-backend/auth"
	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/middlewares"
	"github.com/jackc/pgx/v5"
)

const adminCookieName = "podcast_admin"

type adminCorpusStore interface {
	ListAdminCandidates(httpContext, int32) ([]db.AdminCandidate, error)
	ListAdminReleases(httpContext, int32) ([]db.AdminRelease, error)
	AdminPromote(httpContext, db.AdminPromoteParams) (string, error)
	AdminRejectCandidate(httpContext, string, string, string) error
	AdminCreateDerivedCandidate(httpContext, db.AdminDerivedCandidateParams) (string, error)
	AdminRollbackRelease(httpContext, string, string) error
	AdminAudit(httpContext, string, string, []byte) error
}

// Alias keeps the admin store contract compact while remaining context.Context.
type httpContext = context.Context

type adminSession struct {
	CSRF      string    `json:"csrf"`
	CreatedAt time.Time `json:"createdAt"`
	LastSeen  time.Time `json:"lastSeen"`
}

func (h Handlers) GetAdmin(w http.ResponseWriter, r *http.Request) {
	h.adminHeaders(w)
	if h.AdminToken == "" {
		http.NotFound(w, r)
		return
	}
	sessionID, session, ok := h.adminSession(r)
	if !ok {
		h.renderAdminLogin(w)
		return
	}
	repository, ok := h.Queries.(adminCorpusStore)
	if !ok {
		w.WriteHeader(http.StatusNotImplemented)
		return
	}
	candidates, err := repository.ListAdminCandidates(r.Context(), 200)
	if err != nil {
		writeError(w, r, err)
		return
	}
	releases, err := repository.ListAdminReleases(r.Context(), 200)
	if err != nil {
		writeError(w, r, err)
		return
	}
	type view struct {
		SessionID, CSRF string
		Candidates      []db.AdminCandidate
		Releases        []db.AdminRelease
	}
	_ = sessionID
	adminPageTemplate.Execute(w, view{CSRF: session.CSRF, Candidates: candidates, Releases: releases})
}

func (h Handlers) PostAdminLogin(w http.ResponseWriter, r *http.Request) {
	h.adminHeaders(w)
	r.Body = http.MaxBytesReader(w, r.Body, maxCorpusMetadataBody)
	if h.AdminToken == "" {
		http.NotFound(w, r)
		return
	}
	allowed, retry, err := redisLimit(r.Context(), h.Redis, "admin-login:"+middlewares.ClientIP(r, h.TrustProxyHeaders), 5, 15*time.Minute)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if !allowed {
		writeRateLimited(w, retry)
		return
	}
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	provided := r.Form.Get("token")
	if subtle.ConstantTimeCompare([]byte(provided), []byte(h.AdminToken)) != 1 {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	sessionID := randomAdminToken(32)
	csrf := randomAdminToken(24)
	now := time.Now().UTC()
	encoded, _ := json.Marshal(adminSession{CSRF: csrf, CreatedAt: now, LastSeen: now})
	if err := h.Redis.Set(r.Context(), "admin:session:"+sessionID, encoded, 30*time.Minute).Err(); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: adminCookieName, Value: sessionID, Path: "/admin", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: int((8 * time.Hour).Seconds())})
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (h Handlers) PostAdminLogout(w http.ResponseWriter, r *http.Request) {
	h.adminHeaders(w)
	r.Body = http.MaxBytesReader(w, r.Body, maxCorpusMetadataBody)
	sessionID, session, ok := h.requireAdminMutation(w, r)
	if !ok {
		return
	}
	_ = session
	_ = h.Redis.Del(r.Context(), "admin:session:"+sessionID).Err()
	http.SetCookie(w, &http.Cookie{Name: adminCookieName, Value: "", Path: "/admin", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (h Handlers) GetAdminArtifact(w http.ResponseWriter, r *http.Request) {
	h.adminHeaders(w)
	_, _, ok := h.adminSession(r)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if h.ObjectStore == nil {
		http.NotFound(w, r)
		return
	}
	repository, ok := h.Queries.(corpusStore)
	if !ok {
		w.WriteHeader(http.StatusNotImplemented)
		return
	}
	artifact, err := repository.GetCorpusArtifact(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.NotFound(w, r)
		} else {
			writeError(w, r, err)
		}
		return
	}
	object, err := h.ObjectStore.Get(r.Context(), artifact.ObjectKey, artifact.ByteLength+1)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", artifact.MediaType)
	w.Header().Set("Content-Disposition", `inline; filename="`+artifact.ID+`"`)
	_, _ = w.Write(object.Body)
}

func (h Handlers) PostAdminReject(w http.ResponseWriter, r *http.Request) {
	h.adminHeaders(w)
	r.Body = http.MaxBytesReader(w, r.Body, maxCorpusMetadataBody)
	sessionID, _, ok := h.requireAdminMutation(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	repository, ok := h.Queries.(adminCorpusStore)
	if !ok {
		w.WriteHeader(http.StatusNotImplemented)
		return
	}
	if err := repository.AdminRejectCandidate(r.Context(), r.PathValue("id"), adminActor(sessionID), r.Form.Get("reason")); err != nil {
		writeError(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (h Handlers) PostAdminPromote(w http.ResponseWriter, r *http.Request) {
	h.adminHeaders(w)
	r.Body = http.MaxBytesReader(w, r.Body, maxCorpusMetadataBody)
	sessionID, _, ok := h.requireAdminMutation(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	languageValue, err := normalizeCorpusLanguage(r.Form.Get("language"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	params := db.AdminPromoteParams{EpisodeUuid: r.Form.Get("episode"), Language: languageValue, Actor: adminActor(sessionID), Reason: r.Form.Get("reason"),
		TranscriptArtifactID: optionalString(r.Form.Get("transcript")), FingerprintArtifactID: optionalString(r.Form.Get("fingerprint")), SummaryArtifactID: optionalString(r.Form.Get("summary")), ChaptersArtifactID: optionalString(r.Form.Get("chapters"))}
	if params.EpisodeUuid == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	repository, ok := h.Queries.(adminCorpusStore)
	if !ok {
		w.WriteHeader(http.StatusNotImplemented)
		return
	}
	if _, err := repository.AdminPromote(r.Context(), params); err != nil {
		writeError(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (h Handlers) PostAdminDerived(w http.ResponseWriter, r *http.Request) {
	h.adminHeaders(w)
	r.Body = http.MaxBytesReader(w, r.Body, maxCorpusMetadataBody)
	sessionID, _, ok := h.requireAdminMutation(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	kind := r.Form.Get("kind")
	if kind != "transcript" && kind != "summary" && kind != "chapters" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	content := []byte(r.Form.Get("content"))
	if len(content) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	languageValue, err := normalizeCorpusLanguage(r.Form.Get("language"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	mediaType, format, extension := "text/plain; charset=utf-8", "plain", "txt"
	if kind == "transcript" {
		if !strings.HasPrefix(strings.TrimSpace(string(content)), "WEBVTT") {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		mediaType, format, extension = "text/vtt", "vtt", "vtt"
	}
	if kind == "summary" && !validSummary(string(content)) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		return
	}
	if kind == "chapters" {
		var chapters []corpusChapter
		if json.Unmarshal(content, &chapters) != nil || len(chapters) < 3 || len(chapters) > 8 {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		mediaType, format, extension = "application/json", "podcast-json", "json"
	}
	hash := sha256.Sum256(content)
	hashString := hex.EncodeToString(hash[:])
	key := "corpus/derived/" + hashString + "." + extension
	if h.ObjectStore == nil || h.ObjectStore.Put(r.Context(), key, content, mediaType) != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	repository, ok := h.Queries.(adminCorpusStore)
	if !ok {
		w.WriteHeader(http.StatusNotImplemented)
		return
	}
	_, err = repository.AdminCreateDerivedCandidate(r.Context(), db.AdminDerivedCandidateParams{ParentCandidateID: r.Form.Get("parent"), Language: languageValue, ContentHash: hashString, Actor: adminActor(sessionID), Kind: kind, ObjectKey: key, MediaType: mediaType, Format: format, ByteLength: int64(len(content))})
	if err != nil {
		writeError(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (h Handlers) PostAdminRollback(w http.ResponseWriter, r *http.Request) {
	h.adminHeaders(w)
	r.Body = http.MaxBytesReader(w, r.Body, maxCorpusMetadataBody)
	sessionID, _, ok := h.requireAdminMutation(w, r)
	if !ok {
		return
	}
	repository, ok := h.Queries.(adminCorpusStore)
	if !ok {
		w.WriteHeader(http.StatusNotImplemented)
		return
	}
	if err := repository.AdminRollbackRelease(r.Context(), r.PathValue("id"), adminActor(sessionID)); err != nil {
		writeError(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (h Handlers) PostAdminPasswordReset(w http.ResponseWriter, r *http.Request) {
	h.adminHeaders(w)
	r.Body = http.MaxBytesReader(w, r.Body, maxCorpusMetadataBody)
	sessionID, _, ok := h.requireAdminMutation(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	user, err := h.Queries.GetUserByEmail(r.Context(), strings.TrimSpace(r.Form.Get("email")))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.NotFound(w, r)
		} else {
			writeError(w, r, err)
		}
		return
	}
	code := randomAdminToken(12)
	hash := auth.HashRefreshToken(code)
	if _, err := h.Queries.CreatePasswordResetCode(r.Context(), db.CreatePasswordResetCodeParams{UserID: user.ID, CodeHash: hash, ExpiresAt: time.Now().Add(15 * time.Minute)}); err != nil {
		writeError(w, r, err)
		return
	}
	if repository, ok := h.Queries.(adminCorpusStore); ok {
		details, _ := json.Marshal(map[string]any{"userId": user.Uuid})
		_ = repository.AdminAudit(r.Context(), adminActor(sessionID), "password_reset_code_generated", details)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "Reset code (expires in 15 minutes): %s\n", code)
}

func (h Handlers) requireAdminMutation(w http.ResponseWriter, r *http.Request) (string, adminSession, bool) {
	if !h.adminSameOrigin(r) {
		w.WriteHeader(http.StatusForbidden)
		return "", adminSession{}, false
	}
	sessionID, session, ok := h.adminSession(r)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		return "", adminSession{}, false
	}
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return "", adminSession{}, false
	}
	if subtle.ConstantTimeCompare([]byte(r.Form.Get("csrf")), []byte(session.CSRF)) != 1 {
		w.WriteHeader(http.StatusForbidden)
		return "", adminSession{}, false
	}
	return sessionID, session, true
}

func (h Handlers) adminSession(r *http.Request) (string, adminSession, bool) {
	if h.Redis == nil {
		return "", adminSession{}, false
	}
	cookie, err := r.Cookie(adminCookieName)
	if err != nil || len(cookie.Value) < 32 {
		return "", adminSession{}, false
	}
	raw, err := h.Redis.Get(r.Context(), "admin:session:"+cookie.Value).Bytes()
	if err != nil {
		return "", adminSession{}, false
	}
	var session adminSession
	if json.Unmarshal(raw, &session) != nil {
		return "", adminSession{}, false
	}
	now := time.Now().UTC()
	if now.Sub(session.CreatedAt) > 8*time.Hour || now.Sub(session.LastSeen) > 30*time.Minute {
		_ = h.Redis.Del(r.Context(), "admin:session:"+cookie.Value).Err()
		return "", adminSession{}, false
	}
	session.LastSeen = now
	encoded, _ := json.Marshal(session)
	_ = h.Redis.Set(r.Context(), "admin:session:"+cookie.Value, encoded, 30*time.Minute).Err()
	return cookie.Value, session, true
}

func (h Handlers) adminSameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	expected := h.PublicBaseURL
	if expected == "" {
		scheme := "https"
		if r.TLS == nil {
			scheme = "http"
		}
		expected = scheme + "://" + r.Host
	}
	want, err := url.Parse(expected)
	return err == nil && strings.EqualFold(parsed.Scheme, want.Scheme) && strings.EqualFold(parsed.Host, want.Host)
}

func (h Handlers) adminHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
}
func (h Handlers) renderAdminLogin(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html><meta name="viewport" content="width=device-width"><title>Podcast corpus admin</title><style>body{font:16px system-ui;max-width:40rem;margin:4rem auto;padding:1rem}input,button{font:inherit;padding:.6rem;width:100%;box-sizing:border-box;margin:.3rem 0}</style><h1>Podcast corpus admin</h1><form method="post" action="/admin/login"><label>Admin token<input name="token" type="password" autocomplete="current-password" required></label><button>Sign in</button></form>`))
}
func randomAdminToken(bytesCount int) string {
	raw := make([]byte, bytesCount)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}
func adminActor(sessionID string) string {
	sum := sha256.Sum256([]byte(sessionID))
	return "admin:" + hex.EncodeToString(sum[:4])
}
func optionalString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

var adminPageTemplate = template.Must(template.New("admin").Parse(`<!doctype html><meta name="viewport" content="width=device-width"><title>Podcast corpus admin</title><style>body{font:14px system-ui;margin:2rem}table{border-collapse:collapse;width:100%;margin-bottom:2rem}td,th{border:1px solid #ccc;padding:.4rem;vertical-align:top}input,textarea,button{font:inherit;margin:.15rem}code{word-break:break-all}.row{display:flex;gap:1rem;flex-wrap:wrap}.card{border:1px solid #ccc;padding:1rem}</style><h1>Corpus candidates</h1><form method="post" action="/admin/logout"><input type="hidden" name="csrf" value="{{.CSRF}}"><button>Sign out</button></form><div class="row"><form class="card" method="post" action="/admin/promote"><input type="hidden" name="csrf" value="{{.CSRF}}"><h2>Promote release</h2><input name="episode" placeholder="episode UUID" required><input name="language" value="und"><input name="transcript" placeholder="transcript artifact UUID"><input name="fingerprint" placeholder="fingerprint artifact UUID"><input name="summary" placeholder="summary artifact UUID"><input name="chapters" placeholder="chapters artifact UUID"><input name="reason" placeholder="decision reason"><button>Promote</button></form><form class="card" method="post" action="/admin/password-reset"><input type="hidden" name="csrf" value="{{.CSRF}}"><h2>Password reset code</h2><input name="email" type="email" required><button>Generate one-time code</button></form><form class="card" method="post" action="/admin/derived"><input type="hidden" name="csrf" value="{{.CSRF}}"><h2>Create derived candidate</h2><input name="parent" placeholder="parent candidate UUID" required><input name="language" value="und"><select name="kind"><option>transcript</option><option>summary</option><option>chapters</option></select><textarea name="content" required></textarea><button>Create immutable edit</button></form></div><table><tr><th>Candidate</th><th>Episode / language</th><th>Source / provenance</th><th>Artifacts</th><th>Decision</th></tr>{{range .Candidates}}<tr><td><code>{{.ID}}</code><br>{{.Status}}<br>{{.CreatedAt}}</td><td><code>{{.EpisodeUuid}}</code><br>{{.Language}}</td><td>{{.Source}} verified={{.PublisherVerified}}<pre>{{printf "%s" .Provenance}}</pre></td><td>{{range .Artifacts}}<a href="/admin/artifact/{{.ID}}">{{.Kind}}</a> <code>{{.ID}}</code><br>{{end}}</td><td><form method="post" action="/admin/candidates/{{.ID}}/reject"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input name="reason" placeholder="reason" required><button>Reject</button></form></td></tr>{{end}}</table><h2>Release history</h2><table><tr><th>Release</th><th>Episode</th><th>Language</th><th>Active</th><th>Action</th></tr>{{range .Releases}}<tr><td><code>{{.ID}}</code><br>{{.CreatedAt}}<br>{{.CreatedBy}}</td><td><code>{{.EpisodeUuid}}</code></td><td>{{.Language}}</td><td>{{.Active}}</td><td><form method="post" action="/admin/releases/{{.ID}}/rollback"><input type="hidden" name="csrf" value="{{$.CSRF}}"><button>Activate this release</button></form></td></tr>{{end}}</table>`))
