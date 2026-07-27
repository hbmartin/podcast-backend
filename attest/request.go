package attest

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
)

var ErrNonCanonicalRequest = errors.New("attest: request target is not canonical")

// CanonicalRequest binds an assertion to the HTTP method and complete request
// target as well as the exact body bytes.
func CanonicalRequest(r *http.Request, body []byte) ([]byte, error) {
	decodedPath := r.URL.Path
	if decodedPath == "" {
		decodedPath = "/"
	}
	if !strings.HasPrefix(decodedPath, "/") || strings.Contains(decodedPath, "\\") || strings.Contains(decodedPath, "//") || path.Clean(decodedPath) != decodedPath {
		return nil, ErrNonCanonicalRequest
	}
	canonicalPath := (&url.URL{Path: decodedPath}).EscapedPath()
	if rawPath := r.URL.RawPath; rawPath != "" && rawPath != canonicalPath {
		return nil, ErrNonCanonicalRequest
	}

	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, ErrNonCanonicalRequest
	}
	type pair struct{ key, value string }
	pairs := make([]pair, 0)
	for key, entries := range values {
		if len(entries) == 0 {
			entries = []string{""}
		}
		for _, value := range entries {
			pairs = append(pairs, pair{rfc3986QueryEscape(key), rfc3986QueryEscape(value)})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].key == pairs[j].key {
			return pairs[i].value < pairs[j].value
		}
		return pairs[i].key < pairs[j].key
	})
	queryParts := make([]string, len(pairs))
	for i, item := range pairs {
		queryParts[i] = item.key + "=" + item.value
	}

	bodyHash := sha256.Sum256(body)
	canonical := strings.Join([]string{
		"v1",
		strings.ToUpper(r.Method),
		canonicalPath,
		strings.Join(queryParts, "&"),
		hex.EncodeToString(bodyHash[:]),
	}, "\n")
	return []byte(canonical), nil
}

func rfc3986QueryEscape(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}
