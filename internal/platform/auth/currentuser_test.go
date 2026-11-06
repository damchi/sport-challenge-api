package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/httpx"
)

const localUserID = "0b9d4a5e-6a43-4c8e-9d1f-7a2b3c4d5e6f"

func decodeCode(t *testing.T, body []byte) string {
	t.Helper()
	if len(body) == 0 {
		return ""
	}
	var res struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("body is not the error format: %s", body)
	}
	return res.Error.Code
}

func TestCurrentUser(t *testing.T) {
	tests := []struct {
		name          string
		authorization string
		resolveID     string
		resolveErr    error
		clientGone    bool
		wantStatus    int
		wantCode      string
		wantLog       string
	}{
		{name: "valid token", authorization: "Bearer " + goodToken, resolveID: localUserID, wantStatus: http.StatusNoContent},
		{name: "no token", wantStatus: 401, wantCode: "unauthenticated"},
		{name: "deleted account", authorization: "Bearer " + goodToken, resolveErr: fmt.Errorf("get or create user: %w", apperr.New(apperr.CodeAccountDeleted, nil)), wantStatus: 401, wantCode: "account_deleted"},
		{name: "database failure", authorization: "Bearer " + goodToken, resolveErr: errors.New("insert user: connection refused"), wantStatus: 500, wantCode: "internal_error", wantLog: "request failed"},
		{name: "empty identifier", authorization: "Bearer " + goodToken, wantStatus: 500, wantCode: "internal_error", wantLog: "empty identifier"},
		{name: "client gone", authorization: "Bearer " + goodToken, resolveErr: fmt.Errorf("read user: %w", context.Canceled), clientGone: true, wantStatus: statusClientClosedRequest},
		{name: "cancelled by the server", authorization: "Bearer " + goodToken, resolveErr: fmt.Errorf("read user: %w", context.Canceled), wantStatus: 500, wantCode: "internal_error", wantLog: "request failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			var gotSubject, gotID string
			var served bool
			resolve := func(_ context.Context, subject string) (string, error) {
				gotSubject = subject
				return tt.resolveID, tt.resolveErr
			}

			r := httpx.NewRouter(logger)
			r.Use(Protect(nil, Authenticate(&fakeVerifier{}, logger), CurrentUser(resolve, logger)))
			r.GET("/private", func(c *gin.Context) {
				served = true
				// The generated handlers receive the *gin.Context as context.Context.
				var ctx context.Context = c
				gotID, _ = UserID(ctx)
				c.Status(http.StatusNoContent)
			})

			req := httptest.NewRequest(http.MethodGet, "/private", nil)
			if tt.authorization != "" {
				req.Header.Set("Authorization", tt.authorization)
			}
			if tt.clientGone {
				ctx, cancel := context.WithCancel(req.Context())
				cancel()
				req = req.WithContext(ctx)
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if code := decodeCode(t, rec.Body.Bytes()); rec.Code != tt.wantStatus || code != tt.wantCode {
				t.Fatalf("got %d %q, want %d %q", rec.Code, code, tt.wantStatus, tt.wantCode)
			}
			if served != (tt.wantStatus == http.StatusNoContent) {
				t.Fatalf("route served = %v with status %d", served, rec.Code)
			}
			if served && (gotID != localUserID || gotSubject != "email|123") {
				t.Fatalf("user ID = %q for subject %q, want %q for email|123", gotID, gotSubject, localUserID)
			}
			if tt.wantLog != "" && !strings.Contains(logs.String(), tt.wantLog) {
				t.Fatalf("logs = %s, want %q", logs.String(), tt.wantLog)
			}
		})
	}
}

func TestCurrentUserWithoutClaimsIsUnauthenticated(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	r := httpx.NewRouter(logger)
	r.GET("/private", CurrentUser(func(context.Context, string) (string, error) {
		t.Error("resolver called without claims")
		return localUserID, nil
	}, logger), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/private", nil))

	if code := decodeCode(t, rec.Body.Bytes()); rec.Code != http.StatusUnauthorized || code != "unauthenticated" {
		t.Fatalf("got %d %q, want 401 unauthenticated", rec.Code, code)
	}
}

func TestUserIDOutsideAProtectedRoute(t *testing.T) {
	if id, ok := UserID(t.Context()); ok || id != "" {
		t.Fatalf("UserID() = %q, %v, want nothing", id, ok)
	}
}

func TestProtect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	var ran []string
	step := func(name string, abort bool) gin.HandlerFunc {
		return func(c *gin.Context) {
			ran = append(ran, name)
			if abort {
				c.AbortWithStatus(http.StatusUnauthorized)
			}
		}
	}

	tests := []struct {
		name       string
		method     string
		path       string
		abortFirst bool
		wantRan    string
		wantStatus int
	}{
		{name: "public route", method: http.MethodGet, path: "/open", wantRan: "", wantStatus: http.StatusNoContent},
		{name: "public path, other method", method: http.MethodPost, path: "/open", wantRan: "first,second", wantStatus: http.StatusNoContent},
		{name: "route not listed", method: http.MethodGet, path: "/items/42", wantRan: "first,second", wantStatus: http.StatusNoContent},
		{name: "first step aborts", method: http.MethodGet, path: "/items/42", abortFirst: true, wantRan: "first", wantStatus: http.StatusUnauthorized},
		{name: "unknown path", method: http.MethodGet, path: "/nowhere", abortFirst: true, wantRan: "first", wantStatus: http.StatusUnauthorized},
		{name: "preflight", method: http.MethodOptions, path: "/items/42", wantRan: "", wantStatus: http.StatusNotFound},
		{name: "registered OPTIONS route", method: http.MethodOptions, path: "/open", wantRan: "first,second", wantStatus: http.StatusNoContent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ran = nil
			r := httpx.NewRouter(logger)
			r.Use(Protect([]string{"GET /open"}, step("first", tt.abortFirst), step("second", false)))
			ok := func(c *gin.Context) { c.Status(http.StatusNoContent) }
			r.GET("/open", ok)
			r.POST("/open", ok)
			r.OPTIONS("/open", ok)
			r.GET("/items/:id", ok)

			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))

			if got := strings.Join(ran, ","); got != tt.wantRan || rec.Code != tt.wantStatus {
				t.Fatalf("ran %q, status %d, want %q, %d", got, rec.Code, tt.wantRan, tt.wantStatus)
			}
		})
	}
}
