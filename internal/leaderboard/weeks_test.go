package leaderboard

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/scoring"
)

func TestWeeks(t *testing.T) {
	c, activities, at := pointsChallenge(t)
	participants := []scoring.Participant{{UserID: "a"}, {UserID: "b"}}
	tests := []struct {
		name string
		c    Challenge
		now  time.Time
		want Weeks
	}{
		{
			name: "week 2 in progress, week 1 locked",
			c:    c, now: at(10, 12),
			want: Weeks{Current: 2, Locked: 1, Points: map[string]map[int]int{"a": {1: 10, 2: 8}, "b": {1: 8, 2: 10}}},
		},
		{
			name: "over and locked: computed, not frozen",
			c:    c, now: c.EndsAt.AddDate(0, 1, 0),
			want: Weeks{Current: 0, Locked: 2, Points: map[string]map[int]int{"a": {1: 10, 2: 8}, "b": {1: 8, 2: 10}}},
		},
		{
			name: "no points category",
			c:    Challenge{ID: c.ID, Timezone: c.Timezone, StartsAt: c.StartsAt, EndsAt: c.EndsAt, Categories: []Category{{Type: CategoryCumulativeTime}}},
			now:  at(10, 12),
			want: Weeks{Current: 2, Locked: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{participants: participants, activities: activities}
			svc := NewService(&fakeChallenges{c: tt.c}, pseudos, repo, clock.NewFake(tt.now), 24*time.Hour)
			got, err := svc.Weeks(t.Context(), auth.Actor{ID: "actor"}, "c1")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Weeks() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestWeeksOfAParticipantWhoLeft(t *testing.T) {
	c, activities, at := pointsChallenge(t)
	// b left during week 2: kept in week 1, locked by then.
	left := at(9, 12)
	participants := []scoring.Participant{{UserID: "a"}, {UserID: "b", Abandoned: true, AbandonedAt: &left}}
	repo := &fakeRepo{participants: participants, activities: activities}
	svc := NewService(&fakeChallenges{c: c}, pseudos, repo, clock.NewFake(at(10, 12)), 24*time.Hour)
	got, err := svc.Weeks(t.Context(), auth.Actor{ID: "actor"}, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]map[int]int{"a": {1: 10, 2: 10}, "b": {1: 8}}; !reflect.DeepEqual(got.Points, want) {
		t.Fatalf("points %v, want %v", got.Points, want)
	}
}

func TestWeeksRefusals(t *testing.T) {
	c, _, at := pointsChallenge(t)
	boom := errors.New("boom")
	for _, tt := range []struct {
		name       string
		challenges *fakeChallenges
		repo       *fakeRepo
	}{
		{name: "challenge not read", challenges: &fakeChallenges{err: boom}, repo: &fakeRepo{}},
		{name: "rows not read", challenges: &fakeChallenges{c: c}, repo: &fakeRepo{err: boom}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(tt.challenges, pseudos, tt.repo, clock.NewFake(at(10, 12)), 24*time.Hour)
			if _, err := svc.Weeks(t.Context(), auth.Actor{ID: "actor"}, "c1"); !errors.Is(err, boom) {
				t.Fatalf("Weeks() error = %v, want boom", err)
			}
		})
	}
}

func TestWeeksWithoutPointsReadsNoActivity(t *testing.T) {
	c, _, at := pointsChallenge(t)
	c.Categories = []Category{{Type: CategoryCumulativeTime}}
	repo := &fakeRepo{}
	svc := NewService(&fakeChallenges{c: c}, pseudos, repo, clock.NewFake(at(10, 12)), 24*time.Hour)
	if _, err := svc.Weeks(t.Context(), auth.Actor{ID: "actor"}, "c1"); err != nil || repo.calls != 0 {
		t.Fatalf("Weeks() error %v after %d reads, want none", err, repo.calls)
	}
}

// TestWeeksIsHiddenFromWhoCannotReadTheChallenge: the rule is the challenge
// module's, asked for the actor first; refused, nothing else is read.
func TestWeeksIsHiddenFromWhoCannotReadTheChallenge(t *testing.T) {
	_, _, at := pointsChallenge(t)
	challenges := &fakeChallenges{err: apperr.New(apperr.CodeNotFound, nil)}
	repo := &fakeRepo{}
	svc := NewService(challenges, pseudos, repo, clock.NewFake(at(10, 12)), 24*time.Hour)
	_, err := svc.Weeks(t.Context(), auth.Actor{ID: "stranger"}, "c1")
	if !errors.Is(err, apperr.New(apperr.CodeNotFound, nil)) || repo.calls != 0 {
		t.Fatalf("Weeks() error %v after %d reads, want not_found and nothing read", err, repo.calls)
	}
	if challenges.actor.ID != "stranger" || challenges.asked != "c1" {
		t.Fatalf("asked for %+v on %q, want the actor on the challenge", challenges.actor, challenges.asked)
	}
}
