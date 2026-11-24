package challenge

import (
	"errors"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
)

// TestServiceAdminDelete: an admin deletes a challenge whatever their role,
// finished or not, at the clock's now; anyone else reaches nothing.
func TestServiceAdminDelete(t *testing.T) {
	tests := []struct {
		name       string
		actor      auth.Actor
		membership Membership
		now        time.Time
		want       apperr.Code
	}{
		{name: "admin, no role, ongoing", actor: auth.Actor{ID: actor, Admin: true}, now: now},
		{name: "admin, no role, finished", actor: auth.Actor{ID: actor, Admin: true}, now: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)},
		{name: "creator without the permission", actor: auth.Actor{ID: actor}, membership: Membership{Creator: true, Participant: ParticipantActive}, now: now, want: apperr.CodeNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{challenge: stored(VisibilityPrivate), membership: tt.membership}
			at, err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(tt.now)).AdminDelete(t.Context(), tt.actor, "c")
			if tt.want != "" {
				if !errors.Is(err, apperr.New(tt.want, nil)) || !repo.deletedAt.IsZero() {
					t.Fatalf("error = %v, deleted at %s, want %s and nothing deleted", err, repo.deletedAt, tt.want)
				}
				return
			}
			if err != nil || !repo.deletedAt.Equal(tt.now) || !at.Equal(tt.now) {
				t.Fatalf("error %v, deleted at %s (returned %s), want the clock's now", err, repo.deletedAt, at)
			}
		})
	}
}

// TestServiceAdminRestore: an admin restores a deleted challenge and learns
// when it was deleted; a challenge that is not deleted, or missing, is not
// found; anyone else reaches nothing.
func TestServiceAdminRestore(t *testing.T) {
	deletedAt := time.Date(2026, 3, 2, 14, 0, 0, 0, time.UTC)
	deleted := *stored(VisibilityPrivate)
	deleted.DeletedAt = &deletedAt
	tests := []struct {
		name      string
		actor     auth.Actor
		challenge *Challenge
		want      apperr.Code
	}{
		{name: "admin, deleted", actor: auth.Actor{ID: actor, Admin: true}, challenge: &deleted},
		{name: "admin, not deleted", actor: auth.Actor{ID: actor, Admin: true}, challenge: stored(VisibilityPrivate), want: apperr.CodeChallengeNotFound},
		{name: "admin, missing", actor: auth.Actor{ID: actor, Admin: true}, want: apperr.CodeChallengeNotFound},
		{name: "not an admin", actor: auth.Actor{ID: actor}, challenge: &deleted, want: apperr.CodeNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{challenge: tt.challenge}
			at, err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)).AdminRestore(t.Context(), tt.actor, "c")
			if tt.want != "" {
				if !errors.Is(err, apperr.New(tt.want, nil)) || repo.restored {
					t.Fatalf("error = %v, restored %v, want %s and nothing restored", err, repo.restored, tt.want)
				}
				return
			}
			if err != nil || !repo.restored || !at.Equal(deletedAt) {
				t.Fatalf("error %v, restored %v, deleted at %s, want restored and the instant of the deletion", err, repo.restored, at)
			}
		})
	}
}

func TestServiceAdminDeleteAndRestoreFailure(t *testing.T) {
	storage := errors.New("connection refused")
	s := NewService(&fakeRepository{err: storage}, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now))
	admin := auth.Actor{ID: actor, Admin: true}
	_, deleteErr := s.AdminDelete(t.Context(), admin, "c")
	_, restoreErr := s.AdminRestore(t.Context(), admin, "c")
	for _, err := range []error{deleteErr, restoreErr} {
		var appErr *apperr.Error
		if !errors.Is(err, storage) || errors.As(err, &appErr) {
			t.Fatalf("storage: %v, want it wrapped as an internal error", err)
		}
	}
}
