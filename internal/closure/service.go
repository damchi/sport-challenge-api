// Package closure runs the work done when a week or a challenge ends: it
// judges the weeks that ended in the run elimination category and records
// the eliminations, and it freezes the final ranking of every category when
// the challenge is over, once. Rankings never read the eliminations (they are
// computed from the raw activities); only a result snapshot is the stored
// result of a finished challenge.
package closure

import (
	"context"
	"encoding/json"
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
	// EndedChallenges lists the challenges not deleted that are over at now
	// and have no result snapshot.
	EndedChallenges(ctx context.Context, now time.Time) ([]EndedChallenge, error)
	// SaveSnapshots records the snapshots of a challenge and the closure of
	// its last week in one transaction. It reports false, and records
	// nothing, when they were already taken (by a run at the same time).
	SaveSnapshots(ctx context.Context, challengeID string, createdAt time.Time, snapshots []Snapshot, last LastWeek) (bool, error)
}

// Category types, as the challenge module stores them (categories.type).
const (
	categoryCumulativeTime     = "cumulative_time"
	categoryCumulativeDistance = "cumulative_distance"
	categoryRunElimination     = "run_elimination"
)

// Activities gives the participants of a challenge and, in one query, the
// totals of the activities of its active participants started in the window,
// per participant and week for weeklySport, per participant and sport for the
// others (module leaderboard, which ranks from the same data).
type Activities interface {
	Participants(ctx context.Context, challengeID string) ([]scoring.Participant, error)
	Activities(ctx context.Context, challengeID string, w scoring.Window, weeklySport string) ([]scoring.Activity, error)
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

// CloseWeeks closes every week that is locked (7 am, local time, on the
// first day of the next week: scoring.Window.WeekLocksAt), in every
// challenge, and has no closure yet: the eliminations of the week are those
// of the scorer, recorded with the closure in one transaction. A week that ended but is not
// locked yet is left for a later run. It can be run again and again: a week
// is judged by its activities, which no longer change once it is locked, so
// when it runs does not change them, even after the challenge is over.
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

// closeChallenge closes the weeks of one challenge that are locked at now and
// not closed, oldest first, and counts the weeks and eliminations recorded.
func (s *Service) closeChallenge(ctx context.Context, c Challenge, now time.Time) (weeks, eliminations int, err error) {
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return 0, 0, fmt.Errorf("challenge time zone %q: %w", c.Timezone, err)
	}
	w := scoring.Window{Start: c.StartsAt, End: c.EndsAt, Location: loc, Now: now}
	// The last week locks after the challenge: its finalization judges it.
	locked := min(w.LockedWeeks(), w.Weeks()-1)
	closed, err := s.repo.ClosedWeeks(ctx, c.ID)
	if err != nil {
		return 0, 0, err
	}
	todo := openWeeks(locked, closed)
	if len(todo) == 0 {
		return 0, 0, nil
	}

	// Who is abandoned is read as of now: someone who abandons before the
	// week is closed is shown as abandoned, not eliminated, and gets no row.
	participants, err := s.data.Participants(ctx, c.ID)
	if err != nil {
		return 0, 0, err
	}
	activities, err := s.data.Activities(ctx, c.ID, w, c.Sport)
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

// openWeeks lists, in order, the weeks 1 to locked that are not in closed.
func openWeeks(locked int, closed []int) []int {
	isClosed := make(map[int]bool, len(closed))
	for _, n := range closed {
		isClosed[n] = true
	}
	var todo []int
	for n := 1; n <= locked; n++ {
		if !isClosed[n] {
			todo = append(todo, n)
		}
	}
	return todo
}

// FinalizeChallenges freezes the results of every challenge whose last week
// is locked (7 am, local time, on the first day after the challenge) and has
// none: until then, an activity of the last week can still be entered. The final ranking
// of each category, computed from the raw activities by the scorer of the
// category, is recorded in one transaction per challenge, with the closure
// of its last week and the eliminations judged in it (as close-weeks records
// the other weeks). It can be run again and again: a challenge with its
// snapshots is not listed, and a run at the same time records nothing. Who
// is abandoned is read as of the run, which changes nothing after the end:
// nobody can abandon or be excluded from a challenge that is over, and the
// activities of a locked week no longer change. Challenges deleted get no
// snapshot. A challenge in error does not stop the others; the errors are
// joined.
func (s *Service) FinalizeChallenges(ctx context.Context) (FinalizeResult, error) {
	now := s.clock.Now()
	challenges, err := s.repo.EndedChallenges(ctx, now)
	if err != nil {
		return FinalizeResult{}, err
	}
	var result FinalizeResult
	var errs []error
	for _, c := range challenges {
		done, err := s.finalizeChallenge(ctx, c, now)
		if done {
			result.Challenges++
		}
		if err != nil {
			s.logger.Error("finalize challenge", slog.String("challenge_id", c.ID), slog.String("error", err.Error()))
			errs = append(errs, fmt.Errorf("challenge %s: %w", c.ID, err))
		}
	}
	return result, errors.Join(errs...)
}

// finalizeChallenge records the snapshots of one challenge once its last
// week is locked, and reports whether this run did.
func (s *Service) finalizeChallenge(ctx context.Context, c EndedChallenge, now time.Time) (bool, error) {
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return false, fmt.Errorf("challenge time zone %q: %w", c.Timezone, err)
	}
	w := scoring.Window{Start: c.StartsAt, End: c.EndsAt, Location: loc, Now: now}
	last := LastWeek{Number: w.Weeks()}
	if now.Before(w.WeekLocksAt(last.Number)) {
		return false, nil
	}
	participants, err := s.data.Participants(ctx, c.ID)
	if err != nil {
		return false, err
	}
	activities, err := s.data.Activities(ctx, c.ID, w, eliminationSport(c.Categories))
	if err != nil {
		return false, err
	}
	snapshots := make([]Snapshot, len(c.Categories))
	for i, cat := range c.Categories {
		scorer, err := scorerOf(cat)
		if err != nil {
			return false, err
		}
		if cat.Type == categoryRunElimination {
			last.CategoryID = cat.ID
			e := scoring.Elimination{Sport: cat.Sport, StepMeters: cat.StepMeters}
			for _, out := range e.Eliminations(w, participants, activities) {
				if out.Week == last.Number {
					last.Eliminated = append(last.Eliminated, out)
				}
			}
		}
		payload, err := json.Marshal(newPayload(scorer.Score(w, participants, activities)))
		if err != nil {
			return false, fmt.Errorf("encode ranking of category %s: %w", cat.ID, err)
		}
		snapshots[i] = Snapshot{CategoryID: cat.ID, Type: cat.Type, Payload: payload}
	}
	done, err := s.repo.SaveSnapshots(ctx, c.ID, now, snapshots, last)
	if err != nil {
		return false, fmt.Errorf("save snapshots: %w", err)
	}
	return done, nil
}

// eliminationSport is the sport of the run elimination category, the only one
// counted week by week; empty without such a category.
func eliminationSport(categories []Category) string {
	for _, cat := range categories {
		if cat.Type == categoryRunElimination {
			return cat.Sport
		}
	}
	return ""
}

// scorerOf returns the scorer of a category, from the type and the settings
// frozen at the creation of the challenge (as the leaderboard does).
func scorerOf(cat Category) (scoring.Scorer, error) {
	switch cat.Type {
	case categoryCumulativeTime:
		return scoring.Duration{}, nil
	case categoryCumulativeDistance:
		return scoring.Distance{Factors: cat.Factors}, nil
	case categoryRunElimination:
		return scoring.Elimination{Sport: cat.Sport, StepMeters: cat.StepMeters}, nil
	default:
		return nil, fmt.Errorf("unknown category type %q", cat.Type)
	}
}

// payload is the stored ranking of a category. It names participants by id,
// never by pseudo; an account deleted since is left out when it is served.
type payload struct {
	Entries []payloadEntry `json:"entries"`
}

type payloadEntry struct {
	UserID    string `json:"user_id"`
	Rank      int    `json:"rank"`
	Value     int    `json:"value"`
	Abandoned bool   `json:"abandoned"`
	// EliminatedWeek is the week of the elimination, in the run elimination
	// category; absent for a survivor.
	EliminatedWeek int `json:"eliminated_week,omitempty"`
}

func newPayload(entries []scoring.Entry) payload {
	p := payload{Entries: make([]payloadEntry, len(entries))}
	for i, e := range entries {
		p.Entries[i] = payloadEntry{UserID: e.UserID, Rank: e.Rank, Value: e.Value, Abandoned: e.Abandoned}
		if e.Progress != nil && e.Progress.Status == scoring.StatusEliminated {
			p.Entries[i].EliminatedWeek = e.Progress.EliminatedWeek
		}
	}
	return p
}
