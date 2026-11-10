package scoring

import "math"

// Distance is category 2, cumulative distance: the distances of the
// activities brought back to a running distance by the factor of their
// sport, in meters.
//
// Factors are the frozen settings of the category, by sport code: every
// sport with a distance, "other" included, chosen at the creation of the
// challenge. A sport missing from them (yoga…) does not count, nor does an
// activity without distance.
type Distance struct {
	Factors map[string]float64
}

var _ Scorer = Distance{}

// factorScale is the precision of the factors: two decimals.
const factorScale = 100

// Score ranks the participants by cumulative distance in the window. The
// sum is exact and rounded to the meter at the end, once per participant.
func (d Distance) Score(w Window, participants []Participant, activities []Activity) []Entry {
	// Meters × factor in hundredths: an exact integer, no float drift.
	scaled := make(map[string]int)
	for _, a := range activities {
		factor, ok := d.Factors[a.SportCode]
		if !ok || a.DistanceMeters == nil || !w.Contains(a.StartedAt) {
			continue
		}
		scaled[a.UserID] += *a.DistanceMeters * int(math.Round(factor*factorScale))
	}
	totals := make(map[string]int, len(scaled))
	// Half up: distances and factors are positive, so v >= 0.
	for user, v := range scaled {
		totals[user] = (v + factorScale/2) / factorScale
	}
	return Rank(participants, totals)
}
