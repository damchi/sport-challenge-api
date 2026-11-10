package activity

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/damchi/sport-challenge-api/internal/platform/db"
)

// errAccountDeleted means the author's account was deleted (anonymized).
var errAccountDeleted = errors.New("account deleted")

// Repository stores the activities in PostgreSQL.
type Repository struct {
	db *gorm.DB
}

// NewRepository returns a Repository on gdb.
func NewRepository(gdb *gorm.DB) *Repository {
	return &Repository{db: gdb}
}

// Record stores a in one transaction, carried by the ctx that decide gets so
// that it reads in it. The author's row is locked first (lockUser). An
// activity of the same author with the same idempotency key is returned as
// is, with false, decide not being called. Otherwise decide refuses with an
// error, nothing being written then, or a is inserted and returned with its
// id and true. An anonymized account gives errAccountDeleted. The
// participations decide reads are not locked: an abandon committed meanwhile
// only changes where the activity counts, which leaderboards compute again
// from the raw activities.
func (r *Repository) Record(ctx context.Context, a Activity, decide func(ctx context.Context) error) (Activity, bool, error) {
	var out Activity
	var created bool
	err := db.InTx(ctx, r.db, func(ctx context.Context) error {
		tx := db.Conn(ctx, r.db)
		if err := lockUser(tx, a.UserID); err != nil {
			return err
		}
		if a.IdempotencyKey != nil {
			found, ok, err := byKey(tx, a.UserID, *a.IdempotencyKey)
			if err != nil || ok {
				out = found
				return err
			}
		}
		if err := decide(ctx); err != nil {
			return err
		}
		var ids []string
		err := tx.Raw(`INSERT INTO activities
			(user_id, sport_code, sport_label, started_at, duration_seconds, distance_meters, source, idempotency_key)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (user_id, idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING
			RETURNING id`,
			a.UserID, a.SportCode, a.SportLabel, a.StartedAt, a.DurationSeconds, a.DistanceMeters, a.Source, a.IdempotencyKey,
		).Scan(&ids).Error
		if err != nil {
			return fmt.Errorf("insert activity: %w", err)
		}
		if len(ids) == 0 {
			if a.IdempotencyKey == nil {
				return errors.New("insert activity: conflict without an idempotency key")
			}
			// A request with the same key committed meanwhile: the insert
			// waited for it, this new statement sees its activity.
			found, ok, err := byKey(tx, a.UserID, *a.IdempotencyKey)
			if err == nil && !ok {
				err = errors.New("insert activity: key conflict without an activity")
			}
			out = found
			return err
		}
		out, created = a, true
		out.ID = ids[0]
		return nil
	})
	if err != nil {
		return Activity{}, false, fmt.Errorf("record activity: %w", err)
	}
	return out, created, nil
}

// byKey returns the activity of userID recorded with key, and false if none.
func byKey(tx *gorm.DB, userID, key string) (Activity, bool, error) {
	var rows []Activity
	err := tx.Raw(`SELECT id, user_id, sport_code, sport_label, started_at, duration_seconds, distance_meters, source, idempotency_key
		FROM activities WHERE user_id = ? AND idempotency_key = ?`, userID, key).Scan(&rows).Error
	if err != nil {
		return Activity{}, false, fmt.Errorf("read activity by key: %w", err)
	}
	if len(rows) == 0 {
		return Activity{}, false, nil
	}
	return rows[0], true, nil
}

// lockUser takes the row of user id FOR SHARE, as every write that
// references a user does (module challenge): an account deletion, which
// locks it first, then makes the recording wait, then refuse. An anonymized
// or unknown user gives errAccountDeleted.
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
