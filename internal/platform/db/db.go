// Package db opens the PostgreSQL connection pool.
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// connectTimeout bounds each new connection, so a database that does not
// answer cannot block a request (or a readiness probe) indefinitely.
const connectTimeout = 5 * time.Second

// NewPool creates the pgx pool for databaseURL. It does not connect: the
// first connection is opened on demand, so the API starts even when the
// database is down and reports it through /readyz. pgx masks the password in
// its errors; the rest of the URL (host, user, database) may appear.
func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	cfg.ConnConfig.ConnectTimeout = connectTimeout
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}
	return pool, nil
}
