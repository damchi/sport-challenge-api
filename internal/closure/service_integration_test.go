//go:build integration

package closure

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damchi/sport-challenge-api/internal/leaderboard"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/platform/db/testdb"
	"github.com/damchi/sport-challenge-api/internal/scoring"
)

type seed struct {
	t    *testing.T
	pool *pgxpool.Pool
}

func (s seed) exec(sql string, args ...any) {
	s.t.Helper()
	if _, err := s.pool.Exec(s.t.Context(), sql, args...); err != nil {
		s.t.Fatal(err)
	}
}

func (s seed) scan(dest any, sql string, args ...any) {
	s.t.Helper()
	if err := s.pool.QueryRow(s.t.Context(), sql, args...).Scan(dest); err != nil {
		s.t.Fatal(err)
	}
}

func (s seed) user(subject string) string {
	s.t.Helper()
	var id string
	s.scan(&id, "INSERT INTO users (auth_subject, pseudo) VALUES ($1, $1) RETURNING id", subject)
	return id
}

// challenge creates a 4-week challenge with its 3 categories (the run
// elimination one with a step of 2000 m); it returns its id and that category's.
func (s seed) challenge(creator string, start time.Time, deleted bool) (id, category string) {
	s.t.Helper()
	var deletedAt *time.Time
	if deleted {
		deletedAt = &start
	}
	s.scan(&id, `INSERT INTO challenges (title, visibility, timezone, starts_at, ends_at, created_by, deleted_at)
		VALUES ('Spring', 'public', 'America/Toronto', $1, $2, $3, $4) RETURNING id`, start, start.AddDate(0, 0, 28), creator, deletedAt)
	s.exec(`INSERT INTO categories (challenge_id, type, position) VALUES ($1, 'cumulative_time', 1), ($1, 'cumulative_distance', 2)`, id)
	s.scan(&category, `INSERT INTO categories (challenge_id, type, position, params)
		VALUES ($1, 'run_elimination', 3, '{"sport": "run", "step_meters": 2000}') RETURNING id`, id)
	return id, category
}

func (s seed) run(user string, started time.Time, meters int) {
	s.t.Helper()
	s.exec(`INSERT INTO activities (user_id, sport_code, started_at, duration_seconds, distance_meters) VALUES ($1, 'run', $2, 1800, $3)`,
		user, started, meters)
}

type recordedElimination struct {
	User     string
	Week     int
	Run, Min int
}

func (s seed) eliminations(challengeID string) []recordedElimination {
	s.t.Helper()
	rows, err := s.pool.Query(s.t.Context(),
		`SELECT user_id, week_number, distance_meters, required_meters FROM elimination WHERE challenge_id = $1 ORDER BY week_number, user_id`, challengeID)
	if err != nil {
		s.t.Fatal(err)
	}
	defer rows.Close()
	var out []recordedElimination
	for rows.Next() {
		var e recordedElimination
		if err := rows.Scan(&e.User, &e.Week, &e.Run, &e.Min); err != nil {
			s.t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

func (s seed) closedWeeks(challengeID string) []int {
	s.t.Helper()
	var weeks []int
	rows, err := s.pool.Query(s.t.Context(), `SELECT week_number FROM week_closure WHERE challenge_id = $1 ORDER BY week_number`, challengeID)
	if err != nil {
		s.t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			s.t.Fatal(err)
		}
		weeks = append(weeks, n)
	}
	return weeks
}

func TestCloseWeeksFromTheDatabase(t *testing.T) {
	pool := testdb.New(t)
	gdb, err := db.NewGorm(pool, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	s := seed{t: t, pool: pool}
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	// The time change of March 8 falls in week 2.
	start := time.Date(2026, 3, 2, 0, 0, 0, 0, loc)
	weekEnd := func(n int) time.Time { return start.AddDate(0, 0, 7*n).UTC() }
	inWeek := func(n int) time.Time { return start.AddDate(0, 0, 7*(n-1)).Add(time.Hour) }

	a, b, quitter, stranger := s.user("a"), s.user("b"), s.user("quitter"), s.user("stranger")
	challengeID, categoryID := s.challenge(a, start, false)
	deletedID, _ := s.challenge(a, start, true)
	s.exec(`INSERT INTO participants (challenge_id, user_id, joined_at) VALUES ($1, $2, $4), ($1, $3, $4)`, challengeID, a, b, start)
	s.exec(`INSERT INTO participants (challenge_id, user_id, status, joined_at, abandoned_at) VALUES ($1, $2, 'abandoned', $3, $4)`,
		challengeID, quitter, start, inWeek(1))
	s.exec(`INSERT INTO participants (challenge_id, user_id, joined_at) VALUES ($1, $2, $3), ($1, $4, $3)`, deletedID, a, start, b)
	// a runs 5000 then 6999 (under 7000); b keeps up; the quitter ran nothing
	// that counts; the stranger is in no challenge.
	s.run(a, inWeek(1), 5000)
	s.run(a, inWeek(2), 6999)
	s.run(b, inWeek(1), 5000)
	s.run(b, inWeek(2), 7000)
	s.run(quitter, inWeek(1), 100)
	s.run(stranger, inWeek(1), 100)

	service := func(now time.Time) *Service {
		return NewService(NewRepository(gdb), leaderboard.NewRepository(gdb), clock.NewFake(now), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	}
	want := []recordedElimination{{User: a, Week: 2, Run: 6999, Min: 7000}}

	t.Run("nothing happens the nanosecond before the end of week 1", func(t *testing.T) {
		got, err := service(weekEnd(1).Add(-time.Nanosecond)).CloseWeeks(t.Context())
		if err != nil || got != (Result{}) {
			t.Fatalf("result = %+v, err = %v, want nothing", got, err)
		}
	})

	t.Run("week 2 is judged an hour late", func(t *testing.T) {
		got, err := service(weekEnd(2).Add(time.Hour)).CloseWeeks(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if want := (Result{Challenges: 1, Weeks: 2, Eliminations: 1}); got != want {
			t.Fatalf("result = %+v, want %+v", got, want)
		}
		if weeks := s.closedWeeks(challengeID); !reflect.DeepEqual(weeks, []int{1, 2}) {
			t.Fatalf("closed weeks = %v, want [1 2]", weeks)
		}
		if got := s.eliminations(challengeID); !reflect.DeepEqual(got, want) {
			t.Fatalf("eliminations = %+v, want %+v", got, want)
		}
		if weeks, elims := s.closedWeeks(deletedID), s.eliminations(deletedID); len(weeks) != 0 || len(elims) != 0 {
			t.Fatalf("deleted challenge: closed %v, eliminations %v, want nothing", weeks, elims)
		}
	})

	t.Run("running again, even later, changes nothing", func(t *testing.T) {
		got, err := service(weekEnd(2).Add(2 * time.Hour)).CloseWeeks(t.Context())
		if err != nil || got != (Result{}) {
			t.Fatalf("result = %+v, err = %v, want nothing", got, err)
		}
		if got := s.eliminations(challengeID); !reflect.DeepEqual(got, want) {
			t.Fatalf("eliminations = %+v, want %+v", got, want)
		}
	})

	t.Run("the next week is judged on its own", func(t *testing.T) {
		s.run(b, inWeek(3), 8999) // under 7000 + 2000
		got, err := service(weekEnd(3)).CloseWeeks(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if want := (Result{Challenges: 1, Weeks: 1, Eliminations: 1}); got != want {
			t.Fatalf("result = %+v, want %+v", got, want)
		}
		wantAll := append(append([]recordedElimination(nil), want...), recordedElimination{User: b, Week: 3, Run: 8999, Min: 9000})
		if got := s.eliminations(challengeID); !reflect.DeepEqual(got, wantAll) {
			t.Fatalf("eliminations = %+v, want %+v", got, wantAll)
		}
	})

	t.Run("the last week is left to the finalization", func(t *testing.T) {
		got, err := service(start.AddDate(0, 0, 28)).CloseWeeks(t.Context())
		if err != nil || got != (Result{}) {
			t.Fatalf("result = %+v, err = %v, want nothing", got, err)
		}
		if weeks := s.closedWeeks(challengeID); !reflect.DeepEqual(weeks, []int{1, 2, 3}) {
			t.Fatalf("closed weeks = %v, want [1 2 3]", weeks)
		}
	})

	t.Run("a challenge over before the worker came is caught up", func(t *testing.T) {
		lateID, _ := s.challenge(a, start, false)
		s.exec(`INSERT INTO participants (challenge_id, user_id, joined_at) VALUES ($1, $2, $3)`, lateID, a, start)
		s.run(a, inWeek(1), 5000)
		got, err := service(start.AddDate(0, 0, 60)).CloseWeeks(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if want := (Result{Challenges: 1, Weeks: 3, Eliminations: 1}); got != want {
			t.Fatalf("result = %+v, want %+v", got, want)
		}
		if weeks := s.closedWeeks(lateID); !reflect.DeepEqual(weeks, []int{1, 2, 3}) {
			t.Fatalf("closed weeks = %v, want [1 2 3]", weeks)
		}
		if got := s.eliminations(lateID); len(got) != 1 || got[0].Week != 2 {
			t.Fatalf("eliminations = %+v, want a in week 2", got)
		}
	})

	t.Run("the transaction holds the closure and the eliminations together", func(t *testing.T) {
		repo := NewRepository(gdb)
		c := Challenge{ID: challengeID, CategoryID: categoryID}
		// A participant of nowhere breaks the participant key of the elimination.
		_, err := repo.CloseWeek(t.Context(), c, 4, weekEnd(3), []scoring.Eliminated{{UserID: stranger, Week: 4, RunMeters: 1, MinimumMeters: 2}})
		if err == nil {
			t.Fatal("error = nil, want the foreign key of the participant")
		}
		if weeks := s.closedWeeks(challengeID); !reflect.DeepEqual(weeks, []int{1, 2, 3}) {
			t.Fatalf("closed weeks = %v: the closure of week 4 stayed without its eliminations", weeks)
		}
	})
}

// TestCloseWeeksOfAParticipantWhoAbandonedSinceTheWeekEnded pins the choice
// documented on CloseWeeks: who is abandoned is read when the task runs.
func TestCloseWeeksOfAParticipantWhoAbandonedSinceTheWeekEnded(t *testing.T) {
	pool := testdb.New(t)
	gdb, err := db.NewGorm(pool, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	s := seed{t: t, pool: pool}
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 3, 2, 0, 0, 0, 0, loc)
	week2End := start.AddDate(0, 0, 14).UTC()

	runner := s.user("runner")
	challengeID, _ := s.challenge(runner, start, false)
	s.exec(`INSERT INTO participants (challenge_id, user_id, status, joined_at, abandoned_at) VALUES ($1, $2, 'abandoned', $3, $4)`,
		challengeID, runner, start, week2End.Add(time.Minute))
	// Under the minimum of week 2, but abandoned after the week ended.
	s.run(runner, start.Add(time.Hour), 5000)

	service := NewService(NewRepository(gdb), leaderboard.NewRepository(gdb), clock.NewFake(week2End.Add(time.Hour)), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	got, err := service.CloseWeeks(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if want := (Result{Challenges: 1, Weeks: 2}); got != want {
		t.Fatalf("result = %+v, want %+v", got, want)
	}
	if elims := s.eliminations(challengeID); len(elims) != 0 {
		t.Fatalf("eliminations = %+v, want none: the participant is shown as abandoned", elims)
	}
}

type storedSnapshot struct {
	Category, Type string
	Payload        payload
	CreatedAt      time.Time
}

func (s seed) snapshots(challengeID string) []storedSnapshot {
	s.t.Helper()
	rows, err := s.pool.Query(s.t.Context(),
		`SELECT rs.category_id, rs.category_type, rs.payload, rs.created_at
		FROM result_snapshot rs JOIN categories c ON c.id = rs.category_id
		WHERE rs.challenge_id = $1 ORDER BY c.position`, challengeID)
	if err != nil {
		s.t.Fatal(err)
	}
	defer rows.Close()
	var out []storedSnapshot
	for rows.Next() {
		var r storedSnapshot
		if err := rows.Scan(&r.Category, &r.Type, &r.Payload, &r.CreatedAt); err != nil {
			s.t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

type staticChallenge struct{ c leaderboard.Challenge }

func (s staticChallenge) Challenge(context.Context, string, string) (leaderboard.Challenge, error) {
	return s.c, nil
}

// everyoneNamed names every user it is asked for: none is deleted.
type everyoneNamed struct{}

func (everyoneNamed) Names(_ context.Context, ids []string) (map[string]leaderboard.Name, error) {
	names := make(map[string]leaderboard.Name, len(ids))
	for _, id := range ids {
		names[id] = leaderboard.Name{}
	}
	return names, nil
}

// withoutSnapshots makes the leaderboard compute its ranking.
type withoutSnapshots struct{ *leaderboard.Repository }

func (withoutSnapshots) Snapshots(context.Context, string) (map[string]json.RawMessage, time.Time, error) {
	return nil, time.Time{}, nil
}

func TestFinalizeChallengesFromTheDatabase(t *testing.T) {
	pool := testdb.New(t)
	gdb, err := db.NewGorm(pool, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	s := seed{t: t, pool: pool}
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 3, 2, 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, 28).UTC()
	inWeek := func(n int) time.Time { return start.AddDate(0, 0, 7*(n-1)).Add(time.Hour) }

	a, b, quitter := s.user("a"), s.user("b"), s.user("quitter")
	challengeID, categoryID := s.challenge(a, start, false)
	deletedID, _ := s.challenge(a, start, true)
	runningID, _ := s.challenge(a, start.AddDate(0, 0, 7), false)
	s.exec(`INSERT INTO participants (challenge_id, user_id, joined_at) VALUES ($1, $2, $4), ($1, $3, $4), ($5, $2, $4), ($6, $2, $4)`,
		challengeID, a, b, start, deletedID, runningID)
	s.exec(`INSERT INTO participants (challenge_id, user_id, status, joined_at, abandoned_at) VALUES ($1, $2, 'abandoned', $3, $4)`,
		challengeID, quitter, start, inWeek(1))
	for week, meters := range []int{5000, 7000, 9000, 10000} { // a misses 9000 + 2000 in week 4
		s.run(a, inWeek(week+1), meters)
	}
	for week, meters := range []int{5000, 7000, 9000, 11000} {
		s.run(b, inWeek(week+1), meters)
	}
	s.run(quitter, inWeek(1), 100)
	// An account deleted between the end and the run: still active here, but
	// no longer ranked.
	gone := s.user("gone")
	s.exec(`INSERT INTO participants (challenge_id, user_id, joined_at) VALUES ($1, $2, $3)`, challengeID, gone, start)
	s.run(gone, inWeek(1), 99000)
	s.exec(`UPDATE users SET anonymized_at = now(), pseudo = NULL WHERE id = $1`, gone)

	service := func(now time.Time) *Service {
		return NewService(NewRepository(gdb), leaderboard.NewRepository(gdb), clock.NewFake(now), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	}

	t.Run("nothing is frozen the nanosecond before the end", func(t *testing.T) {
		got, err := service(end.Add(-time.Nanosecond)).FinalizeChallenges(t.Context())
		if err != nil || got != (FinalizeResult{}) {
			t.Fatalf("result = %+v, err = %v, want nothing", got, err)
		}
	})

	t.Run("the end freezes the three categories, not the deleted challenge", func(t *testing.T) {
		got, err := service(end.Add(time.Hour)).FinalizeChallenges(t.Context())
		if err != nil || got != (FinalizeResult{Challenges: 1}) {
			t.Fatalf("result = %+v, err = %v, want 1 challenge", got, err)
		}
		snaps := s.snapshots(challengeID)
		if len(snaps) != 3 {
			t.Fatalf("%d snapshots, want 3", len(snaps))
		}
		wantTypes := []string{"cumulative_time", "cumulative_distance", "run_elimination"}
		for i, snap := range snaps {
			if snap.Type != wantTypes[i] || !snap.CreatedAt.Equal(end.Add(time.Hour)) {
				t.Fatalf("snapshot %d = %s at %v, want %s at the instant of the clock", i, snap.Type, snap.CreatedAt, wantTypes[i])
			}
		}
		want := payload{Entries: []payloadEntry{
			{UserID: b, Rank: 1, Value: 32000},
			{UserID: a, Rank: 2, Value: 31000, EliminatedWeek: 4},
			{UserID: quitter, Abandoned: true},
		}}
		if !reflect.DeepEqual(snaps[2].Payload, want) {
			t.Fatalf("elimination ranking = %+v, want %+v", snaps[2].Payload, want)
		}
		// The last week is closed with the results (SC-API-089): its closure
		// and its eliminations, a in week 4 (10000 run, 11000 needed), as
		// close-weeks records the others (not run here: nothing else).
		if weeks := s.closedWeeks(challengeID); !reflect.DeepEqual(weeks, []int{4}) {
			t.Fatalf("closed weeks = %v, want [4]", weeks)
		}
		wantLast := []recordedElimination{{User: a, Week: 4, Run: 10000, Min: 11000}}
		if got := s.eliminations(challengeID); !reflect.DeepEqual(got, wantLast) {
			t.Fatalf("eliminations = %+v, want %+v", got, wantLast)
		}
		var category string
		s.scan(&category, "SELECT category_id FROM elimination WHERE challenge_id = $1 AND week_number = 4", challengeID)
		if category != categoryID {
			t.Fatalf("elimination of week 4 in category %s, want the run elimination one %s", category, categoryID)
		}
		if got := s.snapshots(deletedID); len(got) != 0 {
			t.Fatalf("deleted challenge has %d snapshots, want none", len(got))
		}
		if weeks := s.closedWeeks(deletedID); len(weeks) != 0 {
			t.Fatalf("deleted challenge has closed weeks %v, want none", weeks)
		}
		if got := s.snapshots(runningID); len(got) != 0 {
			t.Fatalf("challenge not over has %d snapshots, want none", len(got))
		}
	})

	// The contract between the two modules: the ranking served from the
	// snapshots written here is the one computed from the activities.
	t.Run("the leaderboard serves what was frozen", func(t *testing.T) {
		c := leaderboard.Challenge{
			ID: challengeID, Timezone: "America/Toronto", StartsAt: start.UTC(), EndsAt: end,
			Categories: []leaderboard.Category{
				{Type: leaderboard.CategoryCumulativeTime},
				{Type: leaderboard.CategoryCumulativeDistance},
				{Type: leaderboard.CategoryRunElimination, Sport: "run", StepMeters: 2000},
			},
		}
		board := func(repo interface {
			Participants(context.Context, string) ([]scoring.Participant, error)
			Activities(context.Context, string, scoring.Window, string) ([]scoring.Activity, error)
			Snapshots(context.Context, string) (map[string]json.RawMessage, time.Time, error)
		},
		) leaderboard.Leaderboard {
			t.Helper()
			svc := leaderboard.NewService(staticChallenge{c}, everyoneNamed{}, repo, clock.NewFake(end.Add(48*time.Hour)), time.Hour)
			got, err := svc.Get(t.Context(), a, challengeID)
			if err != nil {
				t.Fatal(err)
			}
			got.ComputedAt = time.Time{}
			return got
		}
		frozen := board(leaderboard.NewRepository(gdb))
		computed := board(withoutSnapshots{leaderboard.NewRepository(gdb)})
		if !reflect.DeepEqual(frozen, computed) {
			t.Fatalf("served from the snapshots = %+v, want what is computed %+v", frozen, computed)
		}
	})

	t.Run("running again, even later, changes nothing", func(t *testing.T) {
		before := s.snapshots(challengeID)
		got, err := service(end.Add(48 * time.Hour)).FinalizeChallenges(t.Context())
		if err != nil || got != (FinalizeResult{}) {
			t.Fatalf("result = %+v, err = %v, want nothing", got, err)
		}
		if after := s.snapshots(challengeID); !reflect.DeepEqual(after, before) {
			t.Fatalf("snapshots changed: %+v, want %+v", after, before)
		}
		if weeks, elims := s.closedWeeks(challengeID), s.eliminations(challengeID); !reflect.DeepEqual(weeks, []int{4}) || len(elims) != 1 {
			t.Fatalf("closed weeks %v, %d eliminations, want [4] and 1: nothing written twice", weeks, len(elims))
		}
	})

	t.Run("a run that lost the race records nothing", func(t *testing.T) {
		repo := NewRepository(gdb)
		taken := s.snapshots(challengeID)[0]
		last := LastWeek{Number: 4, CategoryID: categoryID, Eliminated: []scoring.Eliminated{{UserID: b, Week: 4, RunMeters: 1, MinimumMeters: 2}}}
		done, err := repo.SaveSnapshots(t.Context(), challengeID, end, []Snapshot{{CategoryID: taken.Category, Type: taken.Type, Payload: []byte(`{"entries": []}`)}}, last)
		if err != nil || done {
			t.Fatalf("done = %v, err = %v, want false: the category already has its snapshot", done, err)
		}
		if after := s.snapshots(challengeID)[0]; !reflect.DeepEqual(after, taken) {
			t.Fatalf("snapshot changed: %+v, want %+v", after, taken)
		}
		if elims := s.eliminations(challengeID); len(elims) != 1 || elims[0].User != a {
			t.Fatalf("eliminations = %+v, want only a's: the lost run wrote nothing", elims)
		}
	})
}

// TestCloseWeeksWhereMidnightIsSkipped runs the closure on a challenge of 3
// weeks in Santiago that starts on 2027-09-05, a day whose midnight is skipped
// (SC-API-079): it starts at 01:00 and its weeks end at local midnight. The
// stored window must still count 3 weeks: weeks 1 and 2 are closed, the last
// one is left to the finalization, and a second run changes nothing.
func TestCloseWeeksWhereMidnightIsSkipped(t *testing.T) {
	pool := testdb.New(t)
	gdb, err := db.NewGorm(pool, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	s := seed{t: t, pool: pool}
	loc, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Fatal(err)
	}
	if time.Date(2027, 9, 5, 0, 0, 0, 0, loc).Day() == 5 {
		t.Skip("this zone database does not skip midnight on 2027-09-05 in Santiago")
	}
	start := scoring.StartOfDay(2027, 9, 5, loc)
	end := scoring.StartOfDay(2027, 9, 26, loc)
	creator := s.user("creator")
	var id string
	s.scan(&id, `INSERT INTO challenges (title, visibility, timezone, starts_at, ends_at, created_by)
		VALUES ('Spring', 'public', 'America/Santiago', $1, $2, $3) RETURNING id`, start, end, creator)
	s.exec(`INSERT INTO categories (challenge_id, type, position) VALUES ($1, 'cumulative_time', 1), ($1, 'cumulative_distance', 2)`, id)
	s.exec(`INSERT INTO categories (challenge_id, type, position, params)
		VALUES ($1, 'run_elimination', 3, '{"sport": "run", "step_meters": 2000}')`, id)
	s.exec(`INSERT INTO participants (challenge_id, user_id, joined_at) VALUES ($1, $2, $3)`, id, creator, start)

	service := NewService(NewRepository(gdb), leaderboard.NewRepository(gdb), clock.NewFake(end), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if _, err := service.CloseWeeks(t.Context()); err != nil {
		t.Fatal(err)
	}
	if weeks := s.closedWeeks(id); !reflect.DeepEqual(weeks, []int{1, 2}) {
		t.Fatalf("closed weeks = %v, want [1 2]", weeks)
	}
	if got, err := service.CloseWeeks(t.Context()); err != nil || got != (Result{}) {
		t.Fatalf("second run: result = %+v, err = %v, want nothing", got, err)
	}
}

// TestCloseWeeksAfterTheFinalization: the worker fell behind (weeks 1 and 2
// closed, not 3) when the finalization closed the last week (4). close-weeks
// must still close week 3: the closure of the last week does not count among
// the weeks it has to close.
func TestCloseWeeksAfterTheFinalization(t *testing.T) {
	pool := testdb.New(t)
	gdb, err := db.NewGorm(pool, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	s := seed{t: t, pool: pool}
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 3, 2, 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, 28).UTC()
	a := s.user("a")
	challengeID, _ := s.challenge(a, start, false)
	s.exec(`INSERT INTO participants (challenge_id, user_id, joined_at) VALUES ($1, $2, $3)`, challengeID, a, start)
	s.exec(`INSERT INTO week_closure (challenge_id, week_number, closed_at) VALUES ($1, 1, $2), ($1, 2, $2)`, challengeID, end)
	service := NewService(NewRepository(gdb), leaderboard.NewRepository(gdb), clock.NewFake(end.Add(time.Hour)), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if _, err := service.FinalizeChallenges(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CloseWeeks(t.Context()); err != nil {
		t.Fatal(err)
	}
	if weeks := s.closedWeeks(challengeID); !reflect.DeepEqual(weeks, []int{1, 2, 3, 4}) {
		t.Fatalf("closed weeks = %v, want [1 2 3 4]: week 3 left behind", weeks)
	}
}
