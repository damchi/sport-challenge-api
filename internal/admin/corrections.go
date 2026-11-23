package admin

import (
	"context"
	"errors"
	"fmt"

	"github.com/damchi/sport-challenge-api/internal/leaderboard"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
)

// Recomputer judges a challenge again after an admin change, in the
// transaction ctx carries (module closure).
type Recomputer interface {
	Recompute(ctx context.Context, challengeID string) error
}

// Boards gives the rankings of a challenge as actor reads them, in the
// transaction ctx carries (module leaderboard).
type Boards interface {
	Get(ctx context.Context, actor auth.Actor, id string) (leaderboard.Leaderboard, error)
}

// correctionStore runs the transaction of an admin change and reads the
// eliminations it may change.
type correctionStore interface {
	// InLockedTx runs fn in a transaction, carried by the ctx it gets, that
	// takes the week lock alone first; rolled back when fn fails.
	InLockedTx(ctx context.Context, fn func(ctx context.Context) error) error
	// Eliminations reads the eliminations of a challenge, earliest week
	// first, in the transaction ctx carries.
	Eliminations(ctx context.Context, challengeID string) ([]EliminationRow, error)
}

// Corrections runs the admin changes that touch the past of challenges:
// each one with its journal line, the recompute of every challenge it
// touches, and its preview (README §2.12).
type Corrections struct {
	store     correctionStore
	journal   *Journal
	recompute Recomputer
	boards    Boards
}

// NewCorrections returns Corrections.
func NewCorrections(store correctionStore, journal *Journal, recompute Recomputer, boards Boards) *Corrections {
	return &Corrections{store: store, journal: journal, recompute: recompute, boards: boards}
}

// Correction is an admin change, run by Corrections.Run.
type Correction struct {
	// Touched reads, in the transaction and before the change, every
	// challenge it touches: those recomputed and previewed. A change that
	// moves a target into other challenges (an activity whose date changes)
	// gives those it counts in before and after it.
	Touched func(ctx context.Context) ([]string, error)
	// Apply makes the change in the transaction and returns its journal
	// entry; its admin, challenges and reason are set by Run.
	Apply func(ctx context.Context) (Entry, error)
	// Reason is the reason of the change, that of its journal entry.
	Reason string
	// DryRun previews the change: everything is done, then rolled back.
	DryRun bool
}

// ChallengePreview is what a change does to a challenge: its rankings
// before and after, and the eliminations that change.
type ChallengePreview struct {
	ChallengeID  string
	Before       leaderboard.Leaderboard
	After        leaderboard.Leaderboard
	Eliminations []EliminationChange
}

// EliminationChange is a participant whose elimination the change gives,
// removes or moves: Before and After are nil when not eliminated.
type EliminationChange struct {
	User   UserRef
	Before *EliminationRow
	After  *EliminationRow
}

// errDryRun rolls back the transaction of a preview.
var errDryRun = errors.New("dry run")

// Run makes change c as actor, in one transaction that takes the week lock
// alone first (so that no activity write and no run of the worker is in
// progress, and in the order of the activity writes: the lock, then the
// rows): it reads the rankings and eliminations of each challenge touched,
// applies the change, writes its journal line, recomputes each challenge
// touched, then reads them again. With c.DryRun, the transaction is rolled
// back: nothing is written, the journal line included, and the previews
// tell what the change would do; a change with effects outside the
// database (Auth0, e-mail) leaves them out when c.DryRun. Admin only (RequireAdmin), checked before
// anything is read; a reason out of its bounds is then refused (400
// validation_failed on reason) before the transaction opens.
func (s *Corrections) Run(ctx context.Context, actor auth.Actor, c Correction) ([]ChallengePreview, error) {
	if err := RequireAdmin(actor); err != nil {
		return nil, err
	}
	if c.Touched == nil || c.Apply == nil {
		return nil, errors.New("correction without its challenges or its change")
	}
	// The reason is checked again by the journal, which the entry reaches
	// only after the change: refused here, nothing is locked for nothing.
	if _, err := validReason(c.Reason); err != nil {
		return nil, err
	}
	var previews []ChallengePreview
	err := s.store.InLockedTx(ctx, func(ctx context.Context) error {
		ids, err := c.Touched(ctx)
		if err != nil {
			return err
		}
		previews = make([]ChallengePreview, len(ids))
		before := make([][]EliminationRow, len(ids))
		for i, id := range ids {
			previews[i].ChallengeID = id
			if previews[i].Before, err = s.boards.Get(ctx, actor, id); err != nil {
				return fmt.Errorf("rank challenge %s before the change: %w", id, err)
			}
			if before[i], err = s.store.Eliminations(ctx, id); err != nil {
				return err
			}
		}
		entry, err := c.Apply(ctx)
		if err != nil {
			return err
		}
		entry.AdminID, entry.ChallengeIDs, entry.Reason = actor.ID, ids, c.Reason
		if err := s.journal.Record(ctx, entry); err != nil {
			return err
		}
		for i, id := range ids {
			if err := s.recompute.Recompute(ctx, id); err != nil {
				return fmt.Errorf("recompute challenge %s: %w", id, err)
			}
			if previews[i].After, err = s.boards.Get(ctx, actor, id); err != nil {
				return fmt.Errorf("rank challenge %s after the change: %w", id, err)
			}
			after, err := s.store.Eliminations(ctx, id)
			if err != nil {
				return err
			}
			previews[i].Eliminations = eliminationChanges(before[i], after)
		}
		if c.DryRun {
			return errDryRun
		}
		return nil
	})
	if err != nil && !errors.Is(err, errDryRun) {
		return nil, err
	}
	return previews, nil
}

// eliminationChanges lists, in the order of after then of before, the
// participants whose elimination differs between before and after: given,
// removed, or of another week or distance.
func eliminationChanges(before, after []EliminationRow) []EliminationChange {
	was := make(map[string]EliminationRow, len(before))
	for _, e := range before {
		was[e.User.ID] = e
	}
	is := make(map[string]bool, len(after))
	var out []EliminationChange
	for _, e := range after {
		is[e.User.ID] = true
		now := e
		old, ok := was[e.User.ID]
		switch {
		case !ok:
			out = append(out, EliminationChange{User: e.User, After: &now})
		case old.WeekNumber != e.WeekNumber || old.DistanceMeters != e.DistanceMeters || old.RequiredMeters != e.RequiredMeters:
			out = append(out, EliminationChange{User: e.User, Before: &old, After: &now})
		}
	}
	for _, e := range before {
		if !is[e.User.ID] {
			old := e
			out = append(out, EliminationChange{User: e.User, Before: &old})
		}
	}
	return out
}
