package invitation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// errInvalid means no invitation valid for the token: unknown, expired,
// revoked, or to a deleted challenge.
var errInvalid = errors.New("invitation invalid")

// Repository is the only access to the invitation tables.
type Repository struct {
	db *gorm.DB
}

// NewRepository returns a Repository on gdb.
func NewRepository(gdb *gorm.DB) *Repository {
	return &Repository{db: gdb}
}

// Create inserts inv and returns it as stored.
func (r *Repository) Create(ctx context.Context, inv Invitation) (Invitation, error) {
	// RETURNING fills the id the database sets.
	if err := r.db.WithContext(ctx).Clauses(clause.Returning{}).Create(&inv).Error; err != nil {
		return Invitation{}, fmt.Errorf("insert invitation: %w", err)
	}
	return inv, nil
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
// first link.
func (r *Repository) Link(ctx context.Context, tokenHash []byte, userID string, now time.Time) (string, error) {
	var ids []string
	err := r.db.WithContext(ctx).Raw(linkQuery, map[string]any{"hash": tokenHash, "user": userID, "now": now}).Scan(&ids).Error
	if err != nil {
		return "", fmt.Errorf("link invitation: %w", err)
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("link invitation: %w", errInvalid)
	}
	return ids[0], nil
}
