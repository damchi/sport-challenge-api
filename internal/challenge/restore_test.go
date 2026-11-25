package challenge

import (
	"errors"
	"reflect"
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
			r, err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)).AdminRestore(t.Context(), tt.actor, "c", nil)
			if tt.want != "" {
				if !errors.Is(err, apperr.New(tt.want, nil)) || repo.restored {
					t.Fatalf("error = %v, restored %v, want %s and nothing restored", err, repo.restored, tt.want)
				}
				return
			}
			if err != nil || !repo.restored || !r.DeletedAt.Equal(deletedAt) || r.Creator.Old != nil || r.Creator.New != nil {
				t.Fatalf("restore = %+v, error %v, restored %v, want restored, the instant of the deletion and no creator named", r, err, repo.restored)
			}
		})
	}
}

func TestServiceAdminDeleteAndRestoreFailure(t *testing.T) {
	storage := errors.New("connection refused")
	s := NewService(&fakeRepository{err: storage}, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now))
	admin := auth.Actor{ID: actor, Admin: true}
	_, deleteErr := s.AdminDelete(t.Context(), admin, "c")
	_, restoreErr := s.AdminRestore(t.Context(), admin, "c", nil)
	for _, err := range []error{deleteErr, restoreErr} {
		var appErr *apperr.Error
		if !errors.Is(err, storage) || errors.As(err, &appErr) {
			t.Fatalf("storage: %v, want it wrapped as an internal error", err)
		}
	}
}

// named is the account a restore names as a creator in the tests below.
const named = "5c0a3b2e-1d4f-4e6a-8b9c-0d1e2f3a4b5c"

// TestServiceAdminRestoreKeepsACreator: a challenge always keeps a creator
// (README §2.12). Without any creator with an account, the restore of a
// challenge that comes back upcoming or ongoing is refused unless it names
// one; a finished one is restored without (README §2.2). The account named
// becomes a creator, first added as an active participant when it never
// joined; it is refused, nothing restored, when unknown or deleted, or when
// it never joined with an incomplete profile.
func TestServiceAdminRestoreKeepsACreator(t *testing.T) {
	deletedAt := time.Date(2026, 3, 3, 14, 0, 0, 0, time.UTC)
	ongoing := *stored(VisibilityPrivate)
	ongoing.DeletedAt = &deletedAt
	over := ongoing
	over.StartsAt, over.EndsAt = time.Date(2026, 1, 5, 5, 0, 0, 0, time.UTC), time.Date(2026, 2, 2, 5, 0, 0, 0, time.UTC)
	upcoming := ongoing
	upcoming.StartsAt, upcoming.EndsAt = time.Date(2026, 4, 6, 4, 0, 0, 0, time.UTC), time.Date(2026, 4, 20, 4, 0, 0, 0, time.UTC)
	creatorID := func() *string { id := named; return &id }

	tests := []struct {
		name        string
		challenge   Challenge
		creatorless bool
		creator     *string
		target      Membership
		complete    bool
		lockErr     error
		want        apperr.Code
		// joined and madeCreator tell what the restore wrote of the account named.
		joined, madeCreator bool
		change              MemberChange
	}{
		{name: "ongoing without a creator, none named", challenge: ongoing, creatorless: true, want: apperr.CodeNoCreator},
		{name: "upcoming without a creator, none named", challenge: upcoming, creatorless: true, want: apperr.CodeNoCreator},
		{name: "finished without a creator, none named", challenge: over, creatorless: true},
		{
			name: "an active participant named", challenge: ongoing, creatorless: true, creator: creatorID(),
			target: Membership{Participant: ParticipantActive}, madeCreator: true,
			change: MemberChange{Old: map[string]any{memberCreator: false}, New: map[string]any{memberCreator: true}},
		},
		{
			name: "one who abandoned named, not reinstated", challenge: ongoing, creatorless: true, creator: creatorID(),
			target: Membership{Participant: ParticipantAbandoned}, madeCreator: true,
			change: MemberChange{Old: map[string]any{memberCreator: false}, New: map[string]any{memberCreator: true}},
		},
		{
			name: "an account that never joined named", challenge: ongoing, creatorless: true, creator: creatorID(), complete: true,
			joined: true, madeCreator: true,
			change: MemberChange{Old: map[string]any{memberStatus: nil, memberCreator: false}, New: map[string]any{memberStatus: ParticipantActive, memberCreator: true}},
		},
		{
			name: "a creator named while others are left", challenge: ongoing, creator: creatorID(),
			target: Membership{Participant: ParticipantActive}, madeCreator: true,
			change: MemberChange{Old: map[string]any{memberCreator: false}, New: map[string]any{memberCreator: true}},
		},
		{
			name: "a creator already named", challenge: ongoing, creator: creatorID(),
			target: Membership{Participant: ParticipantActive, Creator: true}, change: noChange(),
		},
		{name: "never joined, profile incomplete", challenge: ongoing, creatorless: true, creator: creatorID(), want: apperr.CodeProfileIncomplete},
		{name: "unknown or deleted account", challenge: ongoing, creatorless: true, creator: creatorID(), lockErr: errAccountDeleted, want: apperr.CodeUserNotFound},
		// The account is locked before the challenge (the order of an
		// account deletion): an unknown one answers first.
		{name: "unknown account, challenge not deleted", challenge: *stored(VisibilityPrivate), creator: creatorID(), lockErr: errAccountDeleted, want: apperr.CodeUserNotFound},
		{name: "known account, challenge not deleted", challenge: *stored(VisibilityPrivate), creator: creatorID(), complete: true, want: apperr.CodeChallengeNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.challenge
			repo := &fakeRepository{
				challenge: &c, creatorless: tt.creatorless, lockErr: tt.lockErr,
				others: map[string]Membership{named: tt.target}, membership: tt.target, target: tt.target,
			}
			svc := NewService(repo, fakeProfiles{complete: tt.complete}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now))
			r, err := svc.AdminRestore(t.Context(), auth.Actor{ID: actor, Admin: true}, "c", tt.creator)
			if tt.want != "" {
				var appErr *apperr.Error
				if !errors.As(err, &appErr) || appErr.Code != tt.want || repo.creatorAdded || !repo.joinedAt.IsZero() {
					t.Fatalf("error = %v, creator added %v, joined at %s, want %s and nothing written", err, repo.creatorAdded, repo.joinedAt, tt.want)
				}
				// Refused before the restore, or in it: the change rolls it back.
				return
			}
			if err != nil || !repo.restored {
				t.Fatalf("error = %v, restored %v, want restored", err, repo.restored)
			}
			if tt.creator != nil && repo.locked != named {
				t.Fatalf("locked %q, want the account named first", repo.locked)
			}
			if got := !repo.joinedAt.IsZero(); got != tt.joined || (tt.joined && !repo.joinedAt.Equal(now)) {
				t.Fatalf("joined at %s, want joined %v at now", repo.joinedAt, tt.joined)
			}
			if repo.creatorAdded != tt.madeCreator {
				t.Fatalf("creator added %v, want %v", repo.creatorAdded, tt.madeCreator)
			}
			if tt.creator == nil && (r.Creator.Old != nil || r.Creator.New != nil) {
				t.Fatalf("creator change %+v, want none", r.Creator)
			}
			if tt.creator != nil && !reflect.DeepEqual(r.Creator, tt.change) {
				t.Fatalf("creator change %+v, want %+v", r.Creator, tt.change)
			}
		})
	}
}
