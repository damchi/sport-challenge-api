//go:build integration

package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

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
	standings := standingsFunc(func(ctx context.Context, actorID string, cs []leaderboard.Challenge, now time.Time) (map[string]leaderboard.Standing, error) {
		return svc.Standings(ctx, actorID, cs, now)
	})
	challenges := challenge.NewService(challenge.NewRepository(gdb), challengeProfiles{users}, sport.NewService(sport.NewRepository(gdb)), standings, clk)
	svc = leaderboard.NewService(leaderboardChallenges{challenges}, leaderboardProfiles{users}, leaderboard.NewRepository(gdb), clk, 24*time.Hour)

	f := leaderboardFixture{t: t, svc: svc, challenges: challenges}
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
	if p == nil || p.Status != scoring.StatusInProgress || p.MinimumMeters == nil || *p.MinimumMeters != 7000 || p.RunMeters != 6000 {
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
			if r.Type != c.Type || r.Rank != mine.Rank || r.RankedCount != ranked || !reflect.DeepEqual(r.Progress, mine.Progress) {
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
