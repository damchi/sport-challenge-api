package health

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/damchi/sport-challenge-api/internal/api"
	"github.com/damchi/sport-challenge-api/internal/api/apitest"
	"github.com/damchi/sport-challenge-api/internal/platform/httpx"
)

func TestGetHealthz(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := httpx.NewRouter(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	api.RegisterHandlers(r, api.NewStrictHandler(Handler{}, nil))

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "{\"status\":\"ok\"}\n" {
		t.Fatalf("body = %q, want {\"status\":\"ok\"}", got)
	}
	apitest.ValidateResponse(t, req, rec)
}
