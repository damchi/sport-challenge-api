package activity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
)

// repository is what the service needs from the storage.
type repository interface {
	Record(ctx context.Context, a Activity, decide func(ctx context.Context) error) (Activity, bool, error)
	Update(ctx context.Context, actorID, id string, a Activity, decide func(ctx context.Context, current Activity) error) (Activity, error)
	Delete(ctx context.Context, actorID, id string, at time.Time, decide func(ctx context.Context, current Activity) error) error
	List(ctx context.Context, q ListQuery) ([]Activity, error)
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

// Challenges gives challenge challengeID, as a participation of userID, for
// actorID to read the activities of userID there; it answers 404
// challenge_not_found or not_found when the rights matrix forbids it
// (module challenge).
type Challenges interface {
	ActivitiesScope(ctx context.Context, actorID, challengeID, userID string) (Participation, error)
}

// Service holds the use cases of the activities.
type Service struct {
	repo           repository
	profiles       Profiles
	catalogue      Catalogue
	participations Participations
	challenges     Challenges
	clock          clock.Clock
}

// NewService returns a Service.
func NewService(repo repository, profiles Profiles, catalogue Catalogue, participations Participations, challenges Challenges, c clock.Clock) *Service {
	return &Service{repo: repo, profiles: profiles, catalogue: catalogue, participations: participations, challenges: challenges, clock: c}
}

// Record records draft for actorID, always for themselves: the single entry
// point of activity creation (manual entry now, imports later). The actor
// needs a complete profile (422 profile_incomplete); the draft is validated
// (Validate), an unknown sport giving 400 validation_failed on sport_code.
// The activity is refused with 422 week_locked when its week has ended in a
// challenge where it counts, checked in the transaction of the write; it is
// recorded even if it counts nowhere (ADR 0001). With idempotencyKey, an
// activity of the actor recorded before with the same key is returned
// instead, nothing being written, or 404 activity_not_found once deleted.
func (s *Service) Record(ctx context.Context, actorID string, idempotencyKey *string, draft Draft) (Recorded, error) {
	complete, err := s.profiles.ProfileComplete(ctx, actorID)
	if err != nil {
		return Recorded{}, fmt.Errorf("check profile: %w", err)
	}
	if !complete {
		return Recorded{}, apperr.New(apperr.CodeProfileIncomplete, nil)
	}

	draft, err = s.validate(ctx, draft)
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
	if err != nil {
		return Recorded{}, storageError(err)
	}
	if !created {
		// Recorded before: where it counts now, outside the transaction
		// that wrote nothing.
		if challenges, err = s.participations.Participations(ctx, actorID); err != nil {
			return Recorded{}, fmt.Errorf("read participations: %w", err)
		}
	}
	return Recorded{
		Activity:     stored,
		ChallengeIDs: CountsIn(stored.StartedAt, challenges),
		Created:      created,
		Locked:       IsLocked(s.clock.Now(), stored.StartedAt, challenges),
	}, nil
}

// Update replaces activity id of actorID with draft, validated as at the
// creation (Record): the body replaces every field entered. The activity of
// someone else, a deleted or a missing one gives 404 activity_not_found. It
// is refused with 422 week_locked when the week of its current start, or of
// the new one, has ended in a challenge where it counts, checked in the
// transaction of the write. The profile is not checked again: the activity
// was recorded with a complete one. A badge won stays won (README §2.7,
// V1.1).
func (s *Service) Update(ctx context.Context, actorID, id string, draft Draft) (Recorded, error) {
	draft, err := s.validate(ctx, draft)
	if err != nil {
		return Recorded{}, err
	}
	a := Activity{
		SportCode:       draft.SportCode,
		SportLabel:      draft.SportLabel,
		StartedAt:       draft.StartedAt.UTC(),
		DurationSeconds: draft.DurationSeconds,
		DistanceMeters:  draft.DistanceMeters,
	}
	var challenges []Participation
	stored, err := s.repo.Update(ctx, actorID, id, a, func(ctx context.Context, current Activity) error {
		// The current start is checked by changeable, the new one here,
		// on the same participations.
		ps, err := s.changeable(ctx, actorID, current)
		if err != nil {
			return err
		}
		if IsLocked(s.clock.Now(), a.StartedAt, ps) {
			return apperr.New(apperr.CodeWeekLocked, nil)
		}
		challenges = ps
		return nil
	})
	if err != nil {
		return Recorded{}, storageError(err)
	}
	return Recorded{
		Activity:     stored,
		ChallengeIDs: CountsIn(stored.StartedAt, challenges),
		Locked:       IsLocked(s.clock.Now(), stored.StartedAt, challenges),
	}, nil
}

// Delete marks activity id of actorID deleted: it counts nowhere from then
// on. As for Update, the activity of someone else gives 404
// activity_not_found and a week ended where it counts 422 week_locked.
func (s *Service) Delete(ctx context.Context, actorID, id string) error {
	err := s.repo.Delete(ctx, actorID, id, s.clock.Now(), func(ctx context.Context, current Activity) error {
		_, err := s.changeable(ctx, actorID, current)
		return err
	})
	if err != nil {
		return storageError(err)
	}
	return nil
}

// Period keeps the activities started in [From, To); a nil bound is open.
type Period struct {
	From *time.Time
	To   *time.Time
}

// Mine returns a page of the activities of actorID started in period, most
// recent first, each with the challenges where it counts now and whether it
// is locked. A period whose To does not come after its From gives 400
// validation_failed on to.
func (s *Service) Mine(ctx context.Context, actorID string, period Period, limit int, after *Position) (Page, error) {
	if limit < 1 {
		return Page{}, apperr.InvalidFields(nil, "limit")
	}
	if period.From != nil && period.To != nil && !period.From.Before(*period.To) {
		return Page{}, apperr.InvalidFields(nil, "to")
	}
	q := ListQuery{UserID: actorID, From: period.From, To: period.To, Limit: limit + 1, After: after}
	return s.page(ctx, q, limit, func(ps []Participation, a Activity) []string {
		return CountsIn(a.StartedAt, ps)
	})
}

// OfParticipant returns a page of the activities of userID started in the
// window of challenge challengeID, most recent first, for actorID: the
// participants of the challenge (abandoned ones included) and its creators
// only, as module challenge decides (Challenges). Each one lists this
// challenge only, when it counts there, never the other challenges of
// userID; whether it is locked is read from all of them.
func (s *Service) OfParticipant(ctx context.Context, actorID, challengeID, userID string, limit int, after *Position) (Page, error) {
	if limit < 1 {
		return Page{}, apperr.InvalidFields(nil, "limit")
	}
	c, err := s.challenges.ActivitiesScope(ctx, actorID, challengeID, userID)
	if err != nil {
		return Page{}, fmt.Errorf("activities scope: %w", err)
	}
	q := ListQuery{UserID: userID, From: &c.StartsAt, To: &c.EndsAt, Limit: limit + 1, After: after}
	return s.page(ctx, q, limit, func(_ []Participation, a Activity) []string {
		return CountsIn(a.StartedAt, []Participation{c})
	})
}

// page reads the activities of q, limit asked plus one to tell whether a
// next page exists, with where each counts as countsIn says and whether it
// is locked, from the participations of q.UserID.
func (s *Service) page(ctx context.Context, q ListQuery, limit int, countsIn func([]Participation, Activity) []string) (Page, error) {
	rows, err := s.repo.List(ctx, q)
	if err != nil {
		return Page{}, fmt.Errorf("list activities: %w", err)
	}
	var page Page
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1]
		page.Next = &Position{StartedAt: last.StartedAt, ID: last.ID}
	}
	page.Items = make([]Recorded, 0, len(rows))
	if len(rows) == 0 {
		return page, nil
	}
	ps, err := s.participations.Participations(ctx, q.UserID)
	if err != nil {
		return Page{}, fmt.Errorf("read participations: %w", err)
	}
	now := s.clock.Now()
	for _, a := range rows {
		page.Items = append(page.Items, Recorded{
			Activity:     a,
			ChallengeIDs: countsIn(ps, a),
			Locked:       IsLocked(now, a.StartedAt, ps),
		})
	}
	return page, nil
}

// validate checks draft against the catalogue at the instant of the clock
// (Validate), an unknown sport giving 400 validation_failed on sport_code.
func (s *Service) validate(ctx context.Context, draft Draft) (Draft, error) {
	mode, found, err := s.catalogue.DistanceMode(ctx, draft.SportCode)
	if err != nil {
		return Draft{}, fmt.Errorf("read catalogue: %w", err)
	}
	if !found {
		return Draft{}, apperr.InvalidFields(nil, "sport_code")
	}
	return Validate(draft, mode, s.clock.Now())
}

// changeable checks that actorID may change current (CanChange, else 404
// activity_not_found) and that its week has not ended in a challenge where
// it counts (422 week_locked). It returns the participations of the author,
// read in the transaction of ctx.
func (s *Service) changeable(ctx context.Context, actorID string, current Activity) ([]Participation, error) {
	if !CanChange(actorID, current.UserID) {
		return nil, apperr.New(apperr.CodeActivityNotFound, nil)
	}
	ps, err := s.participations.Participations(ctx, actorID)
	if err != nil {
		return nil, fmt.Errorf("read participations: %w", err)
	}
	if IsLocked(s.clock.Now(), current.StartedAt, ps) {
		return nil, apperr.New(apperr.CodeWeekLocked, nil)
	}
	return ps, nil
}

// storageError turns the errors of the storage into errors of the API.
func storageError(err error) error {
	switch {
	case errors.Is(err, errActivityNotFound):
		return apperr.New(apperr.CodeActivityNotFound, nil)
	case errors.Is(err, errAccountDeleted):
		return apperr.New(apperr.CodeAccountDeleted, nil)
	}
	return err
}
