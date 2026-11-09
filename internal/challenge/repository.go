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

// membershipQuery reads challenge @id unless deleted, with the membership of
// @actor. It never takes a lock: under READ COMMITTED, a statement that
// waits for a lock keeps the snapshot taken before the wait, so its
// membership subqueries would miss a participation committed meanwhile.
// Join locks the challenge in a statement of its own, then runs this query.
const membershipQuery = `SELECT c.*,
	EXISTS (SELECT 1 FROM challenge_creators WHERE challenge_id = c.id AND user_id = @actor) AS creator,
	(SELECT status FROM participants WHERE challenge_id = c.id AND user_id = @actor) AS participant
	FROM challenges c WHERE c.id = @id AND c.deleted_at IS NULL`

// readWithMembership runs membershipQuery on gdb. A missing or deleted
// challenge gives errChallengeNotFound.
func readWithMembership(gdb *gorm.DB, id, actorID string) (Challenge, Membership, error) {
	var row struct {
		Challenge
		Creator     bool
		Participant *string
	}
	err := gdb.Raw(membershipQuery, map[string]any{"id": id, "actor": actorID}).Scan(&row).Error
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

// Get returns the challenge id unless deleted, without its categories, and
// the membership of actorID in it, in one query: a missing, deleted or
// private challenge costs the same before the service decides what the
// actor may see. A missing or deleted challenge gives errChallengeNotFound.
func (r *Repository) Get(ctx context.Context, id, actorID string) (Challenge, Membership, error) {
	return readWithMembership(r.db.WithContext(ctx), id, actorID)
}

// errAlreadyParticipant means the participation to insert already exists.
var errAlreadyParticipant = errors.New("already a participant")

// withLockedMembership runs fn in a transaction on challenge id, locked
// first (FOR UPDATE) so that writes on its participations, and its deletion,
// never interleave. The membership is read after the lock, in a new
// statement, so it sees a change committed by a transaction that held the
// lock before (see membershipQuery). A missing or deleted challenge gives
// errChallengeNotFound; an error of fn rolls everything back.
func (r *Repository) withLockedMembership(ctx context.Context, id, actorID string, fn func(tx *gorm.DB, c Challenge, m Membership) error) error {
	return db.WithTx(ctx, r.db, func(tx *gorm.DB) error {
		var locked []string
		if err := tx.Raw("SELECT id FROM challenges WHERE id = ? AND deleted_at IS NULL FOR UPDATE", id).Scan(&locked).Error; err != nil {
			return fmt.Errorf("lock challenge: %w", err)
		}
		if len(locked) == 0 {
			return fmt.Errorf("lock challenge: %w", errChallengeNotFound)
		}
		c, m, err := readWithMembership(tx, id, actorID)
		if err != nil {
			return err
		}
		return fn(tx, c, m)
	})
}

// Join adds actorID to challenge id as an active participant, under the
// lock of withLockedMembership. decide gets the challenge and the membership
// and refuses with an error, nothing being written then; otherwise it
// returns the instant of the join. An existing participation gives
// errAlreadyParticipant (the lock makes it impossible, the insert stays
// safe).
func (r *Repository) Join(ctx context.Context, id, actorID string, decide func(Challenge, Membership) (time.Time, error)) error {
	err := r.withLockedMembership(ctx, id, actorID, func(tx *gorm.DB, c Challenge, m Membership) error {
		joinedAt, err := decide(c, m)
		if err != nil {
			return err
		}
		p := participant{ChallengeID: id, UserID: actorID, Status: ParticipantActive, JoinedAt: joinedAt}
		res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&p)
		if res.Error != nil {
			return fmt.Errorf("insert participant: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return errAlreadyParticipant
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("join challenge: %w", err)
	}
	return nil
}

// Abandon marks the active participation of actorID in challenge id as
// abandoned, under the lock of withLockedMembership. decide gets the
// challenge and the membership and refuses with an error, or answers that
// nothing is to write (already abandoned), or gives the instant of the
// abandon.
func (r *Repository) Abandon(ctx context.Context, id, actorID string, decide func(Challenge, Membership) (noop bool, at time.Time, err error)) error {
	err := r.withLockedMembership(ctx, id, actorID, func(tx *gorm.DB, c Challenge, m Membership) error {
		noop, at, err := decide(c, m)
		if err != nil || noop {
			return err
		}
		return abandonParticipation(tx, id, actorID, at)
	})
	if err != nil {
		return fmt.Errorf("abandon challenge: %w", err)
	}
	return nil
}

// Exclude marks the active participation of targetID in challenge id as
// abandoned, on behalf of actorID, under the lock of withLockedMembership.
// decide gets the challenge, the membership of the actor and the one of the
// target (read after the lock too), and refuses with an error, or answers
// that nothing is to write, or gives the instant of the exclusion.
func (r *Repository) Exclude(ctx context.Context, id, actorID, targetID string, decide func(c Challenge, actor, target Membership) (noop bool, at time.Time, err error)) error {
	err := r.withLockedMembership(ctx, id, actorID, func(tx *gorm.DB, c Challenge, actor Membership) error {
		_, target, err := readWithMembership(tx, id, targetID)
		if err != nil {
			return err
		}
		noop, at, err := decide(c, actor, target)
		if err != nil || noop {
			return err
		}
		return abandonParticipation(tx, id, targetID, at)
	})
	if err != nil {
		return fmt.Errorf("exclude participant: %w", err)
	}
	return nil
}

// AddCreator makes targetID a creator of challenge id, on behalf of actorID,
// under the lock of withLockedMembership: an exclusion of the same target
// waits for it, then sees the new creator. decide gets the challenge, the
// membership of the actor and the one of the target (read after the lock),
// and refuses with an error, or answers that nothing is to write.
func (r *Repository) AddCreator(ctx context.Context, id, actorID, targetID string, decide func(c Challenge, actor, target Membership) (noop bool, err error)) error {
	err := r.withLockedMembership(ctx, id, actorID, func(tx *gorm.DB, c Challenge, actor Membership) error {
		_, target, err := readWithMembership(tx, id, targetID)
		if err != nil {
			return err
		}
		noop, err := decide(c, actor, target)
		if err != nil || noop {
			return err
		}
		// The lock makes a duplicate impossible; DO NOTHING keeps it harmless.
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&creator{ChallengeID: id, UserID: targetID}).Error; err != nil {
			return fmt.Errorf("insert creator: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("add creator: %w", err)
	}
	return nil
}

// abandonParticipation turns the active participation of userID in
// challenge id into an abandon at the given instant: by the user's choice,
// or by an exclusion, both shown alike.
func abandonParticipation(tx *gorm.DB, id, userID string, at time.Time) error {
	res := tx.Model(&participant{}).
		Where("challenge_id = ? AND user_id = ? AND status = ?", id, userID, ParticipantActive).
		Updates(map[string]any{"status": ParticipantAbandoned, "abandoned_at": at})
	if res.Error != nil {
		return fmt.Errorf("update participant: %w", res.Error)
	}
	// The lock makes it impossible: a silent success would hide a bug.
	if res.RowsAffected == 0 {
		return errors.New("update participant: no active participation")
	}
	return nil
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
