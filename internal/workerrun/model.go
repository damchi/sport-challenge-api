// Package workerrun records each run of a worker task in worker_runs: when it
// started and ended, its outcome and, on failure, a stable error code. The
// admin dashboard reads the last run and the last failure of each task
// (README §2.12). Never any personal data.
package workerrun

// Outcome is how a run ended.
type Outcome string

// Outcomes of a run. A run still going on, or killed before it could end its
// row, has none.
const (
	OutcomeSucceeded Outcome = "succeeded"
	OutcomeFailed    Outcome = "failed"
)
