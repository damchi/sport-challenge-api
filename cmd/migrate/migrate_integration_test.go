//go:build integration

package main

import (
	"errors"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damchi/sport-challenge-api/internal/platform/db/testdb"
)

// env returns the getenv of a run against the database at url.
func env(url string) func(string) string {
	return func(key string) string {
		if key == "DATABASE_URL" {
			return url
		}
		return ""
	}
}

// version reads the state golang-migrate keeps in schema_migrations.
func version(t *testing.T, pool *pgxpool.Pool) (version int, dirty bool) {
	t.Helper()
	err := pool.QueryRow(t.Context(), "SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty)
	if err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	return version, dirty
}

func TestUpAppliesEveryMigrationAndIsRepeatable(t *testing.T) {
	pool, url := testdb.NewEmpty(t)

	if err := run([]string{"up"}, env(url)); err != nil {
		t.Fatalf("first up: %v", err)
	}
	first, dirty := version(t, pool)
	if first == 0 || dirty {
		t.Fatalf("after up: version %d, dirty %v", first, dirty)
	}
	var tables int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM pg_tables WHERE schemaname = 'public' AND tablename = 'challenges'").Scan(&tables); err != nil || tables != 1 {
		t.Fatalf("the schema was not applied: %d challenges table(s), %v", tables, err)
	}

	// A deployment without a new migration changes nothing.
	if err := run([]string{"up"}, env(url)); err != nil {
		t.Fatalf("second up: %v", err)
	}
	if second, _ := version(t, pool); second != first {
		t.Fatalf("second up moved the version from %d to %d", first, second)
	}
}

func TestDownRollsBackTheGivenNumberOfMigrations(t *testing.T) {
	pool, url := testdb.NewEmpty(t)
	if err := run([]string{"up"}, env(url)); err != nil {
		t.Fatalf("up: %v", err)
	}
	head, _ := version(t, pool)

	if err := run([]string{"down", "1"}, env(url)); err != nil {
		t.Fatalf("down 1: %v", err)
	}
	if got, dirty := version(t, pool); got != head-1 || dirty {
		t.Fatalf("after down 1: version %d dirty %v, want %d clean", got, dirty, head-1)
	}
}

func TestDirtyDatabaseStopsEveryMigrationAndIsNeverForced(t *testing.T) {
	pool, url := testdb.NewEmpty(t)
	if err := run([]string{"up"}, env(url)); err != nil {
		t.Fatalf("up: %v", err)
	}
	head, _ := version(t, pool)
	// What a migration that failed halfway leaves behind.
	if _, err := pool.Exec(t.Context(), "UPDATE schema_migrations SET dirty = true"); err != nil {
		t.Fatalf("mark dirty: %v", err)
	}

	for _, args := range [][]string{{"up"}, {"down", "1"}} {
		err := run(args, env(url))
		if !errors.Is(err, errDirty) {
			t.Fatalf("run %v on a dirty database = %v, want %v", args, err, errDirty)
		}
		if got, dirty := version(t, pool); got != head || !dirty {
			t.Fatalf("run %v touched the dirty state: version %d dirty %v", args, got, dirty)
		}
	}

	// Only an explicit force, by a person, clears it.
	if err := run([]string{"force", strconv.Itoa(head)}, env(url)); err != nil {
		t.Fatalf("force: %v", err)
	}
	if got, dirty := version(t, pool); got != head || dirty {
		t.Fatalf("after force: version %d dirty %v, want %d clean", got, dirty, head)
	}
	if err := run([]string{"up"}, env(url)); err != nil {
		t.Fatalf("up after force: %v", err)
	}
}
