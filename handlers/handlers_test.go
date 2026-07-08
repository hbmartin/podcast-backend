package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hbmartin/podcast-backend/auth"
	"github.com/hbmartin/podcast-backend/config"
	"github.com/hbmartin/podcast-backend/db"

	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/proto"
)

// QuerierMock implements db.Store with canned results. The embedded
// interface covers methods a test doesn't stub (calling one panics); only
// the fields a test sets matter.
type QuerierMock struct {
	db.Store

	PingDbResult int32
	PingDbError  error

	CreateUserResult db.User
	CreateUserError  error
	CreateUserParams *db.CreateUserParams

	GetUserByEmailResult db.User
	GetUserByEmailError  error

	GetUserByUUIDResult db.User
	GetUserByUUIDError  error

	GetUserByIDResult db.User
	GetUserByIDError  error

	UpdateUserEmailResult    int64
	UpdateUserEmailError     error
	UpdateUserPasswordResult int64
	UpdateUserPasswordError  error
	UpdateUserPasswordParams *db.UpdateUserPasswordParams
	SoftDeleteUserResult     int64
	SoftDeleteUserError      error
	SoftDeletedUserID        int64

	CreateRefreshTokenResult db.RefreshToken
	CreateRefreshTokenError  error
	CreateRefreshTokenParams *db.CreateRefreshTokenParams

	GetRefreshTokenByHashResult db.RefreshToken
	GetRefreshTokenByHashError  error

	RevokeRefreshTokenResult     int64
	RevokeRefreshTokenError      error
	RevokedTokenHash             string
	RevokeAllRefreshTokensResult int64
	RevokeAllRefreshTokensError  error
	RevokeAllRefreshTokensUserID int64
}

// InTx runs fn against the mock itself, mimicking a transaction.
func (m *QuerierMock) InTx(ctx context.Context, fn func(db.Querier) error) error {
	return fn(m)
}

func (m *QuerierMock) PingDb(ctx context.Context) (int32, error) {
	return m.PingDbResult, m.PingDbError
}

func (m *QuerierMock) CreateUser(ctx context.Context, arg db.CreateUserParams) (db.User, error) {
	m.CreateUserParams = &arg
	return m.CreateUserResult, m.CreateUserError
}

func (m *QuerierMock) GetUserByEmail(ctx context.Context, email string) (db.User, error) {
	return m.GetUserByEmailResult, m.GetUserByEmailError
}

func (m *QuerierMock) GetUserByUUID(ctx context.Context, uuid string) (db.User, error) {
	return m.GetUserByUUIDResult, m.GetUserByUUIDError
}

func (m *QuerierMock) GetUserByID(ctx context.Context, id int64) (db.User, error) {
	return m.GetUserByIDResult, m.GetUserByIDError
}

func (m *QuerierMock) UpdateUserEmail(ctx context.Context, arg db.UpdateUserEmailParams) (int64, error) {
	return m.UpdateUserEmailResult, m.UpdateUserEmailError
}

func (m *QuerierMock) UpdateUserPassword(ctx context.Context, arg db.UpdateUserPasswordParams) (int64, error) {
	m.UpdateUserPasswordParams = &arg
	return m.UpdateUserPasswordResult, m.UpdateUserPasswordError
}

func (m *QuerierMock) SoftDeleteUser(ctx context.Context, id int64) (int64, error) {
	m.SoftDeletedUserID = id
	return m.SoftDeleteUserResult, m.SoftDeleteUserError
}

func (m *QuerierMock) CreateRefreshToken(ctx context.Context, arg db.CreateRefreshTokenParams) (db.RefreshToken, error) {
	m.CreateRefreshTokenParams = &arg
	return m.CreateRefreshTokenResult, m.CreateRefreshTokenError
}

func (m *QuerierMock) GetRefreshTokenByHash(ctx context.Context, tokenHash string) (db.RefreshToken, error) {
	return m.GetRefreshTokenByHashResult, m.GetRefreshTokenByHashError
}

func (m *QuerierMock) RevokeRefreshToken(ctx context.Context, tokenHash string) (int64, error) {
	m.RevokedTokenHash = tokenHash
	return m.RevokeRefreshTokenResult, m.RevokeRefreshTokenError
}

func (m *QuerierMock) RevokeAllRefreshTokens(ctx context.Context, userID int64) (int64, error) {
	m.RevokeAllRefreshTokensUserID = userID
	return m.RevokeAllRefreshTokensResult, m.RevokeAllRefreshTokensError
}

var testAuthConfig = &config.AuthConfiguration{
	JWTSecret:       "0123456789abcdef0123456789abcdef",
	AccessTokenTTL:  time.Hour,
	RefreshTokenTTL: 30 * 24 * time.Hour,
}

const testUserUUID = "6f2ec5b9-6f5d-4b0a-a2d5-1f2c3d4e5f60"

// setup builds a router with all routes the handler tests exercise.
// Authenticated routes go through mockAuthMiddleware, mirroring main.go's
// chains without the real token validation.
func setup(querierMock *QuerierMock) *http.ServeMux {
	auth.Init(testAuthConfig)

	router := http.NewServeMux()
	handlers := Handlers{Queries: querierMock, Config: testAuthConfig}

	router.HandleFunc("GET /health", handlers.GetHealth)
	router.HandleFunc("GET /health.html", handlers.GetHealthHTML)
	router.HandleFunc("POST /user/login", handlers.PostUserLogin)
	router.HandleFunc("POST /user/register", handlers.PostUserRegister)
	router.HandleFunc("POST /user/forgot_password", handlers.PostForgotPassword)
	router.HandleFunc("POST /user/token", handlers.PostUserToken)
	router.Handle("POST /user/change_email", mockAuthMiddleware(http.HandlerFunc(handlers.PostChangeEmail)))
	router.Handle("POST /user/change_password", mockAuthMiddleware(http.HandlerFunc(handlers.PostChangePassword)))
	router.Handle("POST /user/delete_account", mockAuthMiddleware(http.HandlerFunc(handlers.PostDeleteAccount)))

	return router
}

func mockAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		user := &auth.User{UUID: testUserUUID, Email: "mail@test.com", Scope: "mobile"}
		r = r.WithContext(context.WithValue(ctx, auth.UserKey, user))

		next.ServeHTTP(w, r)
	})
}

// makeRequest sends a JSON request and decodes a JSON response of type K.
func makeRequest[K any | []any](router *http.ServeMux, method string, url string, body any) (code int, respBody *K, headers http.Header, err error) {
	inputBody := ""

	if body != nil {
		inputBodyJson, _ := json.Marshal(body)
		inputBody = string(inputBodyJson)
	}

	req, _ := http.NewRequest(method, url, bytes.NewReader([]byte(inputBody)))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	result := new(K)

	switch any(result).(type) {
	case *string:
		// do nothing as we don't care about string
		result = nil
	default:
		err = json.Unmarshal(rr.Body.Bytes(), &result)
	}

	return rr.Code, result, rr.Result().Header, err
}

// makeProtoRequest sends a protobuf request body and unmarshals the response
// into resp when the status is 200; on error statuses the raw body is
// returned so callers can decode the JSON error envelope.
func makeProtoRequest(router *http.ServeMux, url string, reqMsg proto.Message, resp proto.Message) (code int, rawBody []byte, err error) {
	body, err := proto.Marshal(reqMsg)
	if err != nil {
		return 0, nil, err
	}

	req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/octet-stream")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	raw := rr.Body.Bytes()
	if rr.Code == http.StatusOK && resp != nil {
		err = proto.Unmarshal(raw, resp)
	}
	return rr.Code, raw, err
}

func TestGetUser(t *testing.T) {
	req, _ := http.NewRequest("GET", "/dummy", bytes.NewReader([]byte("")))
	body := &auth.User{UUID: "test-uuid", Email: "email@test.com", Scope: "mobile"}
	newReq := req.WithContext(context.WithValue(req.Context(), auth.UserKey, body))

	user := getUser(newReq.Context())

	assert.Equal(t, "test-uuid", user.UUID)
	assert.Equal(t, "email@test.com", user.Email)
}

func TestGetUserEmail(t *testing.T) {
	req, _ := http.NewRequest("GET", "/dummy", bytes.NewReader([]byte("")))
	body := &auth.User{UUID: "test-uuid", Email: "email@test.com"}
	newReq := req.WithContext(context.WithValue(req.Context(), auth.UserKey, body))

	email := getUserEmail(newReq.Context())

	assert.Equal(t, "email@test.com", email)
}

func TestGetUserEmailEmpty1(t *testing.T) {
	email := getUserEmail(context.TODO())

	assert.Equal(t, "", email)
}

func TestGetUserEmailEmpty2(t *testing.T) {
	req, _ := http.NewRequest("GET", "/dummy", bytes.NewReader([]byte("")))

	email := getUserEmail(req.Context())

	assert.Equal(t, "", email)
}

func TestErrorTranslationSuccess(t *testing.T) {
	type TestStruct struct {
		Req   string `validate:"required"`
		Lt    string `validate:"required,lt=10"`
		Lte   string `validate:"required,lte=1"`
		Gt    int    `validate:"required,gt=1"`
		Gte   int    `validate:"required,gte=10"`
		Min   string `validate:"min=10"`
		Max   string `validate:"max=9"`
		Alpha string `validate:"alpha"`
	}

	user := TestStruct{
		Req:   "",
		Lt:    "0123456789",
		Lte:   "012345678",
		Gt:    1,
		Gte:   1,
		Min:   "012345678",
		Max:   "0123456789",
		Alpha: "0123456789",
	}

	validate := validator.New()
	err := validate.Struct(user)
	code, result := errorToHttpResult(err, context.Background())

	assert.Equal(t, http.StatusBadRequest, code)

	assert.Len(t, result.Errors, 8)
	assert.Equal(t, "Req is required", result.Errors[0])
	assert.Equal(t, "Lt should be less than 10", result.Errors[1])
	assert.Equal(t, "Lte should be less than or equal to 1", result.Errors[2])
	assert.Equal(t, "Gt should be greater than 1", result.Errors[3])
	assert.Equal(t, "Gte should be greater than or equal to 10", result.Errors[4])
	assert.Equal(t, "Min should have minimum length of 10", result.Errors[5])
	assert.Equal(t, "Max should have maximum length of 9", result.Errors[6])
	assert.Equal(t, "Alpha should contain alpha characters only", result.Errors[7])
}

func TestErrorTranslationServerError(t *testing.T) {
	code, result := errorToHttpResult(fmt.Errorf("Something went wrong"), context.Background())
	assert.Equal(t, http.StatusInternalServerError, code)

	assert.Equal(t, "Unknown error", result.Errors[0])
}
