//go:build integration

package admin

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damchi/sport-challenge-api/internal/activity"
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

	t.Run("blocked", func(t *testing.T) {
		blocked := insertUser(t, pool, "email|blocked", created, false)
		gone := insertUser(t, pool, "email|blocked-gone", created, true)
		exec(t, pool, `UPDATE users SET blocked_at = $2 WHERE id IN ($1, $3)`, blocked, created.Add(time.Hour), gone)
		got, err := r.User(t.Context(), blocked)
		if err != nil || got.State != UserBlocked {
			t.Fatalf("User() = %+v, %v, want blocked", got, err)
		}
		// Deleted once blocked: the deletion tells its state.
		if got, err := r.User(t.Context(), gone); err != nil || got.State != UserDeleted {
			t.Fatalf("User() = %+v, %v, want deleted", got, err)
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
	// An elimination by repetitions (deleted challenge), one in meters, and
	// none for the others; another reference sport than the default.
	exec(t, pool, `INSERT INTO categories (challenge_id, type, position, params) VALUES
		($1, 'elimination', 1, '{"exercise": "burpee", "step_repetitions": 10}'),
		($2, 'cumulative_time', 1, '{}'), ($2, 'elimination', 2, '{"sport": "run", "step_meters": 2000}')`, gone, left)
	exec(t, pool, `UPDATE challenges SET reference_sport = 'ride' WHERE id = $1`, created)

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
	burpee := "burpee"
	want := []UserChallengeRow{
		{ID: gone, Title: "Gone", Visibility: "public", Timezone: "America/Toronto", ReferenceSport: "run", Exercise: &burpee, StartsAt: starts, EndsAt: ends, CreatedAt: at(4), DeletedAt: &deletedAt, IsCreator: true, ParticipantStatus: &active, JoinedAt: ptr(at(4))},
		{ID: creatorOnly, Title: "Creator", Visibility: "public", Timezone: "America/Toronto", ReferenceSport: "run", StartsAt: starts, EndsAt: ends, CreatedAt: at(3), IsCreator: true},
		{ID: left, Title: "Left", Visibility: "public", Timezone: "America/Toronto", ReferenceSport: "run", StartsAt: starts, EndsAt: ends, CreatedAt: at(2), ParticipantStatus: &abandoned, JoinedAt: ptr(at(2)), AbandonedAt: ptr(at(3))},
		{ID: created, Title: "Mine", Visibility: "private", Timezone: "America/Toronto", ReferenceSport: "ride", StartsAt: starts, EndsAt: ends, CreatedAt: at(1), IsCreator: true, ParticipantStatus: &active, JoinedAt: ptr(at(1))},
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

	t.Run("the repetitions of each, by exercise", func(t *testing.T) {
		// On session(2) of ann and session(5) of bob, never in her page.
		exec(t, pool, `INSERT INTO activity_repetitions (activity_id, exercise_code, repetitions)
			SELECT id, e, n FROM activities, (VALUES ('push_up', 12), ('burpee', 30)) AS r (e, n) WHERE started_at IN ($1, $2)`, session(2), session(5))
		got, err := r.ListUserActivities(t.Context(), ann, ActivityQuery{Limit: 10})
		if err != nil || len(got) != 3 {
			t.Fatalf("ListUserActivities() = %+v, %v; want 3 sessions", got, err)
		}
		want := []activity.Repetitions{{Exercise: "burpee", Count: 30}, {Exercise: "push_up", Count: 12}}
		if got[0].Repetitions != nil || !slices.Equal(got[1].Repetitions, want) || got[2].Repetitions != nil {
			t.Fatalf("repetitions %v, %v, %v; want none, %v, none", got[0].Repetitions, got[1].Repetitions, got[2].Repetitions, want)
		}
	})

	t.Run("the last change of an admin", func(t *testing.T) {
		dan := insertUser(t, pool, "email|dan", recorded, false)
		// session(6) entered by ann; the three others of ann listed above.
		insertActivity(t, pool, ann, recorded, false, session(6))
		all, err := r.ListUserActivities(t.Context(), ann, ActivityQuery{Limit: 10})
		if err != nil || len(all) != 4 {
			t.Fatalf("ListUserActivities() = %d activities, %v; want 4", len(all), err)
		}
		untouched, added, addedThenChanged, entered := all[0], all[1], all[2], all[3]
		at := func(n int) time.Time { return recorded.Add(time.Duration(n) * time.Hour) }
		line := func(action, target string, when time.Time) {
			exec(t, pool, `INSERT INTO admin_actions (admin_id, created_at, action, target_type, target_id, reason)
				VALUES ($1, $2, $3, 'activity', $4, 'Correction')`, dan, when, action, target)
		}
		line(ActionActivityCreate, added.ID, at(1))
		// The update written before the creation in the table, later in time.
		line(ActionActivityUpdate, addedThenChanged.ID, at(3))
		line(ActionActivityCreate, addedThenChanged.ID, at(2))
		line(ActionActivityUpdate, entered.ID, at(4))
		// Neither an activity line nor another target counts.
		exec(t, pool, `INSERT INTO admin_actions (admin_id, created_at, action, target_type, target_id, reason)
			VALUES ($1, $2, 'user.block', 'user', $3, 'Blocage')`, dan, at(5), untouched.ID)

		got, err := r.ListUserActivities(t.Context(), ann, ActivityQuery{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		want := []*ActivityChange{
			nil,
			{Action: ActivityCreated, At: at(1), AdminID: dan},
			{Action: ActivityUpdated, At: at(3), AdminID: dan},
			{Action: ActivityUpdated, At: at(4), AdminID: dan},
		}
		for i, a := range got {
			if c := a.AdminChange; c != nil {
				c.At = c.At.UTC()
			}
			if !reflect.DeepEqual(a.AdminChange, want[i]) {
				t.Fatalf("activity %d: admin change %+v, want %+v", i, a.AdminChange, want[i])
			}
		}
	})

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
