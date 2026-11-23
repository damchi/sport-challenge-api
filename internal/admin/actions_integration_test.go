//go:build integration

package admin

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
)

func countActions(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM admin_actions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func actionIDs(actions []Action) []string {
	out := make([]string, len(actions))
	for i, a := range actions {
		out[i] = a.ID
	}
	return out
}

func TestJournalRecordsInTheTransactionOfTheChange(t *testing.T) {
	r, pool := newRepository(t)
	now := time.Date(2026, 11, 23, 15, 0, 0, 0, time.UTC)
	ann := insertUser(t, pool, "email|ann", now.AddDate(0, -1, 0), false)
	exec(t, pool, `UPDATE users SET pseudo = 'Ann' WHERE id = $1`, ann)
	challenge := insertChallengeRow(t, pool, ann, challengeRow{title: "Défi", visibility: "private", startsAt: now, weeks: 2, createdAt: now})
	j := NewJournal(r, clock.NewFake(now))
	entry := Entry{
		AdminID: ann, Action: "challenge.update", TargetType: TargetChallenge, TargetID: challenge,
		ChallengeIDs: []string{challenge}, OldValue: map[string]any{"title": "Défi"}, NewValue: map[string]any{"title": "Défi 2"},
		Reason: "  Faute de frappe  ",
	}

	t.Run("a change that fails leaves no line", func(t *testing.T) {
		boom := errors.New("the change failed")
		err := db.InTx(t.Context(), r.db, func(ctx context.Context) error {
			if err := j.Record(ctx, entry); err != nil {
				return err
			}
			return boom
		})
		if !errors.Is(err, boom) {
			t.Fatalf("got %v, want %v", err, boom)
		}
		if n := countActions(t, pool); n != 0 {
			t.Fatalf("%d lines after a rolled back change, want 0", n)
		}
	})

	t.Run("a reason out of its bounds rolls the change back", func(t *testing.T) {
		bad := entry
		bad.Reason = "ab"
		err := db.InTx(t.Context(), r.db, func(ctx context.Context) error {
			if err := db.Conn(ctx, r.db).Exec(`UPDATE challenges SET title = 'Changé' WHERE id = ?`, challenge).Error; err != nil {
				return err
			}
			return j.Record(ctx, bad)
		})
		if fieldsOf(err) != "reason" {
			t.Fatalf("got %v, want validation_failed on reason", err)
		}
		var title string
		if err := pool.QueryRow(t.Context(), `SELECT title FROM challenges WHERE id = $1`, challenge).Scan(&title); err != nil {
			t.Fatal(err)
		}
		if title != "Défi" || countActions(t, pool) != 0 {
			t.Fatalf("title %q and %d lines, want the change rolled back", title, countActions(t, pool))
		}
	})

	t.Run("a change that commits keeps its line", func(t *testing.T) {
		err := db.InTx(t.Context(), r.db, func(ctx context.Context) error { return j.Record(ctx, entry) })
		if err != nil {
			t.Fatal(err)
		}
		got, err := r.ListActions(t.Context(), ActionQuery{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Fatalf("%d lines, want 1", len(got))
		}
		a := got[0]
		a.ID = ""
		want := Action{
			Admin: UserRef{ID: ann, Pseudo: ptr("Ann")}, CreatedAt: now, Action: "challenge.update",
			TargetType: TargetChallenge, TargetID: challenge, ChallengeIDs: []string{challenge},
			OldValue: []byte(`{"title": "Défi"}`), NewValue: []byte(`{"title": "Défi 2"}`), Reason: "Faute de frappe",
		}
		a.CreatedAt = a.CreatedAt.UTC()
		if !reflect.DeepEqual(a, want) {
			t.Fatalf("read %+v\nwant %+v", a, want)
		}
	})
}

// recordAt writes an action of admin on target at at, touching challenges.
func recordAt(t *testing.T, r *Repository, at time.Time, admin, targetType, target string, challenges ...string) string {
	t.Helper()
	if challenges == nil {
		challenges = []string{}
	}
	err := NewJournal(r, clock.NewFake(at)).Record(t.Context(), Entry{
		AdminID: admin, Action: targetType + ".update", TargetType: targetType, TargetID: target,
		ChallengeIDs: challenges, Reason: "Correction",
	})
	if err != nil {
		t.Fatal(err)
	}
	var id string
	if err := r.db.Raw(`SELECT id FROM admin_actions WHERE created_at = ? AND target_id = ?`, at, target).Scan(&id).Error; err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRepositoryListActions(t *testing.T) {
	r, pool := newRepository(t)
	now := time.Date(2026, 11, 23, 15, 0, 0, 0, time.UTC)
	old := now.AddDate(0, -1, 0)
	ann := insertUser(t, pool, "email|ann", old, false)
	bob := insertUser(t, pool, "email|bob", old, false)
	spring := insertChallengeRow(t, pool, ann, challengeRow{title: "Spring", visibility: "public", startsAt: now, weeks: 2, createdAt: old})
	autumn := insertChallengeRow(t, pool, ann, challengeRow{title: "Autumn", visibility: "public", startsAt: now, weeks: 2, createdAt: old})
	activity := "4d5e6f7a-8b9c-4dae-8f01-2b3c4d5e6f7a"

	at := func(n int) time.Time { return now.Add(time.Duration(n) * time.Minute) }
	onSpring := recordAt(t, r, at(1), ann, TargetChallenge, spring, spring)
	onActivity := recordAt(t, r, at(2), ann, TargetActivity, activity, spring, autumn)
	onBob := recordAt(t, r, at(3), bob, TargetUser, ann)
	onParticipant := recordAt(t, r, at(4), bob, TargetParticipant, ann, autumn)

	tests := []struct {
		name string
		q    ActionQuery
		want []string
	}{
		{name: "every action, most recent first", q: ActionQuery{}, want: []string{onParticipant, onBob, onActivity, onSpring}},
		{name: "a target type", q: ActionQuery{TargetType: TargetChallenge}, want: []string{onSpring}},
		{name: "a target id", q: ActionQuery{TargetID: ann}, want: []string{onParticipant, onBob}},
		{name: "a target", q: ActionQuery{TargetType: TargetUser, TargetID: ann}, want: []string{onBob}},
		{name: "an admin", q: ActionQuery{AdminID: ann}, want: []string{onActivity, onSpring}},
		{name: "a challenge touched, whatever the target", q: ActionQuery{ChallengeID: autumn}, want: []string{onParticipant, onActivity}},
		{name: "filters add up", q: ActionQuery{ChallengeID: spring, AdminID: ann, TargetType: TargetActivity}, want: []string{onActivity}},
		{name: "no match", q: ActionQuery{AdminID: activity}, want: []string{}},
		{name: "after a position", q: ActionQuery{After: &Position{CreatedAt: at(3), ID: onBob}}, want: []string{onActivity, onSpring}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := tt.q
			q.Limit = 10
			got, err := r.ListActions(t.Context(), q)
			if err != nil {
				t.Fatal(err)
			}
			if ids := actionIDs(got); !slices.Equal(ids, tt.want) {
				t.Fatalf("got %v, want %v", ids, tt.want)
			}
		})
	}

	t.Run("pages", func(t *testing.T) {
		var seen []string
		q := ActionQuery{Limit: 1}
		for {
			got, err := r.ListActions(t.Context(), q)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) == 0 {
				break
			}
			seen = append(seen, got[0].ID)
			q.After = &Position{CreatedAt: got[0].CreatedAt, ID: got[0].ID}
		}
		if want := []string{onParticipant, onBob, onActivity, onSpring}; !slices.Equal(seen, want) {
			t.Fatalf("pages %v, want %v", seen, want)
		}
	})

	t.Run("a deleted admin is not named", func(t *testing.T) {
		// Deleting an account erases its names (user.Repository.Anonymize).
		exec(t, pool, `UPDATE users SET pseudo = NULL, first_name = NULL, last_name = NULL, anonymized_at = now() WHERE id = $1`, bob)
		got, err := r.ListActions(t.Context(), ActionQuery{AdminID: bob, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0].Admin != (UserRef{ID: bob}) {
			t.Fatalf("got %+v, want the actions of the account, unnamed", got)
		}
	})
}

func TestRepositoryChallengeRecentActions(t *testing.T) {
	r, pool := newRepository(t)
	now := time.Date(2026, 11, 23, 15, 0, 0, 0, time.UTC)
	ann := insertUser(t, pool, "email|ann", now.AddDate(0, -1, 0), false)
	spring := insertChallengeRow(t, pool, ann, challengeRow{title: "Spring", visibility: "public", startsAt: now, weeks: 2, createdAt: now})
	autumn := insertChallengeRow(t, pool, ann, challengeRow{title: "Autumn", visibility: "public", startsAt: now, weeks: 2, createdAt: now})

	var want []string
	for n := range recentActions + 2 {
		id := recordAt(t, r, now.Add(time.Duration(n)*time.Minute), ann, TargetChallenge, spring, spring)
		want = append([]string{id}, want...)
	}
	// Later, but on another challenge: never in the detail of spring.
	recordAt(t, r, now.Add(time.Hour), ann, TargetChallenge, autumn, autumn)

	data, err := r.Challenge(t.Context(), spring)
	if err != nil {
		t.Fatal(err)
	}
	if got := actionIDs(data.RecentActions); !slices.Equal(got, want[:recentActions]) {
		t.Fatalf("recent actions %v, want the %d most recent of spring %v", got, recentActions, want[:recentActions])
	}

	empty, err := r.Challenge(t.Context(), insertChallengeRow(t, pool, ann, challengeRow{title: "New", visibility: "public", startsAt: now, weeks: 2, createdAt: now}))
	if err != nil {
		t.Fatal(err)
	}
	if empty.RecentActions == nil || len(empty.RecentActions) != 0 {
		t.Fatalf("recent actions %v, want none", empty.RecentActions)
	}
}
