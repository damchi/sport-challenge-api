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
	vancouverLock1 := time.Date(2026, 6, 8, 7, 0, 0, 0, pacific).UTC()

	noZone := monday
	noZone.Location = nil
	emptyWindow := monday
	emptyWindow.EndsAt = emptyWindow.StartsAt

	// Abandoned, excluded, or a creator who abandoned: no longer competes.
	notActive := monday
	notActive.Active = false

	// A week locks at 7 am, local time, on the day after it ends.
	tuesday := toronto(t, 2026, 6, 2, 10)       // week 1 of monday, before thursday starts
	friday := toronto(t, 2026, 6, 5, 10)        // week 1 of both
	endMonday1 := toronto(t, 2026, 6, 8, 0)     // end of week 1 of monday
	lockMonday1 := toronto(t, 2026, 6, 8, 7)    // week 1 of monday locks
	lockThursday1 := toronto(t, 2026, 6, 11, 7) // week 1 of thursday locks
	lockSpring1 := toronto(t, 2026, 3, 9, 7)    // in EDT, 11:00 UTC
	lockFall1 := toronto(t, 2026, 11, 2, 7)     // in EST, 12:00 UTC
	sec := time.Second

	tests := []struct {
		name       string
		now        time.Time
		startedAt  time.Time
		challenges []Participation
		want       bool
	}{
		{name: "no challenge", now: lockMonday1, startedAt: tuesday},
		{name: "at the end of the week: still open", now: endMonday1, startedAt: tuesday, challenges: []Participation{monday}},
		{name: "one second before 7 am", now: lockMonday1.Add(-sec), startedAt: tuesday, challenges: []Participation{monday}},
		{name: "at 7 am on the day after", now: lockMonday1, startedAt: tuesday, challenges: []Participation{monday}, want: true},
		{name: "across the end of the week: open until 7 am", now: lockMonday1.Add(-sec), startedAt: endMonday1.Add(-30 * time.Minute), challenges: []Participation{monday}},
		{name: "across the end of the week: locked with the week it starts in", now: lockMonday1, startedAt: endMonday1.Add(-sec), challenges: []Participation{monday}, want: true},
		{name: "first second of the next week stays open", now: lockMonday1, startedAt: endMonday1, challenges: []Participation{monday}},
		{name: "week 1 locked from the start of the window", now: lockMonday1, startedAt: monday.StartsAt, challenges: []Participation{monday}, want: true},
		{name: "week 2 open while week 1 is locked", now: lockMonday1.Add(time.Hour), startedAt: endMonday1.Add(time.Minute), challenges: []Participation{monday}},
		{name: "week 2 open at its end", now: toronto(t, 2026, 6, 15, 0), startedAt: endMonday1.Add(time.Minute), challenges: []Participation{monday}},
		{name: "week 2 locked at 7 am the day after", now: toronto(t, 2026, 6, 15, 7), startedAt: endMonday1.Add(time.Minute), challenges: []Participation{monday}, want: true},
		{name: "before the window: counts nowhere", now: lockMonday1, startedAt: monday.StartsAt.Add(-sec), challenges: []Participation{monday}},

		// Two challenges whose weeks are shifted: one is enough to lock.
		{name: "shifted weeks: open in both", now: lockMonday1.Add(-sec), startedAt: friday, challenges: []Participation{monday, thursday}},
		{name: "shifted weeks: locked by the first to lock", now: lockMonday1, startedAt: friday, challenges: []Participation{monday, thursday}, want: true},
		{name: "shifted weeks: order does not matter", now: lockMonday1, startedAt: friday, challenges: []Participation{thursday, monday}, want: true},
		{name: "shifted weeks: outside the second window", now: lockThursday1.Add(-sec), startedAt: tuesday, challenges: []Participation{thursday}},
		{name: "shifted weeks: second challenge only", now: lockThursday1, startedAt: friday, challenges: []Participation{thursday}, want: true},

		// Two time zones: each locks at 7 am in its own, 3 hours later in Vancouver.
		{name: "time zones: open in Vancouver after Toronto's 7 am", now: lockMonday1, startedAt: tuesday, challenges: []Participation{vancouver}},
		{name: "time zones: locked at Vancouver's 7 am", now: vancouverLock1, startedAt: tuesday, challenges: []Participation{vancouver}, want: true},
		{name: "time zones: Toronto locks first", now: lockMonday1, startedAt: tuesday, challenges: []Participation{vancouver, monday}, want: true},

		// Time changes: 7 am local, 7 local days after the start of the week.
		{name: "spring forward: one second before 7 am", now: lockSpring1.Add(-sec), startedAt: springForward.StartsAt, challenges: []Participation{springForward}},
		{name: "spring forward: at 7 am", now: lockSpring1, startedAt: springForward.StartsAt, challenges: []Participation{springForward}, want: true},
		{name: "spring forward: not 7 × 24 h + 7 h", now: springForward.StartsAt.Add(7*24*time.Hour + 7*time.Hour - sec), startedAt: springForward.StartsAt, challenges: []Participation{springForward}, want: true},
		{name: "fall back: one second before 7 am", now: lockFall1.Add(-sec), startedAt: fallBack.StartsAt, challenges: []Participation{fallBack}},
		{name: "fall back: at 7 am", now: lockFall1, startedAt: fallBack.StartsAt, challenges: []Participation{fallBack}, want: true},
		{name: "fall back: still open after 7 × 24 h + 7 h", now: fallBack.StartsAt.Add(7*24*time.Hour + 7*time.Hour), startedAt: fallBack.StartsAt, challenges: []Participation{fallBack}},

		// The last week of a finished challenge: open until 7 am the day after the end.
		{name: "finished challenge: last week open until 7 am", now: toronto(t, 2026, 5, 11, 6), startedAt: toronto(t, 2026, 5, 10, 8), challenges: []Participation{finished}},
		{name: "finished challenge: locked from 7 am the day after the end", now: toronto(t, 2026, 5, 11, 7), startedAt: toronto(t, 2026, 5, 10, 8), challenges: []Participation{finished}, want: true},
		{name: "finished challenge, long after", now: toronto(t, 2026, 11, 9, 12), startedAt: toronto(t, 2026, 5, 10, 8), challenges: []Participation{finished}, want: true},
		{name: "after a finished challenge: counts nowhere", now: toronto(t, 2026, 11, 9, 12), startedAt: finished.EndsAt, challenges: []Participation{finished}},

		// Broken data fails closed or counts nowhere, never panics.
		{name: "no time zone: locked", now: endMonday1.Add(-sec), startedAt: tuesday, challenges: []Participation{noZone}, want: true},
		{name: "no time zone: outside its window, counts nowhere", now: lockMonday1, startedAt: noZone.StartsAt.Add(-sec), challenges: []Participation{noZone}},
		{name: "empty window: counts nowhere", now: lockMonday1, startedAt: tuesday, challenges: []Participation{emptyWindow}},

		// Ignored: where the activity does not count.
		{name: "not an active participant: ignored", now: lockMonday1, startedAt: tuesday, challenges: []Participation{notActive}},
		{name: "ignored challenge does not hide an active one", now: lockMonday1, startedAt: tuesday, challenges: []Participation{notActive, monday}, want: true},

		// Left the challenge: the weeks locked by then stay locked (README §2.3).
		{name: "left before the week locked: open", now: lockMonday1.Add(24 * time.Hour), startedAt: tuesday, challenges: []Participation{leftAt(monday, lockMonday1.Add(-sec))}},
		{name: "left as the week locked: locked", now: lockMonday1.Add(24 * time.Hour), startedAt: tuesday, challenges: []Participation{leftAt(monday, lockMonday1)}, want: true},
		{name: "left after the week locked: locked", now: lockMonday1.Add(24 * time.Hour), startedAt: tuesday, challenges: []Participation{leftAt(monday, lockMonday1.Add(time.Hour))}, want: true},
		{name: "left after the week locked: a later week stays open", now: toronto(t, 2026, 6, 15, 7), startedAt: endMonday1.Add(time.Minute), challenges: []Participation{leftAt(monday, lockMonday1.Add(time.Hour))}},
		{name: "left after the week locked: outside the window, counts nowhere", now: lockMonday1.Add(time.Hour), startedAt: monday.StartsAt.Add(-sec), challenges: []Participation{leftAt(monday, lockMonday1)}},
		{name: "left, no time zone: locked", now: lockMonday1, startedAt: tuesday, challenges: []Participation{leftAt(noZone, lockMonday1)}, want: true},
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
// challenge (scoring.Distance); none in a challenge without the cumulative
// distance, which has no factors.
func TestContributionsWeighDistance(t *testing.T) {
	// Both from Monday 2026-06-01: an activity on Tuesday counts in both.
	run := participation(t, [3]int{2026, 6, 1}, 3)
	run.ChallengeID, run.Title = "run", "Défi 100 km"
	run.Factors = map[string]float64{"run": 1, "ride": 0.3}
	bike := participation(t, [3]int{2026, 6, 1}, 3)
	bike.ChallengeID, bike.Title = "bike", "Vélo d'été"
	bike.Factors = map[string]float64{"run": 1, "ride": 1}
	timeOnly := participation(t, [3]int{2026, 6, 1}, 3)
	timeOnly.ChallengeID, timeOnly.Title = "time", "Temps seul"
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
			{ChallengeID: "time", ChallengeTitle: "Temps seul", DurationSeconds: 3600},
		}},
		{name: "rounded to the meter", sport: "ride", distance: m(1001), want: []Contribution{
			{ChallengeID: "run", ChallengeTitle: "Défi 100 km", DistanceMeters: m(300), DurationSeconds: 3600},
			{ChallengeID: "bike", ChallengeTitle: "Vélo d'été", DistanceMeters: m(1001), DurationSeconds: 3600},
			{ChallengeID: "time", ChallengeTitle: "Temps seul", DurationSeconds: 3600},
		}},
		{name: "sport without distance: duration only", sport: "yoga", want: []Contribution{
			{ChallengeID: "run", ChallengeTitle: "Défi 100 km", DurationSeconds: 3600},
			{ChallengeID: "bike", ChallengeTitle: "Vélo d'été", DurationSeconds: 3600},
			{ChallengeID: "time", ChallengeTitle: "Temps seul", DurationSeconds: 3600},
		}},
		{name: "sport without a factor in the challenge", sport: "rowing", distance: m(5000), want: []Contribution{
			{ChallengeID: "run", ChallengeTitle: "Défi 100 km", DurationSeconds: 3600},
			{ChallengeID: "bike", ChallengeTitle: "Vélo d'été", DurationSeconds: 3600},
			{ChallengeID: "time", ChallengeTitle: "Temps seul", DurationSeconds: 3600},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := Activity{SportCode: tt.sport, StartedAt: at, DurationSeconds: 3600, DistanceMeters: tt.distance}
			got := Contributions(a, []Participation{run, bike, timeOnly})
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

// leftAt is c once its author left it (abandoned or excluded) at at.
func leftAt(c Participation, at time.Time) Participation {
	c.Active, c.AbandonedAt = false, &at
	return c
}
