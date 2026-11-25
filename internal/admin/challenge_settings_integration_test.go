//go:build integration

package admin

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/challenge"
	"github.com/damchi/sport-challenge-api/internal/closure"
	"github.com/damchi/sport-challenge-api/internal/invitation"
	"github.com/damchi/sport-challenge-api/internal/leaderboard"
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/sport"
)

// briefLocks is the store of the corrections with a wait of 200 ms for each
// lock, instead of 10 s.
type briefLocks struct{ *Repository }

func (b briefLocks) InLockedTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return b.inLockedTx(ctx, "200ms", fn)
}

// settingsState is what a change of the settings may touch.
type settingsState struct {
	EndsAt       time.Time
	Snapshots    int
	Closures     int
	Actions      int
	Eliminations []EliminationRow
	// Expiries are those of the invitation links, active then revoked.
	Expiries []time.Time
	Types    []string
}

// TestChallengeCorrectionsUpdateSettingsFromTheDatabase: a finished
// challenge made longer is in progress again, its frozen results erased and
// frozen again by finalize-challenges only at its new end; a preview
// changes nothing; a first day moved moves the expiry of the links not
// revoked; a lock waited for too long answers busy, nothing written.
func TestChallengeCorrectionsUpdateSettingsFromTheDatabase(t *testing.T) {
	r, pool := newRepository(t)
	gdb := r.db
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	// Two weeks from Monday 2026-03-02; week n locks at 7 am on the first
	// day of week n+1.
	start := time.Date(2026, 3, 2, 0, 0, 0, 0, loc)
	locked := func(n int) time.Time { return time.Date(2026, 3, 2+7*n, 7, 0, 0, 0, loc).UTC() }
	ann := insertUser(t, pool, "email|ann", start.AddDate(0, -1, 0), false)
	bob := insertUser(t, pool, "email|bob", start.AddDate(0, -1, 0), false)
	id := insertChallengeRow(t, pool, ann, challengeRow{title: "Spring", visibility: "public", startsAt: start.UTC(), weeks: 2, createdAt: start.AddDate(0, 0, -7).UTC()})
	// Two weeks in local days: the time changes on 2026-03-08.
	exec(t, pool, `UPDATE challenges SET ends_at = $2 WHERE id = $1`, id, start.AddDate(0, 0, 14).UTC())
	exec(t, pool, `INSERT INTO categories (challenge_id, type, position, params) VALUES ($1, 'cumulative_time', 1, '{}'),
		($1, 'cumulative_distance', 2, '{"factors": {"run": 1, "walk": 1, "ride": 0.3, "other": 1}}'),
		($1, 'elimination', 3, '{"sport": "run", "step_meters": 2000}')`, id)
	exec(t, pool, `INSERT INTO participants (challenge_id, user_id, joined_at) VALUES ($1, $2, $3)`, id, bob, start)
	for _, a := range []struct {
		user         string
		week, meters int
	}{{ann, 1, 5000}, {bob, 1, 5000}, {ann, 2, 7000}, {bob, 2, 6000}} {
		exec(t, pool, `INSERT INTO activities (user_id, sport_code, started_at, duration_seconds, distance_meters) VALUES ($1, 'run', $2, 1800, $3)`,
			a.user, start.AddDate(0, 0, 7*(a.week-1)).Add(10*time.Hour), a.meters)
	}
	weekOneEnd := start.AddDate(0, 0, 7).UTC()
	// An active link (token 1) and a revoked one (token 2).
	for token, revoked := range []bool{false, true} {
		hash := make([]byte, 32)
		hash[0] = byte(token + 1)
		var revokedAt *time.Time
		if revoked {
			at := start.UTC()
			revokedAt = &at
		}
		exec(t, pool, `INSERT INTO invitations (challenge_id, invited_by, token_hash, expires_at, created_at, revoked_at) VALUES ($1, $2, $3, $4, $5, $6)`,
			id, ann, hash, weekOneEnd, start.UTC(), revokedAt)
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	worker := func(at time.Time) *closure.Service {
		return closure.NewService(closure.NewRepository(gdb), leaderboard.NewRepository(gdb), clock.NewFake(at), logger)
	}
	finalize := func(at time.Time) {
		t.Helper()
		if _, err := worker(at).CloseWeeks(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, err := worker(at).FinalizeChallenges(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	// Over: bob, under the minimum of week 2, is eliminated, the results frozen.
	finalize(locked(2))
	now := locked(2).Add(4 * 24 * time.Hour)
	clk := clock.NewFake(now)
	challenges := challenge.NewService(challenge.NewRepository(gdb), nil, sport.NewService(sport.NewRepository(gdb)), nil, clk)
	invitations := invitation.NewService(nil, invitation.NewRepository(gdb), clk)
	newCorrections := func(store correctionStore) *ChallengeCorrections {
		return NewChallengeCorrections(NewCorrections(store, NewJournal(r, clk), worker(now), noBoards{}), challenges, invitations)
	}
	corrections := newCorrections(r)

	read := func() settingsState {
		t.Helper()
		var s settingsState
		if err := pool.QueryRow(t.Context(), `SELECT ends_at,
			(SELECT count(*) FROM result_snapshot WHERE challenge_id = $1),
			(SELECT count(*) FROM week_closure WHERE challenge_id = $1),
			(SELECT count(*) FROM admin_actions),
			ARRAY(SELECT expires_at FROM invitations WHERE challenge_id = $1 ORDER BY token_hash),
			ARRAY(SELECT type FROM categories WHERE challenge_id = $1 ORDER BY position)
			FROM challenges WHERE id = $1`, id).Scan(&s.EndsAt, &s.Snapshots, &s.Closures, &s.Actions, &s.Expiries, &s.Types); err != nil {
			t.Fatal(err)
		}
		state, err := r.State(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		s.Eliminations = state.Eliminations
		s.EndsAt = s.EndsAt.UTC()
		for i := range s.Expiries {
			s.Expiries[i] = s.Expiries[i].UTC()
		}
		return s
	}
	actor := auth.Actor{ID: ann, Admin: true}
	bobOut := []EliminationRow{{User: UserRef{ID: bob}, WeekNumber: 2, DistanceMeters: 6000, RequiredMeters: 7000}}
	before := read()
	if before.Snapshots != 3 || before.Closures != 2 || !reflect.DeepEqual(before.Eliminations, bobOut) {
		t.Fatalf("state before %+v, want the results frozen and bob eliminated in week 2", before)
	}
	four := 4

	t.Run("a preview changes nothing", func(t *testing.T) {
		previews, err := corrections.UpdateSettings(t.Context(), actor, id, challenge.SettingsChanges{Weeks: &four}, "Durée mal saisie", true)
		if err != nil {
			t.Fatal(err)
		}
		if len(previews) != 1 || previews[0].ChallengeID != id || len(previews[0].Eliminations) != 0 {
			t.Fatalf("previews %+v, want the challenge, bob still eliminated", previews)
		}
		if got := read(); !reflect.DeepEqual(got, before) {
			t.Fatalf("state after a preview %+v, want %+v", got, before)
		}
	})

	t.Run("a preview tells the reference sport, the status and the results", func(t *testing.T) {
		ride := "ride"
		previews, err := corrections.UpdateSettings(t.Context(), actor, id, challenge.SettingsChanges{Weeks: &four, ReferenceSport: &ride}, "Mauvais sport", true)
		if err != nil {
			t.Fatal(err)
		}
		// Two more weeks: in progress again, its results reopened.
		p := previews[0]
		if p.ReferenceSport != (SportChange{Before: "run", After: "ride"}) || p.Status != StatusOngoing || p.Results != ResultsReopened || p.Title == "" {
			t.Fatalf("preview %+v, want run then ride, ongoing, the results reopened", p)
		}
		if got := read(); !reflect.DeepEqual(got, before) {
			t.Fatalf("state after a preview %+v, want %+v", got, before)
		}
	})

	t.Run("a lock waited for too long answers busy, nothing written", func(t *testing.T) {
		// An activity write in progress holds the week lock shared.
		writer := gdb.Begin()
		t.Cleanup(func() { writer.Rollback() })
		if err := db.ShareWeekLock(writer); err != nil {
			t.Fatal(err)
		}
		_, err := newCorrections(briefLocks{r}).UpdateSettings(t.Context(), actor, id, challenge.SettingsChanges{Weeks: &four}, "Durée mal saisie", false)
		var appErr *apperr.Error
		if !errors.As(err, &appErr) || appErr.Code != apperr.CodeBusy || appErr.Status != http.StatusServiceUnavailable {
			t.Fatalf("UpdateSettings() = %v, want 503 busy", err)
		}
		writer.Rollback()
		if got := read(); !reflect.DeepEqual(got, before) {
			t.Fatalf("state after a lock timeout %+v, want %+v", got, before)
		}
	})

	t.Run("a second lock waited for too long answers busy, nothing written", func(t *testing.T) {
		// The week lock is free; a write in progress holds the challenge.
		writer := gdb.Begin()
		t.Cleanup(func() { writer.Rollback() })
		if err := writer.Exec(`SELECT 1 FROM challenges WHERE id = ? FOR UPDATE`, id).Error; err != nil {
			t.Fatal(err)
		}
		_, err := newCorrections(briefLocks{r}).UpdateSettings(t.Context(), actor, id, challenge.SettingsChanges{Weeks: &four}, "Durée mal saisie", false)
		var appErr *apperr.Error
		if !errors.As(err, &appErr) || appErr.Code != apperr.CodeBusy || appErr.Status != http.StatusServiceUnavailable {
			t.Fatalf("UpdateSettings() = %v, want 503 busy", err)
		}
		writer.Rollback()
		if got := read(); !reflect.DeepEqual(got, before) {
			t.Fatalf("state after a lock timeout %+v, want %+v", got, before)
		}
	})

	t.Run("the deadline of the request met while waiting answers busy, nothing written", func(t *testing.T) {
		writer := gdb.Begin()
		t.Cleanup(func() { writer.Rollback() })
		if err := db.ShareWeekLock(writer); err != nil {
			t.Fatal(err)
		}
		// The deadline falls long before the 10 s a lock is waited for.
		ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
		defer cancel()
		_, err := newCorrections(r).UpdateSettings(ctx, actor, id, challenge.SettingsChanges{Weeks: &four}, "Durée mal saisie", false)
		var appErr *apperr.Error
		if !errors.As(err, &appErr) || appErr.Code != apperr.CodeBusy || appErr.Status != http.StatusServiceUnavailable {
			t.Fatalf("UpdateSettings() = %v, want 503 busy", err)
		}
		writer.Rollback()
		if got := read(); !reflect.DeepEqual(got, before) {
			t.Fatalf("state after a deadline %+v, want %+v", got, before)
		}
	})

	t.Run("made longer, in progress again until its new end", func(t *testing.T) {
		if _, err := corrections.UpdateSettings(t.Context(), actor, id, challenge.SettingsChanges{Weeks: &four}, "Durée mal saisie", false); err != nil {
			t.Fatal(err)
		}
		got := read()
		newEnd := start.AddDate(0, 0, 28).UTC()
		// Weeks 1 and 2 are locked at now and judged again; week 2 is no
		// longer the last one: no results frozen.
		if !got.EndsAt.Equal(newEnd) || got.Snapshots != 0 || got.Closures != 2 || got.Actions != 1 || !reflect.DeepEqual(got.Eliminations, bobOut) {
			t.Fatalf("state %+v, want it to end at %v, nothing frozen, weeks 1 and 2 judged", got, newEnd)
		}
		if !reflect.DeepEqual(got.Expiries, before.Expiries) {
			t.Fatalf("expiries %v, want them as they were: week 1 did not move", got.Expiries)
		}
		actions, err := r.ListActions(t.Context(), ActionQuery{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		// challenge_ids names the challenge (journal filter, admin detail).
		if a := actions[0]; a.Action != ActionChallengeUpdate || a.TargetID != id || !reflect.DeepEqual(a.ChallengeIDs, []string{id}) || string(a.OldValue) != `{"weeks": 2}` || string(a.NewValue) != `{"weeks": 4}` {
			t.Fatalf("journal line %+v %s %s, want the weeks", a, a.OldValue, a.NewValue)
		}
		// The worker, at the old end or later, freezes nothing before the
		// last week locks.
		finalize(locked(3))
		if got := read(); got.Snapshots != 0 || got.Closures != 3 {
			t.Fatalf("state at week 3 %+v, want week 3 judged and nothing frozen", got)
		}
		finalize(locked(4))
		if got := read(); got.Snapshots != 3 || got.Closures != 4 {
			t.Fatalf("state at the new end %+v, want the results frozen with week 4", got)
		}
	})

	t.Run("a first day moved moves the links not revoked; a category removed goes", func(t *testing.T) {
		later := "2026-03-09"
		cats := &challenge.CategoryChoice{Distance: true}
		if _, err := corrections.UpdateSettings(t.Context(), actor, id, challenge.SettingsChanges{StartDate: &later, Categories: cats}, "Mauvais premier jour", false); err != nil {
			t.Fatal(err)
		}
		got := read()
		newDeadline := time.Date(2026, 3, 16, 0, 0, 0, 0, loc).UTC()
		if want := []time.Time{newDeadline, weekOneEnd}; !reflect.DeepEqual(got.Expiries, want) {
			t.Fatalf("expiries %v, want %v (the revoked link as it was)", got.Expiries, want)
		}
		if want := []string{challenge.CategoryCumulativeTime, challenge.CategoryCumulativeDistance}; !reflect.DeepEqual(got.Types, want) {
			t.Fatalf("categories %v, want %v", got.Types, want)
		}
		// No elimination category left: nothing judged week by week, and
		// from 2026-03-09 for 4 weeks, nothing frozen.
		if len(got.Eliminations) != 0 || got.Snapshots != 0 || got.Closures != 0 {
			t.Fatalf("state %+v, want no elimination, no closure, nothing frozen", got)
		}
	})
}
