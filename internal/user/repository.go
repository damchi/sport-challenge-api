package user

import (
	"context"
	"errors"
	"fmt"

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

// Pseudos returns the pseudo of each user of ids that has one. An anonymized
// user is left out, like an unknown one: its pseudo is never served.
func (r *Repository) Pseudos(ctx context.Context, ids []string) (map[string]string, error) {
	var rows []struct {
		ID     string
		Pseudo string
	}
	err := r.db.WithContext(ctx).Model(&User{}).Select("id, pseudo").
		Where("id IN ? AND anonymized_at IS NULL AND pseudo IS NOT NULL", ids).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read pseudos: %w", err)
	}
	pseudos := make(map[string]string, len(rows))
	for _, row := range rows {
		pseudos[row.ID] = row.Pseudo
	}
	return pseudos, nil
}

// Delete anonymizes the account of user id and makes its deletion final, in
// one transaction that abandon joins (ctx carries it): the row is locked,
// the fingerprint of its subject is stored, the subject is replaced by a
// placeholder and the pseudo and avatar are erased; then abandon runs. It
// returns the former subject, for the deletion at Auth0. An unknown or
// already anonymized user gives errUserNotFound; an error of abandon rolls
// everything back.
func (r *Repository) Delete(ctx context.Context, id string, abandon func(ctx context.Context) error) (string, error) {
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
		if err := tx.Exec("INSERT INTO deleted_subjects (fingerprint) VALUES (decode(?, 'hex'))", subjectFingerprint(u.AuthSubject)).Error; err != nil {
			return fmt.Errorf("insert deleted subject: %w", err)
		}
		// The placeholder keeps auth_subject unique and tells nothing of the subject.
		res := tx.Model(&User{}).Where("id = ?", id).Updates(map[string]any{
			"auth_subject":  gorm.Expr("'deleted|' || id::text"),
			"pseudo":        nil,
			"avatar_key":    nil,
			"anonymized_at": gorm.Expr("now()"),
		})
		if res.Error != nil {
			return fmt.Errorf("anonymize user: %w", res.Error)
		}
		subject = u.AuthSubject
		return abandon(ctx)
	})
	if err != nil {
		return "", fmt.Errorf("delete user: %w", err)
	}
	return subject, nil
}

// AddPendingAuth0Deletion records a deletion at Auth0 that failed, for the
// worker to replay it (task retry-auth0-deletions). Recording it twice
// keeps one row.
func (r *Repository) AddPendingAuth0Deletion(ctx context.Context, subject string) error {
	err := r.db.WithContext(ctx).Exec("INSERT INTO pending_auth0_deletions (auth_subject) VALUES (?) ON CONFLICT DO NOTHING", subject).Error
	if err != nil {
		return fmt.Errorf("insert pending auth0 deletion: %w", err)
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
