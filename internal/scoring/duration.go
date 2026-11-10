package scoring

// Duration is category 1, cumulative time: the durations of all the
// activities, every sport included, in seconds.
type Duration struct{}

var _ Scorer = Duration{}

// Score ranks the participants by cumulative time in the window.
func (Duration) Score(w Window, participants []Participant, activities []Activity) []Entry {
	totals := make(map[string]int)
	for _, a := range activities {
		if w.Contains(a.StartedAt) {
			totals[a.UserID] += a.DurationSeconds
		}
	}
	return Rank(participants, totals)
}
