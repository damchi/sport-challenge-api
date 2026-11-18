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
	svc := NewService(staticChallenge(c), fakeProfiles{}, NewRepository(gdb), clock.NewFake(end), 24*time.Hour)

	got, err := svc.Get(t.Context(), runner, challengeID)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		Type    string
		Entries []scoring.Entry
	}{
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
	// The live progress of the elimination category has its own tests.
	plain := make([]struct {
		Type    string
		Entries []scoring.Entry
	}, len(got.Categories))
	for i, category := range got.Categories {
		plain[i].Type = category.Type
		for _, r := range category.Entries {
			r.Progress = nil
			plain[i].Entries = append(plain[i].Entries, r.Entry)
		}
	}
	if !reflect.DeepEqual(plain, want) {
		t.Fatalf("Get() = %+v, want %+v", plain, want)
	}
}

// TestGetServesTheSnapshotsOfAnEndedChallenge: once the challenge is over and
// its results are frozen, the ranking is the snapshot: an activity added to
// the table afterwards (the weeks are locked by the API, not by the table)
// changes nothing.
func TestGetServesTheSnapshotsOfAnEndedChallenge(t *testing.T) {
	pool := testdb.New(t)
	gdb, err := db.NewGorm(pool, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	s := seed{t: t, pool: pool}
	start := time.Date(2026, 6, 1, 0, 0, 0, 0, toronto(t))
	end := start.AddDate(0, 0, 14)
	runner := s.user("runner")
	var challengeID, timeCategory string
	if err := pool.QueryRow(t.Context(), `INSERT INTO challenges (title, visibility, timezone, starts_at, ends_at, created_by)
		VALUES ('Summer', 'public', 'America/Toronto', $1, $2, $3) RETURNING id`, start, end, runner).Scan(&challengeID); err != nil {
		t.Fatal(err)
	}
	s.exec("INSERT INTO participants (challenge_id, user_id, joined_at) VALUES ($1, $2, $3)", challengeID, runner, start)
	if err := pool.QueryRow(t.Context(), "INSERT INTO categories (challenge_id, type, position) VALUES ($1, 'cumulative_time', 1) RETURNING id", challengeID).Scan(&timeCategory); err != nil {
		t.Fatal(err)
	}
	s.activity(runner, "run", start.Add(time.Hour), 3600, nil, false)

	c := Challenge{
		ID: challengeID, Timezone: "America/Toronto", StartsAt: start.UTC(), EndsAt: end.UTC(),
		Categories: []Category{{Type: CategoryCumulativeTime}},
	}
	fake := clock.NewFake(end)
	svc := NewService(staticChallenge(c), fakeProfiles{}, NewRepository(gdb), fake, 24*time.Hour)
	valueOf := func() (int, time.Time) {
		t.Helper()
		got, err := svc.Get(t.Context(), runner, challengeID)
		if err != nil {
			t.Fatal(err)
		}
		return got.Categories[0].Entries[0].Value, got.ComputedAt
	}

	// The worker has not run: computed from the activities.
	if v, _ := valueOf(); v != 3600 {
		t.Fatalf("value = %d, want 3600 computed", v)
	}

	frozenAt := end.Add(time.Hour).UTC()
	s.exec(`INSERT INTO result_snapshot (category_id, challenge_id, category_type, payload, created_at) VALUES ($1, $2, 'cumulative_time', $3, $4)`,
		timeCategory, challengeID, `{"entries":[{"user_id":"`+runner+`","rank":1,"value":3600,"abandoned":false}]}`, frozenAt)
	s.activity(runner, "run", start.Add(2*time.Hour), 7200, nil, false)
	fake.Set(end.AddDate(0, 1, 0))

	if v, at := valueOf(); v != 3600 || !at.Equal(frozenAt) {
		t.Fatalf("value = %d at %s, want the snapshot 3600 at %s", v, at, frozenAt)
	}
}

// TestActivitiesGiveTheRankingsOfTheRawActivities checks that the totals of
// Activities (per participant, sport and week, SC-API-082) rank exactly as the
// raw activities do, for the three scorers, at every instant that matters: a
// varied data set over 4 weeks across the spring time change in Toronto, with
// several activities per week and sport, sports with and without distance,
// an optional distance left empty, a deleted activity and the first and last
// instants of each week.
func TestActivitiesGiveTheRankingsOfTheRawActivities(t *testing.T) {
	pool := testdb.New(t)
	gdb, err := db.NewGorm(pool, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	s := seed{t: t, pool: pool}
	loc := toronto(t)
	start := scoring.StartOfDay(2026, 3, 2, loc) // the time changes on March 8, in week 1
	end := scoring.StartOfDay(2026, 3, 30, loc)
	users := []string{s.user("u1"), s.user("u2"), s.user("u3"), s.user("u4")}
	var challengeID string
	if err := pool.QueryRow(t.Context(), `INSERT INTO challenges (title, visibility, timezone, starts_at, ends_at, created_by)
		VALUES ('Spring', 'public', 'America/Toronto', $1, $2, $3) RETURNING id`, start, end, users[0]).Scan(&challengeID); err != nil {
		t.Fatal(err)
	}
	for _, u := range users {
		s.exec("INSERT INTO participants (challenge_id, user_id, joined_at) VALUES ($1, $2, $3)", challengeID, u, start)
	}
	w := scoring.Window{Start: start, End: end, Location: loc}
	km := func(m int) *int { return &m }
	for i, u := range users {
		for n := 1; n <= 4; n++ {
			from, to := w.WeekBounds(n)
			s.activity(u, "run", from, 1800+i, km(5000+1000*n+i), false)              // first instant of the week
			s.activity(u, "run", to.Add(-time.Second), 1200, km(2000*(i+1)), false)   // last instant of the week
			s.activity(u, "ride", from.Add(30*time.Hour), 3600, km(20000+i*n), false) // another sport
			s.activity(u, "yoga", from.Add(50*time.Hour), 2700, nil, false)           // no distance
			// Optional distance left empty (a sport typed by hand needs its label).
			s.exec(`INSERT INTO activities (user_id, sport_code, sport_label, started_at, duration_seconds) VALUES ($1, 'other', 'Padel', $2, 600)`, u, from.Add(70*time.Hour))
			s.activity(u, "run", from.Add(90*time.Hour), 900, km(7000), i == 2) // deleted for u3
			if i == 0 {                                                         // u1 runs 3 km more each week: never eliminated
				s.activity(u, "run", from.Add(110*time.Hour), 900, km(3000*n), false)
			}
		}
	}
	s.activity(users[0], "run", start.Add(-time.Second), 3600, km(99000), false) // before the window
	s.activity(users[1], "run", end, 3600, km(99000), false)                     // at the end, outside

	repo := NewRepository(gdb)
	participants, err := repo.Participants(t.Context(), challengeID)
	if err != nil {
		t.Fatal(err)
	}
	// The raw activities, as Activities read them before SC-API-082.
	rows, err := pool.Query(t.Context(), `SELECT a.user_id, a.sport_code, a.started_at, a.duration_seconds, a.distance_meters
		FROM participants p JOIN activities a ON a.user_id = p.user_id
		WHERE p.challenge_id = $1 AND p.status = 'active' AND a.deleted_at IS NULL AND a.started_at >= $2 AND a.started_at < $3`,
		challengeID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	var raw []scoring.Activity
	for rows.Next() {
		var a scoring.Activity
		if err := rows.Scan(&a.UserID, &a.SportCode, &a.StartedAt, &a.DurationSeconds, &a.DistanceMeters); err != nil {
			t.Fatal(err)
		}
		a.StartedAt = a.StartedAt.UTC()
		raw = append(raw, a)
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	totals, err := repo.Activities(t.Context(), challengeID, w, "run")
	if err != nil {
		t.Fatal(err)
	}
	if len(totals) >= len(raw) {
		t.Fatalf("%d totals for %d raw activities, want fewer rows", len(totals), len(raw))
	}
	// Without an elimination category, nothing is summed per week.
	noWeekly, err := repo.Activities(t.Context(), challengeID, w, "")
	if err != nil {
		t.Fatal(err)
	}
	elimination := scoring.Elimination{Sport: "run", StepMeters: 1500}
	scorers := []struct {
		name   string
		scorer scoring.Scorer
		totals []scoring.Activity
	}{
		{"time", scoring.Duration{}, totals},
		{"distance", scoring.Distance{Factors: map[string]float64{"run": 1, "ride": 0.3, "other": 1}}, totals},
		{"elimination", elimination, totals},
		{"time, no elimination category", scoring.Duration{}, noWeekly},
		{"distance, no elimination category", scoring.Distance{Factors: map[string]float64{"run": 1, "ride": 0.3, "other": 1}}, noWeekly},
	}
	instants := []time.Time{start.Add(-time.Hour), end, end.Add(24 * time.Hour)}
	for n := 1; n <= 4; n++ {
		from, weekEnd := w.WeekBounds(n)
		instants = append(instants, from.Add(3*24*time.Hour), weekEnd.Add(-time.Second), weekEnd)
	}
	for _, sc := range scorers {
		for _, now := range instants {
			w.Now = now
			want, got := sc.scorer.Score(w, participants, raw), sc.scorer.Score(w, participants, sc.totals)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s at %s: totals give %+v, raw activities %+v", sc.name, now, got, want)
			}
		}
	}
	// The data set must judge something: at the end, someone is eliminated
	// and someone is not.
	w.Now = end
	var eliminated, survivors int
	for _, e := range elimination.Score(w, participants, raw) {
		if e.Progress != nil && e.Progress.Status == scoring.StatusEliminated {
			eliminated++
		} else {
			survivors++
		}
	}
	if eliminated == 0 || survivors == 0 {
		t.Fatalf("%d eliminated, %d survivors: want both, or the comparison proves little", eliminated, survivors)
	}
}

// BenchmarkActivities5000 measures what one leaderboard read loads for a
// challenge of 5 000 participants over 12 weeks, 5 activities a week each
// (300 000 activities), as SC-API-082 asks: the raw activities, as before,
// and the totals (per week for the runs, per sport for the rest). Run on demand:
//
//	go test -tags integration -run '^$' -bench Activities5000 -benchtime 10x ./internal/leaderboard/
func BenchmarkActivities5000(b *testing.B) {
	pool := testdb.New(b)
	gdb, err := db.NewGorm(pool, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		b.Fatal(err)
	}
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		b.Fatal(err)
	}
	start := scoring.StartOfDay(2026, 3, 2, loc)
	end := scoring.StartOfDay(2026, 5, 25, loc)
	var challengeID string
	if err := pool.QueryRow(b.Context(), `WITH u AS (
			INSERT INTO users (auth_subject, pseudo) SELECT 'bench|' || g, 'bench_' || g FROM generate_series(1, 5000) g RETURNING id)
		INSERT INTO challenges (title, visibility, timezone, starts_at, ends_at, created_by)
		SELECT 'Bench', 'public', 'America/Toronto', $1, $2, (SELECT id FROM u LIMIT 1) RETURNING id`, start, end).Scan(&challengeID); err != nil {
		b.Fatal(err)
	}
	if _, err := pool.Exec(b.Context(), `INSERT INTO participants (challenge_id, user_id, joined_at) SELECT $1::uuid, id, $2::timestamptz FROM users`, challengeID, start); err != nil {
		b.Fatal(err)
	}
	// 5 activities a week per participant: 3 runs, a ride and a yoga session.
	if _, err := pool.Exec(b.Context(), `INSERT INTO activities (user_id, sport_code, started_at, duration_seconds, distance_meters)
		SELECT u.id, s.sport, $1::timestamptz + (w * 7 + s.day) * interval '1 day' + interval '7 hours', 1800,
			CASE WHEN s.sport = 'yoga' THEN NULL ELSE 5000 + w * 100 END
		FROM users u, generate_series(0, 11) w,
			(VALUES ('run', 0), ('run', 2), ('run', 4), ('ride', 1), ('yoga', 3)) AS s(sport, day)`, start); err != nil {
		b.Fatal(err)
	}
	if _, err := pool.Exec(b.Context(), `ANALYZE`); err != nil {
		b.Fatal(err)
	}
	repo := NewRepository(gdb)
	w := scoring.Window{Start: start, End: end, Location: loc}
	// As Activities read them before SC-API-082, through GORM.
	b.Run("raw activities", func(b *testing.B) {
		for b.Loop() {
			var rows []struct {
				UserID          string
				SportCode       string
				StartedAt       time.Time
				DurationSeconds int
				DistanceMeters  *int
			}
			err := gdb.WithContext(b.Context()).Raw(`SELECT a.user_id, a.sport_code, a.started_at, a.duration_seconds, a.distance_meters
				FROM participants p JOIN activities a ON a.user_id = p.user_id
				WHERE p.challenge_id = @id AND p.status = 'active' AND a.deleted_at IS NULL AND a.started_at >= @from AND a.started_at < @to`,
				map[string]any{"id": challengeID, "from": start, "to": end}).Scan(&rows).Error
			if err != nil {
				b.Fatal(err)
			}
			b.ReportMetric(float64(len(rows)), "rows")
		}
	})
	b.Run("totals per week", func(b *testing.B) {
		for b.Loop() {
			totals, err := repo.Activities(b.Context(), challengeID, w, "run")
			if err != nil {
				b.Fatal(err)
			}
			b.ReportMetric(float64(len(totals)), "rows")
		}
	})
}
