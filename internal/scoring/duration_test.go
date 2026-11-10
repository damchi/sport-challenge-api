package scoring

import (
	"reflect"
	"testing"
	"time"
)

func TestDurationScore(t *testing.T) {
	w := window(t, "America/Toronto", [3]int{2026, 3, 2}, 2)
	in := w.Start.Add(time.Hour)
	run := func(user string, at time.Time, seconds int) Activity {
		return Activity{UserID: user, SportCode: "run", StartedAt: at, DurationSeconds: seconds}
	}
	tests := []struct {
		name         string
		participants []Participant
		activities   []Activity
		want         []Entry
	}{
		{
			name:         "every sport counts, other included",
			participants: []Participant{{UserID: "a"}, {UserID: "b"}},
			activities: []Activity{
				run("a", in, 1800),
				{UserID: "a", SportCode: "yoga", StartedAt: in, DurationSeconds: 3600},
				{UserID: "a", SportCode: "other", StartedAt: in, DurationSeconds: 600},
				{UserID: "b", SportCode: "bike", StartedAt: in, DurationSeconds: 5000},
			},
			want: []Entry{{UserID: "a", Rank: 1, Value: 6000}, {UserID: "b", Rank: 2, Value: 5000}},
		},
		{
			name:         "only the activities of the window count",
			participants: []Participant{{UserID: "a"}},
			activities: []Activity{
				run("a", w.Start.Add(-time.Second), 100),
				run("a", w.Start, 10),
				run("a", w.End.Add(-time.Second), 20),
				run("a", w.End, 1000),
			},
			want: []Entry{{UserID: "a", Rank: 1, Value: 30}},
		},
		{
			name:         "equal times share the rank",
			participants: []Participant{{UserID: "a"}, {UserID: "b"}, {UserID: "c"}},
			activities:   []Activity{run("a", in, 600), run("b", in, 600), run("c", in, 300)},
			want:         []Entry{{UserID: "a", Rank: 1, Value: 600}, {UserID: "b", Rank: 1, Value: 600}, {UserID: "c", Rank: 3, Value: 300}},
		},
		{
			name:         "participant without activity at 0",
			participants: []Participant{{UserID: "a"}, {UserID: "b"}},
			activities:   []Activity{run("a", in, 600)},
			want:         []Entry{{UserID: "a", Rank: 1, Value: 600}, {UserID: "b", Rank: 2}},
		},
		{
			name:         "activities of non-participants ignored",
			participants: []Participant{{UserID: "a"}},
			activities:   []Activity{run("a", in, 600), run("z", in, 9000)},
			want:         []Entry{{UserID: "a", Rank: 1, Value: 600}},
		},
		{
			name:         "abandoned participant listed, unranked, activities not counted",
			participants: []Participant{{UserID: "a", Abandoned: true}, {UserID: "b"}},
			activities:   []Activity{run("a", in, 9000), run("b", in, 600)},
			want:         []Entry{{UserID: "b", Rank: 1, Value: 600}, {UserID: "a", Abandoned: true}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (Duration{}).Score(w, tt.participants, tt.activities); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Score() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
