//go:build integration

package activity

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"gorm.io/gorm"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/platform/db/testdb"
)

// These tests cover the storage of module activity with fakes of the other
// modules; cmd/api runs the recording end to end, with the real
// participations.

// staticParticipations gives the same challenges to every user.
type staticParticipations []Participation

func (s staticParticipations) Participations(context.Context, string) ([]Participation, error) {
	return s, nil
}

type fixture struct {
	t    *testing.T
	pool *pgxpool.Pool
	gdb  *gorm.DB
	user string
}

// setupRecord opens a database with a user whose profile is complete.
func setupRecord(t *testing.T) fixture {
	t.Helper()
	pool := testdb.New(t)
	gdb, err := db.NewGorm(pool, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	var user string
	if err := pool.QueryRow(t.Context(), "INSERT INTO users (auth_subject, pseudo) VALUES ('email|runner', 'runner') RETURNING id").Scan(&user); err != nil {
		t.Fatal(err)
	}
	return fixture{t: t, pool: pool, gdb: gdb, user: user}
}

// service records at recordNow, the user taking part in challenges.
func (f fixture) service(challenges ...Participation) *Service {
	return NewService(NewRepository(f.gdb), fakeProfiles{complete: true}, catalogue, staticParticipations(challenges), nil, clock.NewFake(recordNow))
}

func (f fixture) count() int {
	f.t.Helper()
	var n int
	if err := f.pool.QueryRow(f.t.Context(), "SELECT count(*) FROM activities WHERE user_id = $1", f.user).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func TestRecordStoresTheActivity(t *testing.T) {
	f := setupRecord(t)
	svc := f.service(challengeOf(t, "c1"), challengeOf(t, "c2"))
	key := "1f0c6e2a-5b7d-4c9e-8a1b-3d5f7e9a0c2b"

	got, err := svc.Record(t.Context(), f.user, &key, run(toronto(t, 2026, 6, 10, 7)))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Created || !slices.Equal(challengeIDs(got.Contributions), []string{"c1", "c2"}) {
		t.Fatalf("Record() = %+v, want created, counting in both challenges", got)
	}
	var source string
	var externalID, storedKey *string
	var distance *int
	var startedAt time.Time
	err = f.pool.QueryRow(t.Context(), "SELECT source, external_id, idempotency_key, distance_meters, started_at FROM activities WHERE id = $1", got.ID).
		Scan(&source, &externalID, &storedKey, &distance, &startedAt)
	if err != nil {
		t.Fatal(err)
	}
	if source != SourceManual || externalID != nil || storedKey == nil || *storedKey != key || distance == nil || *distance != 5000 || !startedAt.Equal(got.StartedAt) {
		t.Fatalf("stored %s %v %v %v %s, want manual, no external id, the key, 5000 m", source, externalID, storedKey, distance, startedAt)
	}

	// The same key again: the same activity, no duplicate.
	again, err := svc.Record(t.Context(), f.user, &key, run(toronto(t, 2026, 6, 10, 9)))
	if err != nil {
		t.Fatal(err)
	}
	if again.Created || again.ID != got.ID || !again.StartedAt.Equal(got.StartedAt) || !slices.Equal(challengeIDs(again.Contributions), challengeIDs(got.Contributions)) || f.count() != 1 {
		t.Fatalf("again %+v, %d rows, want the first activity and one row", again, f.count())
	}

	// Without a key, the same body would be another activity, at the same
	// time: an overlap, refused (SC-API-084). The one right after is another.
	_, err = svc.Record(t.Context(), f.user, nil, run(toronto(t, 2026, 6, 10, 7)))
	wantCode(t, err, apperr.CodeActivityOverlap)
	if _, err := svc.Record(t.Context(), f.user, nil, run(got.StartedAt.Add(time.Duration(got.DurationSeconds)*time.Second))); err != nil || f.count() != 2 {
		t.Fatalf("error %v, %d rows, want a second activity", err, f.count())
	}
}

// TestRecordSameKeyConcurrently: requests racing with one key store one
// activity and all answer it.
func TestRecordSameKeyConcurrently(t *testing.T) {
	f := setupRecord(t)
	svc := f.service()
	key := "race"
	const n = 8
	ids := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			got, err := svc.Record(t.Context(), f.user, &key, run(toronto(t, 2026, 6, 10, 7)))
			ids[i], errs[i] = got.ID, err
		})
	}
	wg.Wait()
	for i := range n {
		if errs[i] != nil || ids[i] != ids[0] {
			t.Fatalf("request %d: %q, %v, want %q", i, ids[i], errs[i], ids[0])
		}
	}
	if f.count() != 1 {
		t.Fatalf("%d rows, want 1", f.count())
	}
}

// TestRecordWeekLocked: the lock refuses in the transaction, which writes
// nothing.
func TestRecordWeekLocked(t *testing.T) {
	f := setupRecord(t)
	_, err := f.service(challengeOf(t, "c1")).Record(t.Context(), f.user, nil, run(toronto(t, 2026, 6, 3, 7)))
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != apperr.CodeWeekLocked || f.count() != 0 {
		t.Fatalf("error = %v, %d rows, want week_locked and nothing written", err, f.count())
	}
}

// TestRecordIntoEndedChallengeWithoutTheWorker: the clock locks the last week
// of a challenge that is over; no worker has run (nothing is frozen), and
// nothing is written.
func TestRecordIntoEndedChallengeWithoutTheWorker(t *testing.T) {
	f := setupRecord(t)
	ended := NewService(NewRepository(f.gdb), fakeProfiles{complete: true}, catalogue, staticParticipations{challengeOf(t, "c1")}, nil, clock.NewFake(toronto(t, 2026, 6, 22, 0)))
	_, err := ended.Record(t.Context(), f.user, nil, run(toronto(t, 2026, 6, 21, 23)))
	wantCode(t, err, apperr.CodeWeekLocked)
	if f.count() != 0 {
		t.Fatalf("%d rows, want nothing written", f.count())
	}
}

func TestRecordOtherSport(t *testing.T) {
	f := setupRecord(t)
	label := " Stand  up paddle "
	got, err := f.service().Record(t.Context(), f.user, nil, Draft{SportCode: "other", SportLabel: &label, StartedAt: toronto(t, 2026, 6, 10, 7), DurationSeconds: 2700})
	if err != nil {
		t.Fatal(err)
	}
	if got.DistanceMeters != nil {
		t.Fatalf("Record() = %+v, want no distance", got)
	}
	var stored string
	if err := f.pool.QueryRow(t.Context(), "SELECT sport_label FROM activities WHERE id = $1", got.ID).Scan(&stored); err != nil || stored != "Stand up paddle" {
		t.Fatalf("stored label %q, %v, want it cleaned", stored, err)
	}
}

func TestRecordAccountDeleted(t *testing.T) {
	f := setupRecord(t)
	// Anonymized after the profile check (complete in the fake): the lock
	// of the write refuses.
	if _, err := f.pool.Exec(t.Context(), "UPDATE users SET anonymized_at = now() WHERE id = $1", f.user); err != nil {
		t.Fatal(err)
	}
	_, err := f.service().Record(t.Context(), f.user, nil, run(toronto(t, 2026, 6, 10, 7)))
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != apperr.CodeAccountDeleted || f.count() != 0 {
		t.Fatalf("error = %v, %d rows, want account_deleted and nothing written", err, f.count())
	}
}

// TestRecordKeyOfAnotherUser: a key is the user's own; another user sending
// it records their own activity and never reads the first one.
func TestRecordKeyOfAnotherUser(t *testing.T) {
	f := setupRecord(t)
	svc := f.service()
	key := "shared-key"
	first, err := svc.Record(t.Context(), f.user, &key, run(toronto(t, 2026, 6, 10, 7)))
	if err != nil {
		t.Fatal(err)
	}
	var other string
	if err := f.pool.QueryRow(t.Context(), "INSERT INTO users (auth_subject, pseudo) VALUES ('email|other', 'other') RETURNING id").Scan(&other); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Record(t.Context(), other, &key, run(toronto(t, 2026, 6, 10, 9)))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Created || got.ID == first.ID || got.UserID != other {
		t.Fatalf("Record() = %+v, want a new activity of the other user", got)
	}
	var owner string
	var startedAt time.Time
	if err := f.pool.QueryRow(t.Context(), "SELECT user_id, started_at FROM activities WHERE id = $1", first.ID).Scan(&owner, &startedAt); err != nil || owner != f.user || !startedAt.Equal(first.StartedAt) {
		t.Fatalf("first activity %s %s, %v, want it unchanged", owner, startedAt, err)
	}
}
