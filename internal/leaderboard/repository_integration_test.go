//go:build integration

package leaderboard

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/platform/db/testdb"
	"github.com/damchi/sport-challenge-api/internal/scoring"
)

// staticChallenge gives the same challenge to every actor: the rights are
// the challenge module's, run end to end by cmd/api.
type staticChallenge Challenge

func (s staticChallenge) Challenge(context.Context, string, string) (Challenge, error) {
	return Challenge(s), nil
}

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

func (s seed) user(subject string) string {
	s.t.Helper()
	var id string
	if err := s.pool.QueryRow(s.t.Context(), "INSERT INTO users (auth_subject, pseudo) VALUES ($1, $1) RETURNING id", subject).Scan(&id); err != nil {
		s.t.Fatal(err)
	}
	return id
}

func (s seed) activity(user, sport string, started time.Time, seconds int, distance *int, deleted bool) {
	s.t.Helper()
	var deletedAt *time.Time
	if deleted {
		deletedAt = &started
	}
	s.exec(`INSERT INTO activities (user_id, sport_code, started_at, duration_seconds, distance_meters, deleted_at)
		VALUES ($1, $2, $3, $4, $5, $6)`, user, sport, started, seconds, distance, deletedAt)
}

func TestGetFromTheDatabase(t *testing.T) {
	pool := testdb.New(t)
	gdb, err := db.NewGorm(pool, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	s := seed{t: t, pool: pool}
	loc := toronto(t)
	start := time.Date(2026, 6, 1, 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, 14)
	at := func(day, hour int) time.Time { return time.Date(2026, 6, day, hour, 0, 0, 0, loc) }

	runner, walker, quitter, stranger := s.user("runner"), s.user("walker"), s.user("quitter"), s.user("stranger")
	var challengeID, other string
	for _, id := range []*string{&challengeID, &other} {
		if err := pool.QueryRow(t.Context(), `INSERT INTO challenges (title, visibility, timezone, starts_at, ends_at, created_by)
			VALUES ('Summer', 'public', 'America/Toronto', $1, $2, $3) RETURNING id`, start, end, runner).Scan(id); err != nil {
			t.Fatal(err)
		}
	}
	s.exec("INSERT INTO participants (challenge_id, user_id, joined_at) VALUES ($1, $2, $3), ($1, $4, $3)", challengeID, runner, start, walker)
	s.exec("INSERT INTO participants (challenge_id, user_id, status, joined_at, abandoned_at) VALUES ($1, $2, 'abandoned', $3, $4)", challengeID, quitter, start, at(5, 0))
	// The runner also takes part in another challenge over the same weeks: the
	// activities must not come twice, nor those of someone who is not here.
	s.exec("INSERT INTO participants (challenge_id, user_id, joined_at) VALUES ($1, $2, $3), ($1, $4, $3)", other, runner, start, stranger)

	km := func(m int) *int { return &m }
	s.activity(runner, "run", at(2, 7), 3600, km(10000), false)
	s.activity(runner, "run", at(9, 7), 3600, km(12000), false)
	s.activity(runner, "ride", at(3, 7), 1800, km(20000), false)
	s.activity(runner, "run", at(4, 7), 3600, km(50000), true)             // deleted
	s.activity(runner, "run", start.Add(-time.Second), 3600, km(1), false) // before the window
	s.activity(runner, "run", end, 3600, km(1), false)                     // at the end: out
	s.activity(walker, "walk", at(2, 8), 7200, km(4000), false)
	s.activity(walker, "yoga", at(3, 8), 3600, nil, false)
	s.activity(walker, "walk", start, 600, km(1000), false)       // at the start: in
	s.activity(quitter, "run", at(2, 9), 3600, km(99000), false)  // abandoned: no longer counts
	s.activity(stranger, "run", at(2, 9), 3600, km(99000), false) // in no participation here

	c := Challenge{
		ID: challengeID, Timezone: "America/Toronto", StartsAt: start.UTC(), EndsAt: end.UTC(),
		Categories: []Category{
			{Type: CategoryCumulativeTime},
			{Type: CategoryCumulativeDistance, Factors: map[string]float64{"run": 1, "walk": 1, "ride": 0.3}},
			{Type: CategoryRunElimination, Sport: "run", StepMeters: 2000},
		},
	}
	svc := NewService(staticChallenge(c), NewRepository(gdb), clock.NewFake(end))

	got, err := svc.Get(t.Context(), runner, challengeID)
	if err != nil {
		t.Fatal(err)
	}
	want := []CategoryRanking{
		{Type: CategoryCumulativeTime, Entries: []scoring.Entry{
			{UserID: walker, Rank: 1, Value: 11400}, {UserID: runner, Rank: 2, Value: 9000}, {UserID: quitter, Abandoned: true},
		}},
		{Type: CategoryCumulativeDistance, Entries: []scoring.Entry{
			{UserID: runner, Rank: 1, Value: 28000}, {UserID: walker, Rank: 2, Value: 5000}, {UserID: quitter, Abandoned: true},
		}},
		{Type: CategoryRunElimination, Entries: []scoring.Entry{
			{UserID: runner, Rank: 1, Value: 22000}, {UserID: walker, Rank: 2}, {UserID: quitter, Abandoned: true},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Get() = %+v, want %+v", got, want)
	}
}
