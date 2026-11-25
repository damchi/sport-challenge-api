//go:build integration

package admin

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damchi/sport-challenge-api/internal/challenge"
	"github.com/damchi/sport-challenge-api/internal/closure"
	"github.com/damchi/sport-challenge-api/internal/leaderboard"
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/sport"
)

// incompleteProfiles is module user for the challenges: the profiles of
// the accounts it holds are incomplete, every other one complete.
type incompleteProfiles map[string]bool

func (p incompleteProfiles) ProfileComplete(_ context.Context, id string) (bool, error) {
	return !p[id], nil
}

func (incompleteProfiles) Names(context.Context, []string) (map[string]challenge.Name, error) {
	return nil, nil
}

// creatorless is a challenge of three weeks from Monday 2026-04-06 in
// Toronto whose only creator, ann, deleted their account: bob takes part,
// cara abandoned, dan and fay never joined (fay's profile is incomplete),
// eve is the admin.
type creatorless struct {
	t                             *testing.T
	pool                          *pgxpool.Pool
	r                             *Repository
	id                            string
	ann, bob, cara, dan, fay, eve string
	start                         time.Time
}

// newCreatorless inserts the challenge, deleted when deleted; with
// keepCreator, ann keeps her account and her role.
func newCreatorless(t *testing.T, deleted, keepCreator bool) creatorless {
	t.Helper()
	r, pool := newRepository(t)
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 4, 6, 0, 0, 0, 0, loc)
	created := start.AddDate(0, 0, -7).UTC()
	f := creatorless{t: t, pool: pool, r: r, start: start}
	f.ann = insertUser(t, pool, "email|ann", created, false)
	f.bob = insertUser(t, pool, "email|bob", created, false)
	f.cara = insertUser(t, pool, "email|cara", created, false)
	f.dan = insertUser(t, pool, "email|dan", created, false)
	f.fay = insertUser(t, pool, "email|fay", created, false)
	f.eve = insertUser(t, pool, "email|eve", created, false)
	f.id = insertChallengeRow(t, pool, f.ann, challengeRow{title: "Spring", visibility: "private", startsAt: start.UTC(), weeks: 3, createdAt: created})
	exec(t, pool, `INSERT INTO categories (challenge_id, type, position, params) VALUES ($1, 'cumulative_time', 1, '{}'),
		($1, 'cumulative_distance', 2, '{"factors": {"run": 1, "walk": 1, "ride": 0.3, "other": 1}}'),
		($1, 'elimination', 3, '{"sport": "run", "step_meters": 2000}')`, f.id)
	exec(t, pool, `INSERT INTO participants (challenge_id, user_id, joined_at) VALUES ($1, $2, $3)`, f.id, f.bob, start.UTC())
	exec(t, pool, `INSERT INTO participants (challenge_id, user_id, joined_at, status, abandoned_at) VALUES ($1, $2, $3, 'abandoned', $4)`,
		f.id, f.cara, start.UTC(), start.AddDate(0, 0, 2).UTC())
	exec(t, pool, `INSERT INTO activities (user_id, sport_code, started_at, duration_seconds, distance_meters) VALUES ($1, 'run', $2, 1800, 5000)`,
		f.bob, start.Add(10*time.Hour))
	if deleted {
		exec(t, pool, `UPDATE challenges SET deleted_at = $2 WHERE id = $1`, f.id, start.AddDate(0, 0, 3).UTC())
	}
	if !keepCreator {
		// As an account deletion erases them (challenge.Repository.EraseUser).
		exec(t, pool, `DELETE FROM challenge_creators WHERE user_id = $1`, f.ann)
		exec(t, pool, `DELETE FROM participants WHERE user_id = $1`, f.ann)
		exec(t, pool, `UPDATE users SET anonymized_at = $2 WHERE id = $1`, f.ann, start.AddDate(0, 0, 4).UTC())
	}
	return f
}

// corrections are the admin changes of the challenge itself at now.
func (f creatorless) corrections(now time.Time) *ChallengeCorrections {
	gdb := f.r.db
	clk := clock.NewFake(now)
	worker := closure.NewService(closure.NewRepository(gdb), leaderboard.NewRepository(gdb), clk, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	challenges := challenge.NewService(challenge.NewRepository(gdb), incompleteProfiles{f.fay: true}, sport.NewService(sport.NewRepository(gdb)), nil, clk)
	return NewChallengeCorrections(NewCorrections(f.r, NewJournal(f.r, clk), worker, noBoards{}), challenges, nil)
}

// creatorlessState is what a restore may write.
type creatorlessState struct {
	Deleted  bool
	Actions  int
	Creators []string
	Status   map[string]string
}

func (f creatorless) read() creatorlessState {
	f.t.Helper()
	s := creatorlessState{Status: map[string]string{}}
	if err := f.pool.QueryRow(f.t.Context(), `SELECT deleted_at IS NOT NULL, (SELECT count(*) FROM admin_actions),
		ARRAY(SELECT user_id::text FROM challenge_creators WHERE challenge_id = $1 ORDER BY user_id)
		FROM challenges WHERE id = $1`, f.id).Scan(&s.Deleted, &s.Actions, &s.Creators); err != nil {
		f.t.Fatal(err)
	}
	rows, err := f.pool.Query(f.t.Context(), `SELECT user_id::text, status FROM participants WHERE challenge_id = $1`, f.id)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var user, status string
		if err := rows.Scan(&user, &status); err != nil {
			f.t.Fatal(err)
		}
		s.Status[user] = status
	}
	if err := rows.Err(); err != nil {
		f.t.Fatal(err)
	}
	return s
}

// lastAction is the newest journal line.
func (f creatorless) lastAction() Action {
	f.t.Helper()
	actions, err := f.r.ListActions(f.t.Context(), ActionQuery{Limit: 1})
	if err != nil {
		f.t.Fatal(err)
	}
	if len(actions) != 1 {
		f.t.Fatalf("journal %+v, want a line", actions)
	}
	return actions[0]
}

func codeOf(err error) apperr.Code {
	var appErr *apperr.Error
	if errors.As(err, &appErr) {
		return appErr.Code
	}
	return ""
}

// TestRestoreKeepsACreatorFromTheDatabase (SC-API-183, README §2.12): the
// restore of a challenge that would come back ongoing without any creator
// is refused unless the admin names one, preview included; the account
// named becomes a creator in the restore, first added as an active
// participant when it never joined, and the journal line names it; a
// refused restore writes nothing.
func TestRestoreKeepsACreatorFromTheDatabase(t *testing.T) {
	ptr := func(s string) *string { return &s }
	// Ongoing: in week 2.
	at := func(f creatorless) time.Time { return f.start.AddDate(0, 0, 9).UTC() }

	t.Run("refused without a creator named, preview included, nothing written", func(t *testing.T) {
		f := newCreatorless(t, true, false)
		before := f.read()
		for _, dryRun := range []bool{true, false} {
			_, err := f.corrections(at(f)).Restore(t.Context(), auth.Actor{ID: f.eve, Admin: true}, f.id, nil, "Supprimé par erreur", dryRun)
			if codeOf(err) != apperr.CodeNoCreator {
				t.Fatalf("restore (dry run %v): %v, want no_creator", dryRun, err)
			}
		}
		if got := f.read(); !got.Deleted || got.Actions != before.Actions || len(got.Creators) != 0 {
			t.Fatalf("state %+v, want it still deleted, nothing written", got)
		}
	})

	t.Run("refused naming an account that cannot be named, nothing restored", func(t *testing.T) {
		f := newCreatorless(t, true, false)
		for _, tt := range []struct {
			name, user string
			want       apperr.Code
		}{
			{name: "incomplete profile", user: f.fay, want: apperr.CodeProfileIncomplete},
			{name: "unknown account", user: uuid.NewString(), want: apperr.CodeUserNotFound},
			{name: "deleted account", user: f.ann, want: apperr.CodeUserNotFound},
		} {
			for _, dryRun := range []bool{true, false} {
				_, err := f.corrections(at(f)).Restore(t.Context(), auth.Actor{ID: f.eve, Admin: true}, f.id, ptr(tt.user), "Supprimé par erreur", dryRun)
				if codeOf(err) != tt.want {
					t.Fatalf("%s (dry run %v): %v, want %s", tt.name, dryRun, err, tt.want)
				}
			}
		}
		if got := f.read(); !got.Deleted || got.Actions != 0 || len(got.Creators) != 0 || got.Status[f.fay] != "" {
			t.Fatalf("state %+v, want nothing written", got)
		}
	})

	t.Run("a participant named becomes a creator, the challenge recomputed", func(t *testing.T) {
		f := newCreatorless(t, true, false)
		if _, err := f.corrections(at(f)).Restore(t.Context(), auth.Actor{ID: f.eve, Admin: true}, f.id, ptr(f.bob), "Supprimé par erreur", false); err != nil {
			t.Fatal(err)
		}
		got := f.read()
		if got.Deleted || len(got.Creators) != 1 || got.Creators[0] != f.bob || got.Status[f.bob] != "active" {
			t.Fatalf("state %+v, want it restored, bob its creator", got)
		}
		var closures int
		if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM week_closure WHERE challenge_id = $1`, f.id).Scan(&closures); err != nil {
			t.Fatal(err)
		}
		if closures != 1 {
			t.Fatalf("%d weeks judged, want week 1, locked while deleted, judged by the recompute", closures)
		}
		a := f.lastAction()
		if a.Action != ActionChallengeRestore ||
			string(a.NewValue) != `{"deleted_at": null, "named_creator": {"creator": true, "user_id": "`+f.bob+`"}}` ||
			string(a.OldValue) != `{"deleted_at": "`+f.start.AddDate(0, 0, 3).UTC().Format(time.RFC3339Nano)+`", "named_creator": {"creator": false, "user_id": "`+f.bob+`"}}` {
			t.Fatalf("journal line %s -> %s, want bob named", a.OldValue, a.NewValue)
		}
	})

	t.Run("one who abandoned named becomes a creator, not reinstated", func(t *testing.T) {
		f := newCreatorless(t, true, false)
		if _, err := f.corrections(at(f)).Restore(t.Context(), auth.Actor{ID: f.eve, Admin: true}, f.id, ptr(f.cara), "Supprimé par erreur", false); err != nil {
			t.Fatal(err)
		}
		if got := f.read(); got.Deleted || len(got.Creators) != 1 || got.Creators[0] != f.cara || got.Status[f.cara] != "abandoned" {
			t.Fatalf("state %+v, want cara a creator, still out", got)
		}
	})

	t.Run("an account that never joined named takes part and becomes a creator", func(t *testing.T) {
		f := newCreatorless(t, true, false)
		if _, err := f.corrections(at(f)).Restore(t.Context(), auth.Actor{ID: f.eve, Admin: true}, f.id, ptr(f.dan), "Supprimé par erreur", false); err != nil {
			t.Fatal(err)
		}
		if got := f.read(); got.Deleted || len(got.Creators) != 1 || got.Creators[0] != f.dan || got.Status[f.dan] != "active" {
			t.Fatalf("state %+v, want dan an active participant and its creator", got)
		}
		a := f.lastAction()
		if string(a.NewValue) != `{"deleted_at": null, "named_creator": {"status": "active", "creator": true, "user_id": "`+f.dan+`"}}` ||
			string(a.OldValue) != `{"deleted_at": "`+f.start.AddDate(0, 0, 3).UTC().Format(time.RFC3339Nano)+`", "named_creator": {"creator": false, "user_id": "`+f.dan+`"}}` {
			t.Fatalf("journal line %s -> %s, want dan's participation and role, none before", a.OldValue, a.NewValue)
		}
	})

	t.Run("a creator named while one is left joins the creators", func(t *testing.T) {
		f := newCreatorless(t, true, true)
		if _, err := f.corrections(at(f)).Restore(t.Context(), auth.Actor{ID: f.eve, Admin: true}, f.id, ptr(f.bob), "Supprimé par erreur", false); err != nil {
			t.Fatal(err)
		}
		got := f.read()
		want := []string{f.ann, f.bob}
		if want[0] > want[1] {
			want[0], want[1] = want[1], want[0]
		}
		if got.Deleted || len(got.Creators) != 2 || got.Creators[0] != want[0] || got.Creators[1] != want[1] {
			t.Fatalf("state %+v, want ann and bob its creators", got)
		}
	})

	t.Run("a finished challenge without a creator is restored without one", func(t *testing.T) {
		f := newCreatorless(t, true, false)
		over := f.start.AddDate(0, 0, 30).UTC()
		if _, err := f.corrections(over).Restore(t.Context(), auth.Actor{ID: f.eve, Admin: true}, f.id, nil, "Supprimé par erreur", false); err != nil {
			t.Fatal(err)
		}
		if got := f.read(); got.Deleted || len(got.Creators) != 0 {
			t.Fatalf("state %+v, want it restored, still without a creator", got)
		}
	})
}

// TestSettingsKeepACreatorFromTheDatabase (SC-API-183, README §2.12): a
// finished challenge without any creator made longer, ongoing again, is
// refused, preview included; the same challenge changed but left finished
// goes through.
func TestSettingsKeepACreatorFromTheDatabase(t *testing.T) {
	f := newCreatorless(t, false, false)
	over := f.start.AddDate(0, 0, 30).UTC()
	admin := auth.Actor{ID: f.eve, Admin: true}
	ptr := func(n int) *int { return &n }
	title := "Spring again"
	later := f.start.AddDate(0, 0, 42).Format(time.DateOnly)
	for _, tt := range []struct {
		name    string
		changes challenge.SettingsChanges
	}{
		{name: "made ongoing again", changes: challenge.SettingsChanges{Weeks: ptr(6)}},
		{name: "made upcoming", changes: challenge.SettingsChanges{StartDate: &later}},
	} {
		for _, dryRun := range []bool{true, false} {
			_, err := f.corrections(over).UpdateSettings(t.Context(), admin, f.id, tt.changes, "Prolongé", dryRun)
			if codeOf(err) != apperr.CodeNoCreator {
				t.Fatalf("%s (dry run %v): %v, want no_creator", tt.name, dryRun, err)
			}
		}
	}
	if got := f.read(); got.Actions != 0 {
		t.Fatalf("journal %d lines, want nothing written", got.Actions)
	}
	if _, err := f.corrections(over).UpdateSettings(t.Context(), admin, f.id, challenge.SettingsChanges{Weeks: ptr(4), Title: &title}, "Prolongé", false); err != nil {
		t.Fatalf("made longer, still finished: %v", err)
	}
	if got := f.read(); got.Actions != 1 {
		t.Fatalf("journal %d lines, want the change recorded", got.Actions)
	}
}
