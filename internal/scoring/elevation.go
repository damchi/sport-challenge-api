package scoring

// Elevation is the cumulative elevation gain category: the elevation gains
// of the activities, in real meters, without factor. Only the sports that
// take an elevation gain have one; an activity without is 0 m.
type Elevation struct{}

var _ Scorer = Elevation{}

// Score ranks the participants by cumulative elevation gain in the window.
func (Elevation) Score(w Window, participants []Participant, activities []Activity) []Entry {
	totals := make(map[string]int)
	for _, a := range activities {
		if a.ElevationMeters != nil && w.Contains(a.StartedAt) {
			totals[a.UserID] += *a.ElevationMeters
		}
	}
	return Rank(participants, totals)
}
