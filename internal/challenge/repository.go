package challenge

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// listColumns are the columns of a Summary; participant_count counts every
// participation, abandoned included.
const listColumns = `
SELECT c.id, c.title, c.visibility, c.timezone, c.starts_at, c.ends_at,
       (SELECT count(*) FROM participants p WHERE p.challenge_id = c.id) AS participant_count,
       me.status AS my_status
FROM challenges c
LEFT JOIN participants me ON me.challenge_id = c.id AND me.user_id = @actor
WHERE c.deleted_at IS NULL`

// List returns at most q.Limit challenges of q, filtered in SQL: a
// deleted challenge never appears, and the public scope holds public
// challenges only. Finished challenges come most recent first, the others
// soonest first; the key (starts_at, id) makes the order total.
func (r *Repository) List(ctx context.Context, q ListQuery) ([]Summary, error) {
	var sql strings.Builder
	sql.WriteString(listColumns)
	args := map[string]any{"actor": q.ActorID, "now": q.Now, "limit": q.Limit}

	switch q.Scope {
	case ScopeMine:
		// The participants (user_id) index: every creator is a participant.
		sql.WriteString(" AND me.user_id IS NOT NULL")
	default:
		sql.WriteString(" AND c.visibility = 'public'")
	}
	switch q.Status {
	case StatusUpcoming:
		sql.WriteString(" AND c.starts_at > @now")
	case StatusOngoing:
		sql.WriteString(" AND c.starts_at <= @now AND c.ends_at > @now")
	case StatusFinished:
		sql.WriteString(" AND c.ends_at <= @now")
	}

	descending := q.Status == StatusFinished
	if q.After != nil {
		args["after_starts"], args["after_id"] = q.After.StartsAt, q.After.ID
		if descending {
			sql.WriteString(" AND (c.starts_at, c.id) < (@after_starts, @after_id)")
		} else {
			sql.WriteString(" AND (c.starts_at, c.id) > (@after_starts, @after_id)")
		}
	}
	if descending {
		sql.WriteString(" ORDER BY c.starts_at DESC, c.id DESC")
	} else {
		sql.WriteString(" ORDER BY c.starts_at, c.id")
	}
	sql.WriteString(" LIMIT @limit")

	var items []Summary
	if err := r.db.WithContext(ctx).Raw(sql.String(), args).Scan(&items).Error; err != nil {
		return nil, fmt.Errorf("list challenges: %w", err)
	}
	return items, nil
}

// errChallengeNotFound means no challenge, or a deleted one.
var errChallengeNotFound = errors.New("challenge not found")

// Get returns the challenge id unless deleted, without its categories, and
// the membership of actorID in it, in one query: a missing, deleted or
// private challenge costs the same before the service decides what the
// actor may see. A missing or deleted challenge gives errChallengeNotFound.
func (r *Repository) Get(ctx context.Context, id, actorID string) (Challenge, Membership, error) {
	var row struct {
		Challenge
		Creator     bool
		Participant *string
	}
	err := r.db.WithContext(ctx).Raw(`SELECT c.*,
		EXISTS (SELECT 1 FROM challenge_creators WHERE challenge_id = c.id AND user_id = @actor) AS creator,
		(SELECT status FROM participants WHERE challenge_id = c.id AND user_id = @actor) AS participant
		FROM challenges c WHERE c.id = @id AND c.deleted_at IS NULL`,
		map[string]any{"id": id, "actor": actorID}).Scan(&row).Error
	if err != nil {
		return Challenge{}, Membership{}, fmt.Errorf("read challenge: %w", err)
	}
	if row.ID == "" {
		return Challenge{}, Membership{}, fmt.Errorf("read challenge: %w", errChallengeNotFound)
	}
	m := Membership{Creator: row.Creator}
	if row.Participant != nil {
		m.Participant = ParticipantStatus(*row.Participant)
	}
	// Invitations come with tickets 065 and 066: nobody is invited yet.
	return row.Challenge, m, nil
}

// Categories returns the categories of challenge id, in position order.
func (r *Repository) Categories(ctx context.Context, id string) ([]Category, error) {
	var cats []Category
	if err := r.db.WithContext(ctx).Where("challenge_id = ?", id).Order("position").Find(&cats).Error; err != nil {
		return nil, fmt.Errorf("read categories: %w", err)
	}
	return cats, nil
}

// UpdateText writes the changed title and description of challenge id
// (an empty description is stored as none). A deleted challenge is never
// written and gives errChallengeNotFound.
func (r *Repository) UpdateText(ctx context.Context, id string, changes TextChanges) error {
	columns := map[string]any{}
	if changes.Title != nil {
		columns["title"] = *changes.Title
	}
	if d := changes.Description; d != nil {
		if *d == "" {
			columns["description"] = nil
		} else {
			columns["description"] = *d
		}
	}
	if len(columns) == 0 {
		return nil
	}
	res := r.db.WithContext(ctx).Model(&Challenge{}).Where("id = ? AND deleted_at IS NULL", id).Updates(columns)
	if res.Error != nil {
		return fmt.Errorf("update challenge: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("update challenge: %w", errChallengeNotFound)
	}
	return nil
}
