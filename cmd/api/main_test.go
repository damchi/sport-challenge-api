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
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/damchi/sport-challenge-api/internal/health"
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
)

func TestNewLoggerIsCloudLoggingCompatible(t *testing.T) {
	tests := []struct {
		level    slog.Level
		severity string
	}{
		{slog.LevelDebug, "DEBUG"},
		{slog.LevelInfo, "INFO"},
		{slog.LevelWarn, "WARNING"},
		{slog.LevelError, "ERROR"},
	}
	for _, tt := range tests {
		t.Run(tt.severity, func(t *testing.T) {
			var out bytes.Buffer
			newLogger(&out, slog.LevelDebug).Log(t.Context(), tt.level, "hello")

			var line map[string]any
			if err := json.Unmarshal(out.Bytes(), &line); err != nil {
				t.Fatalf("not JSON: %q", out.String())
			}
			if line["severity"] != tt.severity || line["message"] != "hello" || line["time"] == nil {
				t.Fatalf("line = %v, want severity %s, message and time", line, tt.severity)
			}
			if ts, _ := line["time"].(string); !strings.HasSuffix(ts, "Z") {
				t.Fatalf("time = %v, want UTC", line["time"])
			}
			if _, ok := line["level"]; ok {
				t.Fatalf("line still has level: %v", line)
			}
		})
	}
}

func TestNewLoggerFiltersBelowLevel(t *testing.T) {
	var out bytes.Buffer
	newLogger(&out, slog.LevelWarn).Info("hidden")
	if out.Len() != 0 {
		t.Fatalf("info written at warn level: %q", out.String())
	}
}

func TestLogRoutesAtDebugLevel(t *testing.T) {
	routes := gin.RoutesInfo{{Method: "GET", Path: "/healthz"}}

	var out bytes.Buffer
	logRoutes(newLogger(&out, slog.LevelDebug), routes)
	var line map[string]any
	if err := json.Unmarshal(out.Bytes(), &line); err != nil {
		t.Fatalf("not JSON: %q", out.String())
	}
	got, _ := line["routes"].([]any)
	if line["message"] != "routes registered" || len(got) != 1 || got[0] != "GET /healthz" {
		t.Fatalf("line = %v, want the GET /healthz route", line)
	}

	out.Reset()
	logRoutes(newLogger(&out, slog.LevelInfo), routes)
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
			pingDatabase(t.Context(), newLogger(&out, slog.LevelDebug), func(ctx context.Context) error {
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

func newTestRouter(t *testing.T, verifier auth.TokenVerifier, resolve auth.UserIDResolver) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	return newRouter(logger, health.NewHandler(pingerFunc(func(context.Context) error { return nil }), logger), verifier, resolve)
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
	r.GET("/api/v1/challenges/:challengeId", func(c *gin.Context) { c.Status(http.StatusNoContent) })

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
	if status, code := serve(t, r, httptest.NewRequest(http.MethodGet, "/api/v1/challenges/x/", nil)); status != http.StatusUnauthorized || code != "unauthenticated" {
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
			r.GET("/api/v1/me", func(c *gin.Context) {
				if id, _ := auth.UserID(c); id != userID {
					t.Errorf("user ID = %q, want %q", id, userID)
				}
				c.Status(http.StatusNoContent)
			})

			req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
			req.Header.Set("Authorization", "Bearer some.access.token")
			if status, code := serve(t, r, req); status != tt.wantStatus || code != tt.wantCode {
				t.Fatalf("got %d %q, want %d %q", status, code, tt.wantStatus, tt.wantCode)
			}
		})
	}
}

func TestPreflightSkipsAuthentication(t *testing.T) {
	r := newTestRouter(t, rejectingVerifier{t}, func(context.Context, string) (string, error) {
		t.Error("user resolved for a preflight")
		return "", nil
	})
	r.GET("/api/v1/me", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	// No CORS middleware yet (SC-API-012): the preflight reaches no route.
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer some.access.token")
	if status, code := serve(t, r, req); status == http.StatusUnauthorized || code == "unauthenticated" {
		t.Fatalf("preflight got %d %q, want no authentication", status, code)
	}
}

type acceptingVerifier struct{}

func (acceptingVerifier) Verify(context.Context, string) (auth.Claims, error) {
	return auth.Claims{Subject: "email|123"}, nil
}
