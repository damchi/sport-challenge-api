package scoring

import (
	"strconv"
	"testing"
	"time"
)

// benchChallenge is a challenge of 5,000 participants over 52 weeks, one
// group of sessions per participant, week and sport (3 sports): 780,000
// groups, as the repository gives them to the points category. Its window
// is read at the end, every week locked.
func benchChallenge(b *testing.B) (Window, []Participant, []Activity) {
	b.Helper()
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		b.Fatal(err)
	}
	const users, weeks = 5000, 52
	start := StartOfDay(2026, 1, 5, loc)
	w := Window{Start: start, End: StartOfDay(2026, 1, 5+7*weeks, loc), Location: loc}
	w.Now = w.End.Add(24 * time.Hour)
	participants := make([]Participant, users)
	activities := make([]Activity, 0, users*weeks*3)
	for u := range users {
		id := strconv.Itoa(u)
		participants[u] = Participant{UserID: id}
		for n := range weeks {
			at := StartOfDay(2026, 1, 5+7*n+u%7, loc).Add(time.Duration(u%20) * time.Hour)
			for i, sport := range []string{"run", "ride", "swim"} {
				meters := 1000 + u%5000 + 100*n + i
				activities = append(activities, Activity{
					UserID: id, SportCode: sport, StartedAt: at, DurationSeconds: 1800 + u%600,
					DistanceMeters: &meters, LongestSeconds: 1800 + u%600, LongestMeters: &meters, Sessions: 1 + u%3,
				})
			}
		}
	}
	return w, participants, activities
}

// BenchmarkScore5000 measures the scoring of one leaderboard read of a large
// challenge, the database aside (BenchmarkActivities5000, module
// leaderboard): the points and elimination categories cut the weeks, the
// cumulative time does not and is the reference.
func BenchmarkScore5000(b *testing.B) {
	w, participants, activities := benchChallenge(b)
	scorers := []struct {
		name   string
		scorer Scorer
	}{
		{"cumulative time", Duration{}},
		{"points", Points{
			Criteria: []string{CriterionLongestDuration, CriterionLongestDistance, CriterionSessionCount}, Podium: 10,
			Factors: map[string]float64{"run": 1, "ride": 0.3, "swim": 1},
		}},
		{"elimination", Elimination{Sport: "run", StepMeters: 100}},
	}
	for _, s := range scorers {
		b.Run(s.name, func(b *testing.B) {
			for b.Loop() {
				s.scorer.Score(w, participants, activities)
			}
		})
	}
}
