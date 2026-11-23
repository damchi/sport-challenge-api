// Package admin holds the cross-module reads of the administration of the
// app (README §2.12): the figures of the dashboard, for the accounts with the
// admin permission of their access token. The admin changes are use cases of
// each module, not of this one.
package admin

import (
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/buildinfo"
)

// recentWindow is "the last 7 days" of the dashboard: 7 × 24 hours before
// the instant of the figures, the same for every figure.
const recentWindow = 7 * 24 * time.Hour

// workerTasks are the tasks of the worker, in the order of the dashboard
// (and of the enum of the contract), each listed even when it never ran.
var workerTasks = [...]string{"close-weeks", "finalize-challenges", "retry-auth0-deletions"}

// Stats are the figures of the dashboard at ComputedAt.
type Stats struct {
	ComputedAt time.Time
	Figures
	// WorkerTasks holds one entry per task of workerTasks, in that order.
	WorkerTasks []WorkerTask
	Build       buildinfo.Info
}

// Figures are the counts of the dashboard, read in one statement.
type Figures struct {
	// Deleted accounts count in none of the account figures.
	AccountsTotal  int64
	AccountsNew    int64
	AccountsActive int64
	// The statuses count the challenges not deleted, Deleted the others.
	Upcoming, Ongoing, Finished, Deleted int64
	// Activities recorded in the window, deleted ones and those of deleted
	// accounts excluded.
	Activities            int64
	PendingAuth0Deletions int64
}

// WorkerTask is the last run and the last failure of a worker task, nil when
// it has none.
type WorkerTask struct {
	Task        string
	LastRun     *WorkerRun
	LastFailure *WorkerRun
}

// WorkerRun is a run of a worker task. EndedAt and Outcome are nil while it
// goes on, or when it was stopped before it could end its row; ErrorCode is
// set for a failure only.
type WorkerRun struct {
	StartedAt time.Time
	EndedAt   *time.Time
	Outcome   *string
	ErrorCode *string
}
