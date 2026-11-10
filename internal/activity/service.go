package activity

import (
	"context"
	"errors"
	"fmt"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
)

// repository is what the service needs from the storage.
type repository interface {
	Record(ctx context.Context, a Activity, decide func(ctx context.Context) error) (Activity, bool, error)
}

// Profiles tells whether a user's profile is complete (module user).
type Profiles interface {
	ProfileComplete(ctx context.Context, userID string) (bool, error)
}

// Catalogue gives the distance mode of a sport, and false for a sport it
// does not have (module sport).
type Catalogue interface {
	DistanceMode(ctx context.Context, code string) (string, bool, error)
}

// Participations gives the challenges a user joined, deleted ones left out,
// read in the transaction of ctx, if any (module challenge).
type Participations interface {
	Participations(ctx context.Context, userID string) ([]Participation, error)
}

// Service holds the use cases of the activities.
type Service struct {
	repo           repository
	profiles       Profiles
	catalogue      Catalogue
	participations Participations
	clock          clock.Clock
}

// NewService returns a Service.
func NewService(repo repository, profiles Profiles, catalogue Catalogue, participations Participations, c clock.Clock) *Service {
	return &Service{repo: repo, profiles: profiles, catalogue: catalogue, participations: participations, clock: c}
}

// Record records draft for actorID, always for themselves: the single entry
// point of activity creation (manual entry now, imports later). The actor
// needs a complete profile (422 profile_incomplete); the draft is validated
// (Validate), an unknown sport giving 400 validation_failed on sport_code.
// The activity is refused with 422 week_locked when its week has ended in a
// challenge where it counts, checked in the transaction of the write; it is
// recorded even if it counts nowhere (ADR 0001). With idempotencyKey, an
// activity of the actor recorded before with the same key is returned
// instead, nothing being written.
func (s *Service) Record(ctx context.Context, actorID string, idempotencyKey *string, draft Draft) (Recorded, error) {
	complete, err := s.profiles.ProfileComplete(ctx, actorID)
	if err != nil {
		return Recorded{}, fmt.Errorf("check profile: %w", err)
	}
	if !complete {
		return Recorded{}, apperr.New(apperr.CodeProfileIncomplete, nil)
	}

	mode, found, err := s.catalogue.DistanceMode(ctx, draft.SportCode)
	if err != nil {
		return Recorded{}, fmt.Errorf("read catalogue: %w", err)
	}
	if !found {
		return Recorded{}, apperr.InvalidFields(nil, "sport_code")
	}
	draft, err = Validate(draft, mode, s.clock.Now())
	if err != nil {
		return Recorded{}, err
	}

	a := Activity{
		UserID:          actorID,
		SportCode:       draft.SportCode,
		SportLabel:      draft.SportLabel,
		StartedAt:       draft.StartedAt.UTC(),
		DurationSeconds: draft.DurationSeconds,
		DistanceMeters:  draft.DistanceMeters,
		Source:          SourceManual,
		IdempotencyKey:  idempotencyKey,
	}
	var challenges []Participation
	stored, created, err := s.repo.Record(ctx, a, func(ctx context.Context) error {
		ps, err := s.participations.Participations(ctx, actorID)
		if err != nil {
			return fmt.Errorf("read participations: %w", err)
		}
		if IsLocked(s.clock.Now(), a.StartedAt, ps) {
			return apperr.New(apperr.CodeWeekLocked, nil)
		}
		challenges = ps
		return nil
	})
	if errors.Is(err, errAccountDeleted) {
		return Recorded{}, apperr.New(apperr.CodeAccountDeleted, nil)
	}
	if err != nil {
		return Recorded{}, err
	}
	if !created {
		// Recorded before: where it counts now, outside the transaction
		// that wrote nothing.
		if challenges, err = s.participations.Participations(ctx, actorID); err != nil {
			return Recorded{}, fmt.Errorf("read participations: %w", err)
		}
	}
	return Recorded{Activity: stored, ChallengeIDs: CountsIn(stored.StartedAt, challenges), Created: created}, nil
}
