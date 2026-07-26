package attest

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCanonicalRequest(t *testing.T) {
	body := []byte(`{"x":1}`)
	req := httptest.NewRequest(http.MethodPost, "/podcasts/%E2%9C%93?b=two&a=hello+world&a=%21", nil)
	canonical, err := CanonicalRequest(req, body)
	require.NoError(t, err)
	hash := sha256.Sum256(body)
	assert.Equal(t, "v1\nPOST\n/podcasts/%E2%9C%93\na=%21&a=hello%20world&b=two\n"+hex.EncodeToString(hash[:]), string(canonical))
}

func TestCanonicalRequestRejectsAmbiguousTargets(t *testing.T) {
	for _, target := range []string{"/a//b", "/a/../b", "/a/%2e%2e/b", "/a?bad=%zz"} {
		t.Run(target, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, target, nil)
			_, err := CanonicalRequest(req, nil)
			assert.ErrorIs(t, err, ErrNonCanonicalRequest)
		})
	}
}
