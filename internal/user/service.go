package user

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
)

// repository is what the service needs from the storage.
type repository interface {
	GetOrCreateBySubject(ctx context.Context, subject string) (User, error)
	GetByID(ctx context.Context, id string) (User, error)
	UpdateProfile(ctx context.Context, id string, changes ProfileChanges) (User, error)
}

// Service holds the use cases of the local users.
type Service struct {
	repo repository
}

// NewService returns a Service on repo.
func NewService(repo repository) *Service {
	return &Service{repo: repo}
}

// GetOrCreateBySubject returns the local user of an auth subject, created
// with an empty profile (no pseudo) on first login. A row that is already
// anonymized answers account_deleted. Anonymization replaces auth_subject, so
// a deleted subject no longer matches that row: SC-API-039 keeps a
// fingerprint of deleted subjects, checked here before any insert, to make
// the deletion final.
func (s *Service) GetOrCreateBySubject(ctx context.Context, subject string) (User, error) {
	u, err := s.repo.GetOrCreateBySubject(ctx, subject)
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
