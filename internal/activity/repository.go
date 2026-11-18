package activity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/damchi/sport-challenge-api/internal/platform/db"
)

// errAccountDeleted means the author's account was deleted (anonymized).
var errAccountDeleted = errors.New("account deleted")

// errActivityOverlap means the activity would overlap another activity of
// its author (README §2.5).
var errActivityOverlap = errors.New("activity overlaps another")

// errActivityNotFound means no activity has this id, or a deleted one.
var errActivityNotFound = errors.New("activity not found")

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
// is, with false, decide not being called; errActivityNotFound once it is
// deleted. Otherwise decide refuses with an
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
		if err := lockActivities(tx, a.UserID); err != nil {
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
		if err := refuseOverlap(tx, a, nil); err != nil {
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

// Update replaces the fields entered of activity id with those of a, in one
// transaction carried by the ctx that decide gets. The row of actorID is
// locked first (lockUser), then the activity FOR UPDATE, so that a change
// or a deletion of the same activity committed meanwhile is seen. decide
// gets the activity as stored and refuses with an error, nothing being
// written then. The source, the external id and the idempotency key stay.
// A missing or deleted activity, or one of someone else, gives
// errActivityNotFound; an anonymized actor errAccountDeleted.
func (r *Repository) Update(ctx context.Context, actorID, id string, a Activity, decide func(ctx context.Context, current Activity) error) (Activity, error) {
	var out Activity
	err := r.change(ctx, actorID, id, decide, func(tx *gorm.DB, current Activity) error {
		// Only a new interval can create an overlap: an activity that keeps
		// its start and duration (an older one maybe recorded before this
		// rule) can still have its sport or distance changed.
		if !a.StartedAt.Equal(current.StartedAt) || a.DurationSeconds != current.DurationSeconds {
			changed := current
			changed.StartedAt, changed.DurationSeconds = a.StartedAt, a.DurationSeconds
			if err := refuseOverlap(tx, changed, &id); err != nil {
				return err
			}
		}
		err := tx.Exec(`UPDATE activities
			SET sport_code = ?, sport_label = ?, started_at = ?, duration_seconds = ?, distance_meters = ?
			WHERE id = ?`,
			a.SportCode, a.SportLabel, a.StartedAt, a.DurationSeconds, a.DistanceMeters, id,
		).Error
		if err != nil {
			return fmt.Errorf("update activity: %w", err)
		}
		out = current
		out.SportCode, out.SportLabel, out.StartedAt = a.SportCode, a.SportLabel, a.StartedAt
		out.DurationSeconds, out.DistanceMeters = a.DurationSeconds, a.DistanceMeters
		return nil
	})
	if err != nil {
		return Activity{}, fmt.Errorf("update activity %s: %w", id, err)
	}
	return out, nil
}

// Delete marks activity id deleted at at, as Update does: decide gets the
// activity as stored and refuses with an error, nothing being written then.
func (r *Repository) Delete(ctx context.Context, actorID, id string, at time.Time, decide func(ctx context.Context, current Activity) error) error {
	err := r.change(ctx, actorID, id, decide, func(tx *gorm.DB, _ Activity) error {
		if err := tx.Exec("UPDATE activities SET deleted_at = ? WHERE id = ?", at, id).Error; err != nil {
			return fmt.Errorf("mark activity deleted: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("delete activity %s: %w", id, err)
	}
	return nil
}

// change runs decide then write on activity id, locked, in one transaction
// (Update, Delete). Only an activity of actorID is read, so that the row of
// someone else is never locked: theirs gives errActivityNotFound, as a
// missing one does.
func (r *Repository) change(ctx context.Context, actorID, id string, decide func(ctx context.Context, current Activity) error, write func(tx *gorm.DB, current Activity) error) error {
	return db.InTx(ctx, r.db, func(ctx context.Context) error {
		tx := db.Conn(ctx, r.db)
		if err := lockUser(tx, actorID); err != nil {
			return err
		}
		if err := lockActivities(tx, actorID); err != nil {
			return err
		}
		var rows []Activity
		err := tx.Raw(`SELECT id, user_id, sport_code, sport_label, started_at, duration_seconds, distance_meters, source, idempotency_key
			FROM activities WHERE id = ? AND user_id = ? AND deleted_at IS NULL FOR UPDATE`, id, actorID).Scan(&rows).Error
		if err != nil {
			return fmt.Errorf("lock activity: %w", err)
		}
		if len(rows) == 0 {
			return errActivityNotFound
		}
		if err := decide(ctx, rows[0]); err != nil {
			return err
		}
		return write(tx, rows[0])
	})
}

// List returns up to q.Limit activities of q.UserID, deleted ones left out,
// started in [q.From, q.To), most recent first (started_at, id), after the
// position q.After when set.
func (r *Repository) List(ctx context.Context, q ListQuery) ([]Activity, error) {
	tx := r.db.WithContext(ctx).Table("activities").
		Select("id, user_id, sport_code, sport_label, started_at, duration_seconds, distance_meters, source").
		Where("user_id = ? AND deleted_at IS NULL", q.UserID)
	if q.From != nil {
		tx = tx.Where("started_at >= ?", *q.From)
	}
	if q.To != nil {
		tx = tx.Where("started_at < ?", *q.To)
	}
	if q.After != nil {
		tx = tx.Where("(started_at, id) < (?, ?)", q.After.StartedAt, q.After.ID)
	}
	var rows []Activity
	if err := tx.Order("started_at DESC, id DESC").Limit(q.Limit).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list activities: %w", err)
	}
	return rows, nil
}

// byKey returns the activity of userID recorded with key, and false if none.
// A deleted one gives errActivityNotFound: its key stays taken (unique
// index), and a creation sent again never brings it back.
func byKey(tx *gorm.DB, userID, key string) (Activity, bool, error) {
	var rows []struct {
		Activity
		Deleted bool
	}
	err := tx.Raw(`SELECT id, user_id, sport_code, sport_label, started_at, duration_seconds, distance_meters, source, idempotency_key,
			deleted_at IS NOT NULL AS deleted
		FROM activities WHERE user_id = ? AND idempotency_key = ?`, userID, key).Scan(&rows).Error
	if err != nil {
		return Activity{}, false, fmt.Errorf("read activity by key: %w", err)
	}
	if len(rows) == 0 {
		return Activity{}, false, nil
	}
	if rows[0].Deleted {
		return Activity{}, false, errActivityNotFound
	}
	return rows[0].Activity, true, nil
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

// lockActivities serializes the writes of the activities of userID (record,
// update, delete) until the end of the transaction, so that two of them
// never both pass the overlap check (refuseOverlap). The row lock of
// lockUser is shared and cannot do it; a transaction-scoped advisory lock on
// the user's id holds no row and is released at commit or rollback. Writes of
// different users never wait for each other (barring a hash collision, which
// only serializes them).
func lockActivities(tx *gorm.DB, userID string) error {
	if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", "activities:"+userID).Error; err != nil {
		return fmt.Errorf("lock activities: %w", err)
	}
	return nil
}

// refuseOverlap gives errActivityOverlap when a, from its start for its
// duration, overlaps another activity of its author, not deleted (except, the
// activity being replaced, when set). Intervals are [start, end): an activity
// starting exactly when another ends is the next one, not an overlap (a
// triathlon). Run under lockActivities.
func refuseOverlap(tx *gorm.DB, a Activity, except *string) error {
	end := a.StartedAt.Add(time.Duration(a.DurationSeconds) * time.Second)
	var overlap []bool
	err := tx.Raw(`SELECT EXISTS (SELECT 1 FROM activities
		WHERE user_id = @user AND deleted_at IS NULL AND (CAST(@except AS uuid) IS NULL OR id <> CAST(@except AS uuid))
			AND started_at < @end AND started_at > @earliest
			AND started_at + make_interval(secs => duration_seconds) > @start)`,
		map[string]any{
			"user": a.UserID, "except": except, "start": a.StartedAt, "end": end,
			// No activity lasts more than maxDurationSeconds: the bound lets
			// the index on (user_id, started_at) find the candidates.
			"earliest": a.StartedAt.Add(-maxDurationSeconds * time.Second),
		}).Scan(&overlap).Error
	if err != nil {
		return fmt.Errorf("check overlap: %w", err)
	}
	if len(overlap) > 0 && overlap[0] {
		return errActivityOverlap
	}
	return nil
}
