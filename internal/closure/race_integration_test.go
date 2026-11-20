//go:build integration

package closure

import (
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/damchi/sport-challenge-api/internal/leaderboard"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/platform/db/testdb"
)

// inFlight inserts a run of user in a transaction that holds the week lock
// shared, as an activity write that checked the lock of its week just before
// it locks, and commits it only once release is called (at the end of the
// test at the latest).
func inFlight(t *testing.T, gdb *gorm.DB, user string, started time.Time, meters int) (release func() error) {
	t.Helper()
	inserted, proceed, done := make(chan error, 1), make(chan struct{}), make(chan error, 1)
	var once sync.Once
	commit := func() { once.Do(func() { close(proceed) }) }
	t.Cleanup(commit)
	go func() {
		done <- db.WithTx(t.Context(), gdb, func(tx *gorm.DB) error {
			if err := db.ShareWeekLock(tx); err != nil {
				inserted <- err
				return err
			}
			err := tx.Exec(`INSERT INTO activities (user_id, sport_code, started_at, duration_seconds, distance_meters) VALUES (?, 'run', ?, 1800, ?)`,
				user, started, meters).Error
			inserted <- err
			if err != nil {
				return err
			}
			<-proceed
			return nil
		})
	}()
	if err := <-inserted; err != nil {
		t.Fatal(err)
	}
	return func() error {
		commit()
		return <-done
	}
}

// awaitWaiter waits until a run waits for the week lock in the database of
// the test (pg_locks shows every database, and packages run in parallel).
func (s seed) awaitWaiter(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM pg_locks
			WHERE locktype = 'advisory' AND mode = 'ExclusiveLock' AND NOT granted
				AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no run waits for the week lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestRunsJudgeTheWritesCheckedBeforeTheLock: an activity accepted just
// before its week locks, committed after the worker started at the lock,
// is in the judgement of the week and in the results frozen.
func TestRunsJudgeTheWritesCheckedBeforeTheLock(t *testing.T) {
	pool := testdb.New(t)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	gdb, err := db.NewGorm(pool, logger)
	if err != nil {
		t.Fatal(err)
	}
	s := seed{t: t, pool: pool}
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 3, 2, 0, 0, 0, 0, loc)
	locked := func(n int) time.Time { return time.Date(2026, 3, 2+7*n, 7, 0, 0, 0, loc).UTC() }
	inWeek := func(n int) time.Time { return start.AddDate(0, 0, 7*(n-1)).Add(time.Hour) }
	service := func(now time.Time) *Service {
		return NewService(NewRepository(gdb), leaderboard.NewRepository(gdb), clock.NewFake(now), logger)
	}

	a := s.user("a")
	challengeID, categoryID := s.challenge(a, start, false)
	s.exec(`INSERT INTO participants (challenge_id, user_id, joined_at) VALUES ($1, $2, $3)`, challengeID, a, start)
	s.run(a, inWeek(1), 5000)

	run := func(f func() error) <-chan error {
		done := make(chan error, 1)
		go func() { done <- f() }()
		return done
	}

	t.Run("close-weeks", func(t *testing.T) {
		// Without it, week 2 would be judged empty: a eliminated.
		release := inFlight(t, gdb, a, inWeek(2), 7000)
		closed := run(func() error {
			got, err := service(locked(2)).CloseWeeks(t.Context())
			if err == nil && got != (Result{Challenges: 1, Weeks: 2}) {
				t.Errorf("result = %+v, want weeks 1 and 2 closed, nobody eliminated", got)
			}
			return err
		})
		s.awaitWaiter(t)
		if err := release(); err != nil {
			t.Fatal(err)
		}
		if err := <-closed; err != nil {
			t.Fatal(err)
		}
		if got := s.eliminations(challengeID); len(got) != 0 {
			t.Fatalf("eliminations = %+v, want none: the run of week 2 counts", got)
		}
	})

	t.Run("finalize-challenges", func(t *testing.T) {
		s.run(a, inWeek(3), 9000)
		if _, err := service(locked(3)).CloseWeeks(t.Context()); err != nil {
			t.Fatal(err)
		}
		// Without it, the last week would be judged empty: a eliminated in week 4.
		release := inFlight(t, gdb, a, inWeek(4), 11000)
		finalized := run(func() error {
			got, err := service(locked(4)).FinalizeChallenges(t.Context())
			if err == nil && got != (FinalizeResult{Challenges: 1}) {
				t.Errorf("result = %+v, want the challenge finalized", got)
			}
			return err
		})
		s.awaitWaiter(t)
		if err := release(); err != nil {
			t.Fatal(err)
		}
		if err := <-finalized; err != nil {
			t.Fatal(err)
		}
		var raw []byte
		s.scan(&raw, `SELECT payload FROM result_snapshot WHERE category_id = $1`, categoryID)
		var p payload
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatal(err)
		}
		if len(p.Entries) != 1 || p.Entries[0].Value != 32000 || p.Entries[0].EliminatedWeek != 0 {
			t.Fatalf("frozen run ranking = %+v, want a with 32000 m, not eliminated", p.Entries)
		}
		if got := s.eliminations(challengeID); len(got) != 0 {
			t.Fatalf("eliminations = %+v, want none", got)
		}
	})
}
