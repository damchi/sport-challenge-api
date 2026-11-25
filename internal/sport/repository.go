package sport

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/damchi/sport-challenge-api/internal/platform/db"
)

// Repository is the only access to the sports table.
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
