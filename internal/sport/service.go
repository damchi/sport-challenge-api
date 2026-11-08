package sport

import (
	"context"
	"fmt"
)

// repository is what the service needs from the storage.
type repository interface {
	List(ctx context.Context) ([]Sport, error)
}

// Service holds the use cases of the sports catalogue.
type Service struct {
	repo repository
}

// NewService returns a Service on repo.
func NewService(repo repository) *Service {
	return &Service{repo: repo}
}

// List returns the catalogue, sorted by code. Every authenticated user may
// read it: it holds no personal data.
func (s *Service) List(ctx context.Context) ([]Sport, error) {
	sports, err := s.repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list catalogue: %w", err)
	}
	return sports, nil
}
