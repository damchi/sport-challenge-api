package user

import (
	"context"
	"fmt"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
)

// repository is what the service needs from the storage.
type repository interface {
	GetOrCreateBySubject(ctx context.Context, subject string) (User, error)
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
