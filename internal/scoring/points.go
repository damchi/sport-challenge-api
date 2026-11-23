package scoring

// Criteria of the points category.
const (
	CriterionLongestDuration = "longest_duration"
	CriterionLongestDistance = "longest_distance"
	CriterionSessionCount    = "session_count"
	CriterionWeeklyElevation = "weekly_elevation"
)

// Points is the points category: each week, each criterion ranks the
// participants, and the top of each of these weekly rankings scores the
// points of the scale.
//
// Criteria, Podium and Factors are the frozen settings of the category: at
// least one criterion, no duplicate; a podium of 5 or 10; the factors of the
// equivalent distance, as Distance. The minimum duration of a session is
// applied by the repository, which counts the Sessions of each group.
//
// The scorer reads groups of sessions (Activity.LongestSeconds,
// LongestMeters, Sessions, and ElevationMeters, the sum of their elevation
// gains), never DurationSeconds nor DistanceMeters.
type Points struct {
	Criteria []string
	Podium   int
	Factors  map[string]float64
}

var _ Scorer = Points{}

// Score ranks the participants by the points of the locked weeks; those of
// the weeks started but not locked yet are only Provisional. A week locks at
// 7 am on the first day of the next week (Window.WeekLocksAt), so a week
// over stays provisional until then.
//
// An abandoned participant stays in the weekly rankings of the weeks locked
// by the time they left, so the points of those weeks do not move; they
// leave the later ones, and are shown abandoned, without rank nor total.
func (p Points) Score(w Window, participants []Participant, activities []Activity) []Entry {
	started := startedWeeks(w)
	locked := w.LockedWeeks()
	bests := p.weeklyBests(w, started, activities)
	acquired := make(map[string]int, len(participants))
	provisional := make(map[string]int, len(participants))
	for n := 1; n <= started; n++ {
		total := acquired
		if n > locked {
			total = provisional
		}
		ranked := rankedIn(w, n, participants)
		for _, c := range p.Criteria {
			for user, v := range p.weekPoints(ranked, bests[n], c) {
				total[user] += v
			}
		}
	}
	entries := Rank(participants, acquired)
	if started > locked {
		for i := range entries {
			if !entries[i].Abandoned {
				v := provisional[entries[i].UserID]
				entries[i].Provisional = &v
			}
		}
	}
	return entries
}

// best is what a participant did in a week, for each criterion.
type best struct {
	longestSeconds int
	longestMeters  int
	sessions       int
	// elevationMeters is the elevation gain of the week, in real meters.
	elevationMeters int
}

// weeklyBests is, for each started week n, what each user did in it:
// bests[n][user], index 0 unused. A group counts for the week of its
// start, even when a session runs into the next one; outside the window, it
// counts nowhere.
func (p Points) weeklyBests(w Window, started int, activities []Activity) []map[string]best {
	distance := Distance{Factors: p.Factors}
	bests := make([]map[string]best, started+1)
	for n := 1; n <= started; n++ {
		bests[n] = make(map[string]best)
	}
	for _, a := range activities {
		n := w.WeekOf(a.StartedAt)
		if n == 0 || n > started {
			continue
		}
		b := bests[n][a.UserID]
		b.longestSeconds = max(b.longestSeconds, a.LongestSeconds)
		if m := distance.Weighted(a.SportCode, a.LongestMeters); m != nil {
			b.longestMeters = max(b.longestMeters, *m)
		}
		b.sessions += a.Sessions
		if a.ElevationMeters != nil {
			b.elevationMeters += *a.ElevationMeters
		}
		bests[n][a.UserID] = b
	}
	return bests
}

// weekPoints ranks the participants of a week on a criterion and gives the
// points of the scale to its top: equal values share the rank and the
// points, at the edge of the top too; a value of 0 does not score.
func (p Points) weekPoints(ranked []Participant, bests map[string]best, criterion string) map[string]int {
	values := make(map[string]int, len(bests))
	for user, b := range bests {
		switch criterion {
		case CriterionLongestDuration:
			values[user] = b.longestSeconds
		case CriterionLongestDistance:
			values[user] = b.longestMeters
		case CriterionSessionCount:
			values[user] = b.sessions
		case CriterionWeeklyElevation:
			values[user] = b.elevationMeters
		}
	}
	scale := pointsScale(p.Podium)
	out := make(map[string]int)
	for _, e := range Rank(ranked, values) {
		if e.Value > 0 && e.Rank <= len(scale) {
			out[e.UserID] = scale[e.Rank-1]
		}
	}
	return out
}

// pointsScale is the points of each rank of the top, from the first.
func pointsScale(podium int) []int {
	switch podium {
	case 5:
		return []int{10, 8, 6, 4, 2}
	case 10:
		return []int{25, 18, 15, 12, 10, 8, 6, 4, 2, 1}
	}
	return nil
}

// rankedIn lists the participants ranked in week n: the active ones, and
// those who left once the week was locked, ranked as active for it.
func rankedIn(w Window, n int, participants []Participant) []Participant {
	locks := w.WeekLocksAt(n)
	ranked := make([]Participant, 0, len(participants))
	for _, p := range participants {
		if !p.Abandoned || (p.AbandonedAt != nil && !p.AbandonedAt.Before(locks)) {
			ranked = append(ranked, Participant{UserID: p.UserID})
		}
	}
	return ranked
}

// startedWeeks is how many weeks have started at Now: 0 before the
// window, all of them after it.
func startedWeeks(w Window) int {
	if n := w.WeekOf(w.Now); n > 0 || w.Now.Before(w.Start) {
		return n
	}
	return w.Weeks()
}
