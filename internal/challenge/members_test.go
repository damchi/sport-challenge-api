package challenge

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
)

// memberUseCase is one of the admin use cases on the members of a challenge.
type memberUseCase func(s *Service, ctx context.Context, actor auth.Actor, id, userID string) (MemberChange, error)

var (
	addParticipant       memberUseCase = (*Service).AdminAddParticipant
	reinstateParticipant memberUseCase = (*Service).AdminReinstateParticipant
	excludeParticipant   memberUseCase = (*Service).AdminExcludeParticipant
	addCreator           memberUseCase = (*Service).AdminAddCreator
	removeCreator        memberUseCase = (*Service).AdminRemoveCreator
)

// TestServiceAdminMembers: an admin changes the members of a finished
// challenge, registration long closed, a creator excluded; the change
// tells the journal what it wrote, nothing when it wrote nothing.
func TestServiceAdminMembers(t *testing.T) {
	finished := *stored(VisibilityPrivate)
	finished.EndsAt = now.Add(-30 * 24 * time.Hour)
	finished.StartsAt = finished.EndsAt.Add(-14 * 24 * time.Hour)
	admin := auth.Actor{ID: actor, Admin: true}
	status := func(s ParticipantStatus) map[string]any { return map[string]any{memberStatus: s} }
	creatorRole := func(b bool) map[string]any { return map[string]any{memberCreator: b} }
	tests := []struct {
		name       string
		useCase    memberUseCase
		target     Membership
		others     int
		incomplete bool
		want       MemberChange
		wantErr    apperr.Code
		// written reads, from the fake, whether the use case wrote.
		written func(f *fakeRepository) bool
	}{
		{
			name: "add someone outside, registration closed", useCase: addParticipant, target: outsider,
			want: MemberChange{New: status(ParticipantActive)}, written: func(f *fakeRepository) bool { return f.joinedAt.Equal(now) },
		},
		{
			name: "add someone with an incomplete profile", useCase: addParticipant, target: outsider, incomplete: true, wantErr: apperr.CodeProfileIncomplete,
			written: func(f *fakeRepository) bool { return !f.joinedAt.IsZero() },
		},
		{
			name: "add someone who abandoned", useCase: addParticipant, target: abandoned, wantErr: apperr.CodeParticipationAbandoned,
			written: func(f *fakeRepository) bool { return !f.joinedAt.IsZero() },
		},
		{
			name: "reinstate someone excluded", useCase: reinstateParticipant, target: abandoned,
			want: MemberChange{Old: status(ParticipantAbandoned), New: status(ParticipantActive)}, written: func(f *fakeRepository) bool { return f.reinstated },
		},
		{
			name: "reinstate an active participant: nothing written", useCase: reinstateParticipant, target: active,
			want: noChange(), written: func(f *fakeRepository) bool { return f.reinstated },
		},
		{
			name: "reinstate someone who never joined", useCase: reinstateParticipant, target: outsider, wantErr: apperr.CodeNotFound,
			written: func(f *fakeRepository) bool { return f.reinstated },
		},
		{
			name: "exclude a creator, after the end", useCase: excludeParticipant, target: creatorActive,
			want: MemberChange{Old: status(ParticipantActive), New: status(ParticipantAbandoned)}, written: func(f *fakeRepository) bool { return f.abandonedAt.Equal(now) },
		},
		{
			name: "exclude someone already out: nothing written", useCase: excludeParticipant, target: abandoned,
			want: noChange(), written: func(f *fakeRepository) bool { return !f.abandonedAt.IsZero() },
		},
		{
			name: "name a creator who abandoned, after the end", useCase: addCreator, target: abandoned,
			want: MemberChange{Old: creatorRole(false), New: creatorRole(true)}, written: func(f *fakeRepository) bool { return f.creatorAdded },
		},
		{
			name: "name someone who never joined", useCase: addCreator, target: invited, wantErr: apperr.CodeNotActiveParticipant,
			written: func(f *fakeRepository) bool { return f.creatorAdded },
		},
		{
			name: "remove a creator, another one stays", useCase: removeCreator, target: creatorActive, others: 1,
			want: MemberChange{Old: creatorRole(true), New: creatorRole(false)}, written: func(f *fakeRepository) bool { return f.creatorRemoved },
		},
		{
			name: "remove the last creator", useCase: removeCreator, target: creatorAbandoned, wantErr: apperr.CodeLastCreator,
			written: func(f *fakeRepository) bool { return f.creatorRemoved },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := finished
			// Join, Abandon, Reinstate and RemoveCreator read the target as
			// membership, AddCreator as target.
			repo := &fakeRepository{challenge: &c, membership: tt.target, target: tt.target, otherCreators: tt.others}
			s := NewService(repo, fakeProfiles{complete: !tt.incomplete}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now))
			got, err := tt.useCase(s, t.Context(), admin, "c", "t")
			if tt.wantErr != "" {
				if !errors.Is(err, apperr.New(tt.wantErr, nil)) {
					t.Fatalf("error = %v, want %s", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatal(err)
			} else if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("change = %+v, want %+v", got, tt.want)
			}
			if wrote := tt.written(repo); wrote != (tt.wantErr == "" && !reflect.DeepEqual(tt.want, noChange())) {
				t.Fatalf("written = %v", wrote)
			}
			if repo.locked != "t" {
				t.Fatalf("locked account %q, want the target", repo.locked)
			}
		})
	}
}

// TestServiceAdminMembersForbiddenAndMissing: without the admin permission,
// every use case answers 404 not_found and locks nothing, a creator of the
// challenge included; an unknown account and an unknown challenge are not
// found; a storage failure stays an internal error.
func TestServiceAdminMembersForbiddenAndMissing(t *testing.T) {
	useCases := map[string]memberUseCase{
		"add": addParticipant, "reinstate": reinstateParticipant, "exclude": excludeParticipant,
		"add creator": addCreator, "remove creator": removeCreator,
	}
	// What an unknown or deleted account answers, by use case.
	missingAccount := map[string]apperr.Code{
		"add": apperr.CodeUserNotFound, "reinstate": apperr.CodeNotFound, "exclude": apperr.CodeNotFound,
		"add creator": apperr.CodeNotActiveParticipant, "remove creator": apperr.CodeNotFound,
	}
	for name, useCase := range useCases {
		t.Run(name, func(t *testing.T) {
			repo := &fakeRepository{challenge: stored(VisibilityPublic), membership: creatorActive, target: active, otherCreators: 1}
			s := NewService(repo, fakeProfiles{complete: true}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now))
			if _, err := useCase(s, t.Context(), auth.Actor{ID: actor}, "c", "t"); !errors.Is(err, apperr.New(apperr.CodeNotFound, nil)) {
				t.Fatalf("not an admin: %v, want not_found", err)
			}
			if repo.locked != "" {
				t.Fatalf("not an admin: account %q locked", repo.locked)
			}
			admin := auth.Actor{ID: actor, Admin: true}

			deleted := &fakeRepository{challenge: stored(VisibilityPublic), lockErr: errAccountDeleted}
			s = NewService(deleted, fakeProfiles{complete: true}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now))
			if _, err := useCase(s, t.Context(), admin, "c", "t"); !errors.Is(err, apperr.New(missingAccount[name], nil)) {
				t.Fatalf("unknown account: %v, want %s", err, missingAccount[name])
			}

			s = NewService(&fakeRepository{target: active}, fakeProfiles{complete: true}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now))
			if _, err := useCase(s, t.Context(), admin, "c", "t"); !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
				t.Fatalf("unknown challenge: %v, want challenge_not_found", err)
			}

			storage := errors.New("connection refused")
			s = NewService(&fakeRepository{err: storage}, fakeProfiles{complete: true}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now))
			_, err := useCase(s, t.Context(), admin, "c", "t")
			var appErr *apperr.Error
			if !errors.Is(err, storage) || errors.As(err, &appErr) {
				t.Fatalf("storage: %v, want it wrapped as an internal error", err)
			}
		})
	}
}
