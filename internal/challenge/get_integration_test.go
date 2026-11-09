//go:build integration

package challenge

import (
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
)

func TestGetReadsTheChallengeAndMyPlace(t *testing.T) {
	pseudo := "marie_92"
	svc, pool, creatorID := setup(t, &pseudo)
	c, err := svc.Create(t.Context(), creatorID, with(func(d *Draft) { d.Visibility = VisibilityPrivate }))
	if err != nil {
		t.Fatal(err)
	}

	// The creator sees it, categories in position order.
	d, err := svc.Get(t.Context(), creatorID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Role != RoleCreator || d.Participant != ParticipantActive || len(d.Categories) != 3 {
		t.Fatalf("detail %+v, want the creator, active, with 3 categories", d)
	}
	for i, cat := range d.Categories {
		if cat.Position != i+1 {
			t.Fatalf("category %d at position %d, want them in order", i, cat.Position)
		}
	}
	if d.Status != StatusOngoing || d.CurrentWeek != 1 || d.CanJoin {
		t.Fatalf("detail %+v, want ongoing, week 1, cannot join again", d)
	}

	// An abandoned participant still sees the private challenge.
	other := insertTestUser(t, pool, "email|abandoned")
	if _, err := pool.Exec(t.Context(), "INSERT INTO participants VALUES ($1, $2, 'abandoned', $3, $3)", c.ID, other, now); err != nil {
		t.Fatal(err)
	}
	if d, err := svc.Get(t.Context(), other, c.ID); err != nil || d.Role != RoleAbandoned {
		t.Fatalf("got %+v, %v, want the abandoned role", d.Role, err)
	}
}

// TestGetNotFoundIsTheSameForEveryCase: missing, deleted and private
// without access answer the very same error.
func TestGetNotFoundIsTheSameForEveryCase(t *testing.T) {
	pseudo := "marie_92"
	svc, pool, creatorID := setup(t, &pseudo)
	private, err := svc.Create(t.Context(), creatorID, with(func(d *Draft) { d.Visibility = VisibilityPrivate }))
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := svc.Create(t.Context(), creatorID, valid())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE challenges SET deleted_at = now() WHERE id = $1", deleted.ID); err != nil {
		t.Fatal(err)
	}
	outsider := insertTestUser(t, pool, "email|outsider")

	cases := map[string]struct{ actor, id string }{
		"missing":                      {outsider, "00000000-0000-0000-0000-000000000000"},
		"deleted, even to its creator": {creatorID, deleted.ID},
		"private without access":       {outsider, private.ID},
	}
	var first *apperr.Error
	for name, tc := range cases {
		_, err := svc.Get(t.Context(), tc.actor, tc.id)
		var appErr *apperr.Error
		if !errors.As(err, &appErr) || appErr.Code != apperr.CodeChallengeNotFound {
			t.Fatalf("%s: error = %v, want challenge_not_found", name, err)
		}
		if first == nil {
			first = appErr
		}
		if appErr.Status != first.Status || appErr.Message != first.Message || appErr.Params != nil {
			t.Fatalf("%s: %+v differs from %+v", name, appErr, first)
		}
	}
}

// TestGetRegistrationDeadline reads the detail one second before and at the
// exact end of week 1, with the real storage.
func TestGetRegistrationDeadline(t *testing.T) {
	pseudo := "marie_92"
	svc, pool, creatorID := setup(t, &pseudo)
	c, err := svc.Create(t.Context(), creatorID, valid()) // from 2026-03-04, Toronto
	if err != nil {
		t.Fatal(err)
	}
	outsider := insertTestUser(t, pool, "email|late")
	deadline := time.Date(2026, 3, 11, 4, 0, 0, 0, time.UTC) // local midnight, after the time change

	gdb, err := db.NewGorm(pool, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(deadline.Add(-time.Second))
	reader := NewService(NewRepository(gdb), nil, nil, clk)

	d, err := reader.Get(t.Context(), outsider, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !d.CanJoin || d.CurrentWeek != 1 || !d.RegistrationDeadline.Equal(deadline) || !d.WeekEndsAt.Equal(deadline) {
		t.Fatalf("one second before: %+v, want can join in week 1 ending %s", d, deadline)
	}

	clk.Set(deadline)
	if d, err = reader.Get(t.Context(), outsider, c.ID); err != nil {
		t.Fatal(err)
	}
	if d.CanJoin || d.CurrentWeek != 2 {
		t.Fatalf("at the deadline: can join %v, week %d, want registration closed in week 2", d.CanJoin, d.CurrentWeek)
	}
}

func TestUpdateTextStoresTheChange(t *testing.T) {
	pseudo := "marie_92"
	svc, pool, creatorID := setup(t, &pseudo)
	desc := "Old description"
	c, err := svc.Create(t.Context(), creatorID, with(func(d *Draft) { d.Description = &desc }))
	if err != nil {
		t.Fatal(err)
	}

	title, empty := "  New title  ", ""
	d, err := svc.UpdateText(t.Context(), creatorID, c.ID, nil, TextChanges{Title: &title, Description: &empty})
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	var description *string
	if err := pool.QueryRow(t.Context(), "SELECT title, description FROM challenges WHERE id = $1", c.ID).Scan(&stored, &description); err != nil {
		t.Fatal(err)
	}
	if stored != "New title" || description != nil || d.Title != "New title" || d.Description != nil {
		t.Fatalf("stored %q %v, detail %q %v, want the trimmed title and no description", stored, description, d.Title, d.Description)
	}

	// A deleted challenge is never written: not found, even for its creator.
	if _, err := pool.Exec(t.Context(), "UPDATE challenges SET deleted_at = now() WHERE id = $1", c.ID); err != nil {
		t.Fatal(err)
	}
	again := "Again"
	if _, err := svc.UpdateText(t.Context(), creatorID, c.ID, nil, TextChanges{Title: &again}); !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
		t.Fatalf("error = %v, want challenge_not_found", err)
	}
	if err := pool.QueryRow(t.Context(), "SELECT title FROM challenges WHERE id = $1", c.ID).Scan(&stored); err != nil || stored != "New title" {
		t.Fatalf("title %q (%v), want the deleted challenge untouched", stored, err)
	}
}

// joinFixture is a public challenge of 6 weeks from 2026-03-04 (Toronto)
// created by a creator, and a service whose clock the test moves.
func joinFixture(t *testing.T) (svc *Service, clk *clock.FakeClock, pool *pgxpool.Pool, challengeID string) {
	t.Helper()
	pseudo := "marie_92"
	creatorSvc, pool, creatorID := setup(t, &pseudo)
	c, err := creatorSvc.Create(t.Context(), creatorID, valid())
	if err != nil {
		t.Fatal(err)
	}
	gdb, err := db.NewGorm(pool, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	clk = clock.NewFake(now)
	return NewService(NewRepository(gdb), sqlProfiles{pool}, sqlCatalogue{pool}, clk), clk, pool, c.ID
}

func insertProfile(t *testing.T, pool *pgxpool.Pool, subject string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(t.Context(), "INSERT INTO users (auth_subject, pseudo) VALUES ($1, $1) RETURNING id", subject).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestJoinAtTheEndOfWeekOne(t *testing.T) {
	svc, clk, pool, id := joinFixture(t)
	deadline := time.Date(2026, 3, 11, 4, 0, 0, 0, time.UTC) // local midnight, after the time change

	early := insertProfile(t, pool, "join_early")
	clk.Set(deadline.Add(-time.Second))
	if _, err := svc.Join(t.Context(), early, id); err != nil {
		t.Fatalf("one second before the end of week 1: %v", err)
	}
	var status string
	var joinedAt time.Time
	if err := pool.QueryRow(t.Context(), "SELECT status, joined_at FROM participants WHERE challenge_id = $1 AND user_id = $2", id, early).Scan(&status, &joinedAt); err != nil {
		t.Fatal(err)
	}
	if status != "active" || !joinedAt.Equal(deadline.Add(-time.Second)) {
		t.Fatalf("stored %s joined at %s, want active at the clock's now", status, joinedAt)
	}

	late := insertProfile(t, pool, "join_late")
	clk.Set(deadline)
	if _, err := svc.Join(t.Context(), late, id); !errors.Is(err, apperr.New(apperr.CodeRegistrationClosed, nil)) {
		t.Fatalf("at the exact end of week 1: %v, want registration_closed", err)
	}
	if n := count(t, pool, "SELECT count(*) FROM participants WHERE user_id = $1", late); n != 0 {
		t.Fatalf("%d participations stored after the deadline, want 0", n)
	}
}

func TestJoinAbandonIsFinal(t *testing.T) {
	svc, _, pool, id := joinFixture(t)
	user := insertProfile(t, pool, "join_quitter")
	if _, err := pool.Exec(t.Context(), "INSERT INTO participants VALUES ($1, $2, 'abandoned', $3, $3)", id, user, now); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Join(t.Context(), user, id); !errors.Is(err, apperr.New(apperr.CodeParticipationAbandoned, nil)) {
		t.Fatalf("error = %v, want participation_abandoned", err)
	}
}

func TestJoinRefusalsStoreNothing(t *testing.T) {
	svc, _, pool, id := joinFixture(t)
	incomplete := insertTestUser(t, pool, "join_no_pseudo")
	if _, err := svc.Join(t.Context(), incomplete, id); !errors.Is(err, apperr.New(apperr.CodeProfileIncomplete, nil)) {
		t.Fatalf("error = %v, want profile_incomplete", err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE challenges SET deleted_at = now() WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	user := insertProfile(t, pool, "join_deleted")
	if _, err := svc.Join(t.Context(), user, id); !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
		t.Fatalf("deleted challenge: %v, want challenge_not_found", err)
	}
	if n := count(t, pool, "SELECT count(*) FROM participants WHERE user_id IN ($1, $2)", incomplete, user); n != 0 {
		t.Fatalf("%d participations stored, want 0", n)
	}
}

// TestJoinRace: the same user joins from 10 requests at once; the lock on
// the challenge makes exactly one succeed and the others see them in.
func TestJoinRace(t *testing.T) {
	svc, _, pool, id := joinFixture(t)
	user := insertProfile(t, pool, "join_racer")
	const n = 10
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Go(func() {
			<-start
			_, errs[i] = svc.Join(t.Context(), user, id)
		})
	}
	close(start)
	wg.Wait()

	var joined int
	for i, err := range errs {
		switch {
		case err == nil:
			joined++
		case !errors.Is(err, apperr.New(apperr.CodeAlreadyParticipant, nil)):
			t.Fatalf("request %d: %v, want nil or already_participant", i, err)
		}
	}
	if joined != 1 {
		t.Fatalf("%d requests joined, want exactly 1", joined)
	}
	if c := count(t, pool, "SELECT count(*) FROM participants WHERE challenge_id = $1 AND user_id = $2", id, user); c != 1 {
		t.Fatalf("%d participations stored, want 1", c)
	}
}

func TestAbandonIsStoredAndFinal(t *testing.T) {
	svc, clk, pool, id := joinFixture(t)
	user := insertProfile(t, pool, "abandon_me")
	if _, err := svc.Join(t.Context(), user, id); err != nil {
		t.Fatal(err)
	}

	clk.Advance(48 * time.Hour)
	d, err := svc.Abandon(t.Context(), user, id)
	if err != nil {
		t.Fatal(err)
	}
	var status string
	var abandonedAt time.Time
	if err := pool.QueryRow(t.Context(), "SELECT status, abandoned_at FROM participants WHERE challenge_id = $1 AND user_id = $2", id, user).Scan(&status, &abandonedAt); err != nil {
		t.Fatal(err)
	}
	if status != "abandoned" || !abandonedAt.Equal(clk.Now()) || d.Role != RoleAbandoned {
		t.Fatalf("stored %s at %s, role %v, want abandoned at the clock's now", status, abandonedAt, d.Role)
	}

	// Again: idempotent, the first instant stays.
	clk.Advance(time.Hour)
	if _, err := svc.Abandon(t.Context(), user, id); err != nil {
		t.Fatal(err)
	}
	var again time.Time
	if err := pool.QueryRow(t.Context(), "SELECT abandoned_at FROM participants WHERE challenge_id = $1 AND user_id = $2", id, user).Scan(&again); err != nil || !again.Equal(abandonedAt) {
		t.Fatalf("abandoned_at %s (%v), want the first abandon kept", again, err)
	}
	// Final: joining again is refused.
	if _, err := svc.Join(t.Context(), user, id); !errors.Is(err, apperr.New(apperr.CodeParticipationAbandoned, nil)) {
		t.Fatalf("join after abandon: %v, want participation_abandoned", err)
	}
}

func TestAbandonByTheCreatorAndAfterTheEnd(t *testing.T) {
	svc, clk, pool, id := joinFixture(t)
	var creatorID string
	if err := pool.QueryRow(t.Context(), "SELECT created_by FROM challenges WHERE id = $1", id).Scan(&creatorID); err != nil {
		t.Fatal(err)
	}
	d, err := svc.Abandon(t.Context(), creatorID, id)
	if err != nil {
		t.Fatal(err)
	}
	if d.Role != RoleCreator || d.Participant != ParticipantAbandoned {
		t.Fatalf("role %v, participation %s, want still creator, abandoned", d.Role, d.Participant)
	}

	user := insertProfile(t, pool, "abandon_late")
	if _, err := svc.Join(t.Context(), user, id); err != nil {
		t.Fatal(err)
	}
	clk.Set(time.Date(2026, 4, 15, 4, 0, 0, 0, time.UTC)) // the exact end of the 6 weeks
	if _, err := svc.Abandon(t.Context(), user, id); !errors.Is(err, apperr.New(apperr.CodeChallengeEnded, nil)) {
		t.Fatalf("abandon at the end: %v, want challenge_ended", err)
	}
	var status string
	if err := pool.QueryRow(t.Context(), "SELECT status FROM participants WHERE challenge_id = $1 AND user_id = $2", id, user).Scan(&status); err != nil || status != "active" {
		t.Fatalf("status %s (%v), want still active", status, err)
	}
}

func TestAbandonDeletedChallenge(t *testing.T) {
	svc, _, pool, id := joinFixture(t)
	user := insertProfile(t, pool, "abandon_deleted")
	if _, err := svc.Join(t.Context(), user, id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE challenges SET deleted_at = now() WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Abandon(t.Context(), user, id); !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
		t.Fatalf("error = %v, want challenge_not_found", err)
	}
	var status string
	if err := pool.QueryRow(t.Context(), "SELECT status FROM participants WHERE challenge_id = $1 AND user_id = $2", id, user).Scan(&status); err != nil || status != "active" {
		t.Fatalf("status %s (%v), want nothing written", status, err)
	}
}
