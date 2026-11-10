package closure

import (
	"context"
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
		for _, e := range eliminated {
			// A participant is eliminated once: an existing row is the earlier judgement.
			if err := tx.Exec(
				`INSERT INTO elimination (challenge_id, category_id, user_id, week_number, distance_meters, required_meters)
				VALUES (@challenge, @category, @user, @week, @run, @minimum)
				ON CONFLICT (category_id, user_id) DO NOTHING`,
				map[string]any{
					"challenge": c.ID, "category": c.CategoryID, "user": e.UserID,
					"week": e.Week, "run": e.RunMeters, "minimum": e.MinimumMeters,
				},
			).Error; err != nil {
				return fmt.Errorf("insert elimination: %w", err)
			}
		}
		recorded = true
		return nil
	})
	return recorded, err
}
