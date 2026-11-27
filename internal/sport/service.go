package sport

import (
	"context"
	"fmt"
	"slices"
)

// repository is what the service needs from the storage.
type repository interface {
	List(ctx context.Context) ([]Sport, error)
	ListExercises(ctx context.Context, sportCode string) ([]Exercise, error)
	ListAllExercises(ctx context.Context) ([]Exercise, error)
}

// Service holds the use cases of the catalogue of sports and exercises.
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

// Catalogue returns the catalogue like List, each sport with its exercises
// in display order (never nil), in two reads whatever the number of sports.
func (s *Service) Catalogue(ctx context.Context) ([]Sport, error) {
	sports, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	exercises, err := s.repo.ListAllExercises(ctx)
	if err != nil {
		return nil, fmt.Errorf("list catalogue exercises: %w", err)
	}
	bySport := make(map[string][]string)
	for _, e := range exercises {
		bySport[e.SportCode] = append(bySport[e.SportCode], e.Code)
	}
	for i := range sports {
		sports[i].Exercises = bySport[sports[i].Code]
		if sports[i].Exercises == nil {
			sports[i].Exercises = []string{}
		}
	}
	return sports, nil
}

// SuggestedFactors returns the suggested distance factor of every sport with
// a distance (required or optional), by code: the defaults of a new
// challenge.
func (s *Service) SuggestedFactors(ctx context.Context) (map[string]float64, error) {
	sports, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	factors := make(map[string]float64, len(sports))
	for _, sp := range sports {
		if sp.SuggestedFactor != nil {
			factors[sp.Code] = *sp.SuggestedFactor
		}
	}
	return factors, nil
}

// DistanceMode returns the distance mode of sport code (required, none or
// optional), and false when the catalogue has no such sport.
func (s *Service) DistanceMode(ctx context.Context, code string) (string, bool, error) {
	sp, found, err := s.Find(ctx, code)
	return sp.DistanceMode, found, err
}

// Find returns sport code, and false when the catalogue has no such sport.
func (s *Service) Find(ctx context.Context, code string) (Sport, bool, error) {
	sports, err := s.List(ctx)
	if err != nil {
		return Sport{}, false, err
	}
	for _, sp := range sports {
		if sp.Code == code {
			return sp, true, nil
		}
	}
	return Sport{}, false, nil
}

// Exercises returns the exercises of sport sportCode in display order, empty
// for a sport without exercises or unknown to the catalogue.
func (s *Service) Exercises(ctx context.Context, sportCode string) ([]Exercise, error) {
	exercises, err := s.repo.ListExercises(ctx, sportCode)
	if err != nil {
		return nil, fmt.Errorf("list exercises of %s: %w", sportCode, err)
	}
	return exercises, nil
}

// HasExercise tells whether exercise code belongs to sport sportCode.
func (s *Service) HasExercise(ctx context.Context, sportCode, code string) (bool, error) {
	exercises, err := s.Exercises(ctx, sportCode)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(exercises, func(e Exercise) bool { return e.Code == code }), nil
}
