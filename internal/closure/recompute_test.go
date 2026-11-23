package closure

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/scoring"
)

func TestRecompute(t *testing.T) {
	// 4 weeks across the time change of March 8.
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 3, 2, 0, 0, 0, 0, loc)
	c := EndedChallenge{
		ID: "c1", Timezone: "America/Toronto", StartsAt: start.UTC(), EndsAt: start.AddDate(0, 0, 28).UTC(),
		Categories: []Category{
			{ID: "cat1", Type: categoryCumulativeTime},
			{ID: "cat3", Type: categoryElimination, Sport: "run", StepMeters: 2000},
		},
	}
	w := scoring.Window{Start: c.StartsAt, End: c.EndsAt, Location: loc}
	locked := w.WeekLocksAt
	runs := func(user string, distances ...int) []scoring.Activity {
		var as []scoring.Activity
		for i, d := range distances {
			m := d
			s, _ := w.WeekBounds(i + 1)
			as = append(as, scoring.Activity{UserID: user, SportCode: "run", StartedAt: s.Add(time.Hour), DurationSeconds: 1800, DistanceMeters: &m})
		}
		return as
	}
	participants := []scoring.Participant{{UserID: "a"}, {UserID: "b"}}
	// a: eliminated in week 2; b: eliminated in week 4, the last one.
	activities := append(runs("a", 5000, 6999, 9000, 11000), runs("b", 5000, 7000, 9000, 10999)...)
	week2 := scoring.Eliminated{UserID: "a", Week: 2, DistanceMeters: 6999, MinimumMeters: 7000}
	week4 := scoring.Eliminated{UserID: "b", Week: 4, DistanceMeters: 10999, MinimumMeters: 11000}

	// stale fills repo with the results of a former judgement, which the
	// recompute erases.
	stale := func(repo *fakeRepo) {
		repo.closed["c1"] = []int{1, 2, 3}
		repo.recorded["c1"] = []scoring.Eliminated{{UserID: "b", Week: 2}}
		repo.snapshots["c1"] = []Snapshot{{CategoryID: "old"}}
	}
	recompute := func(t *testing.T, repo *fakeRepo, at time.Time) error {
		t.Helper()
		return NewService(repo, &fakeData{participants: participants, activities: activities}, clock.NewFake(at), discard()).Recompute(t.Context(), "c1")
	}

	t.Run("a challenge in progress: its locked weeks judged again", func(t *testing.T) {
		repo := newFakeRepo()
		repo.ended = []EndedChallenge{c}
		stale(repo)
		if err := recompute(t, repo, locked(2)); err != nil {
			t.Fatal(err)
		}
		if want := []string{"lock", "clear c1"}; !reflect.DeepEqual(repo.steps, want) {
			t.Fatalf("steps = %v, want the week lock first, then the results cleared", repo.steps)
		}
		if !reflect.DeepEqual(repo.closed["c1"], []int{1, 2}) {
			t.Fatalf("closed weeks = %v, want [1 2]", repo.closed["c1"])
		}
		if !reflect.DeepEqual(repo.recorded["c1"], []scoring.Eliminated{week2}) {
			t.Fatalf("eliminations = %+v, want %+v", repo.recorded["c1"], week2)
		}
		if at := repo.closedAt["c1/2"]; !at.Equal(locked(2)) {
			t.Fatalf("closed at %v, want the instant of the clock", at)
		}
		if _, ok := repo.snapshots["c1"]; ok {
			t.Fatalf("snapshots = %+v, want none: the challenge is not over", repo.snapshots["c1"])
		}
	})

	t.Run("a challenge whose last week is locked: its results frozen again", func(t *testing.T) {
		repo := newFakeRepo()
		repo.ended = []EndedChallenge{c}
		stale(repo)
		if err := recompute(t, repo, locked(4)); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(repo.closed["c1"], []int{1, 2, 3}) {
			t.Fatalf("closed weeks = %v, want [1 2 3], the last one left to the snapshots", repo.closed["c1"])
		}
		if got := repo.snapshots["c1"]; len(got) != 2 || got[0].CategoryID != "cat1" || got[1].CategoryID != "cat3" {
			t.Fatalf("snapshots = %+v, want one per category", got)
		}
		if !repo.snapshotAt.Equal(locked(4)) {
			t.Fatalf("snapshots at %v, want the instant of the clock", repo.snapshotAt)
		}
		if want := (LastWeek{Number: 4, CategoryID: "cat3", Eliminated: []scoring.Eliminated{week4}}); !reflect.DeepEqual(repo.last, want) {
			t.Fatalf("last week = %+v, want %+v", repo.last, want)
		}
	})

	t.Run("a challenge over whose last week is not locked yet: no snapshot", func(t *testing.T) {
		repo := newFakeRepo()
		repo.ended = []EndedChallenge{c}
		stale(repo)
		if err := recompute(t, repo, locked(4).Add(-time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, ok := repo.snapshots["c1"]; ok || !reflect.DeepEqual(repo.closed["c1"], []int{1, 2, 3}) {
			t.Fatalf("snapshots %+v, closed %v, want no snapshot and weeks 1 to 3", repo.snapshots["c1"], repo.closed["c1"])
		}
	})

	t.Run("a challenge not started: everything cleared, nothing judged", func(t *testing.T) {
		repo := newFakeRepo()
		repo.ended = []EndedChallenge{c}
		stale(repo)
		if err := recompute(t, repo, c.StartsAt.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		if len(repo.closed["c1"]) != 0 || len(repo.recorded["c1"]) != 0 || len(repo.snapshots["c1"]) != 0 {
			t.Fatalf("closed %v, eliminations %v, snapshots %v, want nothing", repo.closed["c1"], repo.recorded["c1"], repo.snapshots["c1"])
		}
	})

	t.Run("a challenge without elimination: no week closed before the last one", func(t *testing.T) {
		repo := newFakeRepo()
		repo.ended = []EndedChallenge{{ID: "c1", Timezone: c.Timezone, StartsAt: c.StartsAt, EndsAt: c.EndsAt, Categories: c.Categories[:1]}}
		if err := recompute(t, repo, locked(4)); err != nil {
			t.Fatal(err)
		}
		if len(repo.closed["c1"]) != 0 || len(repo.snapshots["c1"]) != 1 || repo.last.Number != 4 {
			t.Fatalf("closed %v, snapshots %+v, last %+v, want only the snapshots and the last week", repo.closed["c1"], repo.snapshots["c1"], repo.last)
		}
	})

	t.Run("an unknown or deleted challenge", func(t *testing.T) {
		repo := newFakeRepo()
		err := recompute(t, repo, locked(2))
		if !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
			t.Fatalf("got %v, want challenge_not_found", err)
		}
		if slices.Contains(repo.steps, "clear c1") {
			t.Fatal("results cleared for a challenge not found")
		}
	})

	t.Run("the week lock refused: nothing done", func(t *testing.T) {
		repo := newFakeRepo()
		repo.ended = []EndedChallenge{c}
		stale(repo)
		repo.lockErr = errors.New("outside a transaction")
		if err := recompute(t, repo, locked(2)); !errors.Is(err, repo.lockErr) {
			t.Fatalf("got %v, want %v", err, repo.lockErr)
		}
		if !reflect.DeepEqual(repo.closed["c1"], []int{1, 2, 3}) {
			t.Fatalf("closed weeks = %v, want them untouched", repo.closed["c1"])
		}
	})

	t.Run("an account ranked deleted since: ranked again", func(t *testing.T) {
		repo := newFakeRepo()
		repo.ended = []EndedChallenge{c}
		repo.deletedSince = 1
		if err := recompute(t, repo, locked(4)); err != nil {
			t.Fatal(err)
		}
		if len(repo.snapshots["c1"]) != 2 {
			t.Fatalf("snapshots = %+v, want them saved on the second try", repo.snapshots["c1"])
		}
	})
}

// TestWorkerRunsEachChallengeUnderTheSharedWeekLock: each challenge a run
// handles is read again and judged in a transaction holding the week lock
// shared, so that no recompute (which takes it alone) runs in between.
func TestWorkerRunsEachChallengeUnderTheSharedWeekLock(t *testing.T) {
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 3, 2, 0, 0, 0, 0, loc)
	c := Challenge{ID: "c1", Timezone: "America/Toronto", StartsAt: start.UTC(), EndsAt: start.AddDate(0, 0, 28).UTC(), CategoryID: "cat3", Sport: "run", StepMeters: 2000}
	w := scoring.Window{Start: c.StartsAt, End: c.EndsAt, Location: loc}

	t.Run("close-weeks", func(t *testing.T) {
		repo := newFakeRepo(c)
		if _, err := NewService(repo, &fakeData{}, clock.NewFake(w.WeekLocksAt(2)), discard()).CloseWeeks(t.Context()); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(repo.steps, []string{"shared"}) || len(repo.closed["c1"]) != 2 {
			t.Fatalf("steps %v, closed %v, want weeks 1 and 2 closed under the shared lock", repo.steps, repo.closed["c1"])
		}
	})

	t.Run("close-weeks of a challenge deleted since the listing", func(t *testing.T) {
		repo := newFakeRepo()
		repo.challenges = nil
		listed := listedOnly{fakeRepo: repo, listed: []Challenge{c}}
		got, err := NewService(listed, &fakeData{}, clock.NewFake(w.WeekLocksAt(2)), discard()).CloseWeeks(t.Context())
		if err != nil || got != (Result{}) || len(repo.closed["c1"]) != 0 {
			t.Fatalf("result %+v, err %v, closed %v, want nothing closed", got, err, repo.closed["c1"])
		}
	})

	t.Run("finalize-challenges", func(t *testing.T) {
		repo := newFakeRepo()
		repo.ended = []EndedChallenge{{ID: "c1", Timezone: c.Timezone, StartsAt: c.StartsAt, EndsAt: c.EndsAt, Categories: []Category{{ID: "cat1", Type: categoryCumulativeTime}}}}
		if _, err := NewService(repo, &fakeData{}, clock.NewFake(w.WeekLocksAt(4)), discard()).FinalizeChallenges(t.Context()); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(repo.steps, []string{"shared"}) || len(repo.snapshots["c1"]) != 1 {
			t.Fatalf("steps %v, snapshots %v, want the snapshots saved under the shared lock", repo.steps, repo.snapshots["c1"])
		}
	})

	t.Run("finalize-challenges of a challenge extended since the listing", func(t *testing.T) {
		repo := newFakeRepo()
		extended := EndedChallenge{ID: "c1", Timezone: c.Timezone, StartsAt: c.StartsAt, EndsAt: c.EndsAt.AddDate(0, 0, 7), Categories: []Category{{ID: "cat1", Type: categoryCumulativeTime}}}
		repo.ended = []EndedChallenge{extended}
		listed := listedOnly{fakeRepo: repo, ended: []EndedChallenge{{ID: "c1", Timezone: c.Timezone, StartsAt: c.StartsAt, EndsAt: c.EndsAt, Categories: extended.Categories}}}
		got, err := NewService(listed, &fakeData{}, clock.NewFake(w.WeekLocksAt(4)), discard()).FinalizeChallenges(t.Context())
		if err != nil || got != (FinalizeResult{}) || len(repo.snapshots["c1"]) != 0 {
			t.Fatalf("result %+v, err %v, snapshots %v, want nothing frozen: the challenge is no longer over", got, err, repo.snapshots["c1"])
		}
	})
}

// listedOnly lists challenges as they were before a change committed since:
// read again under the lock, they are those of fakeRepo.
type listedOnly struct {
	*fakeRepo
	listed []Challenge
	ended  []EndedChallenge
}

func (l listedOnly) ClosableChallenges(context.Context, time.Time) ([]Challenge, error) {
	return l.listed, nil
}

func (l listedOnly) EndedChallenges(context.Context, time.Time) ([]EndedChallenge, error) {
	return l.ended, nil
}
