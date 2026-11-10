package scoring

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

	values := make(map[string]int, len(participants))
	// A survivor is in the tier above every week of elimination.
	tiers := make(map[string]int, len(participants))
	for user, distances := range weekly {
		last, tier := weeks, weeks+1
		for n := 2; n <= judged; n++ {
			if isEliminated(distances[n-1], distances[n], e.StepMeters) {
				last, tier = n, n
				break
			}
		}
		for _, d := range distances[1 : last+1] {
			values[user] += d
		}
		tiers[user] = tier
	}
	return rankByTier(participants, values, tiers)
}

// isEliminated is the rule of the minimum, the only place it lives: the
// minimum of a week is the distance really run the week before plus the
// step, so running more than the minimum raises the next one. Exactly the
// minimum is enough.
func isEliminated(previous, current, step int) bool {
	return current < previous+step
}
