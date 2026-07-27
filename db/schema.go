package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ExpectedSchemaVersion is the newest migration required by this binary.
// Dedicated non-web roles wait for the web role to reach this version.
const ExpectedSchemaVersion = 31

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func SchemaReady(ctx context.Context, query rowQuerier) error {
	var version int
	var dirty bool
	if err := query.QueryRow(ctx, `SELECT version, dirty FROM schema_migrations LIMIT 1`).Scan(&version, &dirty); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if dirty {
		return errors.New("database migration state is dirty")
	}
	if version < ExpectedSchemaVersion {
		return fmt.Errorf("schema version %d is older than required version %d", version, ExpectedSchemaVersion)
	}
	return nil
}

func WaitForSchema(ctx context.Context, query rowQuerier, timeout time.Duration) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastErr error
	for {
		if err := SchemaReady(waitCtx, query); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("schema did not become ready: %w", lastErr)
		case <-ticker.C:
		}
	}
}
