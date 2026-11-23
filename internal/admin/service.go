package admin

import (
	"context"
	"fmt"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/buildinfo"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
)

// repository is what the service needs from the storage.
type repository interface {
	Figures(ctx context.Context, now, since time.Time) (Figures, error)
	LastWorkerRuns(ctx context.Context) ([]TaskRun, error)
}

// Service holds the admin reads.
type Service struct {
	repo  repository
	clock clock.Clock
	build buildinfo.Info
}

// NewService returns a Service on repo; build is the build of the running
// API, given on the dashboard.
func NewService(repo repository, clk clock.Clock, build buildinfo.Info) *Service {
	return &Service{repo: repo, clock: clk, build: build}
}

// Stats returns the figures of the dashboard, read now, never stored. Admin
// only (RequireAdmin), checked before anything is read.
func (s *Service) Stats(ctx context.Context, actor auth.Actor) (Stats, error) {
	if err := RequireAdmin(actor); err != nil {
		return Stats{}, err
	}
	now := s.clock.Now()
	figures, err := s.repo.Figures(ctx, now, now.Add(-recentWindow))
	if err != nil {
		return Stats{}, fmt.Errorf("read dashboard figures: %w", err)
	}
	runs, err := s.repo.LastWorkerRuns(ctx)
	if err != nil {
		return Stats{}, fmt.Errorf("read worker runs: %w", err)
	}
	return Stats{ComputedAt: now, Figures: figures, WorkerTasks: byTask(runs), Build: s.build}, nil
}

// byTask gives every task of workerTasks, in that order, its last run and
// last failure among runs; a task of runs not in workerTasks is left out.
func byTask(runs []TaskRun) []WorkerTask {
	out := make([]WorkerTask, len(workerTasks))
	index := make(map[string]int, len(workerTasks))
	for i, task := range workerTasks {
		out[i] = WorkerTask{Task: task}
		index[task] = i
	}
	for _, r := range runs {
		i, ok := index[r.Task]
		if !ok {
			continue
		}
		run := r.Run
		if r.Failure {
			out[i].LastFailure = &run
		} else {
			out[i].LastRun = &run
		}
	}
	return out
}
