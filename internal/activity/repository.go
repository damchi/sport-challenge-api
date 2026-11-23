package activity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
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

// invalidParameterValue is SQLSTATE 22023: here, a time zone PostgreSQL
// does not know (Weeks).
const invalidParameterValue = "22023"

// Repository stores the activities in PostgreSQL.
type Repository struct {
	db *gorm.DB
}

// NewRepository returns a Repository on gdb.
func NewRepository(gdb *gorm.DB) *Repository {
	return &Repository{db: gdb}
}

// Record stores a in one transaction, carried by the ctx that decide gets so
// that it reads in it. The week lock is taken shared first, before decide
// reads the clock (db.ShareWeekLock: the worker judges a week only once the
// writes checked against its lock are committed), then the author's row
// (lockUser). An activity of the same author with the same idempotency key
// is returned as is, with false, decide not being called;
// errActivityNotFound once it is deleted. Otherwise decide refuses with an
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
		if err := db.ShareWeekLock(tx); err != nil {
			return err
		}
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
			(user_id, sport_code, sport_label, started_at, duration_seconds, distance_meters, elevation_gain_meters, source, idempotency_key)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (user_id, idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING
			RETURNING id`,
			a.UserID, a.SportCode, a.SportLabel, a.StartedAt, a.DurationSeconds, a.DistanceMeters, a.ElevationGainMeters, a.Source, a.IdempotencyKey,
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
			SET sport_code = ?, sport_label = ?, started_at = ?, duration_seconds = ?, distance_meters = ?, elevation_gain_meters = ?
			WHERE id = ?`,
			a.SportCode, a.SportLabel, a.StartedAt, a.DurationSeconds, a.DistanceMeters, a.ElevationGainMeters, id,
		).Error
		if err != nil {
			return fmt.Errorf("update activity: %w", err)
		}
		out = current
		out.SportCode, out.SportLabel, out.StartedAt = a.SportCode, a.SportLabel, a.StartedAt
		out.DurationSeconds, out.DistanceMeters, out.ElevationGainMeters = a.DurationSeconds, a.DistanceMeters, a.ElevationGainMeters
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

// EraseUser deletes every activity of userID, those marked deleted
// included. It runs in the transaction of ctx (account deletion).
func (r *Repository) EraseUser(ctx context.Context, userID string) error {
	if err := db.Conn(ctx, r.db).Exec("DELETE FROM activities WHERE user_id = ?", userID).Error; err != nil {
		return fmt.Errorf("delete activities: %w", err)
	}
	return nil
}

// change runs decide then write on activity id, locked, in one transaction
// (Update, Delete), the week lock taken shared first as in Record. Only an
// activity of actorID is read, so that the row of someone else is never
// locked: theirs gives errActivityNotFound, as a missing one does.
func (r *Repository) change(ctx context.Context, actorID, id string, decide func(ctx context.Context, current Activity) error, write func(tx *gorm.DB, current Activity) error) error {
	return db.InTx(ctx, r.db, func(ctx context.Context) error {
		tx := db.Conn(ctx, r.db)
		if err := db.ShareWeekLock(tx); err != nil {
			return err
		}
		if err := lockUser(tx, actorID); err != nil {
			return err
		}
		if err := lockActivities(tx, actorID); err != nil {
			return err
		}
		var rows []Activity
		err := tx.Raw(`SELECT id, user_id, sport_code, sport_label, started_at, duration_seconds, distance_meters, elevation_gain_meters, source, idempotency_key
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

// List returns up to q.Limit activities of the users of q.UserIDs, deleted
// ones left out, started in [q.From, q.To), of sport q.SportCode when set,
// most recent first (started_at, id), after the position q.After when set.
func (r *Repository) List(ctx context.Context, q ListQuery) ([]Activity, error) {
	if len(q.UserIDs) == 0 {
		return nil, errors.New("list activities: no user")
	}
	tx := r.db.WithContext(ctx).Table("activities").
		Select("id, user_id, sport_code, sport_label, started_at, duration_seconds, distance_meters, elevation_gain_meters, source").
		Where("user_id IN ? AND deleted_at IS NULL", q.UserIDs)
	if q.From != nil {
		tx = tx.Where("started_at >= ?", *q.From)
	}
	if q.To != nil {
		tx = tx.Where("started_at < ?", *q.To)
	}
	if q.SportCode != nil {
		tx = tx.Where("sport_code = ?", *q.SportCode)
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

// DayTotals returns the totals of the activities of userID, deleted ones
// left out, started on each day of bounds: item i sums those started in
// [bounds[i], bounds[i+1]), so it has one item less than bounds (at least
// two, in increasing order).
func (r *Repository) DayTotals(ctx context.Context, userID string, bounds []time.Time) ([]Totals, error) {
	if len(bounds) < 2 {
		return nil, errors.New("day totals: fewer than two bounds")
	}
	var rows []struct {
		Day int
		Totals
	}
	// width_bucket gives 1 for [bounds[0], bounds[1]), and so on. One
	// placeholder per bound: GORM would turn a slice into a row.
	args := make([]any, 0, len(bounds)+3)
	for _, b := range bounds {
		args = append(args, b)
	}
	args = append(args, userID, bounds[0], bounds[len(bounds)-1])
	err := r.db.WithContext(ctx).Raw(`SELECT width_bucket(started_at, CAST(ARRAY[`+strings.TrimSuffix(strings.Repeat("?, ", len(bounds)), ", ")+`] AS timestamptz[])) AS day,
			count(*) AS sessions, sum(duration_seconds) AS duration_seconds, coalesce(sum(distance_meters), 0) AS distance_meters
		FROM activities
		WHERE user_id = ? AND deleted_at IS NULL AND started_at >= ? AND started_at < ?
		GROUP BY day`, args...,
	).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("day totals: %w", err)
	}
	out := make([]Totals, len(bounds)-1)
	for _, row := range rows {
		if row.Day < 1 || row.Day > len(out) {
			return nil, fmt.Errorf("day totals: bucket %d out of the days", row.Day)
		}
		out[row.Day-1] = row.Totals
	}
	return out, nil
}

// SportCounts returns the number of activities of userID of each sport,
// deleted ones left out, most frequent first, then by code, and the sum of
// the durations of those same activities, in seconds.
func (r *Repository) SportCounts(ctx context.Context, userID string) ([]SportCount, int, error) {
	var rows []struct {
		SportCount
		DurationSeconds int
	}
	err := r.db.WithContext(ctx).Raw(`SELECT sport_code, count(*) AS count, sum(duration_seconds) AS duration_seconds
		FROM activities WHERE user_id = ? AND deleted_at IS NULL
		GROUP BY sport_code ORDER BY count DESC, sport_code`, userID).Scan(&rows).Error
	if err != nil {
		return nil, 0, fmt.Errorf("sport counts: %w", err)
	}
	out := make([]SportCount, 0, len(rows))
	duration := 0
	for _, row := range rows {
		out = append(out, row.SportCount)
		duration += row.DurationSeconds
	}
	return out, duration, nil
}

// Weeks returns up to q.Limit weeks of the activities of q.UserID, with
// their totals, as WeekQuery selects them: a week is cut on the local dates
// in q.Zone, from Monday (date_trunc of the local time). A zone PostgreSQL
// does not know gives errUnknownZone.
func (r *Repository) Weeks(ctx context.Context, q WeekQuery) ([]WeekTotals, error) {
	tx := r.db.WithContext(ctx).Table("activities").
		Select(`CAST(date_trunc('week', started_at AT TIME ZONE ?) AS date) AS monday,
			count(*) AS sessions, sum(duration_seconds) AS duration_seconds, coalesce(sum(distance_meters), 0) AS distance_meters`, q.Zone).
		Where("user_id = ? AND deleted_at IS NULL", q.UserID)
	if q.SportCode != nil {
		tx = tx.Where("sport_code = ?", *q.SportCode)
	}
	if q.Before != nil {
		tx = tx.Where("started_at < ?", *q.Before)
	}
	var rows []WeekTotals
	err := tx.Group("monday").Order("monday DESC").Limit(q.Limit).Scan(&rows).Error
	var pgErr *pgconn.PgError
	switch {
	case errors.As(err, &pgErr) && pgErr.Code == invalidParameterValue:
		return nil, fmt.Errorf("list weeks: %w", errUnknownZone)
	case err != nil:
		return nil, fmt.Errorf("list weeks: %w", err)
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
	err := tx.Raw(`SELECT id, user_id, sport_code, sport_label, started_at, duration_seconds, distance_meters, elevation_gain_meters, source, idempotency_key,
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
