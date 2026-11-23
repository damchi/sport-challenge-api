package leaderboard

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/scoring"
)

// Breakdown returns where the points of participant userID come from in
// the points category of challenge id, as actor may read its leaderboard
// (the rule is the challenge module's, and nothing is loaded before it
// passes): week by week and criterion by criterion, computed as Get ranks
// them (scoring.Points.Breakdown), so the locked weeks sum to the value of
// the leaderboard and the others to its provisional points. The values
// behind the points are shown only to a reader who may see the activities
// (hideValues). Once the challenge is over and its results frozen, the
// breakdown is the frozen one; results frozen before the breakdown was
// stored are computed, which gives the frozen points unless an account was
// deleted after the freeze (its points then go to the others in the
// breakdown, not in the frozen leaderboard). A challenge without
// a points category, a user who never joined it or whose account was
// deleted, answer 404 not_found.
func (s *Service) Breakdown(ctx context.Context, actor auth.Actor, id, userID string) (Breakdown, error) {
	c, err := s.challenges.Challenge(ctx, actor, id)
	if err != nil {
		return Breakdown{}, err
	}
	var cat *Category
	for i := range c.Categories {
		if c.Categories[i].Type == CategoryPoints {
			cat = &c.Categories[i]
		}
	}
	if cat == nil {
		return Breakdown{}, apperr.New(apperr.CodeNotFound, nil)
	}
	now := s.clock.Now()
	w, _, err := s.prepare(c, now)
	if err != nil {
		return Breakdown{}, err
	}
	if !now.Before(c.EndsAt) {
		b, ok, err := s.frozenBreakdown(ctx, c.ID, w, userID)
		if err != nil || ok {
			return hideValues(b, c.ReadsActivities), err
		}
	}

	byChallenge, err := s.repo.ParticipantsOf(ctx, []string{id})
	if err != nil {
		return Breakdown{}, err
	}
	participants := byChallenge[id]
	var me *scoring.Participant
	for i := range participants {
		if participants[i].UserID == userID {
			me = &participants[i]
		}
	}
	if me == nil {
		return Breakdown{}, apperr.New(apperr.CodeNotFound, nil)
	}
	activitiesOf, err := s.repo.ActivitiesOf(ctx, []Scope{scopeOf(id, c.Categories, w)})
	if err != nil {
		return Breakdown{}, err
	}
	points := scoring.Points{Criteria: cat.Criteria, Podium: cat.Podium, Factors: cat.Factors}
	weeks := points.Breakdown(w, participants, activitiesOf[id])[userID]

	locked := w.LockedWeeks()
	out := Breakdown{UserID: userID, ComputedAt: now, Abandoned: me.Abandoned, Weeks: make([]BreakdownWeek, len(weeks))}
	provisional := 0
	pending := false
	for i, wp := range weeks {
		out.Weeks[i] = weekOf(w, wp.Week, wp.Week <= locked, wp.Points, wp.Criteria)
		if out.Weeks[i].Locked {
			out.Acquired += wp.Points
		} else {
			provisional += wp.Points
			pending = true
		}
	}
	if pending && !me.Abandoned {
		out.Provisional = &provisional
	}
	return hideValues(out, c.ReadsActivities), nil
}

// hideValues leaves out of b the values behind the points, for a reader who
// may not see the activities of the participants (README §5.7: outside its
// members, a public challenge shows its leaderboard only); the ranks and the
// points stay.
func hideValues(b Breakdown, readsActivities bool) Breakdown {
	if readsActivities {
		return b
	}
	b.ValuesHidden = true
	for i := range b.Weeks {
		for j := range b.Weeks[i].Criteria {
			b.Weeks[i].Criteria[j].Value = 0
		}
	}
	return b
}

// frozenBreakdown reads the breakdown of userID in the frozen results of
// challenge id, in window w, and reports false when they are not frozen yet
// or were frozen without it. A user absent from them, or whose account was
// deleted since, answers 404 not_found.
func (s *Service) frozenBreakdown(ctx context.Context, id string, w scoring.Window, userID string) (Breakdown, bool, error) {
	payloads, createdAt, err := s.repo.Snapshots(ctx, id)
	if err != nil {
		return Breakdown{}, false, err
	}
	payload, ok := payloads[CategoryPoints]
	if !ok {
		return Breakdown{}, false, nil
	}
	var r frozenRanking
	if err := json.Unmarshal(payload, &r); err != nil {
		return Breakdown{}, false, fmt.Errorf("decode snapshot of category %s: %w", CategoryPoints, err)
	}
	for _, e := range r.Entries {
		if e.UserID != userID {
			continue
		}
		if e.Weeks == nil {
			return Breakdown{}, false, nil
		}
		deleted, err := s.repo.DeletedAccounts(ctx, []string{userID})
		if err != nil {
			return Breakdown{}, false, err
		}
		if deleted[userID] {
			return Breakdown{}, false, apperr.New(apperr.CodeNotFound, nil)
		}
		out := Breakdown{UserID: userID, ComputedAt: createdAt, Abandoned: e.Abandoned, Frozen: true, Weeks: make([]BreakdownWeek, len(*e.Weeks))}
		for i, fw := range *e.Weeks {
			criteria := make([]scoring.CriterionPoints, len(fw.Criteria))
			for j, c := range fw.Criteria {
				criteria[j] = scoring.CriterionPoints{Criterion: c.Criterion, Rank: c.Rank, Value: c.Value, Points: c.Points}
			}
			out.Weeks[i] = weekOf(w, fw.Week, true, fw.Points, criteria)
			out.Acquired += fw.Points
		}
		return out, true, nil
	}
	return Breakdown{}, false, apperr.New(apperr.CodeNotFound, nil)
}

// weekOf is week n of window w with its points.
func weekOf(w scoring.Window, n int, locked bool, points int, criteria []scoring.CriterionPoints) BreakdownWeek {
	start, end := w.WeekBounds(n)
	return BreakdownWeek{Number: n, StartsAt: start, EndsAt: end, Locked: locked, Points: points, Criteria: criteria}
}
