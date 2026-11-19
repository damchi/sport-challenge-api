//go:build integration

package main

import (
	"errors"
	"io"
	"log/slog"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damchi/sport-challenge-api/internal/activity"
	"github.com/damchi/sport-challenge-api/internal/challenge"
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/platform/db/testdb"
	"github.com/damchi/sport-challenge-api/internal/sport"
	"github.com/damchi/sport-challenge-api/internal/user"
)

// recordNow is a Thursday: week 2 of a challenge started on Monday
// 2026-06-01 in Toronto, whose week 1 ended on 2026-06-08.
var recordNow = time.Date(2026, 6, 11, 16, 0, 0, 0, time.UTC)

// recording wires the modules as run does, at recordNow, for a user whose
// profile is complete.
type recording struct {
	t          *testing.T
	pool       *pgxpool.Pool
	activities *activity.Service
	user       string
}

func newRecording(t *testing.T) recording {
	t.Helper()
	pool := testdb.New(t)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	gdb, err := db.NewGorm(pool, logger)
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(recordNow)
	users := user.NewService(user.NewRepository(gdb), nil, nil, logger)
	sports := sport.NewService(sport.NewRepository(gdb))
	challenges := challenge.NewService(challenge.NewRepository(gdb), challengeProfiles{users}, sports, clk)
	activities := activity.NewService(activity.NewRepository(gdb), users, sports, participations{challenges}, activityScopes{challenges}, clk)

	var id string
	if err := pool.QueryRow(t.Context(), "INSERT INTO users (auth_subject, pseudo, first_name, last_name) VALUES ('email|runner', 'runner', 'Rose', 'Runner') RETURNING id").Scan(&id); err != nil {
		t.Fatal(err)
	}
	return recording{t: t, pool: pool, activities: activities, user: id}
}

// toronto is local time in Toronto, in UTC.
func toronto(t *testing.T, y, mo, d, h int) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	return time.Date(y, time.Month(mo), d, h, 0, 0, 0, loc).UTC()
}

// challenge inserts a public challenge of weeks weeks from local midnight
// of day in Toronto, created by the user. status is the user's
// participation ("" when they never joined).
func (r recording) challenge(day [3]int, weeks int, status string, deleted bool) string {
	r.t.Helper()
	start := toronto(r.t, day[0], day[1], day[2], 0)
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		r.t.Fatal(err)
	}
	end := start.In(loc).AddDate(0, 0, 7*weeks).UTC()
	var deletedAt *time.Time
	if deleted {
		deletedAt = &recordNow
	}
	var id string
	if err := r.pool.QueryRow(r.t.Context(), `INSERT INTO challenges (title, visibility, timezone, starts_at, ends_at, created_by, deleted_at)
		VALUES ('Spring', 'public', 'America/Toronto', $1, $2, $3, $4) RETURNING id`, start, end, r.user, deletedAt).Scan(&id); err != nil {
		r.t.Fatal(err)
	}
	if status == "" {
		return id
	}
	if _, err := r.pool.Exec(r.t.Context(), "INSERT INTO challenge_creators (challenge_id, user_id) VALUES ($1, $2)", id, r.user); err != nil {
		r.t.Fatal(err)
	}
	var abandonedAt *time.Time
	if status == string(challenge.ParticipantAbandoned) {
		abandonedAt = &start
	}
	if _, err := r.pool.Exec(r.t.Context(), "INSERT INTO participants VALUES ($1, $2, $3, $4, $5)", id, r.user, status, start, abandonedAt); err != nil {
		r.t.Fatal(err)
	}
	return id
}

func (r recording) record(startedAt time.Time) (activity.Recorded, error) {
	d := 5000
	return r.activities.Record(r.t.Context(), r.user, nil, activity.Draft{SportCode: "run", StartedAt: startedAt, DurationSeconds: 1500, DistanceMeters: &d})
}

func (r recording) count() int {
	r.t.Helper()
	var n int
	if err := r.pool.QueryRow(r.t.Context(), "SELECT count(*) FROM activities WHERE user_id = $1", r.user).Scan(&n); err != nil {
		r.t.Fatal(err)
	}
	return n
}

func TestRecordCountsInTheChallengesJoined(t *testing.T) {
	r := newRecording(t)
	monday := r.challenge([3]int{2026, 6, 1}, 3, "active", false) // in week 2 at recordNow
	second := r.challenge([3]int{2026, 6, 8}, 2, "active", false) // in week 1 at recordNow

	got, err := r.record(toronto(t, 2026, 6, 10, 7))
	if err != nil {
		t.Fatal(err)
	}
	// Memberships lists by start: monday first.
	if !got.Created || !slices.Equal(challengeIDs(got.Contributions), []string{monday, second}) {
		t.Fatalf("Record() = %+v, want created, counting in both challenges", got)
	}
}

// distanceCategory gives challenge id a title and its cumulative distance
// category with factors.
func (r recording) distanceCategory(id, title, factors string) {
	r.t.Helper()
	if _, err := r.pool.Exec(r.t.Context(), "UPDATE challenges SET title = $1 WHERE id = $2", title, id); err != nil {
		r.t.Fatal(err)
	}
	if _, err := r.pool.Exec(r.t.Context(), `INSERT INTO categories (challenge_id, type, position, params)
		VALUES ($1, 'cumulative_distance', 2, jsonb_build_object('factors', $2::jsonb))`, id, factors); err != nil {
		r.t.Fatal(err)
	}
}

// challengeIDs are the challenges of contributions, nil for none.
func challengeIDs(contributions []activity.Contribution) []string {
	var ids []string
	for _, c := range contributions {
		ids = append(ids, c.ChallengeID)
	}
	return ids
}

// TestActivityContributions: a ride counting in several challenges brings
// each its distance weighted by the factor of that challenge, and its
// duration; a challenge without a distance category gets the duration only;
// a deleted challenge never appears. The challenge listing shows that
// challenge alone.
func TestActivityContributions(t *testing.T) {
	r := newRecording(t)
	first := r.challenge([3]int{2026, 6, 1}, 3, "active", false)
	r.distanceCategory(first, "Défi 100 km", `{"run": 1, "ride": 0.3}`)
	second := r.challenge([3]int{2026, 6, 8}, 2, "active", false)
	r.distanceCategory(second, "Vélo d'été", `{"run": 1, "ride": 1}`)
	deleted := r.challenge([3]int{2026, 6, 1}, 3, "active", true)
	r.distanceCategory(deleted, "Supprimé", `{"ride": 1}`)
	// Without a distance category (the fixtures of the lock tests have
	// none either): the duration only. From Tuesday: listed second.
	bare := r.challenge([3]int{2026, 6, 2}, 2, "active", false)

	d := 20001
	rec, err := r.activities.Record(t.Context(), r.user, nil, activity.Draft{SportCode: "ride", StartedAt: toronto(t, 2026, 6, 10, 7), DurationSeconds: 3600, DistanceMeters: &d})
	if err != nil {
		t.Fatal(err)
	}
	m := func(v int) *int { return &v }
	want := []activity.Contribution{
		// 20001 × 0.3 = 6000.3: to the meter, as the ranking.
		{ChallengeID: first, ChallengeTitle: "Défi 100 km", DistanceMeters: m(6000), DurationSeconds: 3600},
		{ChallengeID: bare, ChallengeTitle: "Spring", DurationSeconds: 3600},
		{ChallengeID: second, ChallengeTitle: "Vélo d'été", DistanceMeters: m(20001), DurationSeconds: 3600},
	}
	if !reflect.DeepEqual(rec.Contributions, want) {
		t.Fatalf("Record() contributions %+v, want %+v", rec.Contributions, want)
	}

	mine, err := r.activities.Mine(t.Context(), r.user, activity.Period{}, 20, nil)
	if err != nil || len(mine.Items) != 1 || !reflect.DeepEqual(mine.Items[0].Contributions, want) {
		t.Fatalf("Mine() = %+v, %v, want contributions %+v", mine, err, want)
	}
	page, err := r.activities.OfParticipant(t.Context(), r.user, first, r.user, 20, nil)
	if err != nil || len(page.Items) != 1 || !reflect.DeepEqual(page.Items[0].Contributions, want[:1]) {
		t.Fatalf("OfParticipant() = %+v, %v, want contributions %+v", page, err, want[:1])
	}
}

func TestRecordWeekLocked(t *testing.T) {
	r := newRecording(t)
	r.challenge([3]int{2026, 6, 1}, 3, "active", false)
	r.challenge([3]int{2026, 4, 27}, 2, "active", false) // finished on 2026-05-11

	for name, startedAt := range map[string]time.Time{
		"week ended":                      toronto(t, 2026, 6, 3, 7),
		"window of a finished challenge":  toronto(t, 2026, 5, 2, 7),
		"last second of the ended week 1": toronto(t, 2026, 6, 8, 0).Add(-time.Second),
	} {
		_, err := r.record(startedAt)
		var appErr *apperr.Error
		if !errors.As(err, &appErr) || appErr.Code != apperr.CodeWeekLocked {
			t.Fatalf("%s: error = %v, want week_locked", name, err)
		}
	}
	if r.count() != 0 {
		t.Fatalf("%d rows, want none", r.count())
	}
}

func TestRecordCountsNowhere(t *testing.T) {
	r := newRecording(t)
	// Week 1 ended in all three, but the user no longer competes in the
	// first (a creator who abandoned), the second is deleted and the third
	// was never joined.
	r.challenge([3]int{2026, 5, 25}, 3, "abandoned", false)
	r.challenge([3]int{2026, 5, 25}, 3, "active", true)
	r.challenge([3]int{2026, 5, 25}, 3, "", false)

	for name, startedAt := range map[string]time.Time{
		"outside every window":                   toronto(t, 2026, 1, 10, 7),
		"abandoned, deleted and not joined only": toronto(t, 2026, 5, 27, 7),
	} {
		got, err := r.record(startedAt)
		if err != nil || !got.Created || len(got.Contributions) != 0 {
			t.Fatalf("%s: %+v, %v, want recorded, counting nowhere", name, got, err)
		}
	}
	if r.count() != 2 {
		t.Fatalf("%d rows, want 2", r.count())
	}
}

// TestChangeActivityLockedInAnyChallenge: an activity counting in two
// challenges is locked as soon as its week has ended in one of them, for a
// move as for a deletion.
func TestChangeActivityLockedInAnyChallenge(t *testing.T) {
	r := newRecording(t)
	first := r.challenge([3]int{2026, 6, 1}, 3, "active", false)  // week 2 at recordNow
	second := r.challenge([3]int{2026, 6, 4}, 2, "active", false) // weeks from Thursday: week 1 ended on 2026-06-11
	d := 5000
	run := activity.Draft{SportCode: "run", DurationSeconds: 1500, DistanceMeters: &d}

	rec, err := r.record(toronto(t, 2026, 6, 11, 7)) // week 2 of both
	if err != nil || !slices.Equal(challengeIDs(rec.Contributions), []string{first, second}) {
		t.Fatalf("Record() = %+v, %v, want it counting in both challenges", rec, err)
	}
	// Into week 1 of the second, ended; week 2 of the first, not ended.
	run.StartedAt = toronto(t, 2026, 6, 9, 7)
	_, err = r.activities.Update(t.Context(), r.user, rec.ID, run)
	wantLocked(t, err)

	// Recorded in that week before it ended: locked for a move and a deletion.
	var locked string
	if err := r.pool.QueryRow(t.Context(), `INSERT INTO activities (user_id, sport_code, started_at, duration_seconds, distance_meters)
		VALUES ($1, 'run', $2, 1500, 5000) RETURNING id`, r.user, toronto(t, 2026, 6, 9, 7)).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	run.StartedAt = toronto(t, 2026, 6, 11, 9)
	_, err = r.activities.Update(t.Context(), r.user, locked, run)
	wantLocked(t, err)
	wantLocked(t, r.activities.Delete(t.Context(), r.user, locked))

	got, err := r.activities.Update(t.Context(), r.user, rec.ID, run)
	if err != nil || !slices.Equal(challengeIDs(got.Contributions), []string{first, second}) {
		t.Fatalf("Update() = %+v, %v, want it counting in both challenges", got, err)
	}
	if err := r.activities.Delete(t.Context(), r.user, rec.ID); err != nil {
		t.Fatal(err)
	}
	var live []string
	rows, err := r.pool.Query(t.Context(), "SELECT id FROM activities WHERE user_id = $1 AND deleted_at IS NULL", r.user)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		live = append(live, id)
	}
	if rows.Err() != nil || !slices.Equal(live, []string{locked}) {
		t.Fatalf("live activities %v, %v, want only the locked one", live, rows.Err())
	}
}

func wantLocked(t *testing.T, err error) {
	t.Helper()
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != apperr.CodeWeekLocked {
		t.Fatalf("error = %v, want week_locked", err)
	}
}

// newUser inserts a user with a complete profile.
func (r recording) newUser(pseudo string) string {
	r.t.Helper()
	var id string
	if err := r.pool.QueryRow(r.t.Context(), "INSERT INTO users (auth_subject, pseudo, first_name, last_name) VALUES ($1, $2, 'Test', 'User') RETURNING id", "email|"+pseudo, pseudo).Scan(&id); err != nil {
		r.t.Fatal(err)
	}
	return id
}

// join makes userID a participant of challengeID with status.
func (r recording) join(challengeID, userID, status string) {
	r.t.Helper()
	var abandonedAt *time.Time
	if status == string(challenge.ParticipantAbandoned) {
		abandonedAt = &recordNow
	}
	if _, err := r.pool.Exec(r.t.Context(), "INSERT INTO participants VALUES ($1, $2, $3, $4, $5)", challengeID, userID, status, recordNow, abandonedAt); err != nil {
		r.t.Fatal(err)
	}
}

// TestListParticipantActivitiesVisibility follows the rights matrix of
// README §5.7 end to end: the activities of a participant are shown to the
// participants of the challenge, abandoned ones included, and its creators;
// a non-participant of a public challenge sees the leaderboard only, and a
// private challenge stays hidden.
func TestListParticipantActivitiesVisibility(t *testing.T) {
	r := newRecording(t)
	public := r.challenge([3]int{2026, 6, 1}, 3, "active", false)
	private := r.challenge([3]int{2026, 6, 1}, 3, "active", false)
	deleted := r.challenge([3]int{2026, 6, 1}, 3, "active", true)
	if _, err := r.pool.Exec(t.Context(), "UPDATE challenges SET visibility = 'private' WHERE id = $1", private); err != nil {
		t.Fatal(err)
	}
	runner, quitter, outsider := r.newUser("runner2"), r.newUser("quitter"), r.newUser("outsider")
	for _, c := range []string{public, private} {
		r.join(c, runner, "active")
		r.join(c, quitter, "abandoned")
	}
	// The creator's activities: one in the window, one before it.
	in, err := r.record(toronto(t, 2026, 6, 10, 7))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.record(toronto(t, 2026, 5, 20, 7)); err != nil {
		t.Fatal(err)
	}
	// Joined afterwards: another challenge of the creator, from Thursday
	// 2026-06-04, whose week 1 (holding the activity) ended on 2026-06-11.
	// It locks the activity for its author but never shows through here.
	r.challenge([3]int{2026, 6, 4}, 3, "active", false)
	mine, err := r.activities.Mine(t.Context(), r.user, activity.Period{}, 1, nil)
	if err != nil || len(mine.Items) != 1 || mine.Items[0].ID != in.ID || !mine.Items[0].Locked {
		t.Fatalf("Mine() = %+v, %v, want the activity locked for its author", mine, err)
	}

	tests := []struct {
		name      string
		actor     string
		challenge string
		target    string
		want      apperr.Code // empty: the activity in the window
	}{
		{name: "participant, private", actor: runner, challenge: private, target: r.user},
		{name: "abandoned participant, private", actor: quitter, challenge: private, target: r.user},
		{name: "creator reads an abandoned participant", actor: r.user, challenge: private, target: quitter},
		{name: "own activities", actor: r.user, challenge: public, target: r.user},
		{name: "non-participant of a public challenge", actor: outsider, challenge: public, target: r.user, want: apperr.CodeNotFound},
		{name: "non-participant of a private challenge", actor: outsider, challenge: private, target: r.user, want: apperr.CodeChallengeNotFound},
		{name: "target who never joined", actor: runner, challenge: public, target: outsider, want: apperr.CodeNotFound},
		{name: "deleted challenge", actor: r.user, challenge: deleted, target: r.user, want: apperr.CodeChallengeNotFound},
		{name: "missing challenge", actor: runner, challenge: "4b3a2c1d-0e9f-4a8b-9c7d-6e5f4a3b2c1d", target: r.user, want: apperr.CodeChallengeNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, err := r.activities.OfParticipant(t.Context(), tt.actor, tt.challenge, tt.target, 20, nil)
			if tt.want != "" {
				var appErr *apperr.Error
				if !errors.As(err, &appErr) || appErr.Code != tt.want {
					t.Fatalf("error = %v, want %s", err, tt.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tt.target != r.user {
				if len(page.Items) != 0 {
					t.Fatalf("items %+v, want none: the target recorded nothing", page.Items)
				}
				return
			}
			// The window only; this challenge only, for where it counts and
			// whether it is locked.
			if len(page.Items) != 1 || page.Items[0].ID != in.ID || !slices.Equal(challengeIDs(page.Items[0].Contributions), []string{tt.challenge}) || page.Items[0].Locked {
				t.Fatalf("items %+v, want the activity in the window, counting in %s only, unlocked there", page.Items, tt.challenge)
			}
		})
	}
}

// TestListActivitiesOfAnAbandonedParticipant pins the decision of SC-API-080
// (README §5.7): the activities of a participant who abandoned stay visible to
// the participants of the challenge, within its window, both before and after
// the abandonment (those no longer count there); an activity outside the
// window never shows. An exclusion is stored as an abandonment (status and
// abandoned_at, abandonParticipation), so it is covered too.
func TestListActivitiesOfAnAbandonedParticipant(t *testing.T) {
	r := newRecording(t)
	c := r.challenge([3]int{2026, 6, 1}, 3, "abandoned", false)
	// The creator abandoned on 2026-06-08, in the middle of the window.
	if _, err := r.pool.Exec(t.Context(), "UPDATE participants SET abandoned_at = $1 WHERE challenge_id = $2 AND user_id = $3",
		toronto(t, 2026, 6, 8, 12), c, r.user); err != nil {
		t.Fatal(err)
	}
	runner := r.newUser("runner3")
	r.join(c, runner, "active")
	var want []string
	for _, at := range []time.Time{toronto(t, 2026, 6, 10, 7), toronto(t, 2026, 6, 3, 7)} { // after, then before the abandonment
		recorded, err := r.record(at)
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, recorded.ID)
	}
	if _, err := r.record(toronto(t, 2026, 5, 20, 7)); err != nil { // before the window
		t.Fatal(err)
	}
	page, err := r.activities.OfParticipant(t.Context(), runner, c, r.user, 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, item := range page.Items {
		if len(item.Contributions) != 0 {
			t.Fatalf("activity %s counts in %v, want nowhere: its author abandoned", item.ID, challengeIDs(item.Contributions))
		}
		got = append(got, item.ID)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("activities %v, want %v: after and before the abandonment, most recent first, not the one outside the window", got, want)
	}
}

func TestListMyActivities(t *testing.T) {
	r := newRecording(t)
	c := r.challenge([3]int{2026, 6, 1}, 3, "active", false)
	week2, err := r.record(toronto(t, 2026, 6, 10, 7))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(t.Context(), `INSERT INTO activities (user_id, sport_code, started_at, duration_seconds, distance_meters)
		VALUES ($1, 'run', $2, 1500, 5000)`, r.user, toronto(t, 2026, 6, 3, 7)); err != nil {
		t.Fatal(err)
	}

	page, err := r.activities.Mine(t.Context(), r.user, activity.Period{}, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != week2.ID || page.Items[0].Locked || !slices.Equal(challengeIDs(page.Items[0].Contributions), []string{c}) || page.Next == nil {
		t.Fatalf("first page %+v, want the run of week 2, unlocked, and a next page", page)
	}
	page, err = r.activities.Mine(t.Context(), r.user, activity.Period{}, 1, page.Next)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || !page.Items[0].Locked || page.Next != nil {
		t.Fatalf("second page %+v, want the run of week 1, locked, and no next page", page)
	}
}
