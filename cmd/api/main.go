// Command api runs the HTTP API.
package main

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/damchi/sport-challenge-api/internal/invitation"
	"github.com/damchi/sport-challenge-api/internal/leaderboard"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/buildinfo"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/config"
	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/platform/httpx"
	"github.com/damchi/sport-challenge-api/internal/platform/logging"
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

	if err := checkTrustedProxies(cfg.Env, cfg.TrustedProxies); err != nil {
		return err
	}

	logger := logging.New(os.Stdout, cfg.LogLevel, "api")
	slog.SetDefault(logger)
	build := buildinfo.Get()
	clk := clock.New(cfg.ClockOffset)
	if cfg.ClockOffset != 0 {
		logger.Warn("clock moved forward", slog.Duration("offset", cfg.ClockOffset))
	}

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
	challenges = challenge.NewService(challenge.NewRepository(gdb), users, sports, clk)
	activities := activity.NewService(activity.NewRepository(gdb), users, sports, participations{challenges}, activityScopes{challenges}, clk)
	leaderboards := leaderboard.NewService(leaderboardChallenges{challenges}, users, leaderboard.NewRepository(gdb), clk, cfg.AtRiskThreshold)
	invitations := invitation.NewService(invitationChallenges{challenges}, invitation.NewRepository(gdb), clk)
	challengeHandler := challenge.NewHandler(challenges)
	handlers := server{
		Handler:            health.NewHandler(pool, logger, build),
		userHandler:        user.NewHandler(users),
		sportHandler:       sport.NewHandler(sports),
		challengeHandler:   challengeHandler,
		activityHandler:    activity.NewHandler(activities),
		leaderboardHandler: leaderboard.NewHandler(leaderboards),
		invitationHandler:  invitation.NewHandler(invitations, challengeHandler),
	}
	opts := routerOptions{
		allowedOrigins: cfg.CORSAllowedOrigins,
		trustedProxies: cfg.TrustedProxies,
		ipLimiter:      httpx.NewRateLimiter(cfg.RateLimitIP.RPS, cfg.RateLimitIP.Burst, clock.Real{}),
		userLimiter:    httpx.NewRateLimiter(cfg.RateLimitUser.RPS, cfg.RateLimitUser.Burst, clock.Real{}),
	}
	router, err := newRouter(logger, opts, handlers, verifier, func(ctx context.Context, subject string) (string, error) {
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
		logger.Info("server started", slog.Int("port", cfg.Port), slog.String("env", string(cfg.Env)),
			slog.String("version", build.Version), slog.String("commit", build.Commit))
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
// activity.Participations (toParticipation).
type participations struct{ challenges memberships }

func (p participations) Participations(ctx context.Context, userID string) ([]activity.Participation, error) {
	ms, err := p.challenges.Memberships(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]activity.Participation, 0, len(ms))
	for _, m := range ms {
		part, err := toParticipation(m)
		if err != nil {
			return nil, err
		}
		out = append(out, part)
	}
	return out, nil
}

// scopes is what activityScopes reads from module challenge.
type scopes interface {
	ActivitiesScope(ctx context.Context, actorID, id, userID string) (challenge.UserChallenge, error)
}

// activityScopes gives module activity the challenge where an actor reads
// the activities of a participant, as activity.Challenges: module challenge
// applies the rights matrix.
type activityScopes struct{ challenges scopes }

func (a activityScopes) ActivitiesScope(ctx context.Context, actorID, challengeID, userID string) (activity.Participation, error) {
	m, err := a.challenges.ActivitiesScope(ctx, actorID, challengeID, userID)
	if err != nil {
		return activity.Participation{}, err
	}
	return toParticipation(m)
}

// toParticipation turns a challenge of a user into an activity.Participation:
// Active comes from the status of the participation, never the role (a
// creator who abandoned no longer competes, ADR 0001).
func toParticipation(m challenge.UserChallenge) (activity.Participation, error) {
	// Validated at creation: an unknown time zone is broken data.
	loc, err := time.LoadLocation(m.Timezone)
	if err != nil {
		return activity.Participation{}, fmt.Errorf("time zone of challenge %s: %w", m.ID, err)
	}
	return activity.Participation{
		ChallengeID: m.ID,
		StartsAt:    m.StartsAt,
		EndsAt:      m.EndsAt,
		Location:    loc,
		Active:      m.Participant == challenge.ParticipantActive,
	}, nil
}

// details is what leaderboardChallenges reads from module challenge.
type details interface {
	Get(ctx context.Context, actorID, id string) (challenge.Detail, error)
}

// leaderboardChallenges gives module leaderboard the challenge an actor
// reads, as leaderboard.Challenges: Get applies the rights matrix, so a
// private challenge the actor may not see is a 404 before anything else is
// loaded.
type leaderboardChallenges struct{ challenges details }

func (l leaderboardChallenges) Challenge(ctx context.Context, actorID, id string) (leaderboard.Challenge, error) {
	d, err := l.challenges.Get(ctx, actorID, id)
	if err != nil {
		return leaderboard.Challenge{}, err
	}
	categories := make([]leaderboard.Category, len(d.Categories))
	for i, c := range d.Categories {
		categories[i] = leaderboard.Category{
			Type: c.Type, Factors: c.Params.Factors, Sport: c.Params.Sport, StepMeters: c.Params.StepMeters,
		}
	}
	return leaderboard.Challenge{ID: d.ID, Timezone: d.Timezone, StartsAt: d.StartsAt, EndsAt: d.EndsAt, Categories: categories}, nil
}

// invited is what invitationChallenges reads from module challenge.
type invited interface {
	InvitationDeadline(ctx context.Context, actorID, id string) (time.Time, error)
	Get(ctx context.Context, actorID, id string) (challenge.Detail, error)
	Join(ctx context.Context, actorID, id string) (challenge.Detail, error)
}

// invitationChallenges gives module invitation the challenge of an
// invitation, as invitation.Challenges: module challenge applies the rights
// matrix and the join rule to the actor.
type invitationChallenges struct{ challenges invited }

func (i invitationChallenges) InvitationDeadline(ctx context.Context, actorID, challengeID string) (time.Time, error) {
	return i.challenges.InvitationDeadline(ctx, actorID, challengeID)
}

func (i invitationChallenges) Preview(ctx context.Context, actorID, challengeID string) (invitation.Challenge, error) {
	d, err := i.challenges.Get(ctx, actorID, challengeID)
	if err != nil {
		return invitation.Challenge{}, err
	}
	return invitation.Challenge{ID: d.ID, Title: d.Title, StartsAt: d.StartsAt, EndsAt: d.EndsAt}, nil
}

func (i invitationChallenges) Join(ctx context.Context, actorID, challengeID string) error {
	_, err := i.challenges.Join(ctx, actorID, challengeID)
	return err
}

// server gathers the handlers of every operation of the contract.
type server struct {
	health.Handler
	userHandler
	sportHandler
	challengeHandler
	activityHandler
	leaderboardHandler
	invitationHandler
}

// These aliases name the embedded handlers: embedding the *Handler of each
// module directly would clash with health.Handler, as every field would be
// called Handler.
type (
	userHandler      = *user.Handler
	sportHandler     = *sport.Handler
	challengeHandler = *challenge.Handler
	activityHandler  = *activity.Handler

	leaderboardHandler = *leaderboard.Handler
	invitationHandler  = *invitation.Handler
)

// checkTrustedProxies refuses to start a deployed API without trusted
// proxies: behind Cloud Run every request comes from the proxy, so all the
// clients would share the bucket of its address and one of them could have
// everyone refused. Only the API needs them, hence a check here rather than
// in the configuration the worker shares.
func checkTrustedProxies(env config.Environment, proxies []string) error {
	if env != config.EnvLocal && len(proxies) == 0 {
		return errors.New("invalid configuration: TRUSTED_PROXIES is required outside the local environment")
	}
	return nil
}

// routerOptions are the settings of the middlewares of newRouter.
type routerOptions struct {
	allowedOrigins []string
	// trustedProxies are the only peers whose X-Forwarded-For is read.
	trustedProxies []string
	// ipLimiter bounds the requests of an IP address, userLimiter the write
	// requests of a user.
	ipLimiter   *httpx.RateLimiter
	userLimiter *httpx.RateLimiter
}

// newRouter registers the routes of the contract behind the middlewares of
// README §5.7: CORS, RateLimitIP, then the protection (Authenticate,
// CurrentUser, RateLimitUser) on every route except the public list below.
// Protect is added before the routes, so it applies to all of them, including
// the ones added later. Each request is then checked against the contract
// before its handler runs.
func newRouter(logger *slog.Logger, opts routerOptions, handler api.StrictServerInterface, verifier auth.TokenVerifier, resolve auth.UserIDResolver) (*gin.Engine, error) {
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
		"GET /health",
		"GET /ready",
	}
	router := httpx.NewRouter(logger)
	if err := router.SetTrustedProxies(opts.trustedProxies); err != nil {
		return nil, fmt.Errorf("trusted proxies: %w", err)
	}
	// CORS goes before Protect: it answers the preflights, which Protect lets
	// through without a token.
	router.Use(httpx.CORS(opts.allowedOrigins))
	// Before Protect: a flood is refused before any token is verified.
	router.Use(httpx.RateLimitIP(opts.ipLimiter, logger))
	router.Use(auth.Protect(public,
		auth.Authenticate(verifier, logger),
		auth.CurrentUser(resolve, logger),
		httpx.RateLimitUser(opts.userLimiter, auth.UserID, logger),
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
// API still starts and /ready reports 503 until the database is back.
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
