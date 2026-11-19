// Package leaderboard assembles the rankings of a challenge: it loads the
// participants and their activities, and has the scorer of each category
// rank them. Nothing is stored: a ranking is always computed from the raw
// activities.
package leaderboard

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/scoring"
)

// repository is what the service needs from the storage.
type repository interface {
	Participants(ctx context.Context, challengeID string) ([]scoring.Participant, error)
	// Activities gives, in one query, the totals of the activities of the
	// active participants started in w: per participant and week for
	// weeklySport, per participant and sport for the others.
	Activities(ctx context.Context, challengeID string, w scoring.Window, weeklySport string) ([]scoring.Activity, error)
	// Snapshots gives the frozen results of the challenge by category type,
	// and when they were frozen; none until the worker has finalized it.
	Snapshots(ctx context.Context, challengeID string) (map[string]json.RawMessage, time.Time, error)
}

// Challenges gives challenge challengeID, with its categories, for actorID
// to read its leaderboard; it answers 404 challenge_not_found when the
// rights matrix forbids it (module challenge).
type Challenges interface {
	Challenge(ctx context.Context, actorID, challengeID string) (Challenge, error)
}

// Profiles gives the public name of each user of ids; an anonymized account
// has none (module user).
type Profiles interface {
	Names(ctx context.Context, ids []string) (map[string]Name, error)
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
// clock. Once the challenge is over, the frozen results are served; until
// the worker has frozen them, the ranking is computed, which gives the same
// result: the weeks that ended are locked by the clock, not by the worker.
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

	now := s.clock.Now()
	if !now.Before(c.EndsAt) {
		board, ok, err := s.frozen(ctx, c)
		if err != nil {
			return Leaderboard{}, err
		}
		if ok {
			return board, nil
		}
	}

	participants, err := s.repo.Participants(ctx, id)
	if err != nil {
		return Leaderboard{}, err
	}
	w := scoring.Window{Start: c.StartsAt, End: c.EndsAt, Location: loc, Now: now}
	activities, err := s.repo.Activities(ctx, id, w, weeklySport(c.Categories))
	if err != nil {
		return Leaderboard{}, err
	}
	ids := make([]string, len(participants))
	for i, p := range participants {
		ids[i] = p.UserID
	}
	names, err := s.profiles.Names(ctx, ids)
	if err != nil {
		return Leaderboard{}, fmt.Errorf("read names: %w", err)
	}

	board := Leaderboard{ComputedAt: now, Categories: make([]CategoryRanking, len(c.Categories))}
	for i, cat := range c.Categories {
		entries := scorers[i].Score(w, participants, activities)
		ranked := make([]Ranked, len(entries))
		for j, e := range entries {
			ranked[j] = Ranked{Entry: e}
			ranked[j].Name = names[e.UserID]
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

// frozenRanking is the ranking of a category as the worker stored it.
type frozenRanking struct {
	Entries []struct {
		UserID         string `json:"user_id"`
		Rank           int    `json:"rank"`
		Value          int    `json:"value"`
		Abandoned      bool   `json:"abandoned"`
		EliminatedWeek int    `json:"eliminated_week"`
	} `json:"entries"`
}

// frozen builds the leaderboard of a challenge that is over from its result
// snapshots, and reports false when they are not all there yet. The names
// are read now: an account anonymized since shows under a neutral name.
func (s *Service) frozen(ctx context.Context, c Challenge) (Leaderboard, bool, error) {
	if len(c.Categories) == 0 {
		return Leaderboard{}, false, nil
	}
	payloads, createdAt, err := s.repo.Snapshots(ctx, c.ID)
	if err != nil {
		return Leaderboard{}, false, err
	}
	rankings := make([]frozenRanking, len(c.Categories))
	var ids []string
	for i, cat := range c.Categories {
		payload, ok := payloads[cat.Type]
		if !ok {
			return Leaderboard{}, false, nil
		}
		if err := json.Unmarshal(payload, &rankings[i]); err != nil {
			return Leaderboard{}, false, fmt.Errorf("decode snapshot of category %s: %w", cat.Type, err)
		}
	}
	seen := make(map[string]bool)
	for _, r := range rankings {
		for _, e := range r.Entries {
			if !seen[e.UserID] {
				seen[e.UserID] = true
				ids = append(ids, e.UserID)
			}
		}
	}
	names, err := s.profiles.Names(ctx, ids)
	if err != nil {
		return Leaderboard{}, false, fmt.Errorf("read names: %w", err)
	}

	board := Leaderboard{ComputedAt: createdAt, Categories: make([]CategoryRanking, len(c.Categories))}
	for i, cat := range c.Categories {
		ranked := make([]Ranked, len(rankings[i].Entries))
		for j, e := range rankings[i].Entries {
			ranked[j] = Ranked{Entry: scoring.Entry{UserID: e.UserID, Rank: e.Rank, Value: e.Value, Abandoned: e.Abandoned}}
			// As computed: a survivor of the elimination category has an empty
			// progress once the challenge is over, an abandoned one has none.
			if cat.Type == CategoryRunElimination && !e.Abandoned {
				ranked[j].Progress = &scoring.Progress{}
				if e.EliminatedWeek > 0 {
					ranked[j].Progress = &scoring.Progress{Status: scoring.StatusEliminated, EliminatedWeek: e.EliminatedWeek}
				}
			}
			ranked[j].Name = names[e.UserID]
		}
		board.Categories[i] = CategoryRanking{Type: cat.Type, Entries: ranked}
	}
	return board, true, nil
}

// weeklySport is the sport of the elimination category, the only one the
// scorers count week by week; empty without such a category.
func weeklySport(categories []Category) string {
	for _, cat := range categories {
		if cat.Type == CategoryRunElimination {
			return cat.Sport
		}
	}
	return ""
}
