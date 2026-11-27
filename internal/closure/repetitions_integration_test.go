//go:build integration

package closure

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/leaderboard"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/platform/db/testdb"
)

// TestEliminationByRepetitions: a 3-week CrossFit challenge whose elimination
// counts the burpees, step 10 (README §2.3). b falls under the minimum in
// week 2, recorded when it locks and not again on a second run; the results
// frozen at the end are in repetitions; a correction of b's repetitions
// judged again moves the elimination to week 3.
func TestEliminationByRepetitions(t *testing.T) {
	pool := testdb.New(t)
	gdb, err := db.NewGorm(pool, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	loc, errLoc := time.LoadLocation("America/Toronto")
	if err != nil || errLoc != nil {
		t.Fatal(err, errLoc)
	}
	s := seed{t: t, pool: pool}
	start := time.Date(2026, 3, 2, 0, 0, 0, 0, loc)
	locked := func(n int) time.Time { return time.Date(2026, 3, 2+7*n, 7, 0, 0, 0, loc).UTC() }
	a, b := s.user("a"), s.user("b")
	var id, eliminationID string
	s.scan(&id, `INSERT INTO challenges (title, visibility, timezone, starts_at, ends_at, created_by)
		VALUES ('Box', 'public', 'America/Toronto', $1, $2, $3) RETURNING id`, start, start.AddDate(0, 0, 21), a)
	s.exec(`INSERT INTO categories (challenge_id, type, position, params) VALUES ($1, 'cumulative_time', 1, '{}')`, id)
	s.scan(&eliminationID, `INSERT INTO categories (challenge_id, type, position, params)
		VALUES ($1, 'elimination', 3, '{"sport": "crossfit", "exercise": "burpee", "step_repetitions": 10}') RETURNING id`, id)
	s.exec(`INSERT INTO participants (challenge_id, user_id, joined_at) VALUES ($1, $2, $4), ($1, $3, $4)`, id, a, b, start)
	// workout records a CrossFit session in week n with its burpees, and
	// wall balls the elimination never counts; it returns the activity.
	workout := func(user string, week, burpees int) string {
		t.Helper()
		var activity string
		s.scan(&activity, `INSERT INTO activities (user_id, sport_code, started_at, duration_seconds) VALUES ($1, 'crossfit', $2, 1800) RETURNING id`,
			user, start.AddDate(0, 0, 7*(week-1)).Add(10*time.Hour))
		s.exec(`INSERT INTO activity_repetitions (activity_id, exercise_code, repetitions) VALUES ($1, 'burpee', $2), ($1, 'wall_ball', 500)`, activity, burpees)
		return activity
	}
	workout(a, 1, 100)
	workout(b, 1, 100)
	workout(a, 2, 110)
	workout(a, 3, 130)
	workout(b, 3, 50)            // after the elimination: not in the frozen value
	bWeek2 := workout(b, 2, 105) // under 110
	service := func(at time.Time) *Service {
		return NewService(NewRepository(gdb), leaderboard.NewRepository(gdb), clock.NewFake(at), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	}

	want := []recordedElimination{{User: b, Week: 2, Run: 105, Min: 110}}
	for run := 1; run <= 2; run++ {
		if _, err := service(locked(2)).CloseWeeks(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := s.eliminations(id); !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d: eliminations %+v, want %+v", run, got, want)
		}
	}

	if got, err := service(locked(3)).FinalizeChallenges(t.Context()); err != nil || got.Challenges != 1 {
		t.Fatalf("finalize = %+v, %v, want 1 challenge", got, err)
	}
	wantFrozen := payload{Exercise: "burpee", Unit: "repetitions", Entries: []payloadEntry{
		{UserID: a, Rank: 1, Value: 340}, {UserID: b, Rank: 2, Value: 205, EliminatedWeek: 2},
	}}
	if snaps := s.snapshots(id); len(snaps) != 2 || snaps[1].Category != eliminationID || !reflect.DeepEqual(snaps[1].Payload, wantFrozen) {
		t.Fatalf("snapshots %+v, want the elimination frozen %+v", snaps, wantFrozen)
	}

	// An admin corrects b's week 2 to 120 burpees: b survives week 2, and
	// with 50 in week 3 falls under 130 then.
	err = db.InTx(t.Context(), gdb, func(ctx context.Context) error {
		tx := db.Conn(ctx, gdb)
		if err := db.LockWeeks(tx); err != nil {
			return err
		}
		if err := tx.Exec(`UPDATE activity_repetitions SET repetitions = 120 WHERE activity_id = ? AND exercise_code = 'burpee'`, bWeek2).Error; err != nil {
			return err
		}
		return service(locked(3).Add(time.Hour)).Recompute(ctx, id)
	})
	wantFrozen.Entries[1] = payloadEntry{UserID: b, Rank: 2, Value: 270, EliminatedWeek: 3}
	got, snaps := s.eliminations(id), s.snapshots(id)
	if err != nil || !reflect.DeepEqual(got, []recordedElimination{{User: b, Week: 3, Run: 50, Min: 130}}) ||
		len(snaps) != 2 || !reflect.DeepEqual(snaps[1].Payload, wantFrozen) {
		t.Fatalf("after the recompute: %v, eliminations %+v, snapshots %+v, want b out in week 3 (50 of 130), frozen %+v", err, got, snaps, wantFrozen)
	}
}
