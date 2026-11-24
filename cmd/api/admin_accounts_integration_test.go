//go:build integration

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/activity"
	"github.com/damchi/sport-challenge-api/internal/admin"
	"github.com/damchi/sport-challenge-api/internal/challenge"
	"github.com/damchi/sport-challenge-api/internal/closure"
	"github.com/damchi/sport-challenge-api/internal/invitation"
	"github.com/damchi/sport-challenge-api/internal/leaderboard"
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/platform/db/testdb"
	"github.com/damchi/sport-challenge-api/internal/sport"
	"github.com/damchi/sport-challenge-api/internal/user"
)

// TestAdminAccountCorrections wires the modules as run does: an admin
// changes the names of an account, blocks it (its next login answers
// account_blocked), unblocks it, then deletes it, each change with its
// journal line. The deletion is refused while the account is the last
// creator of a challenge not finished, nothing written. Once deleted, by an
// admin or by DELETE /me, the profile values of the journal lines that
// target the account, as an account or as a participant, are erased; the
// action, the instant, the target and the reason stay, and the lines of the
// other accounts are left as they are.
func TestAdminAccountCorrections(t *testing.T) {
	pool := testdb.New(t)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	gdb, err := db.NewGorm(pool, logger)
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(recordNow)
	var (
		challenges  *challenge.Service
		activities  *activity.Service
		invitations *invitation.Service
	)
	adminRepo := admin.NewRepository(gdb)
	journal := admin.NewJournal(adminRepo, clk)
	erasers := []user.Eraser{
		journal,
		eraseUserFunc(func(ctx context.Context, userID string) error { return invitations.EraseUser(ctx, userID) }),
		eraseUserFunc(func(ctx context.Context, userID string) error { return activities.EraseUser(ctx, userID) }),
		eraseUserFunc(func(ctx context.Context, userID string) error { return challenges.EraseUser(ctx, userID) }),
	}
	users := user.NewService(user.NewRepository(gdb), abandonAllFunc(func(ctx context.Context, userID string) error {
		return challenges.AbandonAll(ctx, userID)
	}), erasers, deletedAtAuth0{}, clk, logger)
	sports := sport.NewService(sport.NewRepository(gdb))
	challenges = challenge.NewService(challenge.NewRepository(gdb), challengeProfiles{users}, sports, challengeStandings{}, clk)
	activities = activity.NewService(activity.NewRepository(gdb), activityProfiles{users}, activityCatalogue{sports}, participations{challenges}, activityScopes{challenges}, clk)
	invitations = invitation.NewService(invitationChallenges{challenges}, invitation.NewRepository(gdb), clk)
	leaderboards := leaderboard.NewService(leaderboardChallenges{challenges}, leaderboardProfiles{users}, leaderboard.NewRepository(gdb), clk, 24*time.Hour)
	recompute := closure.NewService(closure.NewRepository(gdb), leaderboard.NewRepository(gdb), clk, logger)
	accounts := admin.NewAccountCorrections(admin.NewCorrections(adminRepo, journal, recompute, leaderboards), users)

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	insertUser := func(subject string) string {
		var id string
		if err := pool.QueryRow(t.Context(), `INSERT INTO users (auth_subject, pseudo, first_name, last_name, email)
			VALUES ($1, $1, 'Test', 'User', $1 || '@example.com') RETURNING id`, subject).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	eve, bob, carl, dana := insertUser("eve"), insertUser("bob"), insertUser("carl"), insertUser("dana")
	adminActor := auth.Actor{ID: eve, Admin: true}
	// Bob is the only creator of a challenge in progress, in which he
	// participates.
	start := recordNow.AddDate(0, 0, -3)
	var solo string
	if err := pool.QueryRow(t.Context(), `INSERT INTO challenges (title, visibility, timezone, starts_at, ends_at, created_by)
		VALUES ('Solo', 'public', 'America/Toronto', $1, $2, $3) RETURNING id`, start, start.AddDate(0, 0, 14), bob).Scan(&solo); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO categories (challenge_id, type, position, params) VALUES ($1, 'cumulative_time', 1, '{}')`, solo)
	exec(`INSERT INTO challenge_creators (challenge_id, user_id) VALUES ($1, $2)`, solo, bob)
	exec(`INSERT INTO participants (challenge_id, user_id, joined_at) VALUES ($1, $2, $3)`, solo, bob, start)
	// Lines of the journal written before: bob as a participant (a profile
	// value among the others), and carl's and dana's names.
	insertAction := func(target, targetType, oldValue, newValue string) {
		exec(`INSERT INTO admin_actions (admin_id, created_at, action, target_type, target_id, old_value, new_value, reason)
			VALUES ($1, $2, 'test.action', $3, $4, $5::jsonb, $6::jsonb, 'Earlier change')`, eve, recordNow.Add(-time.Hour), targetType, target, oldValue, newValue)
	}
	insertAction(bob, admin.TargetParticipant, `{"status": "abandoned", "email": "bob@example.com"}`, `{"status": "active"}`)
	insertAction(carl, admin.TargetUser, `{"first_name": "Carl", "pseudo": "carl"}`, `{"first_name": "Carlos", "pseudo": null}`)
	insertAction(dana, admin.TargetUser, `{"last_name": "Old"}`, `{"last_name": "New"}`)

	// lines reads the values of the journal lines that target id, oldest
	// first, with their action and reason.
	type line struct {
		Action, Reason   string
		OldValue, Values map[string]any
	}
	lines := func(id string) []line {
		t.Helper()
		rows, err := pool.Query(t.Context(), `SELECT action, reason, old_value, new_value FROM admin_actions
			WHERE target_id = $1 ORDER BY created_at, action`, id)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []line
		for rows.Next() {
			var l line
			var oldValue, newValue []byte
			if err := rows.Scan(&l.Action, &l.Reason, &oldValue, &newValue); err != nil {
				t.Fatal(err)
			}
			if oldValue != nil {
				_ = json.Unmarshal(oldValue, &l.OldValue)
			}
			if newValue != nil {
				_ = json.Unmarshal(newValue, &l.Values)
			}
			out = append(out, l)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}

	// The profile, with the rules of PATCH /me.
	clk.Set(recordNow.Add(time.Minute))
	robert := "Robert"
	if err := accounts.UpdateProfile(t.Context(), adminActor, bob, user.ProfileChanges{FirstName: &robert, RemovePseudo: true}, "Typo in the name"); err != nil {
		t.Fatal(err)
	}
	got := lines(bob)
	if len(got) != 2 || got[1].Action != admin.ActionUserUpdate ||
		!reflect.DeepEqual(got[1].OldValue, map[string]any{"first_name": "Test", "pseudo": "bob"}) ||
		!reflect.DeepEqual(got[1].Values, map[string]any{"first_name": "Robert", "pseudo": nil}) {
		t.Fatalf("journal of bob %+v, want the change of his names", got)
	}

	// Blocked: his next login is refused; unblocked: served again.
	clk.Set(recordNow.Add(2 * time.Minute))
	if err := accounts.Block(t.Context(), adminActor, bob, "Spam"); err != nil {
		t.Fatal(err)
	}
	if _, err := users.GetOrCreateBySubject(t.Context(), "bob", ""); !errors.Is(err, apperr.New(apperr.CodeAccountBlocked, nil)) {
		t.Fatalf("login of a blocked account: %v, want account_blocked", err)
	}
	// Blocked already: blocked since the first time, an empty line.
	clk.Set(recordNow.Add(150 * time.Second))
	if err := accounts.Block(t.Context(), adminActor, bob, "Spam again"); err != nil {
		t.Fatal(err)
	}
	if err := accounts.Block(t.Context(), adminActor, eve, "Spam"); !errors.Is(err, apperr.New(apperr.CodeForbidden, nil)) {
		t.Fatalf("self block: %v, want forbidden", err)
	}
	clk.Set(recordNow.Add(3 * time.Minute))
	if err := accounts.Unblock(t.Context(), adminActor, bob, "Blocked by mistake"); err != nil {
		t.Fatal(err)
	}
	if _, err := users.GetOrCreateBySubject(t.Context(), "bob", ""); err != nil {
		t.Fatalf("login once unblocked: %v", err)
	}
	// Nothing else changed: his participation is as it was.
	var participations int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM participants
		WHERE challenge_id = $1 AND user_id = $2 AND abandoned_at IS NULL AND joined_at = $3`, solo, bob, start).Scan(&participations); err != nil || participations != 1 {
		t.Fatalf("%d active participations of bob, %v, want his own untouched", participations, err)
	}

	// The last creator of a challenge in progress: refused, as DELETE /me.
	clk.Set(recordNow.Add(4 * time.Minute))
	if err := accounts.Delete(t.Context(), adminActor, bob, "Asked by the owner"); !errors.Is(err, apperr.New(apperr.CodeLastCreator, nil)) {
		t.Fatalf("delete the last creator: %v, want last_creator", err)
	}
	if n := len(lines(bob)); n != 5 {
		t.Fatalf("%d journal lines of bob, want 5: nothing written by the refusal", n)
	}
	exec(`INSERT INTO challenge_creators (challenge_id, user_id) VALUES ($1, $2)`, solo, eve)

	if err := accounts.Delete(t.Context(), adminActor, bob, "Asked by the owner"); err != nil {
		t.Fatal(err)
	}
	var anonymized, pending int
	if err := pool.QueryRow(t.Context(), `SELECT
		(SELECT count(*) FROM users WHERE id = $1 AND anonymized_at IS NOT NULL AND first_name IS NULL),
		(SELECT count(*) FROM pending_auth0_deletions WHERE user_id = $1)`, bob).Scan(&anonymized, &pending); err != nil {
		t.Fatal(err)
	}
	// Deleted at Auth0 after the commit: nothing left pending.
	if anonymized != 1 || pending != 0 {
		t.Fatalf("anonymized %d, pending at Auth0 %d, want 1 and 0", anonymized, pending)
	}
	want := []line{
		{Action: "test.action", Reason: "Earlier change", OldValue: map[string]any{"status": "abandoned"}, Values: map[string]any{"status": "active"}},
		{Action: admin.ActionUserUpdate, Reason: "Typo in the name", OldValue: map[string]any{}, Values: map[string]any{}},
		{Action: admin.ActionUserBlock, Reason: "Spam", OldValue: map[string]any{"blocked_at": nil}, Values: map[string]any{"blocked_at": recordNow.Add(2 * time.Minute).Format(time.RFC3339)}},
		{Action: admin.ActionUserBlock, Reason: "Spam again", OldValue: map[string]any{}, Values: map[string]any{}},
		{Action: admin.ActionUserUnblock, Reason: "Blocked by mistake", OldValue: map[string]any{"blocked_at": recordNow.Add(2 * time.Minute).Format(time.RFC3339)}, Values: map[string]any{"blocked_at": nil}},
		{Action: admin.ActionUserDelete, Reason: "Asked by the owner", OldValue: map[string]any{"deleted_at": nil}, Values: map[string]any{"deleted_at": recordNow.Add(4 * time.Minute).Format(time.RFC3339)}},
	}
	if got := lines(bob); !reflect.DeepEqual(got, want) {
		t.Fatalf("journal of bob\n%+v\nwant\n%+v", got, want)
	}

	// DELETE /me erases them too; dana's line stays as it was.
	if err := users.Delete(t.Context(), carl); err != nil {
		t.Fatal(err)
	}
	if got := lines(carl); len(got) != 1 || len(got[0].OldValue) != 0 || len(got[0].Values) != 0 || got[0].Reason != "Earlier change" {
		t.Fatalf("journal of carl %+v, want his line without its profile values", got)
	}
	if got := lines(dana); len(got) != 1 || got[0].OldValue["last_name"] != "Old" || got[0].Values["last_name"] != "New" {
		t.Fatalf("journal of dana %+v, want it untouched", got)
	}
}
