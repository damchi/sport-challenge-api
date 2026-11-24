//go:build integration

package admin

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
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
)

// lifecycleState is what a deletion or a restore may touch.
type lifecycleState struct {
	Deleted      bool
	Closures     int
	Actions      int
	Eliminations []EliminationRow
}

// TestChallengeCorrectionsDeleteAndRestoreFromTheDatabase: a challenge
// deleted by an admin is skipped by the worker while two of its weeks lock;
// restored, those two weeks are judged by the recompute, and its links not
// expired are valid again, an expired one staying invalid. A preview of the
// restore changes nothing; a challenge deleted already, or not deleted,
// answers challenge_not_found.
func TestChallengeCorrectionsDeleteAndRestoreFromTheDatabase(t *testing.T) {
	r, pool := newRepository(t)
	gdb := r.db
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	// Four weeks from Monday 2026-03-02; week n locks at 7 am on the first
	// day of week n+1.
	start := time.Date(2026, 3, 2, 0, 0, 0, 0, loc)
	locked := func(n int) time.Time { return time.Date(2026, 3, 2+7*n, 7, 0, 0, 0, loc).UTC() }
	ann := insertUser(t, pool, "email|ann", start.AddDate(0, -1, 0), false)
	bob := insertUser(t, pool, "email|bob", start.AddDate(0, -1, 0), false)
	cara := insertUser(t, pool, "email|cara", start.AddDate(0, -1, 0), false)
	// Dan, under the minimum of week 2 too, deletes their account while the
	// challenge is deleted.
	dan := insertUser(t, pool, "email|dan", start.AddDate(0, -1, 0), false)
	id := insertChallengeRow(t, pool, ann, challengeRow{title: "Spring", visibility: "private", startsAt: start.UTC(), weeks: 4, createdAt: start.AddDate(0, 0, -7).UTC()})
	// Four weeks in local days: the time changes on 2026-03-08.
	exec(t, pool, `UPDATE challenges SET ends_at = $2 WHERE id = $1`, id, start.AddDate(0, 0, 28).UTC())
	exec(t, pool, `INSERT INTO categories (challenge_id, type, position, params) VALUES ($1, 'cumulative_time', 1, '{}'),
		($1, 'elimination', 2, '{"sport": "run", "step_meters": 2000}')`, id)
	for _, user := range []string{bob, dan} {
		exec(t, pool, `INSERT INTO participants (challenge_id, user_id, joined_at) VALUES ($1, $2, $3)`, id, user, start)
	}
	for _, a := range []struct {
		user         string
		week, meters int
	}{{ann, 1, 5000}, {bob, 1, 5000}, {dan, 1, 5000}, {ann, 2, 7000}, {bob, 2, 6000}, {dan, 2, 6000}, {ann, 3, 9000}} {
		exec(t, pool, `INSERT INTO activities (user_id, sport_code, started_at, duration_seconds, distance_meters) VALUES ($1, 'run', $2, 1800, $3)`,
			a.user, start.AddDate(0, 0, 7*(a.week-1)).Add(10*time.Hour), a.meters)
	}
	deleteAt := locked(1).Add(24 * time.Hour)
	restoreAt := locked(3).Add(24 * time.Hour)
	// A link valid until after the restore (token 1), one expired between
	// the deletion and the restore (token 2).
	tokens := make([]string, 2)
	for i, expiresAt := range []time.Time{restoreAt.Add(48 * time.Hour), locked(2)} {
		raw := make([]byte, 32)
		raw[0] = byte(i + 1)
		hash := sha256.Sum256(raw)
		tokens[i] = base64.RawURLEncoding.EncodeToString(raw)
		exec(t, pool, `INSERT INTO invitations (challenge_id, invited_by, token_hash, expires_at, created_at) VALUES ($1, $2, $3, $4, $5)`,
			id, ann, hash[:], expiresAt, start.UTC())
	}
	link := func(at time.Time, token string) (string, error) {
		t.Helper()
		return invitation.NewService(nil, invitation.NewRepository(gdb), clock.NewFake(at)).Link(t.Context(), cara, token)
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	worker := func(at time.Time) *closure.Service {
		return closure.NewService(closure.NewRepository(gdb), leaderboard.NewRepository(gdb), clock.NewFake(at), logger)
	}
	closeWeeks := func(at time.Time) {
		t.Helper()
		if _, err := worker(at).CloseWeeks(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	correctionsAt := func(at time.Time) *ChallengeCorrections {
		clk := clock.NewFake(at)
		challenges := challenge.NewService(challenge.NewRepository(gdb), nil, nil, nil, clk)
		return NewChallengeCorrections(NewCorrections(r, NewJournal(r, clk), worker(at), noBoards{}), challenges, nil)
	}
	read := func() lifecycleState {
		t.Helper()
		var s lifecycleState
		if err := pool.QueryRow(t.Context(), `SELECT deleted_at IS NOT NULL,
			(SELECT count(*) FROM week_closure WHERE challenge_id = $1),
			(SELECT count(*) FROM admin_actions)
			FROM challenges WHERE id = $1`, id).Scan(&s.Deleted, &s.Closures, &s.Actions); err != nil {
			t.Fatal(err)
		}
		if s.Eliminations, err = r.Eliminations(t.Context(), id); err != nil {
			t.Fatal(err)
		}
		return s
	}
	actor := auth.Actor{ID: ann, Admin: true}
	isNotFound := func(err error) bool {
		var appErr *apperr.Error
		return errors.As(err, &appErr) && appErr.Code == apperr.CodeChallengeNotFound
	}
	isInvalid := func(err error) bool {
		var appErr *apperr.Error
		return errors.As(err, &appErr) && appErr.Code == apperr.CodeInvitationInvalid
	}

	// Week 1 is judged, then the challenge is deleted in week 2, after a
	// preview that changes nothing.
	closeWeeks(locked(1))
	live := read()
	if _, err := correctionsAt(deleteAt).Delete(t.Context(), actor, id, "Supprimé par erreur", true); err != nil {
		t.Fatal(err)
	}
	if got := read(); !reflect.DeepEqual(got, live) || got.Deleted {
		t.Fatalf("state after a preview of the deletion %+v, want %+v", got, live)
	}
	previews, err := correctionsAt(deleteAt).Delete(t.Context(), actor, id, "Supprimé par erreur", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(previews) != 1 || previews[0].Before == nil || previews[0].After != nil {
		t.Fatalf("previews %+v, want the rankings before the deletion only", previews)
	}
	if got := read(); !got.Deleted || got.Closures != 1 || got.Actions != 1 {
		t.Fatalf("state after the deletion %+v, want it deleted, week 1 judged, one journal line", got)
	}
	if _, err := link(deleteAt, tokens[0]); !isInvalid(err) {
		t.Fatalf("link to a deleted challenge: %v, want invitation_invalid", err)
	}
	if _, err := correctionsAt(deleteAt).Delete(t.Context(), actor, id, "Supprimé par erreur", false); !isNotFound(err) {
		t.Fatalf("delete a deleted challenge: %v, want challenge_not_found", err)
	}

	// Weeks 2 and 3 lock while it is deleted: the worker skips them. Dan's
	// account is deleted meanwhile (anonymized, as by user.Service).
	exec(t, pool, `UPDATE users SET anonymized_at = $2, pseudo = NULL, first_name = NULL, last_name = NULL, email = NULL WHERE id = $1`, dan, locked(2).Add(time.Hour))
	closeWeeks(locked(2))
	closeWeeks(locked(3))
	deleted := read()
	if deleted.Closures != 1 || len(deleted.Eliminations) != 0 {
		t.Fatalf("state while deleted %+v, want week 1 judged only", deleted)
	}

	t.Run("a preview of the restore changes nothing", func(t *testing.T) {
		previews, err := correctionsAt(restoreAt).Restore(t.Context(), actor, id, "Supprimé par erreur", true)
		if err != nil {
			t.Fatal(err)
		}
		if len(previews) != 1 || previews[0].Before != nil || previews[0].After == nil || len(previews[0].Eliminations) != 1 {
			t.Fatalf("previews %+v, want the rankings after the restore and bob eliminated", previews)
		}
		if got := read(); !reflect.DeepEqual(got, deleted) {
			t.Fatalf("state after a preview %+v, want %+v", got, deleted)
		}
	})

	t.Run("restored, the weeks locked meanwhile are judged, without the account deleted meanwhile, and its links valid again", func(t *testing.T) {
		if _, err := correctionsAt(restoreAt).Restore(t.Context(), actor, id, "Supprimé par erreur", false); err != nil {
			t.Fatal(err)
		}
		got := read()
		bobOut := []EliminationRow{{User: UserRef{ID: bob}, WeekNumber: 2, DistanceMeters: 6000, RequiredMeters: 7000}}
		if got.Deleted || got.Closures != 3 || got.Actions != 2 || !reflect.DeepEqual(got.Eliminations, bobOut) {
			t.Fatalf("state after the restore %+v, want it restored, weeks 1 to 3 judged, bob eliminated in week 2", got)
		}
		actions, err := r.ListActions(t.Context(), ActionQuery{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		wantOld := `{"deleted_at": "` + deleteAt.Format(time.RFC3339Nano) + `"}`
		if a := actions[0]; a.Action != ActionChallengeRestore || a.TargetType != TargetChallenge || a.TargetID != id ||
			!reflect.DeepEqual(a.ChallengeIDs, []string{id}) || string(a.OldValue) != wantOld || string(a.NewValue) != `{"deleted_at": null}` {
			t.Fatalf("journal line %+v %s %s, want the restore of the deletion of %s", a, a.OldValue, a.NewValue, wantOld)
		}
		if a := actions[1]; a.Action != ActionChallengeDelete || string(a.NewValue) != wantOld || !reflect.DeepEqual(a.ChallengeIDs, []string{id}) {
			t.Fatalf("journal line %+v %s, want the deletion", a, a.NewValue)
		}
		if challengeID, err := link(restoreAt, tokens[0]); err != nil || challengeID != id {
			t.Fatalf("link not expired: %s, %v, want valid again", challengeID, err)
		}
		if _, err := link(restoreAt, tokens[1]); !isInvalid(err) {
			t.Fatalf("link expired meanwhile: %v, want invitation_invalid", err)
		}
	})

	t.Run("a challenge that is not deleted is not found", func(t *testing.T) {
		if _, err := correctionsAt(restoreAt).Restore(t.Context(), actor, id, "Supprimé par erreur", false); !isNotFound(err) {
			t.Fatalf("restore a challenge not deleted: %v, want challenge_not_found", err)
		}
		if got := read(); got.Actions != 2 {
			t.Fatalf("journal %d lines, want nothing written", got.Actions)
		}
	})
}
