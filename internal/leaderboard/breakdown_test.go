package leaderboard

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/scoring"
)

// pointsChallenge is twoWeeks with the cumulative time and the points on
// the longest session, and its activities: a has the longest session of
// week 1, b that of week 2.
func pointsChallenge(t *testing.T) (Challenge, []scoring.Activity, func(day, hour int) time.Time) {
	t.Helper()
	c := twoWeeks(t)
	c.Categories = []Category{
		{Type: CategoryCumulativeTime},
		{Type: CategoryPoints, Criteria: []string{scoring.CriterionLongestDuration}, Podium: 5, MinSessionSeconds: 1200},
	}
	// A member reads it: the values are shown.
	c.ReadsActivities = true
	loc := toronto(t)
	at := func(day, hour int) time.Time { return time.Date(2026, 6, day, hour, 0, 0, 0, loc).UTC() }
	group := func(user string, week time.Time, longest int) scoring.Activity {
		return scoring.Activity{UserID: user, SportCode: "run", StartedAt: week, DurationSeconds: longest, LongestSeconds: longest, Sessions: 1}
	}
	return c, []scoring.Activity{
		group("a", at(1, 10), 3600), group("b", at(1, 10), 1800),
		group("a", at(8, 10), 1000), group("b", at(8, 10), 3000),
	}, at
}

func TestBreakdownLive(t *testing.T) {
	c, activities, at := pointsChallenge(t)
	participants := []scoring.Participant{{UserID: "a"}, {UserID: "b"}}
	now := at(10, 12)
	repo := &fakeRepo{participants: participants, activities: activities}
	svc := NewService(&fakeChallenges{c: c}, pseudos, repo, clock.NewFake(now), 24*time.Hour)
	got, err := svc.Breakdown(t.Context(), auth.Actor{ID: "actor"}, "c1", "b")
	if err != nil {
		t.Fatal(err)
	}
	ten := 10
	start1, end1 := at(1, 0), at(8, 0)
	want := Breakdown{
		UserID: "b", ComputedAt: now, Acquired: 8, Provisional: &ten,
		Weeks: []BreakdownWeek{
			{Number: 1, StartsAt: start1, EndsAt: end1, Locked: true, Points: 8, Criteria: []scoring.CriterionPoints{
				{Criterion: scoring.CriterionLongestDuration, Rank: 2, Value: 1800, Points: 8},
			}},
			{Number: 2, StartsAt: end1, EndsAt: at(15, 0), Locked: false, Points: 10, Criteria: []scoring.CriterionPoints{
				{Criterion: scoring.CriterionLongestDuration, Rank: 1, Value: 3000, Points: 10},
			}},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Breakdown() = %+v\nwant %+v", got, want)
	}
	// The same as the leaderboard at the same instant.
	board, err := svc.Get(t.Context(), auth.Actor{ID: "actor"}, "c1")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range board.Categories[1].Entries {
		if e.UserID == "b" && (e.Value != got.Acquired || *e.Provisional != *got.Provisional) {
			t.Fatalf("leaderboard %d (+%d), breakdown %d (+%d)", e.Value, *e.Provisional, got.Acquired, *got.Provisional)
		}
	}
}

func TestBreakdownOfAParticipantWhoLeft(t *testing.T) {
	c, activities, at := pointsChallenge(t)
	// a left in week 2, once week 1 was locked.
	left := at(9, 12)
	participants := []scoring.Participant{{UserID: "a", Abandoned: true, AbandonedAt: &left}, {UserID: "b"}}
	repo := &fakeRepo{participants: participants, activities: activities}
	svc := NewService(&fakeChallenges{c: c}, pseudos, repo, clock.NewFake(at(10, 12)), 24*time.Hour)
	got, err := svc.Breakdown(t.Context(), auth.Actor{ID: "actor"}, "c1", "a")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Abandoned || got.Provisional != nil || got.Acquired != 10 || len(got.Weeks) != 1 || got.Weeks[0].Number != 1 {
		t.Fatalf("Breakdown() = %+v, want week 1 only, 10 acquired, no provisional points", got)
	}
}

func TestBreakdownFrozen(t *testing.T) {
	c, _, at := pointsChallenge(t)
	frozenAt := at(15, 8)
	repo := &fakeRepo{frozenAt: frozenAt, snapshots: map[string]json.RawMessage{
		CategoryCumulativeTime: json.RawMessage(`{"entries":[{"user_id":"a","rank":1,"value":4600,"abandoned":false}]}`),
		CategoryPoints: json.RawMessage(`{"entries":[{"user_id":"b","rank":1,"value":18,"abandoned":false,"weeks":[
			{"week":1,"points":8,"criteria":[{"criterion":"longest_duration","rank":2,"value":1800,"points":8}]},
			{"week":2,"points":10,"criteria":[{"criterion":"longest_duration","rank":1,"value":3000,"points":10}]}]}]}`),
	}}
	svc := NewService(&fakeChallenges{c: c}, pseudos, repo, clock.NewFake(c.EndsAt.AddDate(0, 1, 0)), 24*time.Hour)
	got, err := svc.Breakdown(t.Context(), auth.Actor{ID: "actor"}, "c1", "b")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Frozen || !got.ComputedAt.Equal(frozenAt) || got.Acquired != 18 || got.Provisional != nil ||
		len(got.Weeks) != 2 || !got.Weeks[1].Locked || got.Weeks[1].Criteria[0].Rank != 1 {
		t.Fatalf("Breakdown() = %+v, want the frozen weeks, 18 acquired", got)
	}
	if repo.calls != 0 {
		t.Fatalf("%d reads of participants or activities, want none once frozen", repo.calls)
	}
}

// TestBreakdownOfResultsFrozenWithoutIt: results frozen before the
// breakdown was stored are computed, as the leaderboard would.
func TestBreakdownOfResultsFrozenWithoutIt(t *testing.T) {
	c, activities, _ := pointsChallenge(t)
	repo := &fakeRepo{
		participants: []scoring.Participant{{UserID: "a"}, {UserID: "b"}}, activities: activities,
		snapshots: map[string]json.RawMessage{
			CategoryPoints: json.RawMessage(`{"entries":[{"user_id":"b","rank":1,"value":18,"abandoned":false}]}`),
		},
	}
	svc := NewService(&fakeChallenges{c: c}, pseudos, repo, clock.NewFake(c.EndsAt.AddDate(0, 1, 0)), 24*time.Hour)
	got, err := svc.Breakdown(t.Context(), auth.Actor{ID: "actor"}, "c1", "b")
	if err != nil {
		t.Fatal(err)
	}
	if got.Frozen || got.Acquired != 18 || len(got.Weeks) != 2 {
		t.Fatalf("Breakdown() = %+v, want 18 computed over 2 weeks", got)
	}
}

func TestBreakdownRefusals(t *testing.T) {
	c, activities, at := pointsChallenge(t)
	withoutPoints := twoWeeks(t)
	frozen := map[string]json.RawMessage{
		CategoryPoints: json.RawMessage(`{"entries":[{"user_id":"b","rank":1,"value":18,"abandoned":false,"weeks":[]}]}`),
	}
	tests := []struct {
		name      string
		c         Challenge
		cErr      error
		user      string
		now       time.Time
		snapshots map[string]json.RawMessage
		deleted   map[string]bool
		wantCode  apperr.Code
	}{
		{name: "challenge hidden", c: c, cErr: apperr.New(apperr.CodeChallengeNotFound, nil), user: "b", now: at(10, 12), wantCode: apperr.CodeChallengeNotFound},
		{name: "no points category", c: withoutPoints, user: "b", now: at(10, 12), wantCode: apperr.CodeNotFound},
		{name: "never joined", c: c, user: "z", now: at(10, 12), wantCode: apperr.CodeNotFound},
		{name: "not in the frozen results", c: c, user: "z", now: c.EndsAt.AddDate(0, 1, 0), snapshots: frozen, wantCode: apperr.CodeNotFound},
		{name: "account deleted since the freeze", c: c, user: "b", now: c.EndsAt.AddDate(0, 1, 0), snapshots: frozen, deleted: map[string]bool{"b": true}, wantCode: apperr.CodeNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{participants: []scoring.Participant{{UserID: "a"}, {UserID: "b"}}, activities: activities, snapshots: tt.snapshots, deleted: tt.deleted}
			svc := NewService(&fakeChallenges{c: tt.c, err: tt.cErr}, pseudos, repo, clock.NewFake(tt.now), 24*time.Hour)
			_, err := svc.Breakdown(t.Context(), auth.Actor{ID: "actor"}, "c1", tt.user)
			var appErr *apperr.Error
			if !errors.As(err, &appErr) || appErr.Code != tt.wantCode || appErr.Status != http.StatusNotFound {
				t.Fatalf("Breakdown() = %v, want 404 %s", err, tt.wantCode)
			}
			if tt.cErr != nil && repo.calls != 0 {
				t.Fatal("read before the rule passed")
			}
		})
	}
}

// TestBreakdownHidesTheValuesFromANonMember: a reader who may not see the
// activities (a non-participant of a public challenge) gets the ranks and
// the points, not the values behind them, live and frozen.
func TestBreakdownHidesTheValuesFromANonMember(t *testing.T) {
	c, activities, at := pointsChallenge(t)
	c.ReadsActivities = false
	frozen := map[string]json.RawMessage{
		CategoryPoints: json.RawMessage(`{"entries":[{"user_id":"b","rank":1,"value":18,"abandoned":false,"weeks":[
			{"week":1,"points":8,"criteria":[{"criterion":"longest_duration","rank":2,"value":1800,"points":8}]}]}]}`),
	}
	for name, now := range map[string]time.Time{"live": at(10, 12), "frozen": c.EndsAt.AddDate(0, 1, 0)} {
		t.Run(name, func(t *testing.T) {
			repo := &fakeRepo{participants: []scoring.Participant{{UserID: "a"}, {UserID: "b"}}, activities: activities, snapshots: frozen}
			svc := NewService(&fakeChallenges{c: c}, pseudos, repo, clock.NewFake(now), 24*time.Hour)
			got, err := svc.Breakdown(t.Context(), auth.Actor{ID: "actor"}, "c1", "b")
			if err != nil {
				t.Fatal(err)
			}
			if !got.ValuesHidden || got.Weeks[0].Points != 8 || got.Weeks[0].Criteria[0] != (scoring.CriterionPoints{Criterion: scoring.CriterionLongestDuration, Rank: 2, Points: 8}) {
				t.Fatalf("Breakdown() = %+v, want the rank and points of week 1, no value", got)
			}
		})
	}
}

// TestBreakdownFrozenWithoutAWeek: a participant who left before any week
// locked has an empty frozen breakdown, served as frozen (not computed).
func TestBreakdownFrozenWithoutAWeek(t *testing.T) {
	c, _, _ := pointsChallenge(t)
	repo := &fakeRepo{snapshots: map[string]json.RawMessage{
		CategoryPoints: json.RawMessage(`{"entries":[{"user_id":"a","rank":0,"value":0,"abandoned":true,"weeks":[]}]}`),
	}}
	svc := NewService(&fakeChallenges{c: c}, pseudos, repo, clock.NewFake(c.EndsAt.AddDate(0, 1, 0)), 24*time.Hour)
	got, err := svc.Breakdown(t.Context(), auth.Actor{ID: "actor"}, "c1", "a")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Frozen || !got.Abandoned || len(got.Weeks) != 0 || got.Acquired != 0 || repo.calls != 0 {
		t.Fatalf("Breakdown() = %+v (%d reads), want frozen, abandoned, no week, nothing computed", got, repo.calls)
	}
}
