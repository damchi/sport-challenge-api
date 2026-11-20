package scoring

import "math"

// Distance is the cumulative distance category: the distances of the
// activities brought back to the reference sport by the factor of their
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
	scaled := make(map[string]int)
	for _, a := range activities {
		v, ok := d.scaled(a.SportCode, a.DistanceMeters)
		if !ok || !w.Contains(a.StartedAt) {
			continue
		}
		scaled[a.UserID] += v
	}
	totals := make(map[string]int, len(scaled))
	for user, v := range scaled {
		totals[user] = toMeters(v)
	}
	return Rank(participants, totals)
}

// Weighted is the distance an activity brings to the category, in meters:
// its value in the ranking were it alone, with the same factor and rounding.
// It is nil when the activity does not count for the distance (no distance,
// or a sport without a factor). A total ranked is rounded once, so it may
// differ by a meter or so from the sum of the weighted distances.
func (d Distance) Weighted(sportCode string, distanceMeters *int) *int {
	v, ok := d.scaled(sportCode, distanceMeters)
	if !ok {
		return nil
	}
	m := toMeters(v)
	return &m
}

// scaled is the distance of an activity times the factor of its sport, in
// hundredths of a meter: an exact integer, no float drift. It is false when
// the activity does not count for the distance.
func (d Distance) scaled(sportCode string, distanceMeters *int) (int, bool) {
	factor, ok := d.Factors[sportCode]
	if !ok || distanceMeters == nil {
		return 0, false
	}
	return *distanceMeters * int(math.Round(factor*factorScale)), true
}

// toMeters rounds a scaled distance to the meter, half up: distances and
// factors are positive, so v >= 0.
func toMeters(v int) int {
	return (v + factorScale/2) / factorScale
}
