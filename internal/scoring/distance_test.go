package scoring

import (
	"reflect"
	"testing"
	"time"
)

// suggested are the factors the catalogue suggests (README §2.4), frozen in
// a challenge that kept them.
var suggested = map[string]float64{
	"run": 1, "walk": 1, "hike": 1, "swim": 1, "other": 1,
	"ride": 0.3, "rowing": 0.5, "roller": 0.5, "xc_ski": 0.5,
}

func meters(m int) *int { return &m }

func TestDistanceScore(t *testing.T) {
	w := window(t, "America/Toronto", [3]int{2026, 3, 2}, 2)
	in := w.Start.Add(time.Hour)
	act := func(user, sport string, at time.Time, distance *int) Activity {
		return Activity{UserID: user, SportCode: sport, StartedAt: at, DurationSeconds: 3600, DistanceMeters: distance}
	}
	one := func(sport string, distance *int) []Activity { return []Activity{act("a", sport, in, distance)} }
	alone := []Participant{{UserID: "a"}}
	tests := []struct {
		name         string
		factors      map[string]float64
		participants []Participant
		activities   []Activity
		want         []Entry
	}{
		{name: "run ×1", factors: suggested, participants: alone, activities: one("run", meters(5000)), want: []Entry{{UserID: "a", Rank: 1, Value: 5000}}},
		{name: "walk ×1", factors: suggested, participants: alone, activities: one("walk", meters(5000)), want: []Entry{{UserID: "a", Rank: 1, Value: 5000}}},
		{name: "hike ×1", factors: suggested, participants: alone, activities: one("hike", meters(5000)), want: []Entry{{UserID: "a", Rank: 1, Value: 5000}}},
		{name: "swim ×1", factors: suggested, participants: alone, activities: one("swim", meters(1500)), want: []Entry{{UserID: "a", Rank: 1, Value: 1500}}},
		{name: "ride ×0.3", factors: suggested, participants: alone, activities: one("ride", meters(20000)), want: []Entry{{UserID: "a", Rank: 1, Value: 6000}}},
		{name: "rowing ×0.5", factors: suggested, participants: alone, activities: one("rowing", meters(8000)), want: []Entry{{UserID: "a", Rank: 1, Value: 4000}}},
		{name: "roller ×0.5", factors: suggested, participants: alone, activities: one("roller", meters(8000)), want: []Entry{{UserID: "a", Rank: 1, Value: 4000}}},
		{name: "xc_ski ×0.5", factors: suggested, participants: alone, activities: one("xc_ski", meters(8000)), want: []Entry{{UserID: "a", Rank: 1, Value: 4000}}},
		{
			name:         "factor chosen by the creator",
			factors:      map[string]float64{"run": 1, "ride": 0.25},
			participants: alone,
			activities:   one("ride", meters(20000)),
			want:         []Entry{{UserID: "a", Rank: 1, Value: 5000}},
		},
		{
			name:         "frozen factor used, whatever the catalogue suggests now",
			factors:      map[string]float64{"run": 1, "ride": 0.4},
			participants: alone,
			activities:   one("ride", meters(10000)),
			want:         []Entry{{UserID: "a", Rank: 1, Value: 4000}},
		},
		{
			name:         "sport without distance does not count",
			factors:      suggested,
			participants: alone,
			activities:   one("yoga", meters(3000)),
			want:         []Entry{{UserID: "a", Rank: 1}},
		},
		{name: "other with a distance counts", factors: suggested, participants: alone, activities: one("other", meters(3000)), want: []Entry{{UserID: "a", Rank: 1, Value: 3000}}},
		{
			name:         "other with its factor chosen by the creator",
			factors:      map[string]float64{"run": 1, "other": 0.5},
			participants: alone,
			activities:   one("other", meters(3000)),
			want:         []Entry{{UserID: "a", Rank: 1, Value: 1500}},
		},
		{name: "other without distance does not count", factors: suggested, participants: alone, activities: one("other", nil), want: []Entry{{UserID: "a", Rank: 1}}},
		{
			name:         "sum exact, rounded to the meter at the end",
			factors:      suggested,
			participants: alone,
			// 1001 × 0.3 = 300.3, twice: 600.6 m, not 300 + 300.
			activities: []Activity{act("a", "ride", in, meters(1001)), act("a", "ride", in, meters(1001))},
			want:       []Entry{{UserID: "a", Rank: 1, Value: 601}},
		},
		{
			name:         "half a meter rounded up",
			factors:      suggested,
			participants: alone,
			// 1001 × 0.5 = 500.5 m.
			activities: one("rowing", meters(1001)),
			want:       []Entry{{UserID: "a", Rank: 1, Value: 501}},
		},
		{
			name:         "factor not exact in binary",
			factors:      map[string]float64{"run": 1, "ride": 0.29},
			participants: alone,
			// 0.29 × 100 is 28.999… in float64.
			activities: one("ride", meters(10000)),
			want:       []Entry{{UserID: "a", Rank: 1, Value: 2900}},
		},
		{
			name:         "non-participant ignored, abandoned at 0",
			factors:      suggested,
			participants: []Participant{{UserID: "a"}, {UserID: "b", Abandoned: true}},
			activities: []Activity{
				act("a", "run", in, meters(1000)), act("b", "run", in, meters(9000)), act("z", "run", in, meters(9000)),
			},
			want: []Entry{{UserID: "a", Rank: 1, Value: 1000}, {UserID: "b", Abandoned: true}},
		},
		{
			name:         "less than a meter apart after rounding: tie",
			factors:      suggested,
			participants: []Participant{{UserID: "a"}, {UserID: "b"}},
			// 10001 × 0.3 = 3000.3 m against 3000 m of running.
			activities: []Activity{act("a", "ride", in, meters(10001)), act("b", "run", in, meters(3000))},
			want:       []Entry{{UserID: "a", Rank: 1, Value: 3000}, {UserID: "b", Rank: 1, Value: 3000}},
		},
		{
			name:         "only the activities of the window count",
			factors:      suggested,
			participants: alone,
			activities: []Activity{
				act("a", "run", w.Start.Add(-time.Second), meters(100)),
				act("a", "run", w.Start, meters(10)),
				act("a", "run", w.End.Add(-time.Second), meters(20)),
				act("a", "run", w.End, meters(1000)),
			},
			want: []Entry{{UserID: "a", Rank: 1, Value: 30}},
		},
		{
			name:         "sports mixed, participant without activity at 0",
			factors:      suggested,
			participants: []Participant{{UserID: "a"}, {UserID: "b"}},
			activities:   []Activity{act("a", "run", in, meters(5000)), act("a", "ride", in, meters(10000))},
			want:         []Entry{{UserID: "a", Rank: 1, Value: 8000}, {UserID: "b", Rank: 2}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Distance{Factors: tt.factors}.Score(w, tt.participants, tt.activities)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Score() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDistanceWeighted(t *testing.T) {
	d := Distance{Factors: map[string]float64{"run": 1, "ride": 0.3, "rowing": 0.55}}
	tests := []struct {
		name     string
		sport    string
		distance *int
		want     *int
	}{
		{name: "run ×1", sport: "run", distance: meters(8200), want: meters(8200)},
		{name: "ride ×0.3", sport: "ride", distance: meters(20000), want: meters(6000)},
		// 1001 × 0.3 = 300.3: rounded to the meter.
		{name: "rounded down", sport: "ride", distance: meters(1001), want: meters(300)},
		// 1 × 0.55 = 0.55: half up, as the ranking.
		{name: "rounded half up", sport: "rowing", distance: meters(1), want: meters(1)},
		{name: "sport without a factor", sport: "yoga", distance: meters(1000), want: nil},
		{name: "no distance", sport: "run", distance: nil, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := d.Weighted(tt.sport, tt.distance)
			if (got == nil) != (tt.want == nil) || (got != nil && *got != *tt.want) {
				t.Fatalf("Weighted = %v, want %v", deref(got), deref(tt.want))
			}
		})
	}
}

// TestDistanceWeightedMatchesScore checks that the weighted distance of a
// lone activity is its value in the ranking.
func TestDistanceWeightedMatchesScore(t *testing.T) {
	w := window(t, "America/Toronto", [3]int{2026, 3, 2}, 2)
	for _, sport := range []string{"run", "ride", "rowing", "roller"} {
		for _, m := range []int{1, 5, 333, 1001, 8217, 42195} {
			a := Activity{UserID: "a", SportCode: sport, StartedAt: w.Start, DurationSeconds: 60, DistanceMeters: meters(m)}
			entries := Distance{Factors: suggested}.Score(w, []Participant{{UserID: "a"}}, []Activity{a})
			got := Distance{Factors: suggested}.Weighted(sport, meters(m))
			if got == nil || *got != entries[0].Value {
				t.Errorf("%s %d m: weighted %v, ranking %d", sport, m, deref(got), entries[0].Value)
			}
		}
	}
}

func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
