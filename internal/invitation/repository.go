package invitation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/damchi/sport-challenge-api/internal/platform/db"
)

// Errors of the repository.
var (
	// errInvalid means no invitation valid for the token: unknown, expired,
	// revoked, or to a deleted challenge.
	errInvalid = errors.New("invitation invalid")
	// errAccountDeleted means the account of the actor was deleted.
	errAccountDeleted = errors.New("account deleted")
)

// Repository is the only access to the invitation tables.
type Repository struct {
	db *gorm.DB
}

// NewRepository returns a Repository on gdb.
func NewRepository(gdb *gorm.DB) *Repository {
	return &Repository{db: gdb}
}

// Create inserts inv and returns it as stored, after locking its author
// (lockUser): errAccountDeleted once the account is deleted.
func (r *Repository) Create(ctx context.Context, inv Invitation) (Invitation, error) {
	err := db.InTx(ctx, r.db, func(ctx context.Context) error {
		tx := db.Conn(ctx, r.db)
		if err := lockUser(tx, inv.InvitedBy); err != nil {
			return err
		}
		// RETURNING fills the id the database sets.
		if err := tx.Clauses(clause.Returning{}).Create(&inv).Error; err != nil {
			return fmt.Errorf("insert invitation: %w", err)
		}
		return nil
	})
	if err != nil {
		return Invitation{}, fmt.Errorf("create invitation: %w", err)
	}
	return inv, nil
}

// lockUser takes the row of user id FOR SHARE, as every write that
// references a user does (modules challenge, activity): an account
// deletion, which locks it first and then erases the invitations and their
// links, makes the write wait, then refuse, so that nothing of the account
// is left behind. An anonymized or unknown user gives errAccountDeleted.
func lockUser(tx *gorm.DB, id string) error {
	var anonymized []bool
	if err := tx.Raw("SELECT anonymized_at IS NOT NULL FROM users WHERE id = ? FOR SHARE", id).Scan(&anonymized).Error; err != nil {
		return fmt.Errorf("lock user: %w", err)
	}
	if len(anonymized) == 0 || anonymized[0] {
		return errAccountDeleted
	}
	return nil
}

// linkQuery finds the invitation of @hash valid at @now and links @user to
// it, in one statement. Valid: not expired, not revoked, its challenge not
// deleted, its author's account not deleted, and its author may still invite
// (a creator, or an active participant when members may invite), the same
// rule as the membership query of module challenge. Both parts see the same
// snapshot, so a link is never made to an invitation the statement found
// invalid.
const linkQuery = `WITH valid AS (
	SELECT i.id, i.challenge_id FROM invitations i JOIN challenges c ON c.id = i.challenge_id
	WHERE i.token_hash = @hash AND i.revoked_at IS NULL AND i.expires_at > @now AND c.deleted_at IS NULL
		AND EXISTS (SELECT 1 FROM users WHERE id = i.invited_by AND anonymized_at IS NULL)
		AND (EXISTS (SELECT 1 FROM challenge_creators WHERE challenge_id = c.id AND user_id = i.invited_by)
			OR (c.members_can_invite AND EXISTS (SELECT 1 FROM participants
				WHERE challenge_id = c.id AND user_id = i.invited_by AND status = 'active')))
), linked AS (
	INSERT INTO invitation_recipients (invitation_id, user_id, created_at)
	SELECT id, @user, @now FROM valid
	ON CONFLICT (invitation_id, user_id) DO NOTHING
)
SELECT challenge_id FROM valid`

// Link links userID, at now, to the invitation of tokenHash valid at now,
// and returns its challenge; errInvalid if none is. Linking twice keeps the
// first link. userID is locked first (lockUser): errAccountDeleted once the
// account is deleted.
func (r *Repository) Link(ctx context.Context, tokenHash []byte, userID string, now time.Time) (string, error) {
	var ids []string
	err := db.InTx(ctx, r.db, func(ctx context.Context) error {
		tx := db.Conn(ctx, r.db)
		if err := lockUser(tx, userID); err != nil {
			return err
		}
		return tx.Raw(linkQuery, map[string]any{"hash": tokenHash, "user": userID, "now": now}).Scan(&ids).Error
	})
	if err != nil {
		return "", fmt.Errorf("link invitation: %w", err)
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("link invitation: %w", errInvalid)
	}
	return ids[0], nil
}

// EraseUser deletes the invitations userID created, with the links of
// everybody to them, and the links of userID to the invitations of others.
// The people who joined through them stay participants. It runs in the
// transaction of ctx (account deletion).
func (r *Repository) EraseUser(ctx context.Context, userID string) error {
	tx := db.Conn(ctx, r.db)
	err := tx.Exec(`DELETE FROM invitation_recipients
		WHERE user_id = @user OR invitation_id IN (SELECT id FROM invitations WHERE invited_by = @user)`,
		map[string]any{"user": userID}).Error
	if err != nil {
		return fmt.Errorf("delete invitation links: %w", err)
	}
	if err := tx.Exec("DELETE FROM invitations WHERE invited_by = ?", userID).Error; err != nil {
		return fmt.Errorf("delete invitations: %w", err)
	}
	return nil
}
