package user

import (
	"context"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Repository is the only access to the users table.
type Repository struct {
	db *gorm.DB
}

// NewRepository returns a Repository on db.
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// GetOrCreateBySubject returns the user of an auth subject, inserting it if
// needed. ON CONFLICT DO NOTHING makes concurrent first logins create a
// single row; on a conflict nothing is returned, so the read that follows
// gives the user in every case.
func (r *Repository) GetOrCreateBySubject(ctx context.Context, subject string) (User, error) {
	db := r.db.WithContext(ctx)
	onConflict := clause.OnConflict{Columns: []clause.Column{{Name: "auth_subject"}}, DoNothing: true}
	if err := db.Clauses(onConflict).Create(&User{AuthSubject: subject}).Error; err != nil {
		return User{}, fmt.Errorf("insert user: %w", err)
	}
	var u User
	if err := db.Where("auth_subject = ?", subject).Take(&u).Error; err != nil {
		return User{}, fmt.Errorf("read user: %w", err)
	}
	return u, nil
}
