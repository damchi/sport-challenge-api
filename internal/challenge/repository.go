package challenge

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/damchi/sport-challenge-api/internal/platform/db"
)

// Repository is the only access to the challenge tables.
type Repository struct {
	db *gorm.DB
}

// NewRepository returns a Repository on gdb.
func NewRepository(gdb *gorm.DB) *Repository {
	return &Repository{db: gdb}
}

// Create inserts c, its creator c.CreatedBy, the creator's participation
// (active, joined at joinedAt) and c.Categories in one transaction, and
// returns the challenge as stored.
func (r *Repository) Create(ctx context.Context, c Challenge, joinedAt time.Time) (Challenge, error) {
	err := db.WithTx(ctx, r.db, func(tx *gorm.DB) error {
		// RETURNING fills the columns the database sets (id, timestamps).
		if err := tx.Clauses(clause.Returning{}).Create(&c).Error; err != nil {
			return fmt.Errorf("insert challenge: %w", err)
		}
		if err := tx.Create(&creator{ChallengeID: c.ID, UserID: c.CreatedBy}).Error; err != nil {
			return fmt.Errorf("insert creator: %w", err)
		}
		p := participant{ChallengeID: c.ID, UserID: c.CreatedBy, Status: ParticipantActive, JoinedAt: joinedAt}
		if err := tx.Create(&p).Error; err != nil {
			return fmt.Errorf("insert participant: %w", err)
		}
		for i := range c.Categories {
			c.Categories[i].ChallengeID = c.ID
		}
		if err := tx.Clauses(clause.Returning{}).Create(&c.Categories).Error; err != nil {
			return fmt.Errorf("insert categories: %w", err)
		}
		return nil
	})
	if err != nil {
		return Challenge{}, fmt.Errorf("create challenge: %w", err)
	}
	return c, nil
}
