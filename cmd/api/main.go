// Command api runs the HTTP API.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/damchi/sport-challenge-api/internal/api"
	"github.com/damchi/sport-challenge-api/internal/health"
	"github.com/damchi/sport-challenge-api/internal/platform/config"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/platform/httpx"
)

// shutdownTimeout stays under the 10 seconds Cloud Run waits after SIGTERM.
const shutdownTimeout = 8 * time.Second

// startupPingTimeout bounds the database check done at startup.
const startupPingTimeout = 5 * time.Second

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}

	logger := newLogger(os.Stdout, cfg.LogLevel)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	// Deferred first, run last: the pool closes after the HTTP server stops.
	defer pool.Close()
	pingDatabase(ctx, logger, pool.Ping)

	gin.SetMode(gin.ReleaseMode)
	router := httpx.NewRouter(logger)
	// The generated defaults send err.Error() to the client: every error
	// handler is replaced by one that answers in the API error format.
	strict := api.NewStrictHandlerWithOptions(health.NewHandler(pool, logger), nil, api.StrictGinServerOptions{
		RequestErrorHandlerFunc:  httpx.BadRequestHandler,
		HandlerErrorFunc:         httpx.InternalErrorHandler(logger),
		ResponseErrorHandlerFunc: httpx.InternalErrorHandler(logger),
	})
	api.RegisterHandlersWithOptions(router, strict, api.GinServerOptions{
		ErrorHandler: httpx.InvalidParamHandler,
	})
	logRoutes(logger, router.Routes())

	srv := &http.Server{
		Addr:              ":" + strconv.Itoa(cfg.Port),
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("server started", slog.Int("port", cfg.Port), slog.String("env", string(cfg.Env)))
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	logger.Info("server stopped")
	return nil
}

// pingDatabase only warns when the database does not answer at startup: the
// API still starts and /readyz reports 503 until the database is back.
func pingDatabase(ctx context.Context, logger *slog.Logger, ping func(context.Context) error) {
	ctx, cancel := context.WithTimeout(ctx, startupPingTimeout)
	defer cancel()
	if err := ping(ctx); err != nil {
		logger.Warn("database unavailable at startup", slog.String("error", err.Error()))
		return
	}
	logger.Info("database connected")
}

// logRoutes lists the registered routes at debug level, as Gin's debug mode
// would, without leaving the JSON log format.
func logRoutes(logger *slog.Logger, routes gin.RoutesInfo) {
	list := make([]string, 0, len(routes))
	for _, r := range routes {
		list = append(list, r.Method+" "+r.Path)
	}
	logger.Debug("routes registered", slog.Any("routes", list))
}

// newLogger writes JSON lines that Cloud Logging understands: "severity"
// (DEBUG, INFO, WARNING, ERROR), "message" and "time" (in UTC).
func newLogger(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) > 0 {
				return a
			}
			switch a.Key {
			case slog.TimeKey:
				a.Value = slog.TimeValue(a.Value.Time().UTC())
			case slog.LevelKey:
				a.Key = "severity"
				if lvl, ok := a.Value.Any().(slog.Level); ok && lvl == slog.LevelWarn {
					a.Value = slog.StringValue("WARNING")
				}
			case slog.MessageKey:
				a.Key = "message"
			}
			return a
		},
	}))
}
