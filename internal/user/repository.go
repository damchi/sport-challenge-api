package user

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/damchi/sport-challenge-api/internal/platform/db"
)

// pseudoIndex is the unique index on lower(pseudo) (decision D5).
const pseudoIndex = "users_pseudo_lower_key"

// Errors of the repository, wrapped with their cause.
var (
	errUserNotFound   = errors.New("user not found")
	errPseudoTaken    = errors.New("pseudo taken")
	errAccountDeleted = errors.New("account deleted")
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
// needed. A subject of a deleted account (its fingerprint is in
// deleted_subjects) gives errAccountDeleted, and nothing is inserted.
//
// The user is read first: the anonymization of an account replaces its
// subject and stores the fingerprint in the same transaction, so a read that
// no longer finds the row is followed by a check that finds the
// fingerprint. ON CONFLICT DO NOTHING makes concurrent first logins create a
// single row; on a conflict nothing is returned, so the read that follows
// gives the user in every case.
func (r *Repository) GetOrCreateBySubject(ctx context.Context, subject string) (User, error) {
	gdb := r.db.WithContext(ctx)
	var found []User
	if err := gdb.Where("auth_subject = ?", subject).Limit(1).Find(&found).Error; err != nil {
		return User{}, fmt.Errorf("read user: %w", err)
	}
	if len(found) == 1 {
		return found[0], nil
	}
	var deleted bool
	if err := gdb.Raw("SELECT EXISTS (SELECT 1 FROM deleted_subjects WHERE fingerprint = decode(?, 'hex'))", subjectFingerprint(subject)).Scan(&deleted).Error; err != nil {
		return User{}, fmt.Errorf("read deleted subjects: %w", err)
	}
	if deleted {
		return User{}, fmt.Errorf("read user: %w", errAccountDeleted)
	}
	onConflict := clause.OnConflict{Columns: []clause.Column{{Name: "auth_subject"}}, DoNothing: true}
	if err := gdb.Clauses(onConflict).Create(&User{AuthSubject: subject}).Error; err != nil {
		return User{}, fmt.Errorf("insert user: %w", err)
	}
	var u User
	if err := gdb.Where("auth_subject = ?", subject).Take(&u).Error; err != nil {
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

// PublicNames returns the public name of each user of ids. An anonymized
// user is left out, like an unknown one: its names are never served. The
// e-mail is never read here.
func (r *Repository) PublicNames(ctx context.Context, ids []string) (map[string]PublicName, error) {
	var rows []struct {
		ID        string
		Pseudo    *string
		FirstName *string
		LastName  *string
	}
	err := r.db.WithContext(ctx).Model(&User{}).Select("id, pseudo, first_name, last_name").
		Where("id IN ? AND anonymized_at IS NULL", ids).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read public names: %w", err)
	}
	names := make(map[string]PublicName, len(rows))
	for _, row := range rows {
		names[row.ID] = PublicName{Pseudo: row.Pseudo, FirstName: row.FirstName, LastName: row.LastName}
	}
	return names, nil
}

// UpdateEmail writes the e-mail of user id. An anonymized user is left as
// is: a deleted account is never written again.
func (r *Repository) UpdateEmail(ctx context.Context, id, email string) error {
	err := r.db.WithContext(ctx).Model(&User{}).Where("id = ? AND anonymized_at IS NULL", id).
		Update("email", email).Error
	if err != nil {
		return fmt.Errorf("update email: %w", err)
	}
	return nil
}

// Delete anonymizes the account of user id and makes its deletion final, in
// one transaction that abandon joins (ctx carries it): the row is locked,
// the fingerprint of its subject is stored, the subject is replaced by a
// placeholder and the pseudo, names, e-mail, avatar and language are erased,
// the deletion at Auth0 is recorded as pending; then abandon runs (the
// abandons, then the erasure of the account's other rows). Both the
// anonymization and the fingerprint are dated at (the service's clock). It
// returns the former subject, for the deletion at Auth0. An unknown or
// already anonymized user gives errUserNotFound; an error of abandon rolls
// everything back, the pending deletion included.
func (r *Repository) Delete(ctx context.Context, id string, at time.Time, abandon func(ctx context.Context) error) (string, error) {
	var subject string
	err := db.InTx(ctx, r.db, func(ctx context.Context) error {
		tx := db.Conn(ctx, r.db)
		var u User
		// The lock makes a concurrent PATCH /me wait, then find nothing to
		// write (anonymized_at IS NULL), and a concurrent join wait, then refuse.
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND anonymized_at IS NULL", id).Take(&u).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errUserNotFound
		}
		if err != nil {
			return fmt.Errorf("lock user: %w", err)
		}
		if err := tx.Exec("INSERT INTO deleted_subjects (fingerprint, deleted_at) VALUES (decode(?, 'hex'), ?)", subjectFingerprint(u.AuthSubject), at).Error; err != nil {
			return fmt.Errorf("insert deleted subject: %w", err)
		}
		// The placeholder keeps auth_subject unique and tells nothing of the subject.
		res := tx.Model(&User{}).Where("id = ?", id).Updates(map[string]any{
			"auth_subject":  gorm.Expr("'deleted|' || id::text"),
			"pseudo":        nil,
			"first_name":    nil,
			"last_name":     nil,
			"email":         nil,
			"avatar_key":    nil,
			"language":      nil,
			"anonymized_at": at,
		})
		if res.Error != nil {
			return fmt.Errorf("anonymize user: %w", res.Error)
		}
		// Recorded with the deletion, not after the commit: a process stopped
		// before Auth0 is called leaves a row the worker replays.
		if err := tx.Exec("INSERT INTO pending_auth0_deletions (auth_subject) VALUES (?)", u.AuthSubject).Error; err != nil {
			return fmt.Errorf("insert pending auth0 deletion: %w", err)
		}
		subject = u.AuthSubject
		return abandon(ctx)
	})
	if err != nil {
		return "", fmt.Errorf("delete user: %w", err)
	}
	return subject, nil
}

// PendingAuth0Deletions returns the subjects whose deletion at Auth0 is
// still to do, oldest first.
func (r *Repository) PendingAuth0Deletions(ctx context.Context) ([]string, error) {
	var subjects []string
	err := r.db.WithContext(ctx).Raw("SELECT auth_subject FROM pending_auth0_deletions ORDER BY created_at, auth_subject").Scan(&subjects).Error
	if err != nil {
		return nil, fmt.Errorf("read pending auth0 deletions: %w", err)
	}
	return subjects, nil
}

// RemovePendingAuth0Deletion removes the record of a deletion done at Auth0.
// A record already removed (by a run at the same time) is not an error.
func (r *Repository) RemovePendingAuth0Deletion(ctx context.Context, subject string) error {
	err := r.db.WithContext(ctx).Exec("DELETE FROM pending_auth0_deletions WHERE auth_subject = ?", subject).Error
	if err != nil {
		return fmt.Errorf("delete pending auth0 deletion: %w", err)
	}
	return nil
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
	if changes.RemovePseudo {
		columns["pseudo"] = nil
	}
	if changes.FirstName != nil {
		columns["first_name"] = *changes.FirstName
	}
	if changes.LastName != nil {
		columns["last_name"] = *changes.LastName
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
