// Package testdb gives each integration test its own PostgreSQL database,
// built from the migrations and dropped at the end of the test.
package testdb

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// New creates a temporary database on the server of TEST_DATABASE_URL
// (a maintenance database such as postgres), applies every
// migrations/*.up.sql file in order, and drops it when the test ends.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Fatal("TEST_DATABASE_URL is not set: run make test-integration (after make docker-up)")
	}
	ctx := t.Context()

	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	name := "test_" + strings.ToLower(rand.Text())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(func() {
		// t.Context() is already cancelled when cleanups run.
		ctx := context.Background()
		if _, err := admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Errorf("drop test database: %v", err)
		}
		_ = admin.Close(ctx)
	})

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)

	applyMigrations(t, pool)
	return pool
}

func applyMigrations(t testing.TB, pool *pgxpool.Pool) {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "migrations")
	files, err := filepath.Glob(filepath.Join(dir, "*.up.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no migration found in %s: %v", dir, err)
	}
	slices.Sort(files) // NNNNNN_ prefix: lexical order is the migration order
	for _, f := range files {
		sql, err := os.ReadFile(f) //nolint:gosec // path built from the repository's migrations directory
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		// Without arguments, pgx uses the simple protocol: a file may hold
		// several statements.
		if _, err := pool.Exec(t.Context(), string(sql)); err != nil {
			t.Fatalf("apply %s: %v", filepath.Base(f), err)
		}
	}
}
