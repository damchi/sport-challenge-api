// Package closure runs the work done when a week ends: it judges the weeks
// that ended in the run elimination category and records the eliminations,
// once. Rankings never read what it records (they are computed from the raw
// activities); it is the trace of the judgements.
package closure

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/scoring"
)

// repository is what the service needs from the storage.
type repository interface {
	// ClosableChallenges lists the challenges not deleted and started at now
	// that still have a week to close before the last one.
	ClosableChallenges(ctx context.Context, now time.Time) ([]Challenge, error)
	// ClosedWeeks gives the numbers of the weeks of the challenge already closed.
	ClosedWeeks(ctx context.Context, challengeID string) ([]int, error)
	// CloseWeek records, in one transaction, the closure of a week and the
	// eliminations of that week. It reports false, and records nothing, when
	// the week was already closed (by a run at the same time).
	CloseWeek(ctx context.Context, c Challenge, week int, closedAt time.Time, eliminated []scoring.Eliminated) (bool, error)
}

// Activities gives the participants of a challenge and, in one query, the
// activities of its active participants started in [from, to) (module
// leaderboard, which ranks from the same data).
type Activities interface {
	Participants(ctx context.Context, challengeID string) ([]scoring.Participant, error)
	Activities(ctx context.Context, challengeID string, from, to time.Time) ([]scoring.Activity, error)
}

// Service holds the use cases of the closure.
type Service struct {
	repo   repository
	data   Activities
	clock  clock.Clock
	logger *slog.Logger
}

// NewService returns a Service.
func NewService(repo repository, data Activities, c clock.Clock, logger *slog.Logger) *Service {
	return &Service{repo: repo, data: data, clock: c, logger: logger}
}

// CloseWeeks closes every week that has ended, in every challenge, and
// has no closure yet: the eliminations of the week are those of the scorer,
// recorded with the closure in one transaction. It can be run again and
// again: a week is judged by its activities, and a week that ended is locked,
// so when it runs does not change them, even after the challenge is over.
// The last week of a challenge is left to its finalization. One thing is read
// as of the run: who is abandoned (see closeChallenge). A challenge in error
// does not stop the others; the errors are joined.
func (s *Service) CloseWeeks(ctx context.Context) (Result, error) {
	now := s.clock.Now()
	challenges, err := s.repo.ClosableChallenges(ctx, now)
	if err != nil {
		return Result{}, err
	}
	var result Result
	var errs []error
	for _, c := range challenges {
		weeks, eliminations, err := s.closeChallenge(ctx, c, now)
		result.Weeks += weeks
		result.Eliminations += eliminations
		if weeks > 0 {
			result.Challenges++
		}
		if err != nil {
			s.logger.Error("close weeks", slog.String("challenge_id", c.ID), slog.String("error", err.Error()))
			errs = append(errs, fmt.Errorf("challenge %s: %w", c.ID, err))
		}
	}
	return result, errors.Join(errs...)
}

// closeChallenge closes the weeks of one challenge that ended at now and are
// not closed, oldest first, and counts the weeks and eliminations recorded.
func (s *Service) closeChallenge(ctx context.Context, c Challenge, now time.Time) (weeks, eliminations int, err error) {
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return 0, 0, fmt.Errorf("challenge time zone %q: %w", c.Timezone, err)
	}
	w := scoring.Window{Start: c.StartsAt, End: c.EndsAt, Location: loc, Now: now}
	// The last week ends with the challenge: its finalization judges it.
	ended := min(w.EndedWeeks(), w.Weeks()-1)
	closed, err := s.repo.ClosedWeeks(ctx, c.ID)
	if err != nil {
		return 0, 0, err
	}
	todo := openWeeks(ended, closed)
	if len(todo) == 0 {
		return 0, 0, nil
	}

	// Who is abandoned is read as of now: someone who abandons before the
	// week is closed is shown as abandoned, not eliminated, and gets no row.
	participants, err := s.data.Participants(ctx, c.ID)
	if err != nil {
		return 0, 0, err
	}
	activities, err := s.data.Activities(ctx, c.ID, c.StartsAt, c.EndsAt)
	if err != nil {
		return 0, 0, err
	}
	scorer := scoring.Elimination{Sport: c.Sport, StepMeters: c.StepMeters}
	eliminated := scorer.Eliminations(w, participants, activities)

	for _, week := range todo {
		var inWeek []scoring.Eliminated
		for _, e := range eliminated {
			if e.Week == week {
				inWeek = append(inWeek, e)
			}
		}
		done, err := s.repo.CloseWeek(ctx, c, week, now, inWeek)
		if err != nil {
			return weeks, eliminations, fmt.Errorf("close week %d: %w", week, err)
		}
		if done {
			weeks++
			eliminations += len(inWeek)
		}
	}
	return weeks, eliminations, nil
}

// openWeeks lists, in order, the weeks 1 to ended that are not in closed.
func openWeeks(ended int, closed []int) []int {
	isClosed := make(map[int]bool, len(closed))
	for _, n := range closed {
		isClosed[n] = true
	}
	var todo []int
	for n := 1; n <= ended; n++ {
		if !isClosed[n] {
			todo = append(todo, n)
		}
	}
	return todo
}
