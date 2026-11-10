package activity

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/damchi/sport-challenge-api/internal/api"
	"github.com/damchi/sport-challenge-api/internal/api/apitest"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/httpx"
)

type acceptingVerifier struct{}

func (acceptingVerifier) Verify(context.Context, string) (auth.Claims, error) {
	return auth.Claims{Subject: "email|123"}, nil
}

type activitiesOnly struct {
	*Handler
	apitest.Others
}

// postActivity serves POST /api/v1/activities as cmd/api does
// (authentication, contract validation, handler) and checks the response
// against the contract. headers are added to the request; an Authorization
// of "" leaves the request without a token.
func postActivity(t *testing.T, svc *Service, body string, headers map[string]string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	spec, err := api.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	validate, err := httpx.RequestValidator(spec, logger)
	if err != nil {
		t.Fatal(err)
	}
	r := httpx.NewRouter(logger)
	r.Use(auth.Protect(nil,
		auth.Authenticate(acceptingVerifier{}, logger),
		auth.CurrentUser(func(context.Context, string) (string, error) { return actor, nil }, logger),
	))
	api.RegisterHandlersWithOptions(r, api.NewStrictHandlerWithOptions(activitiesOnly{Handler: NewHandler(svc)}, nil, api.StrictGinServerOptions{
		RequestErrorHandlerFunc:  httpx.BadRequestHandler(logger),
		HandlerErrorFunc:         httpx.ErrorHandler(logger),
		ResponseErrorHandlerFunc: httpx.ErrorHandler(logger),
	}), api.GinServerOptions{
		ErrorHandler: httpx.InvalidParamHandler(logger),
		Middlewares:  []api.MiddlewareFunc{api.MiddlewareFunc(validate)},
	})

	const target = "/api/v1/activities"
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer some.access.token")
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		if v == "" {
			req.Header.Del(k)
			continue
		}
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	apitest.ValidateResponse(t, httptest.NewRequest(http.MethodPost, target, nil), rec)

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q is not JSON: %v", rec.Body.String(), err)
	}
	return rec, got
}

func errorCode(body map[string]any) any {
	e, _ := body["error"].(map[string]any)
	return e["code"]
}

const runBody = `{"sport_code":"run","started_at":"2026-06-10T11:00:00Z","duration_seconds":1500,"distance_meters":5000}`

func TestCreateActivityRecords(t *testing.T) {
	repo := &fakeRepository{}
	c1 := challengeOf(t, "8a0c2f4e-1b3d-4e5f-9a7b-6c8d0e2f4a6b")
	svc := newService(t, repo, fakeParticipations{challenges: []Participation{c1}})
	rec, body := postActivity(t, svc, runBody, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d (%s), want 201", rec.Code, rec.Body)
	}
	// 5 km in 25 min: 3.33 m/s, 300 s/km.
	if body["source"] != "manual" || body["pace_seconds_per_km"] != 300.0 || body["speed_mps"] != 5000.0/1500 || body["sport_label"] != nil {
		t.Fatalf("body %v, want a manual activity with its computed speed", body)
	}
	if ids, _ := body["challenge_ids"].([]any); len(ids) != 1 || ids[0] != c1.ChallengeID {
		t.Fatalf("challenge_ids %v, want [%s]", body["challenge_ids"], c1.ChallengeID)
	}
}

func TestCreateActivityWithoutDistanceOrChallenge(t *testing.T) {
	svc := newService(t, &fakeRepository{}, fakeParticipations{})
	rec, body := postActivity(t, svc, `{"sport_code":"yoga","started_at":"2026-06-10T11:00:00Z","duration_seconds":3600}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d (%s), want 201", rec.Code, rec.Body)
	}
	if body["distance_meters"] != nil || body["speed_mps"] != nil || body["pace_seconds_per_km"] != nil {
		t.Fatalf("body %v, want no distance and no speed", body)
	}
	// An empty list, never null.
	if ids, ok := body["challenge_ids"].([]any); !ok || len(ids) != 0 {
		t.Fatalf("challenge_ids %v, want []", body["challenge_ids"])
	}
}

func TestCreateActivityIdempotencyKey(t *testing.T) {
	key := "1f0c6e2a-5b7d-4c9e-8a1b-3d5f7e9a0c2b"
	repo := &fakeRepository{}
	svc := newService(t, repo, fakeParticipations{})
	rec, _ := postActivity(t, svc, runBody, map[string]string{"Idempotency-Key": key})
	if rec.Code != http.StatusCreated || repo.recorded.IdempotencyKey == nil || *repo.recorded.IdempotencyKey != key {
		t.Fatalf("status %d, recorded %+v, want 201 with the key", rec.Code, repo.recorded)
	}

	repo.existing = repo.recorded
	repo.recorded = nil
	rec, body := postActivity(t, svc, runBody, map[string]string{"Idempotency-Key": key})
	if rec.Code != http.StatusOK || body["id"] != repo.existing.ID || repo.recorded != nil {
		t.Fatalf("status %d, body %v, want 200 with the first activity and nothing written", rec.Code, body)
	}
}

func TestCreateActivityRefusals(t *testing.T) {
	locked := []Participation{challengeOf(t, "8a0c2f4e-1b3d-4e5f-9a7b-6c8d0e2f4a6b")}
	tests := []struct {
		name    string
		body    string
		headers map[string]string
		status  int
		code    string
	}{
		{name: "no token", body: runBody, headers: map[string]string{"Authorization": ""}, status: http.StatusUnauthorized, code: "unauthenticated"},
		{name: "week locked", body: `{"sport_code":"run","started_at":"2026-06-03T11:00:00Z","duration_seconds":1500,"distance_meters":5000}`, status: http.StatusUnprocessableEntity, code: "week_locked"},
		{name: "distance required", body: `{"sport_code":"run","started_at":"2026-06-10T11:00:00Z","duration_seconds":1500}`, status: http.StatusUnprocessableEntity, code: "distance_required"},
		{name: "unknown sport", body: `{"sport_code":"chess","started_at":"2026-06-10T11:00:00Z","duration_seconds":1500}`, status: http.StatusBadRequest, code: "validation_failed"},
		{name: "speed sent", body: `{"sport_code":"run","started_at":"2026-06-10T11:00:00Z","duration_seconds":1500,"distance_meters":5000,"speed_mps":3}`, status: http.StatusBadRequest, code: "validation_failed"},
		{name: "source sent", body: `{"sport_code":"run","started_at":"2026-06-10T11:00:00Z","duration_seconds":1500,"distance_meters":5000,"source":"strava"}`, status: http.StatusBadRequest, code: "validation_failed"},
		{name: "duration missing", body: `{"sport_code":"run","started_at":"2026-06-10T11:00:00Z","distance_meters":5000}`, status: http.StatusBadRequest, code: "validation_failed"},
		{name: "duration zero", body: `{"sport_code":"run","started_at":"2026-06-10T11:00:00Z","duration_seconds":0,"distance_meters":5000}`, status: http.StatusBadRequest, code: "validation_failed"},
		{name: "key with a space", body: runBody, headers: map[string]string{"Idempotency-Key": "a key"}, status: http.StatusBadRequest, code: "validation_failed"},
		{name: "key too long", body: runBody, headers: map[string]string{"Idempotency-Key": strings.Repeat("k", 129)}, status: http.StatusBadRequest, code: "validation_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{}
			svc := NewService(repo, fakeProfiles{complete: true}, catalogue, fakeParticipations{t: t, challenges: locked}, clock.NewFake(recordNow))
			rec, body := postActivity(t, svc, tt.body, tt.headers)
			if rec.Code != tt.status || errorCode(body) != tt.code {
				t.Fatalf("got %d %v, want %d %s", rec.Code, body, tt.status, tt.code)
			}
			if repo.recorded != nil {
				t.Fatalf("recorded %+v, want nothing", repo.recorded)
			}
		})
	}
}
