//go:build integration

package closure

import (
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

	t.Run("a closure that fails records nothing", func(t *testing.T) {
		// Week 4 ends with the challenge, which is no longer open: no closure.
		got, err := service(start.AddDate(0, 0, 28)).CloseWeeks(t.Context())
		if err != nil || got != (Result{}) {
			t.Fatalf("result = %+v, err = %v, want nothing", got, err)
		}
		if weeks := s.closedWeeks(challengeID); !reflect.DeepEqual(weeks, []int{1, 2, 3}) {
			t.Fatalf("closed weeks = %v, want [1 2 3]", weeks)
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
