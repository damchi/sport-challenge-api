package workerrun

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
)

// keptRuns is the number of most recent runs kept for each task.
const keptRuns = 500

// finishTimeout bounds the end of the recording, which runs even when the
// context of the task was cancelled (SIGTERM of the job).
const finishTimeout = 10 * time.Second

// repository writes the runs; implemented by Repository.
type repository interface {
	Start(ctx context.Context, task string, startedAt time.Time) (string, error)
	Finish(ctx context.Context, id string, endedAt time.Time, outcome Outcome, errorCode *string) error
	Trim(ctx context.Context, task string, keep int) error
}

// Service records the runs of the worker tasks.
type Service struct {
	repo  repository
	clock clock.Clock
}

// NewService returns a Service.
func NewService(repo repository, c clock.Clock) *Service {
	return &Service{repo: repo, clock: c}
}

// Run records a run of task around fn: a row when it starts, ended with its
// outcome when fn returns, failure included; then only the most recent runs
// of the task are kept. fn does not run if its start cannot be recorded. The
// error of fn is returned, joined with the one of the recording if any.
func (s *Service) Run(ctx context.Context, task string, fn func(context.Context) error) error {
	id, err := s.repo.Start(ctx, task, s.clock.Now())
	if err != nil {
		return fmt.Errorf("record the start of %s: %w", task, err)
	}
	runErr := fn(ctx)

	// The row is ended even if the job is being stopped.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finishTimeout)
	defer cancel()
	outcome, code := OutcomeSucceeded, (*string)(nil)
	if runErr != nil {
		outcome, code = OutcomeFailed, errorCode(runErr)
	}
	if err := s.repo.Finish(ctx, id, s.clock.Now(), outcome, code); err != nil {
		return errors.Join(runErr, fmt.Errorf("record the end of %s: %w", task, err))
	}
	if err := s.repo.Trim(ctx, task, keptRuns); err != nil {
		return errors.Join(runErr, fmt.Errorf("trim the runs of %s: %w", task, err))
	}
	return runErr
}

// errorCode is the stable code of err: its application code, internal_error
// otherwise. Never the message, which may carry personal data.
func errorCode(err error) *string {
	code := string(apperr.CodeInternal)
	if appErr, ok := errors.AsType[*apperr.Error](err); ok {
		code = string(appErr.Code)
	}
	return &code
}
