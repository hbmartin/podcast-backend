package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hbmartin/podcast-backend/config"

	"github.com/stretchr/testify/assert"
)

func initTestConfig(t *testing.T) {
	t.Helper()
	Init(&config.AuthConfiguration{
		JWTSecret:       "0123456789abcdef0123456789abcdef",
		AccessTokenTTL:  time.Hour,
		RefreshTokenTTL: 24 * time.Hour,
	})
}

func TestPasswordHashRoundTrip(t *testing.T) {
	hash, err := HashPassword("hunter2hunter2")

	assert.NoError(t, err)
	assert.NotEqual(t, "hunter2hunter2", hash)
	assert.True(t, CheckPassword(hash, "hunter2hunter2"))
	assert.False(t, CheckPassword(hash, "wrong"))
}

func TestAccessTokenRoundTrip(t *testing.T) {
	initTestConfig(t)

	token, expiresIn, err := MintAccessToken("user-uuid", "a@b.co", "mobile")
	assert.NoError(t, err)
	assert.Equal(t, int32(3600), expiresIn)

	user, err := ValidateAccessToken(token)
	assert.NoError(t, err)
	assert.Equal(t, "user-uuid", user.UUID)
	assert.Equal(t, "a@b.co", user.Email)
	assert.Equal(t, "mobile", user.Scope)
}

func TestAccessTokenExpired(t *testing.T) {
	Init(&config.AuthConfiguration{
		JWTSecret:      "0123456789abcdef0123456789abcdef",
		AccessTokenTTL: -time.Minute,
	})

	token, _, err := MintAccessToken("user-uuid", "a@b.co", "mobile")
	assert.NoError(t, err)

	_, err = ValidateAccessToken(token)
	assert.Error(t, err)
}

func TestAccessTokenBadSignature(t *testing.T) {
	initTestConfig(t)
	token, _, _ := MintAccessToken("user-uuid", "a@b.co", "mobile")

	Init(&config.AuthConfiguration{
		JWTSecret:      "another-secret-another-secret-32",
		AccessTokenTTL: time.Hour,
	})

	_, err := ValidateAccessToken(token)
	assert.Error(t, err)
}

func TestRefreshTokenHashing(t *testing.T) {
	token, hash, err := NewRefreshToken()

	assert.NoError(t, err)
	assert.NotEmpty(t, token)
	assert.Equal(t, hash, HashRefreshToken(token))

	token2, hash2, err := NewRefreshToken()
	assert.NoError(t, err)
	assert.NotEqual(t, token, token2)
	assert.NotEqual(t, hash, hash2)
}

func TestTokenAuthMiddleware(t *testing.T) {
	initTestConfig(t)

	var gotUser *User
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, _ = r.Context().Value(UserKey).(*User)
		w.WriteHeader(http.StatusOK)
	})
	handler := TokenAuthMiddleware(inner)

	// no token -> 401
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/user/sync/update", nil)
	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)

	// garbage token -> 401
	rr = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/user/sync/update", nil)
	req.Header.Set("Authorization", "Bearer garbage")
	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)

	// valid token -> 200 with user in context
	token, _, _ := MintAccessToken("user-uuid", "a@b.co", "mobile")
	rr = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/user/sync/update", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "user-uuid", gotUser.UUID)

	// disallowed scope -> 401
	badScope, _, _ := MintAccessToken("user-uuid", "a@b.co", "web")
	rr = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/user/sync/update", nil)
	req.Header.Set("Authorization", "Bearer "+badScope)
	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestOptionalTokenMiddleware(t *testing.T) {
	initTestConfig(t)

	var gotUser *User
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, _ = r.Context().Value(UserKey).(*User)
		w.WriteHeader(http.StatusOK)
	})
	handler := OptionalTokenMiddleware(inner)

	// anonymous passes through
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest("POST", "/user/update", nil))
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Nil(t, gotUser)

	// valid token populates user
	token, _, _ := MintAccessToken("user-uuid", "a@b.co", "mobile")
	req := httptest.NewRequest("POST", "/user/update", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "user-uuid", gotUser.UUID)
}
