package workerrun

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
)

// keptRuns is the number of most recent runs kept for each task, besides its
// last failure.
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
	repo   repository
	clock  clock.Clock
	logger *slog.Logger
}

// NewService returns a Service.
func NewService(repo repository, c clock.Clock, logger *slog.Logger) *Service {
	return &Service{repo: repo, clock: c, logger: logger}
}

// Run records a run of task around fn: a row when it starts, ended with its
// outcome when fn returns, failure included; then only the most recent runs
// of the task are kept, with its last failure. The record only observes: a failure to write it is
// logged, never stops fn nor changes its result, which Run returns.
func (s *Service) Run(ctx context.Context, task string, fn func(context.Context) error) error {
	id, err := s.repo.Start(ctx, task, s.clock.Now())
	if err != nil {
		s.logger.Error("record the start of a run", slog.String("task", task), slog.String("error", err.Error()))
		return fn(ctx)
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
		s.logger.Error("record the end of a run", slog.String("task", task), slog.String("error", err.Error()))
	} else if err := s.repo.Trim(ctx, task, keptRuns); err != nil {
		s.logger.Error("trim the runs", slog.String("task", task), slog.String("error", err.Error()))
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
