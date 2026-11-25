package user

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
)

// The admin use cases below change any account as actor, an admin
// (adminRefusal, 404 not_found otherwise), with the validation of the user
// (README §2.12). Each one runs in the transaction ctx carries
// (admin.AccountCorrections, which records the change in the same
// transaction) and first locks the row of the account: an unknown or
// deleted account answers 404 user_not_found.

// AccountChange is what an admin change of an account wrote, for its
// journal line: the fields that changed, before (Old) and after (New), both
// empty when nothing changed.
type AccountChange struct {
	Old map[string]any
	New map[string]any
}

// The journal fields of an account. The profile ones (pseudo and names)
// are erased from the journal with the account (admin.Journal.EraseUser).
const (
	journalPseudo    = "pseudo"
	journalFirstName = "first_name"
	journalLastName  = "last_name"
	journalBlockedAt = "blocked_at"
)

// noChange is the AccountChange of an admin change that wrote nothing.
func noChange() AccountChange {
	return AccountChange{Old: map[string]any{}, New: map[string]any{}}
}

// Deletion is an account an admin erased (AdminDelete), whose identity at
// Auth0 is to delete once the transaction commits (CompleteDeletion). Its
// subject stays in the module: never logged nor journaled.
type Deletion struct {
	// At is the instant of the deletion.
	At      time.Time
	subject string
}

// AdminUpdateProfile changes the pseudo (or removes it) and the names of
// account id, with the rules of UpdateProfile: 400 validation_failed for an
// invalid field, 409 validation_failed for a pseudo already taken. The
// language stays the user's: changes.Language is ignored.
func (s *Service) AdminUpdateProfile(ctx context.Context, actor auth.Actor, id string, changes ProfileChanges) (AccountChange, error) {
	if err := adminRefusal(actor); err != nil {
		return AccountChange{}, err
	}
	changes.Language = nil
	if fields := invalidProfileFields(changes); len(fields) > 0 {
		return AccountChange{}, apperr.InvalidFields(nil, fields...)
	}
	before, err := s.repo.Lock(ctx, id)
	if err != nil {
		return AccountChange{}, accountErr(err)
	}
	after, err := s.repo.UpdateProfile(ctx, id, changes)
	if errors.Is(err, errPseudoTaken) {
		e := apperr.InvalidFields(err, "pseudo")
		e.Status = http.StatusConflict
		return AccountChange{}, e
	}
	if err != nil {
		return AccountChange{}, accountErr(err)
	}
	return profileChange(before, after), nil
}

// profileChange lists the profile fields that differ between before and
// after.
func profileChange(before, after User) AccountChange {
	c := noChange()
	for _, f := range []struct {
		name          string
		before, after *string
	}{
		{journalPseudo, before.Pseudo, after.Pseudo},
		{journalFirstName, before.FirstName, after.FirstName},
		{journalLastName, before.LastName, after.LastName},
	} {
		if value(f.before) != value(f.after) {
			c.Old[f.name], c.New[f.name] = value(f.before), value(f.after)
		}
	}
	return c
}

// value is the value of a profile field, nil when not set.
func value(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// AdminBlock blocks account id at the clock's now: from then on, its
// requests but the deletion of its own account answer account_blocked
// (GetOrCreateBySubject, blockedRefusal). Its challenges are left as they
// are. An account already blocked stays blocked since the first time, and
// nothing changes. An admin never blocks their own account (blockRefusal,
// 403 forbidden).
func (s *Service) AdminBlock(ctx context.Context, actor auth.Actor, id string) (AccountChange, error) {
	if err := adminRefusal(actor); err != nil {
		return AccountChange{}, err
	}
	if err := blockRefusal(actor, id); err != nil {
		return AccountChange{}, err
	}
	u, err := s.repo.Lock(ctx, id)
	if err != nil {
		return AccountChange{}, accountErr(err)
	}
	if u.BlockedAt != nil {
		return noChange(), nil
	}
	at := s.clock.Now()
	if err := s.repo.SetBlockedAt(ctx, id, &at); err != nil {
		return AccountChange{}, fmt.Errorf("block account: %w", err)
	}
	return AccountChange{Old: map[string]any{journalBlockedAt: nil}, New: map[string]any{journalBlockedAt: at}}, nil
}

// AdminUnblock unblocks account id: its requests are served again, nothing
// else changes. An account that is not blocked stays so, and nothing
// changes.
func (s *Service) AdminUnblock(ctx context.Context, actor auth.Actor, id string) (AccountChange, error) {
	if err := adminRefusal(actor); err != nil {
		return AccountChange{}, err
	}
	u, err := s.repo.Lock(ctx, id)
	if err != nil {
		return AccountChange{}, accountErr(err)
	}
	if u.BlockedAt == nil {
		return noChange(), nil
	}
	if err := s.repo.SetBlockedAt(ctx, id, nil); err != nil {
		return AccountChange{}, fmt.Errorf("unblock account: %w", err)
	}
	return AccountChange{Old: map[string]any{journalBlockedAt: u.BlockedAt.UTC()}, New: map[string]any{journalBlockedAt: nil}}, nil
}

// AdminDelete erases account id at the clock's now, with the effect of
// Delete (README §2.12): refused (422 last_creator) while it is the last
// creator of a challenge not finished, its rows erased, its
// deletion at Auth0 recorded as pending. The caller calls CompleteDeletion
// once its transaction commits, never before: the identity is deleted at
// Auth0 only with the account.
func (s *Service) AdminDelete(ctx context.Context, actor auth.Actor, id string) (Deletion, error) {
	if err := adminRefusal(actor); err != nil {
		return Deletion{}, err
	}
	at := s.clock.Now()
	subject, err := s.erase(ctx, id, at)
	if err != nil {
		return Deletion{}, accountErr(err)
	}
	return Deletion{At: at, subject: subject}, nil
}

// CompleteDeletion deletes the identity of d at Auth0, after the commit of
// its account deletion (AdminDelete), as Delete does: a failure stays
// pending for the worker (retry-auth0-deletions), so nothing fails.
func (s *Service) CompleteDeletion(ctx context.Context, d Deletion) {
	s.deleteIdentity(ctx, d.subject)
}

// accountErr turns an unknown or deleted account into 404 user_not_found,
// and wraps anything else.
func accountErr(err error) error {
	if errors.Is(err, errUserNotFound) {
		return apperr.New(apperr.CodeUserNotFound, err)
	}
	return fmt.Errorf("admin account change: %w", err)
}
