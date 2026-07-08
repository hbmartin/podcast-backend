package handlers

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/hbmartin/podcast-backend/auth"
	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/pcerrors"
	pb "github.com/hbmartin/podcast-backend/protos/api"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
)

func testUser(t *testing.T, password string) db.User {
	t.Helper()
	hash, err := auth.HashPassword(password)
	assert.NoError(t, err)
	return db.User{
		ID:           42,
		Uuid:         testUserUUID,
		Email:        "mail@test.com",
		PasswordHash: hash,
		Scope:        "mobile",
	}
}

func decodeErrorEnvelope(t *testing.T, raw []byte) string {
	t.Helper()
	var envelope struct {
		ErrorMessageID string `json:"errorMessageId"`
	}
	assert.NoError(t, json.Unmarshal(raw, &envelope))
	return envelope.ErrorMessageID
}

func TestLoginSuccess(t *testing.T) {
	user := testUser(t, "secret-pass")
	router := setup(&QuerierMock{GetUserByEmailResult: user})

	resp := &pb.UserLoginResponse{}
	code, _, err := makeProtoRequest(router, "/user/login",
		&pb.UserLoginRequest{Email: "mail@test.com", Password: "secret-pass", Scope: "mobile"}, resp)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, testUserUUID, resp.Uuid)
	assert.Equal(t, "mail@test.com", resp.Email)

	// minted token must round-trip through our own validator
	parsed, err := auth.ValidateAccessToken(resp.Token)
	assert.NoError(t, err)
	assert.Equal(t, testUserUUID, parsed.UUID)
	assert.Equal(t, "mobile", parsed.Scope)
}

func TestLoginWrongPassword(t *testing.T) {
	user := testUser(t, "secret-pass")
	router := setup(&QuerierMock{GetUserByEmailResult: user})

	code, raw, _ := makeProtoRequest(router, "/user/login",
		&pb.UserLoginRequest{Email: "mail@test.com", Password: "wrong"}, nil)

	assert.Equal(t, http.StatusUnauthorized, code)
	assert.Equal(t, pcerrors.IncorrectPassword, decodeErrorEnvelope(t, raw))
}

func TestLoginUnknownEmail(t *testing.T) {
	router := setup(&QuerierMock{GetUserByEmailError: pgx.ErrNoRows})

	code, raw, _ := makeProtoRequest(router, "/user/login",
		&pb.UserLoginRequest{Email: "nobody@test.com", Password: "whatever"}, nil)

	assert.Equal(t, http.StatusUnauthorized, code)
	assert.Equal(t, pcerrors.EmailNotFound, decodeErrorEnvelope(t, raw))
}

func TestLoginBlankFields(t *testing.T) {
	router := setup(&QuerierMock{})

	code, raw, _ := makeProtoRequest(router, "/user/login", &pb.UserLoginRequest{Password: "x"}, nil)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, pcerrors.BlankEmail, decodeErrorEnvelope(t, raw))

	code, raw, _ = makeProtoRequest(router, "/user/login", &pb.UserLoginRequest{Email: "a@b.co"}, nil)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, pcerrors.BlankPassword, decodeErrorEnvelope(t, raw))
}

func TestRegisterSuccess(t *testing.T) {
	mock := &QuerierMock{
		CreateUserResult: db.User{ID: 7, Uuid: testUserUUID, Email: "new@test.com", Scope: "mobile"},
	}
	router := setup(mock)

	resp := &pb.RegisterResponse{}
	code, _, err := makeProtoRequest(router, "/user/register",
		&pb.RegisterRequest{Email: "new@test.com", Password: "longenough", Scope: "mobile"}, resp)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.True(t, resp.Success.GetValue())
	assert.Equal(t, testUserUUID, resp.Uuid)
	assert.NotEmpty(t, resp.Token)
	// password must be stored hashed
	assert.NotEqual(t, "longenough", mock.CreateUserParams.PasswordHash)
	assert.True(t, auth.CheckPassword(mock.CreateUserParams.PasswordHash, "longenough"))
}

func TestRegisterEmailTaken(t *testing.T) {
	router := setup(&QuerierMock{CreateUserError: &pgconn.PgError{Code: "23505"}})

	code, raw, _ := makeProtoRequest(router, "/user/register",
		&pb.RegisterRequest{Email: "dup@test.com", Password: "longenough"}, nil)

	assert.Equal(t, http.StatusConflict, code)
	assert.Equal(t, pcerrors.EmailTaken, decodeErrorEnvelope(t, raw))
}

func TestRegisterValidation(t *testing.T) {
	router := setup(&QuerierMock{})

	code, raw, _ := makeProtoRequest(router, "/user/register",
		&pb.RegisterRequest{Email: "not-an-email", Password: "longenough"}, nil)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, pcerrors.EmailInvalid, decodeErrorEnvelope(t, raw))

	code, raw, _ = makeProtoRequest(router, "/user/register",
		&pb.RegisterRequest{Email: "a@b.co", Password: "tiny"}, nil)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, pcerrors.PasswordInvalid, decodeErrorEnvelope(t, raw))
}

func TestTokenRefreshGrant(t *testing.T) {
	token, hash, err := auth.NewRefreshToken()
	assert.NoError(t, err)

	mock := &QuerierMock{
		GetRefreshTokenByHashResult: db.RefreshToken{
			ID: 1, UserID: 42, TokenHash: hash, Scope: "mobile",
			ExpiresAt: time.Now().Add(time.Hour),
		},
		GetUserByIDResult: db.User{ID: 42, Uuid: testUserUUID, Email: "mail@test.com"},
	}
	router := setup(mock)

	resp := &pb.TokenLoginResponse{}
	code, _, err := makeProtoRequest(router, "/user/token",
		&pb.UserTokenRequest{GrantType: "refresh_token", RefreshToken: token, Scope: "mobile"}, resp)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "Bearer", resp.TokenType)
	assert.NotEmpty(t, resp.AccessToken)
	assert.NotEmpty(t, resp.RefreshToken)
	assert.NotEqual(t, token, resp.RefreshToken, "refresh token must rotate")
	assert.Equal(t, hash, mock.RevokedTokenHash, "old token must be revoked")
	assert.Equal(t, auth.HashRefreshToken(resp.RefreshToken), mock.CreateRefreshTokenParams.TokenHash)
	assert.Greater(t, resp.ExpiresIn, int32(0))
}

func TestTokenInvalidGrant(t *testing.T) {
	router := setup(&QuerierMock{GetRefreshTokenByHashError: pgx.ErrNoRows})

	code, raw, _ := makeProtoRequest(router, "/user/token",
		&pb.UserTokenRequest{GrantType: "refresh_token", RefreshToken: "bogus"}, nil)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, pcerrors.InvalidGrant, decodeErrorEnvelope(t, raw))

	code, raw, _ = makeProtoRequest(router, "/user/token",
		&pb.UserTokenRequest{GrantType: "password"}, nil)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, pcerrors.InvalidGrant, decodeErrorEnvelope(t, raw))
}

func TestForgotPasswordAlwaysSucceeds(t *testing.T) {
	router := setup(&QuerierMock{})

	resp := &pb.UserChangeResponse{}
	code, _, err := makeProtoRequest(router, "/user/forgot_password",
		&pb.EmailRequest{Email: "anyone@test.com"}, resp)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.True(t, resp.Success.GetValue())
}

func TestChangeEmailSuccess(t *testing.T) {
	user := testUser(t, "secret-pass")
	router := setup(&QuerierMock{GetUserByUUIDResult: user, UpdateUserEmailResult: 1})

	resp := &pb.UserChangeResponse{}
	code, _, err := makeProtoRequest(router, "/user/change_email",
		&pb.UserChangeEmailRequest{Email: "new@test.com", Password: "secret-pass"}, resp)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.True(t, resp.Success.GetValue())
}

func TestChangeEmailWrongPassword(t *testing.T) {
	user := testUser(t, "secret-pass")
	router := setup(&QuerierMock{GetUserByUUIDResult: user})

	resp := &pb.UserChangeResponse{}
	code, _, err := makeProtoRequest(router, "/user/change_email",
		&pb.UserChangeEmailRequest{Email: "new@test.com", Password: "wrong"}, resp)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.False(t, resp.Success.GetValue())
	assert.Equal(t, pcerrors.IncorrectPassword, resp.MessageId)
}

func TestChangePasswordRevokesRefreshTokens(t *testing.T) {
	user := testUser(t, "old-password")
	mock := &QuerierMock{GetUserByUUIDResult: user, UpdateUserPasswordResult: 1, RevokeAllRefreshTokensResult: 2}
	router := setup(mock)

	resp := &pb.UserChangeResponse{}
	code, _, err := makeProtoRequest(router, "/user/change_password",
		&pb.UserChangePasswordRequest{OldPassword: "old-password", NewPassword: "new-password"}, resp)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.True(t, resp.Success.GetValue())
	assert.Equal(t, user.ID, mock.RevokeAllRefreshTokensUserID)
	assert.True(t, auth.CheckPassword(mock.UpdateUserPasswordParams.PasswordHash, "new-password"))
}

func TestDeleteAccount(t *testing.T) {
	user := testUser(t, "secret-pass")
	mock := &QuerierMock{GetUserByUUIDResult: user, SoftDeleteUserResult: 1, RevokeAllRefreshTokensResult: 1}
	router := setup(mock)

	resp := &pb.UserChangeResponse{}
	code, _, err := makeProtoRequest(router, "/user/delete_account", &pb.BasicRequest{}, resp)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.True(t, resp.Success.GetValue())
	assert.Equal(t, user.ID, mock.SoftDeletedUserID)
}
