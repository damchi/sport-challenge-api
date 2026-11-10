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

	"github.com/damchi/sport-challenge-api/internal/challenge"
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/platform/db/testdb"
	"github.com/damchi/sport-challenge-api/internal/sport"
)

type sqlProfiles struct{ pool *pgxpool.Pool }

func (p sqlProfiles) ProfileComplete(ctx context.Context, id string) (bool, error) {
	var complete bool
	err := p.pool.QueryRow(ctx, "SELECT pseudo IS NOT NULL FROM users WHERE id = $1", id).Scan(&complete)
	return complete, err
}

// memberships maps the challenges of module challenge as cmd/api does.
type memberships struct{ repo *challenge.Repository }

func (m memberships) Participations(ctx context.Context, userID string) ([]Participation, error) {
	ms, err := m.repo.Memberships(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]Participation, 0, len(ms))
	for _, c := range ms {
		loc, err := time.LoadLocation(c.Timezone)
		if err != nil {
			return nil, err
		}
		out = append(out, Participation{ChallengeID: c.ID, StartsAt: c.StartsAt, EndsAt: c.EndsAt, Location: loc, Active: c.Participant == challenge.ParticipantActive})
	}
	return out, nil
}

type fixture struct {
	t    *testing.T
	pool *pgxpool.Pool
	svc  *Service
	user string
}

// setupRecord opens a database with a user whose profile is complete, at
// recordNow.
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
	svc := NewService(NewRepository(gdb), sqlProfiles{pool}, sport.NewService(sport.NewRepository(gdb)),
		memberships{challenge.NewRepository(gdb)}, clock.NewFake(recordNow))
	return fixture{t: t, pool: pool, svc: svc, user: user}
}

// challenge inserts a challenge of weeks weeks from local midnight of day in
// Toronto, created by the user, who joined it with status.
func (f fixture) challenge(day [3]int, weeks int, status string, deleted bool) string {
	f.t.Helper()
	start := toronto(f.t, day[0], day[1], day[2], 0)
	loc, _ := time.LoadLocation("America/Toronto")
	end := start.In(loc).AddDate(0, 0, 7*weeks).UTC()
	var deletedAt *time.Time
	if deleted {
		deletedAt = &recordNow
	}
	var id string
	err := f.pool.QueryRow(f.t.Context(), `INSERT INTO challenges (title, visibility, timezone, starts_at, ends_at, created_by, deleted_at)
		VALUES ('Spring', 'public', 'America/Toronto', $1, $2, $3, $4) RETURNING id`, start, end, f.user, deletedAt).Scan(&id)
	if err != nil {
		f.t.Fatal(err)
	}
	var abandonedAt *time.Time
	if status == "abandoned" {
		abandonedAt = &start
	}
	if _, err := f.pool.Exec(f.t.Context(), "INSERT INTO challenge_creators (challenge_id, user_id) VALUES ($1, $2)", id, f.user); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.t.Context(), "INSERT INTO participants VALUES ($1, $2, $3, $4, $5)", id, f.user, status, start, abandonedAt); err != nil {
		f.t.Fatal(err)
	}
	return id
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
	monday := f.challenge([3]int{2026, 6, 1}, 3, "active", false) // in week 2 at recordNow
	second := f.challenge([3]int{2026, 6, 8}, 2, "active", false) // in week 1 at recordNow
	key := "1f0c6e2a-5b7d-4c9e-8a1b-3d5f7e9a0c2b"

	got, err := f.svc.Record(t.Context(), f.user, &key, run(toronto(t, 2026, 6, 10, 7)))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Created || !slices.Equal(got.ChallengeIDs, []string{monday, second}) {
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
	again, err := f.svc.Record(t.Context(), f.user, &key, run(toronto(t, 2026, 6, 10, 9)))
	if err != nil {
		t.Fatal(err)
	}
	if again.Created || again.ID != got.ID || !again.StartedAt.Equal(got.StartedAt) || !slices.Equal(again.ChallengeIDs, got.ChallengeIDs) || f.count() != 1 {
		t.Fatalf("again %+v, %d rows, want the first activity and one row", again, f.count())
	}

	// Without a key, the same body is another activity.
	if _, err := f.svc.Record(t.Context(), f.user, nil, run(toronto(t, 2026, 6, 10, 7))); err != nil || f.count() != 2 {
		t.Fatalf("error %v, %d rows, want a second activity", err, f.count())
	}
}

// TestRecordSameKeyConcurrently: requests racing with one key store one
// activity and all answer it.
func TestRecordSameKeyConcurrently(t *testing.T) {
	f := setupRecord(t)
	key := "race"
	const n = 8
	ids := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			got, err := f.svc.Record(t.Context(), f.user, &key, run(toronto(t, 2026, 6, 10, 7)))
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

func TestRecordWeekLocked(t *testing.T) {
	f := setupRecord(t)
	f.challenge([3]int{2026, 6, 1}, 3, "active", false)
	f.challenge([3]int{2026, 4, 27}, 2, "active", false) // finished on 2026-05-11

	for name, startedAt := range map[string]time.Time{
		"week ended":                      toronto(t, 2026, 6, 3, 7),
		"window of a finished challenge":  toronto(t, 2026, 5, 2, 7),
		"last second of the ended week 1": toronto(t, 2026, 6, 8, 0).Add(-time.Second),
	} {
		_, err := f.svc.Record(t.Context(), f.user, nil, run(startedAt))
		var appErr *apperr.Error
		if !errors.As(err, &appErr) || appErr.Code != apperr.CodeWeekLocked {
			t.Fatalf("%s: error = %v, want week_locked", name, err)
		}
	}
	if f.count() != 0 {
		t.Fatalf("%d rows, want none", f.count())
	}
}

func TestRecordCountsNowhere(t *testing.T) {
	f := setupRecord(t)
	// Week 1 ended in all three, but the user no longer competes in the
	// first (a creator who abandoned), the second is deleted and the third
	// was never joined.
	f.challenge([3]int{2026, 5, 25}, 3, "abandoned", false)
	f.challenge([3]int{2026, 5, 25}, 3, "active", true)
	// A public challenge the user never joined.
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO challenges (title, visibility, timezone, starts_at, ends_at, created_by)
		VALUES ('Open', 'public', 'America/Toronto', $1, $2, $3)`, toronto(t, 2026, 5, 25, 0), toronto(t, 2026, 6, 15, 0), f.user); err != nil {
		t.Fatal(err)
	}

	for name, startedAt := range map[string]time.Time{
		"outside every window":                   toronto(t, 2026, 1, 10, 7),
		"abandoned, deleted and not joined only": toronto(t, 2026, 5, 27, 7),
	} {
		got, err := f.svc.Record(t.Context(), f.user, nil, run(startedAt))
		if err != nil || !got.Created || len(got.ChallengeIDs) != 0 {
			t.Fatalf("%s: %+v, %v, want recorded, counting nowhere", name, got, err)
		}
	}
	if f.count() != 2 {
		t.Fatalf("%d rows, want 2", f.count())
	}
}

func TestRecordOtherSport(t *testing.T) {
	f := setupRecord(t)
	monday := f.challenge([3]int{2026, 6, 1}, 3, "active", false)
	label := " Stand  up paddle "
	got, err := f.svc.Record(t.Context(), f.user, nil, Draft{SportCode: "other", SportLabel: &label, StartedAt: toronto(t, 2026, 6, 10, 7), DurationSeconds: 2700})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.ChallengeIDs, []string{monday}) || got.DistanceMeters != nil {
		t.Fatalf("Record() = %+v, want counting in the challenge, without distance", got)
	}
	var stored string
	if err := f.pool.QueryRow(t.Context(), "SELECT sport_label FROM activities WHERE id = $1", got.ID).Scan(&stored); err != nil || stored != "Stand up paddle" {
		t.Fatalf("stored label %q, %v, want it cleaned", stored, err)
	}
}

func TestRecordAccountDeleted(t *testing.T) {
	f := setupRecord(t)
	// Anonymized between the profile check and the write: the lock refuses.
	if _, err := f.pool.Exec(t.Context(), "UPDATE users SET anonymized_at = now() WHERE id = $1", f.user); err != nil {
		t.Fatal(err)
	}
	_, err := f.svc.Record(t.Context(), f.user, nil, run(toronto(t, 2026, 6, 10, 7)))
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != apperr.CodeAccountDeleted || f.count() != 0 {
		t.Fatalf("error = %v, %d rows, want account_deleted and nothing written", err, f.count())
	}
}

// TestRecordKeyOfAnotherUser: a key is the user's own; another user sending
// it records their own activity and never reads the first one.
func TestRecordKeyOfAnotherUser(t *testing.T) {
	f := setupRecord(t)
	key := "shared-key"
	first, err := f.svc.Record(t.Context(), f.user, &key, run(toronto(t, 2026, 6, 10, 7)))
	if err != nil {
		t.Fatal(err)
	}
	var other string
	if err := f.pool.QueryRow(t.Context(), "INSERT INTO users (auth_subject, pseudo) VALUES ('email|other', 'other') RETURNING id").Scan(&other); err != nil {
		t.Fatal(err)
	}
	got, err := f.svc.Record(t.Context(), other, &key, run(toronto(t, 2026, 6, 10, 9)))
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
