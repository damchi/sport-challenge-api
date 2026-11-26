package scoring

import (
	"cmp"
	"slices"
)

// Criteria of the points category.
const (
	CriterionLongestDuration = "longest_duration"
	CriterionLongestDistance = "longest_distance"
	CriterionSessionCount    = "session_count"
	CriterionWeeklyElevation = "weekly_elevation"
	CriterionFastestRun      = "fastest_run"
)

// RunSport is the sport of the fastest run criterion, whatever the
// reference sport of the challenge.
const RunSport = "run"

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
// LongestMeters, Sessions, ElevationMeters, the sum of their elevation
// gains, and FastestSeconds and FastestMeters), never DurationSeconds nor
// DistanceMeters.
type Points struct {
	Criteria []string
	Podium   int
	Factors  map[string]float64
}

var _ Scorer = Points{}

// Score ranks the participants by the points of the locked weeks; those of
// the weeks started but not locked yet are only Provisional. A week locks at
// 7 am on the first day of the next week (Window.WeekLocksAt), so a week
// over stays provisional until then. The points are those of Breakdown,
// summed.
//
// An abandoned participant stays in the weekly rankings of the weeks locked
// by the time they left, so the points of those weeks do not move; they
// leave the later ones, and are shown abandoned, without rank nor total.
func (p Points) Score(w Window, participants []Participant, activities []Activity) []Entry {
	locked := w.LockedWeeks()
	acquired := make(map[string]int, len(participants))
	provisional := make(map[string]int, len(participants))
	for user, weeks := range p.Breakdown(w, participants, activities) {
		for _, wp := range weeks {
			if wp.Week <= locked {
				acquired[user] += wp.Points
			} else {
				provisional[user] += wp.Points
			}
		}
	}
	entries := Rank(participants, acquired)
	if startedWeeks(w) > locked {
		for i := range entries {
			if !entries[i].Abandoned {
				v := provisional[entries[i].UserID]
				entries[i].Provisional = &v
			}
		}
	}
	return entries
}

// WeekPoints is what a participant scored in a week of the points category:
// the line of each criterion, in the order of the criteria of the category,
// and their sum.
type WeekPoints struct {
	Week     int
	Points   int
	Criteria []CriterionPoints
}

// CriterionPoints is the line of a participant in the ranking of a
// criterion for a week: their rank in it (0 without a value, which does
// not score), their value (seconds, equivalent meters, sessions, meters of
// elevation gain or seconds per km, by criterion) and the points of the
// scale it gives.
type CriterionPoints struct {
	Criterion string
	Rank      int
	Value     int
	Points    int
}

// Breakdown gives, by participant, the points of each week started at
// Now, in order: those Score sums (acquired once the week is locked,
// provisional before). A participant is in the weeks they are ranked in:
// all of them while active; once abandoned, those locked by the time they
// left, so that their points do not move.
//
// Participants are indexed by position, and the buffers of a week are
// reused for the next one: a large challenge ranks thousands of
// participants on each criterion of each week.
func (p Points) Breakdown(w Window, participants []Participant, activities []Activity) map[string][]WeekPoints {
	started := startedWeeks(w)
	bests := p.weeklyBests(w, started, participants, activities)
	scale := pointsScale(p.Podium)
	nc := len(p.Criteria)
	weeks := make([][]WeekPoints, len(participants))
	ranked := make([]int, 0, len(participants))
	totals := make([]int, len(participants))
	lines := make([]valueAt, 0, len(participants))
	for n := 1; n <= started; n++ {
		ranked = rankedIn(w, n, participants, ranked[:0])
		week := bests[(n-1)*len(participants) : n*len(participants)]
		criteria := make([]CriterionPoints, len(ranked)*nc)
		totals = totals[:len(ranked)]
		clear(totals)
		for c, criterion := range p.Criteria {
			lines = lines[:0]
			for k, i := range ranked {
				v, none := week[i].of(criterion)
				lines = append(lines, valueAt{k: k, value: v, none: none})
			}
			for _, l := range weekRanking(lines, scale, criterion == CriterionFastestRun) {
				criteria[l.k*nc+c] = CriterionPoints{Criterion: criterion, Rank: l.rank, Value: l.value, Points: l.points}
				totals[l.k] += l.points
			}
		}
		for k, i := range ranked {
			if weeks[i] == nil {
				weeks[i] = make([]WeekPoints, 0, started)
			}
			weeks[i] = append(weeks[i], WeekPoints{Week: n, Points: totals[k], Criteria: criteria[k*nc : (k+1)*nc : (k+1)*nc]})
		}
	}
	out := make(map[string][]WeekPoints, len(participants))
	for i, pt := range participants {
		if weeks[i] != nil {
			out[pt.UserID] = weeks[i]
		}
	}
	return out
}

// best is what a participant did in a week, for each criterion.
type best struct {
	longestSeconds int
	longestMeters  int
	sessions       int
	// elevationMeters is the elevation gain of the week, in real meters.
	elevationMeters int
	// fastestPace is the lowest pace of a run of the week, in seconds per
	// km, when hasPace: a pace of 0 is a value.
	fastestPace int
	hasPace     bool
}

// of is the value of b on criterion, and none when it has no value, which
// does not score: no run for the fastest run, 0 for the others.
func (b best) of(criterion string) (value int, none bool) {
	switch criterion {
	case CriterionLongestDuration:
		value = b.longestSeconds
	case CriterionLongestDistance:
		value = b.longestMeters
	case CriterionSessionCount:
		value = b.sessions
	case CriterionWeeklyElevation:
		value = b.elevationMeters
	case CriterionFastestRun:
		return b.fastestPace, !b.hasPace
	}
	return value, value <= 0
}

// pace is the pace of a session in seconds per km, rounded to the second,
// half a second up (README §2.3), in exact integer arithmetic. meters is
// positive.
func pace(seconds, meters int) int {
	return (2000*seconds + meters) / (2 * meters)
}

// weeklyBests is what each participant did in each started week n:
// bests[(n-1)*len(participants)+i] for participants[i]. A group counts for
// the week of its start, even when a session runs into the next one;
// outside the window, or of a user who is not a participant, it counts
// nowhere.
func (p Points) weeklyBests(w Window, started int, participants []Participant, activities []Activity) []best {
	index := make(map[string]int, len(participants))
	for i, pt := range participants {
		index[pt.UserID] = i
	}
	distance := Distance{Factors: p.Factors}
	bests := make([]best, started*len(participants))
	// The groups of a participant come one after the other: look their
	// position up once.
	user, i, ok := "", 0, false
	for _, a := range activities {
		if a.UserID != user || user == "" {
			user = a.UserID
			i, ok = index[user]
		}
		n := w.WeekOf(a.StartedAt)
		if n == 0 || n > started || !ok {
			continue
		}
		b := &bests[(n-1)*len(participants)+i]
		b.longestSeconds = max(b.longestSeconds, a.LongestSeconds)
		if m := distance.Weighted(a.SportCode, a.LongestMeters); m != nil {
			b.longestMeters = max(b.longestMeters, *m)
		}
		b.sessions += a.Sessions
		if a.ElevationMeters != nil {
			b.elevationMeters += *a.ElevationMeters
		}
		if a.SportCode == RunSport && a.FastestMeters != nil && *a.FastestMeters > 0 {
			if v := pace(a.FastestSeconds, *a.FastestMeters); !b.hasPace || v < b.fastestPace {
				b.fastestPace, b.hasPace = v, true
			}
		}
	}
	return bests
}

// valueAt is the value of the participant at position k of the ranked
// participants of a week, on a criterion, and what it gives in the ranking;
// none when the participant has no value on it.
type valueAt struct {
	k, value, rank, points int
	none                   bool
}

// weekRanking ranks the values of a week on a criterion, highest first, or
// lowest first when ascending (the fastest run), and gives the points of
// the scale to its top: equal values share the rank and the points, at the
// edge of the top too; a line without value has no rank and does not
// score. It sorts lines in place and returns them. It shares the ties of
// Rank, on positions rather than user IDs: the order within a tie does not
// matter here.
func weekRanking(lines []valueAt, scale []int, ascending bool) []valueAt {
	slices.SortFunc(lines, func(a, b valueAt) int {
		if a.none != b.none {
			if a.none {
				return 1
			}
			return -1
		}
		if ascending {
			return cmp.Compare(a.value, b.value)
		}
		return cmp.Compare(b.value, a.value)
	})
	for j := range lines {
		l := &lines[j]
		switch {
		case l.none:
			l.rank, l.value = 0, 0
		case j > 0 && l.value == lines[j-1].value:
			l.rank = lines[j-1].rank
		default:
			l.rank = j + 1
		}
		if l.rank > 0 && l.rank <= len(scale) {
			l.points = scale[l.rank-1]
		}
	}
	return lines
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

// rankedIn appends to ranked the positions of the participants ranked in
// week n: the active ones, and those who left once the week was locked,
// ranked as active for it.
func rankedIn(w Window, n int, participants []Participant, ranked []int) []int {
	locks := w.WeekLocksAt(n)
	for i, p := range participants {
		if !p.Abandoned || (p.AbandonedAt != nil && !p.AbandonedAt.Before(locks)) {
			ranked = append(ranked, i)
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
