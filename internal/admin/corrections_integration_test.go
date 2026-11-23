//go:build integration

package admin

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/closure"
	"github.com/damchi/sport-challenge-api/internal/leaderboard"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
)

// noBoards ranks nothing: the rankings are those of module leaderboard,
// tested there.
type noBoards struct{}

func (noBoards) Get(context.Context, auth.Actor, string) (leaderboard.Leaderboard, error) {
	return leaderboard.Leaderboard{}, nil
}

// state is what a change may touch: the activity, the journal and what the
// worker recorded.
type state struct {
	Meters       int
	Actions      int
	Eliminations []EliminationRow
	Weeks        int
}

// TestCorrectionsRunFromTheDatabase: a preview leaves the database as it
// was, the journal included; the same change committed is recorded with its
// line and the challenge recomputed.
func TestCorrectionsRunFromTheDatabase(t *testing.T) {
	r, pool := newRepository(t)
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 3, 2, 0, 0, 0, 0, loc)
	locked := func(n int) time.Time { return time.Date(2026, 3, 2+7*n, 7, 0, 0, 0, loc).UTC() }
	ann := insertUser(t, pool, "email|ann", start.AddDate(0, -1, 0), false)
	bob := insertUser(t, pool, "email|bob", start.AddDate(0, -1, 0), false)
	challenge := insertChallengeRow(t, pool, ann, challengeRow{title: "Spring", visibility: "public", startsAt: start.UTC(), weeks: 4, createdAt: start.UTC()})
	exec(t, pool, `UPDATE challenges SET timezone = 'America/Toronto' WHERE id = $1`, challenge)
	exec(t, pool, `INSERT INTO categories (challenge_id, type, position, params) VALUES ($1, 'cumulative_time', 1, '{}'),
		($1, 'elimination', 3, '{"sport": "run", "step_meters": 2000}')`, challenge)
	exec(t, pool, `INSERT INTO participants (challenge_id, user_id, joined_at) VALUES ($1, $2, $3)`, challenge, bob, start)
	run := func(user string, week, meters int) string {
		var id string
		if err := pool.QueryRow(t.Context(), `INSERT INTO activities (user_id, sport_code, started_at, duration_seconds, distance_meters)
			VALUES ($1, 'run', $2, 1800, $3) RETURNING id`, user, start.AddDate(0, 0, 7*(week-1)).Add(10*time.Hour), meters).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	for _, user := range []string{ann, bob} {
		run(user, 1, 5000)
	}
	run(ann, 2, 7000)
	bobWeek2 := run(bob, 2, 7000)

	gdb := r.db
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	worker := closure.NewService(closure.NewRepository(gdb), leaderboard.NewRepository(gdb), clock.NewFake(locked(2)), logger)
	if _, err := worker.CloseWeeks(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := locked(2).Add(time.Hour)
	corrections := NewCorrections(r, NewJournal(r, clock.NewFake(now)),
		closure.NewService(closure.NewRepository(gdb), leaderboard.NewRepository(gdb), clock.NewFake(now), logger), noBoards{})

	read := func() state {
		var s state
		if err := pool.QueryRow(t.Context(), `SELECT distance_meters, (SELECT count(*) FROM admin_actions),
			(SELECT count(*) FROM week_closure WHERE challenge_id = $2) FROM activities WHERE id = $1`, bobWeek2, challenge).
			Scan(&s.Meters, &s.Actions, &s.Weeks); err != nil {
			t.Fatal(err)
		}
		if s.Eliminations, err = r.Eliminations(t.Context(), challenge); err != nil {
			t.Fatal(err)
		}
		return s
	}
	// Bob runs 6999 m in week 2 instead of 7000: eliminated in week 2.
	shorten := func(dryRun bool) Correction {
		return Correction{
			Touched: func(context.Context) ([]string, error) { return []string{challenge}, nil },
			Apply: func(ctx context.Context) (Entry, error) {
				err := db.Conn(ctx, gdb).Exec(`UPDATE activities SET distance_meters = 6999 WHERE id = ?`, bobWeek2).Error
				return Entry{
					Action: "activity.update", TargetType: TargetActivity, TargetID: bobWeek2,
					OldValue: map[string]any{"distance_meters": 7000}, NewValue: map[string]any{"distance_meters": 6999},
				}, err
			},
			Reason: "Distance mal saisie",
			DryRun: dryRun,
		}
	}
	actor := auth.Actor{ID: ann, Admin: true}
	before := read()
	if before.Meters != 7000 || before.Actions != 0 || len(before.Eliminations) != 0 || before.Weeks != 2 {
		t.Fatalf("state before %+v, want weeks 1 and 2 judged, nobody eliminated", before)
	}
	bobOut := EliminationRow{User: UserRef{ID: bob, Pseudo: nil}, WeekNumber: 2, DistanceMeters: 6999, RequiredMeters: 7000}

	t.Run("a preview changes nothing", func(t *testing.T) {
		previews, err := corrections.Run(t.Context(), actor, shorten(true))
		if err != nil {
			t.Fatal(err)
		}
		if len(previews) != 1 || len(previews[0].Eliminations) != 1 || !reflect.DeepEqual(*previews[0].Eliminations[0].After, bobOut) {
			t.Fatalf("previews = %+v, want bob eliminated in week 2", previews)
		}
		if got := read(); !reflect.DeepEqual(got, before) {
			t.Fatalf("state after a preview %+v, want %+v", got, before)
		}
	})

	t.Run("the change committed", func(t *testing.T) {
		if _, err := corrections.Run(t.Context(), actor, shorten(false)); err != nil {
			t.Fatal(err)
		}
		got := read()
		if got.Meters != 6999 || got.Actions != 1 || got.Weeks != 2 || !reflect.DeepEqual(got.Eliminations, []EliminationRow{bobOut}) {
			t.Fatalf("state %+v, want the change, its line and bob eliminated in week 2", got)
		}
		actions, err := r.ListActions(t.Context(), ActionQuery{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if a := actions[0]; a.Admin.ID != ann || !reflect.DeepEqual(a.ChallengeIDs, []string{challenge}) || a.TargetID != bobWeek2 {
			t.Fatalf("journal line %+v, want the admin, the challenge and the activity", a)
		}
	})
}
