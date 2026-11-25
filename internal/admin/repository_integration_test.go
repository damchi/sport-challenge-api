//go:build integration

package admin

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/platform/db/testdb"
)

func newRepository(t *testing.T) (*Repository, *pgxpool.Pool) {
	t.Helper()
	pool := testdb.New(t)
	gdb, err := db.NewGorm(pool, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return NewRepository(gdb), pool
}

func exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

// insertUser inserts an account created at createdAt, deleted (anonymized)
// when deleted, and returns its id.
func insertUser(t *testing.T, pool *pgxpool.Pool, subject string, createdAt time.Time, deleted bool) string {
	t.Helper()
	var anonymizedAt *time.Time
	if deleted {
		anonymizedAt = &createdAt
	}
	var id string
	err := pool.QueryRow(t.Context(),
		`INSERT INTO users (auth_subject, created_at, anonymized_at) VALUES ($1, $2, $3) RETURNING id`,
		subject, createdAt, anonymizedAt,
	).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// insertActivity inserts an activity of userID recorded at createdAt, that
// started at createdAt unless startedAt is given.
func insertActivity(t *testing.T, pool *pgxpool.Pool, userID string, createdAt time.Time, deleted bool, startedAt ...time.Time) {
	t.Helper()
	var deletedAt *time.Time
	if deleted {
		deletedAt = &createdAt
	}
	started := createdAt
	if len(startedAt) > 0 {
		started = startedAt[0]
	}
	exec(t, pool, `INSERT INTO activities (user_id, sport_code, started_at, duration_seconds, distance_meters, created_at, deleted_at)
		VALUES ($1, 'run', $2, 1800, 5000, $3, $4)`, userID, started, createdAt, deletedAt)
}

func insertChallenge(t *testing.T, pool *pgxpool.Pool, creator string, startsAt time.Time, weeks int, deleted bool) {
	t.Helper()
	var deletedAt *time.Time
	if deleted {
		deletedAt = &startsAt
	}
	exec(t, pool, `INSERT INTO challenges (title, visibility, timezone, starts_at, ends_at, created_by, deleted_at)
		VALUES ('Défi', 'private', 'America/Toronto', $1, $2, $3, $4)`,
		startsAt, startsAt.AddDate(0, 0, 7*weeks), creator, deletedAt)
}

func TestFigures(t *testing.T) {
	r, pool := newRepository(t)
	now := time.Date(2026, 11, 23, 15, 0, 0, 0, time.UTC)
	since := now.Add(-recentWindow)
	old := now.AddDate(0, -2, 0)

	ann := insertUser(t, pool, "email|ann", old, false)
	bob := insertUser(t, pool, "email|bob", since, false) // new: the bound is included
	cid := insertUser(t, pool, "email|cid", since.Add(-time.Second), false)
	dan := insertUser(t, pool, "email|dan", old, false)
	gone := insertUser(t, pool, "email|gone", now.Add(-time.Hour), true)
	// Blocked: counted among the accounts and the blocked ones; a deleted
	// account that was blocked counts nowhere.
	exec(t, pool, `UPDATE users SET blocked_at = $2 WHERE id IN ($1, $3)`, dan, now.Add(-time.Hour), gone)

	insertActivity(t, pool, ann, now.Add(-time.Hour), false)
	insertActivity(t, pool, ann, now.Add(-2*time.Hour), false)
	insertActivity(t, pool, bob, now.Add(-time.Hour), true) // deleted: bob is not active
	insertActivity(t, pool, bob, old, false)                // too old
	insertActivity(t, pool, gone, now.Add(-time.Hour), false)
	// The date of entry counts, not that of the session: cid recorded at the
	// bound (included) a session of last month; dan's session of the window
	// was recorded before it.
	insertActivity(t, pool, cid, since, false, old)
	insertActivity(t, pool, dan, since.Add(-time.Second), false, now.Add(-time.Hour))

	insertChallenge(t, pool, ann, now.Add(time.Hour), 2, false)     // upcoming
	insertChallenge(t, pool, ann, now, 2, false)                    // ongoing: started at now
	insertChallenge(t, pool, ann, now.AddDate(0, 0, -14), 2, false) // finished: ended at now
	insertChallenge(t, pool, ann, now.AddDate(0, 0, -21), 2, false) // finished
	insertChallenge(t, pool, ann, now.Add(time.Hour), 2, true)      // deleted, whatever its status
	insertChallenge(t, pool, ann, now.AddDate(0, 0, -21), 2, true)

	exec(t, pool, `INSERT INTO pending_auth0_deletions (auth_subject, last_error_code, blocked) VALUES ('email|x', NULL, false), ('email|y', 'auth0_status_400', true)`)

	got, err := r.Figures(t.Context(), now, since)
	if err != nil {
		t.Fatal(err)
	}
	want := Figures{
		AccountsTotal: 4, AccountsNew: 1, AccountsActive: 2, AccountsBlocked: 1,
		Upcoming: 1, Ongoing: 1, Finished: 2, Deleted: 2,
		// Never those of a deleted account.
		Activities:            3,
		PendingAuth0Deletions: 2,
		BlockedAuth0Deletions: 1,
	}
	if got != want {
		t.Fatalf("Figures() = %+v, want %+v", got, want)
	}
}

func TestFiguresOfAnEmptyApp(t *testing.T) {
	r, _ := newRepository(t)
	now := time.Date(2026, 11, 23, 15, 0, 0, 0, time.UTC)
	got, err := r.Figures(t.Context(), now, now.Add(-recentWindow))
	if err != nil {
		t.Fatal(err)
	}
	if got != (Figures{}) {
		t.Fatalf("Figures() = %+v, want zeros", got)
	}
}

func TestLastWorkerRuns(t *testing.T) {
	r, pool := newRepository(t)
	at := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	run := func(task string, startedAt time.Time, outcome, errorCode *string) {
		t.Helper()
		var endedAt *time.Time
		if outcome != nil {
			e := startedAt.Add(time.Minute)
			endedAt = &e
		}
		exec(t, pool, `INSERT INTO worker_runs (task, started_at, ended_at, outcome, error_code) VALUES ($1, $2, $3, $4, $5)`,
			task, startedAt, endedAt, outcome, errorCode)
	}
	succeeded, failed, code := ptr("succeeded"), ptr("failed"), ptr("internal_error")
	run("close-weeks", at.Add(-3*time.Hour), failed, code)
	run("close-weeks", at.Add(-2*time.Hour), failed, ptr("week_lock_timeout"))
	run("close-weeks", at.Add(-time.Hour), succeeded, nil)
	run("close-weeks", at, nil, nil) // going on: the last run
	run("finalize-challenges", at.Add(-time.Hour), succeeded, nil)

	got, err := r.LastWorkerRuns(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	byKey := make(map[string]WorkerRun, len(got))
	for _, tr := range got {
		key := tr.Task
		if tr.Failure {
			key += " failure"
		}
		if _, dup := byKey[key]; dup {
			t.Fatalf("two rows for %s", key)
		}
		byKey[key] = WorkerRun{StartedAt: tr.Run.StartedAt.UTC(), EndedAt: utc(tr.Run.EndedAt), Outcome: tr.Run.Outcome, ErrorCode: tr.Run.ErrorCode}
	}
	ended := at.Add(-2 * time.Hour).Add(time.Minute)
	finalized := at.Add(-time.Hour).Add(time.Minute)
	want := map[string]WorkerRun{
		"close-weeks":         {StartedAt: at},
		"close-weeks failure": {StartedAt: at.Add(-2 * time.Hour), EndedAt: &ended, Outcome: failed, ErrorCode: ptr("week_lock_timeout")},
		"finalize-challenges": {StartedAt: at.Add(-time.Hour), EndedAt: &finalized, Outcome: succeeded},
	}
	if !reflect.DeepEqual(byKey, want) {
		t.Fatalf("LastWorkerRuns() = %+v, want %+v", byKey, want)
	}
}

// TestInLockedTxKeepsARefusalMetAtTheDeadline: only the error of the
// deadline of the request answers busy; a refusal of the change returned
// once the deadline has fallen keeps its code.
func TestInLockedTxKeepsARefusalMetAtTheDeadline(t *testing.T) {
	r, _ := newRepository(t)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	err := r.InLockedTx(ctx, func(ctx context.Context) error {
		<-ctx.Done()
		return apperr.New(apperr.CodeNotFound, nil)
	})
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != apperr.CodeNotFound {
		t.Fatalf("InLockedTx() = %v, want not_found", err)
	}
}

// TestInTxAnswersBusyOnADeadlock: a change PostgreSQL stops on a deadlock
// (two changes that lock the same rows crossed) answers busy, rolled back.
func TestInTxAnswersBusyOnADeadlock(t *testing.T) {
	r, pool := newRepository(t)
	var ann, bob string
	if err := pool.QueryRow(t.Context(), `INSERT INTO users (auth_subject) VALUES ('ann') RETURNING id`).Scan(&ann); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `INSERT INTO users (auth_subject) VALUES ('bob') RETURNING id`).Scan(&bob); err != nil {
		t.Fatal(err)
	}
	// The other change holds bob, then, once the change below waits for
	// him, asks for ann: the change waited first, so PostgreSQL finds the
	// cycle from its side and rolls it back.
	other, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Rollback(context.Background()) })
	if _, err := other.Exec(t.Context(), `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, bob); err != nil {
		t.Fatal(err)
	}
	crossed := make(chan error, 1)
	go func() {
		// Once the change waits for bob.
		for {
			var waiting int
			if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting); err != nil {
				crossed <- err
				return
			}
			if waiting > 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		_, err := other.Exec(t.Context(), `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, ann)
		crossed <- err
	}()
	err = r.InTx(t.Context(), func(ctx context.Context) error {
		tx := db.Conn(ctx, r.db)
		if err := tx.Exec(`UPDATE users SET pseudo = 'ann' WHERE id = ?`, ann).Error; err != nil {
			return err
		}
		return tx.Exec(`SELECT 1 FROM users WHERE id = ? FOR UPDATE`, bob).Error
	})
	var appErr *apperr.Error
	var pgErr *pgconn.PgError
	if !errors.As(err, &appErr) || appErr.Code != apperr.CodeBusy || !errors.As(err, &pgErr) || pgErr.Code != deadlockDetected {
		t.Fatalf("InTx() = %v, want busy on a deadlock", err)
	}
	if err := <-crossed; err != nil {
		t.Fatalf("the other change: %v, want it to go through once the deadlock is ended", err)
	}
	_ = other.Rollback(t.Context())
	var pseudo *string
	if err := pool.QueryRow(t.Context(), `SELECT pseudo FROM users WHERE id = $1`, ann).Scan(&pseudo); err != nil {
		t.Fatal(err)
	}
	if pseudo != nil {
		t.Fatalf("pseudo %q after a deadlock, want nothing written", *pseudo)
	}
}
