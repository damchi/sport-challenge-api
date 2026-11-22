//go:build integration

package main

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/activity"
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
)

// insertActivity stores an activity of userID started at startedAt, deleted
// when deleted is set, as is: never through the service, so that any week
// may hold it.
func (r recording) insertActivity(userID string, startedAt time.Time, deleted bool) string {
	r.t.Helper()
	var deletedAt *time.Time
	if deleted {
		deletedAt = &recordNow
	}
	var id string
	if err := r.pool.QueryRow(r.t.Context(), `INSERT INTO activities (user_id, sport_code, started_at, duration_seconds, distance_meters, deleted_at)
		VALUES ($1, 'run', $2, 600, 2000, $3) RETURNING id`, userID, startedAt, deletedAt).Scan(&id); err != nil {
		r.t.Fatal(err)
	}
	return id
}

// TestListChallengeActivities lists the activities of every participant of
// a challenge (SC-API-143): most recent first, ties broken by id, on several
// pages; those of a participant who abandoned stay, counting nowhere; those
// outside the window, deleted, of a deleted account or of someone who never
// joined never show; contributions and locked are those of the list of the
// author.
func TestListChallengeActivities(t *testing.T) {
	r := newRecording(t)
	c := r.challenge([3]int{2026, 6, 1}, 3, "active", false)
	if _, err := r.pool.Exec(t.Context(), "UPDATE challenges SET visibility = 'private' WHERE id = $1", c); err != nil {
		t.Fatal(err)
	}
	public := r.challenge([3]int{2026, 6, 1}, 3, "active", false)
	runner, quitter, gone, outsider, boss := r.newUser("runner2"), r.newUser("quitter"), r.newUser("gone"), r.newUser("outsider"), r.newUser("boss")
	r.join(c, runner, "active")
	// Left at recordNow, once week 1 was locked: their activity of week 1
	// stays locked.
	r.join(c, quitter, "abandoned")
	r.join(c, gone, "active")
	// A creator who is not a participant still reads the activities.
	if _, err := r.pool.Exec(t.Context(), "INSERT INTO challenge_creators (challenge_id, user_id) VALUES ($1, $2)", c, boss); err != nil {
		t.Fatal(err)
	}

	tie := toronto(t, 2026, 6, 10, 7)
	listed := []string{
		r.insertActivity(r.user, tie, false),
		r.insertActivity(runner, tie, false),
		r.insertActivity(quitter, tie, false),
		r.insertActivity(runner, toronto(t, 2026, 6, 9, 7), false),
		r.insertActivity(quitter, toronto(t, 2026, 6, 3, 7), false),
		r.insertActivity(r.user, toronto(t, 2026, 6, 1, 0), false), // the first instant of the window
	}
	r.insertActivity(runner, toronto(t, 2026, 5, 31, 23), false) // before the window
	r.insertActivity(runner, toronto(t, 2026, 6, 22, 0), false)  // at its end, outside it
	r.insertActivity(runner, toronto(t, 2026, 6, 8, 7), true)    // deleted
	r.insertActivity(outsider, toronto(t, 2026, 6, 8, 7), false) // never joined
	r.insertActivity(boss, toronto(t, 2026, 6, 8, 7), false)     // creator, not a participant
	r.insertActivity(gone, toronto(t, 2026, 6, 8, 7), false)
	// A deleted account, whose rows are left: never listed.
	if _, err := r.pool.Exec(t.Context(), "UPDATE users SET anonymized_at = $1 WHERE id = $2", recordNow, gone); err != nil {
		t.Fatal(err)
	}
	// Most recent first, then by id, descending.
	slices.SortStableFunc(listed[:3], func(a, b string) int { return strings.Compare(b, a) })

	whole, err := r.activities.OfChallenge(t.Context(), runner, c, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := authoredIDs(whole.Items); !slices.Equal(got, listed) || whole.Next != nil {
		t.Fatalf("activities %v, next %v, want %v on one page", got, whole.Next, listed)
	}

	// The same list, two by two.
	var paged []string
	var after *activity.Position
	for range len(listed) {
		page, err := r.activities.OfChallenge(t.Context(), runner, c, 2, after)
		if err != nil {
			t.Fatal(err)
		}
		paged = append(paged, authoredIDs(page.Items)...)
		if after = page.Next; after == nil {
			break
		}
	}
	if !slices.Equal(paged, listed) {
		t.Fatalf("paged activities %v, want %v", paged, listed)
	}

	names := map[string]string{r.user: "runner", runner: "runner2", quitter: "quitter"}
	for _, it := range whole.Items {
		if it.Author.Pseudo == nil || *it.Author.Pseudo != names[it.UserID] {
			t.Errorf("activity %s: author %+v, want %s", it.ID, it.Author, names[it.UserID])
		}
		// As the list of the author shows it, at the same instant.
		own, err := r.activities.OfParticipant(t.Context(), runner, c, it.UserID, 100, nil)
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(own.Items, func(o activity.Recorded) bool { return o.ID == it.ID })
		if i < 0 || !reflect.DeepEqual(own.Items[i].Contributions, it.Contributions) || own.Items[i].Locked != it.Locked {
			t.Errorf("activity %s: contributions %+v, locked %v, want those of the list of its author", it.ID, it.Contributions, it.Locked)
		}
		switch {
		case it.UserID == quitter && len(it.Contributions) != 0:
			t.Errorf("activity %s of the participant who abandoned counts in %v, want nowhere", it.ID, challengeIDs(it.Contributions))
		case it.UserID != quitter && !slices.Equal(challengeIDs(it.Contributions), []string{c}):
			t.Errorf("activity %s counts in %v, want [%s]", it.ID, challengeIDs(it.Contributions), c)
		}
		// Week 1 ended on 2026-06-08 at 7 am: locked; week 2 still open.
		if wantLocked := it.StartedAt.Before(toronto(t, 2026, 6, 8, 0)); it.Locked != wantLocked {
			t.Errorf("activity %s started at %s: locked %v, want %v", it.ID, it.StartedAt, it.Locked, wantLocked)
		}
	}

	tests := []struct {
		name      string
		actor     string
		challenge string
		want      apperr.Code // empty: the whole list
	}{
		{name: "creator, participant", actor: r.user, challenge: c},
		{name: "participant who abandoned", actor: quitter, challenge: c},
		{name: "creator who is not a participant", actor: boss, challenge: c},
		{name: "non-participant of a public challenge", actor: outsider, challenge: public, want: apperr.CodeNotFound},
		{name: "non-participant of a private challenge", actor: outsider, challenge: c, want: apperr.CodeChallengeNotFound},
		{name: "missing challenge", actor: runner, challenge: "4b3a2c1d-0e9f-4a8b-9c7d-6e5f4a3b2c1d", want: apperr.CodeChallengeNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, err := r.activities.OfChallenge(t.Context(), tt.actor, tt.challenge, 100, nil)
			if tt.want != "" {
				var appErr *apperr.Error
				if !errors.As(err, &appErr) || appErr.Code != tt.want {
					t.Fatalf("error = %v, want %s", err, tt.want)
				}
				return
			}
			if err != nil || !slices.Equal(authoredIDs(page.Items), listed) {
				t.Fatalf("OfChallenge() = %v, %v, want %v", authoredIDs(page.Items), err, listed)
			}
		})
	}
}

func authoredIDs(items []activity.Authored) []string {
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	return ids
}
