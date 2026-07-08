package push

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testKeyPEM(t *testing.T) ([]byte, *ecdsa.PublicKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), &key.PublicKey
}

type recordedPush struct {
	path    string
	headers http.Header
	body    []byte
}

func TestClientSendsAlert(t *testing.T) {
	keyPEM, pubKey := testKeyPEM(t)

	var got recordedPush
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = recordedPush{path: r.URL.Path, headers: r.Header.Clone(), body: body}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := NewClient(keyPEM, "KEY123", "TEAM456", "com.example.pods", server.URL)
	require.NoError(t, err)

	err = client.Send(context.Background(), "DEADBEEF", Notification{Title: "Test Show", Body: "Episode Three"})
	require.NoError(t, err)

	assert.Equal(t, "/3/device/DEADBEEF", got.path)
	assert.Equal(t, "com.example.pods", got.headers.Get("apns-topic"))
	assert.Equal(t, "alert", got.headers.Get("apns-push-type"))
	assert.Equal(t, "10", got.headers.Get("apns-priority"))
	assert.NotEmpty(t, got.headers.Get("apns-expiration"))

	var payload struct {
		Aps struct {
			Alert Notification `json:"alert"`
			Sound string       `json:"sound"`
		} `json:"aps"`
	}
	require.NoError(t, json.Unmarshal(got.body, &payload))
	assert.Equal(t, "Test Show", payload.Aps.Alert.Title)
	assert.Equal(t, "Episode Three", payload.Aps.Alert.Body)
	assert.Equal(t, "default", payload.Aps.Sound)

	// the provider token is a valid ES256 JWT carrying the team and key ids
	authz := got.headers.Get("Authorization")
	require.True(t, len(authz) > len("bearer "))
	parsed, err := jwt.Parse(authz[len("bearer "):], func(tok *jwt.Token) (any, error) {
		assert.Equal(t, "ES256", tok.Method.Alg())
		assert.Equal(t, "KEY123", tok.Header["kid"])
		return pubKey, nil
	})
	require.NoError(t, err)
	claims := parsed.Claims.(jwt.MapClaims)
	assert.Equal(t, "TEAM456", claims["iss"])
	assert.NotNil(t, claims["iat"])
}

func TestClientReusesProviderToken(t *testing.T) {
	keyPEM, _ := testKeyPEM(t)

	var tokens []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokens = append(tokens, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := NewClient(keyPEM, "K", "T", "topic", server.URL)
	require.NoError(t, err)

	require.NoError(t, client.Send(context.Background(), "A", Notification{}))
	require.NoError(t, client.Send(context.Background(), "B", Notification{}))
	require.Len(t, tokens, 2)
	assert.Equal(t, tokens[0], tokens[1], "JWT cached across sends")
}

func TestClientUnregisteredToken(t *testing.T) {
	keyPEM, _ := testKeyPEM(t)

	responses := []struct {
		status int
		reason string
		expect error
	}{
		{http.StatusGone, "Unregistered", ErrUnregistered},
		{http.StatusBadRequest, "BadDeviceToken", ErrUnregistered},
	}

	for _, tc := range responses {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			json.NewEncoder(w).Encode(map[string]string{"reason": tc.reason})
		}))

		client, err := NewClient(keyPEM, "K", "T", "topic", server.URL)
		require.NoError(t, err)

		err = client.Send(context.Background(), "DEAD", Notification{})
		assert.ErrorIs(t, err, tc.expect, "status %d reason %s", tc.status, tc.reason)
		server.Close()
	}
}

func TestClientOtherErrorsAreNotUnregistered(t *testing.T) {
	keyPEM, _ := testKeyPEM(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]string{"reason": "TooManyRequests"})
	}))
	defer server.Close()

	client, err := NewClient(keyPEM, "K", "T", "topic", server.URL)
	require.NoError(t, err)

	err = client.Send(context.Background(), "DEAD", Notification{})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrUnregistered)
}

func TestNewClientRejectsBadKey(t *testing.T) {
	_, err := NewClient([]byte("not pem"), "K", "T", "topic", "")
	assert.Error(t, err)
}
