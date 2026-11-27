package scoring

import (
	"cmp"
	"slices"
	"time"
)

// Elimination is category 3, elimination in the reference sport: the
// distance covered in a week must be at least the distance of the week
// before plus the step, or the participant is eliminated from this
// category, for good.
//
// Sport and StepMeters are the frozen settings of the category. Only the
// activities of that sport count. A week is judged when it locks
// (Window.WeekLocksAt), 7 am on the first day of the next week, never
// before: until then the minimum of the next week may still rise. There is
// no elimination in week 1, which has no reference.
//
// Value is the cumulative distance of a survivor, and of an eliminated
// participant up to the week of the elimination: later activities change
// nothing.
// With an Exercise, a week is the repetitions of it in the activities of the
// sport instead (0 without), the step StepRepetitions, under the same rules,
// Value, Progress and Eliminated in repetitions (README §2.3).
type Elimination struct {
	Sport           string
	StepMeters      int
	Exercise        string
	StepRepetitions int
	// AtRisk is how long before the end of the week a participant under the
	// minimum is at risk (a setting of the service, not of the challenge).
	AtRisk time.Duration
}

var _ Scorer = Elimination{}

// Score ranks the survivors first, by cumulative distance (or repetitions),
// whatever their base; then the eliminated, the last one eliminated first,
// then by cumulative distance up to their elimination.
func (e Elimination) Score(w Window, participants []Participant, activities []Activity) []Entry {
	weeks := w.Weeks()
	weekly := e.weeklyMeasures(w, participants, activities)
	judged := w.LockedWeeks()

	week, weekEnd := w.CurrentWeek()
	progress := make(map[string]*Progress, len(participants))
	values := make(map[string]int, len(participants))
	// A survivor is in the tier above every week of elimination.
	tiers := make(map[string]int, len(participants))
	for user, distances := range weekly {
		last, tier, eliminatedWeek := weeks, weeks+1, 0
		if n := firstEliminatedWeek(distances, judged, e.step()); n > 0 {
			last, tier, eliminatedWeek = n, n, n
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

// Eliminated is a participant eliminated from the category: the first week
// judged under the minimum, the distance covered that week and the minimum,
// or with an exercise the repetitions and their minimum.
type Eliminated struct {
	UserID             string
	Week               int
	DistanceMeters     int
	MinimumMeters      int
	Repetitions        int
	MinimumRepetitions int
}

// step is the increment of the category, in its unit.
func (e Elimination) step() int {
	if e.Exercise != "" {
		return e.StepRepetitions
	}
	return e.StepMeters
}

// Eliminations lists the participants eliminated in the weeks locked at the
// instant of the window, by week then participant. An abandoned participant
// is judged only in the weeks locked when they left (AbandonedAt at or after
// Window.WeekLocksAt, the bound of the points category): an elimination
// judged before they left stays, and the weeks locked after are not judged
// (shown as abandoned, not as eliminated); without AbandonedAt, in none. A
// week judged keeps its eliminations whenever it is computed, abandons
// since included: running it later, or again after an admin change (README
// §2.12), only adds the weeks that locked since.
func (e Elimination) Eliminations(w Window, participants []Participant, activities []Activity) []Eliminated {
	weekly := e.weeklyMeasures(w, participants, activities)
	judged := w.LockedWeeks()
	var out []Eliminated
	for _, p := range participants {
		upTo := judged
		if p.Abandoned {
			upTo = lockedWhenLeft(w, p.AbandonedAt, judged)
		}
		distances := weekly[p.UserID]
		if n := firstEliminatedWeek(distances, upTo, e.step()); n > 0 {
			el := Eliminated{UserID: p.UserID, Week: n, DistanceMeters: distances[n], MinimumMeters: minimumMeters(distances[n-1], e.step())}
			if e.Exercise != "" {
				el.Repetitions, el.MinimumRepetitions, el.DistanceMeters, el.MinimumMeters = el.DistanceMeters, el.MinimumMeters, 0, 0
			}
			out = append(out, el)
		}
	}
	slices.SortFunc(out, func(a, b Eliminated) int {
		return cmp.Or(cmp.Compare(a.Week, b.Week), cmp.Compare(a.UserID, b.UserID))
	})
	return out
}

// lockedWhenLeft is how many of the judged weeks were locked when a
// participant left at left (nil: none).
func lockedWhenLeft(w Window, left *time.Time, judged int) int {
	if left == nil {
		return 0
	}
	n := 0
	for n < judged && !left.Before(w.WeekLocksAt(n+1)) {
		n++
	}
	return n
}

// weeklyMeasures is the distance covered in the sport, or the repetitions
// of the exercise, by each participant in each week: weekly[user][n] for
// week n, index 0 unused.
func (e Elimination) weeklyMeasures(w Window, participants []Participant, activities []Activity) map[string][]int {
	weeks := w.Weeks()
	weekly := make(map[string][]int, len(participants))
	for _, p := range participants {
		weekly[p.UserID] = make([]int, weeks+1)
	}
	for _, a := range activities {
		n := w.WeekOf(a.StartedAt)
		if a.SportCode != e.Sport || n == 0 {
			continue
		}
		measure := a.Repetitions[e.Exercise]
		if e.Exercise == "" {
			if a.DistanceMeters == nil {
				continue
			}
			measure = *a.DistanceMeters
		}
		if distances, ok := weekly[a.UserID]; ok {
			distances[n] += measure
		}
	}
	return weekly
}

// firstEliminatedWeek is the first of the judged weeks, from week 2 (week 1
// has no reference), where the distance misses the minimum; 0 if none.
func firstEliminatedWeek(distances []int, judged, step int) int {
	for n := 2; n <= judged; n++ {
		if isEliminated(distances[n-1], distances[n], step) {
			return n
		}
	}
	return 0
}

// progress is the live situation of a participant eliminated in
// eliminatedWeek (0 if not) while week, ending at weekEnd, is running (0 if
// none), in repetitions with an exercise.
func (e Elimination) progress(distances []int, eliminatedWeek, week int, weekEnd, now time.Time) *Progress {
	p := e.progressRaw(distances, eliminatedWeek, week, weekEnd, now)
	if e.Exercise != "" {
		p.MinimumRepetitions, p.Repetitions = p.MinimumMeters, p.DistanceMeters
		p.MinimumMeters, p.DistanceMeters, p.InRepetitions = nil, 0, true
	}
	return p
}

// progressRaw is progress, its numbers in the meters fields whatever the unit.
func (e Elimination) progressRaw(distances []int, eliminatedWeek, week int, weekEnd, now time.Time) *Progress {
	switch {
	case eliminatedWeek > 0:
		return &Progress{Status: StatusEliminated, EliminatedWeek: eliminatedWeek}
	case week == 0:
		return &Progress{}
	case week == 1:
		return &Progress{DistanceMeters: distances[1], WeekEndsAt: weekEnd}
	}
	minimum := minimumMeters(distances[week-1], e.step())
	p := &Progress{MinimumMeters: &minimum, DistanceMeters: distances[week], WeekEndsAt: weekEnd}
	switch {
	case p.DistanceMeters >= minimum:
		p.Status = StatusReached
	case weekEnd.Sub(now) <= e.AtRisk:
		p.Status = StatusAtRisk
	default:
		p.Status = StatusInProgress
	}
	return p
}

// minimumMeters is the rule of the minimum, the only place it lives: the
// minimum of a week is the distance really covered the week before plus
// the step, so covering more than the minimum raises the next one.
func minimumMeters(previous, step int) int {
	return previous + step
}

// isEliminated reports whether current misses the minimum. Exactly the
// minimum is enough.
func isEliminated(previous, current, step int) bool {
	return current < minimumMeters(previous, step)
}
