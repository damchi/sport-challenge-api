package admin

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// Repository reads the tables of every module for the dashboard. Raw SQL,
// always parameterized; it never writes.
type Repository struct {
	db *gorm.DB
}

// NewRepository returns a Repository.
func NewRepository(gdb *gorm.DB) *Repository {
	return &Repository{db: gdb}
}

// Figures counts, in one statement (one snapshot), the accounts, the
// challenges at now, the activities recorded since since and the pending
// Auth0 deletions. A deleted account (anonymized_at) counts nowhere, its
// activities neither (they are erased with it).
func (r *Repository) Figures(ctx context.Context, now, since time.Time) (Figures, error) {
	var out Figures
	err := r.db.WithContext(ctx).Raw(`SELECT
		(SELECT count(*) FROM users WHERE anonymized_at IS NULL) AS accounts_total,
		(SELECT count(*) FROM users WHERE anonymized_at IS NULL AND created_at >= @since) AS accounts_new,
		(SELECT count(DISTINCT a.user_id) FROM activities a JOIN users u ON u.id = a.user_id
			WHERE u.anonymized_at IS NULL AND a.deleted_at IS NULL AND a.created_at >= @since) AS accounts_active,
		c.upcoming, c.ongoing, c.finished, c.deleted,
		(SELECT count(*) FROM activities a JOIN users u ON u.id = a.user_id
			WHERE u.anonymized_at IS NULL AND a.deleted_at IS NULL AND a.created_at >= @since) AS activities,
		(SELECT count(*) FROM pending_auth0_deletions) AS pending_auth0_deletions
		FROM (SELECT
			count(*) FILTER (WHERE deleted_at IS NULL AND starts_at > @now) AS upcoming,
			count(*) FILTER (WHERE deleted_at IS NULL AND starts_at <= @now AND ends_at > @now) AS ongoing,
			count(*) FILTER (WHERE deleted_at IS NULL AND ends_at <= @now) AS finished,
			count(*) FILTER (WHERE deleted_at IS NOT NULL) AS deleted
			FROM challenges) c`,
		map[string]any{"now": now, "since": since},
	).Scan(&out).Error
	if err != nil {
		return Figures{}, fmt.Errorf("count dashboard figures: %w", err)
	}
	return out, nil
}

// TaskRun is a run of a task: its last run, or its last failure when
// Failure.
type TaskRun struct {
	Task    string
	Failure bool
	Run     WorkerRun `gorm:"embedded"`
}

// LastWorkerRuns returns the last run of each task that ran, whatever its
// outcome (one still going on included), and the last failure of each task
// that failed. Both follow the order of the trimming of the worker
// (started_at, then id), on the index worker_runs_task_started_at_idx.
func (r *Repository) LastWorkerRuns(ctx context.Context) ([]TaskRun, error) {
	var out []TaskRun
	err := r.db.WithContext(ctx).Raw(`(SELECT DISTINCT ON (task) task, false AS failure, started_at, ended_at, outcome, error_code
			FROM worker_runs ORDER BY task, started_at DESC, id DESC)
		UNION ALL
		(SELECT DISTINCT ON (task) task, true AS failure, started_at, ended_at, outcome, error_code
			FROM worker_runs WHERE outcome = 'failed' ORDER BY task, started_at DESC, id DESC)`,
	).Scan(&out).Error
	if err != nil {
		return nil, fmt.Errorf("read last worker runs: %w", err)
	}
	return out, nil
}
