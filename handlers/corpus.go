package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hbmartin/podcast-backend/db"
	"github.com/jackc/pgx/v5"
	"golang.org/x/text/language"
)

const maxCorpusMetadataBody = 128 << 10

type contributionCorpusStore interface {
	CreateContributionCandidate(context.Context, db.CreateContributionCandidateParams) (db.CreateContributionCandidateResult, error)
}

type corpusStore interface {
	AttachCandidateMetadata(context.Context, db.AttachCandidateMetadataParams) error
	CandidateMetadataContext(context.Context, string, string) (db.CandidateMetadataContext, error)
	GetActiveCorpusRelease(context.Context, string, []string) (db.ActiveCorpusRelease, error)
	GetCorpusArtifact(context.Context, string) (db.CorpusArtifact, error)
	GetEpisodeCorpusLanguage(context.Context, string) (string, error)
}

type corpusChapter struct {
	Title     string  `json:"title"`
	Timestamp string  `json:"timestamp,omitempty"`
	StartTime float64 `json:"startTime"`
}

type corpusMetadataRequest struct {
	CandidateID     string          `json:"candidateId"`
	AttachmentToken string          `json:"attachmentToken"`
	Summary         string          `json:"summary"`
	Chapters        []corpusChapter `json:"chapters"`
}

func (h Handlers) PostCorpusMetadata(w http.ResponseWriter, r *http.Request) {
	if !h.CorpusEnabled || h.ObjectStore == nil {
		http.NotFound(w, r)
		return
	}
	var request corpusMetadataRequest
	if err := bindJSON(r, &request); err != nil || request.CandidateID == "" || len(request.AttachmentToken) < 32 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	repository, ok := h.Queries.(corpusStore)
	if !ok {
		w.WriteHeader(http.StatusNotImplemented)
		return
	}
	tokenHash := sha256.Sum256([]byte(request.AttachmentToken))
	tokenHashString := hex.EncodeToString(tokenHash[:])
	metadataContext, err := repository.CandidateMetadataContext(r.Context(), request.CandidateID, tokenHashString)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			w.WriteHeader(http.StatusUnauthorized)
		} else {
			writeError(w, r, err)
		}
		return
	}
	if !validSummary(request.Summary) || !validChapters(request.Chapters, metadataContext.Duration) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		return
	}
	summary := []byte(strings.TrimSpace(request.Summary))
	chapters, _ := json.Marshal(request.Chapters)
	summaryHash := sha256.Sum256(summary)
	chaptersHash := sha256.Sum256(chapters)
	summaryHashString, chaptersHashString := hex.EncodeToString(summaryHash[:]), hex.EncodeToString(chaptersHash[:])
	summaryKey, chaptersKey := "corpus/summaries/"+summaryHashString+".txt", "corpus/chapters/"+chaptersHashString+".json"
	if err := h.ObjectStore.Put(r.Context(), summaryKey, summary, "text/plain; charset=utf-8"); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if err := h.ObjectStore.Put(r.Context(), chaptersKey, chapters, "application/json"); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	err = repository.AttachCandidateMetadata(r.Context(), db.AttachCandidateMetadataParams{
		CandidateID: request.CandidateID, TokenHash: tokenHashString,
		Artifacts: []db.CorpusArtifactInput{
			{Kind: "summary", ObjectKey: summaryKey, ContentHash: summaryHashString, MediaType: "text/plain; charset=utf-8", Format: "plain", ByteLength: int64(len(summary)), Language: metadataContext.Language, Source: "contribution"},
			{Kind: "chapters", ObjectKey: chaptersKey, ContentHash: chaptersHashString, MediaType: "application/json", Format: "podcast-json", ByteLength: int64(len(chapters)), Language: metadataContext.Language, Source: "contribution"},
		},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			w.WriteHeader(http.StatusUnauthorized)
		} else {
			writeError(w, r, err)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func validSummary(summary string) bool {
	words := strings.Fields(summary)
	return len(words) >= 150 && len(words) <= 250
}

func validChapters(chapters []corpusChapter, duration float64) bool {
	if len(chapters) < 3 || len(chapters) > 8 || duration <= 0 {
		return false
	}
	previous := -1.0
	for _, chapter := range chapters {
		if strings.TrimSpace(chapter.Title) == "" || chapter.StartTime < 0 || chapter.StartTime > duration || chapter.StartTime <= previous {
			return false
		}
		previous = chapter.StartTime
	}
	return true
}

func normalizeCorpusLanguage(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "und", nil
	}
	tag, err := language.Parse(value)
	if err != nil {
		return "", err
	}
	return tag.String(), nil
}

func (h Handlers) GetCorpusManifest(w http.ResponseWriter, r *http.Request) {
	if !h.allowCorpusRead(w, r) {
		return
	}
	episode := r.PathValue("episode")
	if episode == "" {
		file := r.PathValue("file")
		episode = strings.TrimSuffix(file, "-meta.json")
		if episode == file {
			http.NotFound(w, r)
			return
		}
	}
	release, repository, ok := h.resolveCorpusRelease(w, r, episode)
	if !ok {
		return
	}
	manifest := map[string]any{
		"release":     map[string]string{"id": release.ID, "language": release.Language},
		"language":    release.Language,
		"transcripts": []any{}, "fingerprints": []any{},
	}
	if release.Transcript != nil {
		manifest["transcripts"] = []any{h.artifactDescriptor(r, *release.Transcript)}
	}
	if release.Fingerprint != nil {
		manifest["fingerprints"] = []any{h.artifactDescriptor(r, *release.Fingerprint)}
	}
	if release.Summary != nil {
		if object, err := h.ObjectStore.Get(r.Context(), release.Summary.ObjectKey, maxCorpusMetadataBody); err == nil {
			manifest["summary"] = string(object.Body)
		}
	}
	if release.Chapters != nil {
		if object, err := h.ObjectStore.Get(r.Context(), release.Chapters.ObjectKey, maxCorpusMetadataBody); err == nil {
			var chapters []corpusChapter
			if json.Unmarshal(object.Body, &chapters) == nil {
				manifest["chapters"] = chapters
			}
		}
	}
	_ = repository
	encoded, _ := json.Marshal(manifest)
	etag := strongETag(encoded)
	w.Header().Set("Vary", "Accept-Language")
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(encoded)
}

func (h Handlers) GetLegacyCorpus(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.PathValue("file"), "-meta.json") {
		h.GetCorpusManifest(w, r)
		return
	}
	h.GetLegacyCorpusArtifact(w, r)
}

func (h Handlers) GetCorpusArtifact(w http.ResponseWriter, r *http.Request) {
	if !h.allowCorpusRead(w, r) {
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
	h.writeCorpusArtifact(w, r, artifact)
}

func (h Handlers) GetLegacyCorpusArtifact(w http.ResponseWriter, r *http.Request) {
	if !h.allowCorpusRead(w, r) {
		return
	}
	file := r.PathValue("file")
	kind, episode := "", ""
	switch {
	case strings.HasSuffix(file, "-fingerprints.json.gz"):
		kind, episode = "fingerprint", strings.TrimSuffix(file, "-fingerprints.json.gz")
	case strings.HasSuffix(file, ".vtt"):
		kind, episode = "transcript", strings.TrimSuffix(file, ".vtt")
	default:
		http.NotFound(w, r)
		return
	}
	release, _, ok := h.resolveCorpusRelease(w, r, episode)
	if !ok {
		return
	}
	var artifact *db.CorpusArtifact
	if kind == "transcript" {
		artifact = release.Transcript
	} else {
		artifact = release.Fingerprint
	}
	if artifact == nil {
		http.NotFound(w, r)
		return
	}
	h.writeCorpusArtifact(w, r, *artifact)
}

func (h Handlers) writeCorpusArtifact(w http.ResponseWriter, r *http.Request, artifact db.CorpusArtifact) {
	object, err := h.ObjectStore.Get(r.Context(), artifact.ObjectKey, artifact.ByteLength+1)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	etag := `"` + artifact.ContentHash + `"`
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", artifact.MediaType)
	_, _ = w.Write(object.Body)
}

func (h Handlers) resolveCorpusRelease(w http.ResponseWriter, r *http.Request, episode string) (db.ActiveCorpusRelease, corpusStore, bool) {
	if !h.CorpusEnabled || h.ObjectStore == nil {
		http.NotFound(w, r)
		return db.ActiveCorpusRelease{}, nil, false
	}
	repository, ok := h.Queries.(corpusStore)
	if !ok {
		w.WriteHeader(http.StatusNotImplemented)
		return db.ActiveCorpusRelease{}, nil, false
	}
	podcastLanguage, _ := repository.GetEpisodeCorpusLanguage(r.Context(), episode)
	languages := corpusLanguagePreferences(r.Header.Get("Accept-Language"), podcastLanguage)
	release, err := repository.GetActiveCorpusRelease(r.Context(), episode, languages)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.NotFound(w, r)
		} else {
			writeError(w, r, err)
		}
		return db.ActiveCorpusRelease{}, nil, false
	}
	return release, repository, true
}

func (h Handlers) allowCorpusRead(w http.ResponseWriter, r *http.Request) bool {
	if !h.CorpusEnabled || h.ObjectStore == nil {
		http.NotFound(w, r)
		return false
	}
	install := h.installationID(r)
	if install == "" {
		w.WriteHeader(http.StatusBadRequest)
		return false
	}
	allowed, retry, err := redisLimit(r.Context(), h.Redis, "corpus:"+install, 120, time.Minute)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return false
	}
	if !allowed {
		writeRateLimited(w, retry)
		return false
	}
	return true
}

func (h Handlers) artifactDescriptor(r *http.Request, artifact db.CorpusArtifact) map[string]any {
	var provenance any = map[string]any{}
	_ = json.Unmarshal(artifact.Provenance, &provenance)
	return map[string]any{
		"url": h.baseURL(r) + "/corpus/artifacts/" + artifact.ID, "mediaType": artifact.MediaType,
		"format": artifact.Format, "sha256": artifact.ContentHash, "source": artifact.Source,
		"language": artifact.Language, "provenance": provenance,
	}
}

func strongETag(data []byte) string {
	sum := sha256.Sum256(data)
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

func corpusLanguagePreferences(header, podcastLanguage string) []string {
	type preference struct {
		tag   string
		q     float64
		order int
	}
	var parsed []preference
	for order, part := range strings.Split(header, ",") {
		pieces := strings.Split(strings.TrimSpace(part), ";")
		if len(pieces) == 0 || pieces[0] == "*" || pieces[0] == "" {
			continue
		}
		q := 1.0
		for _, option := range pieces[1:] {
			option = strings.TrimSpace(option)
			if strings.HasPrefix(option, "q=") {
				q, _ = strconv.ParseFloat(strings.TrimPrefix(option, "q="), 64)
			}
		}
		if normalized, err := normalizeCorpusLanguage(pieces[0]); err == nil && q > 0 {
			parsed = append(parsed, preference{normalized, q, order})
		}
	}
	sort.SliceStable(parsed, func(i, j int) bool { return parsed[i].q > parsed[j].q })
	result, seen := []string{}, map[string]bool{}
	add := func(value string) {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
		if index := strings.IndexByte(value, '-'); index > 0 {
			base := value[:index]
			if !seen[base] {
				seen[base] = true
				result = append(result, base)
			}
		}
	}
	for _, item := range parsed {
		add(item.tag)
	}
	if normalized, err := normalizeCorpusLanguage(podcastLanguage); err == nil {
		add(normalized)
	}
	add("und")
	return result
}
