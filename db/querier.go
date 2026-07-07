package db

import "context"

// Querier is the dependency-injection seam between handlers/services and the
// sqlc-generated queries. Keep it in sync with queries.sql.go; tests provide
// hand-written mocks.
type Querier interface {
	PingDb(ctx context.Context) (int32, error)

	CreateUser(ctx context.Context, arg CreateUserParams) (User, error)
	GetUserByEmail(ctx context.Context, email string) (User, error)
	GetUserByUUID(ctx context.Context, uuid string) (User, error)
	GetUserByID(ctx context.Context, id int64) (User, error)
	UpdateUserEmail(ctx context.Context, arg UpdateUserEmailParams) (int64, error)
	UpdateUserPassword(ctx context.Context, arg UpdateUserPasswordParams) (int64, error)
	SoftDeleteUser(ctx context.Context, id int64) (int64, error)

	CreateRefreshToken(ctx context.Context, arg CreateRefreshTokenParams) (RefreshToken, error)
	GetRefreshTokenByHash(ctx context.Context, tokenHash string) (RefreshToken, error)
	RevokeRefreshToken(ctx context.Context, tokenHash string) (int64, error)
	RevokeAllRefreshTokens(ctx context.Context, userID int64) (int64, error)
}
