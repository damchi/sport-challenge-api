// Command worker runs one scheduled task and exits: the same image as the
// API with another command, started by Cloud Scheduler as a Cloud Run job.
//
//	worker close-weeks
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	// The time zone database, embedded: challenges are cut in their own time
	// zone, and a minimal image may have no zoneinfo.
	_ "time/tzdata"

	"github.com/damchi/sport-challenge-api/internal/closure"
	"github.com/damchi/sport-challenge-api/internal/leaderboard"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/config"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/platform/logging"
)

// taskCloseWeeks is the name of the task that closes the weeks that ended.
const taskCloseWeeks = "close-weeks"

// errUsage is returned for a missing or unknown task.
var errUsage = errors.New("usage: worker close-weeks")

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	task, err := parseTask(args)
	if err != nil {
		return err
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	logger := logging.New(os.Stdout, cfg.LogLevel)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	gdb, err := db.NewGorm(pool, logger)
	if err != nil {
		return err
	}

	switch task {
	case taskCloseWeeks:
		// The closure judges the weeks from the same data as the rankings.
		service := closure.NewService(closure.NewRepository(gdb), leaderboard.NewRepository(gdb), clock.Real{}, logger)
		result, err := service.CloseWeeks(ctx)
		logger.Info("task done", slog.String("task", task),
			slog.Int("challenges", result.Challenges), slog.Int("weeks", result.Weeks), slog.Int("eliminations", result.Eliminations))
		return err
	default:
		return errUsage
	}
}

// parseTask reads the name of the task: exactly one known argument.
func parseTask(args []string) (string, error) {
	if len(args) != 1 || args[0] != taskCloseWeeks {
		return "", errUsage
	}
	return args[0], nil
}
