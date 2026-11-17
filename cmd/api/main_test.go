package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/damchi/sport-challenge-api/internal/activity"
	"github.com/damchi/sport-challenge-api/internal/api"
	"github.com/damchi/sport-challenge-api/internal/challenge"
	"github.com/damchi/sport-challenge-api/internal/health"
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/buildinfo"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/config"
	"github.com/damchi/sport-challenge-api/internal/platform/httpx"
	"github.com/damchi/sport-challenge-api/internal/platform/logging"
	"github.com/damchi/sport-challenge-api/internal/sport"
	"github.com/damchi/sport-challenge-api/internal/user"
)

func TestLogRoutesAtDebugLevel(t *testing.T) {
	routes := gin.RoutesInfo{{Method: "GET", Path: "/healthz"}}

	var out bytes.Buffer
	logRoutes(logging.New(&out, slog.LevelDebug, "api"), routes)
	var line map[string]any
	if err := json.Unmarshal(out.Bytes(), &line); err != nil {
		t.Fatalf("not JSON: %q", out.String())
	}
	got, _ := line["routes"].([]any)
	if line["message"] != "routes registered" || len(got) != 1 || got[0] != "GET /healthz" {
		t.Fatalf("line = %v, want the GET /healthz route", line)
	}

	out.Reset()
	logRoutes(logging.New(&out, slog.LevelInfo, "api"), routes)
	if out.Len() != 0 {
		t.Fatalf("routes logged at info level: %q", out.String())
	}
}

func TestPingDatabase(t *testing.T) {
	tests := []struct {
		name         string
		pingErr      error
		wantSeverity string
		wantMessage  string
	}{
		{name: "available", wantSeverity: "INFO", wantMessage: "database connected"},
		{name: "unavailable", pingErr: errors.New("connection refused"), wantSeverity: "WARNING", wantMessage: "database unavailable at startup"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			pingDatabase(t.Context(), logging.New(&out, slog.LevelDebug, "api"), func(ctx context.Context) error {
				if _, ok := ctx.Deadline(); !ok {
					t.Error("ping context has no deadline")
				}
				return tt.pingErr
			})

			var line map[string]any
			if err := json.Unmarshal(out.Bytes(), &line); err != nil {
				t.Fatalf("not JSON: %q", out.String())
			}
			if line["severity"] != tt.wantSeverity || line["message"] != tt.wantMessage {
				t.Fatalf("line = %v, want %s %q", line, tt.wantSeverity, tt.wantMessage)
			}
		})
	}
}

// rejectingVerifier fails the test if a token reaches it: the guard only sends
// requests without a token.
type rejectingVerifier struct{ t *testing.T }

func (v rejectingVerifier) Verify(context.Context, string) (auth.Claims, error) {
	v.t.Error("verifier called for a request without a token")
	return auth.Claims{}, auth.ErrInvalidToken
}

type pingerFunc func(context.Context) error

func (f pingerFunc) Ping(ctx context.Context) error { return f(ctx) }

const webOrigin = "https://app.example.com"

// testHandlers are the handlers of the tests of the router. No request in
// these tests reaches a business handler: their services stay nil.
func testHandlers() server {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	return server{
		Handler:          health.NewHandler(pingerFunc(func(context.Context) error { return nil }), logger, buildinfo.Get()),
		userHandler:      user.NewHandler(nil),
		sportHandler:     sport.NewHandler(nil),
		challengeHandler: challenge.NewHandler(nil),
		activityHandler:  activity.NewHandler(nil),
	}
}

// testLimiter is a rate limiter of burst requests per key, never refilled
// (the clock does not move).
func testLimiter(burst int) *httpx.RateLimiter {
	return httpx.NewRateLimiter(1, burst, clock.NewFake(time.Date(2026, 3, 4, 15, 0, 0, 0, time.UTC)))
}

// newTestRouter returns the router of the API with rate limits no test reaches.
func newTestRouter(t *testing.T, verifier auth.TokenVerifier, resolve auth.UserIDResolver) *gin.Engine {
	t.Helper()
	opts := routerOptions{allowedOrigins: []string{webOrigin}, ipLimiter: testLimiter(1000), userLimiter: testLimiter(1000)}
	return newTestRouterWith(t, opts, testHandlers(), verifier, resolve)
}

func newTestRouterWith(t *testing.T, opts routerOptions, handlers api.StrictServerInterface, verifier auth.TokenVerifier, resolve auth.UserIDResolver) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r, err := newRouter(slog.New(slog.NewJSONHandler(io.Discard, nil)), opts, handlers, verifier, resolve)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func serve(t *testing.T, r *gin.Engine, req *http.Request) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body.Error.Code
}

// pathParam matches the ":name" and "*name" segments of a Gin route.
var pathParam = regexp.MustCompile(`(/)[:*][^/]+`)

// TestEveryRouteIsProtectedByDefault walks the registered routes: each one
// outside the public list must answer 401 unauthenticated without a token. A
// route registered after newRouter, as any future handler, is covered too:
// until the contract has a protected operation, that test route is the one
// that keeps the guard from passing on public routes only.
func TestEveryRouteIsProtectedByDefault(t *testing.T) {
	public := map[string]bool{"GET /healthz": true, "GET /readyz": true}
	r := newTestRouter(t, rejectingVerifier{t}, func(context.Context, string) (string, error) {
		t.Error("user resolved for a request without a token")
		return "", nil
	})
	r.GET("/api/v1/test-only/:itemId", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	var protected int
	for _, route := range r.Routes() {
		name := route.Method + " " + route.Path
		t.Run(name, func(t *testing.T) {
			path := pathParam.ReplaceAllString(route.Path, "${1}x")
			status, code := serve(t, r, httptest.NewRequest(route.Method, path, nil))
			if public[name] {
				if status == http.StatusUnauthorized {
					t.Fatalf("public route answers 401")
				}
				return
			}
			protected++
			if status != http.StatusUnauthorized || code != "unauthenticated" {
				t.Fatalf("got %d %q without a token, want 401 unauthenticated", status, code)
			}
		})
	}
	if protected == 0 {
		t.Fatal("no protected route checked")
	}
	// A trailing slash is not redirected before the middlewares.
	if status, code := serve(t, r, httptest.NewRequest(http.MethodGet, "/api/v1/test-only/x/", nil)); status != http.StatusUnauthorized || code != "unauthenticated" {
		t.Errorf("trailing slash got %d %q without a token, want 401 unauthenticated", status, code)
	}
	for name := range public {
		if !slices.ContainsFunc(r.Routes(), func(ri gin.RouteInfo) bool { return ri.Method+" "+ri.Path == name }) {
			t.Errorf("public route %s is not registered", name)
		}
	}
}

func TestProtectedRouteResolvesTheCurrentUser(t *testing.T) {
	const userID = "0b9d4a5e-6a43-4c8e-9d1f-7a2b3c4d5e6f"
	tests := []struct {
		name       string
		resolveErr error
		wantStatus int
		wantCode   string
	}{
		{name: "valid token", wantStatus: http.StatusNoContent},
		{name: "deleted account", resolveErr: apperr.New(apperr.CodeAccountDeleted, nil), wantStatus: http.StatusUnauthorized, wantCode: "account_deleted"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newTestRouter(t, acceptingVerifier{}, func(_ context.Context, subject string) (string, error) {
				if subject != "email|123" {
					t.Errorf("subject = %q, want email|123", subject)
				}
				return userID, tt.resolveErr
			})
			r.GET("/api/v1/probe", func(c *gin.Context) {
				if id, _ := auth.UserID(c); id != userID {
					t.Errorf("user ID = %q, want %q", id, userID)
				}
				c.Status(http.StatusNoContent)
			})

			req := httptest.NewRequest(http.MethodGet, "/api/v1/probe", nil)
			req.Header.Set("Authorization", "Bearer some.access.token")
			if status, code := serve(t, r, req); status != tt.wantStatus || code != tt.wantCode {
				t.Fatalf("got %d %q, want %d %q", status, code, tt.wantStatus, tt.wantCode)
			}
		})
	}
}

// sportCatalogue is the storage of the sport catalogue in the tests of the
// router.
type sportCatalogue []sport.Sport

func (s sportCatalogue) List(context.Context) ([]sport.Sport, error) { return s, nil }

func TestSecurityHeaders(t *testing.T) {
	handlers := testHandlers()
	handlers.sportHandler = sport.NewHandler(sport.NewService(sportCatalogue{{Code: "run", DistanceMode: "required"}}))
	opts := routerOptions{allowedOrigins: []string{webOrigin}, ipLimiter: testLimiter(1000), userLimiter: testLimiter(1000)}
	r := newTestRouterWith(t, opts, handlers, acceptingVerifier{}, func(context.Context, string) (string, error) {
		return "0b9d4a5e-6a43-4c8e-9d1f-7a2b3c4d5e6f", nil
	})
	r.GET("/api/v1/probe", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"pseudo": "runner"}) })
	r.GET("/api/v1/panic", func(*gin.Context) { panic("boom") })

	tests := []struct {
		name         string
		path         string
		token        bool
		wantStatus   int
		wantCacheCtl string
	}{
		{name: "protected route", path: "/api/v1/probe", token: true, wantStatus: http.StatusOK, wantCacheCtl: "no-store"},
		{name: "refused without a token", path: "/api/v1/probe", wantStatus: http.StatusUnauthorized, wantCacheCtl: "no-store"},
		{name: "recovered panic", path: "/api/v1/panic", token: true, wantStatus: http.StatusInternalServerError, wantCacheCtl: "no-store"},
		{name: "unknown route", path: "/api/v1/unknown", token: true, wantStatus: http.StatusNotFound, wantCacheCtl: "no-store"},
		{name: "sports keep their own cache", path: "/api/v1/sports", token: true, wantStatus: http.StatusOK, wantCacheCtl: "private, max-age=300"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if tt.token {
				req.Header.Set("Authorization", "Bearer some.access.token")
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
			}
			if got := rec.Header().Values("Cache-Control"); len(got) != 1 || got[0] != tt.wantCacheCtl {
				t.Errorf("Cache-Control = %q, want %q", got, tt.wantCacheCtl)
			}
		})
	}
}

// TestSecurityHeadersBeforeEarlyAnswers checks the answers given before
// authentication: a preflight and a refused flood carry the headers too.
func TestSecurityHeadersBeforeEarlyAnswers(t *testing.T) {
	preflight := httptest.NewRequest(http.MethodOptions, "/api/v1/me", nil)
	preflight.Header.Set("Origin", webOrigin)
	preflight.Header.Set("Access-Control-Request-Method", http.MethodGet)
	flooded := routerOptions{allowedOrigins: []string{webOrigin}, ipLimiter: testLimiter(0), userLimiter: testLimiter(1000)}
	tests := []struct {
		name       string
		router     *gin.Engine
		req        *http.Request
		wantStatus int
	}{
		{name: "preflight", router: newTestRouter(t, rejectingVerifier{t}, nil), req: preflight, wantStatus: http.StatusNoContent},
		{name: "rate limited", router: newTestRouterWith(t, flooded, testHandlers(), rejectingVerifier{t}, nil), req: httptest.NewRequest(http.MethodGet, "/api/v1/me", nil), wantStatus: http.StatusTooManyRequests},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tt.router.ServeHTTP(rec, tt.req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}
		})
	}
}

func TestPreflightSkipsAuthentication(t *testing.T) {
	r := newTestRouter(t, rejectingVerifier{t}, func(context.Context, string) (string, error) {
		t.Error("user resolved for a preflight")
		return "", nil
	})
	// A browser sends no token with a preflight.
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/me", nil)
	req.Header.Set("Origin", webOrigin)
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	req.Header.Set("Access-Control-Request-Headers", "authorization")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != webOrigin {
		t.Fatalf("preflight got %d, Access-Control-Allow-Origin %q, want 204 for %s",
			rec.Code, rec.Header().Get("Access-Control-Allow-Origin"), webOrigin)
	}
}

// TestRateLimitIPComesBeforeAuthentication: beyond the limit of its address,
// a request is refused before its token is verified, probes included.
func TestRateLimitIPComesBeforeAuthentication(t *testing.T) {
	var verified int
	opts := routerOptions{ipLimiter: testLimiter(2), userLimiter: testLimiter(1000)}
	r := newTestRouterWith(t, opts, testHandlers(), countingVerifier{&verified}, func(context.Context, string) (string, error) {
		return "0b9d4a5e-6a43-4c8e-9d1f-7a2b3c4d5e6f", nil
	})
	r.GET("/api/v1/probe", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	request := func(path string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer some.access.token")
		// No proxy is trusted: the header does not give another bucket.
		req.Header.Set("X-Forwarded-For", "203.0.113."+strconv.Itoa(verified+1))
		return req
	}
	for range 2 {
		if status, code := serve(t, r, request("/api/v1/probe")); status != http.StatusNoContent {
			t.Fatalf("within the limit got %d %q, want 204", status, code)
		}
	}
	for _, path := range []string{"/api/v1/probe", "/healthz", "/readyz", "/unknown"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, request(path))
		if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
			t.Fatalf("%s beyond the limit got %d, Retry-After %q, want 429", path, rec.Code, rec.Header().Get("Retry-After"))
		}
	}
	if verified != 2 {
		t.Fatalf("%d tokens verified, want 2: a refused request must not reach Authenticate", verified)
	}
}

// TestTrustedProxyGivesTheClientIP: behind a declared proxy, each client has
// its own bucket, found in the entry the proxy appended to X-Forwarded-For.
func TestTrustedProxyGivesTheClientIP(t *testing.T) {
	// 192.0.2.1 is the peer address of httptest requests.
	opts := routerOptions{trustedProxies: []string{"192.0.2.1"}, ipLimiter: testLimiter(1), userLimiter: testLimiter(1000)}
	r := newTestRouterWith(t, opts, testHandlers(), rejectingVerifier{t}, nil)
	get := func(forwardedFor string) int {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.Header.Set("X-Forwarded-For", forwardedFor)
		status, _ := serve(t, r, req)
		return status
	}
	if status := get("198.51.100.7"); status != http.StatusOK {
		t.Fatalf("first client got %d, want 200", status)
	}
	if status := get("198.51.100.8"); status != http.StatusOK {
		t.Fatalf("second client got %d, want 200", status)
	}
	if status := get("203.0.113.9, 198.51.100.7"); status != http.StatusTooManyRequests {
		t.Fatalf("first client with a forged entry got %d, want 429", status)
	}
}

func TestCheckTrustedProxies(t *testing.T) {
	tests := []struct {
		name    string
		env     config.Environment
		proxies []string
		wantErr bool
	}{
		{name: "local without proxy", env: config.EnvLocal},
		{name: "local with a proxy", env: config.EnvLocal, proxies: []string{"10.0.0.1"}},
		{name: "production without proxy", env: config.EnvProduction, wantErr: true},
		{name: "production with a proxy", env: config.EnvProduction, proxies: []string{"169.254.0.0/16"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := checkTrustedProxies(tt.env, tt.proxies); (err != nil) != tt.wantErr {
				t.Fatalf("checkTrustedProxies() error = %v, want an error: %v", err, tt.wantErr)
			}
		})
	}
}

func TestInvalidTrustedProxyFailsAtStartup(t *testing.T) {
	opts := routerOptions{trustedProxies: []string{"not-an-address"}, ipLimiter: testLimiter(1), userLimiter: testLimiter(1)}
	if _, err := newRouter(slog.New(slog.NewJSONHandler(io.Discard, nil)), opts, testHandlers(), rejectingVerifier{t}, nil); err == nil {
		t.Fatal("newRouter() error = nil, want an error for an invalid proxy")
	}
}

// takenPseudo answers every PATCH /me as the user service does for a pseudo
// already taken.
type takenPseudo struct {
	server
	calls *int
}

func (h takenPseudo) PatchMe(context.Context, api.PatchMeRequestObject) (api.PatchMeResponseObject, error) {
	*h.calls++
	e := apperr.InvalidFields(nil, "pseudo")
	e.Status = http.StatusConflict
	return nil, e
}

// TestRateLimitUserComesBeforeTheHandler: beyond the limit of the user,
// PATCH /me answers 429 before any 409, which would otherwise tell which
// pseudos are taken, one guess after another. Another user and the reads of
// the same user are not affected.
func TestRateLimitUserComesBeforeTheHandler(t *testing.T) {
	var calls int
	opts := routerOptions{ipLimiter: testLimiter(1000), userLimiter: testLimiter(2)}
	// The token stands for the subject, the subject for the user.
	r := newTestRouterWith(t, opts, takenPseudo{testHandlers(), &calls}, subjectVerifier{}, func(_ context.Context, subject string) (string, error) {
		return subject, nil
	})
	r.GET("/api/v1/probe", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	patchMe := func(user string) (int, string) {
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/me", strings.NewReader(`{"pseudo":"taken"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+user)
		return serve(t, r, req)
	}

	for range 2 {
		if status, code := patchMe("user-1"); status != http.StatusConflict {
			t.Fatalf("within the limit got %d %q, want 409", status, code)
		}
	}
	for range 3 {
		if status, code := patchMe("user-1"); status != http.StatusTooManyRequests || code != "rate_limited" {
			t.Fatalf("beyond the limit got %d %q, want 429 rate_limited", status, code)
		}
	}
	if calls != 2 {
		t.Fatalf("handler called %d times, want 2", calls)
	}

	if status, code := patchMe("user-2"); status != http.StatusConflict {
		t.Fatalf("another user got %d %q, want 409", status, code)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/probe", nil)
	req.Header.Set("Authorization", "Bearer user-1")
	if status, code := serve(t, r, req); status != http.StatusNoContent {
		t.Fatalf("read of the limited user got %d %q, want 204", status, code)
	}
}

// countingVerifier accepts every token and counts them.
type countingVerifier struct{ verified *int }

func (v countingVerifier) Verify(context.Context, string) (auth.Claims, error) {
	*v.verified++
	return auth.Claims{Subject: "email|123"}, nil
}

// subjectVerifier accepts every token and takes it as the subject.
type subjectVerifier struct{}

func (subjectVerifier) Verify(_ context.Context, token string) (auth.Claims, error) {
	return auth.Claims{Subject: token}, nil
}

type acceptingVerifier struct{}

func (acceptingVerifier) Verify(context.Context, string) (auth.Claims, error) {
	return auth.Claims{Subject: "email|123"}, nil
}

type membershipsFunc func(ctx context.Context, userID string) ([]challenge.UserChallenge, error)

func (f membershipsFunc) Memberships(ctx context.Context, userID string) ([]challenge.UserChallenge, error) {
	return f(ctx, userID)
}

// TestParticipationsActiveComesFromTheStatus: only an active participation
// competes, whatever the role.
func TestParticipationsActiveComesFromTheStatus(t *testing.T) {
	start := time.Date(2026, 6, 1, 4, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 21)
	tests := []struct {
		name       string
		membership challenge.Membership
		want       bool
	}{
		{name: "active participant", membership: challenge.Membership{Participant: challenge.ParticipantActive}, want: true},
		{name: "active creator", membership: challenge.Membership{Creator: true, Participant: challenge.ParticipantActive}, want: true},
		{name: "creator who abandoned", membership: challenge.Membership{Creator: true, Participant: challenge.ParticipantAbandoned}},
		{name: "abandoned participant", membership: challenge.Membership{Participant: challenge.ParticipantAbandoned}},
		{name: "invited", membership: challenge.Membership{Invited: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := participations{membershipsFunc(func(_ context.Context, userID string) ([]challenge.UserChallenge, error) {
				if userID != "u1" {
					t.Errorf("memberships of %q, want u1", userID)
				}
				return []challenge.UserChallenge{{ID: "c1", Timezone: "America/Toronto", StartsAt: start, EndsAt: end, Membership: tt.membership}}, nil
			})}
			got, err := p.Participations(t.Context(), "u1")
			if err != nil || len(got) != 1 {
				t.Fatalf("Participations() = %+v, %v", got, err)
			}
			g := got[0]
			if g.Active != tt.want || g.ChallengeID != "c1" || !g.StartsAt.Equal(start) || !g.EndsAt.Equal(end) || g.Location == nil || g.Location.String() != "America/Toronto" {
				t.Fatalf("participation %+v, want active %v on c1 in Toronto", g, tt.want)
			}
		})
	}
}

func TestParticipationsUnknownZoneAndFailure(t *testing.T) {
	p := participations{membershipsFunc(func(context.Context, string) ([]challenge.UserChallenge, error) {
		return []challenge.UserChallenge{{ID: "c1", Timezone: "Mars/Olympus"}}, nil
	})}
	if got, err := p.Participations(t.Context(), "u1"); err == nil {
		t.Fatalf("Participations() = %+v, want an error for an unknown time zone", got)
	}

	storage := errors.New("connection refused")
	p = participations{membershipsFunc(func(context.Context, string) ([]challenge.UserChallenge, error) { return nil, storage })}
	if _, err := p.Participations(t.Context(), "u1"); !errors.Is(err, storage) {
		t.Fatalf("error = %v, want %v", err, storage)
	}
}
