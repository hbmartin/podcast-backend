package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
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
		&pb.UserLoginRequest{Email: "mail@test.com", Password: "secret-pass", Scope: "mobile", Device: "install-1"}, resp)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, testUserUUID, resp.Uuid)
	assert.Equal(t, "mail@test.com", resp.Email)
	assert.NotEmpty(t, resp.RefreshToken)

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
		&pb.UserLoginRequest{Email: "mail@test.com", Password: "wrong", Device: "install-1"}, nil)

	assert.Equal(t, http.StatusUnauthorized, code)
	assert.Equal(t, pcerrors.IncorrectPassword, decodeErrorEnvelope(t, raw))
}

func TestLoginUnknownEmail(t *testing.T) {
	router := setup(&QuerierMock{GetUserByEmailError: pgx.ErrNoRows})

	code, raw, _ := makeProtoRequest(router, "/user/login",
		&pb.UserLoginRequest{Email: "nobody@test.com", Password: "whatever", Device: "install-1"}, nil)

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
		&pb.RegisterRequest{Email: "new@test.com", Password: "long-enough-password", Scope: "mobile", Device: "install-1"}, resp)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.True(t, resp.Success.GetValue())
	assert.Equal(t, testUserUUID, resp.Uuid)
	assert.NotEmpty(t, resp.Token)
	assert.NotEmpty(t, resp.RefreshToken)
	// password must be stored hashed
	assert.NotEqual(t, "long-enough-password", mock.CreateUserParams.PasswordHash)
	assert.True(t, auth.CheckPassword(mock.CreateUserParams.PasswordHash, "long-enough-password"))
}

func TestRegisterEmailTaken(t *testing.T) {
	router := setup(&QuerierMock{CreateUserError: &pgconn.PgError{Code: "23505"}})

	code, raw, _ := makeProtoRequest(router, "/user/register",
		&pb.RegisterRequest{Email: "dup@test.com", Password: "long-enough-password", Device: "install-1"}, nil)

	assert.Equal(t, http.StatusConflict, code)
	assert.Equal(t, pcerrors.EmailTaken, decodeErrorEnvelope(t, raw))
}

func TestRegisterValidation(t *testing.T) {
	router := setup(&QuerierMock{})

	code, raw, _ := makeProtoRequest(router, "/user/register",
		&pb.RegisterRequest{Email: "not-an-email", Password: "long-enough-password", Device: "install-1"}, nil)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, pcerrors.EmailInvalid, decodeErrorEnvelope(t, raw))

	code, raw, _ = makeProtoRequest(router, "/user/register",
		&pb.RegisterRequest{Email: "a@b.co", Password: "tiny", Device: "install-1"}, nil)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, pcerrors.PasswordInvalid, decodeErrorEnvelope(t, raw))

	code, raw, _ = makeProtoRequest(router, "/user/register",
		&pb.RegisterRequest{Email: "a@b.co", Password: strings.Repeat("x", maxPasswordBytes+1), Device: "install-1"}, nil)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, pcerrors.PasswordInvalid, decodeErrorEnvelope(t, raw))
}

func TestTokenRefreshGrant(t *testing.T) {
	token, hash, err := auth.NewRefreshToken()
	assert.NoError(t, err)

	mock := &QuerierMock{
		GetRefreshTokenByHashResult: db.RefreshToken{
			ID: 1, UserID: 42, TokenHash: hash, Scope: "mobile",
			ExpiresAt: time.Now().Add(time.Hour), FamilyID: testUserUUID, DeviceID: "install-1",
		},
		GetUserByIDResult:        db.User{ID: 42, Uuid: testUserUUID, Email: "mail@test.com"},
		RevokeRefreshTokenResult: 1,
	}
	router := setup(mock)

	resp := &pb.TokenLoginResponse{}
	code, _, err := makeProtoRequest(router, "/user/token",
		&pb.UserTokenRequest{GrantType: "refresh_token", RefreshToken: token, Scope: "mobile", Device: "install-1"}, resp)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "Bearer", resp.TokenType)
	assert.NotEmpty(t, resp.AccessToken)
	assert.NotEmpty(t, resp.RefreshToken)
	assert.NotEqual(t, token, resp.RefreshToken, "refresh token must rotate")
	assert.Equal(t, hash, mock.RevokedTokenHash, "old token must be revoked")
	assert.Equal(t, auth.HashRefreshToken(resp.RefreshToken), mock.CreateRefreshTokenParams.TokenHash)
	assert.Greater(t, resp.ExpiresIn, int32(0))
	assert.Equal(t, 1, mock.InTxCalls)
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

func storedRefreshToken(t *testing.T, expiresAt time.Time, revokedAt *time.Time) (string, db.RefreshToken) {
	t.Helper()
	token, hash, err := auth.NewRefreshToken()
	assert.NoError(t, err)
	return token, db.RefreshToken{
		ID: 1, UserID: 42, TokenHash: hash, Scope: "mobile",
		ExpiresAt: expiresAt, FamilyID: testUserUUID, DeviceID: "install-1",
		RevokedAt: revokedAt,
	}
}

func TestTokenReuseRevokesFamily(t *testing.T) {
	revokedAt := time.Now().Add(-time.Minute)
	token, stored := storedRefreshToken(t, time.Now().Add(time.Hour), &revokedAt)
	mock := &QuerierMock{GetRefreshTokenByHashResult: stored}
	router := setup(mock)

	code, raw, _ := makeProtoRequest(router, "/user/token",
		&pb.UserTokenRequest{GrantType: "refresh_token", RefreshToken: token, Scope: "mobile", Device: "install-1"}, nil)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, pcerrors.InvalidGrant, decodeErrorEnvelope(t, raw))
	assert.Equal(t, testUserUUID, mock.RevokedFamilyID, "reuse of a rotated token must revoke the family")
}

func TestTokenDeviceMismatchRevokesFamily(t *testing.T) {
	token, stored := storedRefreshToken(t, time.Now().Add(time.Hour), nil)
	mock := &QuerierMock{GetRefreshTokenByHashResult: stored}
	router := setup(mock)

	code, raw, _ := makeProtoRequest(router, "/user/token",
		&pb.UserTokenRequest{GrantType: "refresh_token", RefreshToken: token, Scope: "mobile", Device: "other-install"}, nil)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, pcerrors.InvalidGrant, decodeErrorEnvelope(t, raw))
	assert.Equal(t, testUserUUID, mock.RevokedFamilyID, "live token from the wrong device must revoke the family")
}

func TestTokenExpiredRejectedWithoutFamilyRevocation(t *testing.T) {
	token, stored := storedRefreshToken(t, time.Now().Add(-time.Hour), nil)
	mock := &QuerierMock{GetRefreshTokenByHashResult: stored}
	router := setup(mock)

	code, raw, _ := makeProtoRequest(router, "/user/token",
		&pb.UserTokenRequest{GrantType: "refresh_token", RefreshToken: token, Scope: "mobile", Device: "install-1"}, nil)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, pcerrors.InvalidGrant, decodeErrorEnvelope(t, raw))
	assert.Empty(t, mock.RevokedFamilyID, "expiry alone must not revoke the family")
}

func TestPasswordResetConsumesCodeForMatchingAccountAndRevokesFamilies(t *testing.T) {
	user := testUser(t, "old-password")
	mock := &QuerierMock{
		GetUserByEmailResult:           user,
		ConsumePasswordResetCodeResult: user.ID,
		UpdateUserPasswordResult:       1,
		RevokeAllRefreshTokensResult:   2,
	}
	router := setup(mock)

	resp := &pb.UserChangeResponse{}
	code, _, err := makeProtoRequest(router, "/user/reset_password",
		&pb.UserResetPasswordRequest{
			Email: "mail@test.com", ResetPasswordToken: "one-time-code",
			Password: "new-password-12", Device: "install-1",
		}, resp)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.True(t, resp.Success.GetValue())
	assert.Equal(t, auth.HashRefreshToken("one-time-code"), mock.ConsumedPasswordResetCodeHash)
	assert.True(t, auth.CheckPassword(mock.UpdateUserPasswordParams.PasswordHash, "new-password-12"))
	assert.Equal(t, user.ID, mock.RevokeAllRefreshTokensUserID)
}

func TestPasswordResetRejectsCodeIssuedForDifferentAccount(t *testing.T) {
	user := testUser(t, "old-password")
	mock := &QuerierMock{GetUserByEmailResult: user, ConsumePasswordResetCodeResult: user.ID + 1}
	router := setup(mock)

	code, raw, _ := makeProtoRequest(router, "/user/reset_password",
		&pb.UserResetPasswordRequest{
			Email: user.Email, ResetPasswordToken: "wrong-account-code",
			Password: "new-password-12", Device: "install-1",
		}, nil)

	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, pcerrors.InvalidGrant, decodeErrorEnvelope(t, raw))
	assert.Nil(t, mock.UpdateUserPasswordParams)
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

func TestChangePasswordRejectsBcryptOverflow(t *testing.T) {
	user := testUser(t, "old-password")
	mock := &QuerierMock{GetUserByUUIDResult: user}
	router := setup(mock)

	resp := &pb.UserChangeResponse{}
	code, _, err := makeProtoRequest(router, "/user/change_password",
		&pb.UserChangePasswordRequest{
			OldPassword: "old-password",
			NewPassword: strings.Repeat("x", maxPasswordBytes+1),
		}, resp)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.False(t, resp.Success.GetValue())
	assert.Equal(t, pcerrors.PasswordInvalid, resp.MessageId)
	assert.Nil(t, mock.UpdateUserPasswordParams)
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
	assert.Equal(t, 1, mock.InTxCalls, "all account erasure mutations share one transaction")
}
