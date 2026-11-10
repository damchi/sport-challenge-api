// Package leaderboard assembles the rankings of a challenge: it loads the
// participants and their activities, and has the scorer of each category
// rank them. Nothing is stored: a ranking is always computed from the raw
// activities.
package leaderboard

import (
	"context"
	"fmt"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/scoring"
)

// repository is what the service needs from the storage.
type repository interface {
	Participants(ctx context.Context, challengeID string) ([]scoring.Participant, error)
	// Activities gives, in one query, the activities of the active
	// participants of the challenge started in [from, to).
	Activities(ctx context.Context, challengeID string, from, to time.Time) ([]scoring.Activity, error)
}

// Challenges gives challenge challengeID, with its categories, for actorID
// to read its leaderboard; it answers 404 challenge_not_found when the
// rights matrix forbids it (module challenge).
type Challenges interface {
	Challenge(ctx context.Context, actorID, challengeID string) (Challenge, error)
}

// Profiles gives the pseudo of each user of ids that has one; an anonymized
// account has none (module user).
type Profiles interface {
	Pseudos(ctx context.Context, ids []string) (map[string]string, error)
}

// Service holds the use cases of the leaderboard.
type Service struct {
	challenges Challenges
	profiles   Profiles
	repo       repository
	clock      clock.Clock
	// atRisk is how long before the end of a week a participant under the
	// minimum is at risk.
	atRisk time.Duration
}

// NewService returns a Service.
func NewService(challenges Challenges, profiles Profiles, repo repository, c clock.Clock, atRisk time.Duration) *Service {
	return &Service{challenges: challenges, profiles: profiles, repo: repo, clock: c, atRisk: atRisk}
}

// Get returns the ranking of every category of challenge id, in position
// order, as actorID may read it (the rule is the challenge module's, and
// nothing is loaded before it passes). Abandoned participants are listed in
// each category, unranked. The weeks are judged at the instant of the
// clock.
func (s *Service) Get(ctx context.Context, actorID, id string) (Leaderboard, error) {
	c, err := s.challenges.Challenge(ctx, actorID, id)
	if err != nil {
		return Leaderboard{}, err
	}
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return Leaderboard{}, fmt.Errorf("challenge time zone %q: %w", c.Timezone, err)
	}
	scorers := make([]scoring.Scorer, len(c.Categories))
	for i, cat := range c.Categories {
		if scorers[i], err = s.scorerOf(cat); err != nil {
			return Leaderboard{}, err
		}
	}

	participants, err := s.repo.Participants(ctx, id)
	if err != nil {
		return Leaderboard{}, err
	}
	activities, err := s.repo.Activities(ctx, id, c.StartsAt, c.EndsAt)
	if err != nil {
		return Leaderboard{}, err
	}
	ids := make([]string, len(participants))
	for i, p := range participants {
		ids[i] = p.UserID
	}
	pseudos, err := s.profiles.Pseudos(ctx, ids)
	if err != nil {
		return Leaderboard{}, fmt.Errorf("read pseudos: %w", err)
	}

	now := s.clock.Now()
	w := scoring.Window{Start: c.StartsAt, End: c.EndsAt, Location: loc, Now: now}
	board := Leaderboard{ComputedAt: now, Categories: make([]CategoryRanking, len(c.Categories))}
	for i, cat := range c.Categories {
		entries := scorers[i].Score(w, participants, activities)
		ranked := make([]Ranked, len(entries))
		for j, e := range entries {
			ranked[j] = Ranked{Entry: e}
			if p, ok := pseudos[e.UserID]; ok {
				ranked[j].Pseudo = &p
			}
		}
		board.Categories[i] = CategoryRanking{Type: cat.Type, Entries: ranked}
	}
	return board, nil
}

// scorerOf returns the scorer of a category, from the type and the settings
// frozen at the creation of the challenge.
func (s *Service) scorerOf(cat Category) (scoring.Scorer, error) {
	switch cat.Type {
	case CategoryCumulativeTime:
		return scoring.Duration{}, nil
	case CategoryCumulativeDistance:
		return scoring.Distance{Factors: cat.Factors}, nil
	case CategoryRunElimination:
		return scoring.Elimination{Sport: cat.Sport, StepMeters: cat.StepMeters, AtRisk: s.atRisk}, nil
	default:
		return nil, fmt.Errorf("unknown category type %q", cat.Type)
	}
}
