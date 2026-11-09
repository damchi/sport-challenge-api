package challenge

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
)

// repository is what the service needs from the storage.
type repository interface {
	Create(ctx context.Context, c Challenge, joinedAt time.Time) (Challenge, error)
	List(ctx context.Context, q ListQuery) ([]Summary, error)
	Get(ctx context.Context, id, actorID string) (Challenge, Membership, error)
	Categories(ctx context.Context, id string) ([]Category, error)
}

// Profiles tells whether a user's profile is complete (module user).
type Profiles interface {
	ProfileComplete(ctx context.Context, userID string) (bool, error)
}

// Catalogue gives the suggested factor of every sport with a distance
// (module sport).
type Catalogue interface {
	SuggestedFactors(ctx context.Context) (map[string]float64, error)
}

// Service holds the use cases of the challenges.
type Service struct {
	repo      repository
	profiles  Profiles
	catalogue Catalogue
	clock     clock.Clock
}

// NewService returns a Service.
func NewService(repo repository, profiles Profiles, catalogue Catalogue, c clock.Clock) *Service {
	return &Service{repo: repo, profiles: profiles, catalogue: catalogue, clock: c}
}

// Create creates the challenge described by draft, with actorID as its
// creator and first participant. The actor needs a complete profile (422
// profile_incomplete). The distance factors are the catalogue's suggestions,
// replaced by those of the draft, then frozen in the category.
func (s *Service) Create(ctx context.Context, actorID string, draft Draft) (Challenge, error) {
	complete, err := s.profiles.ProfileComplete(ctx, actorID)
	if err != nil {
		return Challenge{}, fmt.Errorf("check profile: %w", err)
	}
	if !complete {
		return Challenge{}, apperr.New(apperr.CodeProfileIncomplete, nil)
	}

	suggested, err := s.catalogue.SuggestedFactors(ctx)
	if err != nil {
		return Challenge{}, fmt.Errorf("read catalogue: %w", err)
	}
	distanceSports := make(map[string]bool, len(suggested))
	for code := range suggested {
		distanceSports[code] = true
	}

	now := s.clock.Now()
	window, err := Validate(draft, now, distanceSports)
	if err != nil {
		return Challenge{}, err
	}

	factors := maps.Clone(suggested)
	maps.Copy(factors, draft.DistanceFactors)
	// Running is the reference of the distance category, whatever the
	// catalogue says (README §2.4).
	factors[referenceSport] = 1

	c, err := s.repo.Create(ctx, Challenge{
		Title:            strings.TrimSpace(draft.Title),
		Description:      draft.Description,
		Visibility:       draft.Visibility,
		MembersCanInvite: draft.MembersCanInvite,
		Timezone:         draft.Timezone,
		StartsAt:         window.StartsAt,
		EndsAt:           window.EndsAt,
		CreatedBy:        actorID,
		Categories: []Category{
			{Type: CategoryCumulativeTime, Position: 1, Params: Params{}},
			{Type: CategoryCumulativeDistance, Position: 2, Params: Params{Factors: factors}},
			{Type: CategoryRunElimination, Position: 3, Params: Params{Sport: referenceSport, StepMeters: draft.EliminationStepMeters}},
		},
	}, now)
	if err != nil {
		return Challenge{}, fmt.Errorf("store challenge: %w", err)
	}
	return c, nil
}

// List returns a page of challenges for actorID: public ones, or those the
// actor created or joined. The status filter uses the clock. The
// visibility rules are applied by the query itself, never by filtering a
// loaded list.
func (s *Service) List(ctx context.Context, actorID, scope, status string, limit int, after *Position) (Page, error) {
	if limit < 1 {
		return Page{}, apperr.InvalidFields(nil, "limit")
	}
	// One more than asked tells whether a next page exists.
	items, err := s.repo.List(ctx, ListQuery{
		ActorID: actorID, Scope: scope, Status: status,
		Now: s.clock.Now(), Limit: limit + 1, After: after,
	})
	if err != nil {
		return Page{}, fmt.Errorf("list challenges: %w", err)
	}
	page := Page{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]
		page.Next = &Position{StartsAt: last.StartsAt, ID: last.ID}
	}
	return page, nil
}

// Get returns challenge id as actorID sees it. A missing or deleted
// challenge, and a private one the actor may not see, all answer the same
// 404 challenge_not_found: nothing tells that a private challenge exists.
func (s *Service) Get(ctx context.Context, actorID, id string) (Detail, error) {
	c, m, err := s.repo.Get(ctx, id, actorID)
	if errors.Is(err, errChallengeNotFound) {
		return Detail{}, apperr.New(apperr.CodeChallengeNotFound, nil)
	}
	if err != nil {
		return Detail{}, fmt.Errorf("get challenge: %w", err)
	}
	role := RoleOf(m)
	if err := RequireView(role, c.Visibility); err != nil {
		return Detail{}, err
	}
	// Only a visible challenge reaches the next reads.
	if c.Categories, err = s.repo.Categories(ctx, c.ID); err != nil {
		return Detail{}, fmt.Errorf("get challenge: %w", err)
	}
	loc, err := location(c.Timezone)
	if err != nil {
		return Detail{}, fmt.Errorf("challenge time zone %q: %w", c.Timezone, err)
	}

	now := s.clock.Now()
	deadline := RegistrationDeadline(c.StartsAt, loc)
	week, weekEnd := CurrentWeek(now, c.StartsAt, c.EndsAt, loc)
	return Detail{
		Challenge:            c,
		Role:                 role,
		Participant:          m.Participant,
		Status:               StatusAt(now, c.StartsAt, c.EndsAt),
		CanJoin:              CanJoin(role, c.Visibility, now, deadline),
		RegistrationDeadline: deadline,
		CurrentWeek:          week,
		WeekEndsAt:           weekEnd,
	}, nil
}
