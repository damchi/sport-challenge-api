package leaderboard

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/damchi/sport-challenge-api/internal/api"
	"github.com/damchi/sport-challenge-api/internal/api/apitest"
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/httpx"
	"github.com/damchi/sport-challenge-api/internal/scoring"
)

const (
	challengeID = "8a0c2f4e-1b3d-4e5f-9a7b-6c8d0e2f4a6b"
	ann         = "11111111-1111-4111-8111-111111111111"
	bob         = "22222222-2222-4222-8222-222222222222"
	gone        = "33333333-3333-4333-8333-333333333333"
)

// adminToken is the access token acceptingVerifier grants the admin
// permission to; it accepts any other one without it.
const adminToken = "admin.access.token"

type acceptingVerifier struct{}

func (acceptingVerifier) Verify(_ context.Context, raw string) (auth.Claims, error) {
	return auth.Claims{Subject: "email|123", Admin: raw == adminToken}, nil
}

type leaderboardOnly struct {
	*Handler
	apitest.Others
}

// get serves GET target as cmd/api does (authentication, contract
// validation, handler) and checks the response against the contract.
func get(t *testing.T, svc *Service, target string, withToken bool) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	token := ""
	if withToken {
		token = "some.access.token"
	}
	return getAs(t, svc, target, token)
}

// getAs is get with token as the access token, none if empty.
func getAs(t *testing.T, svc *Service, target, token string) (*httptest.ResponseRecorder, map[string]any) {
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
		auth.CurrentUser(func(context.Context, auth.Claims) (string, error) { return ann, nil }, logger),
	))
	api.RegisterHandlersWithOptions(r, api.NewStrictHandlerWithOptions(leaderboardOnly{Handler: NewHandler(svc)}, nil, api.StrictGinServerOptions{
		RequestErrorHandlerFunc:  httpx.BadRequestHandler(logger),
		HandlerErrorFunc:         httpx.ErrorHandler(logger),
		ResponseErrorHandlerFunc: httpx.ErrorHandler(logger),
	}), api.GinServerOptions{
		ErrorHandler: httpx.InvalidParamHandler(logger),
		Middlewares:  []api.MiddlewareFunc{api.MiddlewareFunc(validate)},
	})

	req := httptest.NewRequest(http.MethodGet, target, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	apitest.ValidateResponse(t, httptest.NewRequest(http.MethodGet, target, nil), rec)

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

// handlerService ranks a challenge of 2 weeks, one week in: ann is on her
// way, bob was eliminated, gone abandoned.
func handlerService(t *testing.T, challenges *fakeChallenges) *Service {
	t.Helper()
	c := twoWeeks(t)
	loc := toronto(t)
	run := func(user string, day, distance int) scoring.Activity {
		return scoring.Activity{
			UserID: user, SportCode: "run", StartedAt: time.Date(2026, 6, day, 7, 0, 0, 0, loc).UTC(),
			DurationSeconds: 1800, DistanceMeters: meters(distance), LongestSeconds: 1800, Sessions: 1,
		}
	}
	repo := &fakeRepo{
		participants: []scoring.Participant{{UserID: ann}, {UserID: bob}, {UserID: gone, Abandoned: true}},
		activities:   []scoring.Activity{run(ann, 2, 5000), run(ann, 9, 4000), run(bob, 2, 5000), run(gone, 2, 9000)},
	}
	if challenges.c.ID == "" {
		challenges.c = c
	}
	// Week 2 is running, a day and a half from its end.
	now := c.EndsAt.Add(-36 * time.Hour)
	profiles := fakeProfiles{names: map[string]Name{ann: annName, bob: bobName}}
	return NewService(challenges, profiles, repo, clock.NewFake(now), 24*time.Hour)
}

func TestGetLeaderboardAnswersEveryCategory(t *testing.T) {
	svc := handlerService(t, &fakeChallenges{})
	rec, body := get(t, svc, "/api/v1/challenges/"+challengeID+"/leaderboard", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d (%s), want 200", rec.Code, rec.Body)
	}
	if body["computed_at"] == nil {
		t.Fatalf("body %v has no computed_at", body)
	}
	categories := body["categories"].([]any)
	if len(categories) != 3 {
		t.Fatalf("%d categories, want 3", len(categories))
	}

	timeCategory := categories[0].(map[string]any)
	if timeCategory["type"] != "cumulative_time" {
		t.Fatalf("first category %v, want cumulative_time", timeCategory["type"])
	}
	// Ann and Bob; the one who abandoned is listed, not counted.
	if timeCategory["ranked_count"] != 2.0 {
		t.Fatalf("ranked_count = %v, want 2", timeCategory["ranked_count"])
	}
	entries := timeCategory["entries"].([]any)
	first := entries[0].(map[string]any)
	if first["rank"] != 1.0 || first["value"] != 3600.0 || first["abandoned"] != false || first["elimination"] != nil {
		t.Fatalf("first entry %v, want rank 1, 3600 s, not abandoned, no elimination", first)
	}
	user := first["user"].(map[string]any)
	if user["id"] != ann || user["pseudo"] != "Ann" || user["first_name"] != "Ann" || user["last_name"] != "Archer" || user["avatar"] != nil {
		t.Fatalf("user %v, want ann with her pseudo, her names and no avatar", user)
	}
	// Never the e-mail of another user.
	if _, ok := user["email"]; ok {
		t.Fatalf("user %v carries an e-mail", user)
	}
	// Abandoned: last, with no rank, no value.
	last := entries[len(entries)-1].(map[string]any)
	if last["abandoned"] != true || last["rank"] != nil || last["value"] != nil || last["user"].(map[string]any)["pseudo"] != nil {
		t.Fatalf("last entry %v, want an abandoned one with no rank, no value, no pseudo", last)
	}

	elimination := categories[2].(map[string]any)["entries"].([]any)
	annLine := elimination[0].(map[string]any)["elimination"].(map[string]any)
	if annLine["status"] != "in_progress" || annLine["minimum_meters"] != 7000.0 || annLine["distance_meters"] != 4000.0 || annLine["eliminated_week"] != nil || annLine["week_ends_at"] == nil {
		t.Fatalf("ann's progress %v, want in progress, 4000 of 7000 m, no elimination", annLine)
	}
}

// TestGetLeaderboardAnswersTheCategoriesOfTheChallenge: 1, 2 or 3
// categories, the answer holds those of the challenge, in their order, and
// matches the contract.
func TestGetLeaderboardAnswersTheCategoriesOfTheChallenge(t *testing.T) {
	all := twoWeeks(t).Categories
	tests := []struct {
		name       string
		categories []Category
		want       []string
	}{
		{"cumulative time only", all[:1], []string{"cumulative_time"}},
		{"with the distance", all[:2], []string{"cumulative_time", "cumulative_distance"}},
		{"with the elimination", []Category{all[0], all[2]}, []string{"cumulative_time", "elimination"}},
		{"every category", all, []string{"cumulative_time", "cumulative_distance", "elimination"}},
		{"with the points", append(all, Category{Type: CategoryPoints, Criteria: []string{scoring.CriterionLongestDuration}, Podium: 5, MinSessionSeconds: 1200}), []string{
			"cumulative_time", "cumulative_distance", "elimination", "points",
		}},
		{"with the elevation", []Category{all[0], {Type: CategoryElevation}}, []string{"cumulative_time", "cumulative_elevation"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := twoWeeks(t)
			c.Categories = tt.categories
			svc := handlerService(t, &fakeChallenges{c: c})
			rec, body := get(t, svc, "/api/v1/challenges/"+challengeID+"/leaderboard", true)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d (%s), want 200", rec.Code, rec.Body)
			}
			categories := body["categories"].([]any)
			got := make([]string, len(categories))
			for i, cat := range categories {
				got[i], _ = cat.(map[string]any)["type"].(string)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("categories %v, want %v", got, tt.want)
			}
		})
	}
}

// TestGetLeaderboardAnswersProvisionalPoints: in the points category, the
// points of the running week are apart, null for an abandoned participant;
// the other categories have none.
func TestGetLeaderboardAnswersProvisionalPoints(t *testing.T) {
	c := twoWeeks(t)
	c.Categories = []Category{{Type: CategoryCumulativeTime}, {Type: CategoryPoints, Criteria: []string{scoring.CriterionLongestDuration}, Podium: 5, MinSessionSeconds: 1200}}
	svc := handlerService(t, &fakeChallenges{c: c})
	rec, body := get(t, svc, "/api/v1/challenges/"+challengeID+"/leaderboard", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d (%s), want 200", rec.Code, rec.Body)
	}
	categories := body["categories"].([]any)
	for _, e := range categories[0].(map[string]any)["entries"].([]any) {
		if v, ok := e.(map[string]any)["provisional_points"]; !ok || v != nil {
			t.Fatalf("time entry %v, want null provisional points", e)
		}
	}
	// Week 1 is locked, ann and bob equal in it; week 2 runs, ann alone in it.
	want := map[string][3]any{ann: {1.0, 10.0, 10.0}, bob: {1.0, 10.0, 0.0}, gone: {nil, nil, nil}}
	for _, e := range categories[1].(map[string]any)["entries"].([]any) {
		entry := e.(map[string]any)
		id := entry["user"].(map[string]any)["id"].(string)
		if got := [3]any{entry["rank"], entry["value"], entry["provisional_points"]}; got != want[id] {
			t.Fatalf("points of %s: rank, value, provisional %v, want %v", id, got, want[id])
		}
	}
}

func TestGetLeaderboardHidesAChallengeTheActorMayNotRead(t *testing.T) {
	svc := handlerService(t, &fakeChallenges{err: apperr.New(apperr.CodeChallengeNotFound, nil)})
	rec, body := get(t, svc, "/api/v1/challenges/"+challengeID+"/leaderboard", true)
	if rec.Code != http.StatusNotFound || errorCode(body) != "challenge_not_found" {
		t.Fatalf("status %d (%s), want 404 challenge_not_found", rec.Code, rec.Body)
	}
}

// TestGetLeaderboardPassesTheAdminPermission: the actor asked for the
// challenge carries the admin permission of the token, and only then.
func TestGetLeaderboardPassesTheAdminPermission(t *testing.T) {
	for token, admin := range map[string]bool{adminToken: true, "some.access.token": false} {
		challenges := &fakeChallenges{}
		rec, _ := getAs(t, handlerService(t, challenges), "/api/v1/challenges/"+challengeID+"/leaderboard", token)
		if rec.Code != http.StatusOK || challenges.actor != (auth.Actor{ID: ann, Admin: admin}) {
			t.Fatalf("token %s: status %d, actor %+v, want 200 and admin %v", token, rec.Code, challenges.actor, admin)
		}
	}
}

func TestGetLeaderboardRefusesBadRequests(t *testing.T) {
	svc := handlerService(t, &fakeChallenges{})
	tests := []struct {
		name      string
		target    string
		withToken bool
		wantCode  int
		wantError string
	}{
		{name: "no token", target: "/api/v1/challenges/" + challengeID + "/leaderboard", wantCode: http.StatusUnauthorized, wantError: "unauthenticated"},
		{name: "challenge id not a UUID", target: "/api/v1/challenges/nope/leaderboard", withToken: true, wantCode: http.StatusBadRequest, wantError: "validation_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, body := get(t, svc, tt.target, tt.withToken)
			if rec.Code != tt.wantCode || errorCode(body) != tt.wantError {
				t.Fatalf("status %d (%s), want %d %s", rec.Code, rec.Body, tt.wantCode, tt.wantError)
			}
		})
	}
}
