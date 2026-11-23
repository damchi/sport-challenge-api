package scoring

import (
	"reflect"
	"testing"
	"time"
)

func TestElevationScore(t *testing.T) {
	// Two weeks from Monday 2026-03-02 in Toronto: the clocks go forward on
	// Sunday 2026-03-08, inside the window.
	w := window(t, "America/Toronto", [3]int{2026, 3, 2}, 2)
	in := w.Start.Add(time.Hour)
	act := func(user, sport string, at time.Time, elevation *int) Activity {
		return Activity{UserID: user, SportCode: sport, StartedAt: at, DurationSeconds: 3600, DistanceMeters: meters(10000), ElevationMeters: elevation}
	}
	tests := []struct {
		name         string
		participants []Participant
		activities   []Activity
		want         []Entry
	}{
		{
			name:         "no activity",
			participants: []Participant{{UserID: "a"}, {UserID: "b"}},
			want:         []Entry{{UserID: "a", Rank: 1}, {UserID: "b", Rank: 1}},
		},
		{
			name:         "every sport with an elevation gain counts, real meters, no factor",
			participants: []Participant{{UserID: "a"}, {UserID: "b"}},
			activities: []Activity{
				act("a", "run", in, meters(300)),
				act("a", "ride", in.Add(2*time.Hour), meters(1200)),
				act("a", "other", in.Add(4*time.Hour), meters(50)),
				act("b", "hike", in, meters(1000)),
			},
			want: []Entry{{UserID: "a", Rank: 1, Value: 1550}, {UserID: "b", Rank: 2, Value: 1000}},
		},
		{
			name:         "an activity without elevation gain counts for 0 m",
			participants: []Participant{{UserID: "a"}, {UserID: "b"}},
			activities: []Activity{
				act("a", "run", in, nil),
				act("a", "swim", in.Add(2*time.Hour), nil),
				act("b", "run", in, meters(0)),
			},
			want: []Entry{{UserID: "a", Rank: 1}, {UserID: "b", Rank: 1}},
		},
		{
			name:         "only the activities started in the window count",
			participants: []Participant{{UserID: "a"}},
			activities: []Activity{
				act("a", "run", w.Start.Add(-time.Second), meters(1000)),
				act("a", "run", w.Start, meters(10)),
				act("a", "run", w.End.Add(-time.Second), meters(20)),
				act("a", "run", w.End, meters(3000)),
			},
			want: []Entry{{UserID: "a", Rank: 1, Value: 30}},
		},
		{
			name:         "across the change of time, the whole local weeks count",
			participants: []Participant{{UserID: "a"}},
			activities: []Activity{
				// Sunday 2026-03-08 23:30 EDT, end of week 1.
				act("a", "run", time.Date(2026, 3, 9, 3, 30, 0, 0, time.UTC), meters(100)),
				// Sunday 2026-03-15 23:59 EDT, last minute of week 2.
				act("a", "run", time.Date(2026, 3, 16, 3, 59, 0, 0, time.UTC), meters(200)),
				// Monday 2026-03-16 00:30 EDT: after the window, though within
				// 14 × 24 h of its start.
				act("a", "run", time.Date(2026, 3, 16, 4, 30, 0, 0, time.UTC), meters(5000)),
			},
			want: []Entry{{UserID: "a", Rank: 1, Value: 300}},
		},
		{
			name:         "equal elevation gains share the rank",
			participants: []Participant{{UserID: "a"}, {UserID: "b"}, {UserID: "c"}},
			activities:   []Activity{act("a", "run", in, meters(500)), act("b", "ride", in, meters(500)), act("c", "walk", in, meters(100))},
			want:         []Entry{{UserID: "a", Rank: 1, Value: 500}, {UserID: "b", Rank: 1, Value: 500}, {UserID: "c", Rank: 3, Value: 100}},
		},
		{
			name:         "activities of non-participants ignored",
			participants: []Participant{{UserID: "a"}},
			activities:   []Activity{act("a", "run", in, meters(100)), act("z", "run", in, meters(9000))},
			want:         []Entry{{UserID: "a", Rank: 1, Value: 100}},
		},
		{
			name:         "abandoned participant listed, unranked, activities not counted",
			participants: []Participant{{UserID: "a", Abandoned: true}, {UserID: "b"}},
			activities:   []Activity{act("a", "run", in, meters(9000)), act("b", "run", in, meters(100))},
			want:         []Entry{{UserID: "b", Rank: 1, Value: 100}, {UserID: "a", Abandoned: true}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (Elevation{}).Score(w, tt.participants, tt.activities); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Score() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
