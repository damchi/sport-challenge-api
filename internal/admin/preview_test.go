package admin

import (
	"maps"
	"reflect"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/leaderboard"
	"github.com/damchi/sport-challenge-api/internal/scoring"
)

func TestResultsChange(t *testing.T) {
	tests := []struct {
		before, after bool
		want          string
	}{
		{true, true, ResultsRefrozen},
		{true, false, ResultsReopened},
		{false, true, ResultsFrozen},
		{false, false, ""},
	}
	for _, tt := range tests {
		if got := resultsChange(tt.before, tt.after); got != tt.want {
			t.Errorf("resultsChange(%v, %v) = %q, want %q", tt.before, tt.after, got, tt.want)
		}
	}
}

// eliminationBoard ranks, in the elimination category, each participant
// with the minimum of the week in progress given.
func eliminationBoard(minimums map[string]int) *leaderboard.Leaderboard {
	cat := leaderboard.CategoryRanking{Type: leaderboard.CategoryElimination}
	for _, user := range []string{"a", "b"} {
		e := leaderboard.Ranked{Entry: scoring.Entry{UserID: user, Progress: &scoring.Progress{}}}
		if m, ok := minimums[user]; ok {
			e.Progress.MinimumMeters = &m
		}
		e.Name = leaderboard.Name{Pseudo: new(user)}
		cat.Entries = append(cat.Entries, e)
	}
	return &leaderboard.Leaderboard{Categories: []leaderboard.CategoryRanking{cat}}
}

func TestPreview(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	starts, ends := time.Date(2026, 6, 1, 4, 0, 0, 0, time.UTC), time.Date(2026, 6, 29, 4, 0, 0, 0, time.UTC)
	state := func(sport string, frozen bool, eliminations ...EliminationRow) ChallengeState {
		return ChallengeState{Title: "Juin", ReferenceSport: sport, StartsAt: starts, EndsAt: ends, Frozen: frozen, Eliminations: eliminations}
	}
	a, b := UserRef{ID: "a", Pseudo: new("a")}, UserRef{ID: "b", Pseudo: new("b")}
	// Weeks 1 and 2 locked, week 3 in progress.
	weeks := func(points map[string]map[int]int) *leaderboard.Weeks {
		return &leaderboard.Weeks{Current: 3, Locked: 2, Points: points}
	}
	tests := []struct {
		name          string
		before, after reading
		lc            lifecycle
		want          ChallengePreview
	}{
		{
			// The points of week 2 move; a no longer eliminated in week 2,
			// the minimum of week 3 moves with it.
			name: "an activity added in a locked week",
			before: reading{
				state: state("run", false, EliminationRow{User: a, WeekNumber: 2, DistanceMeters: 4000, RequiredMeters: 5000}),
				board: eliminationBoard(map[string]int{"b": 7000}),
				weeks: weeks(map[string]map[int]int{"a": {1: 10, 2: 8, 3: 10}, "b": {1: 8, 2: 10, 3: 8}}),
			},
			after: reading{
				state: state("run", false),
				board: eliminationBoard(map[string]int{"a": 8000, "b": 7000}),
				weeks: weeks(map[string]map[int]int{"a": {1: 10, 2: 10, 3: 10}, "b": {1: 8, 2: 8, 3: 8}}),
			},
			want: ChallengePreview{
				ChallengeID: challengeID, Title: "Juin", Status: StatusOngoing, ReferenceSport: SportChange{"run", "run"},
				Weeks: []WeekChange{{Number: 2, Locked: true}, {Number: 3}},
				Points: []PointsChange{
					{User: a, Week: 2, Before: 8, After: 10},
					{User: b, Week: 2, Before: 10, After: 8},
				},
				Eliminations: []EliminationChange{{User: a, Before: &EliminationRow{User: a, WeekNumber: 2, DistanceMeters: 4000, RequiredMeters: 5000}}},
			},
		},
		{
			name: "provisional points of the week in progress",
			before: reading{
				state: state("run", false), board: eliminationBoard(nil),
				weeks: weeks(map[string]map[int]int{"a": {3: 8}, "b": {3: 10}}),
			},
			after: reading{
				state: state("run", false), board: eliminationBoard(nil),
				weeks: weeks(map[string]map[int]int{"a": {3: 10}, "b": {3: 8}}),
			},
			want: ChallengePreview{
				ChallengeID: challengeID, Title: "Juin", Status: StatusOngoing, ReferenceSport: SportChange{"run", "run"},
				Weeks: []WeekChange{{Number: 3}},
				Points: []PointsChange{
					{User: a, Week: 3, Before: 8, After: 10, Provisional: true},
					{User: b, Week: 3, Before: 10, After: 8, Provisional: true},
				},
			},
		},
		{
			name: "eliminations alone judged again",
			before: reading{
				state: state("run", false, EliminationRow{User: a, WeekNumber: 2, DistanceMeters: 4000, RequiredMeters: 5000}),
				board: eliminationBoard(nil), weeks: weeks(nil),
			},
			after: reading{
				state: state("run", false, EliminationRow{User: a, WeekNumber: 2, DistanceMeters: 4500, RequiredMeters: 5000}),
				board: eliminationBoard(nil), weeks: weeks(nil),
			},
			want: ChallengePreview{
				ChallengeID: challengeID, Title: "Juin", Status: StatusOngoing, ReferenceSport: SportChange{"run", "run"},
				Weeks: []WeekChange{{Number: 2, Locked: true}},
				Eliminations: []EliminationChange{{
					User:   a,
					Before: &EliminationRow{User: a, WeekNumber: 2, DistanceMeters: 4000, RequiredMeters: 5000},
					After:  &EliminationRow{User: a, WeekNumber: 2, DistanceMeters: 4500, RequiredMeters: 5000},
				}},
			},
		},
		{
			name:   "the minimum of the week in progress alone recomputed",
			before: reading{state: state("run", false), board: eliminationBoard(map[string]int{"a": 7000}), weeks: weeks(nil)},
			after:  reading{state: state("run", false), board: eliminationBoard(map[string]int{"a": 7500}), weeks: weeks(nil)},
			want: ChallengePreview{
				ChallengeID: challengeID, Title: "Juin", Status: StatusOngoing, ReferenceSport: SportChange{"run", "run"},
				Weeks: []WeekChange{{Number: 3}},
			},
		},
		{
			// Week 2, locked before, is in progress after: its elimination goes.
			name: "a first day moved later",
			before: reading{
				state: state("run", false, EliminationRow{User: a, WeekNumber: 2, DistanceMeters: 4000, RequiredMeters: 5000}),
				board: eliminationBoard(nil), weeks: weeks(nil),
			},
			after: reading{state: state("run", false), board: eliminationBoard(nil), weeks: &leaderboard.Weeks{Current: 2, Locked: 1}},
			want: ChallengePreview{
				ChallengeID: challengeID, Title: "Juin", Status: StatusOngoing, ReferenceSport: SportChange{"run", "run"},
				Weeks:        []WeekChange{{Number: 2}},
				Eliminations: []EliminationChange{{User: a, Before: &EliminationRow{User: a, WeekNumber: 2, DistanceMeters: 4000, RequiredMeters: 5000}}},
			},
		},
		{
			// Over at 4 weeks, week 4 not locked yet: no week in progress.
			name:   "the last week before it locks",
			before: reading{state: state("run", false), board: eliminationBoard(nil), weeks: &leaderboard.Weeks{Locked: 3, Points: map[string]map[int]int{"a": {4: 8}}}},
			after:  reading{state: state("run", false), board: eliminationBoard(nil), weeks: &leaderboard.Weeks{Locked: 3, Points: map[string]map[int]int{"a": {4: 10}}}},
			want: ChallengePreview{
				ChallengeID: challengeID, Title: "Juin", Status: StatusOngoing, ReferenceSport: SportChange{"run", "run"},
				Weeks:  []WeekChange{{Number: 4}},
				Points: []PointsChange{{User: a, Week: 4, Before: 8, After: 10, Provisional: true}},
			},
		},
		{
			name:   "a points category added",
			before: reading{state: state("run", false), board: eliminationBoard(nil), weeks: weeks(nil)},
			after:  reading{state: state("run", false), board: eliminationBoard(nil), weeks: weeks(map[string]map[int]int{"a": {1: 10}})},
			want: ChallengePreview{
				ChallengeID: challengeID, Title: "Juin", Status: StatusOngoing, ReferenceSport: SportChange{"run", "run"},
				Weeks:  []WeekChange{{Number: 1, Locked: true}},
				Points: []PointsChange{{User: a, Week: 1, Before: 0, After: 10}},
			},
		},
		{
			// b left in week 3: their provisional points go.
			name:   "a participant who left",
			before: reading{state: state("run", false), board: eliminationBoard(nil), weeks: weeks(map[string]map[int]int{"a": {3: 8}, "b": {3: 10}})},
			after:  reading{state: state("run", false), board: eliminationBoard(nil), weeks: weeks(map[string]map[int]int{"a": {3: 10}})},
			want: ChallengePreview{
				ChallengeID: challengeID, Title: "Juin", Status: StatusOngoing, ReferenceSport: SportChange{"run", "run"},
				Weeks: []WeekChange{{Number: 3}},
				Points: []PointsChange{
					{User: a, Week: 3, Before: 8, After: 10, Provisional: true},
					{User: b, Week: 3, Before: 10, After: 0, Provisional: true},
				},
			},
		},
		{
			name:   "a deletion: nothing judged again",
			before: reading{state: state("run", true), board: eliminationBoard(nil), weeks: weeks(map[string]map[int]int{"a": {1: 10}})},
			after:  reading{state: state("run", true)},
			lc:     deletes,
			want:   ChallengePreview{ChallengeID: challengeID, Title: "Juin", Status: StatusOngoing, ReferenceSport: SportChange{"run", "run"}, Before: eliminationBoard(nil)},
		},
		{
			name:   "a restore: no weeks nor points to compare",
			before: reading{state: state("run", true)},
			after:  reading{state: state("run", true), board: eliminationBoard(nil), weeks: weeks(map[string]map[int]int{"a": {1: 10}})},
			lc:     restores,
			want: ChallengePreview{
				ChallengeID: challengeID, Title: "Juin", Status: StatusOngoing, ReferenceSport: SportChange{"run", "run"},
				Results: ResultsRefrozen, After: eliminationBoard(nil),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := preview(challengeID, tt.before, tt.after, now, tt.lc)
			if tt.want.Before == nil {
				tt.want.Before = tt.before.board
			}
			if tt.want.After == nil {
				tt.want.After = tt.after.board
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("preview() =\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}

func TestPreviewStatusAfterTheChange(t *testing.T) {
	starts := time.Date(2026, 6, 1, 4, 0, 0, 0, time.UTC)
	now := starts.AddDate(0, 0, 20)
	before := reading{state: ChallengeState{StartsAt: starts, EndsAt: starts.AddDate(0, 0, 14)}}
	// Two more weeks: ongoing again.
	after := reading{state: ChallengeState{StartsAt: starts, EndsAt: starts.AddDate(0, 0, 28)}}
	if got := preview(challengeID, before, after, now, 0).Status; got != StatusOngoing {
		t.Fatalf("status %q, want %q", got, StatusOngoing)
	}
}

func TestPreviewOfAFinishedChallenge(t *testing.T) {
	starts := time.Date(2026, 6, 1, 4, 0, 0, 0, time.UTC)
	ends := starts.AddDate(0, 0, 28)
	state := func(sport string) ChallengeState {
		return ChallengeState{Title: "Juin", ReferenceSport: sport, StartsAt: starts, EndsAt: ends, Frozen: true}
	}
	board := eliminationBoard(nil)
	weeks := &leaderboard.Weeks{Locked: 4}
	got := preview(challengeID, reading{state: state("run"), board: board, weeks: weeks},
		reading{state: state("ride"), board: board, weeks: weeks}, ends.AddDate(0, 0, 3), 0)
	want := ChallengePreview{
		ChallengeID: challengeID, Title: "Juin", Status: StatusFinished, ReferenceSport: SportChange{"run", "ride"},
		Results: ResultsRefrozen, Before: board, After: board,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("preview() =\n%+v\nwant\n%+v", got, want)
	}
}

func TestMinimums(t *testing.T) {
	progress := func(meters, repetitions *int) leaderboard.Leaderboard {
		e := leaderboard.Ranked{Entry: scoring.Entry{UserID: "a", Progress: &scoring.Progress{MinimumMeters: meters, MinimumRepetitions: repetitions}}}
		return leaderboard.Leaderboard{Categories: []leaderboard.CategoryRanking{{Type: leaderboard.CategoryElimination, Entries: []leaderboard.Ranked{e}}}}
	}
	tests := []struct {
		name          string
		before, after leaderboard.Leaderboard
		differ        bool
	}{
		{"same meters", progress(new(5000), nil), progress(new(5000), nil), false},
		{"other meters", progress(new(5000), nil), progress(new(6000), nil), true},
		{"same repetitions", progress(nil, new(50)), progress(nil, new(50)), false},
		{"other repetitions", progress(nil, new(50)), progress(nil, new(60)), true},
		{"repetitions appear", progress(nil, nil), progress(nil, new(50)), true},
		{"unit changes", progress(new(50), nil), progress(nil, new(50)), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := !maps.Equal(minimums(tt.before), minimums(tt.after)); got != tt.differ {
				t.Errorf("minimums differ = %v, want %v", got, tt.differ)
			}
		})
	}
}
