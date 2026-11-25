package leaderboard

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/scoring"
)

// cached is a service over the points challenge, at 10 June 2026, noon, in
// its week 2 (week 2 locks on June 15 at 7 am), with its fakes.
func cached(t *testing.T) (*Service, *fakeChallenges, *fakeRepo, *clock.FakeClock, time.Time) {
	t.Helper()
	c, activities, at := pointsChallenge(t)
	repo := &fakeRepo{participants: []scoring.Participant{{UserID: "a"}, {UserID: "b"}}, activities: activities}
	challenges := &fakeChallenges{c: c}
	now := at(10, 12)
	fake := clock.NewFake(now)
	return NewService(challenges, pseudos, repo, fake, 24*time.Hour), challenges, repo, fake, now
}

func TestReadsWithinTheCacheComputeOnce(t *testing.T) {
	svc, _, repo, fake, now := cached(t)
	actor := auth.Actor{ID: "a"}
	first, err := svc.Get(t.Context(), actor, "c1")
	if err != nil {
		t.Fatal(err)
	}
	// One read of the participants and one of the activities.
	if repo.calls != 2 {
		t.Fatalf("first read: %d reads, want 2", repo.calls)
	}

	// An activity entered just after: not shown until the ranking expires.
	repo.activities = append(repo.activities, scoring.Activity{UserID: "a", SportCode: "run", StartedAt: now, DurationSeconds: 9000, LongestSeconds: 9000, Sessions: 1})
	fake.Set(now.Add(keptFor - time.Nanosecond))
	if _, err := svc.Get(t.Context(), actor, "c1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Page(t.Context(), actor, "c1", PageRequest{Limit: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Breakdown(t.Context(), actor, "c1", "b"); err != nil {
		t.Fatal(err)
	}
	again, err := svc.Get(t.Context(), actor, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if repo.calls != 2 {
		t.Fatalf("within %s: %d reads, want the 2 of the first", keptFor, repo.calls)
	}
	if !again.ComputedAt.Equal(now) || again.Categories[0].Entries[0].Value != first.Categories[0].Entries[0].Value {
		t.Fatalf("within %s: computed at %s, %+v; want the ranking of %s", keptFor, again.ComputedAt, again.Categories[0].Entries[0], now)
	}

	fake.Set(now.Add(keptFor))
	later, err := svc.Get(t.Context(), actor, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if repo.calls != 4 {
		t.Fatalf("after %s: %d reads, want 4", keptFor, repo.calls)
	}
	if !later.ComputedAt.Equal(now.Add(keptFor)) || later.Categories[0].Entries[0].Value == first.Categories[0].Entries[0].Value {
		t.Fatalf("after %s: computed at %s, %+v; want the new activity", keptFor, later.ComputedAt, later.Categories[0].Entries[0])
	}
}

// TestBreakdownIsThatOfTheKeptRanking: a breakdown read within the cache is
// that of the leaderboard kept, at its instant.
func TestBreakdownIsThatOfTheKeptRanking(t *testing.T) {
	svc, _, repo, fake, now := cached(t)
	if _, err := svc.Get(t.Context(), auth.Actor{ID: "a"}, "c1"); err != nil {
		t.Fatal(err)
	}
	repo.activities = nil
	fake.Set(now.Add(time.Second))
	got, err := svc.Breakdown(t.Context(), auth.Actor{ID: "a"}, "c1", "b")
	if err != nil {
		t.Fatal(err)
	}
	if !got.ComputedAt.Equal(now) || got.Acquired != 8 {
		t.Fatalf("Breakdown() computed at %s with %d points, want %s and the 8 of week 1", got.ComputedAt, got.Acquired, now)
	}
}

func TestTheCacheEndsWhenAWeekLocks(t *testing.T) {
	svc, _, repo, fake, _ := cached(t)
	lock := time.Date(2026, 6, 15, 7, 0, 0, 0, toronto(t)).UTC()
	fake.Set(lock.Add(-time.Second))
	if _, err := svc.Get(t.Context(), auth.Actor{ID: "a"}, "c1"); err != nil {
		t.Fatal(err)
	}
	fake.Set(lock)
	if _, err := svc.Get(t.Context(), auth.Actor{ID: "a"}, "c1"); err != nil {
		t.Fatal(err)
	}
	if repo.calls != 4 {
		t.Fatalf("%d reads, want the ranking computed again when week 2 locks", repo.calls)
	}
}

func TestTheCacheChecksTheRightsOnEveryRead(t *testing.T) {
	svc, challenges, repo, _, _ := cached(t)
	if _, err := svc.Get(t.Context(), auth.Actor{ID: "a"}, "c1"); err != nil {
		t.Fatal(err)
	}
	// The actor may no longer see the private challenge: it is not found,
	// though its ranking is kept.
	challenges.err = apperr.New(apperr.CodeChallengeNotFound, nil)
	reads := map[string]func() error{
		"Get": func() error { _, err := svc.Get(t.Context(), auth.Actor{ID: "a"}, "c1"); return err },
		"Page": func() error {
			_, err := svc.Page(t.Context(), auth.Actor{ID: "a"}, "c1", PageRequest{Limit: 1})
			return err
		},
		"Breakdown": func() error { _, err := svc.Breakdown(t.Context(), auth.Actor{ID: "a"}, "c1", "b"); return err },
	}
	for name, read := range reads {
		var e *apperr.Error
		if err := read(); !errors.As(err, &e) || e.Status != http.StatusNotFound {
			t.Fatalf("%s: %v, want 404", name, err)
		}
	}
	if repo.calls != 2 {
		t.Fatalf("%d reads, want none after the refusal", repo.calls)
	}
}

func TestTheCacheHidesTheValuesPerReader(t *testing.T) {
	svc, challenges, _, _, _ := cached(t)
	if _, err := svc.Breakdown(t.Context(), auth.Actor{ID: "a"}, "c1", "b"); err != nil {
		t.Fatal(err)
	}
	// A reader who may not see the activities, from the same ranking.
	challenges.c.ReadsActivities = false
	got, err := svc.Breakdown(t.Context(), auth.Actor{ID: "stranger"}, "c1", "b")
	if err != nil {
		t.Fatal(err)
	}
	if !got.ValuesHidden || got.Weeks[0].Criteria[0].Value != 0 {
		t.Fatalf("Breakdown() = %+v, want the values hidden", got)
	}
	// Hiding them for one reader leaves them to the next.
	challenges.c.ReadsActivities = true
	if got, err = svc.Breakdown(t.Context(), auth.Actor{ID: "a"}, "c1", "b"); err != nil || got.Weeks[0].Criteria[0].Value != 1800 {
		t.Fatalf("Breakdown() = %+v, %v, want the value 1800 shown", got, err)
	}
}

func TestTheCacheFollowsAChangedChallenge(t *testing.T) {
	svc, challenges, repo, _, _ := cached(t)
	if _, err := svc.Get(t.Context(), auth.Actor{ID: "a"}, "c1"); err != nil {
		t.Fatal(err)
	}
	// An admin removes the points category.
	challenges.c.Categories = challenges.c.Categories[:1]
	got, err := svc.Get(t.Context(), auth.Actor{ID: "a"}, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Categories) != 1 || repo.calls != 4 {
		t.Fatalf("%d categories after %d reads, want 1 after 4", len(got.Categories), repo.calls)
	}
	// An admin moves the start.
	challenges.c.StartsAt = challenges.c.StartsAt.Add(-24 * time.Hour)
	if _, err := svc.Get(t.Context(), auth.Actor{ID: "a"}, "c1"); err != nil {
		t.Fatal(err)
	}
	if repo.calls != 6 {
		t.Fatalf("%d reads, want the ranking computed again for the new start", repo.calls)
	}
}

func TestAFailedComputationIsNotKept(t *testing.T) {
	svc, _, repo, _, _ := cached(t)
	repo.err = errors.New("db down")
	if _, err := svc.Get(t.Context(), auth.Actor{ID: "a"}, "c1"); err == nil {
		t.Fatal("Get() succeeded, want the error")
	}
	repo.err = nil
	if _, err := svc.Get(t.Context(), auth.Actor{ID: "a"}, "c1"); err != nil {
		t.Fatalf("Get() = %v, want the ranking computed again", err)
	}
}

// gatedRepo holds the reads of the participants until open is closed, and
// counts them; safe for concurrent use.
type gatedRepo struct {
	*fakeRepo
	open  chan struct{}
	mu    sync.Mutex
	reads int
}

func (g *gatedRepo) ParticipantsOf(ctx context.Context, ids []string) (map[string][]scoring.Participant, error) {
	<-g.open
	g.mu.Lock()
	defer g.mu.Unlock()
	g.reads++
	return g.fakeRepo.ParticipantsOf(ctx, ids)
}

func (g *gatedRepo) ActivitiesOf(ctx context.Context, scopes []Scope) (map[string][]scoring.Activity, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.fakeRepo.ActivitiesOf(ctx, scopes)
}

// countedChallenges is fakeChallenges counting the reads, safe for
// concurrent use.
type countedChallenges struct {
	c     Challenge
	asked sync.WaitGroup
}

func (f *countedChallenges) Challenge(context.Context, auth.Actor, string) (Challenge, error) {
	f.asked.Done()
	return f.c, nil
}

func TestConcurrentReadsComputeOnce(t *testing.T) {
	c, activities, at := pointsChallenge(t)
	repo := &gatedRepo{fakeRepo: &fakeRepo{participants: []scoring.Participant{{UserID: "a"}, {UserID: "b"}}, activities: activities}, open: make(chan struct{})}
	const readers = 20
	challenges := &countedChallenges{c: c}
	challenges.asked.Add(readers)
	svc := NewService(challenges, pseudos, repo, clock.NewFake(at(10, 12)), 24*time.Hour)

	var wg sync.WaitGroup
	errs := make(chan error, readers)
	for i := range readers {
		wg.Go(func() {
			if i%2 == 0 {
				_, err := svc.Get(t.Context(), auth.Actor{ID: "a"}, "c1")
				errs <- err
				return
			}
			_, err := svc.Breakdown(t.Context(), auth.Actor{ID: "a"}, "c1", "b")
			errs <- err
		})
	}
	// Every reader is past its rights before the first computation reads.
	challenges.asked.Wait()
	close(repo.open)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if repo.reads != 1 {
		t.Fatalf("%d computations, want 1", repo.reads)
	}
}

func TestAWaitingReadEndsWithItsRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRankings()
		c := Challenge{ID: "c1"}
		now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
		release := make(chan struct{})
		go func() {
			_, _ = r.get(context.Background(), c, now, now.Add(keptFor), func() (computed, error) {
				<-release
				return computed{}, nil
			})
		}()
		synctest.Wait()
		ctx, cancel := context.WithCancel(t.Context())
		waited := make(chan error)
		go func() {
			_, err := r.get(ctx, c, now, now.Add(keptFor), func() (computed, error) {
				t.Error("computed again during the computation")
				return computed{}, nil
			})
			waited <- err
		}()
		// The second read waits for the first, until its request ends.
		synctest.Wait()
		cancel()
		if err := <-waited; !errors.Is(err, context.Canceled) {
			t.Fatalf("get() = %v, want context.Canceled", err)
		}
		close(release)
	})
}

func TestWaitingReadsComputeAgainAfterAFailure(t *testing.T) {
	for name, fail := range map[string]func() (computed, error){
		"error": func() (computed, error) { return computed{}, context.Canceled },
		"panic": func() (computed, error) { panic("boom") },
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := newRankings()
				c := Challenge{ID: "c1"}
				now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
				release := make(chan struct{})
				go func() {
					// A panic of the computation goes on in its own read.
					defer func() { _ = recover() }()
					_, _ = r.get(context.Background(), c, now, now.Add(keptFor), func() (computed, error) {
						<-release
						return fail()
					})
				}()
				synctest.Wait()
				var computedAgain bool
				waited := make(chan error)
				go func() {
					_, err := r.get(t.Context(), c, now, now.Add(keptFor), func() (computed, error) {
						computedAgain = true
						return computed{}, nil
					})
					waited <- err
				}()
				// The second read is waiting for the first when it fails.
				synctest.Wait()
				close(release)
				if err := <-waited; err != nil || !computedAgain {
					t.Fatalf("get() = %v, computed again %t; want the ranking computed again", err, computedAgain)
				}
				if e := r.entries["c1"]; e == nil || e.err != nil {
					t.Fatal("the ranking computed again is not kept")
				}
			})
		})
	}
}

func TestTheCacheIsBounded(t *testing.T) {
	r := newRankings()
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	ok := func() (computed, error) { return computed{}, nil }
	for i := range keptChallenges + 1 {
		if _, err := r.get(t.Context(), Challenge{ID: strconv.Itoa(i)}, now.Add(time.Duration(i)*time.Millisecond), now.Add(time.Duration(i)*time.Millisecond).Add(keptFor), ok); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.entries) != keptChallenges {
		t.Fatalf("%d rankings kept, want %d", len(r.entries), keptChallenges)
	}
	if _, kept := r.entries["0"]; kept {
		t.Fatal("the oldest ranking is still kept")
	}
	// Any read lets the expired ones go.
	if _, err := r.get(t.Context(), Challenge{ID: "1"}, now.Add(keptFor+time.Second), now.Add(keptFor+time.Second).Add(keptFor), ok); err != nil {
		t.Fatal(err)
	}
	if len(r.entries) != 1 {
		t.Fatalf("%d rankings kept, want the one read alone", len(r.entries))
	}
}

func TestARankingBeingComputedIsNotLetGo(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRankings()
		now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
		release := make(chan struct{})
		go func() {
			_, _ = r.get(context.Background(), Challenge{ID: "slow"}, now, now.Add(keptFor), func() (computed, error) {
				<-release
				return computed{}, nil
			})
		}()
		synctest.Wait()
		ok := func() (computed, error) { return computed{}, nil }
		for i := range keptChallenges {
			if _, err := r.get(t.Context(), Challenge{ID: strconv.Itoa(i)}, now.Add(time.Millisecond), now.Add(time.Millisecond).Add(keptFor), ok); err != nil {
				t.Fatal(err)
			}
		}
		if _, kept := r.entries["slow"]; !kept {
			t.Fatal("the oldest ranking, being computed, was let go")
		}
		close(release)
	})
}

// TestARankingBeingComputedEndsWhenAWeekLocks: a read at the lock does not
// join the computation started before it, which ranks the week unjudged.
func TestARankingBeingComputedEndsWhenAWeekLocks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, activities, _ := pointsChallenge(t)
		lock := time.Date(2026, 6, 15, 7, 0, 0, 0, toronto(t)).UTC()
		repo := &gatedRepo{fakeRepo: &fakeRepo{participants: []scoring.Participant{{UserID: "a"}, {UserID: "b"}}, activities: activities}, open: make(chan struct{})}
		challenges := &countedChallenges{c: c}
		challenges.asked.Add(2)
		fake := clock.NewFake(lock.Add(-time.Second))
		svc := NewService(challenges, pseudos, repo, fake, 24*time.Hour)
		first := make(chan struct{})
		go func() {
			defer close(first)
			_, _ = svc.Get(context.Background(), auth.Actor{ID: "a"}, "c1")
		}()
		synctest.Wait()
		fake.Set(lock)
		got := make(chan Leaderboard)
		go func() {
			board, err := svc.Get(t.Context(), auth.Actor{ID: "a"}, "c1")
			if err != nil {
				t.Error(err)
			}
			got <- board
		}()
		synctest.Wait()
		close(repo.open)
		if board := <-got; !board.ComputedAt.Equal(lock) {
			t.Fatalf("read at the lock: computed at %s, want %s", board.ComputedAt, lock)
		}
		<-first
		if repo.reads != 2 {
			t.Fatalf("%d computations, want 2", repo.reads)
		}
	})
}
