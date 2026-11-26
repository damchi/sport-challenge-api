package scoring

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"
)

// minSession is the minimum duration of a session in the tests, the
// default of the points category.
const minSession = 20 * 60

// session is the group of a single session, as the repository returns it:
// its longest session is itself, and it counts once if it lasts at least
// minSession.
func session(user, sport string, start time.Time, seconds int, distance *int) Activity {
	a := Activity{
		UserID: user, SportCode: sport, StartedAt: start,
		DurationSeconds: seconds, DistanceMeters: distance,
		LongestSeconds: seconds, LongestMeters: distance,
		FastestSeconds: seconds, FastestMeters: distance,
	}
	if seconds >= minSession {
		a.Sessions = 1
	}
	return a
}

func points(v int) *int { return &v }

func at(t time.Time) *time.Time { return &t }

func users(ids ...string) []Participant {
	ps := make([]Participant, len(ids))
	for i, id := range ids {
		ps[i] = Participant{UserID: id}
	}
	return ps
}

// formatEntries shows entries one per line, the provisional points
// dereferenced.
func formatEntries(entries []Entry) string {
	var s string
	for _, e := range entries {
		s += fmt.Sprintf("  %s rank %d value %d abandoned %t provisional %v\n", e.UserID, e.Rank, e.Value, e.Abandoned, deref(e.Provisional))
	}
	return s
}

func TestPointsScore(t *testing.T) {
	// 3 weeks in Montreal, from Monday 2026-01-05.
	w := window(t, "America/Toronto", [3]int{2026, 1, 5}, 3)
	weekStart := func(n int) time.Time { s, _ := w.WeekBounds(n); return s }
	lock := w.WeekLocksAt
	final := lock(3)
	if got := lock(1).In(w.Location); got.Hour() != 7 || got.Minute() != 0 || got.Weekday() != time.Monday {
		t.Fatalf("week 1 locks at %s, want Monday 7:00", got)
	}
	// run is a run of minutes minutes in week n, without distance.
	run := func(user string, n, minutes int) Activity {
		return session(user, "run", weekStart(n).Add(time.Hour), minutes*60, nil)
	}
	factors := map[string]float64{"run": 1, "ride": 0.3, "other": 1}
	only := func(c string) Points { return Points{Criteria: []string{c}, Podium: 5, Factors: factors} }
	longest := only(CriterionLongestDuration)
	abandoned := func(id string, left time.Time) Participant {
		return Participant{UserID: id, Abandoned: true, AbandonedAt: at(left)}
	}

	tests := []struct {
		name         string
		points       Points
		now          time.Time
		participants []Participant
		activities   []Activity
		want         []Entry
	}{
		{
			name:         "longest session in time, not the sum",
			points:       longest,
			now:          final,
			participants: users("a", "b", "c"),
			activities:   []Activity{run("a", 1, 60), run("b", 1, 45), run("b", 1, 30), run("c", 1, 45)},
			want: []Entry{
				{UserID: "a", Rank: 1, Value: 10}, {UserID: "b", Rank: 2, Value: 8}, {UserID: "c", Rank: 2, Value: 8},
			},
		},
		{
			name:         "longest session in equivalent distance",
			points:       only(CriterionLongestDistance),
			now:          final,
			participants: users("a", "b", "c", "d"),
			activities: []Activity{
				session("a", "run", weekStart(1), 3600, meters(10000)),
				// 40 km at 0.3 is 12 km; the shorter ride does not add up.
				session("b", "ride", weekStart(1), 7200, meters(40000)),
				session("b", "ride", weekStart(1), 3600, meters(20000)),
				// Neither a sport without a factor nor a session without distance.
				session("c", "yoga", weekStart(1), 7200, meters(50000)),
				session("d", "other", weekStart(1), 7200, nil),
			},
			want: []Entry{
				{UserID: "b", Rank: 1, Value: 10},
				{UserID: "a", Rank: 2, Value: 8},
				{UserID: "c", Rank: 3},
				{UserID: "d", Rank: 3},
			},
		},
		{
			name:         "session count: exactly the minimum counts, a second less does not",
			points:       only(CriterionSessionCount),
			now:          final,
			participants: users("a", "b"),
			activities: slices.Concat(
				slices.Repeat([]Activity{session("a", "run", weekStart(1), minSession, nil)}, 3),
				slices.Repeat([]Activity{session("b", "run", weekStart(1), minSession-1, nil)}, 5),
			),
			want: []Entry{{UserID: "a", Rank: 1, Value: 10}, {UserID: "b", Rank: 2}},
		},
		{
			name:         "a session under the minimum still counts for the longest session",
			points:       longest,
			now:          final,
			participants: users("a", "b"),
			activities: []Activity{
				session("a", "run", weekStart(1), minSession-1, nil), session("b", "run", weekStart(1), 60, nil),
			},
			want: []Entry{{UserID: "a", Rank: 1, Value: 10}, {UserID: "b", Rank: 2, Value: 8}},
		},
		{
			name:         "the criteria and the weeks add up",
			points:       Points{Criteria: []string{CriterionLongestDuration, CriterionLongestDistance, CriterionSessionCount}, Podium: 5, Factors: factors},
			now:          final,
			participants: users("a", "b"),
			activities: []Activity{
				// Week 1: a longest (10), b farthest (10), a 2 sessions to 1 (10, 8).
				session("a", "run", weekStart(1), 3600, meters(5000)),
				session("a", "run", weekStart(1), 1800, meters(4000)),
				session("b", "run", weekStart(1), 3000, meters(9000)),
				// Week 3: b alone (10 × 3).
				session("b", "run", weekStart(3), 3000, meters(9000)),
			},
			want: []Entry{{UserID: "b", Rank: 1, Value: 8 + 10 + 8 + 30}, {UserID: "a", Rank: 2, Value: 10 + 8 + 10}},
		},
		{
			name:         "tie at the top: same rank, same points (1, 1, 3)",
			points:       longest,
			now:          final,
			participants: users("a", "b", "c"),
			activities:   []Activity{run("a", 1, 60), run("b", 1, 60), run("c", 1, 50)},
			want: []Entry{
				{UserID: "a", Rank: 1, Value: 10}, {UserID: "b", Rank: 1, Value: 10}, {UserID: "c", Rank: 3, Value: 6},
			},
		},
		{
			name:         "three tied at the edge of the top 5 each get the points of 5th",
			points:       longest,
			now:          final,
			participants: users("a", "b", "c", "d", "e", "f", "g", "h"),
			activities: []Activity{
				run("a", 1, 100), run("b", 1, 90), run("c", 1, 80), run("d", 1, 70),
				run("e", 1, 60), run("f", 1, 60), run("g", 1, 60), run("h", 1, 50),
			},
			want: []Entry{
				{UserID: "a", Rank: 1, Value: 10},
				{UserID: "b", Rank: 2, Value: 8},
				{UserID: "c", Rank: 3, Value: 6},
				{UserID: "d", Rank: 4, Value: 4},
				{UserID: "e", Rank: 5, Value: 2},
				{UserID: "f", Rank: 5, Value: 2},
				{UserID: "g", Rank: 5, Value: 2},
				{UserID: "h", Rank: 8},
			},
		},
		{
			name:         "top 10 scale",
			points:       Points{Criteria: []string{CriterionLongestDuration}, Podium: 10},
			now:          final,
			participants: users("a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"),
			activities: []Activity{
				run("a", 1, 110), run("b", 1, 100), run("c", 1, 90), run("d", 1, 80), run("e", 1, 70), run("f", 1, 60),
				run("g", 1, 50), run("h", 1, 40), run("i", 1, 30), run("j", 1, 20), run("k", 1, 10),
			},
			want: []Entry{
				{UserID: "a", Rank: 1, Value: 25},
				{UserID: "b", Rank: 2, Value: 18},
				{UserID: "c", Rank: 3, Value: 15},
				{UserID: "d", Rank: 4, Value: 12},
				{UserID: "e", Rank: 5, Value: 10},
				{UserID: "f", Rank: 6, Value: 8},
				{UserID: "g", Rank: 7, Value: 6},
				{UserID: "h", Rank: 8, Value: 4},
				{UserID: "i", Rank: 9, Value: 2},
				{UserID: "j", Rank: 10, Value: 1},
				{UserID: "k", Rank: 11},
			},
		},
		{
			name:         "no activity during a week: nobody scores, provisional 0",
			points:       longest,
			now:          weekStart(2).Add(time.Hour),
			participants: users("a", "b"),
			want: []Entry{
				{UserID: "a", Rank: 1, Provisional: points(0)}, {UserID: "b", Rank: 1, Provisional: points(0)},
			},
		},
		{
			name:         "no activity at the end: nobody scores",
			points:       longest,
			now:          final,
			participants: users("a", "b"),
			want:         []Entry{{UserID: "a", Rank: 1}, {UserID: "b", Rank: 1}},
		},
		{
			name:         "before the start: nothing provisional",
			points:       longest,
			now:          w.Start.Add(-time.Hour),
			participants: users("a"),
			want:         []Entry{{UserID: "a", Rank: 1}},
		},
		{
			name:         "6:59 on the first day of week 2: week 1 still provisional, the rank ignores it",
			points:       longest,
			now:          lock(1).Add(-time.Minute),
			participants: users("a", "b"),
			activities:   []Activity{run("a", 1, 60), run("b", 1, 45), run("b", 2, 50)},
			want: []Entry{
				{UserID: "a", Rank: 1, Provisional: points(10)}, {UserID: "b", Rank: 1, Provisional: points(8 + 10)},
			},
		},
		{
			name:         "7:00 on the first day of week 2: week 1 acquired",
			points:       longest,
			now:          lock(1),
			participants: users("a", "b"),
			activities:   []Activity{run("a", 1, 60), run("b", 1, 45), run("b", 2, 50)},
			want: []Entry{
				{UserID: "a", Rank: 1, Value: 10, Provisional: points(0)}, {UserID: "b", Rank: 2, Value: 8, Provisional: points(10)},
			},
		},
		{
			name:         "after the end, before the final lock: the last week still provisional",
			points:       longest,
			now:          lock(3).Add(-time.Second),
			participants: users("a", "b"),
			activities:   []Activity{run("a", 1, 60), run("b", 1, 45), run("b", 2, 50), run("a", 3, 40)},
			want: []Entry{
				{UserID: "b", Rank: 1, Value: 8 + 10, Provisional: points(0)}, {UserID: "a", Rank: 2, Value: 10, Provisional: points(10)},
			},
		},
		{
			name:         "at the final lock: everything acquired, nothing provisional",
			points:       longest,
			now:          lock(3),
			participants: users("a", "b"),
			activities:   []Activity{run("a", 1, 60), run("b", 1, 45), run("b", 2, 50), run("a", 3, 40)},
			want:         []Entry{{UserID: "a", Rank: 1, Value: 10 + 10}, {UserID: "b", Rank: 2, Value: 8 + 10}},
		},
		{
			name:   "abandoned after the lock of week 1: kept in week 1, out of week 2",
			points: longest,
			now:    final,
			participants: []Participant{
				abandoned("a", lock(1).Add(time.Hour)), {UserID: "b"}, {UserID: "c"},
			},
			activities: []Activity{
				run("a", 1, 60), run("b", 1, 45), run("c", 1, 30),
				run("a", 2, 60), run("b", 2, 45), run("c", 2, 30),
			},
			want: []Entry{
				{UserID: "b", Rank: 1, Value: 8 + 10},
				{UserID: "c", Rank: 2, Value: 6 + 8},
				{UserID: "a", Abandoned: true},
			},
		},
		{
			name:         "abandoned at the very instant of the lock: kept in that week",
			points:       longest,
			now:          final,
			participants: []Participant{abandoned("a", lock(1)), {UserID: "b"}},
			activities:   []Activity{run("a", 1, 60), run("b", 1, 45)},
			want:         []Entry{{UserID: "b", Rank: 1, Value: 8}, {UserID: "a", Abandoned: true}},
		},
		{
			name:         "abandoned before the lock of week 1: out of week 1 too",
			points:       longest,
			now:          final,
			participants: []Participant{abandoned("a", lock(1).Add(-time.Second)), {UserID: "b"}, {UserID: "c"}},
			activities: []Activity{
				run("a", 1, 60), run("b", 1, 45), run("c", 1, 30),
				run("a", 2, 60), run("b", 2, 45), run("c", 2, 30),
			},
			want: []Entry{
				{UserID: "b", Rank: 1, Value: 10 + 10},
				{UserID: "c", Rank: 2, Value: 8 + 8},
				{UserID: "a", Abandoned: true},
			},
		},
		{
			name:         "abandoned during a week: out of its provisional points, nil provisional",
			points:       longest,
			now:          weekStart(2).Add(2 * time.Hour),
			participants: []Participant{abandoned("a", weekStart(2).Add(time.Hour)), {UserID: "b"}, {UserID: "c"}},
			activities:   []Activity{run("a", 1, 60), run("b", 1, 45), run("c", 1, 30)},
			want: []Entry{
				{UserID: "b", Rank: 1, Provisional: points(10)},
				{UserID: "c", Rank: 1, Provisional: points(8)},
				{UserID: "a", Abandoned: true},
			},
		},
		{
			name:         "abandoned with no instant: out of every week",
			points:       longest,
			now:          final,
			participants: []Participant{{UserID: "a", Abandoned: true}, {UserID: "b"}},
			activities:   []Activity{run("a", 1, 60), run("b", 1, 45)},
			want:         []Entry{{UserID: "b", Rank: 1, Value: 10}, {UserID: "a", Abandoned: true}},
		},
		{
			name:         "the window: ends_at - 1s counts, ends_at and before the start do not",
			points:       longest,
			now:          final,
			participants: users("a", "b", "c"),
			activities: []Activity{
				session("a", "run", w.End.Add(-time.Second), 1800, nil),
				session("b", "run", w.End, 3600, nil),
				session("c", "run", w.Start.Add(-time.Second), 3600, nil),
			},
			want: []Entry{{UserID: "a", Rank: 1, Value: 10}, {UserID: "b", Rank: 2}, {UserID: "c", Rank: 2}},
		},
		{
			name:         "last second of a week and the next one; a session running into week 2 counts for week 1",
			points:       longest,
			now:          lock(1),
			participants: users("a", "b"),
			activities: []Activity{
				session("a", "run", weekStart(2).Add(-time.Second), 3600, nil),
				session("b", "run", weekStart(2), 1800, nil),
			},
			want: []Entry{
				{UserID: "a", Rank: 1, Value: 10, Provisional: points(0)}, {UserID: "b", Rank: 2, Provisional: points(10)},
			},
		},
		{
			name:         "a group is read by its longest session and its session count, not its totals",
			points:       Points{Criteria: []string{CriterionLongestDuration, CriterionLongestDistance, CriterionSessionCount}, Podium: 5, Factors: factors},
			now:          final,
			participants: users("a", "b"),
			activities: []Activity{
				// Three sessions summed: 2 h and 20 km, the longest 1 h and 8 km.
				{
					UserID: "a", SportCode: "run", StartedAt: weekStart(1),
					DurationSeconds: 7200, DistanceMeters: meters(20000),
					LongestSeconds: 3600, LongestMeters: meters(8000), Sessions: 3,
				},
				// b beats a's longest session, not a's totals; and has 2 sessions.
				{
					UserID: "b", SportCode: "run", StartedAt: weekStart(1),
					DurationSeconds: 5400, DistanceMeters: meters(10000),
					LongestSeconds: 5400, LongestMeters: meters(10000), Sessions: 2,
				},
			},
			want: []Entry{{UserID: "b", Rank: 1, Value: 10 + 10 + 8}, {UserID: "a", Rank: 2, Value: 8 + 8 + 10}},
		},
		{
			name:         "several sports in a week: the longest across them, the sessions summed",
			points:       Points{Criteria: []string{CriterionLongestDuration, CriterionLongestDistance, CriterionSessionCount}, Podium: 5, Factors: factors},
			now:          final,
			participants: users("a", "b"),
			activities: []Activity{
				// a: longest 90 min (ride), farthest 9 km eq. (run, ride 20 km is 6), 3 sessions.
				{UserID: "a", SportCode: "run", StartedAt: weekStart(1), LongestSeconds: 3000, LongestMeters: meters(9000), Sessions: 1},
				{UserID: "a", SportCode: "ride", StartedAt: weekStart(1), LongestSeconds: 5400, LongestMeters: meters(20000), Sessions: 2},
				// b: 80 min, 8 km, 2 sessions in a single sport.
				{UserID: "b", SportCode: "run", StartedAt: weekStart(1), LongestSeconds: 4800, LongestMeters: meters(8000), Sessions: 2},
			},
			want: []Entry{{UserID: "a", Rank: 1, Value: 30}, {UserID: "b", Rank: 2, Value: 24}},
		},
		{
			name:         "two tied at the edge of the top 10 each get the points of 10th",
			points:       Points{Criteria: []string{CriterionLongestDuration}, Podium: 10},
			now:          final,
			participants: users("a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"),
			activities: []Activity{
				run("a", 1, 110), run("b", 1, 100), run("c", 1, 90), run("d", 1, 80), run("e", 1, 70), run("f", 1, 60),
				run("g", 1, 50), run("h", 1, 40), run("i", 1, 30), run("j", 1, 20), run("k", 1, 20),
			},
			want: []Entry{
				{UserID: "a", Rank: 1, Value: 25},
				{UserID: "b", Rank: 2, Value: 18},
				{UserID: "c", Rank: 3, Value: 15},
				{UserID: "d", Rank: 4, Value: 12},
				{UserID: "e", Rank: 5, Value: 10},
				{UserID: "f", Rank: 6, Value: 8},
				{UserID: "g", Rank: 7, Value: 6},
				{UserID: "h", Rank: 8, Value: 4},
				{UserID: "i", Rank: 9, Value: 2},
				{UserID: "j", Rank: 10, Value: 1},
				{UserID: "k", Rank: 10, Value: 1},
			},
		},
		{
			name:         "a non-participant takes no place",
			points:       longest,
			now:          final,
			participants: users("a"),
			activities:   []Activity{run("x", 1, 90), run("a", 1, 30)},
			want:         []Entry{{UserID: "a", Rank: 1, Value: 10}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := w
			w.Now = tt.now
			got := tt.points.Score(w, tt.participants, tt.activities)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Score() =\n%s\nwant\n%s", formatEntries(got), formatEntries(tt.want))
			}
		})
	}
}

func TestPointsDaylightSaving(t *testing.T) {
	longest := Points{Criteria: []string{CriterionLongestDuration}, Podium: 5}
	tests := []struct {
		name  string
		start [3]int
		hours float64
		// locks is when week 1 locks: 7:00 local on Monday, in UTC.
		locks time.Time
	}{
		{"march, spring forward", [3]int{2026, 3, 2}, 167, time.Date(2026, 3, 9, 11, 0, 0, 0, time.UTC)},
		{"november, fall back", [3]int{2026, 10, 26}, 169, time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := window(t, "America/Toronto", tt.start, 2)
			start, end := w.WeekBounds(1)
			if got := end.Sub(start).Hours(); got != tt.hours {
				t.Fatalf("week 1 lasts %v h, want %v", got, tt.hours)
			}
			if got := w.WeekLocksAt(1); !got.Equal(tt.locks) {
				t.Fatalf("week 1 locks at %s, want %s", got, tt.locks)
			}
			activities := []Activity{
				session("a", "run", end.Add(-time.Second), 3600, nil),
				session("b", "run", end, 3600, nil),
			}
			for _, c := range []struct {
				now  time.Time
				want []Entry
			}{
				{tt.locks.Add(-time.Second), []Entry{
					{UserID: "a", Rank: 1, Provisional: points(10)}, {UserID: "b", Rank: 1, Provisional: points(10)},
				}},
				{tt.locks, []Entry{
					{UserID: "a", Rank: 1, Value: 10, Provisional: points(0)}, {UserID: "b", Rank: 2, Provisional: points(10)},
				}},
			} {
				w.Now = c.now
				if got := longest.Score(w, users("a", "b"), activities); !reflect.DeepEqual(got, c.want) {
					t.Fatalf("at %s, Score() =\n%s\nwant\n%s", c.now, formatEntries(got), formatEntries(c.want))
				}
			}
		})
	}
}

func TestPointsWeeklyElevation(t *testing.T) {
	// 3 weeks in Toronto from Monday 2026-03-02: the clocks go forward on
	// Sunday 2026-03-08, in week 1 (167 h).
	w := window(t, "America/Toronto", [3]int{2026, 3, 2}, 3)
	weekStart := func(n int) time.Time { s, _ := w.WeekBounds(n); return s }
	lock := w.WeekLocksAt
	final := lock(3)
	factors := map[string]float64{"run": 1, "ride": 0.3, "hike": 1}
	elevation := Points{Criteria: []string{CriterionWeeklyElevation}, Podium: 5, Factors: factors}
	// climb is a session of sport in week n with an elevation gain.
	climb := func(user, sport string, start time.Time, gain *int) Activity {
		a := session(user, sport, start, 3600, meters(10000))
		a.ElevationMeters = gain
		return a
	}
	in := func(n int) time.Time { return weekStart(n).Add(time.Hour) }
	abandoned := func(id string, left time.Time) Participant {
		return Participant{UserID: id, Abandoned: true, AbandonedAt: at(left)}
	}

	tests := []struct {
		name         string
		points       Points
		now          time.Time
		participants []Participant
		activities   []Activity
		want         []Entry
	}{
		{
			name:         "the elevation gain of the week, summed over the sports, real meters without factor",
			points:       elevation,
			now:          final,
			participants: users("a", "b"),
			activities: []Activity{
				// a: 300 + 400 = 700 m; b: a single ride of 600 m, no factor.
				climb("a", "run", in(1), meters(300)), climb("a", "hike", in(1).Add(2*time.Hour), meters(400)),
				climb("b", "ride", in(1), meters(600)),
			},
			want: []Entry{{UserID: "a", Rank: 1, Value: 10}, {UserID: "b", Rank: 2, Value: 8}},
		},
		{
			name:         "no elevation gain, or 0 m, does not score",
			points:       elevation,
			now:          final,
			participants: users("a", "b", "c"),
			activities: []Activity{
				climb("a", "run", in(1), meters(50)), climb("b", "run", in(1), meters(0)), climb("c", "run", in(1), nil),
			},
			want: []Entry{{UserID: "a", Rank: 1, Value: 10}, {UserID: "b", Rank: 2}, {UserID: "c", Rank: 2}},
		},
		{
			name:         "a session under the minimum still counts for the elevation gain",
			points:       elevation,
			now:          final,
			participants: users("a", "b"),
			activities: func() []Activity {
				short := session("a", "run", in(1), minSession-1, meters(3000))
				short.ElevationMeters = meters(300)
				return []Activity{short, climb("b", "run", in(1), meters(100))}
			}(),
			want: []Entry{{UserID: "a", Rank: 1, Value: 10}, {UserID: "b", Rank: 2, Value: 8}},
		},
		{
			name:         "three tied at the edge of the top 5 each get the points of 5th",
			points:       elevation,
			now:          final,
			participants: users("a", "b", "c", "d", "e", "f", "g", "h"),
			activities: []Activity{
				climb("a", "run", in(1), meters(900)), climb("b", "run", in(1), meters(800)),
				climb("c", "run", in(1), meters(700)), climb("d", "run", in(1), meters(600)),
				climb("e", "run", in(1), meters(500)), climb("f", "ride", in(1), meters(500)),
				climb("g", "hike", in(1), meters(500)), climb("h", "run", in(1), meters(400)),
			},
			want: []Entry{
				{UserID: "a", Rank: 1, Value: 10},
				{UserID: "b", Rank: 2, Value: 8},
				{UserID: "c", Rank: 3, Value: 6},
				{UserID: "d", Rank: 4, Value: 4},
				{UserID: "e", Rank: 5, Value: 2},
				{UserID: "f", Rank: 5, Value: 2},
				{UserID: "g", Rank: 5, Value: 2},
				{UserID: "h", Rank: 8},
			},
		},
		{
			name:         "6:59 on the first day of week 2: week 1 still provisional",
			points:       elevation,
			now:          lock(1).Add(-time.Minute),
			participants: users("a", "b"),
			activities:   []Activity{climb("a", "run", in(1), meters(300)), climb("b", "run", in(1), meters(200))},
			want:         []Entry{{UserID: "a", Rank: 1, Provisional: points(10)}, {UserID: "b", Rank: 1, Provisional: points(8)}},
		},
		{
			name:         "7:00 on the first day of week 2: week 1 acquired",
			points:       elevation,
			now:          lock(1),
			participants: users("a", "b"),
			activities:   []Activity{climb("a", "run", in(1), meters(300)), climb("b", "run", in(1), meters(200))},
			want: []Entry{
				{UserID: "a", Rank: 1, Value: 10, Provisional: points(0)}, {UserID: "b", Rank: 2, Value: 8, Provisional: points(0)},
			},
		},
		{
			name:         "across the change of time, a session running into week 2 counts for week 1",
			points:       elevation,
			now:          final,
			participants: users("a", "b"),
			activities: []Activity{
				// a: Sunday 23:30 EDT, the last day of week 1 (167 h), 600 m.
				climb("a", "run", weekStart(2).Add(-30*time.Minute), meters(600)),
				// b: 300 m in week 1, 900 m at the first instant of week 2.
				climb("b", "run", in(1), meters(300)), climb("b", "run", weekStart(2), meters(900)),
			},
			// Week 1: a 10, b 8; week 2: b alone 10.
			want: []Entry{{UserID: "b", Rank: 1, Value: 18}, {UserID: "a", Rank: 2, Value: 10}},
		},
		{
			name:         "abandoned after the lock of week 1: kept in week 1, out of week 2",
			points:       elevation,
			now:          final,
			participants: []Participant{abandoned("q", lock(1).Add(time.Hour)), {UserID: "a"}},
			activities: []Activity{
				climb("q", "run", in(1), meters(900)), climb("a", "run", in(1), meters(100)),
				climb("q", "run", in(2), meters(900)), climb("a", "run", in(2), meters(100)),
			},
			// a: 8 in week 1 behind q, 10 in week 2 alone.
			want: []Entry{{UserID: "a", Rank: 1, Value: 18}, {UserID: "q", Abandoned: true}},
		},
		{
			name:         "abandoned before the lock of week 1: out of week 1 too",
			points:       elevation,
			now:          final,
			participants: []Participant{abandoned("q", lock(1).Add(-time.Second)), {UserID: "a"}},
			activities:   []Activity{climb("q", "run", in(1), meters(900)), climb("a", "run", in(1), meters(100))},
			want:         []Entry{{UserID: "a", Rank: 1, Value: 10}, {UserID: "q", Abandoned: true}},
		},
		{
			name:         "with the other criteria, each its own weekly ranking",
			points:       Points{Criteria: []string{CriterionLongestDuration, CriterionWeeklyElevation}, Podium: 5, Factors: factors},
			now:          final,
			participants: users("a", "b"),
			activities: []Activity{
				// a has the longest session, b the most elevation gain.
				session("a", "run", in(1), 7200, nil), climb("b", "hike", in(1).Add(3*time.Hour), meters(500)),
			},
			// Longest: a 10, b 8; elevation: b 10, a nothing.
			want: []Entry{{UserID: "b", Rank: 1, Value: 18}, {UserID: "a", Rank: 2, Value: 10}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := w
			w.Now = tt.now
			got := tt.points.Score(w, tt.participants, tt.activities)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Score() =\n%s\nwant\n%s", formatEntries(got), formatEntries(tt.want))
			}
		})
	}
}

func TestPointsFastestRun(t *testing.T) {
	w := window(t, "America/Toronto", [3]int{2026, 3, 2}, 3)
	lock := w.WeekLocksAt
	final := lock(3)
	in := func(n int) time.Time { s, _ := w.WeekBounds(n); return s.Add(time.Hour) }
	// The reference sport is ride and run has no frozen factor: the
	// criterion uses the real distance of runs all the same.
	fastest := Points{Criteria: []string{CriterionFastestRun}, Podium: 5, Factors: map[string]float64{"ride": 1}}
	// run is a run of km kilometers at pace seconds per km.
	run := func(user string, start time.Time, km, pace int) Activity {
		return session(user, "run", start, km*pace, meters(km*1000))
	}
	abandoned := func(id string, left time.Time) Participant {
		return Participant{UserID: id, Abandoned: true, AbandonedAt: at(left)}
	}

	tests := []struct {
		name         string
		now          time.Time
		participants []Participant
		activities   []Activity
		want         []Entry
	}{
		{
			name:         "the fastest run of the week counts, not the longest, and the lowest pace wins",
			now:          final,
			participants: users("a", "b"),
			// a: 10 km at 5:00, 5 km at 4:30; b: 5 km at 4:40.
			activities: []Activity{run("a", in(1), 10, 300), run("a", in(1).Add(time.Hour), 5, 270), run("b", in(1), 5, 280)},
			want:       []Entry{{UserID: "a", Rank: 1, Value: 10}, {UserID: "b", Rank: 2, Value: 8}},
		},
		{
			name:         "other sports and runs without distance do not count; no run, no rank",
			now:          final,
			participants: users("a", "b", "c"),
			activities: []Activity{
				session("a", "ride", in(1), 1800, meters(20000)), session("a", "run", in(1), 600, nil),
				session("a", "run", in(1), 600, meters(0)),
				session("b", "hike", in(1), 600, meters(5000)), run("c", in(1), 5, 400),
			},
			want: []Entry{{UserID: "c", Rank: 1, Value: 10}, {UserID: "a", Rank: 2}, {UserID: "b", Rank: 2}},
		},
		{
			name:         "paces rounded to 0 s/km score, and tie",
			now:          final,
			participants: users("a", "b", "c"),
			activities: []Activity{
				session("a", "run", in(1), 1, meters(3000)), session("b", "run", in(1), 1, meters(4000)), run("c", in(1), 5, 300),
			},
			want: []Entry{{UserID: "a", Rank: 1, Value: 10}, {UserID: "b", Rank: 1, Value: 10}, {UserID: "c", Rank: 3, Value: 6}},
		},
		{
			name:         "ties to the second per km, three of them at the edge of the top 5",
			now:          final,
			participants: users("a", "b", "c", "d", "e", "f", "g", "h"),
			activities: []Activity{
				run("a", in(1), 5, 240), run("b", in(1), 5, 250), run("c", in(1), 5, 260), run("d", in(1), 5, 270),
				// f: 559 s over 2 km is 279.5 s/km, rounded half up to 280.
				run("e", in(1), 5, 280), session("f", "run", in(1), 559, meters(2000)), run("g", in(1), 5, 280),
				run("h", in(1), 5, 290),
			},
			want: []Entry{
				{UserID: "a", Rank: 1, Value: 10},
				{UserID: "b", Rank: 2, Value: 8},
				{UserID: "c", Rank: 3, Value: 6},
				{UserID: "d", Rank: 4, Value: 4},
				{UserID: "e", Rank: 5, Value: 2},
				{UserID: "f", Rank: 5, Value: 2},
				{UserID: "g", Rank: 5, Value: 2},
				{UserID: "h", Rank: 8},
			},
		},
		{
			name:         "6:59 on the first day of week 2: week 1 still provisional",
			now:          lock(1).Add(-time.Minute),
			participants: users("a", "b"),
			activities:   []Activity{run("a", in(1), 5, 270), run("b", in(1), 5, 300)},
			want:         []Entry{{UserID: "a", Rank: 1, Provisional: points(10)}, {UserID: "b", Rank: 1, Provisional: points(8)}},
		},
		{
			name:         "7:00 on the first day of week 2: week 1 acquired",
			now:          lock(1),
			participants: users("a", "b"),
			activities:   []Activity{run("a", in(1), 5, 270), run("b", in(1), 5, 300)},
			want: []Entry{
				{UserID: "a", Rank: 1, Value: 10, Provisional: points(0)}, {UserID: "b", Rank: 2, Value: 8, Provisional: points(0)},
			},
		},
		{
			name: "abandoned: kept in the weeks locked by the time they left, out of the others",
			now:  final,
			// q left after the lock of week 1, r before it.
			participants: []Participant{abandoned("q", lock(1).Add(time.Hour)), abandoned("r", lock(1).Add(-time.Second)), {UserID: "a"}},
			activities: []Activity{
				run("q", in(1), 5, 240), run("r", in(1), 5, 230), run("a", in(1), 5, 300), run("q", in(2), 5, 240), run("a", in(2), 5, 300),
			},
			// a: 8 in week 1 behind q, 10 in week 2 alone.
			want: []Entry{{UserID: "a", Rank: 1, Value: 18}, {UserID: "q", Abandoned: true}, {UserID: "r", Abandoned: true}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := w
			w.Now = tt.now
			got := fastest.Score(w, tt.participants, tt.activities)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Score() =\n%s\nwant\n%s", formatEntries(got), formatEntries(tt.want))
			}
		})
	}

	t.Run("the breakdown gives the pace in seconds per km, beside a criterion ranked highest first", func(t *testing.T) {
		w := w
		w.Now = final
		both := Points{Criteria: []string{CriterionLongestDuration, CriterionFastestRun}, Podium: 5}
		// a: the longest session, the slowest; b: no run.
		got := both.Breakdown(w, users("a", "b", "c"), []Activity{
			run("a", in(1), 10, 300), session("b", "hike", in(1), 600, meters(2000)), run("c", in(1), 5, 270),
		})
		want := map[string][]CriterionPoints{
			"a": {{Criterion: CriterionLongestDuration, Rank: 1, Value: 3000, Points: 10}, {Criterion: CriterionFastestRun, Rank: 2, Value: 300, Points: 8}},
			"b": {{Criterion: CriterionLongestDuration, Rank: 3, Value: 600, Points: 6}, {Criterion: CriterionFastestRun}},
			"c": {{Criterion: CriterionLongestDuration, Rank: 2, Value: 1350, Points: 8}, {Criterion: CriterionFastestRun, Rank: 1, Value: 270, Points: 10}},
		}
		for user, criteria := range want {
			if line := got[user][0].Criteria; !reflect.DeepEqual(line, criteria) {
				t.Errorf("Breakdown() %s week 1 = %+v, want %+v", user, line, criteria)
			}
		}
	})
}

func TestPaceRounding(t *testing.T) {
	tests := []struct {
		name            string
		seconds, meters int
		want            int
	}{
		{name: "4:30 per km", seconds: 1350, meters: 5000, want: 270},
		{name: "half a second rounds up", seconds: 539, meters: 2000, want: 270},
		{name: "just under half a second rounds down", seconds: 5389, meters: 20000, want: 269},
		{name: "under half a second per km is 0", seconds: 1, meters: 2001, want: 0},
		{name: "half a second per km is 1", seconds: 1, meters: 2000, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pace(tt.seconds, tt.meters); got != tt.want {
				t.Fatalf("pace(%d, %d) = %d, want %d", tt.seconds, tt.meters, got, tt.want)
			}
		})
	}
}

// TestWeekRankingMatchesRank checks that weekRanking, which ranks positions,
// gives each participant the rank Rank gives them on the same values (none
// for a value of 0), and the points of the scale for that rank: the ties of
// README §2.6 live in both.
func TestWeekRankingMatchesRank(t *testing.T) {
	tests := []struct {
		name       string
		podium     int
		values     []int
		wantPoints []int
	}{
		{name: "empty list", podium: 5},
		{name: "one participant", podium: 5, values: []int{15}, wantPoints: []int{10}},
		{name: "one participant at 0", podium: 10, values: []int{0}, wantPoints: []int{0}},
		{name: "distinct values", podium: 5, values: []int{10, 30, 20}, wantPoints: []int{6, 10, 8}},
		{name: "tie at the top", podium: 5, values: []int{30, 30, 20}, wantPoints: []int{10, 10, 6}},
		{
			name:       "tie at the edge of the top 5",
			podium:     5,
			values:     []int{60, 50, 40, 30, 20, 20, 10},
			wantPoints: []int{10, 8, 6, 4, 2, 2, 0},
		},
		{
			name:       "tie across the edge of the top 5",
			podium:     5,
			values:     []int{50, 40, 30, 20, 20, 20},
			wantPoints: []int{10, 8, 6, 4, 4, 4},
		},
		{
			name:       "tie at the edge of the top 10",
			podium:     10,
			values:     []int{110, 100, 90, 80, 70, 60, 50, 40, 30, 20, 20, 10},
			wantPoints: []int{25, 18, 15, 12, 10, 8, 6, 4, 2, 1, 1, 0},
		},
		{name: "values at 0", podium: 5, values: []int{20, 0, 0, 10}, wantPoints: []int{10, 0, 0, 8}},
		{name: "all at 0", podium: 10, values: []int{0, 0}, wantPoints: []int{0, 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			participants := make([]Participant, len(tt.values))
			values := make(map[string]int, len(tt.values))
			lines := make([]valueAt, len(tt.values))
			for k, v := range tt.values {
				id := fmt.Sprintf("u%02d", k)
				participants[k] = Participant{UserID: id}
				values[id] = v
				lines[k] = valueAt{k: k, value: v, none: v <= 0}
			}
			ranks := make(map[string]int, len(participants))
			for _, e := range Rank(participants, values) {
				ranks[e.UserID] = e.Rank
			}
			scale := pointsScale(tt.podium)
			got := weekRanking(lines, scale, false)
			if len(got) != len(tt.values) {
				t.Fatalf("weekRanking() returned %d lines, want %d", len(got), len(tt.values))
			}
			for _, l := range got {
				id, v := participants[l.k].UserID, tt.values[l.k]
				wantRank := 0
				if v > 0 {
					wantRank = ranks[id]
				}
				if l.rank != wantRank {
					t.Errorf("%s (value %d): weekRanking rank %d, Rank gives %d", id, v, l.rank, wantRank)
				}
				wantScale := 0
				if l.rank > 0 && l.rank <= len(scale) {
					wantScale = scale[l.rank-1]
				}
				if l.points != wantScale {
					t.Errorf("%s (rank %d): %d points, the scale gives %d", id, l.rank, l.points, wantScale)
				}
				if l.points != tt.wantPoints[l.k] {
					t.Errorf("%s (rank %d): %d points, want %d", id, l.rank, l.points, tt.wantPoints[l.k])
				}
			}
		})
	}
}
