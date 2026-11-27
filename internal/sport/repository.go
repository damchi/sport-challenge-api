package sport

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/damchi/sport-challenge-api/internal/platform/db"
)

// Repository is the only access to the sports and exercises tables.
type Repository struct {
	db *gorm.DB
}

// NewRepository returns a Repository on gdb.
func NewRepository(gdb *gorm.DB) *Repository {
	return &Repository{db: gdb}
}

// List returns every sport, sorted by code. It reads in the transaction ctx
// carries (db.Conn): an admin change reads the catalogue under the week
// lock, and a second connection would wait for those the activity writes
// hold while they wait for that lock.
func (r *Repository) List(ctx context.Context) ([]Sport, error) {
	var sports []Sport
	if err := db.Conn(ctx, r.db).Order("code").Find(&sports).Error; err != nil {
		return nil, fmt.Errorf("list sports: %w", err)
	}
	return sports, nil
}

// ListAllExercises returns every exercise, sorted by sport then display
// order, in the transaction ctx carries (db.Conn), like List.
func (r *Repository) ListAllExercises(ctx context.Context) ([]Exercise, error) {
	var exercises []Exercise
	if err := db.Conn(ctx, r.db).Order("sport_code, position").Find(&exercises).Error; err != nil {
		return nil, fmt.Errorf("list all exercises: %w", err)
	}
	return exercises, nil
}

// ListExercises returns the exercises of sport sportCode, in display order,
// in the transaction ctx carries (db.Conn), like List.
func (r *Repository) ListExercises(ctx context.Context, sportCode string) ([]Exercise, error) {
	var exercises []Exercise
	if err := db.Conn(ctx, r.db).Where("sport_code = ?", sportCode).Order("position").Find(&exercises).Error; err != nil {
		return nil, fmt.Errorf("list exercises: %w", err)
	}
	return exercises, nil
}
