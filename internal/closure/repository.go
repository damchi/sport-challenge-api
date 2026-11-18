package closure

import (
	"context"
	"encoding/json"
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

// ClosableChallenges lists the challenges not deleted and started at now,
// over or not, that still have a week to close before the last one (which is
// left to the finalization of the challenge), each with its run elimination
// category (the frozen settings are in categories.params). A window holds a
// whole number of weeks; rounding the number of weeks absorbs a time change.
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
		JOIN categories cat ON cat.challenge_id = c.id AND cat.type = 'run_elimination'
		WHERE c.deleted_at IS NULL AND c.starts_at <= @now
			AND (SELECT count(*) FROM week_closure wc WHERE wc.challenge_id = c.id)
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
// earlier judgement.
func insertEliminations(tx *gorm.DB, challengeID, categoryID string, eliminated []scoring.Eliminated) error {
	for _, e := range eliminated {
		if err := tx.Exec(
			`INSERT INTO elimination (challenge_id, category_id, user_id, week_number, distance_meters, required_meters)
			VALUES (@challenge, @category, @user, @week, @run, @minimum)
			ON CONFLICT (category_id, user_id) DO NOTHING`,
			map[string]any{
				"challenge": challengeID, "category": categoryID, "user": e.UserID,
				"week": e.Week, "run": e.RunMeters, "minimum": e.MinimumMeters,
			},
		).Error; err != nil {
			return fmt.Errorf("insert elimination: %w", err)
		}
	}
	return nil
}

// EndedChallenges lists the challenges not deleted whose end is reached at
// now and that have no result snapshot, each with its categories. The
// snapshots of a challenge are written together, so none means none.
func (r *Repository) EndedChallenges(ctx context.Context, now time.Time) ([]EndedChallenge, error) {
	var rows []struct {
		ID         string
		Timezone   string
		StartsAt   time.Time
		EndsAt     time.Time
		CategoryID string
		Type       string
		Factors    string
		Sport      string
		StepMeters int
	}
	err := db.Conn(ctx, r.db).Raw(
		`SELECT c.id, c.timezone, c.starts_at, c.ends_at, cat.id AS category_id, cat.type,
			COALESCE(cat.params->>'factors', '{}') AS factors,
			COALESCE(cat.params->>'sport', '') AS sport,
			COALESCE((cat.params->>'step_meters')::int, 0) AS step_meters
		FROM challenges c
		JOIN categories cat ON cat.challenge_id = c.id
		WHERE c.deleted_at IS NULL AND c.ends_at <= @now
			AND NOT EXISTS (SELECT 1 FROM result_snapshot rs WHERE rs.challenge_id = c.id)
		ORDER BY c.ends_at, c.id, cat.position`,
		map[string]any{"now": now},
	).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read ended challenges: %w", err)
	}
	var out []EndedChallenge
	for _, row := range rows {
		if len(out) == 0 || out[len(out)-1].ID != row.ID {
			out = append(out, EndedChallenge{ID: row.ID, Timezone: row.Timezone, StartsAt: row.StartsAt.UTC(), EndsAt: row.EndsAt.UTC()})
		}
		cat := Category{ID: row.CategoryID, Type: row.Type, Sport: row.Sport, StepMeters: row.StepMeters}
		if err := json.Unmarshal([]byte(row.Factors), &cat.Factors); err != nil {
			return nil, fmt.Errorf("read factors of category %s: %w", row.CategoryID, err)
		}
		last := &out[len(out)-1]
		last.Categories = append(last.Categories, cat)
	}
	return out, nil
}

// SaveSnapshots records the snapshots of a challenge, then the closure of its
// last week and the eliminations judged in it, in one transaction. A run that
// finalized the challenge at the same time wins: this one records nothing
// (false). A closure or an elimination already there is kept.
func (r *Repository) SaveSnapshots(ctx context.Context, challengeID string, createdAt time.Time, snapshots []Snapshot, last LastWeek) (bool, error) {
	var recorded bool
	err := db.WithTx(ctx, r.db, func(tx *gorm.DB) error {
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
