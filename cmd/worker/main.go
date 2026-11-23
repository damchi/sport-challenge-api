// Command worker runs one scheduled task and exits: the same image as the
// API with another command, started by Cloud Scheduler as a Cloud Run job.
//
//	worker close-weeks
//	worker finalize-challenges
//	worker retry-auth0-deletions
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	// The time zone database, embedded: challenges are cut in their own time
	// zone, and a minimal image may have no zoneinfo.
	_ "time/tzdata"

	"github.com/damchi/sport-challenge-api/internal/closure"
	"github.com/damchi/sport-challenge-api/internal/leaderboard"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/buildinfo"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/config"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/platform/logging"
	"github.com/damchi/sport-challenge-api/internal/user"
	"github.com/damchi/sport-challenge-api/internal/workerrun"
)

// taskCloseWeeks is the name of the task that closes the weeks that ended.
const taskCloseWeeks = "close-weeks"

// taskFinalizeChallenges is the name of the task that freezes the results of
// the challenges that are over.
const taskFinalizeChallenges = "finalize-challenges"

// taskRetryAuth0Deletions is the name of the task that replays the deletions
// at Auth0 that failed.
const taskRetryAuth0Deletions = "retry-auth0-deletions"

// poolSize is the number of database connections of a worker job. A task runs
// its queries one after the other: one connection would do, the second is a
// margin. Counted in the budget of docs/deploiement.md.
const poolSize = 2

// errUsage is returned for a missing or unknown task.
var errUsage = errors.New("usage: worker close-weeks|finalize-challenges|retry-auth0-deletions")

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// workerEnv reads the environment for config.Load. The worker has no server,
// but the configuration requires a port and Cloud Run does not let a job set
// PORT (it is a reserved name), so the worker falls back to the API default.
func workerEnv(key string) string {
	value := os.Getenv(key)
	if key == "PORT" && strings.TrimSpace(value) == "" {
		return "8080"
	}
	return value
}

func run(args []string) error {
	task, err := parseTask(args)
	if err != nil {
		return err
	}
	cfg, err := config.Load(workerEnv)
	if err != nil {
		return err
	}
	logger := logging.New(os.Stdout, cfg.LogLevel, "worker")
	slog.SetDefault(logger)
	build := buildinfo.Get()
	logger.Info("worker started", slog.String("task", task),
		slog.String("version", build.Version), slog.String("commit", build.Commit))
	clk := clock.New(cfg.ClockOffset)
	if cfg.ClockOffset != 0 {
		logger.Warn("clock moved forward", slog.Duration("offset", cfg.ClockOffset))
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	pool, err := db.NewPool(ctx, cfg.DatabaseURL, poolSize)
	if err != nil {
		return err
	}
	defer pool.Close()
	gdb, err := db.NewGorm(pool, logger)
	if err != nil {
		return err
	}

	// The closure judges from the same data as the rankings.
	service := closure.NewService(closure.NewRepository(gdb), leaderboard.NewRepository(gdb), clk, logger)
	runs := workerrun.NewService(workerrun.NewRepository(gdb), clk, logger)
	// Each run is recorded for the admin dashboard, failure included.
	return runs.Run(ctx, task, func(ctx context.Context) error {
		switch task {
		case taskCloseWeeks:
			result, err := service.CloseWeeks(ctx)
			logger.Info("task done", slog.String("task", task),
				slog.Int("challenges", result.Challenges), slog.Int("weeks", result.Weeks), slog.Int("eliminations", result.Eliminations))
			return err
		case taskFinalizeChallenges:
			result, err := service.FinalizeChallenges(ctx)
			logger.Info("task done", slog.String("task", task), slog.Int("challenges", result.Challenges))
			return err
		case taskRetryAuth0Deletions:
			// Only the replay is used: no participations to abandon.
			management := auth.NewAuth0Management(cfg.Auth0MgmtDomain, cfg.Auth0MgmtClientID, cfg.Auth0MgmtClientSecret)
			result, err := user.NewService(user.NewRepository(gdb), nil, nil, management, clk, logger).RetryAuth0Deletions(ctx)
			logger.Info("task done", slog.String("task", task), slog.Int("deleted", result.Deleted), slog.Int("failed", result.Failed))
			return err
		default:
			return errUsage
		}
	})
}

// parseTask reads the name of the task: exactly one known argument.
func parseTask(args []string) (string, error) {
	if len(args) != 1 || (args[0] != taskCloseWeeks && args[0] != taskFinalizeChallenges && args[0] != taskRetryAuth0Deletions) {
		return "", errUsage
	}
	return args[0], nil
}
