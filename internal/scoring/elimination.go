package scoring

import "time"

// Elimination is category 3, run with elimination: the distance run in a
// week must be at least the distance of the week before plus the step, or
// the participant is eliminated from this category, for good.
//
// Sport and StepMeters are the frozen settings of the category. Only the
// runs count. A week is judged at the exact instant it ends, never before;
// there is no elimination in week 1, which has no reference.
//
// Value is the cumulative distance of a survivor, and of an eliminated
// participant up to the week of the elimination: later runs change nothing.
type Elimination struct {
	Sport      string
	StepMeters int
	// AtRisk is how long before the end of the week a participant under the
	// minimum is at risk (a setting of the service, not of the challenge).
	AtRisk time.Duration
}

var _ Scorer = Elimination{}

// Score ranks the survivors first, by cumulative distance, whatever their
// base; then the eliminated, the last one eliminated first, then by
// cumulative distance up to their elimination.
func (e Elimination) Score(w Window, participants []Participant, activities []Activity) []Entry {
	weeks := w.Weeks()
	// weekly[user][n] is the distance run in week n; index 0 is unused.
	weekly := make(map[string][]int, len(participants))
	for _, p := range participants {
		weekly[p.UserID] = make([]int, weeks+1)
	}
	for _, a := range activities {
		n := w.WeekOf(a.StartedAt)
		if a.SportCode != e.Sport || a.DistanceMeters == nil || n == 0 {
			continue
		}
		if distances, ok := weekly[a.UserID]; ok {
			distances[n] += *a.DistanceMeters
		}
	}

	judged := 0
	for n := 1; n <= weeks; n++ {
		if _, end := w.WeekBounds(n); !w.Now.Before(end) {
			judged = n
		}
	}

	week, weekEnd := w.CurrentWeek()
	progress := make(map[string]*Progress, len(participants))
	values := make(map[string]int, len(participants))
	// A survivor is in the tier above every week of elimination.
	tiers := make(map[string]int, len(participants))
	for user, distances := range weekly {
		last, tier, eliminatedWeek := weeks, weeks+1, 0
		for n := 2; n <= judged; n++ {
			if isEliminated(distances[n-1], distances[n], e.StepMeters) {
				last, tier, eliminatedWeek = n, n, n
				break
			}
		}
		progress[user] = e.progress(distances, eliminatedWeek, week, weekEnd, w.Now)
		for _, d := range distances[1 : last+1] {
			values[user] += d
		}
		tiers[user] = tier
	}
	entries := rankByTier(participants, values, tiers)
	for i := range entries {
		if !entries[i].Abandoned {
			entries[i].Progress = progress[entries[i].UserID]
		}
	}
	return entries
}

// progress is the live situation of a participant eliminated in
// eliminatedWeek (0 if not) while week, ending at weekEnd, is running (0 if
// none).
func (e Elimination) progress(distances []int, eliminatedWeek, week int, weekEnd, now time.Time) *Progress {
	switch {
	case eliminatedWeek > 0:
		return &Progress{Status: StatusEliminated, EliminatedWeek: eliminatedWeek}
	case week == 0:
		return &Progress{}
	case week == 1:
		return &Progress{RunMeters: distances[1], WeekEndsAt: weekEnd}
	}
	minimum := minimumMeters(distances[week-1], e.StepMeters)
	p := &Progress{MinimumMeters: &minimum, RunMeters: distances[week], WeekEndsAt: weekEnd}
	switch {
	case p.RunMeters >= minimum:
		p.Status = StatusReached
	case weekEnd.Sub(now) <= e.AtRisk:
		p.Status = StatusAtRisk
	default:
		p.Status = StatusInProgress
	}
	return p
}

// minimumMeters is the rule of the minimum, the only place it lives: the
// minimum of a week is the distance really run the week before plus the
// step, so running more than the minimum raises the next one.
func minimumMeters(previous, step int) int {
	return previous + step
}

// isEliminated reports whether current misses the minimum. Exactly the
// minimum is enough.
func isEliminated(previous, current, step int) bool {
	return current < minimumMeters(previous, step)
}
