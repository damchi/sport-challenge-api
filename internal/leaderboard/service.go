// Package leaderboard assembles the rankings of a challenge: it loads the
// participants and their activities, and has the scorer of each category
// rank them. Nothing is stored: a ranking is always computed from the raw
// activities.
package leaderboard

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/scoring"
)

// repository is what the service needs from the storage.
type repository interface {
	// ParticipantsOf gives the participants of each challenge, in one query.
	ParticipantsOf(ctx context.Context, challengeIDs []string) (map[string][]scoring.Participant, error)
	// ActivitiesOf gives, in one query, the totals of the activities of the
	// active participants of each challenge started in its window: per
	// participant and week for its weekly sport (abandoned participants
	// included), per participant and sport for the others; with a points
	// category, per participant, week and sport for every sport, abandoned
	// participants included.
	ActivitiesOf(ctx context.Context, scopes []Scope) (map[string][]scoring.Activity, error)
	// Snapshots gives the frozen results of the challenge by category type,
	// and when they were frozen; none until the worker has finalized it.
	Snapshots(ctx context.Context, challengeID string) (map[string]json.RawMessage, time.Time, error)
	// SnapshotsOf gives, in one query, the frozen results of each challenge
	// by category type; none for a challenge not finalized yet.
	SnapshotsOf(ctx context.Context, challengeIDs []string) (map[string]map[string]json.RawMessage, error)
	// DeletedAccounts gives which of ids are deleted (anonymized) accounts,
	// left out of the frozen results.
	DeletedAccounts(ctx context.Context, ids []string) (map[string]bool, error)
}

// Challenges gives challenge challengeID, with its categories, for actor to
// read its leaderboard; it answers 404 challenge_not_found when the rights
// matrix forbids it (module challenge).
type Challenges interface {
	Challenge(ctx context.Context, actor auth.Actor, challengeID string) (Challenge, error)
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
// order, as actor may read it (the rule is the challenge module's, and
// nothing is loaded before it passes). Abandoned participants are listed in
// each category, unranked; deleted accounts are not listed at all. The weeks
// locked at the instant of the clock are judged (7 am, local time, on the first
// day of the next week). Once the challenge is over, the frozen results are
// served; until the worker has frozen them, at the earliest when the last
// week locks, the ranking is computed, which gives the same result once it
// is locked: the weeks are locked by the clock, not by the worker.
func (s *Service) Get(ctx context.Context, actor auth.Actor, id string) (Leaderboard, error) {
	c, err := s.challenges.Challenge(ctx, actor, id)
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
	activitiesOf, err := s.repo.ActivitiesOf(ctx, []Scope{scopeOf(id, c.Categories, w)})
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
		board.Categories[i] = CategoryRanking{Type: cat.Type, RankedCount: rankedCount(entries), Entries: ranked}
	}
	return board, nil
}

// Standings returns where actorID stands in each of challenges that is in
// its weeks at now, by challenge ID: the current week and the actor's line
// in every category, as Get computes it at that instant. now is the caller's,
// so that its own statuses and the standings tell the same instant. A
// challenge outside its weeks, or where the actor is not an active
// participant, is left out. The challenges are the caller's list of the
// actor's own; however many, they are read in two queries.
func (s *Service) Standings(ctx context.Context, actorID string, challenges []Challenge, now time.Time) (map[string]Standing, error) {
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
			scopes = append(scopes, scopeOf(r.c.ID, r.c.Categories, r.w))
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
	p := Place{Type: categoryType, RankedCount: rankedCount(entries)}
	for _, e := range entries {
		if e.UserID == userID {
			p.Entry = e
		}
	}
	return p
}

// rankedCount is how many of the ranking entries of a category are ranked:
// an abandoned participant has no rank. The count of the leaderboard and of
// the standings.
func rankedCount(entries []scoring.Entry) int {
	n := 0
	for _, e := range entries {
		if e.Rank > 0 {
			n++
		}
	}
	return n
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
	case CategoryElimination:
		return scoring.Elimination{Sport: cat.Sport, StepMeters: cat.StepMeters, AtRisk: s.atRisk}, nil
	case CategoryPoints:
		return scoring.Points{Criteria: cat.Criteria, Podium: cat.Podium, Factors: cat.Factors}, nil
	case CategoryElevation:
		return scoring.Elevation{}, nil
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
	payloads, createdAt, err := s.repo.Snapshots(ctx, c.ID)
	if err != nil {
		return Leaderboard{}, false, err
	}
	rankings, ok, err := decodeFrozen(c.Categories, payloads)
	if err != nil || !ok {
		return Leaderboard{}, false, err
	}
	ids := frozenUsers(nil, rankings)
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
		entries := thaw(cat.Type, rankings[i], deleted)
		ranked := make([]Ranked, len(entries))
		for j, e := range entries {
			ranked[j] = Ranked{Entry: e, Name: names[e.UserID]}
		}
		board.Categories[i] = CategoryRanking{Type: cat.Type, RankedCount: rankedCount(entries), Entries: ranked}
	}
	return board, true, nil
}

// Results returns actorID's line in each category of the frozen results of
// challenges, in category order, by challenge ID: the lines of Get once the
// challenge is over, deleted accounts left out and the participants after
// them moving up. A line where actorID is not in the frozen ranking has a
// zero entry (no rank). A challenge whose results are not all frozen yet is
// left out. However many, the challenges are read in two queries.
func (s *Service) Results(ctx context.Context, actorID string, challenges []Challenge) (map[string][]Place, error) {
	out := make(map[string][]Place, len(challenges))
	if len(challenges) == 0 {
		return out, nil
	}
	ids := make([]string, len(challenges))
	for i, c := range challenges {
		ids[i] = c.ID
	}
	payloads, err := s.repo.SnapshotsOf(ctx, ids)
	if err != nil {
		return nil, err
	}
	frozenOf := make(map[string][]frozenRanking, len(challenges))
	var users []string
	for _, c := range challenges {
		rankings, ok, err := decodeFrozen(c.Categories, payloads[c.ID])
		if err != nil {
			return nil, err
		}
		if ok {
			frozenOf[c.ID] = rankings
			users = frozenUsers(users, rankings)
		}
	}
	if len(frozenOf) == 0 {
		return out, nil
	}
	deleted, err := s.repo.DeletedAccounts(ctx, users)
	if err != nil {
		return nil, err
	}
	for _, c := range challenges {
		rankings, ok := frozenOf[c.ID]
		if !ok {
			continue
		}
		places := make([]Place, len(c.Categories))
		for i, cat := range c.Categories {
			places[i] = placeOf(cat.Type, thaw(cat.Type, rankings[i], deleted), actorID)
		}
		out[c.ID] = places
	}
	return out, nil
}

// decodeFrozen decodes the frozen ranking of each of categories, in their
// order, from payloads by category type, and reports false when one is not
// there yet, or when there is no category.
func decodeFrozen(categories []Category, payloads map[string]json.RawMessage) ([]frozenRanking, bool, error) {
	if len(categories) == 0 {
		return nil, false, nil
	}
	rankings := make([]frozenRanking, len(categories))
	for i, cat := range categories {
		payload, ok := payloads[cat.Type]
		if !ok {
			return nil, false, nil
		}
		if err := json.Unmarshal(payload, &rankings[i]); err != nil {
			return nil, false, fmt.Errorf("decode snapshot of category %s: %w", cat.Type, err)
		}
	}
	return rankings, true, nil
}

// frozenUsers appends to ids the users of rankings not in it yet, in their
// order.
func frozenUsers(ids []string, rankings []frozenRanking) []string {
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		seen[id] = true
	}
	for _, r := range rankings {
		for _, e := range r.Entries {
			if !seen[e.UserID] {
				seen[e.UserID] = true
				ids = append(ids, e.UserID)
			}
		}
	}
	return ids
}

// thaw returns the entries of the frozen ranking r of a category of type
// categoryType without the deleted accounts: the ranks are given again, in
// the frozen order, and equal ones still share a rank (1, 1, 3).
func thaw(categoryType string, r frozenRanking, deleted map[string]bool) []scoring.Entry {
	entries := make([]scoring.Entry, 0, len(r.Entries))
	var frozenRank, rank int
	for _, e := range r.Entries {
		if deleted[e.UserID] {
			continue
		}
		entry := scoring.Entry{UserID: e.UserID, Value: e.Value, Abandoned: e.Abandoned}
		if e.Rank > 0 {
			if e.Rank != frozenRank {
				frozenRank, rank = e.Rank, len(entries)+1
			}
			entry.Rank = rank
		}
		// As computed: a survivor of the elimination category has an empty
		// progress once the challenge is over, an abandoned one has none.
		if categoryType == CategoryElimination && !e.Abandoned {
			entry.Progress = &scoring.Progress{}
			if e.EliminatedWeek > 0 {
				entry.Progress = &scoring.Progress{Status: scoring.StatusEliminated, EliminatedWeek: e.EliminatedWeek}
			}
		}
		entries = append(entries, entry)
	}
	return entries
}

// scopeOf is what the activities of challenge id, with categories, are read
// for in window w: the sport of its elimination category summed per week,
// the groups of its points category, and the elevation gains of its
// cumulative elevation category or of the weekly elevation criterion of its
// points category; none without such a category.
func scopeOf(id string, categories []Category, w scoring.Window) Scope {
	sc := Scope{ChallengeID: id, Window: w}
	for _, cat := range categories {
		switch cat.Type {
		case CategoryElimination:
			sc.WeeklySport = cat.Sport
		case CategoryPoints:
			sc.Points = &PointsRead{MinSessionSeconds: cat.MinSessionSeconds}
			if slices.Contains(cat.Criteria, scoring.CriterionWeeklyElevation) {
				sc.Elevation = true
			}
		case CategoryElevation:
			sc.Elevation = true
		}
	}
	return sc
}
