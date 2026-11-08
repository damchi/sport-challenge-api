package sport

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// Repository is the only access to the sports table.
type Repository struct {
	db *gorm.DB
}

// NewRepository returns a Repository on db.
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// List returns every sport, sorted by code.
func (r *Repository) List(ctx context.Context) ([]Sport, error) {
	var sports []Sport
	if err := r.db.WithContext(ctx).Order("code").Find(&sports).Error; err != nil {
		return nil, fmt.Errorf("list sports: %w", err)
	}
	return sports, nil
}
