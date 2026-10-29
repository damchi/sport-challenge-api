package health

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/damchi/sport-challenge-api/internal/api"
	"github.com/damchi/sport-challenge-api/internal/api/apitest"
	"github.com/damchi/sport-challenge-api/internal/platform/httpx"
)

type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

// serve sends a GET to path through the real router and checks the response
// against the contract.
func serve(t *testing.T, db Pinger, path string) *httptest.ResponseRecorder {
	t.Helper()
	return serveWithLogs(t, db, path, io.Discard)
}

func serveWithLogs(t *testing.T, db Pinger, path string, logs io.Writer) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	r := httpx.NewRouter(logger)
	api.RegisterHandlers(r, api.NewStrictHandler(NewHandler(db, logger), nil))

	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	apitest.ValidateResponse(t, req, rec)
	return rec
}

func TestGetHealthz(t *testing.T) {
	// Liveness does not depend on the database.
	rec := serve(t, fakePinger{err: errors.New("down")}, "/healthz")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "{\"status\":\"ok\"}\n" {
		t.Fatalf("body = %q, want {\"status\":\"ok\"}", got)
	}
}

func TestGetReadyz(t *testing.T) {
	tests := []struct {
		name       string
		pingErr    error
		wantStatus int
		wantBody   string
	}{
		{name: "database available", wantStatus: http.StatusOK, wantBody: "{\"status\":\"ok\"}\n"},
		{
			name:       "database unavailable",
			pingErr:    errors.New("dial tcp 10.0.0.1:5432: connection refused"),
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   "{\"status\":\"unavailable\"}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(t, fakePinger{err: tt.pingErr}, "/readyz")

			if rec.Code != tt.wantStatus || rec.Body.String() != tt.wantBody {
				t.Fatalf("got %d %q, want %d %q", rec.Code, rec.Body, tt.wantStatus, tt.wantBody)
			}
		})
	}
}

func TestGetReadyzLogsWhyItIsNotReady(t *testing.T) {
	var logs bytes.Buffer
	serveWithLogs(t, fakePinger{err: errors.New("connection refused")}, "/readyz", &logs)

	if !strings.Contains(logs.String(), "database not ready") || !strings.Contains(logs.String(), "connection refused") {
		t.Fatalf("logs = %s, want the ping error", logs.String())
	}
}

func TestGetReadyzBoundsThePing(t *testing.T) {
	var deadline bool
	db := pingerFunc(func(ctx context.Context) error {
		_, deadline = ctx.Deadline()
		return nil
	})
	serve(t, db, "/readyz")

	if !deadline {
		t.Fatal("ping context has no deadline")
	}
}

type pingerFunc func(context.Context) error

func (f pingerFunc) Ping(ctx context.Context) error { return f(ctx) }
