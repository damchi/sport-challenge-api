//go:build integration

package workerrun

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/platform/db/testdb"
)

var runStart = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

func newService(t *testing.T) (*Service, *pgxpool.Pool, *clock.FakeClock) {
	t.Helper()
	pool := testdb.New(t)
	gdb, err := db.NewGorm(pool, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(runStart)
	return NewService(NewRepository(gdb), clk, slog.New(slog.NewJSONHandler(io.Discard, nil))), pool, clk
}

type runRow struct {
	task               string
	startedAt, endedAt time.Time
	outcome            string
	errorCode          *string
}

func onlyRun(t *testing.T, pool *pgxpool.Pool) runRow {
	t.Helper()
	var r runRow
	err := pool.QueryRow(t.Context(),
		`SELECT task, started_at, ended_at, outcome, error_code FROM worker_runs`,
	).Scan(&r.task, &r.startedAt, &r.endedAt, &r.outcome, &r.errorCode)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRunRecordsASuccess(t *testing.T) {
	s, pool, clk := newService(t)
	err := s.Run(t.Context(), "close-weeks", func(context.Context) error {
		clk.Advance(90 * time.Second)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	r := onlyRun(t, pool)
	if r.task != "close-weeks" || !r.startedAt.Equal(runStart) || !r.endedAt.Equal(runStart.Add(90*time.Second)) ||
		r.outcome != "succeeded" || r.errorCode != nil {
		t.Fatalf("run = %+v; want close-weeks succeeded from %v for 90 s, no error code", r, runStart)
	}
}

func TestRunRecordsAFailure(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode string
	}{
		{name: "plain error", err: errors.New("read closable challenges: timeout"), wantCode: "internal_error"},
		{name: "application error", err: apperr.New(apperr.CodeNotFound, nil), wantCode: "not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, pool, clk := newService(t)
			err := s.Run(t.Context(), "retry-auth0-deletions", func(context.Context) error {
				clk.Advance(time.Second)
				return tt.err
			})
			if !errors.Is(err, tt.err) {
				t.Fatalf("Run() = %v; want %v", err, tt.err)
			}
			r := onlyRun(t, pool)
			if r.outcome != "failed" || r.errorCode == nil || *r.errorCode != tt.wantCode ||
				!r.endedAt.Equal(runStart.Add(time.Second)) {
				t.Fatalf("run = %+v; want failed with %s, ended at %v", r, tt.wantCode, runStart.Add(time.Second))
			}
		})
	}
}

// Only the 500 most recent runs of a task are kept, trimmed at the end of a
// run; the runs of another task are left alone.
func TestRunKeepsTheLastRunsOfTheTask(t *testing.T) {
	s, pool, clk := newService(t)
	ctx := t.Context()
	// 500 older runs of the task, one a minute before the new one, and 3 of
	// another task.
	for _, seed := range []struct {
		task  string
		count int
	}{{"finalize-challenges", keptRuns}, {"close-weeks", 3}} {
		_, err := pool.Exec(ctx, `INSERT INTO worker_runs (task, started_at, ended_at, outcome)
			SELECT $1, t, t, 'succeeded' FROM generate_series($2::timestamptz - make_interval(mins => $3), $2::timestamptz - interval '1 minute', interval '1 minute') t`,
			seed.task, runStart, seed.count)
		if err != nil {
			t.Fatal(err)
		}
	}

	if err := s.Run(ctx, "finalize-challenges", func(context.Context) error { clk.Advance(time.Second); return nil }); err != nil {
		t.Fatal(err)
	}

	var kept int
	var oldest time.Time
	if err := pool.QueryRow(ctx, `SELECT count(*), min(started_at) FROM worker_runs WHERE task = 'finalize-challenges'`).
		Scan(&kept, &oldest); err != nil {
		t.Fatal(err)
	}
	// The oldest seeded run (500 minutes before) is gone; the new run is kept.
	if wantOldest := runStart.Add(-(keptRuns - 1) * time.Minute); kept != keptRuns || !oldest.Equal(wantOldest) {
		t.Fatalf("kept %d runs from %v; want %d from %v", kept, oldest, keptRuns, wantOldest)
	}
	var newest int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM worker_runs WHERE task = 'finalize-challenges' AND started_at = $1`, runStart).
		Scan(&newest); err != nil || newest != 1 {
		t.Fatalf("new run kept: %d, %v; want 1", newest, err)
	}
	var other int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM worker_runs WHERE task = 'close-weeks'`).Scan(&other); err != nil || other != 3 {
		t.Fatalf("runs of close-weeks: %d, %v; want 3", other, err)
	}
}

// The schema keeps personal data out: an error code is a snake_case code,
// never a message, and only a failure has one.
func TestSchemaRefusesInvalidRuns(t *testing.T) {
	_, pool, _ := newService(t)
	tests := []struct {
		name string
		sql  string
	}{
		{name: "error message as code", sql: `INSERT INTO worker_runs (task, started_at, ended_at, outcome, error_code)
			VALUES ('close-weeks', now(), now(), 'failed', 'delete user jane@example.com: 500')`},
		{name: "code too long", sql: `INSERT INTO worker_runs (task, started_at, ended_at, outcome, error_code)
			VALUES ('close-weeks', now(), now(), 'failed', repeat('a', 65))`},
		{name: "failure without code", sql: `INSERT INTO worker_runs (task, started_at, ended_at, outcome)
			VALUES ('close-weeks', now(), now(), 'failed')`},
		{name: "success with code", sql: `INSERT INTO worker_runs (task, started_at, ended_at, outcome, error_code)
			VALUES ('close-weeks', now(), now(), 'succeeded', 'internal_error')`},
		{name: "running with code", sql: `INSERT INTO worker_runs (task, started_at, error_code)
			VALUES ('close-weeks', now(), 'internal_error')`},
		{name: "ended without outcome", sql: `INSERT INTO worker_runs (task, started_at, ended_at)
			VALUES ('close-weeks', now(), now())`},
		{name: "ended before started", sql: `INSERT INTO worker_runs (task, started_at, ended_at, outcome)
			VALUES ('close-weeks', now(), now() - interval '1 second', 'succeeded')`},
		{name: "unknown task", sql: `INSERT INTO worker_runs (task, started_at) VALUES ('sync', now())`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := pool.Exec(t.Context(), tt.sql); err == nil {
				t.Fatal("insert accepted; want a check violation")
			}
		})
	}
}

// The last failure of a task is kept however old, for the dashboard; an
// older failure goes with the other old runs.
func TestRunKeepsTheLastFailureOfTheTask(t *testing.T) {
	s, pool, clk := newService(t)
	ctx := t.Context()
	lastFailure := runStart.Add(-(keptRuns + 10) * time.Minute)
	olderFailure := runStart.Add(-(keptRuns + 20) * time.Minute)
	for _, at := range []time.Time{olderFailure, lastFailure} {
		if _, err := pool.Exec(ctx, `INSERT INTO worker_runs (task, started_at, ended_at, outcome, error_code)
			VALUES ('close-weeks', $1, $1, 'failed', 'internal_error')`, at); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO worker_runs (task, started_at, ended_at, outcome)
		SELECT 'close-weeks', t, t, 'succeeded' FROM generate_series($1::timestamptz - make_interval(mins => $2), $1::timestamptz - interval '1 minute', interval '1 minute') t`,
		runStart, keptRuns); err != nil {
		t.Fatal(err)
	}

	if err := s.Run(ctx, "close-weeks", func(context.Context) error { clk.Advance(time.Second); return nil }); err != nil {
		t.Fatal(err)
	}

	var total int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM worker_runs WHERE task = 'close-weeks'`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	var failures []time.Time
	rows, err := pool.Query(ctx, `SELECT started_at FROM worker_runs WHERE outcome = 'failed' ORDER BY started_at`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var at time.Time
		if err := rows.Scan(&at); err != nil {
			t.Fatal(err)
		}
		failures = append(failures, at)
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	if total != keptRuns+1 || len(failures) != 1 || !failures[0].Equal(lastFailure) {
		t.Fatalf("kept %d runs, failures %v; want %d runs with only the failure of %v", total, failures, keptRuns+1, lastFailure)
	}
}
