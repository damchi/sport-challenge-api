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
	// The time zone database, embedded: challenges are cut in their own time
	// zone, and a minimal image may have no zoneinfo.
	_ "time/tzdata"

	"github.com/gin-gonic/gin"

	"github.com/damchi/sport-challenge-api/internal/activity"
	"github.com/damchi/sport-challenge-api/internal/api"
	"github.com/damchi/sport-challenge-api/internal/challenge"
	"github.com/damchi/sport-challenge-api/internal/health"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/config"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/platform/httpx"
	"github.com/damchi/sport-challenge-api/internal/sport"
	"github.com/damchi/sport-challenge-api/internal/user"
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
	// The pool closes when run returns, after srv.Shutdown below has drained
	// the requests. Keep Shutdown in the body of run, not in a defer.
	defer pool.Close()
	pingDatabase(ctx, logger, pool.Ping)

	// ctx stops the background JWKS refresh when the API shuts down.
	verifier, err := auth.NewAuth0Verifier(ctx, cfg.Auth0Domain, cfg.Auth0Audience)
	if err != nil {
		return err
	}
	gdb, err := db.NewGorm(pool, logger)
	if err != nil {
		return err
	}
	// Each needs the other: deleting an account abandons its challenges, a
	// challenge reads the profiles. The closure resolves challenges once set.
	var challenges *challenge.Service
	abandonAll := abandonAllFunc(func(ctx context.Context, userID string) error {
		return challenges.AbandonAll(ctx, userID)
	})
	management := auth.NewAuth0Management(cfg.Auth0Domain, cfg.Auth0MgmtClientID, cfg.Auth0MgmtClientSecret)
	users := user.NewService(user.NewRepository(gdb), abandonAll, management, logger)

	gin.SetMode(gin.ReleaseMode)
	sports := sport.NewService(sport.NewRepository(gdb))
	challenges = challenge.NewService(challenge.NewRepository(gdb), users, sports, clock.Real{})
	activities := activity.NewService(activity.NewRepository(gdb), users, sports, participations{challenges}, clock.Real{})
	handlers := server{
		Handler:          health.NewHandler(pool, logger),
		userHandler:      user.NewHandler(users),
		sportHandler:     sport.NewHandler(sports),
		challengeHandler: challenge.NewHandler(challenges),
		activityHandler:  activity.NewHandler(activities),
	}
	router, err := newRouter(logger, cfg.CORSAllowedOrigins, handlers, verifier, func(ctx context.Context, subject string) (string, error) {
		u, err := users.GetOrCreateBySubject(ctx, subject)
		return u.ID, err
	})
	if err != nil {
		return err
	}
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

// abandonAllFunc is a function as a user.Participations.
type abandonAllFunc func(ctx context.Context, userID string) error

func (f abandonAllFunc) AbandonAll(ctx context.Context, userID string) error {
	return f(ctx, userID)
}

// memberships is what participations reads from module challenge.
type memberships interface {
	Memberships(ctx context.Context, userID string) ([]challenge.UserChallenge, error)
}

// participations gives module activity the challenges of a user, as
// activity.Participations: Active comes from the status of the
// participation, never the role (a creator who abandoned no longer
// competes, ADR 0001).
type participations struct{ challenges memberships }

func (p participations) Participations(ctx context.Context, userID string) ([]activity.Participation, error) {
	ms, err := p.challenges.Memberships(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]activity.Participation, 0, len(ms))
	for _, m := range ms {
		// Validated at creation: an unknown time zone is broken data.
		loc, err := time.LoadLocation(m.Timezone)
		if err != nil {
			return nil, fmt.Errorf("time zone of challenge %s: %w", m.ID, err)
		}
		out = append(out, activity.Participation{
			ChallengeID: m.ID,
			StartsAt:    m.StartsAt,
			EndsAt:      m.EndsAt,
			Location:    loc,
			Active:      m.Participant == challenge.ParticipantActive,
		})
	}
	return out, nil
}

// server gathers the handlers of every operation of the contract.
type server struct {
	health.Handler
	userHandler
	sportHandler
	challengeHandler
	activityHandler
}

// These aliases name the embedded handlers: embedding the *Handler of each
// module directly would clash with health.Handler, as every field would be
// called Handler.
type (
	userHandler      = *user.Handler
	sportHandler     = *sport.Handler
	challengeHandler = *challenge.Handler
	activityHandler  = *activity.Handler
)

// newRouter registers the routes of the contract behind the protection:
// Authenticate then CurrentUser on every route except the public list below.
// Protect is added before the routes, so it applies to all of them, including
// the ones added later. Each request is then checked against the contract
// before its handler runs.
func newRouter(logger *slog.Logger, allowedOrigins []string, handler api.StrictServerInterface, verifier auth.TokenVerifier, resolve auth.UserIDResolver) (*gin.Engine, error) {
	spec, err := api.GetSpec()
	if err != nil {
		return nil, fmt.Errorf("load embedded contract: %w", err)
	}
	validate, err := httpx.RequestValidator(spec, logger)
	if err != nil {
		return nil, err
	}

	// The only routes served without an access token. Keep it in line with
	// the "security: []" of api/openapi.yaml.
	public := []string{
		"GET /healthz",
		"GET /readyz",
	}
	router := httpx.NewRouter(logger)
	// CORS goes before Protect: it answers the preflights, which Protect lets
	// through without a token.
	router.Use(httpx.CORS(allowedOrigins))
	router.Use(auth.Protect(public,
		auth.Authenticate(verifier, logger),
		auth.CurrentUser(resolve, logger),
	))
	// The generated defaults send err.Error() to the client: every error
	// handler is replaced by one that answers in the API error format.
	strict := api.NewStrictHandlerWithOptions(handler, nil, api.StrictGinServerOptions{
		RequestErrorHandlerFunc:  httpx.BadRequestHandler(logger),
		HandlerErrorFunc:         httpx.ErrorHandler(logger),
		ResponseErrorHandlerFunc: httpx.ErrorHandler(logger),
	})
	api.RegisterHandlersWithOptions(router, strict, api.GinServerOptions{
		ErrorHandler: httpx.InvalidParamHandler(logger),
		Middlewares:  []api.MiddlewareFunc{api.MiddlewareFunc(validate)},
	})
	return router, nil
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
