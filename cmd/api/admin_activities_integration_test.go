//go:build integration

package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/activity"
	"github.com/damchi/sport-challenge-api/internal/admin"
	"github.com/damchi/sport-challenge-api/internal/challenge"
	"github.com/damchi/sport-challenge-api/internal/closure"
	"github.com/damchi/sport-challenge-api/internal/leaderboard"
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/platform/db/testdb"
	"github.com/damchi/sport-challenge-api/internal/sport"
	"github.com/damchi/sport-challenge-api/internal/user"
)

// TestAdminActivityCorrections: an admin changes, in weeks locked and in
// finished challenges, the activities a user can no longer change; every
// challenge where the activity counts, before or after the change, is
// judged again and named in the journal line; a preview changes nothing.
func TestAdminActivityCorrections(t *testing.T) {
	pool := testdb.New(t)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	gdb, err := db.NewGorm(pool, logger)
	if err != nil {
		t.Fatal(err)
	}
	// Every challenge below is over, its weeks judged and its results
	// frozen at now.
	now := toronto(t, 2026, 5, 20, 12)
	clk := clock.NewFake(now)
	var challenges *challenge.Service
	users := user.NewService(user.NewRepository(gdb), lockForDeletionFunc(func(ctx context.Context, userID string) error {
		return challenges.LockForDeletion(ctx, userID)
	}), nil, deletedAtAuth0{}, clk, logger)
	sports := sport.NewService(sport.NewRepository(gdb))
	challenges = challenge.NewService(challenge.NewRepository(gdb), challengeProfiles{users}, sports, challengeStandings{}, clk)
	activities := activity.NewService(activity.NewRepository(gdb), activityProfiles{users}, activityCatalogue{sports}, participations{challenges}, activityScopes{challenges}, clk)
	leaderboards := leaderboard.NewService(leaderboardChallenges{challenges}, leaderboardProfiles{users}, leaderboard.NewRepository(gdb), clk, 24*time.Hour)
	worker := closure.NewService(closure.NewRepository(gdb), leaderboard.NewRepository(gdb), clk, logger)
	adminRepo := admin.NewRepository(gdb)
	corrections := admin.NewActivityCorrections(admin.NewCorrections(adminRepo, admin.NewJournal(adminRepo, clk), worker, leaderboards), activities)

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	insertUser := func(subject string) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(t.Context(), "INSERT INTO users (auth_subject, first_name, last_name, pseudo) VALUES ($1, 'Test', 'User', $1) RETURNING id", subject).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	ann, bob, dan := insertUser("ann"), insertUser("bob"), insertUser("dan")
	// insertChallenge creates a challenge of weeks weeks from start, its
	// categories those of categories (SQL values of $1), ann and bob its
	// participants.
	insertChallenge := func(title string, start time.Time, weeks int, categories string) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(t.Context(), `INSERT INTO challenges (title, visibility, timezone, starts_at, ends_at, created_by)
			VALUES ($1, 'private', 'America/Toronto', $2, $3, $4) RETURNING id`, title, start, start.AddDate(0, 0, 7*weeks), ann).Scan(&id); err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO categories (challenge_id, type, position, params) VALUES `+categories, id)
		exec(`INSERT INTO challenge_creators (challenge_id, user_id) VALUES ($1, $2)`, id, ann)
		for _, u := range []string{ann, bob} {
			exec(`INSERT INTO participants (challenge_id, user_id, joined_at) VALUES ($1, $2, $3)`, id, u, start)
		}
		return id
	}
	// spring and april share their weeks; may comes after them.
	spring := insertChallenge("Spring", toronto(t, 2026, 4, 6, 0), 2, `($1, 'cumulative_time', 1, '{}'),
		($1, 'cumulative_distance', 2, '{"factors": {"run": 1}}'), ($1, 'elimination', 3, '{"sport": "run", "step_meters": 2000}')`)
	april := insertChallenge("April", toronto(t, 2026, 4, 6, 0), 3, `($1, 'cumulative_time', 1, '{}'),
		($1, 'points', 2, '{"criteria": ["longest_duration"], "podium": 5, "min_session_seconds": 1200}')`)
	may := insertChallenge("May", toronto(t, 2026, 5, 4, 0), 2, `($1, 'cumulative_time', 1, '{}')`)

	insertRun := func(userID string, at time.Time, meters int) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(t.Context(), `INSERT INTO activities (user_id, sport_code, started_at, duration_seconds, distance_meters)
			VALUES ($1, 'run', $2, 1800, $3) RETURNING id`, userID, at, meters).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	// Week 2 of spring needs 7 km: ann, at 6 km, is eliminated in it.
	insertRun(ann, toronto(t, 2026, 4, 7, 10), 5000)
	annWeek2 := insertRun(ann, toronto(t, 2026, 4, 14, 10), 6000)
	bobWeek1 := insertRun(bob, toronto(t, 2026, 4, 7, 10), 5000)
	insertRun(bob, toronto(t, 2026, 4, 14, 10), 7000)
	if _, err := worker.CloseWeeks(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.FinalizeChallenges(t.Context()); err != nil {
		t.Fatal(err)
	}

	adminActor := auth.Actor{ID: dan, Admin: true}
	eliminated := func(challengeID string) []string {
		t.Helper()
		rows, err := pool.Query(t.Context(), `SELECT user_id FROM elimination WHERE challenge_id = $1 ORDER BY user_id`, challengeID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			out = append(out, id)
		}
		return out
	}
	// journal reads the journal lines; those of the tests share the instant
	// of the fake clock, each one found by its reason.
	journal := func() []admin.Action {
		t.Helper()
		list, err := adminRepo.ListActions(t.Context(), admin.ActionQuery{Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		return list
	}
	line := func(reason string) admin.Action {
		t.Helper()
		var found []admin.Action
		for _, a := range journal() {
			if a.Reason == reason {
				slices.Sort(a.ChallengeIDs)
				found = append(found, a)
			}
		}
		if len(found) != 1 {
			t.Fatalf("%d journal lines %q, want one", len(found), reason)
		}
		return found[0]
	}
	sorted := func(ids ...string) []string { return slices.Sorted(slices.Values(ids)) }
	previewed := func(previews []admin.ChallengePreview) []string {
		ids := make([]string, len(previews))
		for i, p := range previews {
			ids[i] = p.ChallengeID
		}
		return sorted(ids...)
	}
	run := func(at time.Time, meters int) activity.Draft {
		return activity.Draft{SportCode: "run", StartedAt: at, DurationSeconds: 1800, DistanceMeters: &meters}
	}
	if got := eliminated(spring); !slices.Equal(got, []string{ann}) {
		t.Fatalf("eliminated in spring %v, want ann", got)
	}

	t.Run("its author can no longer change it", func(t *testing.T) {
		_, err := activities.Update(t.Context(), ann, annWeek2, run(toronto(t, 2026, 4, 14, 10), 7500))
		if !errors.Is(err, apperr.New(apperr.CodeWeekLocked, nil)) {
			t.Fatalf("update by its author: %v, want week_locked", err)
		}
	})

	t.Run("a preview changes nothing", func(t *testing.T) {
		before := len(journal())
		previews, err := corrections.Delete(t.Context(), adminActor, annWeek2, "Doublon", true)
		if err != nil {
			t.Fatal(err)
		}
		if got := previewed(previews); !slices.Equal(got, sorted(spring, april)) {
			t.Fatalf("previewed %v, want spring and april", got)
		}
		if got := eliminated(spring); !slices.Equal(got, []string{ann}) || len(journal()) != before {
			t.Fatalf("after a preview: eliminated %v, a journal line written", got)
		}
		// Both over and frozen: frozen again. In spring, ann is still out
		// in week 2, with 0 km instead of 6; in april, she loses the points
		// of week 2, where bob alone has a session.
		annRef := admin.UserRef{ID: ann, Pseudo: new("ann"), FirstName: new("Test"), LastName: new("User")}
		for _, p := range previews {
			if p.Status != admin.StatusFinished || p.Results != admin.ResultsRefrozen || p.ReferenceSport != (admin.SportChange{Before: "run", After: "run"}) {
				t.Fatalf("preview of %s: status %q, results %q, reference %+v; want finished, refrozen, run", p.Title, p.Status, p.Results, p.ReferenceSport)
			}
			wantWeeks := []admin.WeekChange{{Number: 2, Locked: true}}
			var wantPoints []admin.PointsChange
			if p.ChallengeID == april {
				wantPoints = []admin.PointsChange{{User: annRef, Week: 2, Before: 10, After: 0}}
			}
			if !reflect.DeepEqual(p.Weeks, wantWeeks) || !reflect.DeepEqual(p.Points, wantPoints) {
				t.Fatalf("preview of %s: weeks %+v, points %+v; want %+v, %+v", p.Title, p.Weeks, p.Points, wantWeeks, wantPoints)
			}
		}
	})

	t.Run("counting in two challenges: both judged again and marked", func(t *testing.T) {
		previews, err := corrections.Update(t.Context(), adminActor, annWeek2, run(toronto(t, 2026, 4, 14, 10), 7500), "Montre mal réglée", false)
		if err != nil {
			t.Fatal(err)
		}
		if got := previewed(previews); !slices.Equal(got, sorted(spring, april)) {
			t.Fatalf("previewed %v, want spring and april", got)
		}
		if got := eliminated(spring); len(got) != 0 {
			t.Fatalf("eliminated in spring %v, want nobody: ann ran 7.5 km in week 2", got)
		}
		a := line("Montre mal réglée")
		if a.Action != admin.ActionActivityUpdate || a.TargetType != admin.TargetActivity || a.TargetID != annWeek2 ||
			!slices.Equal(a.ChallengeIDs, sorted(spring, april)) || string(a.OldValue) != `{"distance_meters": 6000}` || string(a.NewValue) != `{"distance_meters": 7500}` {
			t.Fatalf("journal line %+v %s %s", a, a.OldValue, a.NewValue)
		}
	})

	t.Run("moved from a challenge to another: all judged again", func(t *testing.T) {
		previews, err := corrections.Update(t.Context(), adminActor, annWeek2, run(toronto(t, 2026, 5, 6, 10), 7500), "Mauvaise date", false)
		if err != nil {
			t.Fatal(err)
		}
		if got := previewed(previews); !slices.Equal(got, sorted(spring, april, may)) {
			t.Fatalf("previewed %v, want spring, april and may", got)
		}
		// Nothing left in week 2 of spring for ann.
		if got := eliminated(spring); !slices.Equal(got, []string{ann}) {
			t.Fatalf("eliminated in spring %v, want ann", got)
		}
		if a := line("Mauvaise date"); !slices.Equal(a.ChallengeIDs, sorted(spring, april, may)) {
			t.Fatalf("journal line touches %v, want the three challenges", a.ChallengeIDs)
		}
	})

	t.Run("recorded and deleted in a finished challenge", func(t *testing.T) {
		previews, err := corrections.Record(t.Context(), adminActor, ann, run(toronto(t, 2026, 4, 15, 10), 8000), "Séance oubliée", false)
		if err != nil {
			t.Fatal(err)
		}
		if got := previewed(previews); !slices.Equal(got, sorted(spring, april)) || len(eliminated(spring)) != 0 {
			t.Fatalf("previewed %v, eliminated %v; want spring and april, nobody out", got, eliminated(spring))
		}
		a := line("Séance oubliée")
		var source string
		if err := pool.QueryRow(t.Context(), `SELECT source FROM activities WHERE id = $1`, a.TargetID).Scan(&source); err != nil {
			t.Fatal(err)
		}
		if a.Action != admin.ActionActivityCreate || a.OldValue != nil || source != activity.SourceManual {
			t.Fatalf("journal line %+v, source %q; want a creation, manual", a, source)
		}
		if _, err := corrections.Delete(t.Context(), adminActor, a.TargetID, "Séance en double", false); err != nil {
			t.Fatal(err)
		}
		if got := eliminated(spring); !slices.Equal(got, []string{ann}) {
			t.Fatalf("eliminated in spring %v, want ann again", got)
		}
		_, err = corrections.Delete(t.Context(), adminActor, a.TargetID, "Séance en double", false)
		if !errors.Is(err, apperr.New(apperr.CodeActivityNotFound, nil)) {
			t.Fatalf("deleted twice: %v, want activity_not_found", err)
		}
	})

	t.Run("validation and overlap unchanged", func(t *testing.T) {
		before := len(journal())
		_, err := corrections.Record(t.Context(), adminActor, bob, run(toronto(t, 2026, 4, 14, 10), 4000), "Séance de bob", false)
		if !errors.Is(err, apperr.New(apperr.CodeActivityOverlap, nil)) {
			t.Fatalf("overlapping: %v, want activity_overlap", err)
		}
		_, err = corrections.Record(t.Context(), adminActor, bob, run(now.Add(time.Hour), 4000), "Séance de bob", false)
		if !errors.Is(err, apperr.New(apperr.CodeValidationFailed, nil)) {
			t.Fatalf("in the future: %v, want validation_failed", err)
		}
		if len(journal()) != before {
			t.Fatal("a refused change wrote a journal line")
		}
	})

	t.Run("left once its week was locked: still judged again", func(t *testing.T) {
		// bob leaves spring after week 1 locked: it still judges him there.
		exec(`UPDATE participants SET status = 'abandoned', abandoned_at = $3 WHERE challenge_id = $1 AND user_id = $2`,
			spring, bob, toronto(t, 2026, 4, 16, 12))
		previews, err := corrections.Update(t.Context(), adminActor, bobWeek1, run(toronto(t, 2026, 4, 7, 10), 5500), "Distance corrigée", false)
		if err != nil {
			t.Fatal(err)
		}
		if got := previewed(previews); !slices.Equal(got, sorted(spring, april)) {
			t.Fatalf("previewed %v, want spring (left after week 1 locked) and april", got)
		}
		if a := line("Distance corrigée"); !slices.Equal(a.ChallengeIDs, sorted(spring, april)) {
			t.Fatalf("journal line touches %v, want spring and april", a.ChallengeIDs)
		}
	})
}
