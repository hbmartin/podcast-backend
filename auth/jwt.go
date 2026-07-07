package auth

import (
	"time"

	"goapi-template/config"
	"goapi-template/errs"

	"github.com/golang-jwt/jwt/v5"
)

var authConfig *config.AuthConfiguration

// Init stores the auth configuration used for minting and validating tokens.
// Must be called once at startup before any token operation.
func Init(configValues *config.AuthConfiguration) {
	authConfig = configValues
}

type accessClaims struct {
	Email string `json:"email"`
	Scope string `json:"scope"`
	jwt.RegisteredClaims
}

// MintAccessToken creates a signed HS256 access token for the user identified
// by uuid. Returns the token and its lifetime in seconds.
func MintAccessToken(uuid string, email string, scope string) (string, int32, error) {
	const op errs.Op = "auth/MintAccessToken"

	now := time.Now()
	claims := accessClaims{
		Email: email,
		Scope: scope,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   uuid,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(authConfig.AccessTokenTTL)),
		},
	}

	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(authConfig.JWTSecret))
	if err != nil {
		return "", 0, errs.E(op, errs.Internal, err)
	}
	return token, int32(authConfig.AccessTokenTTL.Seconds()), nil
}

// ValidateAccessToken verifies signature and expiry and returns the token's
// user identity.
func ValidateAccessToken(token string) (*User, error) {
	const op errs.Op = "auth/ValidateAccessToken"

	claims := &accessClaims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		return []byte(authConfig.JWTSecret), nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil || !parsed.Valid {
		return nil, errs.E(op, errs.Unauthenticated, errs.Code("token_invalid"), "invalid access token")
	}

	if claims.Subject == "" {
		return nil, errs.E(op, errs.Unauthenticated, errs.Code("token_invalid"), "token has no subject")
	}

	return &User{UUID: claims.Subject, Email: claims.Email, Scope: claims.Scope}, nil
}
