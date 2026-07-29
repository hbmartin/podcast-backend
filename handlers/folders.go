package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/hbmartin/podcast-backend/db"
)

const maxFolderUUIDs = 1000

var errPermanentGemini = errors.New("permanent Gemini failure")

type folderMetadataStore interface {
	GetPodcastSuggestionMetadata(context.Context, []string) ([]db.PodcastSuggestionMetadata, error)
}

type FolderSuggester interface {
	Suggest(context.Context, string, []db.PodcastSuggestionMetadata) (map[string][]string, error)
	Available() bool
}

type geminiFolderSuggester struct {
	apiKey string
	model  string
	client *http.Client
	open   atomic.Bool
}

func NewGeminiFolderSuggester(apiKey, model string, client *http.Client) FolderSuggester {
	if apiKey == "" {
		return nil
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &geminiFolderSuggester{apiKey: apiKey, model: model, client: client}
}

func (g *geminiFolderSuggester) Available() bool { return !g.open.Load() }

func (g *geminiFolderSuggester) Suggest(ctx context.Context, language string, podcasts []db.PodcastSuggestionMetadata) (map[string][]string, error) {
	if g.open.Load() {
		return nil, errPermanentGemini
	}
	metadata, _ := json.Marshal(podcasts)
	prompt := "Group every podcast UUID exactly once into at most eight useful folders. Use folder names in " + language + ". Do not invent UUIDs. Podcast metadata: " + string(metadata)
	body := map[string]any{
		"contents": []any{map[string]any{"parts": []any{map[string]string{"text": prompt}}}},
		"generationConfig": map[string]any{
			"responseMimeType": "application/json",
			"responseSchema": map[string]any{
				"type": "ARRAY", "items": map[string]any{
					"type": "OBJECT", "properties": map[string]any{
						"name":  map[string]string{"type": "STRING"},
						"uuids": map[string]any{"type": "ARRAY", "items": map[string]string{"type": "STRING"}},
					}, "required": []string{"name", "uuids"},
				},
			},
		},
	}
	encoded, _ := json.Marshal(body)
	endpoint := "https://generativelanguage.googleapis.com/v1beta/models/" + url.PathEscape(g.model) + ":generateContent"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-goog-api-key", g.apiKey)
	response, err := g.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 && response.StatusCode < 500 && response.StatusCode != http.StatusTooManyRequests {
		g.open.Store(true)
		return nil, errPermanentGemini
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini status %d", response.StatusCode)
	}
	var envelope struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&envelope); err != nil || len(envelope.Candidates) == 0 || len(envelope.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("malformed Gemini response")
	}
	var groups []struct {
		Name  string   `json:"name"`
		UUIDs []string `json:"uuids"`
	}
	if err := json.Unmarshal([]byte(envelope.Candidates[0].Content.Parts[0].Text), &groups); err != nil {
		return nil, fmt.Errorf("malformed Gemini output: %w", err)
	}
	result := make(map[string][]string, len(groups))
	for _, group := range groups {
		result[group.Name] = append(result[group.Name], group.UUIDs...)
	}
	return result, nil
}

type folderSuggestionRequest struct {
	Language string   `json:"language"`
	UUIDs    []string `json:"uuids"`
}

func (h Handlers) PostSuggestFolders(w http.ResponseWriter, r *http.Request) {
	if h.FolderSuggester == nil || !h.FolderSuggester.Available() {
		http.NotFound(w, r)
		return
	}
	var request folderSuggestionRequest
	if err := bindJSON(r, &request); err != nil || len(request.UUIDs) == 0 || len(request.UUIDs) > maxFolderUUIDs {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	seen := make(map[string]struct{}, len(request.UUIDs))
	for _, uuid := range request.UUIDs {
		if !uuidPattern.MatchString(uuid) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if _, duplicate := seen[uuid]; duplicate {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		seen[uuid] = struct{}{}
	}
	install := h.installationID(r)
	if install == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	allowed, retry, err := redisLimit(r.Context(), h.Redis, "folders:"+install, 5, time.Minute)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if !allowed {
		writeRateLimited(w, retry)
		return
	}

	repository, ok := h.Queries.(folderMetadataStore)
	if !ok {
		w.WriteHeader(http.StatusNotImplemented)
		return
	}
	metadata, err := repository.GetPodcastSuggestionMetadata(r.Context(), request.UUIDs)
	if err != nil {
		writeError(w, r, err)
		return
	}
	language := supportedFolderLanguage(request.Language)
	cacheKey := folderCacheKey(language, request.UUIDs, metadata)
	if cached, err := h.Redis.Get(r.Context(), cacheKey).Bytes(); err == nil {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(cached)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	groups, providerErr := h.FolderSuggester.Suggest(ctx, language, metadata)
	if providerErr != nil {
		if errors.Is(providerErr, errPermanentGemini) {
			slog.Error("Gemini folder suggestions disabled until restart", "error", providerErr)
			http.NotFound(w, r)
			return
		}
		slog.Warn("Gemini folder suggestions fell back locally", "error", providerErr)
		groups = categoryFolderFallback(metadata)
	}
	result := repairFolderAssignments(groups, request.UUIDs, localizedOther(language))
	encoded, _ := json.Marshal(result)
	_ = h.Redis.Set(r.Context(), cacheKey, encoded, 7*24*time.Hour).Err()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(encoded)
}

func folderCacheKey(language string, uuids []string, metadata []db.PodcastSuggestionMetadata) string {
	sortedUUIDs := append([]string(nil), uuids...)
	sort.Strings(sortedUUIDs)
	sortedMetadata := append([]db.PodcastSuggestionMetadata(nil), metadata...)
	sort.Slice(sortedMetadata, func(i, j int) bool { return sortedMetadata[i].Uuid < sortedMetadata[j].Uuid })
	encoded, _ := json.Marshal([]any{language, sortedUUIDs, sortedMetadata})
	sum := sha256.Sum256(encoded)
	return "folders:cache:" + hex.EncodeToString(sum[:])
}

func categoryFolderFallback(metadata []db.PodcastSuggestionMetadata) map[string][]string {
	result := map[string][]string{}
	for _, podcast := range metadata {
		name := strings.TrimSpace(strings.Split(podcast.Category, ",")[0])
		if name == "" {
			name = "Other"
		}
		result[name] = append(result[name], podcast.Uuid)
	}
	return result
}

func repairFolderAssignments(groups map[string][]string, submitted []string, other string) map[string][]string {
	allowed := make(map[string]struct{}, len(submitted))
	for _, uuid := range submitted {
		allowed[uuid] = struct{}{}
	}
	assignments := map[string]string{}
	duplicate := map[string]bool{}
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		return strings.ToLower(strings.TrimSpace(names[i])) < strings.ToLower(strings.TrimSpace(names[j]))
	})
	canonicalNames := map[string]string{}
	for _, rawName := range names {
		name := strings.TrimSpace(rawName)
		if name == "" {
			name = other
		}
		folded := strings.ToLower(name)
		if existing := canonicalNames[folded]; existing != "" {
			name = existing
		} else {
			canonicalNames[folded] = name
		}
		for _, uuid := range groups[rawName] {
			if _, ok := allowed[uuid]; !ok {
				continue
			}
			if _, exists := assignments[uuid]; exists {
				duplicate[uuid] = true
				continue
			}
			assignments[uuid] = name
		}
	}
	result := map[string][]string{}
	for _, uuid := range submitted {
		name, found := assignments[uuid]
		if !found || duplicate[uuid] {
			name = other
		}
		result[name] = append(result[name], uuid)
	}
	for name, members := range result {
		if name != other && len(members) < 2 {
			result[other] = append(result[other], members...)
			delete(result, name)
		}
	}
	if len(result) > 8 {
		type ranked struct {
			name  string
			count int
		}
		list := make([]ranked, 0, len(result))
		for name, members := range result {
			if name != other {
				list = append(list, ranked{name, len(members)})
			}
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].count == list[j].count {
				return list[i].name < list[j].name
			}
			return list[i].count > list[j].count
		})
		keep := map[string]bool{}
		for i := 0; i < len(list) && i < 7; i++ {
			keep[list[i].name] = true
		}
		for _, item := range list {
			if !keep[item.name] {
				result[other] = append(result[other], result[item.name]...)
				delete(result, item.name)
			}
		}
	}
	for name := range result {
		sort.Strings(result[name])
	}
	return result
}

func supportedFolderLanguage(input string) string {
	value := strings.ReplaceAll(strings.TrimSpace(input), "_", "-")
	parts := strings.Split(value, "-")
	base := strings.ToLower(parts[0])
	if base == "zh" && len(parts) > 1 {
		script := strings.ToLower(parts[1])
		if script == "hans" {
			return "zh-Hans"
		}
		if script == "hant" {
			return "zh-Hant"
		}
	}
	if (base == "es" || base == "fr") && len(parts) > 1 {
		region := strings.ToUpper(parts[1])
		candidate := base + "-" + region
		if candidate == "es-MX" || candidate == "fr-CA" {
			return candidate
		}
	}
	for _, supported := range []string{"ca", "de", "en", "es", "fr", "it", "ja", "nl", "pt-BR", "ru", "sv"} {
		if base == strings.ToLower(strings.Split(supported, "-")[0]) {
			return supported
		}
	}
	return "en"
}

func localizedOther(language string) string {
	base := strings.Split(language, "-")[0]
	translated := map[string]string{"ca": "Altres", "de": "Andere", "es": "Otros", "fr": "Autres", "it": "Altro", "ja": "その他", "nl": "Overig", "pt": "Outros", "ru": "Другое", "sv": "Övrigt", "zh": "其他"}[base]
	if translated == "" {
		return "Other"
	}
	return translated
}
