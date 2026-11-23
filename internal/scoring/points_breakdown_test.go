package scoring

import (
	"reflect"
	"testing"
	"time"
)

// TestPointsBreakdown: each started week, each criterion of the category
// in its order, the rank in the weekly ranking (none for a value of 0),
// the value and the points; the sum of the locked weeks is the value of
// the ranking, that of the others its provisional points.
func TestPointsBreakdown(t *testing.T) {
	w := window(t, "America/Toronto", [3]int{2026, 1, 5}, 3)
	weekStart := func(n int) time.Time { s, _ := w.WeekBounds(n); return s }
	p := Points{
		Criteria: []string{CriterionSessionCount, CriterionLongestDuration},
		Podium:   5,
	}
	// Week 1: ann 2 sessions of 40 and 30 min, bob 1 session of 40 min,
	// cid nothing. Week 2: ann 1 of 50 min, bob 1 of 50 min (equal), cid 1
	// of 10 min (under the minimum: no session counted, but a duration).
	activities := []Activity{
		session("ann", "run", weekStart(1).Add(10*time.Hour), 40*60, nil),
		session("ann", "run", weekStart(1).Add(30*time.Hour), 30*60, nil),
		session("bob", "run", weekStart(1).Add(10*time.Hour), 40*60, nil),
		session("ann", "run", weekStart(2).Add(10*time.Hour), 50*60, nil),
		session("bob", "run", weekStart(2).Add(10*time.Hour), 50*60, nil),
		session("cid", "run", weekStart(2).Add(10*time.Hour), 10*60, nil),
	}
	// Week 1 locked, week 2 started, week 3 not started.
	w.Now = weekStart(2).Add(48 * time.Hour)
	participants := users("ann", "bob", "cid")

	got := p.Breakdown(w, participants, activities)
	want := map[string][]WeekPoints{
		"ann": {
			{Week: 1, Points: 20, Criteria: []CriterionPoints{
				{Criterion: CriterionSessionCount, Rank: 1, Value: 2, Points: 10},
				{Criterion: CriterionLongestDuration, Rank: 1, Value: 40 * 60, Points: 10},
			}},
			{Week: 2, Points: 20, Criteria: []CriterionPoints{
				{Criterion: CriterionSessionCount, Rank: 1, Value: 1, Points: 10},
				{Criterion: CriterionLongestDuration, Rank: 1, Value: 50 * 60, Points: 10},
			}},
		},
		"bob": {
			{Week: 1, Points: 18, Criteria: []CriterionPoints{
				{Criterion: CriterionSessionCount, Rank: 2, Value: 1, Points: 8},
				{Criterion: CriterionLongestDuration, Rank: 1, Value: 40 * 60, Points: 10},
			}},
			{Week: 2, Points: 20, Criteria: []CriterionPoints{
				{Criterion: CriterionSessionCount, Rank: 1, Value: 1, Points: 10},
				{Criterion: CriterionLongestDuration, Rank: 1, Value: 50 * 60, Points: 10},
			}},
		},
		"cid": {
			// A value of 0 has no rank and scores nothing.
			{Week: 1, Points: 0, Criteria: []CriterionPoints{
				{Criterion: CriterionSessionCount},
				{Criterion: CriterionLongestDuration},
			}},
			{Week: 2, Points: 6, Criteria: []CriterionPoints{
				{Criterion: CriterionSessionCount},
				{Criterion: CriterionLongestDuration, Rank: 3, Value: 10 * 60, Points: 6},
			}},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Breakdown() = %+v\nwant %+v", got, want)
	}

	// The sum of the breakdown is the ranking: locked weeks acquired, the
	// others provisional.
	for _, e := range p.Score(w, participants, activities) {
		acquired, provisional := 0, 0
		for _, wp := range got[e.UserID] {
			if wp.Week <= w.LockedWeeks() {
				acquired += wp.Points
			} else {
				provisional += wp.Points
			}
		}
		if e.Value != acquired || e.Provisional == nil || *e.Provisional != provisional {
			t.Fatalf("%s: ranking %d (+%v), breakdown %d (+%d)", e.UserID, e.Value, deref(e.Provisional), acquired, provisional)
		}
	}
}

// TestPointsBreakdownOfAParticipantWhoLeft: a participant who abandoned
// keeps the weeks locked by the time they left, as in the ranking, and
// none after.
func TestPointsBreakdownOfAParticipantWhoLeft(t *testing.T) {
	w := window(t, "America/Toronto", [3]int{2026, 1, 5}, 3)
	weekStart := func(n int) time.Time { s, _ := w.WeekBounds(n); return s }
	p := Points{Criteria: []string{CriterionLongestDuration}, Podium: 5}
	activities := []Activity{
		session("ann", "run", weekStart(1).Add(10*time.Hour), 40*60, nil),
		session("ann", "run", weekStart(2).Add(10*time.Hour), 40*60, nil),
		session("bob", "run", weekStart(2).Add(10*time.Hour), 30*60, nil),
	}
	w.Now = w.WeekLocksAt(3)
	// Ann left in week 2, once week 1 was locked.
	participants := []Participant{
		{UserID: "ann", Abandoned: true, AbandonedAt: at(weekStart(2).Add(20 * time.Hour))},
		{UserID: "bob"},
	}
	got := p.Breakdown(w, participants, activities)
	if weeks := got["ann"]; len(weeks) != 1 || weeks[0].Week != 1 || weeks[0].Points != 10 {
		t.Fatalf("ann %+v, want week 1 only, 10 points", weeks)
	}
	// Without ann in week 2, bob is first.
	if weeks := got["bob"]; len(weeks) != 3 || weeks[1].Points != 10 {
		t.Fatalf("bob %+v, want 3 weeks, first in week 2", weeks)
	}
}

// TestPointsBreakdownBeforeTheStart: no week started, no week.
func TestPointsBreakdownBeforeTheStart(t *testing.T) {
	w := window(t, "America/Toronto", [3]int{2026, 1, 5}, 3)
	w.Now = w.Start.Add(-time.Hour)
	got := Points{Criteria: []string{CriterionLongestDuration}, Podium: 5}.Breakdown(w, users("ann"), nil)
	if len(got["ann"]) != 0 {
		t.Fatalf("Breakdown() = %+v, want no week", got)
	}
}
