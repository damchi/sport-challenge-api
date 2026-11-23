//go:build integration

package admin

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// setProfile gives account id its pseudo, names and e-mail.
func setProfile(t *testing.T, pool *pgxpool.Pool, id, pseudo, first, last, email string) {
	t.Helper()
	exec(t, pool, `UPDATE users SET pseudo = $2, first_name = $3, last_name = $4, email = $5 WHERE id = $1`, id, pseudo, first, last, email)
}

func userIDs(rows []UserRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

func TestRepositoryListUsers(t *testing.T) {
	r, pool := newRepository(t)
	old := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	// Created one minute apart, most recent last.
	at := func(n int) time.Time { return old.Add(time.Duration(n) * time.Minute) }
	ann := insertUser(t, pool, "email|ann", at(1), false)
	setProfile(t, pool, ann, "ann_1", "Ann", "Lée", "ann@example.com")
	bob := insertUser(t, pool, "email|bob", at(2), false)
	setProfile(t, pool, bob, "bob_100", "Bob", "Martin", "b_o%b@mail.test")
	// Deleted accounts have no profile left; one still waits for Auth0.
	pending := insertUser(t, pool, "deleted|pending", at(3), true)
	exec(t, pool, `INSERT INTO pending_auth0_deletions (auth_subject, user_id) VALUES ('email|pending', $1)`, pending)
	gone := insertUser(t, pool, "deleted|gone", at(4), true)
	// A row recorded before the link: it names no account.
	exec(t, pool, `INSERT INTO pending_auth0_deletions (auth_subject) VALUES ('email|before')`)
	fresh := insertUser(t, pool, "email|fresh", at(5), false)

	tests := []struct {
		name string
		q    UserQuery
		want []string
	}{
		{name: "every account, deleted included, most recent first", q: UserQuery{}, want: []string{fresh, gone, pending, bob, ann}},
		{name: "pseudo, case insensitive", q: UserQuery{Search: "ANN_"}, want: []string{ann}},
		{name: "first name", q: UserQuery{Search: "bo"}, want: []string{bob}},
		{name: "last name, accents count", q: UserQuery{Search: "lée"}, want: []string{ann}},
		{name: "accents are not ignored", q: UserQuery{Search: "lee"}, want: nil},
		{name: "e-mail", q: UserQuery{Search: "@example.COM"}, want: []string{ann}},
		{name: "% is a plain character", q: UserQuery{Search: "o%b"}, want: []string{bob}},
		{name: "_ is a plain character", q: UserQuery{Search: "b_o"}, want: []string{bob}},
		{name: "any of the columns", q: UserQuery{Search: "n"}, want: []string{bob, ann}},
		{name: "a deleted account is never found", q: UserQuery{Search: "deleted"}, want: nil},
		{name: "after a position", q: UserQuery{After: &Position{CreatedAt: at(3), ID: pending}}, want: []string{bob, ann}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := tt.q
			q.Limit = 10
			got, err := r.ListUsers(t.Context(), q)
			if err != nil {
				t.Fatal(err)
			}
			if g := userIDs(got); !slices.Equal(g, tt.want) {
				t.Fatalf("ListUsers() = %v, want %v", g, tt.want)
			}
		})
	}

	t.Run("the limit", func(t *testing.T) {
		got, err := r.ListUsers(t.Context(), UserQuery{Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		if g := userIDs(got); !slices.Equal(g, []string{fresh, gone}) {
			t.Fatalf("ListUsers() = %v, want the 2 most recent", g)
		}
	})

	t.Run("states", func(t *testing.T) {
		got, err := r.ListUsers(t.Context(), UserQuery{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		states := map[string]string{}
		for _, u := range got {
			states[u.ID] = u.State
		}
		want := map[string]string{fresh: UserActive, gone: UserDeleted, pending: UserAuth0DeletionPending, bob: UserActive, ann: UserActive}
		if !reflect.DeepEqual(states, want) {
			t.Fatalf("states = %v, want %v", states, want)
		}
	})
}

func TestRepositoryUser(t *testing.T) {
	r, pool := newRepository(t)
	created := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	ann := insertUser(t, pool, "email|ann", created, false)
	setProfile(t, pool, ann, "ann_1", "Ann", "Lee", "ann@example.com")
	bob := insertUser(t, pool, "email|bob", created, false)

	// Her last activity is the last one recorded, not the last session; a
	// deleted one, or one of another account, does not count.
	insertActivity(t, pool, ann, created.Add(time.Hour), false, created.Add(48*time.Hour))
	insertActivity(t, pool, ann, created.Add(2*time.Hour), false, created.Add(time.Hour))
	insertActivity(t, pool, ann, created.Add(3*time.Hour), true)
	insertActivity(t, pool, bob, created.Add(4*time.Hour), false)

	// Her challenges: one she joined, one she creates and joined (counted
	// once), one she only creates, and a deleted one; not bob's.
	starts := created.AddDate(0, 0, 7)
	joined := insertChallengeRow(t, pool, bob, challengeRow{title: "A", visibility: "public", startsAt: starts, weeks: 2, createdAt: created})
	exec(t, pool, `INSERT INTO participants (challenge_id, user_id, joined_at) VALUES ($1, $2, $3)`, joined, ann, created)
	insertChallengeRow(t, pool, ann, challengeRow{title: "B", visibility: "public", startsAt: starts, weeks: 2, createdAt: created})
	creatorOnly := insertChallengeRow(t, pool, bob, challengeRow{title: "C", visibility: "public", startsAt: starts, weeks: 2, createdAt: created})
	exec(t, pool, `INSERT INTO challenge_creators (challenge_id, user_id, created_at) VALUES ($1, $2, $3)`, creatorOnly, ann, created)
	deleted := created.Add(time.Hour)
	insertChallengeRow(t, pool, ann, challengeRow{title: "D", visibility: "private", startsAt: starts, weeks: 2, createdAt: created, deletedAt: &deleted})
	insertChallengeRow(t, pool, bob, challengeRow{title: "E", visibility: "public", startsAt: starts, weeks: 2, createdAt: created})

	got, err := r.User(t.Context(), ann)
	if err != nil {
		t.Fatal(err)
	}
	got.CreatedAt = got.CreatedAt.UTC()
	got.LastActivityAt = utc(got.LastActivityAt)
	last := created.Add(2 * time.Hour)
	want := UserRow{
		ID: ann, Pseudo: ptr("ann_1"), FirstName: ptr("Ann"), LastName: ptr("Lee"), Email: ptr("ann@example.com"),
		State: UserActive, CreatedAt: created, LastActivityAt: &last, ChallengeCount: 4,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("User() =\n%+v\nwant\n%+v", got, want)
	}

	t.Run("without activity nor challenge", func(t *testing.T) {
		none := insertUser(t, pool, "email|none", created, false)
		got, err := r.User(t.Context(), none)
		if err != nil || got.LastActivityAt != nil || got.ChallengeCount != 0 {
			t.Fatalf("User() = %+v, %v, want no last activity and no challenge", got, err)
		}
	})

	t.Run("unknown", func(t *testing.T) {
		if _, err := r.User(t.Context(), "0b9d4a5e-6a43-4c8e-9d1f-7a2b3c4d5e6f"); !errors.Is(err, errUserNotFound) {
			t.Fatalf("User() error = %v, want errUserNotFound", err)
		}
	})
}

func TestRepositoryListUserChallenges(t *testing.T) {
	r, pool := newRepository(t)
	old := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	at := func(n int) time.Time { return old.Add(time.Duration(n) * time.Minute) }
	ann := insertUser(t, pool, "email|ann", old, false)
	bob := insertUser(t, pool, "email|bob", old, false)
	starts := old.AddDate(0, 0, 7)

	// Created one minute apart, most recent last.
	created := insertChallengeRow(t, pool, ann, challengeRow{title: "Mine", visibility: "private", startsAt: starts, weeks: 2, createdAt: at(1)})
	left := insertChallengeRow(t, pool, bob, challengeRow{title: "Left", visibility: "public", startsAt: starts, weeks: 2, createdAt: at(2)})
	exec(t, pool, `INSERT INTO participants (challenge_id, user_id, joined_at, status, abandoned_at) VALUES ($1, $2, $3, 'abandoned', $4)`, left, ann, at(2), at(3))
	creatorOnly := insertChallengeRow(t, pool, bob, challengeRow{title: "Creator", visibility: "public", startsAt: starts, weeks: 2, createdAt: at(3)})
	exec(t, pool, `INSERT INTO challenge_creators (challenge_id, user_id, created_at) VALUES ($1, $2, $3)`, creatorOnly, ann, at(3))
	deletedAt := at(10)
	gone := insertChallengeRow(t, pool, ann, challengeRow{title: "Gone", visibility: "public", startsAt: starts, weeks: 2, createdAt: at(4), deletedAt: &deletedAt})
	insertChallengeRow(t, pool, bob, challengeRow{title: "Not hers", visibility: "public", startsAt: starts, weeks: 2, createdAt: at(5)})

	got, err := r.ListUserChallenges(t.Context(), ann, PageQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for i := range got {
		g := &got[i]
		g.StartsAt, g.EndsAt, g.CreatedAt = g.StartsAt.UTC(), g.EndsAt.UTC(), g.CreatedAt.UTC()
		g.DeletedAt, g.JoinedAt, g.AbandonedAt = utc(g.DeletedAt), utc(g.JoinedAt), utc(g.AbandonedAt)
	}
	ends := starts.AddDate(0, 0, 14)
	active := "active"
	abandoned := "abandoned"
	want := []UserChallengeRow{
		{ID: gone, Title: "Gone", Visibility: "public", StartsAt: starts, EndsAt: ends, CreatedAt: at(4), DeletedAt: &deletedAt, IsCreator: true, ParticipantStatus: &active, JoinedAt: ptr(at(4))},
		{ID: creatorOnly, Title: "Creator", Visibility: "public", StartsAt: starts, EndsAt: ends, CreatedAt: at(3), IsCreator: true},
		{ID: left, Title: "Left", Visibility: "public", StartsAt: starts, EndsAt: ends, CreatedAt: at(2), ParticipantStatus: &abandoned, JoinedAt: ptr(at(2)), AbandonedAt: ptr(at(3))},
		{ID: created, Title: "Mine", Visibility: "private", StartsAt: starts, EndsAt: ends, CreatedAt: at(1), IsCreator: true, ParticipantStatus: &active, JoinedAt: ptr(at(1))},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListUserChallenges() =\n%+v\nwant\n%+v", got, want)
	}

	t.Run("a page after a position", func(t *testing.T) {
		got, err := r.ListUserChallenges(t.Context(), ann, PageQuery{Limit: 1, After: &Position{CreatedAt: at(3), ID: creatorOnly}})
		if err != nil || len(got) != 1 || got[0].ID != left {
			t.Fatalf("ListUserChallenges() = %+v, %v, want the one after the position", got, err)
		}
	})

	t.Run("an account without challenge", func(t *testing.T) {
		none := insertUser(t, pool, "email|none", old, false)
		got, err := r.ListUserChallenges(t.Context(), none, PageQuery{Limit: 10})
		if err != nil || len(got) != 0 {
			t.Fatalf("ListUserChallenges() = %+v, %v, want none", got, err)
		}
	})

	t.Run("unknown", func(t *testing.T) {
		if _, err := r.ListUserChallenges(t.Context(), "0b9d4a5e-6a43-4c8e-9d1f-7a2b3c4d5e6f", PageQuery{Limit: 10}); !errors.Is(err, errUserNotFound) {
			t.Fatalf("ListUserChallenges() error = %v, want errUserNotFound", err)
		}
	})
}

func TestRepositoryListUserActivities(t *testing.T) {
	r, pool := newRepository(t)
	recorded := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	ann := insertUser(t, pool, "email|ann", recorded, false)
	bob := insertUser(t, pool, "email|bob", recorded, false)

	// Sessions an hour apart, recorded in another order; one deleted, one of
	// bob's.
	session := func(n int) time.Time { return recorded.Add(-time.Duration(n) * time.Hour) }
	insertActivity(t, pool, ann, recorded, false, session(3))
	insertActivity(t, pool, ann, recorded.Add(time.Minute), false, session(1))
	insertActivity(t, pool, ann, recorded.Add(2*time.Minute), false, session(2))
	insertActivity(t, pool, ann, recorded, true, session(4))
	insertActivity(t, pool, bob, recorded, false, session(5))
	exec(t, pool, `UPDATE activities SET elevation_gain_meters = 40 WHERE started_at = $1`, session(1))

	got, err := r.ListUserActivities(t.Context(), ann, ActivityQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var starts []time.Time
	for _, a := range got {
		starts = append(starts, a.StartedAt.UTC())
	}
	if want := []time.Time{session(1), session(2), session(3)}; !slices.Equal(starts, want) {
		t.Fatalf("sessions = %v, want %v, the most recent first", starts, want)
	}
	first := got[0]
	first.StartedAt, first.CreatedAt = first.StartedAt.UTC(), first.CreatedAt.UTC()
	want := ActivityRow{
		ID: first.ID, SportCode: "run", StartedAt: session(1), DurationSeconds: 1800, DistanceMeters: ptr(5000),
		ElevationGainMeters: ptr(40), Source: "manual", CreatedAt: recorded.Add(time.Minute),
	}
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("first = %+v, want %+v", first, want)
	}

	t.Run("a page after a position", func(t *testing.T) {
		got2, err := r.ListUserActivities(t.Context(), ann, ActivityQuery{Limit: 1, After: &ActivityPosition{StartedAt: got[0].StartedAt, ID: got[0].ID}})
		if err != nil || len(got2) != 1 || got2[0].ID != got[1].ID {
			t.Fatalf("ListUserActivities() = %+v, %v, want the second session", got2, err)
		}
	})

	t.Run("unknown", func(t *testing.T) {
		if _, err := r.ListUserActivities(t.Context(), "0b9d4a5e-6a43-4c8e-9d1f-7a2b3c4d5e6f", ActivityQuery{Limit: 10}); !errors.Is(err, errUserNotFound) {
			t.Fatalf("ListUserActivities() error = %v, want errUserNotFound", err)
		}
	})
}
