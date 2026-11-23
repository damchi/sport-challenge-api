package closure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/scoring"
)

// Repository reads the open challenges and writes the closures. Raw SQL,
// always parameterized.
type Repository struct {
	db *gorm.DB
}

// NewRepository returns a Repository.
func NewRepository(gdb *gorm.DB) *Repository {
	return &Repository{db: gdb}
}

// AwaitActivityWrites waits for the end of the activity writes that checked
// the lock of their weeks before it was called (db.AwaitWeekWrites).
func (r *Repository) AwaitActivityWrites(ctx context.Context) error {
	return db.AwaitWeekWrites(ctx, r.db)
}

// ClosableChallenges lists the challenges not deleted and started at now,
// over or not, that still have a week to close before the last one (which is
// left to the finalization of the challenge), each with its elimination
// category (the frozen settings are in categories.params). A window holds a
// whole number of weeks; rounding the number of weeks absorbs a time change.
// Only the closures of the weeks before the last one count: the finalization
// may have closed the last week while an earlier one is still open.
func (r *Repository) ClosableChallenges(ctx context.Context, now time.Time) ([]Challenge, error) {
	var rows []struct {
		ID         string
		Timezone   string
		StartsAt   time.Time
		EndsAt     time.Time
		CategoryID string
		Sport      string
		StepMeters int
	}
	err := db.Conn(ctx, r.db).Raw(
		`SELECT c.id, c.timezone, c.starts_at, c.ends_at, cat.id AS category_id,
			COALESCE(cat.params->>'sport', '') AS sport,
			COALESCE((cat.params->>'step_meters')::int, 0) AS step_meters
		FROM challenges c
		JOIN categories cat ON cat.challenge_id = c.id AND cat.type = 'elimination'
		WHERE c.deleted_at IS NULL AND c.starts_at <= @now
			AND (SELECT count(*) FROM week_closure wc WHERE wc.challenge_id = c.id
					AND wc.week_number < round(extract(epoch FROM c.ends_at - c.starts_at) / 604800))
				< round(extract(epoch FROM c.ends_at - c.starts_at) / 604800) - 1
		ORDER BY c.starts_at, c.id`,
		map[string]any{"now": now},
	).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read closable challenges: %w", err)
	}
	out := make([]Challenge, len(rows))
	for i, row := range rows {
		out[i] = Challenge{
			ID: row.ID, Timezone: row.Timezone, StartsAt: row.StartsAt.UTC(), EndsAt: row.EndsAt.UTC(),
			CategoryID: row.CategoryID, Sport: row.Sport, StepMeters: row.StepMeters,
		}
	}
	return out, nil
}

// ClosedWeeks gives the numbers of the weeks of the challenge already closed.
func (r *Repository) ClosedWeeks(ctx context.Context, challengeID string) ([]int, error) {
	var weeks []int
	err := db.Conn(ctx, r.db).Raw(
		`SELECT week_number FROM week_closure WHERE challenge_id = @id ORDER BY week_number`,
		map[string]any{"id": challengeID},
	).Scan(&weeks).Error
	if err != nil {
		return nil, fmt.Errorf("read closed weeks: %w", err)
	}
	return weeks, nil
}

// CloseWeek records the closure of a week and its eliminations in one
// transaction. The closure goes first: a run that closed the week at the
// same time wins, and this one records nothing (false).
func (r *Repository) CloseWeek(ctx context.Context, c Challenge, week int, closedAt time.Time, eliminated []scoring.Eliminated) (bool, error) {
	var recorded bool
	err := db.WithTx(ctx, r.db, func(tx *gorm.DB) error {
		res := tx.Exec(
			`INSERT INTO week_closure (challenge_id, week_number, closed_at) VALUES (@id, @week, @closed_at)
			ON CONFLICT (challenge_id, week_number) DO NOTHING`,
			map[string]any{"id": c.ID, "week": week, "closed_at": closedAt},
		)
		if res.Error != nil {
			return fmt.Errorf("insert week closure: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return nil
		}
		if err := insertEliminations(tx, c.ID, c.CategoryID, eliminated); err != nil {
			return err
		}
		recorded = true
		return nil
	})
	return recorded, err
}

// insertEliminations records the eliminations of a week in category
// categoryID. A participant is eliminated once: an existing row is the
// earlier judgement. A participation erased since the judgement (account
// deleted) gets none.
func insertEliminations(tx *gorm.DB, challengeID, categoryID string, eliminated []scoring.Eliminated) error {
	for _, e := range eliminated {
		if err := tx.Exec(
			`INSERT INTO elimination (challenge_id, category_id, user_id, week_number, distance_meters, required_meters)
			SELECT @challenge, @category, @user, @week, @distance, @minimum
			WHERE EXISTS (SELECT 1 FROM participants WHERE challenge_id = @challenge AND user_id = @user)
			ON CONFLICT (category_id, user_id) DO NOTHING`,
			map[string]any{
				"challenge": challengeID, "category": categoryID, "user": e.UserID,
				"week": e.Week, "distance": e.DistanceMeters, "minimum": e.MinimumMeters,
			},
		).Error; err != nil {
			return fmt.Errorf("insert elimination: %w", err)
		}
	}
	return nil
}

// EndedChallenges lists the challenges not deleted whose end is reached at
// now and that have no result snapshot, each with its categories. Their last
// week may not be locked yet: the service checks it, in the time zone. The
// snapshots of a challenge are written together, so none means none.
func (r *Repository) EndedChallenges(ctx context.Context, now time.Time) ([]EndedChallenge, error) {
	out, err := r.readChallenges(ctx, `c.ends_at <= @now
		AND NOT EXISTS (SELECT 1 FROM result_snapshot rs WHERE rs.challenge_id = c.id)`, map[string]any{"now": now})
	if err != nil {
		return nil, fmt.Errorf("read ended challenges: %w", err)
	}
	return out, nil
}

// Challenge reads challenge id, not deleted, with its categories, whatever
// its status; false when there is none.
func (r *Repository) Challenge(ctx context.Context, id string) (EndedChallenge, bool, error) {
	out, err := r.readChallenges(ctx, `c.id = @id`, map[string]any{"id": id})
	if err != nil {
		return EndedChallenge{}, false, fmt.Errorf("read challenge: %w", err)
	}
	if len(out) == 0 {
		return EndedChallenge{}, false, nil
	}
	return out[0], true, nil
}

// readChallenges reads the challenges not deleted that match where (SQL on
// challenges c, a constant of the callers, never an input; parameters args), each with its categories in position
// order, the frozen settings of each read from categories.params.
func (r *Repository) readChallenges(ctx context.Context, where string, args map[string]any) ([]EndedChallenge, error) {
	var rows []struct {
		ID                string
		Timezone          string
		StartsAt          time.Time
		EndsAt            time.Time
		CategoryID        string
		Type              string
		Factors           string
		Sport             string
		StepMeters        int
		Criteria          string
		Podium            int
		MinSessionSeconds int
	}
	err := db.Conn(ctx, r.db).Raw(
		`SELECT c.id, c.timezone, c.starts_at, c.ends_at, cat.id AS category_id, cat.type,
			COALESCE(cat.params->>'factors', '{}') AS factors,
			COALESCE(cat.params->>'sport', '') AS sport,
			COALESCE((cat.params->>'step_meters')::int, 0) AS step_meters,
			COALESCE(cat.params->>'criteria', '[]') AS criteria,
			COALESCE((cat.params->>'podium')::int, 0) AS podium,
			COALESCE((cat.params->>'min_session_seconds')::int, 0) AS min_session_seconds
		FROM challenges c
		JOIN categories cat ON cat.challenge_id = c.id
		WHERE c.deleted_at IS NULL AND `+where+`
		ORDER BY c.ends_at, c.id, cat.position`,
		args,
	).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	var out []EndedChallenge
	for _, row := range rows {
		if len(out) == 0 || out[len(out)-1].ID != row.ID {
			out = append(out, EndedChallenge{ID: row.ID, Timezone: row.Timezone, StartsAt: row.StartsAt.UTC(), EndsAt: row.EndsAt.UTC()})
		}
		cat := Category{
			ID: row.CategoryID, Type: row.Type, Sport: row.Sport, StepMeters: row.StepMeters,
			Podium: row.Podium, MinSessionSeconds: row.MinSessionSeconds,
		}
		if err := json.Unmarshal([]byte(row.Factors), &cat.Factors); err != nil {
			return nil, fmt.Errorf("read factors of category %s: %w", row.CategoryID, err)
		}
		if err := json.Unmarshal([]byte(row.Criteria), &cat.Criteria); err != nil {
			return nil, fmt.Errorf("read criteria of category %s: %w", row.CategoryID, err)
		}
		last := &out[len(out)-1]
		last.Categories = append(last.Categories, cat)
	}
	return out, nil
}

// LockWeeks takes the week lock alone in the transaction ctx carries
// (db.LockWeeks), until it ends; outside of one, the lock would be released
// at once, so it is refused.
func (r *Repository) LockWeeks(ctx context.Context) error {
	tx, ok := db.Tx(ctx)
	if !ok {
		return errors.New("lock weeks: no transaction")
	}
	return db.LockWeeks(tx)
}

// WithWeeksShared runs fn in a transaction, carried by the ctx it gets,
// holding the week lock shared (db.ShareWeekLock): a recompute, which takes
// it alone, runs before or after fn, never during.
func (r *Repository) WithWeeksShared(ctx context.Context, fn func(ctx context.Context) error) error {
	return db.InTx(ctx, r.db, func(ctx context.Context) error {
		if err := db.ShareWeekLock(db.Conn(ctx, r.db)); err != nil {
			return err
		}
		return fn(ctx)
	})
}

// ClearResults erases what the worker recorded for challenge id: its
// eliminations, the closures of its weeks and its result snapshots.
func (r *Repository) ClearResults(ctx context.Context, id string) error {
	tx := db.Conn(ctx, r.db)
	for _, table := range []string{"elimination", "week_closure", "result_snapshot"} {
		// table is one of the constant names above, never an input.
		if err := tx.Exec(`DELETE FROM `+table+` WHERE challenge_id = ?`, id).Error; err != nil {
			return fmt.Errorf("clear %s: %w", table, err)
		}
	}
	return nil
}

// lockParticipantsQuery takes FOR SHARE the users of the participants of
// @id whose account is not deleted, in one global order: an account
// deletion, which locks its user row first, then waits for the snapshots to
// be written, or they wait for it. A deletion erases the participations, so
// an account deleted before is not among them.
const lockParticipantsQuery = `SELECT u.id FROM participants p JOIN users u ON u.id = p.user_id
WHERE p.challenge_id = @id AND u.anonymized_at IS NULL ORDER BY u.id FOR SHARE OF u`

// SaveSnapshots records the snapshots of a challenge, then the closure of its
// last week and the eliminations judged in it, in one transaction. A run that
// finalized the challenge at the same time wins: this one records nothing
// (false). A closure or an elimination already there is kept.
//
// The rankings were computed before the transaction: an account deleted
// since would stay in them, its erasure having found nothing to erase. So
// the users of the participants are locked first (lockParticipantsQuery): a
// deletion either commits before, and an account ranked is missing from
// them (errRankedAccountDeleted, nothing written: the results are not frozen
// yet, so the challenge is ranked again without it), or waits and erases its
// entries itself.
func (r *Repository) SaveSnapshots(ctx context.Context, challengeID string, createdAt time.Time, snapshots []Snapshot, last LastWeek) (bool, error) {
	var recorded bool
	err := db.WithTx(ctx, r.db, func(tx *gorm.DB) error {
		var locked []string
		if err := tx.Raw(lockParticipantsQuery, map[string]any{"id": challengeID}).Scan(&locked).Error; err != nil {
			return fmt.Errorf("lock participants: %w", err)
		}
		if err := checkRanked(snapshots, locked); err != nil {
			return err
		}
		for i, s := range snapshots {
			res := tx.Exec(
				`INSERT INTO result_snapshot (category_id, challenge_id, category_type, payload, created_at)
				VALUES (@category, @challenge, @type, @payload, @created_at)
				ON CONFLICT (category_id) DO NOTHING`,
				map[string]any{
					"category": s.CategoryID, "challenge": challengeID, "type": s.Type,
					"payload": string(s.Payload), "created_at": createdAt,
				},
			)
			if res.Error != nil {
				return fmt.Errorf("insert result snapshot: %w", res.Error)
			}
			// The snapshots go together: the first one taken means the others are.
			if i == 0 && res.RowsAffected == 0 {
				return nil
			}
		}
		if len(snapshots) == 0 {
			return nil
		}
		if err := tx.Exec(
			`INSERT INTO week_closure (challenge_id, week_number, closed_at) VALUES (@id, @week, @closed_at)
			ON CONFLICT (challenge_id, week_number) DO NOTHING`,
			map[string]any{"id": challengeID, "week": last.Number, "closed_at": createdAt},
		).Error; err != nil {
			return fmt.Errorf("insert last week closure: %w", err)
		}
		if err := insertEliminations(tx, challengeID, last.CategoryID, last.Eliminated); err != nil {
			return err
		}
		recorded = true
		return nil
	})
	return recorded, err
}

// checkRanked fails with errRankedAccountDeleted when a participant ranked
// in snapshots is not among the participants locked, whose account is not
// deleted.
func checkRanked(snapshots []Snapshot, locked []string) error {
	active := make(map[string]bool, len(locked))
	for _, id := range locked {
		active[id] = true
	}
	for _, s := range snapshots {
		var p payload
		if err := json.Unmarshal(s.Payload, &p); err != nil {
			return fmt.Errorf("decode snapshot of category %s: %w", s.CategoryID, err)
		}
		for _, e := range p.Entries {
			if !active[e.UserID] {
				return errRankedAccountDeleted
			}
		}
	}
	return nil
}
