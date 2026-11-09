package user

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
)

// repository is what the service needs from the storage.
type repository interface {
	GetOrCreateBySubject(ctx context.Context, subject string) (User, error)
	GetByID(ctx context.Context, id string) (User, error)
	UpdateProfile(ctx context.Context, id string, changes ProfileChanges) (User, error)
	Pseudos(ctx context.Context, ids []string) (map[string]string, error)
	Delete(ctx context.Context, id string, abandon func(ctx context.Context) error) (string, error)
	AddPendingAuth0Deletion(ctx context.Context, subject string) error
}

// Participations makes a user abandon their challenges (module challenge).
type Participations interface {
	AbandonAll(ctx context.Context, userID string) error
}

// Identities deletes a user at the auth provider (Auth0 Management API).
type Identities interface {
	DeleteUser(ctx context.Context, subject string) error
}

// Service holds the use cases of the local users.
type Service struct {
	repo           repository
	participations Participations
	identities     Identities
	logger         *slog.Logger
}

// NewService returns a Service on repo.
func NewService(repo repository, participations Participations, identities Identities, logger *slog.Logger) *Service {
	return &Service{repo: repo, participations: participations, identities: identities, logger: logger}
}

// GetOrCreateBySubject returns the local user of an auth subject, created
// with an empty profile (no pseudo) on first login. A row that is already
// anonymized answers account_deleted. Anonymization replaces auth_subject, so
// a deleted subject no longer matches that row: its fingerprint, checked
// before any insert, answers account_deleted too, so the deletion is final.
func (s *Service) GetOrCreateBySubject(ctx context.Context, subject string) (User, error) {
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
	return u, nil
}

// Profile returns the profile of user actorID. Each user only reads their
// own profile: the ID comes from the access token, never from the request.
func (s *Service) Profile(ctx context.Context, actorID string) (User, error) {
	u, err := s.repo.GetByID(ctx, actorID)
	if errors.Is(err, errUserNotFound) {
		return User{}, apperr.New(apperr.CodeNotFound, err)
	}
	if err != nil {
		return User{}, fmt.Errorf("get profile: %w", err)
	}
	return u, nil
}

// UpdateProfile changes the pseudo and the language of user actorID. Invalid
// fields answer 400 validation_failed; a pseudo already taken, regardless of
// case, answers 409 validation_failed, both listing the fields in
// params.fields.
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
		return User{}, apperr.New(apperr.CodeNotFound, err)
	case err != nil:
		return User{}, fmt.Errorf("update profile: %w", err)
	}
	return u, nil
}

// ProfileComplete reports whether the profile of user id is complete (pseudo
// set): creating or joining a challenge requires it.
func (s *Service) ProfileComplete(ctx context.Context, id string) (bool, error) {
	u, err := s.Profile(ctx, id)
	if err != nil {
		return false, err
	}
	return u.ProfileComplete(), nil
}

// Pseudos returns the pseudo of each user of ids that has one; an anonymized
// user has none, and the apps show a neutral name.
func (s *Service) Pseudos(ctx context.Context, ids []string) (map[string]string, error) {
	if len(ids) == 0 {
		return map[string]string{}, nil
	}
	return s.repo.Pseudos(ctx, ids)
}

// Delete deletes the account of user actorID (README §6): in one
// transaction, the account is anonymized (pseudo and avatar erased, subject
// replaced, its fingerprint kept so that it never logs in again) and every
// challenge not finished is abandoned. The user is then deleted at Auth0; if
// that fails, the deletion is recorded for the worker to replay. Only a
// failure to record it fails the call, the account being deleted already.
func (s *Service) Delete(ctx context.Context, actorID string) error {
	subject, err := s.repo.Delete(ctx, actorID, func(ctx context.Context) error {
		return s.participations.AbandonAll(ctx, actorID)
	})
	// Deleted meanwhile by a concurrent request: the account is gone.
	if errors.Is(err, errUserNotFound) {
		return apperr.New(apperr.CodeAccountDeleted, err)
	}
	if err != nil {
		return fmt.Errorf("delete account: %w", err)
	}
	// The account is deleted: a client that goes away must not stop what
	// follows, or the deletion at Auth0 would be neither done nor recorded.
	ctx = context.WithoutCancel(ctx)
	deleteErr := s.identities.DeleteUser(ctx, subject)
	if deleteErr == nil {
		return nil
	}
	if err := s.repo.AddPendingAuth0Deletion(ctx, subject); err != nil {
		return fmt.Errorf("delete account: auth0 deletion failed (%w) and could not be recorded: %w", deleteErr, err)
	}
	// Never the subject: the error of the Auth0 client does not carry it.
	s.logger.WarnContext(ctx, "auth0 deletion failed, recorded for retry", slog.String("error", deleteErr.Error()))
	return nil
}
