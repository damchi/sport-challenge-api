// Command migrate applies the SQL migrations embedded in the binary: the same
// image as the API, started by a Cloud Run job before each deployment (never
// by the API itself).
//
//	migrate up           apply every pending migration
//	migrate down <n>     roll back the last n migrations (3 at most)
//	migrate force <v>    set the version after a failed migration, by hand
//
// A database left "dirty" by a failed migration stops up and down with an
// error: the version is never forced automatically.
package main

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/golang-migrate/migrate/v4"
	pgxdriver "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver

	"github.com/damchi/sport-challenge-api/internal/platform/logging"
	"github.com/damchi/sport-challenge-api/migrations"
)

// errUsage is returned for a missing, unknown or malformed command.
var errUsage = errors.New("usage: migrate up | migrate down <n> | migrate force <version>")

// errDatabaseURL is returned when DATABASE_URL is not set.
var errDatabaseURL = errors.New("DATABASE_URL is required")

// maxDown bounds migrate down: rolling the whole schema back (and its data)
// is never one command away, it is a restore from a backup.
const maxDown = 3

// errDirty is returned when a previous migration failed halfway.
var errDirty = errors.New("database is dirty")

// command is a parsed command line: n is the number of migrations for down
// and the version for force.
type command struct {
	name string
	n    int
}

func main() {
	if err := run(os.Args[1:], os.Getenv); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run reads only DATABASE_URL: the migrations need no other setting.
func run(args []string, getenv func(string) string) error {
	cmd, err := parseCommand(args)
	if err != nil {
		return err
	}
	url := strings.TrimSpace(getenv("DATABASE_URL"))
	if url == "" {
		return errDatabaseURL
	}
	logger := logging.New(os.Stdout, slog.LevelInfo, "migrate")

	db, err := sql.Open("pgx", url)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = db.Close() }()
	driver, err := pgxdriver.WithInstance(db, &pgxdriver.Config{})
	if err != nil {
		return fmt.Errorf("connect to the database: %w", err)
	}
	source, err := iofs.New(migrations.Files, ".")
	if err != nil {
		return fmt.Errorf("read the embedded migrations: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", source, "pgx5", driver)
	if err != nil {
		return fmt.Errorf("prepare the migrations: %w", err)
	}

	// Cloud Run stops a job with SIGTERM: finish the migration in progress
	// instead of leaving it halfway (it only grants a few seconds, enough for
	// a short migration).
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, os.Interrupt)
	defer signal.Stop(stop)
	go func() {
		if _, ok := <-stop; ok {
			m.GracefulStop <- true
		}
	}()

	err = execute(m, cmd)
	version, dirty, _ := m.Version()
	switch {
	case errors.Is(err, migrate.ErrNoChange):
		logger.Info("no migration to apply", slog.Uint64("version", uint64(version)))
		return nil
	case err != nil:
		return failure(err, version)
	}
	logger.Info("migrations done", slog.String("command", cmd.name),
		slog.Uint64("version", uint64(version)), slog.Bool("dirty", dirty))
	return nil
}

// execute runs the command. The migrate library refuses up and down on a
// dirty database (migrate.ErrDirty); force is the only way out, by a person.
func execute(m *migrate.Migrate, cmd command) error {
	switch cmd.name {
	case "up":
		return m.Up()
	case "down":
		return m.Steps(-cmd.n)
	default:
		return m.Force(cmd.n)
	}
}

// failure explains a failed run. Nothing is forced: after a dirty stop the
// person on call decides, with the procedure of docs/migrations.md.
func failure(err error, version uint) error {
	var dirty migrate.ErrDirty
	if errors.As(err, &dirty) {
		return fmt.Errorf("%w at version %d: a migration failed halfway, fix the schema by hand then run migrate force <version>", errDirty, dirty.Version)
	}
	return fmt.Errorf("migration failed (version %d): %w", version, err)
}

// parseCommand reads the command line: up, down <n> (1 to maxDown) or
// force <version> (0 or more).
func parseCommand(args []string) (command, error) {
	if len(args) == 0 {
		return command{}, errUsage
	}
	switch name := args[0]; {
	case name == "up" && len(args) == 1:
		return command{name: name}, nil
	case name == "down" && len(args) == 2:
		n, err := strconv.Atoi(args[1])
		if err != nil || n < 1 || n > maxDown {
			return command{}, errUsage
		}
		return command{name: name, n: n}, nil
	case name == "force" && len(args) == 2:
		n, err := strconv.Atoi(args[1])
		if err != nil || n < 0 {
			return command{}, errUsage
		}
		return command{name: name, n: n}, nil
	default:
		return command{}, errUsage
	}
}
