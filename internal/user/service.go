package user

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
)

// repository is what the service needs from the storage.
type repository interface {
	GetOrCreateBySubject(ctx context.Context, subject string) (User, error)
	GetByID(ctx context.Context, id string) (User, error)
	UpdateProfile(ctx context.Context, id string, changes ProfileChanges) (User, error)
	UpdateEmail(ctx context.Context, id, email string) error
	PublicNames(ctx context.Context, ids []string) (map[string]PublicName, error)
	Delete(ctx context.Context, id string, at time.Time, erase func(ctx context.Context) error) (string, error)
	// Lock and SetBlockedAt serve the admin changes of an account, in the
	// transaction ctx carries.
	Lock(ctx context.Context, id string) (User, error)
	SetBlockedAt(ctx context.Context, id string, at *time.Time) error
	PendingAuth0Deletions(ctx context.Context) ([]string, error)
	RemovePendingAuth0Deletion(ctx context.Context, subject string) error
}

// DeletionLock locks the challenges an account deletion touches and
// refuses it while the user is their last creator (module challenge).
type DeletionLock interface {
	LockForDeletion(ctx context.Context, userID string) error
}

// Eraser deletes the rows of a user kept by another module (challenge,
// activity, invitation), in the transaction of the account deletion that
// ctx carries.
type Eraser interface {
	EraseUser(ctx context.Context, userID string) error
}

// Identities deletes a user at the auth provider (Auth0 Management API).
type Identities interface {
	DeleteUser(ctx context.Context, subject string) error
}

// pendingRemovalTimeout bounds the removal of the pending Auth0 deletion
// after an account deletion, waiting for a connection of the pool included:
// detached from the request, it is outside the request deadline.
const pendingRemovalTimeout = 5 * time.Second

// Service holds the use cases of the local users.
type Service struct {
	repo         repository
	deletionLock DeletionLock
	erasers      []Eraser
	identities   Identities
	clock        clock.Clock
	logger       *slog.Logger
}

// NewService returns a Service on repo.
func NewService(repo repository, deletionLock DeletionLock, erasers []Eraser, identities Identities, c clock.Clock, logger *slog.Logger) *Service {
	return &Service{repo: repo, deletionLock: deletionLock, erasers: erasers, identities: identities, clock: c, logger: logger}
}

// GetOrCreateBySubject returns the local user of an auth subject, created
// with an empty profile (no pseudo, no names) on first login. A row that is
// already anonymized answers account_deleted, one an admin blocked
// account_blocked (README §2.12). Anonymization replaces
// auth_subject, so a deleted subject no longer matches that row: its
// fingerprint, checked before any insert, answers account_deleted too, so
// the deletion is final.
//
// email is the e-mail of the access token, empty when the token has none: it
// is stored when it differs from the one kept, so a change at the auth
// provider follows; a token without it leaves the stored one as is. Failing
// to store it does not refuse the request: the e-mail is only displayed, and
// the next request tries again.
func (s *Service) GetOrCreateBySubject(ctx context.Context, subject, email string) (User, error) {
	u, err := s.repo.GetOrCreateBySubject(ctx, subject)
	if errors.Is(err, errAccountDeleted) {
		return User{}, apperr.New(apperr.CodeAccountDeleted, nil)
	}
	if err != nil {
		return User{}, fmt.Errorf("get or create user: %w", err)
	}
	if u.AnonymizedAt != nil {
		return User{}, apperr.New(apperr.CodeAccountDeleted, nil)
	}
	if u.BlockedAt != nil {
		return User{}, apperr.New(apperr.CodeAccountBlocked, nil)
	}
	if email, ok := tokenEmail(email); ok && (u.Email == nil || *u.Email != email) {
		if err := s.repo.UpdateEmail(ctx, u.ID, email); err != nil {
			// Never the e-mail: the error of the repository does not carry it.
			s.logger.WarnContext(ctx, "e-mail of the token not stored", slog.String("error", err.Error()))
		} else {
			u.Email = &email
		}
	}
	return u, nil
}

// Profile returns the profile of user actorID. Each user only reads their
// own profile: the ID comes from the access token, never from the request.
// The actor's row exists (CurrentUser created it), so a missing one was
// anonymized after the middleware read it: 401 account_deleted, as the
// client must log the user out.
func (s *Service) Profile(ctx context.Context, actorID string) (User, error) {
	u, err := s.repo.GetByID(ctx, actorID)
	if errors.Is(err, errUserNotFound) {
		return User{}, apperr.New(apperr.CodeAccountDeleted, err)
	}
	if err != nil {
		return User{}, fmt.Errorf("get profile: %w", err)
	}
	return u, nil
}

// UpdateProfile changes the pseudo (or removes it), the names and the
// language of user actorID. Invalid fields answer 400 validation_failed; a
// pseudo already taken, regardless of case, answers 409 validation_failed,
// both listing the fields in params.fields; an account deleted meanwhile
// answers 401 account_deleted (as Profile).
func (s *Service) UpdateProfile(ctx context.Context, actorID string, changes ProfileChanges) (User, error) {
	if fields := invalidProfileFields(changes); len(fields) > 0 {
		return User{}, apperr.InvalidFields(nil, fields...)
	}
	u, err := s.repo.UpdateProfile(ctx, actorID, changes)
	switch {
	case errors.Is(err, errPseudoTaken):
		e := apperr.InvalidFields(err, "pseudo")
		// The request is valid but conflicts with another user's pseudo.
		e.Status = http.StatusConflict
		return User{}, e
	case errors.Is(err, errUserNotFound):
		return User{}, apperr.New(apperr.CodeAccountDeleted, err)
	case err != nil:
		return User{}, fmt.Errorf("update profile: %w", err)
	}
	return u, nil
}

// ProfileComplete reports whether the profile of user id is complete
// (first name and last name set, the pseudo is optional): creating or
// joining a challenge and recording an activity require it. An account deleted meanwhile answers
// account_deleted (Profile), not an incomplete profile: anonymization clears
// the pseudo and the names, and a request racing the deletion may come
// after it.
func (s *Service) ProfileComplete(ctx context.Context, id string) (bool, error) {
	u, err := s.Profile(ctx, id)
	if err != nil {
		return false, err
	}
	return u.ProfileComplete(), nil
}

// PublicNames returns what the other users see of each user of ids: pseudo,
// first name and last name, never the e-mail. An anonymized user is left
// out, like an unknown one: a deleted account is listed nowhere.
func (s *Service) PublicNames(ctx context.Context, ids []string) (map[string]PublicName, error) {
	if len(ids) == 0 {
		return map[string]PublicName{}, nil
	}
	return s.repo.PublicNames(ctx, ids)
}

// Delete deletes the account of user actorID (README §6): in one
// transaction, the account is anonymized (pseudo, names, e-mail, avatar and
// language erased, subject replaced, its fingerprint kept so that it never logs in
// again), its deletion at Auth0 is recorded as pending, every challenge not
// finished it touches is locked (which refuses the last creator), then every row of
// the account kept by the other modules is erased (erasers); the row of the
// user stays, anonymized, for the challenges it created. The deletion is
// dated by the clock, like the other business dates. The user is then
// deleted at Auth0 and the pending deletion removed, even if the client
// left; the removal has its own deadline. Nothing after the commit fails the
// call: a deletion at Auth0 that fails, or whose record cannot be removed in
// time, stays pending for the worker to replay.
func (s *Service) Delete(ctx context.Context, actorID string) error {
	subject, err := s.erase(ctx, actorID, s.clock.Now())
	// Deleted meanwhile by a concurrent request: the account is gone.
	if errors.Is(err, errUserNotFound) {
		return apperr.New(apperr.CodeAccountDeleted, err)
	}
	if err != nil {
		return fmt.Errorf("delete account: %w", err)
	}
	s.deleteIdentity(ctx, subject)
	return nil
}

// erase anonymizes account id at the instant at, in one transaction (that
// of ctx, if any): its deletion at Auth0 recorded as pending, its
// challenges not finished locked, its rows in the other modules erased
// (Delete). It returns its former subject, for deleteIdentity. An unknown
// or anonymized account gives errUserNotFound.
func (s *Service) erase(ctx context.Context, id string, at time.Time) (string, error) {
	return s.repo.Delete(ctx, id, at, func(ctx context.Context) error {
		if err := s.deletionLock.LockForDeletion(ctx, id); err != nil {
			return err
		}
		for _, e := range s.erasers {
			if err := e.EraseUser(ctx, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// deleteIdentity deletes subject at Auth0, once its account is erased and
// committed, then removes its pending deletion, even if the client left;
// the removal has its own deadline. Nothing fails: a deletion at Auth0 that
// fails, or whose record cannot be removed in time, stays pending for the
// worker to replay.
func (s *Service) deleteIdentity(ctx context.Context, subject string) {
	// The account is deleted: a client that goes away must not stop what
	// follows, or the deletion at Auth0 would wait for the worker.
	ctx = context.WithoutCancel(ctx)
	// Never the subject in a log: the errors below do not carry it.
	if err := s.identities.DeleteUser(ctx, subject); err != nil {
		s.logger.WarnContext(ctx, "auth0 deletion failed, kept for retry", slog.String("error", err.Error()))
		return
	}
	// Removed only after the deletion: if this fails or runs out of time,
	// the worker replays a deletion already done, which is harmless.
	removeCtx, cancel := context.WithTimeout(ctx, pendingRemovalTimeout)
	defer cancel()
	if err := s.repo.RemovePendingAuth0Deletion(removeCtx, subject); err != nil {
		s.logger.WarnContext(ctx, "auth0 deletion done, pending record not removed", slog.String("error", err.Error()))
	}
}

// RetryResult counts what a run of RetryAuth0Deletions did, for the log.
type RetryResult struct {
	// Deleted is the number of users deleted at Auth0 and removed from the
	// pending list.
	Deleted int
	// Failed is the number of deletions that failed again and stay pending.
	Failed int
}

// RetryAuth0Deletions replays the deletions at Auth0 still pending (failed,
// or interrupted, when the accounts were deleted), and removes each one once
// done. It can be run again and again: a user Auth0 no longer knows counts as
// deleted, and a deletion that fails stays pending for the next run. A failure does not stop the
// others; the errors are joined. Never the subject in an error or a log.
func (s *Service) RetryAuth0Deletions(ctx context.Context) (RetryResult, error) {
	subjects, err := s.repo.PendingAuth0Deletions(ctx)
	if err != nil {
		return RetryResult{}, err
	}
	var result RetryResult
	var errs []error
	for _, subject := range subjects {
		// Stopped (SIGTERM): the rest stays pending, not counted as failed.
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		if err := s.identities.DeleteUser(ctx, subject); err != nil {
			result.Failed++
			errs = append(errs, fmt.Errorf("replay auth0 deletion: %w", err))
			continue
		}
		// Removed only after the deletion: a crash in between replays it,
		// which is harmless.
		if err := s.repo.RemovePendingAuth0Deletion(ctx, subject); err != nil {
			result.Failed++
			errs = append(errs, err)
			continue
		}
		result.Deleted++
	}
	return result, errors.Join(errs...)
}
