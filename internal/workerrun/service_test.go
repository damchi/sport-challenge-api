package workerrun

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
)

// discard drops the logs of the tests.
var discard = slog.New(slog.DiscardHandler)

// fakeRepo records the calls of the service.
type fakeRepo struct {
	startErr, finishErr, trimErr error

	started   bool
	startedAt time.Time
	finished  bool
	endedAt   time.Time
	outcome   Outcome
	code      *string
	finishCtx error // ctx.Err() seen by Finish
	trimmed   int
}

func (f *fakeRepo) Start(_ context.Context, _ string, startedAt time.Time) (string, error) {
	if f.startErr != nil {
		return "", f.startErr
	}
	f.started, f.startedAt = true, startedAt
	return "run-1", nil
}

func (f *fakeRepo) Finish(ctx context.Context, _ string, endedAt time.Time, outcome Outcome, code *string) error {
	f.finished, f.endedAt, f.outcome, f.code, f.finishCtx = true, endedAt, outcome, code, ctx.Err()
	return f.finishErr
}

func (f *fakeRepo) Trim(_ context.Context, _ string, keep int) error {
	f.trimmed = keep
	return f.trimErr
}

func TestRun(t *testing.T) {
	start := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	errDB := errors.New("connection refused")
	errTask := errors.New("close weeks: boom")
	notFound := apperr.New(apperr.CodeNotFound, nil)

	tests := []struct {
		name        string
		repo        fakeRepo
		fnErr       error
		wantRan     bool
		wantOutcome Outcome
		wantCode    string // "" for none
		wantErrs    []error
	}{
		{name: "success", wantRan: true, wantOutcome: OutcomeSucceeded},
		{
			name: "plain failure is internal_error", fnErr: errTask, wantRan: true,
			wantOutcome: OutcomeFailed, wantCode: "internal_error", wantErrs: []error{errTask},
		},
		{
			name: "wrapped application error keeps its code", fnErr: fmt.Errorf("task: %w", notFound), wantRan: true,
			wantOutcome: OutcomeFailed, wantCode: "not_found", wantErrs: []error{notFound},
		},
		{name: "start not recorded: the task runs all the same", repo: fakeRepo{startErr: errDB}, wantRan: true},
		{
			name: "start not recorded: the failure of the task is returned", repo: fakeRepo{startErr: errDB}, fnErr: errTask,
			wantRan: true, wantErrs: []error{errTask},
		},
		{
			name: "end not recorded: the result of the task is kept", repo: fakeRepo{finishErr: errDB}, fnErr: errTask,
			wantRan: true, wantOutcome: OutcomeFailed, wantCode: "internal_error", wantErrs: []error{errTask},
		},
		{name: "trim failed after a success: still a success", repo: fakeRepo{trimErr: errDB}, wantRan: true, wantOutcome: OutcomeSucceeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := tt.repo
			clk := clock.NewFake(start)
			ran := false
			err := NewService(&repo, clk, discard).Run(t.Context(), "close-weeks", func(context.Context) error {
				ran = true
				clk.Advance(time.Minute)
				return tt.fnErr
			})

			for _, want := range tt.wantErrs {
				if !errors.Is(err, want) {
					t.Errorf("Run() = %v; want it to wrap %v", err, want)
				}
			}
			if len(tt.wantErrs) == 0 && err != nil {
				t.Errorf("Run() = %v; want nil", err)
			}
			if errors.Is(err, errDB) {
				t.Errorf("Run() = %v; a recording failure must not reach the result", err)
			}
			if ran != tt.wantRan {
				t.Fatalf("task ran = %v; want %v", ran, tt.wantRan)
			}
			if tt.repo.startErr != nil {
				if repo.finished || repo.trimmed != 0 {
					t.Errorf("a run never recorded was ended or trimmed")
				}
				return
			}
			if !repo.startedAt.Equal(start) || !repo.endedAt.Equal(start.Add(time.Minute)) {
				t.Errorf("run from %v to %v; want %v to %v", repo.startedAt, repo.endedAt, start, start.Add(time.Minute))
			}
			if repo.outcome != tt.wantOutcome {
				t.Errorf("outcome = %q; want %q", repo.outcome, tt.wantOutcome)
			}
			gotCode := ""
			if repo.code != nil {
				gotCode = *repo.code
			}
			if gotCode != tt.wantCode {
				t.Errorf("error code = %q; want %q", gotCode, tt.wantCode)
			}
			if tt.repo.finishErr == nil && repo.trimmed != keptRuns {
				t.Errorf("trimmed to %d; want %d", repo.trimmed, keptRuns)
			}
		})
	}
}

// A job stopped by SIGTERM cancels the context of the task: its row is ended
// anyway, as a failure.
func TestRunEndsTheRowOfACancelledTask(t *testing.T) {
	repo := &fakeRepo{}
	ctx, cancel := context.WithCancel(t.Context())
	err := NewService(repo, clock.NewFake(time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)), discard).
		Run(ctx, "finalize-challenges", func(ctx context.Context) error {
			cancel()
			return ctx.Err()
		})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() = %v; want context.Canceled", err)
	}
	if !repo.finished || repo.finishCtx != nil || repo.outcome != OutcomeFailed {
		t.Fatalf("finished = %v with ctx error %v, outcome %q; want ended as failed with a live context",
			repo.finished, repo.finishCtx, repo.outcome)
	}
}
