package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/hbmartin/podcast-backend/auth"
	"github.com/hbmartin/podcast-backend/db"
	"github.com/hbmartin/podcast-backend/middlewares"
	"github.com/hbmartin/podcast-backend/pcerrors"
	pb "github.com/hbmartin/podcast-backend/protos/api"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const (
	minPasswordLength = 12
	maxPasswordBytes  = 72 // bcrypt input limit
)

// PostUserLogin handles POST /user/login: email/password exchange for an
// access token (Api_UserLoginRequest -> Api_UserLoginResponse).
func (h Handlers) PostUserLogin(w http.ResponseWriter, r *http.Request) {
	req := &pb.UserLoginRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.InvalidGrant, "invalid request")
		return
	}
	if !h.allowCredentialRequest(w, r, req.Email) {
		return
	}

	if req.Email == "" {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.BlankEmail, "email is required")
		return
	}
	if req.Password == "" {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.BlankPassword, "password is required")
		return
	}
	if !validDeviceID(req.Device) {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.InvalidGrant, "device is required")
		return
	}

	user, err := h.Queries.GetUserByEmail(r.Context(), req.Email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			pcerrors.Write(w, http.StatusUnauthorized, pcerrors.EmailNotFound, "email not found")
			return
		}
		writeError(w, r, err)
		return
	}

	if !auth.CheckPassword(user.PasswordHash, req.Password) {
		pcerrors.Write(w, http.StatusUnauthorized, pcerrors.IncorrectPassword, "incorrect password")
		return
	}

	scope := scopeOrMobile(req.Scope)
	token, expiresIn, err := auth.MintAccessToken(user.Uuid, user.Email, scope)
	if err != nil {
		writeError(w, r, err)
		return
	}
	refreshToken, err := h.issueRefreshToken(r.Context(), h.Queries, user.ID, scope, req.Device, uuid.NewString(), nil)
	if err != nil {
		writeError(w, r, err)
		return
	}

	writeProto(w, http.StatusOK, &pb.UserLoginResponse{
		Token:        token,
		Uuid:         user.Uuid,
		Email:        user.Email,
		RefreshToken: refreshToken,
		ExpiresIn:    expiresIn,
	})
}

// PostUserRegister handles POST /user/register
// (Api_RegisterRequest -> Api_RegisterResponse).
func (h Handlers) PostUserRegister(w http.ResponseWriter, r *http.Request) {
	req := &pb.RegisterRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.UserRegisterFailed, "invalid request")
		return
	}
	if !h.allowCredentialRequest(w, r, req.Email) {
		return
	}

	if req.Email == "" {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.BlankEmail, "email is required")
		return
	}
	if _, err := mail.ParseAddress(req.Email); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.EmailInvalid, "email is invalid")
		return
	}
	if req.Password == "" {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.BlankPassword, "password is required")
		return
	}
	if !validDeviceID(req.Device) {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.UserRegisterFailed, "device is required")
		return
	}
	if len(req.Password) < minPasswordLength {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.PasswordInvalid, "password is too short")
		return
	}
	if len(req.Password) > maxPasswordBytes {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.PasswordInvalid, "password is too long")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, r, err)
		return
	}

	scope := scopeOrMobile(req.Scope)
	var user db.User
	var refreshToken string
	err = h.Queries.InTx(r.Context(), func(q db.Querier) error {
		var createErr error
		user, createErr = q.CreateUser(r.Context(), db.CreateUserParams{
			Uuid: uuid.NewString(), Email: req.Email, PasswordHash: hash, Scope: scope,
		})
		if createErr != nil {
			return createErr
		}
		refreshToken, createErr = h.issueRefreshToken(r.Context(), q, user.ID, scope, req.Device, uuid.NewString(), nil)
		return createErr
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			pcerrors.Write(w, http.StatusConflict, pcerrors.EmailTaken, "email already in use")
			return
		}
		writeError(w, r, err)
		return
	}

	token, expiresIn, err := auth.MintAccessToken(user.Uuid, user.Email, scope)
	if err != nil {
		writeError(w, r, err)
		return
	}

	writeProto(w, http.StatusOK, &pb.RegisterResponse{
		Success:      wrapperspb.Bool(true),
		Token:        token,
		Uuid:         user.Uuid,
		RefreshToken: refreshToken,
		ExpiresIn:    expiresIn,
	})
}

// PostUserToken handles POST /user/token: OAuth-style token issuance. Only
// the refresh_token grant is supported (Api_UserTokenRequest ->
// Api_TokenLoginResponse with a rotated refresh token).
func (h Handlers) PostUserToken(w http.ResponseWriter, r *http.Request) {
	req := &pb.UserTokenRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.InvalidGrant, "invalid request")
		return
	}
	if !h.allowCredentialRequest(w, r, auth.HashRefreshToken(req.RefreshToken)) {
		return
	}

	if req.GrantType != "refresh_token" || !auth.ValidRefreshToken(req.RefreshToken) || !validDeviceID(req.Device) {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.InvalidGrant, "unsupported grant type")
		return
	}

	var response *pb.TokenLoginResponse
	invalidGrant := false
	err := h.Queries.InTx(r.Context(), func(q db.Querier) error {
		stored, err := q.GetRefreshTokenByHash(r.Context(), auth.HashRefreshToken(req.RefreshToken))
		if err != nil {
			return err
		}
		if stored.RevokedAt != nil {
			_, err = q.RevokeRefreshTokenFamily(r.Context(), stored.FamilyID)
			invalidGrant = true
			return err
		}
		if time.Now().After(stored.ExpiresAt) || stored.DeviceID != req.Device {
			invalidGrant = true
			return nil
		}
		user, err := q.GetUserByID(r.Context(), stored.UserID)
		if err != nil {
			return err
		}
		accessToken, expiresIn, err := auth.MintAccessToken(user.Uuid, user.Email, stored.Scope)
		if err != nil {
			return err
		}
		newRefresh, err := h.rotateRefreshToken(r.Context(), q, stored, user.ID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				invalidGrant = true
				return nil
			}
			return err
		}
		response = &pb.TokenLoginResponse{
			Email:        user.Email,
			Uuid:         user.Uuid,
			IsNew:        false,
			AccessToken:  accessToken,
			TokenType:    "Bearer",
			ExpiresIn:    expiresIn,
			RefreshToken: newRefresh,
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			pcerrors.Write(w, http.StatusBadRequest, pcerrors.InvalidGrant, "refresh token is invalid or expired")
			return
		}
		writeError(w, r, err)
		return
	}
	if invalidGrant {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.InvalidGrant, "refresh token is invalid or expired")
		return
	}
	writeProto(w, http.StatusOK, response)
}

func (h Handlers) rotateRefreshToken(ctx context.Context, q db.Querier, old db.RefreshToken, userID int64) (string, error) {
	revoked, err := q.RevokeRefreshToken(ctx, old.TokenHash)
	if err != nil {
		return "", err
	}
	if revoked != 1 {
		_, _ = q.RevokeRefreshTokenFamily(ctx, old.FamilyID)
		return "", pgx.ErrNoRows
	}
	return h.issueRefreshToken(ctx, q, userID, old.Scope, old.DeviceID, old.FamilyID, &old.ID)
}

func (h Handlers) issueRefreshToken(ctx context.Context, q db.Querier, userID int64, scope, device, family string, rotatedFrom *int64) (string, error) {
	token, hash, err := auth.NewRefreshToken()
	if err != nil {
		return "", err
	}
	_, err = q.CreateRefreshToken(ctx, db.CreateRefreshTokenParams{
		UserID: userID, TokenHash: hash, Scope: scope,
		ExpiresAt: time.Now().Add(h.Config.RefreshTokenTTL), FamilyID: family,
		DeviceID: device, RotatedFromID: rotatedFrom,
	})
	return token, err
}

func validDeviceID(device string) bool {
	device = strings.TrimSpace(device)
	return device != "" && len(device) <= 256
}

// PostUserTokenRevoke revokes the complete refresh-token family. Token
// possession is sufficient; unknown or inactive tokens are idempotent.
func (h Handlers) PostUserTokenRevoke(w http.ResponseWriter, r *http.Request) {
	req := &pb.UserRevokeRequest{}
	if err := bindProto(r, req); err != nil || !auth.ValidRefreshToken(req.RefreshToken) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if !h.allowCredentialRequest(w, r, auth.HashRefreshToken(req.RefreshToken)) {
		return
	}
	err := h.Queries.InTx(r.Context(), func(q db.Querier) error {
		stored, err := q.GetRefreshTokenByHash(r.Context(), auth.HashRefreshToken(req.RefreshToken))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		_, err = q.RevokeRefreshTokenFamily(r.Context(), stored.FamilyID)
		return err
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// PostResetPassword consumes an operator-issued one-time code.
func (h Handlers) PostResetPassword(w http.ResponseWriter, r *http.Request) {
	req := &pb.UserResetPasswordRequest{}
	if err := bindProto(r, req); err != nil || !validNewPassword(req.Password) || req.ResetPasswordToken == "" || !validDeviceID(req.Device) || strings.TrimSpace(req.Email) == "" {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.PasswordInvalid, "invalid reset request")
		return
	}
	if !h.allowCredentialRequest(w, r, req.Email) {
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, r, err)
		return
	}
	err = h.Queries.InTx(r.Context(), func(q db.Querier) error {
		user, err := q.GetUserByEmail(r.Context(), strings.TrimSpace(req.Email))
		if err != nil {
			return err
		}
		userID, err := q.ConsumePasswordResetCode(r.Context(), auth.HashRefreshToken(req.ResetPasswordToken))
		if err != nil {
			return err
		}
		if userID != user.ID {
			return pgx.ErrNoRows
		}
		if _, err = q.UpdateUserPassword(r.Context(), db.UpdateUserPasswordParams{ID: userID, PasswordHash: hash}); err != nil {
			return err
		}
		_, err = q.RevokeAllRefreshTokens(r.Context(), userID)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.InvalidGrant, "reset code is invalid or expired")
		return
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, &pb.UserChangeResponse{Success: wrapperspb.Bool(true)})
}

func validNewPassword(password string) bool {
	return len(password) >= minPasswordLength && len(password) <= maxPasswordBytes
}

func (h Handlers) allowCredentialRequest(w http.ResponseWriter, r *http.Request, identifier string) bool {
	digest := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(identifier))))
	checks := []struct {
		key   string
		limit int64
	}{{"credential:ip:" + middlewares.ClientIP(r, h.TrustProxyHeaders), 10}, {"credential:identifier:" + hex.EncodeToString(digest[:]), 5}}
	for _, check := range checks {
		allowed, retry, err := redisLimit(r.Context(), h.Redis, check.key, check.limit, time.Minute)
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return false
		}
		if !allowed {
			writeRateLimited(w, retry)
			return false
		}
	}
	return true
}

// PostChangeEmail handles POST /user/change_email (authenticated).
func (h Handlers) PostChangeEmail(w http.ResponseWriter, r *http.Request) {
	req := &pb.UserChangeEmailRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.EmailInvalid, "invalid request")
		return
	}

	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	if _, err := mail.ParseAddress(req.Email); err != nil {
		writeProto(w, http.StatusOK, changeFailure(pcerrors.EmailInvalid, "email is invalid"))
		return
	}

	if !auth.CheckPassword(user.PasswordHash, req.Password) {
		writeProto(w, http.StatusOK, changeFailure(pcerrors.IncorrectPassword, "incorrect password"))
		return
	}

	if _, err := h.Queries.UpdateUserEmail(r.Context(), db.UpdateUserEmailParams{ID: user.ID, Email: req.Email}); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeProto(w, http.StatusOK, changeFailure(pcerrors.EmailTaken, "email already in use"))
			return
		}
		writeError(w, r, err)
		return
	}

	writeProto(w, http.StatusOK, &pb.UserChangeResponse{Success: wrapperspb.Bool(true)})
}

// PostChangePassword handles POST /user/change_password (authenticated).
// All refresh tokens are revoked on success.
func (h Handlers) PostChangePassword(w http.ResponseWriter, r *http.Request) {
	req := &pb.UserChangePasswordRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.PasswordInvalid, "invalid request")
		return
	}

	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	if !auth.CheckPassword(user.PasswordHash, req.OldPassword) {
		writeProto(w, http.StatusOK, changeFailure(pcerrors.IncorrectPassword, "incorrect password"))
		return
	}

	if len(req.NewPassword) < minPasswordLength {
		writeProto(w, http.StatusOK, changeFailure(pcerrors.PasswordInvalid, "password is too short"))
		return
	}
	if len(req.NewPassword) > maxPasswordBytes {
		writeProto(w, http.StatusOK, changeFailure(pcerrors.PasswordInvalid, "password is too long"))
		return
	}

	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeError(w, r, err)
		return
	}

	if _, err := h.Queries.UpdateUserPassword(r.Context(), db.UpdateUserPasswordParams{ID: user.ID, PasswordHash: hash}); err != nil {
		writeError(w, r, err)
		return
	}

	if _, err := h.Queries.RevokeAllRefreshTokens(r.Context(), user.ID); err != nil {
		writeError(w, r, err)
		return
	}

	writeProto(w, http.StatusOK, &pb.UserChangeResponse{Success: wrapperspb.Bool(true)})
}

// PostDeleteAccount handles POST /user/delete_account (authenticated):
// erases the social profile (GDPR — profile PII deleted, handle tombstoned;
// docs/SocialModeration.md), soft-deletes the user and revokes all refresh
// tokens. Every mutation shares one transaction so failures cannot leave a
// partially erased account; social erasure is idempotent for never-joined
// accounts.
func (h Handlers) PostDeleteAccount(w http.ResponseWriter, r *http.Request) {
	req := &pb.BasicRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.AccessDenied, "invalid request")
		return
	}

	user, ok := h.currentDbUser(w, r)
	if !ok {
		return
	}

	err := h.Queries.InTx(r.Context(), func(q db.Querier) error {
		// A deleted account's devices must stop receiving pushes (QA finding).
		if err := q.ClearPushStateForUser(r.Context(), user.ID); err != nil {
			return err
		}
		if err := socialEraseWithQuerier(r.Context(), q, user.ID); err != nil {
			return err
		}
		if _, err := q.SoftDeleteUser(r.Context(), user.ID); err != nil {
			return err
		}
		_, err := q.RevokeAllRefreshTokens(r.Context(), user.ID)
		return err
	})
	if err != nil {
		writeError(w, r, err)
		return
	}

	writeProto(w, http.StatusOK, &pb.UserChangeResponse{Success: wrapperspb.Bool(true)})
}

// currentDbUser resolves the authenticated context user to its database row.
// Replies 401 and returns ok=false when the token subject no longer exists.
func (h Handlers) currentDbUser(w http.ResponseWriter, r *http.Request) (db.User, bool) {
	ctxUser := getUser(r.Context())
	if ctxUser == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return db.User{}, false
	}

	user, err := h.Queries.GetUserByUUID(r.Context(), ctxUser.UUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			w.WriteHeader(http.StatusUnauthorized)
			return db.User{}, false
		}
		writeError(w, r, err)
		return db.User{}, false
	}
	return user, true
}

func changeFailure(messageID string, message string) *pb.UserChangeResponse {
	return &pb.UserChangeResponse{
		Success:   wrapperspb.Bool(false),
		Message:   message,
		MessageId: messageID,
	}
}

func scopeOrMobile(scope string) string {
	if scope == "" {
		return "mobile"
	}
	return scope
}
