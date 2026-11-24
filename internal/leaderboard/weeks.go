package leaderboard

import (
	"context"

	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/scoring"
)

// Weeks is where the weeks of a challenge stand at an instant: the week in
// progress (0 outside the window), how many are locked (weeks 1 to
// Locked), and the points of each participant in each week begun of the
// points category, by user then week number (nil without a points
// category): acquired in a locked week, provisional in the others.
type Weeks struct {
	Current int
	Locked  int
	Points  map[string]map[int]int
}

// Weeks returns where the weeks of challenge id stand at the clock instant,
// as actor may read its leaderboard (the rule is the challenge module's).
// The points are computed from the activities as Get ranks them
// (scoring.Points.Breakdown), frozen results or not: an admin preview
// compares them before and after a change, which freezes them again.
func (s *Service) Weeks(ctx context.Context, actor auth.Actor, id string) (Weeks, error) {
	c, err := s.challenges.Challenge(ctx, actor, id)
	if err != nil {
		return Weeks{}, err
	}
	w, _, err := s.prepare(c, s.clock.Now())
	if err != nil {
		return Weeks{}, err
	}
	current, _ := w.CurrentWeek()
	out := Weeks{Current: current, Locked: w.LockedWeeks()}
	var cat *Category
	for i := range c.Categories {
		if c.Categories[i].Type == CategoryPoints {
			cat = &c.Categories[i]
		}
	}
	if cat == nil {
		return out, nil
	}
	byChallenge, err := s.repo.ParticipantsOf(ctx, []string{id})
	if err != nil {
		return Weeks{}, err
	}
	activitiesOf, err := s.repo.ActivitiesOf(ctx, []Scope{scopeOf(id, c.Categories, w)})
	if err != nil {
		return Weeks{}, err
	}
	points := scoring.Points{Criteria: cat.Criteria, Podium: cat.Podium, Factors: cat.Factors}
	breakdown := points.Breakdown(w, byChallenge[id], activitiesOf[id])
	out.Points = make(map[string]map[int]int, len(breakdown))
	for user, weeks := range breakdown {
		out.Points[user] = make(map[int]int, len(weeks))
		for _, wp := range weeks {
			out.Points[user][wp.Week] = wp.Points
		}
	}
	return out, nil
}
