package sport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/damchi/sport-challenge-api/internal/api"
	"github.com/damchi/sport-challenge-api/internal/api/apitest"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/httpx"
)

type fakeRepository struct {
	sports []Sport
	err    error
}

func (f fakeRepository) List(context.Context) ([]Sport, error) { return f.sports, f.err }

type acceptingVerifier struct{}

func (acceptingVerifier) Verify(context.Context, string) (auth.Claims, error) {
	return auth.Claims{Subject: "email|123"}, nil
}

// sportsOnly completes the contract with the operations of other modules,
// never called here.
type sportsOnly struct {
	*Handler
	apitest.Others
}

// getSports serves GET /api/v1/sports as cmd/api does (authentication, then
// the handler on repo) and checks the response against the contract.
func getSports(t *testing.T, repo repository) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	r := httpx.NewRouter(logger)
	r.Use(auth.Protect(nil,
		auth.Authenticate(acceptingVerifier{}, logger),
		auth.CurrentUser(func(context.Context, string) (string, error) { return "0b9d4a5e-6a43-4c8e-9d1f-7a2b3c4d5e6f", nil }, logger),
	))
	api.RegisterHandlers(r, api.NewStrictHandlerWithOptions(sportsOnly{Handler: NewHandler(NewService(repo))}, nil, api.StrictGinServerOptions{
		RequestErrorHandlerFunc:  httpx.BadRequestHandler(logger),
		HandlerErrorFunc:         httpx.ErrorHandler(logger),
		ResponseErrorHandlerFunc: httpx.ErrorHandler(logger),
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sports", nil)
	req.Header.Set("Authorization", "Bearer some.access.token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	apitest.ValidateResponse(t, req, rec)
	return rec
}

func factor(v float64) *float64 { return &v }

func TestGetSports(t *testing.T) {
	rec := getSports(t, fakeRepository{sports: []Sport{
		{Code: "other", DistanceMode: "optional", SuggestedFactor: factor(1)},
		{Code: "ride", DistanceMode: "required", SuggestedFactor: factor(0.3)},
		{Code: "yoga", DistanceMode: "none"},
	}})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Cache-Control"); got != "private, max-age=300" {
		t.Fatalf("Cache-Control = %q, want private, max-age=300", got)
	}
	var body struct {
		Sports []map[string]any `json:"sports"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{
		{"code": "other", "distance_mode": "optional", "suggested_factor": 1.0},
		{"code": "ride", "distance_mode": "required", "suggested_factor": 0.3},
		{"code": "yoga", "distance_mode": "none", "suggested_factor": nil},
	}
	if len(body.Sports) != len(want) {
		t.Fatalf("sports = %v, want %v", body.Sports, want)
	}
	for i, w := range want {
		for k, v := range w {
			if got, ok := body.Sports[i][k]; !ok || got != v {
				t.Fatalf("sports[%d][%s] = %v, want %v", i, k, got, v)
			}
		}
	}
}

func TestGetSportsEmptyCatalogueIsAnEmptyList(t *testing.T) {
	rec := getSports(t, fakeRepository{})
	if rec.Code != http.StatusOK || rec.Body.String() != "{\"sports\":[]}\n" {
		t.Fatalf("got %d %s, want 200 with an empty list", rec.Code, rec.Body)
	}
}

func TestGetSportsStorageFailure(t *testing.T) {
	rec := getSports(t, fakeRepository{err: errors.New("connection refused")})
	if rec.Code != http.StatusInternalServerError || rec.Header().Get("Cache-Control") != "" {
		t.Fatalf("got %d, Cache-Control %q, want 500 without cache", rec.Code, rec.Header().Get("Cache-Control"))
	}
}
