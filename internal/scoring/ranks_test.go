package scoring

import (
	"reflect"
	"testing"
)

func TestRank(t *testing.T) {
	active := func(ids ...string) []Participant {
		ps := make([]Participant, len(ids))
		for i, id := range ids {
			ps[i] = Participant{UserID: id}
		}
		return ps
	}
	tests := []struct {
		name         string
		participants []Participant
		values       map[string]int
		want         []Entry
	}{
		{name: "empty list", want: []Entry{}},
		{
			name:         "no tie, highest value first",
			participants: active("a", "b", "c"),
			values:       map[string]int{"a": 10, "b": 30, "c": 20},
			want:         []Entry{{UserID: "b", Rank: 1, Value: 30}, {UserID: "c", Rank: 2, Value: 20}, {UserID: "a", Rank: 3, Value: 10}},
		},
		{
			name:         "tie at the top",
			participants: active("a", "b", "c"),
			values:       map[string]int{"a": 30, "b": 30, "c": 20},
			want:         []Entry{{UserID: "a", Rank: 1, Value: 30}, {UserID: "b", Rank: 1, Value: 30}, {UserID: "c", Rank: 3, Value: 20}},
		},
		{
			name:         "tie in the middle",
			participants: active("a", "b", "c", "d"),
			values:       map[string]int{"a": 40, "b": 20, "c": 20, "d": 10},
			want: []Entry{
				{UserID: "a", Rank: 1, Value: 40},
				{UserID: "b", Rank: 2, Value: 20},
				{UserID: "c", Rank: 2, Value: 20},
				{UserID: "d", Rank: 4, Value: 10},
			},
		},
		{
			name:         "tie at the end",
			participants: active("a", "b", "c"),
			values:       map[string]int{"a": 40, "b": 10, "c": 10},
			want:         []Entry{{UserID: "a", Rank: 1, Value: 40}, {UserID: "b", Rank: 2, Value: 10}, {UserID: "c", Rank: 2, Value: 10}},
		},
		{
			name:         "all equal",
			participants: active("c", "a", "b"),
			values:       map[string]int{"a": 5, "b": 5, "c": 5},
			want:         []Entry{{UserID: "a", Rank: 1, Value: 5}, {UserID: "b", Rank: 1, Value: 5}, {UserID: "c", Rank: 1, Value: 5}},
		},
		{
			name:         "participant without value ranked at 0",
			participants: active("a", "b"),
			values:       map[string]int{"b": 5},
			want:         []Entry{{UserID: "b", Rank: 1, Value: 5}, {UserID: "a", Rank: 2}},
		},
		{
			name:         "tie at 0 at the bottom",
			participants: active("a", "b", "c"),
			values:       map[string]int{"c": 5},
			want:         []Entry{{UserID: "c", Rank: 1, Value: 5}, {UserID: "a", Rank: 2}, {UserID: "b", Rank: 2}},
		},
		{
			name: "abandoned listed after the ranked, without rank and at 0",
			participants: []Participant{
				{UserID: "c", Abandoned: true}, {UserID: "b"}, {UserID: "a", Abandoned: true}, {UserID: "d"},
			},
			values: map[string]int{"a": 50, "b": 10, "c": 100, "d": 20},
			want: []Entry{
				{UserID: "d", Rank: 1, Value: 20},
				{UserID: "b", Rank: 2, Value: 10},
				{UserID: "a", Abandoned: true},
				{UserID: "c", Abandoned: true},
			},
		},
		{
			name:         "only abandoned",
			participants: []Participant{{UserID: "a", Abandoned: true}},
			values:       map[string]int{"a": 10},
			want:         []Entry{{UserID: "a", Abandoned: true}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Rank(tt.participants, tt.values); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Rank() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRankByTier(t *testing.T) {
	participants := []Participant{{UserID: "a"}, {UserID: "b"}, {UserID: "c"}, {UserID: "d"}}
	values := map[string]int{"a": 10, "b": 10, "c": 10, "d": 20}
	tiers := map[string]int{"a": 2, "b": 3, "c": 2, "d": 1}
	// Same value in different tiers: distinct ranks; same tier and value: shared.
	want := []Entry{
		{UserID: "b", Rank: 1, Value: 10},
		{UserID: "a", Rank: 2, Value: 10},
		{UserID: "c", Rank: 2, Value: 10},
		{UserID: "d", Rank: 4, Value: 20},
	}
	if got := rankByTier(participants, values, tiers); !reflect.DeepEqual(got, want) {
		t.Fatalf("rankByTier() = %+v, want %+v", got, want)
	}
}
