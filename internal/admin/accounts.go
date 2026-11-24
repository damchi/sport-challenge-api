package admin

import (
	"context"

	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/user"
)

// The journal codes of the admin changes of an account.
const (
	ActionUserUpdate  = "user.update"
	ActionUserBlock   = "user.block"
	ActionUserUnblock = "user.unblock"
	ActionUserDelete  = "user.delete"
)

// journalUserDeletedAt is the journal field of the deletion of an account.
const journalUserDeletedAt = "deleted_at"

// AccountChanges changes any account as an admin, in the transaction ctx
// carries, each change first locking the row of the account (module user).
type AccountChanges interface {
	AdminUpdateProfile(ctx context.Context, actor auth.Actor, id string, changes user.ProfileChanges) (user.AccountChange, error)
	AdminBlock(ctx context.Context, actor auth.Actor, id string) (user.AccountChange, error)
	AdminUnblock(ctx context.Context, actor auth.Actor, id string) (user.AccountChange, error)
	AdminDelete(ctx context.Context, actor auth.Actor, id string) (user.Deletion, error)
	// CompleteDeletion deletes the identity at Auth0, once the deletion is
	// committed.
	CompleteDeletion(ctx context.Context, d user.Deletion)
}

// AccountCorrections are the admin changes of an account (README §2.12):
// each one and its journal line (the account as target, no challenge), in
// one transaction (Corrections.Run). None judges a challenge again, so none
// is previewed (no dry run) and none names a challenge in the journal; the
// deletion has the effect of DELETE /me, which judges nothing either.
type AccountCorrections struct {
	corrections *Corrections
	users       AccountChanges
}

// NewAccountCorrections returns AccountCorrections.
func NewAccountCorrections(corrections *Corrections, users AccountChanges) *AccountCorrections {
	return &AccountCorrections{corrections: corrections, users: users}
}

// accountChange is one of the changes of AccountChanges that give an
// AccountChange.
type accountChange func(ctx context.Context, actor auth.Actor, id string) (user.AccountChange, error)

// UpdateProfile changes the pseudo and the names of account id, with the
// rules of PATCH /me.
func (s *AccountCorrections) UpdateProfile(ctx context.Context, actor auth.Actor, id string, changes user.ProfileChanges, reason string) error {
	return s.run(ctx, actor, id, ActionUserUpdate, func(ctx context.Context, actor auth.Actor, id string) (user.AccountChange, error) {
		return s.users.AdminUpdateProfile(ctx, actor, id, changes)
	}, reason)
}

// Block blocks account id, never that of actor.
func (s *AccountCorrections) Block(ctx context.Context, actor auth.Actor, id, reason string) error {
	return s.run(ctx, actor, id, ActionUserBlock, s.users.AdminBlock, reason)
}

// Unblock unblocks account id.
func (s *AccountCorrections) Unblock(ctx context.Context, actor auth.Actor, id, reason string) error {
	return s.run(ctx, actor, id, ActionUserUnblock, s.users.AdminUnblock, reason)
}

// run makes change on account id as actor, in Corrections.Run, recorded
// under action.
func (s *AccountCorrections) run(ctx context.Context, actor auth.Actor, id, action string, change accountChange, reason string) error {
	_, err := s.corrections.Run(ctx, actor, Correction{
		Touched: noChallenge,
		Apply: func(ctx context.Context) (Entry, error) {
			c, err := change(ctx, actor, id)
			if err != nil {
				return Entry{}, err
			}
			return Entry{Action: action, TargetType: TargetUser, TargetID: id, OldValue: c.Old, NewValue: c.New}, nil
		},
		Reason: reason,
	})
	return err
}

// Delete deletes account id with the effect of DELETE /me: its deletion,
// which erases the profile values of its journal lines too (Journal.EraseUser,
// one of its erasers), and its own journal line commit together; its
// identity is then deleted at Auth0, a failure being replayed by the worker.
func (s *AccountCorrections) Delete(ctx context.Context, actor auth.Actor, id, reason string) error {
	var deletion user.Deletion
	_, err := s.corrections.Run(ctx, actor, Correction{
		Touched: noChallenge,
		Apply: func(ctx context.Context) (Entry, error) {
			d, err := s.users.AdminDelete(ctx, actor, id)
			if err != nil {
				return Entry{}, err
			}
			deletion = d
			return Entry{
				Action: ActionUserDelete, TargetType: TargetUser, TargetID: id,
				OldValue: map[string]any{journalUserDeletedAt: nil}, NewValue: map[string]any{journalUserDeletedAt: d.At},
			}, nil
		},
		Reason: reason,
	})
	if err != nil {
		return err
	}
	// Committed: only now may the identity go.
	s.users.CompleteDeletion(ctx, deletion)
	return nil
}

// noChallenge is the Touched of a change that touches no challenge.
func noChallenge(context.Context) ([]string, error) { return nil, nil }
