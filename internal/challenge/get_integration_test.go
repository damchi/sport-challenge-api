//go:build integration

package challenge

import (
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

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
