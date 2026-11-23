package workerrun

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/damchi/sport-challenge-api/internal/platform/db"
)

// Repository writes worker_runs. Raw SQL, always parameterized.
type Repository struct {
	db *gorm.DB
}

// NewRepository returns a Repository.
func NewRepository(gdb *gorm.DB) *Repository {
	return &Repository{db: gdb}
}

// Start inserts the row of a run that starts and returns its id.
func (r *Repository) Start(ctx context.Context, task string, startedAt time.Time) (string, error) {
	var id string
	err := db.Conn(ctx, r.db).Raw(
		`INSERT INTO worker_runs (task, started_at) VALUES (@task, @started_at) RETURNING id`,
		map[string]any{"task": task, "started_at": startedAt},
	).Scan(&id).Error
	if err != nil {
		return "", fmt.Errorf("insert worker run: %w", err)
	}
	return id, nil
}

// Finish ends the row of a run with its outcome and, on failure, its error
// code.
func (r *Repository) Finish(ctx context.Context, id string, endedAt time.Time, outcome Outcome, errorCode *string) error {
	err := db.Conn(ctx, r.db).Exec(
		`UPDATE worker_runs SET ended_at = @ended_at, outcome = @outcome, error_code = @error_code WHERE id = @id`,
		map[string]any{"id": id, "ended_at": endedAt, "outcome": string(outcome), "error_code": errorCode},
	).Error
	if err != nil {
		return fmt.Errorf("end worker run: %w", err)
	}
	return nil
}

// Trim deletes the runs of task beyond the keep most recent ones.
func (r *Repository) Trim(ctx context.Context, task string, keep int) error {
	err := db.Conn(ctx, r.db).Exec(
		`DELETE FROM worker_runs WHERE task = @task AND id NOT IN (
			SELECT id FROM worker_runs WHERE task = @task ORDER BY started_at DESC, id DESC LIMIT @keep)`,
		map[string]any{"task": task, "keep": keep},
	).Error
	if err != nil {
		return fmt.Errorf("trim worker runs: %w", err)
	}
	return nil
}
