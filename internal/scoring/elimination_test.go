package scoring

import (
	"reflect"
	"slices"
	"testing"
	"time"
)

func TestEliminationScore(t *testing.T) {
	// 4 weeks across the time change of March 8.
	w := window(t, "America/Toronto", [3]int{2026, 3, 2}, 4)
	const step = 2000
	weekStart := func(n int) time.Time { s, _ := w.WeekBounds(n); return s }
	weekEnd := func(n int) time.Time { _, e := w.WeekBounds(n); return e }
	locked := w.WeekLocksAt
	act := func(user, sport string, week, distance int) Activity {
		return Activity{
			UserID: user, SportCode: sport, StartedAt: weekStart(week).Add(time.Hour),
			DurationSeconds: 1800, DistanceMeters: meters(distance),
		}
	}
	// runs is one run per week, none for 0.
	runs := func(user string, distances ...int) []Activity {
		var as []Activity
		for i, d := range distances {
			if d > 0 {
				as = append(as, act(user, "run", i+1, d))
			}
		}
		return as
	}
	users := func(ids ...string) []Participant {
		ps := make([]Participant, len(ids))
		for i, id := range ids {
			ps[i] = Participant{UserID: id}
		}
		return ps
	}

	tests := []struct {
		name string
		// sport is the reference sport, run when empty.
		sport        string
		now          time.Time
		participants []Participant
		activities   []Activity
		want         []Entry
	}{
		{
			name:         "exactly the minimum is not eliminated",
			now:          locked(2),
			participants: users("a", "b"),
			activities:   slices.Concat(runs("a", 5000, 7000), runs("b", 1000, 3000)),
			want:         []Entry{{UserID: "a", Rank: 1, Value: 12000}, {UserID: "b", Rank: 2, Value: 4000}},
		},
		{
			name:         "one meter below the minimum is eliminated, behind the survivors",
			now:          locked(2),
			participants: users("a", "b"),
			activities:   slices.Concat(runs("a", 5000, 6999), runs("b", 1000, 3000)),
			want:         []Entry{{UserID: "b", Rank: 1, Value: 4000}, {UserID: "a", Rank: 2, Value: 11999}},
		},
		{
			name:         "covering more than the minimum raises the base",
			now:          locked(3),
			participants: users("a", "c"),
			// Week 3 needs 10000 + 2000, not 5000 + 4000.
			activities: slices.Concat(runs("a", 5000, 10000, 12000), runs("c", 5000, 10000, 11999)),
			want:       []Entry{{UserID: "a", Rank: 1, Value: 27000}, {UserID: "c", Rank: 2, Value: 26999}},
		},
		{
			name:         "no elimination in week 1",
			now:          locked(1),
			participants: users("a", "b"),
			activities:   runs("b", 3000),
			want:         []Entry{{UserID: "b", Rank: 1, Value: 3000}, {UserID: "a", Rank: 2}},
		},
		{
			name:         "a week without a run is eliminated",
			now:          locked(2),
			participants: users("a", "b"),
			activities:   slices.Concat(runs("a", 5000), runs("b", 1000, 3000)),
			want:         []Entry{{UserID: "b", Rank: 1, Value: 4000}, {UserID: "a", Rank: 2, Value: 5000}},
		},
		{
			name:         "the runs of a week add up",
			now:          locked(2),
			participants: users("a"),
			activities: []Activity{
				act("a", "run", 1, 2500), act("a", "run", 1, 2500), act("a", "run", 2, 3500), act("a", "run", 2, 3500),
			},
			want: []Entry{{UserID: "a", Rank: 1, Value: 12000}},
		},
		{
			name:         "only the reference sport counts",
			now:          locked(2),
			participants: users("a"),
			activities:   []Activity{act("a", "run", 1, 5000), act("a", "ride", 2, 9000)},
			want:         []Entry{{UserID: "a", Rank: 1, Value: 5000}},
		},
		{
			name:         "another reference sport: its distance counts, not the runs",
			sport:        "ride",
			now:          locked(2),
			participants: users("a", "b"),
			activities: []Activity{
				act("a", "ride", 1, 5000), act("a", "ride", 2, 7000), act("a", "run", 2, 9000),
				act("b", "run", 1, 5000), act("b", "run", 2, 9000),
			},
			want: []Entry{{UserID: "a", Rank: 1, Value: 12000}, {UserID: "b", Rank: 2}},
		},
		{
			name:         "survivors with different bases: the most km win",
			now:          locked(4),
			participants: users("a", "b"),
			activities:   slices.Concat(runs("a", 5000, 7000, 9000, 11000), runs("b", 10000, 12000, 14000, 16000)),
			want:         []Entry{{UserID: "b", Rank: 1, Value: 52000}, {UserID: "a", Rank: 2, Value: 32000}},
		},
		{
			name:         "survivors, then the last eliminated first, then km",
			now:          locked(3),
			participants: users("s", "c", "d", "f"),
			activities: slices.Concat(
				runs("s", 1000, 3000, 5000),
				runs("c", 20000, 1000),     // eliminated in week 2, 21000 m
				runs("d", 5000, 7000, 100), // eliminated in week 3, 12100 m
				runs("f", 5000),            // eliminated in week 2, 5000 m
			),
			want: []Entry{
				{UserID: "s", Rank: 1, Value: 9000},
				{UserID: "d", Rank: 2, Value: 12100},
				{UserID: "c", Rank: 3, Value: 21000},
				{UserID: "f", Rank: 4, Value: 5000},
			},
		},
		{
			name:         "tie on the week of elimination and the km",
			now:          locked(2),
			participants: users("g", "h", "j"),
			activities:   slices.Concat(runs("g", 5000, 1000), runs("h", 3000, 3000), runs("j", 5000)),
			want: []Entry{
				{UserID: "g", Rank: 1, Value: 6000}, {UserID: "h", Rank: 1, Value: 6000}, {UserID: "j", Rank: 3, Value: 5000},
			},
		},
		{
			name:         "week not judged at its exact end",
			now:          weekEnd(2),
			participants: users("a", "b"),
			activities:   slices.Concat(runs("a", 5000, 6999), runs("b", 1000, 3000)),
			want:         []Entry{{UserID: "a", Rank: 1, Value: 11999}, {UserID: "b", Rank: 2, Value: 4000}},
		},
		{
			name:         "week judged at its last second before 7 am: not yet",
			now:          locked(2).Add(-time.Second),
			participants: users("a", "b"),
			activities:   slices.Concat(runs("a", 5000, 6999), runs("b", 1000, 3000)),
			want:         []Entry{{UserID: "a", Rank: 1, Value: 11999}, {UserID: "b", Rank: 2, Value: 4000}},
		},
		{
			name:         "week judged when it locks, at 7 am the day after",
			now:          locked(2),
			participants: users("a", "b"),
			activities:   slices.Concat(runs("a", 5000, 6999), runs("b", 1000, 3000)),
			want:         []Entry{{UserID: "b", Rank: 1, Value: 4000}, {UserID: "a", Rank: 2, Value: 11999}},
		},
		{
			name:         "a week in progress is not judged",
			now:          weekStart(2).Add(time.Hour),
			participants: users("a"),
			activities:   runs("a", 5000),
			want:         []Entry{{UserID: "a", Rank: 1, Value: 5000}},
		},
		{
			name:         "elimination is final: later runs do not count",
			now:          locked(3),
			participants: users("a"),
			activities:   runs("a", 5000, 1000, 20000),
			want:         []Entry{{UserID: "a", Rank: 1, Value: 6000}},
		},
		{
			name:         "activity outside the window ignored",
			now:          locked(2),
			participants: users("a"),
			activities: slices.Concat(runs("a", 5000, 7000), []Activity{
				{UserID: "a", SportCode: "run", StartedAt: w.Start.Add(-time.Second), DurationSeconds: 60, DistanceMeters: meters(9000)},
			}),
			want: []Entry{{UserID: "a", Rank: 1, Value: 12000}},
		},
		{
			name:         "abandoned listed last, unranked",
			now:          locked(2),
			participants: []Participant{{UserID: "a", Abandoned: true}, {UserID: "b"}},
			activities:   slices.Concat(runs("a", 5000, 7000), runs("b", 1000, 3000)),
			want:         []Entry{{UserID: "b", Rank: 1, Value: 4000}, {UserID: "a", Abandoned: true}},
		},
		{
			name:         "last week not judged at the end of the challenge",
			now:          w.End,
			participants: users("a", "b"),
			activities:   slices.Concat(runs("a", 5000, 7000, 9000, 8999), runs("b", 1000, 3000, 5000, 7000)),
			want:         []Entry{{UserID: "a", Rank: 1, Value: 29999}, {UserID: "b", Rank: 2, Value: 16000}},
		},
		{
			name:         "last week judged when it locks, the day after the end",
			now:          locked(4),
			participants: users("a", "b"),
			activities:   slices.Concat(runs("a", 5000, 7000, 9000, 8999), runs("b", 1000, 3000, 5000, 7000)),
			want:         []Entry{{UserID: "b", Rank: 1, Value: 16000}, {UserID: "a", Rank: 2, Value: 29999}},
		},
		{
			name:         "last week still judged long after the end",
			now:          w.End.Add(30 * 24 * time.Hour),
			participants: users("a", "b"),
			activities:   slices.Concat(runs("a", 5000, 7000, 9000, 8999), runs("b", 1000, 3000, 5000, 7000)),
			want:         []Entry{{UserID: "b", Rank: 1, Value: 16000}, {UserID: "a", Rank: 2, Value: 29999}},
		},
		{
			name:         "the end of a week belongs to the next one",
			now:          locked(2),
			participants: users("a"),
			activities: []Activity{
				{UserID: "a", SportCode: "run", StartedAt: weekEnd(1).Add(-time.Nanosecond), DurationSeconds: 60, DistanceMeters: meters(5000)},
				{UserID: "a", SportCode: "run", StartedAt: weekEnd(1), DurationSeconds: 60, DistanceMeters: meters(7000)},
			},
			want: []Entry{{UserID: "a", Rank: 1, Value: 12000}},
		},
		{
			name:         "a run across the end of the week, entered before 7 am, counts for its start",
			now:          locked(2),
			participants: users("a"),
			activities: slices.Concat(runs("a", 5000, 4000), []Activity{
				{UserID: "a", SportCode: "run", StartedAt: weekEnd(2).Add(-30 * time.Minute), DurationSeconds: 3600, DistanceMeters: meters(3000)},
			}),
			want: []Entry{{UserID: "a", Rank: 1, Value: 12000}},
		},
		{
			name:         "no activity at all is eliminated once week 2 is judged",
			now:          locked(2),
			participants: users("a", "b"),
			activities:   runs("b", 1000, 3000),
			want:         []Entry{{UserID: "b", Rank: 1, Value: 4000}, {UserID: "a", Rank: 2}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := w
			w.Now = tt.now
			sport := tt.sport
			if sport == "" {
				sport = "run"
			}
			got := Elimination{Sport: sport, StepMeters: step}.Score(w, tt.participants, tt.activities)
			// The live progress has its own test.
			for i := range got {
				got[i].Progress = nil
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Score() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestIsEliminated(t *testing.T) {
	tests := []struct {
		name              string
		previous, current int
		want              bool
	}{
		{name: "exactly the minimum", previous: 5000, current: 7000},
		{name: "one meter below", previous: 5000, current: 6999, want: true},
		{name: "above the minimum", previous: 5000, current: 10000},
		{name: "no run after a run", previous: 5000, want: true},
		{name: "no run after no run", previous: 0, current: 0, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isEliminated(tt.previous, tt.current, 2000); got != tt.want {
				t.Fatalf("isEliminated(%d, %d) = %v, want %v", tt.previous, tt.current, got, tt.want)
			}
		})
	}
}

func TestEliminationProgress(t *testing.T) {
	w := window(t, "America/Toronto", [3]int{2026, 3, 2}, 4)
	const step = 2000
	atRisk := 24 * time.Hour
	weekStart := func(n int) time.Time { s, _ := w.WeekBounds(n); return s }
	weekEnd := func(n int) time.Time { _, e := w.WeekBounds(n); return e }
	run := func(user string, week, distance int) Activity {
		return Activity{
			UserID: user, SportCode: "run", StartedAt: weekStart(week).Add(time.Hour),
			DurationSeconds: 1800, DistanceMeters: meters(distance),
		}
	}
	progress := func(now time.Time, participants []Participant, activities ...Activity) map[string]*Progress {
		t.Helper()
		w := w
		w.Now = now
		out := map[string]*Progress{}
		for _, e := range (Elimination{Sport: "run", StepMeters: step, AtRisk: atRisk}).Score(w, participants, activities) {
			out[e.UserID] = e.Progress
		}
		return out
	}
	a := []Participant{{UserID: "a"}}

	tests := []struct {
		name string
		got  *Progress
		want *Progress
	}{
		{
			name: "week 1: no minimum, no status",
			got:  progress(weekEnd(1).Add(-time.Hour), a, run("a", 1, 3000))["a"],
			want: &Progress{DistanceMeters: 3000, WeekEndsAt: weekEnd(1)},
		},
		{
			name: "goal reached once the week reaches the minimum",
			got:  progress(weekEnd(2).Add(-48*time.Hour), a, run("a", 1, 5000), run("a", 2, 7000))["a"],
			want: &Progress{Status: StatusReached, MinimumMeters: meters(7000), DistanceMeters: 7000, WeekEndsAt: weekEnd(2)},
		},
		{
			name: "goal reached even in the last hours",
			got:  progress(weekEnd(2).Add(-time.Hour), a, run("a", 1, 5000), run("a", 2, 9000))["a"],
			want: &Progress{Status: StatusReached, MinimumMeters: meters(7000), DistanceMeters: 9000, WeekEndsAt: weekEnd(2)},
		},
		{
			name: "25 hours left: in progress",
			got:  progress(weekEnd(2).Add(-25*time.Hour), a, run("a", 1, 5000), run("a", 2, 6000))["a"],
			want: &Progress{Status: StatusInProgress, MinimumMeters: meters(7000), DistanceMeters: 6000, WeekEndsAt: weekEnd(2)},
		},
		{
			name: "24 hours left: at risk",
			got:  progress(weekEnd(2).Add(-24*time.Hour), a, run("a", 1, 5000), run("a", 2, 6000))["a"],
			want: &Progress{Status: StatusAtRisk, MinimumMeters: meters(7000), DistanceMeters: 6000, WeekEndsAt: weekEnd(2)},
		},
		{
			name: "23 hours left: at risk",
			got:  progress(weekEnd(2).Add(-23*time.Hour), a, run("a", 1, 5000), run("a", 2, 6000))["a"],
			want: &Progress{Status: StatusAtRisk, MinimumMeters: meters(7000), DistanceMeters: 6000, WeekEndsAt: weekEnd(2)},
		},
		{
			name: "the minimum follows the distance really covered the week before",
			got:  progress(weekEnd(3).Add(-48*time.Hour), a, run("a", 1, 5000), run("a", 2, 10000), run("a", 3, 11000))["a"],
			want: &Progress{Status: StatusInProgress, MinimumMeters: meters(12000), DistanceMeters: 11000, WeekEndsAt: weekEnd(3)},
		},
		{
			name: "the exact end of a week is already the next week",
			got:  progress(weekEnd(2), a, run("a", 1, 5000), run("a", 2, 7000))["a"],
			want: &Progress{Status: StatusInProgress, MinimumMeters: meters(9000), WeekEndsAt: weekEnd(3)},
		},
		{
			name: "week without a run yet",
			got:  progress(weekStart(3).Add(time.Hour), a, run("a", 1, 5000), run("a", 2, 7000))["a"],
			want: &Progress{Status: StatusInProgress, MinimumMeters: meters(9000), WeekEndsAt: weekEnd(3)},
		},
		{
			name: "before week 2 locks: not eliminated yet, a minimum that may still rise",
			got:  progress(w.WeekLocksAt(2).Add(-time.Nanosecond), a, run("a", 1, 5000), run("a", 2, 6999))["a"],
			want: &Progress{Status: StatusInProgress, MinimumMeters: meters(8999), WeekEndsAt: weekEnd(3)},
		},
		{
			name: "eliminated once the week locks: the week of the elimination only",
			got:  progress(w.WeekLocksAt(2), a, run("a", 1, 5000), run("a", 2, 6999))["a"],
			want: &Progress{Status: StatusEliminated, EliminatedWeek: 2},
		},
		{
			name: "the challenge is over: no live status",
			got:  progress(w.End, a, run("a", 1, 5000), run("a", 2, 7000), run("a", 3, 9000), run("a", 4, 11000))["a"],
			want: &Progress{},
		},
		{
			name: "the challenge has not started",
			got:  progress(w.Start.Add(-time.Hour), a)["a"],
			want: &Progress{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !reflect.DeepEqual(tt.got, tt.want) {
				t.Fatalf("Progress = %+v, want %+v", tt.got, tt.want)
			}
		})
	}

	t.Run("abandoned participant has none", func(t *testing.T) {
		got := progress(weekEnd(2).Add(-time.Hour), []Participant{{UserID: "a", Abandoned: true}}, run("a", 1, 5000))
		if got["a"] != nil {
			t.Fatalf("Progress = %+v, want none", got["a"])
		}
	})
}

func TestEliminations(t *testing.T) {
	// 4 weeks across the time change of March 8.
	w := window(t, "America/Toronto", [3]int{2026, 3, 2}, 4)
	const step = 2000
	weekStart := func(n int) time.Time { s, _ := w.WeekBounds(n); return s }
	locked := w.WeekLocksAt
	// sessions is one activity of sport per week, none for 0.
	sessions := func(sport, user string, distances ...int) []Activity {
		var as []Activity
		for i, d := range distances {
			if d > 0 {
				as = append(as, Activity{
					UserID: user, SportCode: sport, StartedAt: weekStart(i + 1).Add(time.Hour),
					DurationSeconds: 1800, DistanceMeters: meters(d),
				})
			}
		}
		return as
	}
	runs := func(user string, distances ...int) []Activity { return sessions("run", user, distances...) }
	rides := func(user string, distances ...int) []Activity { return sessions("ride", user, distances...) }
	users := func(ids ...string) []Participant {
		ps := make([]Participant, len(ids))
		for i, id := range ids {
			ps[i] = Participant{UserID: id}
		}
		return ps
	}

	tests := []struct {
		name string
		// sport is the reference sport, run when empty.
		sport        string
		now          time.Time
		participants []Participant
		activities   []Activity
		want         []Eliminated
	}{
		{
			name:         "the distance covered and the minimum it missed",
			now:          locked(2),
			participants: users("a", "b"),
			activities:   slices.Concat(runs("a", 5000, 6999), runs("b", 1000, 3000)),
			want:         []Eliminated{{UserID: "a", Week: 2, DistanceMeters: 6999, MinimumMeters: 7000}},
		},
		{
			name:         "exactly the minimum is not eliminated",
			now:          locked(2),
			participants: users("a"),
			activities:   runs("a", 5000, 7000),
		},
		{
			name:         "no run in the week is eliminated at zero",
			now:          locked(2),
			participants: users("a"),
			activities:   runs("a", 5000),
			want:         []Eliminated{{UserID: "a", Week: 2, DistanceMeters: 0, MinimumMeters: 7000}},
		},
		{
			name:         "week 2 is judged when it locks, not before",
			now:          locked(2).Add(-time.Nanosecond),
			participants: users("a"),
			activities:   runs("a", 5000),
		},
		{
			name:         "no elimination in week 1",
			now:          locked(1),
			participants: users("a"),
		},
		{
			name:         "the minimum follows the distance really covered",
			now:          locked(3),
			participants: users("a", "c"),
			// Week 3 needs 10000 + 2000, not 5000 + 4000.
			activities: slices.Concat(runs("a", 5000, 10000, 12000), runs("c", 5000, 10000, 11999)),
			want:       []Eliminated{{UserID: "c", Week: 3, DistanceMeters: 11999, MinimumMeters: 12000}},
		},
		{
			name:         "only the first elimination of a participant",
			now:          locked(4),
			participants: users("a"),
			activities:   runs("a", 5000, 1000, 9000, 20000),
			want:         []Eliminated{{UserID: "a", Week: 2, DistanceMeters: 1000, MinimumMeters: 7000}},
		},
		{
			name:         "ordered by week then participant",
			now:          locked(3),
			participants: users("d", "c", "b", "a"),
			activities:   slices.Concat(runs("a", 5000, 7000, 1), runs("b", 5000, 100, 100), runs("c", 5000, 7000, 1), runs("d", 5000, 7000, 9000)),
			want: []Eliminated{
				{UserID: "b", Week: 2, DistanceMeters: 100, MinimumMeters: 7000},
				{UserID: "a", Week: 3, DistanceMeters: 1, MinimumMeters: 9000},
				{UserID: "c", Week: 3, DistanceMeters: 1, MinimumMeters: 9000},
			},
		},
		{
			name:         "an abandoned participant is not eliminated",
			now:          locked(2),
			participants: []Participant{{UserID: "a", Abandoned: true}, {UserID: "b"}},
			activities:   runs("b", 5000, 7000),
		},
		{
			name:         "an activity of another sport does not count",
			now:          locked(2),
			participants: users("a"),
			activities: slices.Concat(runs("a", 5000), []Activity{{
				UserID: "a", SportCode: "ride", StartedAt: weekStart(2).Add(time.Hour),
				DurationSeconds: 1800, DistanceMeters: meters(20000),
			}}),
			want: []Eliminated{{UserID: "a", Week: 2, DistanceMeters: 0, MinimumMeters: 7000}},
		},
		{
			name:         "another reference sport: a longer run neither saves nor eliminates",
			sport:        "ride",
			now:          locked(2),
			participants: users("a", "b"),
			activities:   slices.Concat(rides("a", 5000, 6999), runs("a", 0, 20000), rides("b", 5000, 7000), runs("b", 9000)),
			want:         []Eliminated{{UserID: "a", Week: 2, DistanceMeters: 6999, MinimumMeters: 7000}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := w
			w.Now = tt.now
			sport := tt.sport
			if sport == "" {
				sport = "run"
			}
			got := Elimination{Sport: sport, StepMeters: step}.Eliminations(w, tt.participants, tt.activities)
			if !reflect.DeepEqual(got, tt.want) && (len(got) != 0 || len(tt.want) != 0) {
				t.Fatalf("Eliminations() = %+v, want %+v", got, tt.want)
			}
		})
	}

	t.Run("the result does not depend on when it is computed", func(t *testing.T) {
		participants := users("a", "b")
		activities := slices.Concat(runs("a", 5000, 6999, 4000), runs("b", 5000, 7000, 8000))
		scorer := Elimination{Sport: "run", StepMeters: step}
		atEnd, later := w, w
		atEnd.Now, later.Now = locked(2), locked(2).Add(30*24*time.Hour)
		onTime := scorer.Eliminations(atEnd, participants, activities)
		// Week 3 is judged later, but week 2 stays what it was.
		var week2 []Eliminated
		for _, e := range scorer.Eliminations(later, participants, activities) {
			if e.Week == 2 {
				week2 = append(week2, e)
			}
		}
		if !reflect.DeepEqual(onTime, week2) {
			t.Fatalf("week 2 judged on time = %+v, judged late = %+v", onTime, week2)
		}
	})

	t.Run("the same eliminations as the ranking", func(t *testing.T) {
		participants := users("a", "b", "c", "d")
		activities := slices.Concat(runs("a", 5000, 6999, 4000), runs("b", 5000, 7000, 8999), runs("c", 5000, 7000, 9000), runs("d", 1000))
		scorer := Elimination{Sport: "run", StepMeters: step}
		for n := 1; n <= 4; n++ {
			w := w
			w.Now = locked(n)
			recorded := map[string]int{}
			for _, e := range scorer.Eliminations(w, participants, activities) {
				recorded[e.UserID] = e.Week
			}
			for _, entry := range scorer.Score(w, participants, activities) {
				if got := recorded[entry.UserID]; got != entry.Progress.EliminatedWeek {
					t.Errorf("lock of week %d, %s: eliminated in week %d, ranking says %d", n, entry.UserID, got, entry.Progress.EliminatedWeek)
				}
			}
		}
	})
}
