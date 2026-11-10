//go:build integration

package main

import (
	"errors"
	"io"
	"log/slog"
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
	challenges := challenge.NewService(challenge.NewRepository(gdb), users, sports, clk)
	activities := activity.NewService(activity.NewRepository(gdb), users, sports, participations{challenges}, clk)

	var id string
	if err := pool.QueryRow(t.Context(), "INSERT INTO users (auth_subject, pseudo) VALUES ('email|runner', 'runner') RETURNING id").Scan(&id); err != nil {
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
	if !got.Created || !slices.Equal(got.ChallengeIDs, []string{monday, second}) {
		t.Fatalf("Record() = %+v, want created, counting in both challenges", got)
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
		if err != nil || !got.Created || len(got.ChallengeIDs) != 0 {
			t.Fatalf("%s: %+v, %v, want recorded, counting nowhere", name, got, err)
		}
	}
	if r.count() != 2 {
		t.Fatalf("%d rows, want 2", r.count())
	}
}
