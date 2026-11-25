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

// Boards gives the rankings of a challenge as actor reads them, and where
// its weeks stand, in the transaction ctx carries (module leaderboard).
type Boards interface {
	Get(ctx context.Context, actor auth.Actor, id string) (leaderboard.Leaderboard, error)
	Weeks(ctx context.Context, actor auth.Actor, id string) (leaderboard.Weeks, error)
}

// correctionStore runs the transaction of an admin change and reads what
// its preview compares.
type correctionStore interface {
	// InLockedTx runs fn in a transaction, carried by the ctx it gets, that
	// takes the week lock alone first; rolled back when fn fails.
	InLockedTx(ctx context.Context, fn func(ctx context.Context) error) error
	// InTx runs fn in a transaction, carried by the ctx it gets, without the
	// week lock; rolled back when fn fails.
	InTx(ctx context.Context, fn func(ctx context.Context) error) error
	// LockParticipants takes FOR SHARE, in the order of their ids, the
	// accounts not deleted of the participants of the challenges ids, in
	// the transaction ctx carries.
	LockParticipants(ctx context.Context, ids []string) error
	// State reads the state of a challenge, deleted or not, in the
	// transaction ctx carries.
	State(ctx context.Context, challengeID string) (ChallengeState, error)
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
	// gives those it counts in before and after it. Nil, and only then, for
	// a change of NoChallenge.
	Touched func(ctx context.Context) ([]string, error)
	// NoChallenge marks a change that touches no challenge (an account):
	// nothing is recomputed, and its transaction takes no week lock. Said
	// rather than read from a nil Touched, so that a change of challenges
	// that forgets its Touched is refused, not run without the week lock.
	NoChallenge bool
	// Apply makes the change in the transaction and returns its journal
	// entry; its admin, challenges and reason are set by Run.
	Apply func(ctx context.Context) (Entry, error)
	// Reason is the reason of the change, that of its journal entry. A
	// preview may go without one (empty): it writes nothing, its journal
	// line neither.
	Reason string
	// DryRun previews the change: everything is done, then rolled back.
	DryRun bool
	// lifecycle tells whether the change deletes or restores the challenges
	// it touches, a deleted challenge having no rankings.
	lifecycle lifecycle
}

// lifecycle is what a correction does to the deletion of the challenges it
// touches. Its zero value leaves them as they are: each one is ranked
// before and after the change, and recomputed.
type lifecycle int

const (
	// deletes deletes them: each one is ranked before the change only, and
	// not recomputed (the worker judges no deleted challenge), its
	// eliminations and frozen results kept for a restore.
	deletes lifecycle = iota + 1
	// restores restores them: each one is ranked after the change only,
	// once recomputed, its eliminations compared with those kept at the
	// deletion.
	restores
)

// ChallengePreview is what a change does to a challenge: its title, status
// and reference sport after it (the latter before too), its rankings before
// and after, the eliminations that change, what becomes of its frozen
// results (Results, "" when frozen on neither side), and the weeks and the
// points of the points category that change. Before is nil when the change
// restores the challenge, After when it deletes it: a deleted challenge has
// no rankings, and neither weeks nor points are compared then.
type ChallengePreview struct {
	ChallengeID    string
	Title          string
	Status         string
	ReferenceSport SportChange
	Results        string
	Weeks          []WeekChange
	Points         []PointsChange
	Before         *leaderboard.Leaderboard
	After          *leaderboard.Leaderboard
	Eliminations   []EliminationChange
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

// Run makes change c as actor, in one transaction: it reads the rankings
// and eliminations of each challenge touched, applies the change, writes its
// journal line, recomputes each challenge touched, then reads them again (a
// deleted challenge is neither ranked nor recomputed: lifecycle). With
// c.DryRun, the transaction is rolled back: nothing is written, the journal
// line included, and the previews tell what the change would do; a change
// with effects outside the database (Auth0, e-mail) leaves them out when
// c.DryRun. Admin only (RequireAdmin), checked before anything is read; a
// reason out of its bounds is then refused (400 validation_failed on
// reason) before the transaction opens, an empty one too unless c.DryRun
// (README §2.12: a preview goes without a reason, and writes no journal
// line). An empty reason thus stands for none: over HTTP, one the client
// sent is refused by the contract (AdminReason, minLength), so a caller of
// Run is to tell the two apart itself.
//
// A change of c.NoChallenge recomputes nothing: its transaction takes none
// of the locks of steps 1 to 3 and 5 below, only those of c.Apply, so that it
// holds back no activity write nor run of the worker (InTx).
//
// The locks are taken in this order, so that nothing waits for the change
// in a cycle:
//  1. the week lock alone (InLockedTx), so that no activity write and no
//     run of the worker is in progress, in the order of the activity writes
//     (the lock, then the rows);
//  2. what c.Touched locks to read the challenges (the row of the author of
//     an activity, FOR UPDATE);
//  3. the accounts of the participants of the challenges touched, FOR SHARE
//     in the order of their ids (LockParticipants): the deletion of one of
//     them locks its row first, then erases its entries from the frozen
//     results, which the recompute deletes and writes again; the deletion
//     either commits before, and the account is in no ranking, or waits for
//     the change, then erases its entries itself;
//  4. what c.Apply locks: the row of the account it targets, FOR UPDATE
//     (stronger than the lock of step 3, which it does not wait for), then
//     those of the challenge;
//  5. the recompute, which takes again the week lock and the accounts of
//     the participants (held already), then deletes and writes the results
//     of each challenge.
//
// The author of step 2 comes before the others, out of the order of the ids:
// no cycle follows between the changes that touch challenges, as only one of
// them runs at a time (the week lock). A change of an account runs beside
// them: it holds the row of its target FOR UPDATE, then its journal line
// takes that of its admin FOR KEY SHARE (admin_actions.admin_id), out of any
// order. The app has a single admin, yet nothing stops two of their
// requests at once: two of them crossed, or one and a correction that holds
// the row of its admin (an activity of that admin, step 2) and waits for its
// target, may thus meet in a deadlock. So may an account deletion and a run
// of the worker (close-weeks), which holds the week lock shared and writes
// the rows of participants and results the deletion writes too. PostgreSQL
// rolls one back, which answers busy (InTx), as rare as the same deadlock
// with DELETE /me, which never took the week lock.
func (s *Corrections) Run(ctx context.Context, actor auth.Actor, c Correction) ([]ChallengePreview, error) {
	if err := RequireAdmin(actor); err != nil {
		return nil, err
	}
	if c.Apply == nil || (c.Touched == nil) != c.NoChallenge {
		return nil, errors.New("correction without its change, or its challenges not said")
	}
	// The reason is checked again by the journal, which the entry reaches
	// only after the change: refused here, nothing is locked for nothing. A
	// preview without one writes no journal line: there is none to check.
	withoutReason := c.DryRun && c.Reason == ""
	if !withoutReason {
		if _, err := validReason(c.Reason); err != nil {
			return nil, err
		}
	}
	inTx := s.store.InLockedTx
	if c.NoChallenge {
		inTx = s.store.InTx
	}
	var previews []ChallengePreview
	err := inTx(ctx, func(ctx context.Context) error {
		var (
			ids []string
			err error
		)
		if !c.NoChallenge {
			if ids, err = c.Touched(ctx); err != nil {
				return err
			}
		}
		if len(ids) > 0 {
			if err := s.store.LockParticipants(ctx, ids); err != nil {
				return err
			}
		}
		previews = make([]ChallengePreview, len(ids))
		before := make([]reading, len(ids))
		for i, id := range ids {
			if before[i], err = s.read(ctx, actor, id, c.lifecycle != restores); err != nil {
				return fmt.Errorf("read challenge %s before the change: %w", id, err)
			}
		}
		entry, err := c.Apply(ctx)
		if err != nil {
			return err
		}
		entry.AdminID, entry.ChallengeIDs, entry.Reason = actor.ID, ids, c.Reason
		if !withoutReason {
			if err := s.journal.Record(ctx, entry); err != nil {
				return err
			}
		}
		// The status after the change is that of the instant the journal
		// dates it by.
		now := s.journal.now()
		for i, id := range ids {
			ranked := c.lifecycle != deletes
			if ranked {
				if err := s.recompute.Recompute(ctx, id); err != nil {
					return fmt.Errorf("recompute challenge %s: %w", id, err)
				}
			}
			after, err := s.read(ctx, actor, id, ranked)
			if err != nil {
				return fmt.Errorf("read challenge %s after the change: %w", id, err)
			}
			previews[i] = preview(id, before[i], after, now, c.lifecycle)
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

// read reads challenge id as actor, in the transaction ctx carries: its
// state, then, when ranked, its rankings and its weeks.
func (s *Corrections) read(ctx context.Context, actor auth.Actor, id string, ranked bool) (reading, error) {
	var r reading
	if ranked {
		board, err := s.boards.Get(ctx, actor, id)
		if err != nil {
			return reading{}, fmt.Errorf("rank: %w", err)
		}
		weeks, err := s.boards.Weeks(ctx, actor, id)
		if err != nil {
			return reading{}, fmt.Errorf("weeks: %w", err)
		}
		r.board, r.weeks = &board, &weeks
	}
	state, err := s.store.State(ctx, id)
	if err != nil {
		return reading{}, err
	}
	r.state = state
	return r, nil
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
