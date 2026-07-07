package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/mail"
	"time"

	"goapi-template/auth"
	"goapi-template/db"
	"goapi-template/pcerrors"
	pb "goapi-template/protos/api"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const minPasswordLength = 6

// PostUserLogin handles POST /user/login: email/password exchange for an
// access token (Api_UserLoginRequest -> Api_UserLoginResponse).
func (h Handlers) PostUserLogin(w http.ResponseWriter, r *http.Request) {
	req := &pb.UserLoginRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.InvalidGrant, "invalid request")
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

	token, _, err := auth.MintAccessToken(user.Uuid, user.Email, scopeOrMobile(req.Scope))
	if err != nil {
		writeError(w, r, err)
		return
	}

	writeProto(w, http.StatusOK, &pb.UserLoginResponse{
		Token: token,
		Uuid:  user.Uuid,
		Email: user.Email,
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
	if len(req.Password) < minPasswordLength {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.PasswordInvalid, "password is too short")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, r, err)
		return
	}

	scope := scopeOrMobile(req.Scope)
	user, err := h.Queries.CreateUser(r.Context(), db.CreateUserParams{
		Uuid:         uuid.NewString(),
		Email:        req.Email,
		PasswordHash: hash,
		Scope:        scope,
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

	token, _, err := auth.MintAccessToken(user.Uuid, user.Email, scope)
	if err != nil {
		writeError(w, r, err)
		return
	}

	writeProto(w, http.StatusOK, &pb.RegisterResponse{
		Success: wrapperspb.Bool(true),
		Token:   token,
		Uuid:    user.Uuid,
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

	if req.GrantType != "refresh_token" || req.RefreshToken == "" {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.InvalidGrant, "unsupported grant type")
		return
	}

	stored, err := h.Queries.GetRefreshTokenByHash(r.Context(), auth.HashRefreshToken(req.RefreshToken))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			pcerrors.Write(w, http.StatusBadRequest, pcerrors.InvalidGrant, "refresh token is invalid or expired")
			return
		}
		writeError(w, r, err)
		return
	}

	user, err := h.Queries.GetUserByID(r.Context(), stored.UserID)
	if err != nil {
		writeError(w, r, err)
		return
	}

	accessToken, expiresIn, err := auth.MintAccessToken(user.Uuid, user.Email, stored.Scope)
	if err != nil {
		writeError(w, r, err)
		return
	}

	newRefresh, err := h.rotateRefreshToken(r.Context(), stored, user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}

	writeProto(w, http.StatusOK, &pb.TokenLoginResponse{
		Email:        user.Email,
		Uuid:         user.Uuid,
		IsNew:        false,
		AccessToken:  accessToken,
		TokenType:    "Bearer",
		ExpiresIn:    expiresIn,
		RefreshToken: newRefresh,
	})
}

func (h Handlers) rotateRefreshToken(ctx context.Context, old db.RefreshToken, userID int64) (string, error) {
	if _, err := h.Queries.RevokeRefreshToken(ctx, old.TokenHash); err != nil {
		return "", err
	}

	token, hash, err := auth.NewRefreshToken()
	if err != nil {
		return "", err
	}

	_, err = h.Queries.CreateRefreshToken(ctx, db.CreateRefreshTokenParams{
		UserID:    userID,
		TokenHash: hash,
		Scope:     old.Scope,
		ExpiresAt: time.Now().Add(h.Config.RefreshTokenTTL),
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// PostForgotPassword handles POST /user/forgot_password. No mailer is wired
// up yet; the request is acknowledged so the client flow completes, and the
// event is logged for the operator.
func (h Handlers) PostForgotPassword(w http.ResponseWriter, r *http.Request) {
	req := &pb.EmailRequest{}
	if err := bindProto(r, req); err != nil {
		pcerrors.Write(w, http.StatusBadRequest, pcerrors.EmailInvalid, "invalid request")
		return
	}

	slog.Info("Password reset requested (no mailer configured)", "email", req.Email)

	writeProto(w, http.StatusOK, &pb.UserChangeResponse{Success: wrapperspb.Bool(true)})
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
// soft-deletes the user and revokes all refresh tokens.
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

	if _, err := h.Queries.SoftDeleteUser(r.Context(), user.ID); err != nil {
		writeError(w, r, err)
		return
	}
	if _, err := h.Queries.RevokeAllRefreshTokens(r.Context(), user.ID); err != nil {
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
