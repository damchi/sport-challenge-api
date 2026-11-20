// Package db opens the PostgreSQL connection pool.
package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// connectTimeout bounds each new connection, so a database that does not
// answer cannot block a request (or a readiness probe) indefinitely.
const connectTimeout = 5 * time.Second

// NewPool creates the pgx pool for databaseURL, holding at most maxConns
// connections. maxConns wins over a pool_max_conns of the URL: each binary
// sizes its pool against the max_connections of the database (see
// docs/deploiement.md). It does not connect: the first connection is opened
// on demand, so the API starts even when the database is down and reports it
// through /ready. pgx masks the password in its errors; the rest of the URL
// (host, user, database) may appear.
func NewPool(ctx context.Context, databaseURL string, maxConns int32) (*pgxpool.Pool, error) {
	if maxConns < 1 {
		return nil, errors.New("database pool size must be at least 1")
	}
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	cfg.ConnConfig.ConnectTimeout = connectTimeout
	cfg.MaxConns = maxConns
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}
	return pool, nil
}
