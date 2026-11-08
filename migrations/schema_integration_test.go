//go:build integration

// Package migrations holds only the tests of the schema: the business rules
// the migrations enforce with constraints, and their reversibility.
// golang-migrate reads the *.up.sql and *.down.sql files and ignores this one.
package migrations

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damchi/sport-challenge-api/internal/platform/db/testdb"
)

const (
	userID      = "00000000-0000-0000-0000-000000000001"
	challengeID = "00000000-0000-0000-0000-0000000000c1"
)

// fixture is a user and a two-week challenge the cases build on.
const fixture = `
INSERT INTO users (id, auth_subject) VALUES ('` + userID + `', 'schema|1');
INSERT INTO challenges (id, title, visibility, timezone, starts_at, ends_at, created_by)
VALUES ('` + challengeID + `', 'Spring', 'public', 'America/Montreal',
        '2026-12-01 05:00Z', '2026-12-15 05:00Z', '` + userID + `');`

// TestConstraints runs each statement on the fixture, in a transaction rolled
// back afterwards, and checks the constraint that refuses it (none: accepted).
func TestConstraints(t *testing.T) {
	pool := testdb.New(t)
	tests := []struct {
		name       string
		sql        string
		constraint string
	}{
		// sports (000003)
		{name: "factor for a sport without distance", sql: `INSERT INTO sports VALUES ('dance', 'none', 1)`, constraint: "sports_suggested_factor_by_mode"},
		{name: "no factor for a sport with distance", sql: `INSERT INTO sports VALUES ('canoe', 'required', NULL)`, constraint: "sports_suggested_factor_by_mode"},
		{name: "factor under 0.1", sql: `INSERT INTO sports VALUES ('canoe', 'required', 0.05)`, constraint: "sports_suggested_factor_check"},
		{name: "factor over 2", sql: `INSERT INTO sports VALUES ('canoe', 'required', 2.5)`, constraint: "sports_suggested_factor_check"},
		{name: "unknown distance mode", sql: `INSERT INTO sports VALUES ('canoe', 'sometimes', 1)`, constraint: "sports_distance_mode_check"},
		{name: "uppercase code", sql: `INSERT INTO sports VALUES ('Canoe', 'required', 1)`, constraint: "sports_code_check"},
		{name: "factor bounds included", sql: `INSERT INTO sports VALUES ('canoe', 'required', 0.1), ('kayak', 'optional', 2)`},

		// challenges (000004)
		{name: "valid challenge", sql: `INSERT INTO challenges (title, visibility, timezone, starts_at, ends_at, created_by) VALUES ('T', 'private', 'UTC', '2026-12-01Z', '2026-12-15Z', '` + userID + `')`},
		{name: "other visibility", sql: `INSERT INTO challenges (title, visibility, timezone, starts_at, ends_at, created_by) VALUES ('T', 'friends', 'UTC', '2026-12-01Z', '2026-12-15Z', '` + userID + `')`, constraint: "challenges_visibility_check"},
		{name: "end before start", sql: `INSERT INTO challenges (title, visibility, timezone, starts_at, ends_at, created_by) VALUES ('T', 'public', 'UTC', '2026-12-15Z', '2026-12-01Z', '` + userID + `')`, constraint: "challenges_window_check"},
		{name: "end at start", sql: `INSERT INTO challenges (title, visibility, timezone, starts_at, ends_at, created_by) VALUES ('T', 'public', 'UTC', '2026-12-01Z', '2026-12-01Z', '` + userID + `')`, constraint: "challenges_window_check"},
		{name: "unknown creator", sql: `INSERT INTO challenges (title, visibility, timezone, starts_at, ends_at, created_by) VALUES ('T', 'public', 'UTC', '2026-12-01Z', '2026-12-15Z', '00000000-0000-0000-0000-00000000dead')`, constraint: "challenges_created_by_fkey"},

		// categories (000004)
		{name: "the 3 categories", sql: `INSERT INTO categories (challenge_id, type, position) VALUES ('` + challengeID + `', 'cumulative_time', 1), ('` + challengeID + `', 'cumulative_distance', 2), ('` + challengeID + `', 'run_elimination', 3)`},
		{name: "unknown category type", sql: `INSERT INTO categories (challenge_id, type, position) VALUES ('` + challengeID + `', 'fastest', 1)`, constraint: "categories_type_check"},
		{name: "position 4", sql: `INSERT INTO categories (challenge_id, type, position) VALUES ('` + challengeID + `', 'cumulative_time', 4)`, constraint: "categories_position_check"},
		{name: "position 0", sql: `INSERT INTO categories (challenge_id, type, position) VALUES ('` + challengeID + `', 'cumulative_time', 0)`, constraint: "categories_position_check"},
		{name: "params not an object", sql: `INSERT INTO categories (challenge_id, type, position, params) VALUES ('` + challengeID + `', 'cumulative_time', 1, '[]')`, constraint: "categories_params_check"},
		{name: "same type twice", sql: `INSERT INTO categories (challenge_id, type, position) VALUES ('` + challengeID + `', 'cumulative_time', 1), ('` + challengeID + `', 'cumulative_time', 2)`, constraint: "categories_challenge_type_key"},
		{name: "same position twice", sql: `INSERT INTO categories (challenge_id, type, position) VALUES ('` + challengeID + `', 'cumulative_time', 1), ('` + challengeID + `', 'run_elimination', 1)`, constraint: "categories_challenge_position_key"},

		// participants (000004)
		{name: "active participant", sql: `INSERT INTO participants VALUES ('` + challengeID + `', '` + userID + `', 'active', '2026-12-02Z', NULL)`},
		{name: "abandoned participant", sql: `INSERT INTO participants VALUES ('` + challengeID + `', '` + userID + `', 'abandoned', '2026-12-02Z', '2026-12-03Z')`},
		{name: "abandoned without a date", sql: `INSERT INTO participants VALUES ('` + challengeID + `', '` + userID + `', 'abandoned', '2026-12-02Z', NULL)`, constraint: "participants_abandoned_at_check"},
		{name: "abandon date while active", sql: `INSERT INTO participants VALUES ('` + challengeID + `', '` + userID + `', 'active', '2026-12-02Z', '2026-12-03Z')`, constraint: "participants_abandoned_at_check"},
		{name: "abandoned before joining", sql: `INSERT INTO participants VALUES ('` + challengeID + `', '` + userID + `', 'abandoned', '2026-12-03Z', '2026-12-02Z')`, constraint: "participants_abandoned_after_joined_check"},
		{name: "unknown status", sql: `INSERT INTO participants VALUES ('` + challengeID + `', '` + userID + `', 'excluded', '2026-12-02Z', NULL)`, constraint: "participants_status_check"},
		{name: "joined twice", sql: `INSERT INTO participants VALUES ('` + challengeID + `', '` + userID + `', 'active', '2026-12-02Z', NULL), ('` + challengeID + `', '` + userID + `', 'active', '2026-12-04Z', NULL)`, constraint: "participants_pkey"},

		// No cascade: results stay (anonymization, soft deletion).
		{name: "deleting a challenge with participants", sql: `INSERT INTO participants VALUES ('` + challengeID + `', '` + userID + `', 'active', '2026-12-02Z', NULL); DELETE FROM challenges WHERE id = '` + challengeID + `'`, constraint: "participants_challenge_id_fkey"},
		{name: "deleting a user who created a challenge", sql: `DELETE FROM users WHERE id = '` + userID + `'`, constraint: "challenges_created_by_fkey"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := inRolledBackTx(t, pool, fixture+tt.sql)
			var pgErr *pgconn.PgError
			switch {
			case tt.constraint == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case tt.constraint == "":
			case !errors.As(err, &pgErr):
				t.Fatalf("error = %v, want constraint %s", err, tt.constraint)
			case pgErr.ConstraintName != tt.constraint:
				t.Fatalf("constraint %q (%s), want %s", pgErr.ConstraintName, pgErr.Message, tt.constraint)
			}
		})
	}
}

// TestNotNull checks the columns the service must always set.
func TestNotNull(t *testing.T) {
	pool := testdb.New(t)
	err := inRolledBackTx(t, pool, fixture+`INSERT INTO participants (challenge_id, user_id) VALUES ('`+challengeID+`', '`+userID+`')`)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23502" || pgErr.ColumnName != "joined_at" {
		t.Fatalf("error = %v, want joined_at NOT NULL (no default: set from platform/clock)", err)
	}
}

// TestDownThenUp rolls every migration back, newest first, checks that
// nothing but the empty schema remains, then applies them again.
func TestDownThenUp(t *testing.T) {
	pool := testdb.New(t) // every up applied
	run(t, pool, migrationFiles(t, "down"), true)

	var tables, functions int
	if err := pool.QueryRow(t.Context(), `SELECT
		(SELECT count(*) FROM pg_tables WHERE schemaname = 'public'),
		(SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace WHERE n.nspname = 'public')`).Scan(&tables, &functions); err != nil {
		t.Fatal(err)
	}
	if tables != 0 || functions != 0 {
		t.Fatalf("%d tables and %d functions left after every down, want none", tables, functions)
	}

	run(t, pool, migrationFiles(t, "up"), false)
	if err := inRolledBackTx(t, pool, fixture); err != nil {
		t.Fatalf("schema unusable after down then up: %v", err)
	}
}

func inRolledBackTx(t *testing.T, pool *pgxpool.Pool, sql string) error {
	t.Helper()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	// Without arguments, pgx uses the simple protocol: several statements.
	_, err = tx.Exec(t.Context(), sql)
	return err
}

func migrationFiles(t *testing.T, direction string) []string {
	t.Helper()
	files, err := filepath.Glob("*." + direction + ".sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("no %s migration: %v", direction, err)
	}
	slices.Sort(files)
	return files
}

func run(t *testing.T, pool *pgxpool.Pool, files []string, reverse bool) {
	t.Helper()
	if reverse {
		slices.Reverse(files)
	}
	for _, f := range files {
		sql, err := os.ReadFile(f) //nolint:gosec // migrations of this repository
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(t.Context(), string(sql)); err != nil {
			t.Fatalf("apply %s: %v", f, err)
		}
	}
}
