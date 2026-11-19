package activity

import (
	"reflect"
	"slices"
	"testing"
	"time"
)

func TestCanChange(t *testing.T) {
	tests := []struct {
		name     string
		actorID  string
		authorID string
		want     bool
	}{
		{name: "author", actorID: "u1", authorID: "u1", want: true},
		{name: "someone else: forbidden", actorID: "u2", authorID: "u1"},
		{name: "no actor: forbidden", actorID: "", authorID: "u1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanChange(tt.actorID, tt.authorID); got != tt.want {
				t.Fatalf("CanChange(%q, %q) = %v, want %v", tt.actorID, tt.authorID, got, tt.want)
			}
		})
	}
}

// participation is a challenge of weeks weeks from local midnight of day in
// Toronto, where the author is an active participant.
func participation(t *testing.T, day [3]int, weeks int) Participation {
	t.Helper()
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(day[0], time.Month(day[1]), day[2], 0, 0, 0, 0, loc)
	return Participation{StartsAt: start.UTC(), EndsAt: start.AddDate(0, 0, 7*weeks).UTC(), Location: loc, Active: true}
}

// toronto is local time in Toronto, in UTC as the clock gives it.
func toronto(t *testing.T, y, mo, d, h int) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	return time.Date(y, time.Month(mo), d, h, 0, 0, 0, loc).UTC()
}

func TestIsLocked(t *testing.T) {
	// Monday 2026-06-01, 3 weeks: week 1 ends on Monday 2026-06-08.
	monday := participation(t, [3]int{2026, 6, 1}, 3)
	// Thursday 2026-06-04, 3 weeks: its weeks are shifted by 3 days, week 1
	// ends on Thursday 2026-06-11.
	thursday := participation(t, [3]int{2026, 6, 4}, 3)
	// Clocks go forward on 2026-03-08, inside week 1 (167 h).
	springForward := participation(t, [3]int{2026, 3, 2}, 2)
	// Clocks go back on 2026-11-01, inside week 1 (169 h).
	fallBack := participation(t, [3]int{2026, 10, 26}, 2)
	// Finished on 2026-05-11 (2 weeks from 2026-04-27).
	finished := participation(t, [3]int{2026, 4, 27}, 2)

	// The same Monday in Vancouver, 3 hours behind Toronto.
	pacific, err := time.LoadLocation("America/Vancouver")
	if err != nil {
		t.Fatal(err)
	}
	vancouverStart := time.Date(2026, 6, 1, 0, 0, 0, 0, pacific)
	vancouver := Participation{StartsAt: vancouverStart.UTC(), EndsAt: vancouverStart.AddDate(0, 0, 21).UTC(), Location: pacific, Active: true}
	vancouverEnd1 := time.Date(2026, 6, 8, 0, 0, 0, 0, pacific).UTC()

	noZone := monday
	noZone.Location = nil
	emptyWindow := monday
	emptyWindow.EndsAt = emptyWindow.StartsAt

	// Abandoned, excluded, or a creator who abandoned: no longer competes.
	notActive := monday
	notActive.Active = false

	tuesday := toronto(t, 2026, 6, 2, 10)      // week 1 of monday, before thursday starts
	friday := toronto(t, 2026, 6, 5, 10)       // week 1 of both
	endMonday1 := toronto(t, 2026, 6, 8, 0)    // end of week 1 of monday
	endThursday1 := toronto(t, 2026, 6, 11, 0) // end of week 1 of thursday
	endSpring1 := toronto(t, 2026, 3, 9, 0)    // 167 h after the start
	endFall1 := toronto(t, 2026, 11, 2, 0)     // 169 h after the start
	sec := time.Second

	tests := []struct {
		name       string
		now        time.Time
		startedAt  time.Time
		challenges []Participation
		want       bool
	}{
		{name: "no challenge", now: endMonday1, startedAt: tuesday},
		{name: "one second before the end of the week", now: endMonday1.Add(-sec), startedAt: tuesday, challenges: []Participation{monday}},
		{name: "at the end of the week", now: endMonday1, startedAt: tuesday, challenges: []Participation{monday}, want: true},
		{name: "last second of the week, judged at its end", now: endMonday1, startedAt: endMonday1.Add(-sec), challenges: []Participation{monday}, want: true},
		{name: "first second of the next week stays open", now: endMonday1, startedAt: endMonday1, challenges: []Participation{monday}},
		{name: "week 1 ended: locked from the start of the window", now: endMonday1, startedAt: monday.StartsAt, challenges: []Participation{monday}, want: true},
		{name: "week 2 open while week 1 is locked", now: endMonday1.Add(time.Hour), startedAt: endMonday1.Add(time.Minute), challenges: []Participation{monday}},
		{name: "week 2 locked at its end", now: toronto(t, 2026, 6, 15, 0), startedAt: endMonday1.Add(time.Minute), challenges: []Participation{monday}, want: true},
		{name: "before the window: counts nowhere", now: endMonday1, startedAt: monday.StartsAt.Add(-sec), challenges: []Participation{monday}},

		// Two challenges whose weeks are shifted: one is enough to lock.
		{name: "shifted weeks: open in both", now: endMonday1.Add(-sec), startedAt: friday, challenges: []Participation{monday, thursday}},
		{name: "shifted weeks: locked by the first to end", now: endMonday1, startedAt: friday, challenges: []Participation{monday, thursday}, want: true},
		{name: "shifted weeks: order does not matter", now: endMonday1, startedAt: friday, challenges: []Participation{thursday, monday}, want: true},
		{name: "shifted weeks: outside the second window", now: endThursday1.Add(-sec), startedAt: tuesday, challenges: []Participation{thursday}},
		{name: "shifted weeks: second challenge only", now: endThursday1, startedAt: friday, challenges: []Participation{thursday}, want: true},

		// Two time zones: the same Monday ends 3 hours later in Vancouver.
		{name: "time zones: open in Vancouver after Toronto's midnight", now: endMonday1, startedAt: tuesday, challenges: []Participation{vancouver}},
		{name: "time zones: locked at Vancouver's midnight", now: vancouverEnd1, startedAt: tuesday, challenges: []Participation{vancouver}, want: true},
		{name: "time zones: Toronto locks first", now: endMonday1, startedAt: tuesday, challenges: []Participation{vancouver, monday}, want: true},

		// Time changes: weeks end at local midnight, 7 local days later.
		{name: "spring forward: one second before (167 h)", now: endSpring1.Add(-sec), startedAt: springForward.StartsAt, challenges: []Participation{springForward}},
		{name: "spring forward: at the end", now: endSpring1, startedAt: springForward.StartsAt, challenges: []Participation{springForward}, want: true},
		{name: "spring forward: not 7 × 24 h", now: springForward.StartsAt.Add(7*24*time.Hour - sec), startedAt: springForward.StartsAt, challenges: []Participation{springForward}, want: true},
		{name: "fall back: one second before (169 h)", now: endFall1.Add(-sec), startedAt: fallBack.StartsAt, challenges: []Participation{fallBack}},
		{name: "fall back: at the end", now: endFall1, startedAt: fallBack.StartsAt, challenges: []Participation{fallBack}, want: true},
		{name: "fall back: still open after 7 × 24 h", now: fallBack.StartsAt.Add(7 * 24 * time.Hour), startedAt: fallBack.StartsAt, challenges: []Participation{fallBack}},

		// A finished challenge locks its window.
		{name: "finished challenge", now: toronto(t, 2026, 11, 9, 12), startedAt: toronto(t, 2026, 5, 10, 8), challenges: []Participation{finished}, want: true},
		{name: "after a finished challenge: counts nowhere", now: toronto(t, 2026, 11, 9, 12), startedAt: finished.EndsAt, challenges: []Participation{finished}},

		// Broken data fails closed or counts nowhere, never panics.
		{name: "no time zone: locked", now: endMonday1.Add(-sec), startedAt: tuesday, challenges: []Participation{noZone}, want: true},
		{name: "no time zone: outside its window, counts nowhere", now: endMonday1, startedAt: noZone.StartsAt.Add(-sec), challenges: []Participation{noZone}},
		{name: "empty window: counts nowhere", now: endMonday1, startedAt: tuesday, challenges: []Participation{emptyWindow}},

		// Ignored: where the activity does not count.
		{name: "not an active participant: ignored", now: endMonday1, startedAt: tuesday, challenges: []Participation{notActive}},
		{name: "ignored challenge does not hide an active one", now: endMonday1, startedAt: tuesday, challenges: []Participation{notActive, monday}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsLocked(tt.now, tt.startedAt, tt.challenges); got != tt.want {
				t.Fatalf("IsLocked(now %s, started %s) = %v, want %v", tt.now, tt.startedAt, got, tt.want)
			}
		})
	}
}

func TestContributions(t *testing.T) {
	// Monday 2026-06-01 and Thursday 2026-06-04, 3 weeks each.
	monday := participation(t, [3]int{2026, 6, 1}, 3)
	monday.ChallengeID = "monday"
	thursday := participation(t, [3]int{2026, 6, 4}, 3)
	thursday.ChallengeID = "thursday"
	finished := participation(t, [3]int{2026, 4, 27}, 2)
	finished.ChallengeID = "finished"
	notActive := monday
	notActive.ChallengeID, notActive.Active = "abandoned", false

	tests := []struct {
		name       string
		startedAt  time.Time
		challenges []Participation
		want       []string
	}{
		{name: "no challenge", startedAt: toronto(t, 2026, 6, 2, 10)},
		{name: "outside every window", startedAt: toronto(t, 2026, 1, 2, 10), challenges: []Participation{monday, thursday, finished}},
		{name: "one window", startedAt: toronto(t, 2026, 6, 2, 10), challenges: []Participation{monday, thursday}, want: []string{"monday"}},
		{name: "two windows, in their order", startedAt: toronto(t, 2026, 6, 5, 10), challenges: []Participation{thursday, monday}, want: []string{"thursday", "monday"}},
		{name: "start of the window included", startedAt: monday.StartsAt, challenges: []Participation{monday}, want: []string{"monday"}},
		{name: "end of the window excluded", startedAt: monday.EndsAt, challenges: []Participation{monday}},
		{name: "finished challenge still holds its window", startedAt: toronto(t, 2026, 5, 1, 10), challenges: []Participation{finished}, want: []string{"finished"}},
		{name: "not active ignored", startedAt: toronto(t, 2026, 6, 2, 10), challenges: []Participation{notActive, monday}, want: []string{"monday"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := challengeIDs(Contributions(Activity{SportCode: "run", StartedAt: tt.startedAt, DurationSeconds: 60}, tt.challenges))
			if !slices.Equal(got, tt.want) {
				t.Fatalf("Contributions in %v, want %v", got, tt.want)
			}
		})
	}
}

// TestContributionsWeighDistance checks what an activity brings to each
// challenge: its duration, and its distance weighted by the factor of the
// challenge (scoring.Distance).
func TestContributionsWeighDistance(t *testing.T) {
	// Both from Monday 2026-06-01: an activity on Tuesday counts in both.
	run := participation(t, [3]int{2026, 6, 1}, 3)
	run.ChallengeID, run.Title = "run", "Défi 100 km"
	run.Factors = map[string]float64{"run": 1, "ride": 0.3}
	bike := participation(t, [3]int{2026, 6, 1}, 3)
	bike.ChallengeID, bike.Title = "bike", "Vélo d'été"
	bike.Factors = map[string]float64{"run": 1, "ride": 1}
	at := toronto(t, 2026, 6, 2, 10)
	m := func(v int) *int { return &v }

	tests := []struct {
		name     string
		sport    string
		distance *int
		want     []Contribution
	}{
		{name: "ride ×0.3 in one, ×1 in the other", sport: "ride", distance: m(20000), want: []Contribution{
			{ChallengeID: "run", ChallengeTitle: "Défi 100 km", DistanceMeters: m(6000), DurationSeconds: 3600},
			{ChallengeID: "bike", ChallengeTitle: "Vélo d'été", DistanceMeters: m(20000), DurationSeconds: 3600},
		}},
		{name: "rounded to the meter", sport: "ride", distance: m(1001), want: []Contribution{
			{ChallengeID: "run", ChallengeTitle: "Défi 100 km", DistanceMeters: m(300), DurationSeconds: 3600},
			{ChallengeID: "bike", ChallengeTitle: "Vélo d'été", DistanceMeters: m(1001), DurationSeconds: 3600},
		}},
		{name: "sport without distance: duration only", sport: "yoga", want: []Contribution{
			{ChallengeID: "run", ChallengeTitle: "Défi 100 km", DurationSeconds: 3600},
			{ChallengeID: "bike", ChallengeTitle: "Vélo d'été", DurationSeconds: 3600},
		}},
		{name: "sport without a factor in the challenge", sport: "rowing", distance: m(5000), want: []Contribution{
			{ChallengeID: "run", ChallengeTitle: "Défi 100 km", DurationSeconds: 3600},
			{ChallengeID: "bike", ChallengeTitle: "Vélo d'été", DurationSeconds: 3600},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := Activity{SportCode: tt.sport, StartedAt: at, DurationSeconds: 3600, DistanceMeters: tt.distance}
			got := Contributions(a, []Participation{run, bike})
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Contributions = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// challengeIDs are the challenges of contributions, nil for none.
func challengeIDs(contributions []Contribution) []string {
	var ids []string
	for _, c := range contributions {
		ids = append(ids, c.ChallengeID)
	}
	return ids
}
