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
	"github.com/damchi/sport-challenge-api/internal/admin"
	"github.com/damchi/sport-challenge-api/internal/api"
	"github.com/damchi/sport-challenge-api/internal/challenge"
	"github.com/damchi/sport-challenge-api/internal/closure"
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

// poolSize is the number of database connections of one instance. 3
// instances at most hold 12 of the 22 that db-f1-micro leaves to the
// application, next to the worker and migrate jobs (docs/deploiement.md).
// Raise it only with that budget.
const poolSize = 4

// requestTimeout bounds each request, waiting for a connection of the pool
// included. It stays under the WriteTimeout of the server (30 s), so the
// error still reaches the client.
const requestTimeout = 25 * time.Second

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

	pool, err := db.NewPool(ctx, cfg.DatabaseURL, poolSize)
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
	// Each needs the other: deleting an account abandons its challenges and
	// erases its rows in the other modules, which read the profiles; the
	// challenge list reads the standings from the leaderboard, which reads
	// the challenges. The closures resolve the services once set.
	var (
		challenges   *challenge.Service
		activities   *activity.Service
		invitations  *invitation.Service
		leaderboards *leaderboard.Service
	)
	standings := challengeStandings{
		standings: func(ctx context.Context, actorID string, cs []leaderboard.Challenge, now time.Time) (map[string]leaderboard.Standing, error) {
			return leaderboards.Standings(ctx, actorID, cs, now)
		},
		results: func(ctx context.Context, actorID string, cs []leaderboard.Challenge) (map[string][]leaderboard.Place, error) {
			return leaderboards.Results(ctx, actorID, cs)
		},
	}
	abandonAll := abandonAllFunc(func(ctx context.Context, userID string) error {
		return challenges.AbandonAll(ctx, userID)
	})
	// The deletion of an account erases its profile from the admin journal
	// too, whoever deletes it.
	adminRepo := admin.NewRepository(gdb)
	journal := admin.NewJournal(adminRepo, clk)
	erasers := []user.Eraser{
		journal,
		eraseUserFunc(func(ctx context.Context, userID string) error { return invitations.EraseUser(ctx, userID) }),
		eraseUserFunc(func(ctx context.Context, userID string) error { return activities.EraseUser(ctx, userID) }),
		eraseUserFunc(func(ctx context.Context, userID string) error { return challenges.EraseUser(ctx, userID) }),
	}
	management := auth.NewAuth0Management(cfg.Auth0MgmtDomain, cfg.Auth0MgmtClientID, cfg.Auth0MgmtClientSecret)
	users := user.NewService(user.NewRepository(gdb), abandonAll, erasers, management, clk, logger)

	gin.SetMode(gin.ReleaseMode)
	sports := sport.NewService(sport.NewRepository(gdb))
	challenges = challenge.NewService(challenge.NewRepository(gdb), challengeProfiles{users}, sports, standings, clk)
	activities = activity.NewService(activity.NewRepository(gdb), activityProfiles{users}, activityCatalogue{sports}, participations{challenges}, activityScopes{challenges}, clk)
	leaderboards = leaderboard.NewService(leaderboardChallenges{challenges}, leaderboardProfiles{users}, leaderboard.NewRepository(gdb), clk, cfg.AtRiskThreshold)
	invitations = invitation.NewService(invitationChallenges{challenges}, invitation.NewRepository(gdb), clk)
	challengeHandler := challenge.NewHandler(challenges)
	// The admin changes judge a challenge again with the code of the worker
	// (cmd/worker), from the same data as the rankings.
	recompute := closure.NewService(closure.NewRepository(gdb), leaderboard.NewRepository(gdb), clk, logger)
	corrections := admin.NewCorrections(adminRepo, journal, recompute, leaderboards)
	challengeCorrections := admin.NewChallengeCorrections(corrections, challenges, invitations)
	memberCorrections := admin.NewMemberCorrections(corrections, challenges)
	activityCorrections := admin.NewActivityCorrections(corrections, activities)
	accountCorrections := admin.NewAccountCorrections(corrections, users)
	handlers := server{
		Handler:            health.NewHandler(pool, logger, build),
		userHandler:        user.NewHandler(users),
		sportHandler:       sport.NewHandler(sports),
		challengeHandler:   challengeHandler,
		activityHandler:    activity.NewHandler(activities),
		leaderboardHandler: leaderboard.NewHandler(leaderboards),
		invitationHandler:  invitation.NewHandler(invitations, challengeHandler),
		adminHandler:       admin.NewHandler(admin.NewService(adminRepo, clk, build), challengeCorrections, memberCorrections, activityCorrections, accountCorrections),
	}
	opts := routerOptions{
		allowedOrigins: cfg.CORSAllowedOrigins,
		trustedProxies: cfg.TrustedProxies,
		ipLimiter:      httpx.NewRateLimiter(cfg.RateLimitIP.RPS, cfg.RateLimitIP.Burst, clock.Real{}),
		userLimiter:    httpx.NewRateLimiter(cfg.RateLimitUser.RPS, cfg.RateLimitUser.Burst, clock.Real{}),
	}
	router, err := newRouter(logger, opts, handlers, verifier, func(ctx context.Context, claims auth.Claims) (string, error) {
		u, err := users.GetOrCreateBySubject(ctx, claims.Subject, claims.Email)
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

// eraseUserFunc is a function as a user.Eraser.
type eraseUserFunc func(ctx context.Context, userID string) error

func (f eraseUserFunc) EraseUser(ctx context.Context, userID string) error {
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
	ActivitiesScope(ctx context.Context, actor auth.Actor, id, userID string) (challenge.UserChallenge, error)
	AllActivitiesScope(ctx context.Context, actor auth.Actor, id string) (challenge.UserChallenge, map[string]challenge.Membership, error)
}

// activityScopes gives module activity the challenge where an actor reads
// the activities of a participant, as activity.Challenges: module challenge
// applies the rights matrix.
type activityScopes struct{ challenges scopes }

func (a activityScopes) ActivitiesScope(ctx context.Context, actor auth.Actor, challengeID, userID string) (activity.Participation, error) {
	m, err := a.challenges.ActivitiesScope(ctx, actor, challengeID, userID)
	if err != nil {
		return activity.Participation{}, err
	}
	return toParticipation(m)
}

func (a activityScopes) AllActivitiesScope(ctx context.Context, actor auth.Actor, challengeID string) (activity.Participation, map[string]activity.Participation, error) {
	c, members, err := a.challenges.AllActivitiesScope(ctx, actor, challengeID)
	if err != nil {
		return activity.Participation{}, nil, err
	}
	out, err := toParticipation(c)
	if err != nil {
		return activity.Participation{}, nil, err
	}
	byUser := make(map[string]activity.Participation, len(members))
	for id, m := range members {
		c.Membership = m
		part, err := toParticipation(c)
		if err != nil {
			return activity.Participation{}, nil, err
		}
		byUser[id] = part
	}
	return out, byUser, nil
}

// toParticipation turns a challenge of a user into an activity.Participation:
// Active comes from the status of the participation, never the role (a
// creator who abandoned no longer competes, ADR 0001); AbandonedAt keeps the
// weeks locked before the abandon locked.
func toParticipation(m challenge.UserChallenge) (activity.Participation, error) {
	// Validated at creation: an unknown time zone is broken data.
	loc, err := time.LoadLocation(m.Timezone)
	if err != nil {
		return activity.Participation{}, fmt.Errorf("time zone of challenge %s: %w", m.ID, err)
	}
	return activity.Participation{
		ChallengeID: m.ID,
		Title:       m.Title,
		StartsAt:    m.StartsAt,
		EndsAt:      m.EndsAt,
		Location:    loc,
		Factors:     m.Factors,
		Active:      m.Participant == challenge.ParticipantActive,
		AbandonedAt: m.AbandonedAt,
	}, nil
}

// profiles is what challengeProfiles and leaderboardProfiles read from module
// user.
type profiles interface {
	ProfileComplete(ctx context.Context, id string) (bool, error)
	PublicNames(ctx context.Context, ids []string) (map[string]user.PublicName, error)
}

// challengeProfiles gives module challenge the profiles, as
// challenge.Profiles.
type challengeProfiles struct{ users profiles }

func (c challengeProfiles) ProfileComplete(ctx context.Context, id string) (bool, error) {
	return c.users.ProfileComplete(ctx, id)
}

func (c challengeProfiles) Names(ctx context.Context, ids []string) (map[string]challenge.Name, error) {
	names, err := c.users.PublicNames(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string]challenge.Name, len(names))
	for id, n := range names {
		out[id] = challenge.Name{Pseudo: n.Pseudo, FirstName: n.FirstName, LastName: n.LastName}
	}
	return out, nil
}

// activityCatalogue gives module activity the rules of a sport of the
// catalogue.
type activityCatalogue struct{ sports *sport.Service }

func (c activityCatalogue) SportRules(ctx context.Context, code string) (activity.SportRules, bool, error) {
	sp, found, err := c.sports.Find(ctx, code)
	return activity.SportRules{DistanceMode: sp.DistanceMode, ElevationAllowed: sp.ElevationAllowed}, found, err
}

// activityProfiles gives module activity the profiles, as
// activity.Profiles.
type activityProfiles struct{ users profiles }

func (a activityProfiles) ProfileComplete(ctx context.Context, id string) (bool, error) {
	return a.users.ProfileComplete(ctx, id)
}

func (a activityProfiles) Names(ctx context.Context, ids []string) (map[string]activity.Name, error) {
	names, err := a.users.PublicNames(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string]activity.Name, len(names))
	for id, n := range names {
		out[id] = activity.Name{Pseudo: n.Pseudo, FirstName: n.FirstName, LastName: n.LastName}
	}
	return out, nil
}

// leaderboardProfiles gives module leaderboard the public names, as
// leaderboard.Profiles.
type leaderboardProfiles struct{ users profiles }

func (l leaderboardProfiles) Names(ctx context.Context, ids []string) (map[string]leaderboard.Name, error) {
	names, err := l.users.PublicNames(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string]leaderboard.Name, len(names))
	for id, n := range names {
		out[id] = leaderboard.Name{Pseudo: n.Pseudo, FirstName: n.FirstName, LastName: n.LastName}
	}
	return out, nil
}

// details is what leaderboardChallenges reads from module challenge.
type details interface {
	Get(ctx context.Context, actor auth.Actor, id string) (challenge.Detail, error)
}

// leaderboardChallenges gives module leaderboard the challenge an actor
// reads, as leaderboard.Challenges: Get applies the rights matrix, so a
// private challenge the actor may not see is a 404 before anything else is
// loaded.
type leaderboardChallenges struct{ challenges details }

func (l leaderboardChallenges) Challenge(ctx context.Context, actor auth.Actor, id string) (leaderboard.Challenge, error) {
	d, err := l.challenges.Get(ctx, actor, id)
	if err != nil {
		return leaderboard.Challenge{}, err
	}
	c := toLeaderboardChallenge(d.Challenge)
	c.ReadsActivities = challenge.ReadsActivities(d.Role, actor.Admin)
	return c, nil
}

// toLeaderboardChallenge gives a challenge, with its categories, the shape
// of module leaderboard.
func toLeaderboardChallenge(c challenge.Challenge) leaderboard.Challenge {
	categories := make([]leaderboard.Category, len(c.Categories))
	for i, cat := range c.Categories {
		categories[i] = leaderboard.Category{
			Type: cat.Type, Factors: cat.Params.Factors, Sport: cat.Params.Sport, StepMeters: cat.Params.StepMeters,
			Criteria: cat.Params.Criteria, Podium: cat.Params.Podium, MinSessionSeconds: cat.Params.MinSessionSeconds,
		}
	}
	return leaderboard.Challenge{ID: c.ID, Timezone: c.Timezone, StartsAt: c.StartsAt, EndsAt: c.EndsAt, Categories: categories}
}

// challengeStandings gives module challenge the standings and the frozen
// results of module leaderboard, as challenge.Standings.
type challengeStandings struct {
	standings func(ctx context.Context, actorID string, challenges []leaderboard.Challenge, now time.Time) (map[string]leaderboard.Standing, error)
	results   func(ctx context.Context, actorID string, challenges []leaderboard.Challenge) (map[string][]leaderboard.Place, error)
}

func (f challengeStandings) Standings(ctx context.Context, actorID string, challenges []challenge.Challenge, now time.Time) (map[string]challenge.Standing, error) {
	standings, err := f.standings(ctx, actorID, toLeaderboardChallenges(challenges), now)
	if err != nil {
		return nil, err
	}
	out := make(map[string]challenge.Standing, len(standings))
	for id, st := range standings {
		out[id] = challenge.Standing{Week: st.Week, WeekEndsAt: st.WeekEndsAt, Rankings: toRankings(st.Categories)}
	}
	return out, nil
}

func (f challengeStandings) Results(ctx context.Context, actorID string, challenges []challenge.Challenge) (map[string][]challenge.Ranking, error) {
	results, err := f.results(ctx, actorID, toLeaderboardChallenges(challenges))
	if err != nil {
		return nil, err
	}
	out := make(map[string][]challenge.Ranking, len(results))
	for id, places := range results {
		out[id] = toRankings(places)
	}
	return out, nil
}

// toLeaderboardChallenges turns challenges into their form of module
// leaderboard.
func toLeaderboardChallenges(challenges []challenge.Challenge) []leaderboard.Challenge {
	out := make([]leaderboard.Challenge, len(challenges))
	for i, c := range challenges {
		out[i] = toLeaderboardChallenge(c)
	}
	return out
}

// toRankings turns the places of module leaderboard into the rankings of
// module challenge, in the same order.
func toRankings(places []leaderboard.Place) []challenge.Ranking {
	out := make([]challenge.Ranking, len(places))
	for i, p := range places {
		out[i] = challenge.Ranking{
			Type: p.Type, Rank: p.Entry.Rank, RankedCount: p.RankedCount, Progress: p.Entry.Progress, Provisional: p.Entry.Provisional,
		}
	}
	return out
}

// invited is what invitationChallenges reads from module challenge.
type invited interface {
	InvitationDeadline(ctx context.Context, actorID, id string) (time.Time, error)
	Get(ctx context.Context, actor auth.Actor, id string) (challenge.Detail, error)
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
	// An invitation is opened as a user: the admin permission plays no part.
	d, err := i.challenges.Get(ctx, auth.Actor{ID: actorID}, challengeID)
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
	adminHandler
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
	adminHandler       = *admin.Handler
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
// before its handler runs; an invalid request to an /admin route without the
// admin permission answers 404 not_found, as a valid one does
// (admin.HideInvalidRequest).
func newRouter(logger *slog.Logger, opts routerOptions, handler api.StrictServerInterface, verifier auth.TokenVerifier, resolve auth.UserIDResolver) (*gin.Engine, error) {
	spec, err := api.GetSpec()
	if err != nil {
		return nil, fmt.Errorf("load embedded contract: %w", err)
	}
	validate, err := httpx.RequestValidator(spec, logger, admin.HideInvalidRequest)
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
	router.Use(httpx.Timeout(requestTimeout))
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
		RequestErrorHandlerFunc:  httpx.BadRequestHandler(logger, admin.HideInvalidRequest),
		HandlerErrorFunc:         httpx.ErrorHandler(logger),
		ResponseErrorHandlerFunc: httpx.ErrorHandler(logger),
	})
	api.RegisterHandlersWithOptions(router, strict, api.GinServerOptions{
		ErrorHandler: httpx.InvalidParamHandler(logger, admin.HideInvalidRequest),
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
