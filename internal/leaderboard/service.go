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
	// ParticipantsOf gives the participants of each challenge, in one query.
	ParticipantsOf(ctx context.Context, challengeIDs []string) (map[string][]scoring.Participant, error)
	// ActivitiesOf gives, in one query, the totals of the activities of the
	// active participants of each challenge started in its window: per
	// participant and week for its weekly sport, per participant and sport
	// for the others.
	ActivitiesOf(ctx context.Context, scopes []Scope) (map[string][]scoring.Activity, error)
	// Snapshots gives the frozen results of the challenge by category type,
	// and when they were frozen; none until the worker has finalized it.
	Snapshots(ctx context.Context, challengeID string) (map[string]json.RawMessage, time.Time, error)
	// DeletedAccounts gives which of ids are deleted (anonymized) accounts,
	// left out of the frozen results.
	DeletedAccounts(ctx context.Context, ids []string) (map[string]bool, error)
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
// each category, unranked; deleted accounts are not listed at all. The weeks
// are judged at the instant of the clock. Once the challenge is over, the
// frozen results are served; until
// the worker has frozen them, the ranking is computed, which gives the same
// result: the weeks that ended are locked by the clock, not by the worker.
func (s *Service) Get(ctx context.Context, actorID, id string) (Leaderboard, error) {
	c, err := s.challenges.Challenge(ctx, actorID, id)
	if err != nil {
		return Leaderboard{}, err
	}
	now := s.clock.Now()
	w, scorers, err := s.prepare(c, now)
	if err != nil {
		return Leaderboard{}, err
	}

	if !now.Before(c.EndsAt) {
		board, ok, err := s.frozen(ctx, c)
		if err != nil {
			return Leaderboard{}, err
		}
		if ok {
			return board, nil
		}
	}

	byChallenge, err := s.repo.ParticipantsOf(ctx, []string{id})
	if err != nil {
		return Leaderboard{}, err
	}
	participants := byChallenge[id]
	activitiesOf, err := s.repo.ActivitiesOf(ctx, []Scope{{ChallengeID: id, Window: w, WeeklySport: weeklySport(c.Categories)}})
	if err != nil {
		return Leaderboard{}, err
	}
	activities := activitiesOf[id]
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

// Standings returns where actorID stands in each of challenges that is in
// its weeks at the instant of the clock, by challenge ID: the current week
// and the actor's line in every category, as Get computes it at the same
// instant. A challenge outside its weeks, or where the actor is not an active
// participant, is left out. The challenges are the caller's list of the
// actor's own; however many, they are read in two queries.
func (s *Service) Standings(ctx context.Context, actorID string, challenges []Challenge) (map[string]Standing, error) {
	now := s.clock.Now()
	type running struct {
		c       Challenge
		w       scoring.Window
		scorers []scoring.Scorer
	}
	var runs []running
	ids := make([]string, 0, len(challenges))
	for _, c := range challenges {
		w, scorers, err := s.prepare(c, now)
		if err != nil {
			return nil, err
		}
		if week, _ := w.CurrentWeek(); week == 0 {
			continue
		}
		runs = append(runs, running{c: c, w: w, scorers: scorers})
		ids = append(ids, c.ID)
	}
	out := make(map[string]Standing, len(runs))
	if len(runs) == 0 {
		return out, nil
	}
	participants, err := s.repo.ParticipantsOf(ctx, ids)
	if err != nil {
		return nil, err
	}
	// The activities are only read where the actor is still in the running.
	kept := runs[:0]
	var scopes []Scope
	for _, r := range runs {
		if isActive(participants[r.c.ID], actorID) {
			kept = append(kept, r)
			scopes = append(scopes, Scope{ChallengeID: r.c.ID, Window: r.w, WeeklySport: weeklySport(r.c.Categories)})
		}
	}
	if len(kept) == 0 {
		return out, nil
	}
	activities, err := s.repo.ActivitiesOf(ctx, scopes)
	if err != nil {
		return nil, err
	}
	for _, r := range kept {
		week, ends := r.w.CurrentWeek()
		st := Standing{Week: week, WeekEndsAt: ends, Categories: make([]Place, len(r.c.Categories))}
		for i, cat := range r.c.Categories {
			st.Categories[i] = placeOf(cat.Type, r.scorers[i].Score(r.w, participants[r.c.ID], activities[r.c.ID]), actorID)
		}
		out[r.c.ID] = st
	}
	return out, nil
}

// isActive reports whether userID is among participants and has not
// abandoned.
func isActive(participants []scoring.Participant, userID string) bool {
	for _, p := range participants {
		if p.UserID == userID {
			return !p.Abandoned
		}
	}
	return false
}

// placeOf is the line of userID in the ranking entries of a category, with
// how many are ranked in it.
func placeOf(categoryType string, entries []scoring.Entry, userID string) Place {
	p := Place{Type: categoryType}
	for _, e := range entries {
		if e.Rank > 0 {
			p.RankedCount++
		}
		if e.UserID == userID {
			p.Entry = e
		}
	}
	return p
}

// prepare returns the window of challenge c at now and the scorer of each of
// its categories.
func (s *Service) prepare(c Challenge, now time.Time) (scoring.Window, []scoring.Scorer, error) {
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return scoring.Window{}, nil, fmt.Errorf("challenge time zone %q: %w", c.Timezone, err)
	}
	scorers := make([]scoring.Scorer, len(c.Categories))
	for i, cat := range c.Categories {
		if scorers[i], err = s.scorerOf(cat); err != nil {
			return scoring.Window{}, nil, err
		}
	}
	return scoring.Window{Start: c.StartsAt, End: c.EndsAt, Location: loc, Now: now}, scorers, nil
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
// are read now, and an account deleted since is left out: the participants
// after it move up.
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
	deleted, err := s.repo.DeletedAccounts(ctx, ids)
	if err != nil {
		return Leaderboard{}, false, err
	}
	// The names of the deleted accounts are not even asked for.
	kept := make([]string, 0, len(ids))
	for _, id := range ids {
		if !deleted[id] {
			kept = append(kept, id)
		}
	}
	names, err := s.profiles.Names(ctx, kept)
	if err != nil {
		return Leaderboard{}, false, fmt.Errorf("read names: %w", err)
	}

	board := Leaderboard{ComputedAt: createdAt, Categories: make([]CategoryRanking, len(c.Categories))}
	for i, cat := range c.Categories {
		ranked := make([]Ranked, 0, len(rankings[i].Entries))
		// The ranks are given again without the deleted accounts, in the
		// frozen order: equal ones still share a rank (1, 1, 3).
		var frozenRank, rank int
		for _, e := range rankings[i].Entries {
			if deleted[e.UserID] {
				continue
			}
			r := Ranked{Entry: scoring.Entry{UserID: e.UserID, Value: e.Value, Abandoned: e.Abandoned}, Name: names[e.UserID]}
			if e.Rank > 0 {
				if e.Rank != frozenRank {
					frozenRank, rank = e.Rank, len(ranked)+1
				}
				r.Rank = rank
			}
			// As computed: a survivor of the elimination category has an empty
			// progress once the challenge is over, an abandoned one has none.
			if cat.Type == CategoryRunElimination && !e.Abandoned {
				r.Progress = &scoring.Progress{}
				if e.EliminatedWeek > 0 {
					r.Progress = &scoring.Progress{Status: scoring.StatusEliminated, EliminatedWeek: e.EliminatedWeek}
				}
			}
			ranked = append(ranked, r)
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
