//go:build integration

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damchi/sport-challenge-api/internal/challenge"
	"github.com/damchi/sport-challenge-api/internal/leaderboard"
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/platform/db/testdb"
	"github.com/damchi/sport-challenge-api/internal/scoring"
	"github.com/damchi/sport-challenge-api/internal/sport"
	"github.com/damchi/sport-challenge-api/internal/user"
)

// leaderboardFixture wires the modules as run does, at recordNow (week 2 of
// the challenges below, 3.5 days before it ends): a runner who created and
// joined two challenges, and a stranger who never joined any.
type leaderboardFixture struct {
	t          *testing.T
	pool       *pgxpool.Pool
	svc        *leaderboard.Service
	challenges *challenge.Service
	runner     string
	stranger   string
	// quitter abandoned the private challenge.
	quitter string
	public  string
	private string
	deleted string
}

func newLeaderboardFixture(t *testing.T) leaderboardFixture {
	t.Helper()
	pool := testdb.New(t)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	gdb, err := db.NewGorm(pool, logger)
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(recordNow)
	users := user.NewService(user.NewRepository(gdb), nil, nil, nil, nil, logger)
	var svc *leaderboard.Service
	standings := challengeStandings{
		standings: func(ctx context.Context, actorID string, cs []leaderboard.Challenge, now time.Time) (map[string]leaderboard.Standing, error) {
			return svc.Standings(ctx, actorID, cs, now)
		},
		results: func(ctx context.Context, actorID string, cs []leaderboard.Challenge) (map[string][]leaderboard.Place, error) {
			return svc.Results(ctx, actorID, cs)
		},
	}
	challenges := challenge.NewService(challenge.NewRepository(gdb), challengeProfiles{users}, sport.NewService(sport.NewRepository(gdb)), standings, clk)
	svc = leaderboard.NewService(leaderboardChallenges{challenges}, leaderboardProfiles{users}, leaderboard.NewRepository(gdb), clk, 24*time.Hour)

	f := leaderboardFixture{t: t, pool: pool, svc: svc, challenges: challenges}
	insertUser := func(subject string) string {
		var id string
		if err := pool.QueryRow(t.Context(), "INSERT INTO users (auth_subject, pseudo, first_name, last_name) VALUES ($1, $1, 'Test', 'User') RETURNING id", subject).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	f.runner, f.stranger, f.quitter = insertUser("runner"), insertUser("stranger"), insertUser("quitter")

	start := toronto(t, 2026, 6, 1, 0)
	end := start.AddDate(0, 0, 21)
	insertChallenge := func(visibility string, deleted bool) string {
		var deletedAt *time.Time
		if deleted {
			deletedAt = &recordNow
		}
		var id string
		if err := pool.QueryRow(t.Context(), `INSERT INTO challenges (title, visibility, timezone, starts_at, ends_at, created_by, deleted_at)
			VALUES ('Spring', $1, 'America/Toronto', $2, $3, $4, $5) RETURNING id`, visibility, start, end, f.runner, deletedAt).Scan(&id); err != nil {
			t.Fatal(err)
		}
		// The categories as the challenge module freezes them.
		for _, c := range []struct{ typ, params string }{
			{challenge.CategoryCumulativeTime, `{}`},
			{challenge.CategoryCumulativeDistance, `{"factors": {"run": 1, "ride": 0.3}}`},
			{challenge.CategoryElimination, `{"sport": "run", "step_meters": 2000}`},
		} {
			if _, err := pool.Exec(t.Context(), `INSERT INTO categories (challenge_id, type, position, params)
				VALUES ($1, $2, (SELECT count(*) + 1 FROM categories WHERE challenge_id = $1), $3::jsonb)`, id, c.typ, c.params); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := pool.Exec(t.Context(), "INSERT INTO challenge_creators (challenge_id, user_id) VALUES ($1, $2)", id, f.runner); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(t.Context(), "INSERT INTO participants (challenge_id, user_id, joined_at) VALUES ($1, $2, $3)", id, f.runner, start); err != nil {
			t.Fatal(err)
		}
		return id
	}
	f.public, f.private, f.deleted = insertChallenge("public", false), insertChallenge("private", false), insertChallenge("private", true)
	if _, err := pool.Exec(t.Context(), `INSERT INTO participants (challenge_id, user_id, status, joined_at, abandoned_at)
		VALUES ($1, $2, 'abandoned', $3, $4)`, f.private, f.quitter, start, start.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	for _, run := range []struct {
		at       time.Time
		distance int
	}{{toronto(t, 2026, 6, 2, 7), 5000}, {toronto(t, 2026, 6, 10, 7), 6000}} {
		if _, err := pool.Exec(t.Context(), `INSERT INTO activities (user_id, sport_code, started_at, duration_seconds, distance_meters)
			VALUES ($1, 'run', $2, 1800, $3)`, f.runner, run.at, run.distance); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func TestLeaderboardOfAPublicChallengeIsOpenToAStranger(t *testing.T) {
	f := newLeaderboardFixture(t)

	got, err := f.svc.Get(t.Context(), f.stranger, f.public)
	if err != nil {
		t.Fatal(err)
	}
	if !got.ComputedAt.Equal(recordNow) || len(got.Categories) != 3 {
		t.Fatalf("Get() = %+v, want 3 categories computed at %s", got, recordNow)
	}
	want := []struct {
		typ   string
		value int
	}{
		{leaderboard.CategoryCumulativeTime, 3600},
		{leaderboard.CategoryCumulativeDistance, 11000},
		{leaderboard.CategoryElimination, 11000},
	}
	for i, w := range want {
		c := got.Categories[i]
		if c.Type != w.typ || len(c.Entries) != 1 {
			t.Fatalf("category %d = %+v, want %s with the runner alone", i, c, w.typ)
		}
		if e := c.Entries[0]; e.UserID != f.runner || e.Rank != 1 || e.Value != w.value || e.Name.Pseudo == nil || *e.Name.Pseudo != "runner" || e.Name.FirstName == nil || *e.Name.FirstName != "Test" {
			t.Fatalf("%s entry = %+v, want the runner first with %d", w.typ, e, w.value)
		}
	}
	// 6 km so far in week 2, 1 km short of the 7 km the week asks for.
	p := got.Categories[2].Entries[0].Progress
	if p == nil || p.Status != scoring.StatusInProgress || p.MinimumMeters == nil || *p.MinimumMeters != 7000 || p.DistanceMeters != 6000 {
		t.Fatalf("progress = %+v, want in progress, 6000 of 7000 m", p)
	}
}

func TestLeaderboardOfAPrivateChallengeIsHiddenFromAStranger(t *testing.T) {
	f := newLeaderboardFixture(t)

	for name, tt := range map[string]struct{ actor, challenge string }{
		"private challenge, not a member":     {f.stranger, f.private},
		"missing challenge":                   {f.runner, "0b1c2d3e-4f50-4a61-8b72-93a4b5c6d7e8"},
		"deleted challenge, even its creator": {f.runner, f.deleted},
	} {
		_, err := f.svc.Get(t.Context(), tt.actor, tt.challenge)
		var appErr *apperr.Error
		if !errors.As(err, &appErr) || appErr.Code != apperr.CodeChallengeNotFound {
			t.Fatalf("%s: error = %v, want challenge_not_found", name, err)
		}
	}
	// Its creator reads it, and so does a participant who abandoned.
	for name, actor := range map[string]string{"creator": f.runner, "participant who abandoned": f.quitter} {
		got, err := f.svc.Get(t.Context(), actor, f.private)
		if err != nil {
			t.Fatalf("%s of the private challenge: %v", name, err)
		}
		// The quitter is listed, unranked, in every category.
		if entries := got.Categories[0].Entries; len(entries) != 2 || !entries[1].Abandoned || entries[1].UserID != f.quitter {
			t.Fatalf("%s: entries = %+v, want the runner then the quitter, who abandoned", name, entries)
		}
	}
}

// TestMyWeekIsTheLeaderboard checks that the standing of the runner in their
// list is their line in the leaderboard of each ongoing challenge, read at
// the same instant, and that the one who abandoned has none.
func TestMyWeekIsTheLeaderboard(t *testing.T) {
	f := newLeaderboardFixture(t)

	page, err := f.challenges.List(t.Context(), f.runner, challenge.ScopeMine, "", 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("%d challenges, want the public and the private ones", len(page.Items))
	}
	for _, it := range page.Items {
		board, err := f.svc.Get(t.Context(), f.runner, it.ID)
		if err != nil {
			t.Fatal(err)
		}
		if it.MyWeek == nil || it.MyWeek.Week != 2 || len(it.MyWeek.Rankings) != len(board.Categories) {
			t.Fatalf("%s: my week %+v, want week 2 in the %d categories", it.ID, it.MyWeek, len(board.Categories))
		}
		for i, c := range board.Categories {
			var ranked int
			var mine leaderboard.Ranked
			for _, e := range c.Entries {
				if e.Rank > 0 {
					ranked++
				}
				if e.UserID == f.runner {
					mine = e
				}
			}
			r := it.MyWeek.Rankings[i]
			if c.RankedCount != ranked {
				t.Fatalf("%s %s: leaderboard ranked count %d, want the %d ranked lines", it.ID, c.Type, c.RankedCount, ranked)
			}
			if r.Type != c.Type || r.Rank != mine.Rank || r.RankedCount != c.RankedCount || !reflect.DeepEqual(r.Progress, mine.Progress) {
				t.Fatalf("%s %s: %+v, want rank %d of %d with %+v", it.ID, c.Type, r, mine.Rank, ranked, mine.Progress)
			}
		}
		if p := it.MyWeek.Rankings[2].Progress; p == nil || !it.MyWeek.WeekEndsAt.Equal(p.WeekEndsAt) {
			t.Fatalf("%s: week ends at %s, want the end of the elimination week %+v", it.ID, it.MyWeek.WeekEndsAt, p)
		}
	}

	quitter, err := f.challenges.List(t.Context(), f.quitter, challenge.ScopeMine, "", 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(quitter.Items) != 1 || quitter.Items[0].MyWeek != nil {
		t.Fatalf("quitter's list %+v, want the private challenge without a standing", quitter.Items)
	}
}

// TestRankedCountIsMyRankingCount: in week 3 of a challenge, the leaderboard
// and the runner's list count the same participants as ranked in each
// category: the one eliminated in week 2 is, the one who abandoned and the
// deleted account are not.
func TestRankedCountIsMyRankingCount(t *testing.T) {
	f := newLeaderboardFixture(t)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := f.pool.Exec(t.Context(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	insertUser := func(subject string) string {
		t.Helper()
		var id string
		if err := f.pool.QueryRow(t.Context(), "INSERT INTO users (auth_subject, pseudo, first_name, last_name) VALUES ($1, $1, 'Test', 'User') RETURNING id", subject).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	dropped, gone := insertUser("dropped"), insertUser("gone")

	// Four weeks from 2026-05-25: recordNow is in week 3, week 2 locked.
	start := toronto(t, 2026, 5, 25, 0)
	var id string
	if err := f.pool.QueryRow(t.Context(), `INSERT INTO challenges (title, visibility, timezone, starts_at, ends_at, created_by)
		VALUES ('Summer', 'public', 'America/Toronto', $1, $2, $3) RETURNING id`, start, start.AddDate(0, 0, 28), f.runner).Scan(&id); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ typ, params string }{
		{challenge.CategoryCumulativeTime, `{}`},
		{challenge.CategoryCumulativeDistance, `{"factors": {"run": 1}}`},
		{challenge.CategoryElimination, `{"sport": "run", "step_meters": 2000}`},
	} {
		exec(`INSERT INTO categories (challenge_id, type, position, params)
			VALUES ($1, $2, (SELECT count(*) + 1 FROM categories WHERE challenge_id = $1), $3::jsonb)`, id, c.typ, c.params)
	}
	exec("INSERT INTO challenge_creators (challenge_id, user_id) VALUES ($1, $2)", id, f.runner)
	for _, u := range []string{f.runner, dropped, gone} {
		exec("INSERT INTO participants (challenge_id, user_id, joined_at) VALUES ($1, $2, $3)", id, u, start)
	}
	exec(`INSERT INTO participants (challenge_id, user_id, status, joined_at, abandoned_at)
		VALUES ($1, $2, 'abandoned', $3, $4)`, id, f.quitter, start, start.Add(time.Hour))
	// runner: 5 then 8 km; dropped: 5 then 5 km, under the 7 km of week 2.
	for _, run := range []struct {
		user    string
		day, km int
	}{{f.runner, 26, 5}, {f.runner, 2, 8}, {dropped, 26, 5}, {dropped, 2, 5}, {gone, 26, 20}} {
		month := 5
		if run.day < 25 {
			month = 6
		}
		exec(`INSERT INTO activities (user_id, sport_code, started_at, duration_seconds, distance_meters)
			VALUES ($1, 'run', $2, 1800, $3)`, run.user, toronto(t, 2026, month, run.day, 7), run.km*1000)
	}
	exec("UPDATE users SET anonymized_at = $1, pseudo = NULL WHERE id = $2", recordNow, gone)

	board, err := f.svc.Get(t.Context(), f.runner, id)
	if err != nil {
		t.Fatal(err)
	}
	page, err := f.challenges.List(t.Context(), f.runner, challenge.ScopeMine, "", 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	var mine *challenge.Standing
	for _, it := range page.Items {
		if it.ID == id {
			mine = it.MyWeek
		}
	}
	if mine == nil || mine.Week != 3 || len(mine.Rankings) != len(board.Categories) {
		t.Fatalf("my week %+v, want week 3 in the %d categories", mine, len(board.Categories))
	}
	for i, c := range board.Categories {
		if c.RankedCount != 2 || mine.Rankings[i].RankedCount != c.RankedCount {
			t.Fatalf("%s: leaderboard counts %d, my week %d, want the runner and dropped", c.Type, c.RankedCount, mine.Rankings[i].RankedCount)
		}
	}
	if p := board.Categories[2].Entries[1].Progress; board.Categories[2].Entries[1].UserID != dropped || p == nil || p.Status != scoring.StatusEliminated {
		t.Fatalf("second in the elimination: %+v, want dropped, eliminated", board.Categories[2].Entries[1])
	}
}

// TestBestResultIsTheFrozenLeaderboard: in the list, the best result of a
// finished challenge is the runner's best line of its frozen leaderboard: the
// deleted account ahead left out, the runner moves up to share the first
// rank in two categories, and the first of them is kept. The participant who
// abandoned gets none.
func TestBestResultIsTheFrozenLeaderboard(t *testing.T) {
	f := newLeaderboardFixture(t)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := f.pool.Exec(t.Context(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	insertUser := func(subject string) string {
		t.Helper()
		var id string
		if err := f.pool.QueryRow(t.Context(), "INSERT INTO users (auth_subject, pseudo, first_name, last_name) VALUES ($1, $1, 'Test', 'User') RETURNING id", subject).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	other, gone := insertUser("other"), insertUser("gone")

	// Three weeks from 2026-04-06, locked long before recordNow.
	start := toronto(t, 2026, 4, 6, 0)
	end := start.AddDate(0, 0, 21)
	var id string
	if err := f.pool.QueryRow(t.Context(), `INSERT INTO challenges (title, visibility, timezone, starts_at, ends_at, created_by)
		VALUES ('April', 'public', 'America/Toronto', $1, $2, $3) RETURNING id`, start, end, f.runner).Scan(&id); err != nil {
		t.Fatal(err)
	}
	exec("INSERT INTO challenge_creators (challenge_id, user_id) VALUES ($1, $2)", id, f.runner)
	for _, u := range []string{f.runner, other, gone} {
		exec("INSERT INTO participants (challenge_id, user_id, joined_at) VALUES ($1, $2, $3)", id, u, start)
	}
	exec(`INSERT INTO participants (challenge_id, user_id, status, joined_at, abandoned_at)
		VALUES ($1, $2, 'abandoned', $3, $4)`, id, f.quitter, start, start.Add(time.Hour))
	entry := func(user string, rank, value int) string {
		return fmt.Sprintf(`{"user_id":%q,"rank":%d,"value":%d,"abandoned":false}`, user, rank, value)
	}
	quit := fmt.Sprintf(`{"user_id":%q,"rank":0,"value":0,"abandoned":true}`, f.quitter)
	for _, c := range []struct {
		typ, params string
		entries     []string
	}{
		{challenge.CategoryCumulativeTime, `{}`, []string{entry(gone, 1, 9000), entry(f.runner, 2, 5000), entry(other, 3, 4000), quit}},
		{challenge.CategoryCumulativeDistance, `{"factors": {"run": 1}}`, []string{entry(f.runner, 1, 20000), entry(other, 1, 20000), entry(gone, 3, 1000), quit}},
		{challenge.CategoryElimination, `{"sport": "run", "step_meters": 2000}`, []string{entry(other, 1, 20000), entry(f.runner, 2, 18000), entry(gone, 3, 1000), quit}},
	} {
		var category string
		if err := f.pool.QueryRow(t.Context(), `INSERT INTO categories (challenge_id, type, position, params)
			VALUES ($1, $2, (SELECT count(*) + 1 FROM categories WHERE challenge_id = $1), $3::jsonb) RETURNING id`, id, c.typ, c.params).Scan(&category); err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO result_snapshot (category_id, challenge_id, category_type, payload, created_at) VALUES ($1, $2, $3, $4, $5)`,
			category, id, c.typ, `{"entries":[`+strings.Join(c.entries, ",")+`]}`, end.Add(7*time.Hour))
	}
	exec("UPDATE users SET anonymized_at = $1, pseudo = NULL WHERE id = $2", recordNow, gone)

	bestOf := func(userID string) *challenge.Ranking {
		t.Helper()
		page, err := f.challenges.List(t.Context(), userID, challenge.ScopeMine, challenge.StatusFinished, 20, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 || page.Items[0].ID != id {
			t.Fatalf("finished challenges %+v, want April alone", page.Items)
		}
		return page.Items[0].BestResult
	}
	board, err := f.svc.Get(t.Context(), f.runner, id)
	if err != nil {
		t.Fatal(err)
	}
	first := board.Categories[0]
	if first.RankedCount != 2 || first.Entries[0].UserID != f.runner || first.Entries[0].Rank != 1 {
		t.Fatalf("frozen time ranking %+v, want the runner first of 2", first)
	}
	want := &challenge.Ranking{Type: challenge.CategoryCumulativeTime, Rank: 1, RankedCount: first.RankedCount}
	if got := bestOf(f.runner); !reflect.DeepEqual(got, want) {
		t.Fatalf("runner's best result %+v, want %+v", got, want)
	}
	if got := bestOf(f.quitter); got != nil {
		t.Fatalf("quitter's best result %+v, want none", got)
	}
}
