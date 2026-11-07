package user

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// pseudoIndex is the unique index on lower(pseudo) (decision D5).
const pseudoIndex = "users_pseudo_lower_key"

// Errors of the repository, wrapped with their cause.
var (
	errUserNotFound = errors.New("user not found")
	errPseudoTaken  = errors.New("pseudo taken")
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

// GetByID returns the active user of id. An unknown or anonymized user gives
// errUserNotFound: a deleted account is never served, even to a request
// racing the deletion.
func (r *Repository) GetByID(ctx context.Context, id string) (User, error) {
	var u User
	err := r.db.WithContext(ctx).Where("id = ? AND anonymized_at IS NULL", id).Take(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return User{}, fmt.Errorf("read user: %w", errUserNotFound)
	}
	if err != nil {
		return User{}, fmt.Errorf("read user: %w", err)
	}
	return u, nil
}

// UpdateProfile writes the changed profile columns of user id and returns the
// row as stored. An unknown or anonymized user gives errUserNotFound. A
// pseudo already used by another user, in any case, gives
// errPseudoTaken: the unique index decides, so two concurrent requests
// cannot both take it.
func (r *Repository) UpdateProfile(ctx context.Context, id string, changes ProfileChanges) (User, error) {
	columns := map[string]any{}
	if changes.Pseudo != nil {
		columns["pseudo"] = *changes.Pseudo
	}
	if changes.Language != nil {
		columns["language"] = *changes.Language
	}
	if len(columns) > 0 {
		// An anonymized account is never written again, even by a PATCH
		// racing the account deletion: it counts as not found.
		res := r.db.WithContext(ctx).Model(&User{}).Where("id = ? AND anonymized_at IS NULL", id).Updates(columns)
		var pgErr *pgconn.PgError
		switch {
		case errors.As(res.Error, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == pseudoIndex:
			return User{}, fmt.Errorf("update user: %w", errPseudoTaken)
		case res.Error != nil:
			return User{}, fmt.Errorf("update user: %w", res.Error)
		case res.RowsAffected == 0:
			return User{}, fmt.Errorf("update user: %w", errUserNotFound)
		}
	}
	return r.GetByID(ctx, id)
}
