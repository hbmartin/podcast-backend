package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the full database dependency handed to handlers and services:
// all queries plus transaction support.
type Store interface {
	Querier
	// InTx runs fn inside a database transaction. The Querier passed to fn is
	// bound to that transaction; the transaction commits when fn returns nil
	// and rolls back otherwise.
	InTx(ctx context.Context, fn func(q Querier) error) error
}

type pgStore struct {
	*Queries
	pool *pgxpool.Pool
}

// NewStore wraps a pgx pool in the Store interface.
func NewStore(pool *pgxpool.Pool) Store {
	return &pgStore{Queries: New(pool), pool: pool}
}

func (s *pgStore) InTx(ctx context.Context, fn func(q Querier) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := fn(s.Queries.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
