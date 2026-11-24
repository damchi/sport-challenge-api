package challenge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/damchi/sport-challenge-api/internal/api"
	"github.com/damchi/sport-challenge-api/internal/api/apitest"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/httpx"
	"github.com/damchi/sport-challenge-api/internal/scoring"
)

// adminToken is the access token acceptingVerifier grants the admin
// permission to; it accepts any other one without it.
const adminToken = "admin.access.token"

type acceptingVerifier struct{}

func (acceptingVerifier) Verify(_ context.Context, raw string) (auth.Claims, error) {
	return auth.Claims{Subject: "email|123", Admin: raw == adminToken}, nil
}

type challengesOnly struct {
	*Handler
	apitest.Others
}

// post serves POST /api/v1/challenges as cmd/api does (authentication,
// contract validation, handler) and checks the response against the
// contract.
func post(t *testing.T, svc *Service, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	return serve(t, svc, http.MethodPost, "/api/v1/challenges", body)
}

func serve(t *testing.T, svc *Service, method, target, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	return serveAs(t, svc, "some.access.token", method, target, body)
}

// serveAs is serve with token as the access token.
func serveAs(t *testing.T, svc *Service, token, method, target, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	spec, err := api.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	validate, err := httpx.RequestValidator(spec, logger, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := httpx.NewRouter(logger)
	r.Use(auth.Protect(nil,
		auth.Authenticate(acceptingVerifier{}, logger),
		auth.CurrentUser(func(context.Context, auth.Claims) (string, error) { return actor, nil }, logger),
	))
	api.RegisterHandlersWithOptions(r, api.NewStrictHandlerWithOptions(challengesOnly{Handler: NewHandler(svc)}, nil, api.StrictGinServerOptions{
		RequestErrorHandlerFunc:  httpx.BadRequestHandler(logger, nil),
		HandlerErrorFunc:         httpx.ErrorHandler(logger),
		ResponseErrorHandlerFunc: httpx.ErrorHandler(logger),
	}), api.GinServerOptions{
		ErrorHandler: httpx.InvalidParamHandler(logger, nil),
		Middlewares:  []api.MiddlewareFunc{api.MiddlewareFunc(validate)},
	})

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	apitest.ValidateResponse(t, httptest.NewRequest(method, target, nil), rec)

	var out map[string]any
	if rec.Body.Len() == 0 { // 204
		return rec, out
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("body is not JSON: %s", rec.Body)
	}
	return rec, out
}

func newTestService(profileComplete bool) (*Service, *fakeRepository) {
	repo := &fakeRepository{}
	return NewService(repo, fakeProfiles{complete: profileComplete}, fakeCatalogue{factors: suggested()}, fakeStandings{}, clock.NewFake(now)), repo
}

const validBody = `{"title":"Spring run","visibility":"public","timezone":"America/Toronto","start_date":"2026-03-04","weeks":6,"categories":{"cumulative_distance":{},"elimination":{"step_meters":2000}},"distance_factors":{"ride":0.25}}`

func TestCreateChallenge(t *testing.T) {
	svc, repo := newTestService(true)
	rec, out := post(t, svc, validBody)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d, want 201: %s", rec.Code, rec.Body)
	}
	if out["title"] != "Spring run" || out["starts_at"] != "2026-03-04T05:00:00Z" || out["members_can_invite"] != false {
		t.Fatalf("body %v, want the created challenge", out)
	}
	cats, _ := out["categories"].([]any)
	if len(cats) != 3 {
		t.Fatalf("categories %v, want 3", out["categories"])
	}
	distance, _ := cats[1].(map[string]any)["params"].(map[string]any)["factors"].(map[string]any)
	if distance["ride"] != 0.25 || distance["walk"] != 1.0 {
		t.Fatalf("frozen factors %v, want ride 0.25 and the suggestions", distance)
	}
	if repo.created == nil || repo.created.CreatedBy != actor {
		t.Fatalf("stored %+v, want a challenge created by the actor from the token", repo.created)
	}
	if repo.created.MembersCanInvite {
		t.Fatal("members_can_invite stored as true, want false when absent")
	}
	if out["reference_sport"] != "run" {
		t.Fatalf("reference_sport %v, want run by default", out["reference_sport"])
	}
}

// TestCreateChallengeOptionalCategories: the answer, checked against the
// contract, holds the chosen categories only, at the position of their type.
func TestCreateChallengeOptionalCategories(t *testing.T) {
	tests := []struct {
		name       string
		categories string // the categories member of the body, empty for none
		want       []string
	}{
		{"cumulative time only", "", []string{"cumulative_time@1"}},
		{"no optional category chosen", `"categories":{},`, []string{"cumulative_time@1"}},
		{"elimination", `"categories":{"elimination":{"step_meters":1500}},`, []string{"cumulative_time@1", "elimination@3"}},
		{"distance", `"categories":{"cumulative_distance":{}},`, []string{"cumulative_time@1", "cumulative_distance@2"}},
		{"points", `"categories":{"points":{"criteria":["session_count"],"podium":5}},`, []string{"cumulative_time@1", "points@4"}},
		{"elevation", `"categories":{"cumulative_elevation":{}},`, []string{"cumulative_time@1", "cumulative_elevation@5"}},
		{"every category", `"categories":{"cumulative_distance":{},"elimination":{"step_meters":1500},"points":{"criteria":["session_count"],"podium":5},"cumulative_elevation":{}},`, []string{
			"cumulative_time@1", "cumulative_distance@2", "elimination@3", "points@4", "cumulative_elevation@5",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newTestService(true)
			body := `{"title":"Spring run","visibility":"public","timezone":"America/Toronto","start_date":"2026-03-04",` + tt.categories + `"weeks":6}`
			rec, out := post(t, svc, body)
			if rec.Code != http.StatusCreated {
				t.Fatalf("status %d, want 201: %s", rec.Code, rec.Body)
			}
			cats, _ := out["categories"].([]any)
			got := make([]string, len(cats))
			for i, c := range cats {
				cat, _ := c.(map[string]any)
				got[i] = fmt.Sprintf("%v@%v", cat["type"], cat["position"])
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("categories %v, want %v", got, tt.want)
			}
		})
	}
}

// TestCreateChallengePoints: the settings of the points category are passed
// on, the minimum session of 20 min by default, and the answer carries them,
// the minimum in seconds and the factors of the longest distance.
func TestCreateChallengePoints(t *testing.T) {
	tests := []struct {
		name         string
		points       string
		wantSecond   float64
		wantCriteria []any
	}{
		{"default minimum session", `{"criteria":["longest_distance","session_count"],"podium":10}`, 1200, []any{"longest_distance", "session_count"}},
		{"chosen minimum session", `{"criteria":["longest_distance","session_count"],"podium":10,"min_session_minutes":45}`, 2700, []any{"longest_distance", "session_count"}},
		{
			"every criterion", `{"criteria":["longest_distance","session_count","longest_duration","weekly_elevation"],"podium":10}`, 1200,
			[]any{"longest_distance", "session_count", "longest_duration", "weekly_elevation"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, repo := newTestService(true)
			body := `{"title":"Spring run","visibility":"public","timezone":"America/Toronto","start_date":"2026-03-04","weeks":6,` +
				`"categories":{"points":` + tt.points + `},"distance_factors":{"ride":0.25}}`
			rec, out := post(t, svc, body)
			if rec.Code != http.StatusCreated {
				t.Fatalf("status %d, want 201: %s", rec.Code, rec.Body)
			}
			cats, _ := out["categories"].([]any)
			params, _ := cats[len(cats)-1].(map[string]any)["params"].(map[string]any)
			factors, _ := params["factors"].(map[string]any)
			criteria, _ := params["criteria"].([]any)
			if !reflect.DeepEqual(criteria, tt.wantCriteria) || params["podium"] != 10.0 || params["min_session_seconds"] != tt.wantSecond || factors["ride"] != 0.25 {
				t.Fatalf("points params %v, want the criteria, a top 10, %v s and ride at 0.25", params, tt.wantSecond)
			}
			if p := repo.created.Categories[len(repo.created.Categories)-1].Params; p.MinSessionSeconds != int(tt.wantSecond) {
				t.Fatalf("stored %d s, want %v", p.MinSessionSeconds, tt.wantSecond)
			}
		})
	}
}

// TestCreateChallengeWithAReferenceSport: the reference is passed on, its
// factor refused, and the answer carries it.
func TestCreateChallengeWithAReferenceSport(t *testing.T) {
	svc, repo := newTestService(true)
	rec, out := post(t, svc, strings.Replace(validBody, `"distance_factors":{"ride":0.25}`, `"reference_sport":"ride","distance_factors":{"run":0.5}`, 1))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d, want 201: %s", rec.Code, rec.Body)
	}
	cats, _ := out["categories"].([]any)
	distance, _ := cats[1].(map[string]any)["params"].(map[string]any)["factors"].(map[string]any)
	elimination, _ := cats[2].(map[string]any)["params"].(map[string]any)
	if out["reference_sport"] != "ride" || repo.created.ReferenceSport != "ride" || distance["ride"] != 1.0 || distance["run"] != 0.5 || elimination["sport"] != "ride" {
		t.Fatalf("body %v, want ride as the reference, ×1, run at 0.5 and the elimination in ride", out)
	}

	// An empty code is not run.
	svc, repo = newTestService(true)
	rec, out = post(t, svc, strings.Replace(validBody, `"distance_factors":{"ride":0.25}`, `"reference_sport":""`, 1))
	e, _ := out["error"].(map[string]any)
	params, _ := e["params"].(map[string]any)
	if rec.Code != http.StatusBadRequest || params["fields"] != "reference_sport" || repo.created != nil {
		t.Fatalf("got %d %v, want 400 validation_failed on reference_sport", rec.Code, out)
	}

	svc, repo = newTestService(true)
	rec, out = post(t, svc, strings.Replace(validBody, `"distance_factors":{"ride":0.25}`, `"reference_sport":"ride","distance_factors":{"ride":1}`, 1))
	e, _ = out["error"].(map[string]any)
	params, _ = e["params"].(map[string]any)
	if rec.Code != http.StatusBadRequest || e["code"] != "validation_failed" || params["fields"] != "distance_factors.ride" || repo.created != nil {
		t.Fatalf("got %d %v, want 400 validation_failed on distance_factors.ride", rec.Code, out)
	}
}

func TestCreateChallengeRefusals(t *testing.T) {
	tests := []struct {
		name       string
		complete   bool
		body       string
		wantStatus int
		wantCode   string
		wantFields string
	}{
		{name: "incomplete profile", body: validBody, wantStatus: 422, wantCode: "profile_incomplete"},
		{name: "rule broken (start in the past)", complete: true, body: strings.Replace(validBody, "2026-03-04", "2026-03-01", 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "start_date"},
		{name: "contract broken (53 weeks, other visibility)", complete: true, body: strings.Replace(strings.Replace(validBody, `"weeks":6`, `"weeks":53`, 1), `"public"`, `"friends"`, 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "visibility,weeks"},
		{name: "null members_can_invite", complete: true, body: strings.Replace(validBody, `"weeks":6`, `"weeks":6,"members_can_invite":null`, 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "members_can_invite"},
		{name: "null reference sport", complete: true, body: strings.Replace(validBody, `"weeks":6`, `"weeks":6,"reference_sport":null`, 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "reference_sport"},
		{name: "reference sport over 32 characters", complete: true, body: strings.Replace(validBody, `"weeks":6`, `"weeks":6,"reference_sport":"`+strings.Repeat("a", 33)+`"`, 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "reference_sport"},
		{name: "factor of an unknown sport: the key is not echoed", complete: true, body: strings.Replace(validBody, `"ride":0.25`, `"<script>":"x"`, 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "distance_factors"},
		{name: "factor out of range", complete: true, body: strings.Replace(validBody, `"ride":0.25`, `"ride":0.123`, 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "distance_factors.ride"},
		{name: "missing title", complete: true, body: strings.Replace(validBody, `"title":"Spring run",`, "", 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "title"},
		{name: "NUL in the title: 400, not a 500 from the database", complete: true, body: strings.Replace(validBody, `"Spring run"`, `"Spring\u0000run"`, 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "title"},
		{name: "categories as a list", complete: true, body: strings.Replace(validBody, `"categories":{"cumulative_distance":{},"elimination":{"step_meters":2000}}`, `"categories":[]`, 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "categories"},
		{name: "unknown category", complete: true, body: strings.Replace(validBody, `"cumulative_distance":{}`, `"cumulative_time":{}`, 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "categories"},
		{name: "settings of the cumulative distance", complete: true, body: strings.Replace(validBody, `"cumulative_distance":{}`, `"cumulative_distance":{"factors":{}}`, 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "categories.cumulative_distance"},
		{name: "elimination step of 0", complete: true, body: strings.Replace(validBody, `"step_meters":2000`, `"step_meters":0`, 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "categories.elimination.step_meters"},
		{name: "elimination step over 100 km", complete: true, body: strings.Replace(validBody, `"step_meters":2000`, `"step_meters":100001`, 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "categories.elimination.step_meters"},
		{name: "elimination without step", complete: true, body: strings.Replace(validBody, `{"step_meters":2000}`, `{}`, 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "categories.elimination.step_meters"},
		{name: "null categories", complete: true, body: strings.Replace(validBody, `"categories":{"cumulative_distance":{},"elimination":{"step_meters":2000}}`, `"categories":null`, 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "categories"},
		{name: "settings of the cumulative elevation", complete: true, body: strings.Replace(validBody, `"cumulative_distance":{}`, `"cumulative_distance":{},"cumulative_elevation":{"factors":{}}`, 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "categories.cumulative_elevation"},
		{name: "null cumulative elevation", complete: true, body: strings.Replace(validBody, `"cumulative_distance":{}`, `"cumulative_distance":{},"cumulative_elevation":null`, 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "categories.cumulative_elevation"},
		{name: "null cumulative distance", complete: true, body: strings.Replace(validBody, `"cumulative_distance":{}`, `"cumulative_distance":null`, 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "categories.cumulative_distance"},
		{name: "null elimination", complete: true, body: strings.Replace(validBody, `"elimination":{"step_meters":2000}`, `"elimination":null`, 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "categories.elimination"},
		{name: "points without criterion", complete: true, body: strings.Replace(validBody, `"elimination":{"step_meters":2000}`, `"points":{"criteria":[],"podium":5}`, 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "categories.points.criteria"},
		{name: "points criterion twice", complete: true, body: strings.Replace(validBody, `"elimination":{"step_meters":2000}`, `"points":{"criteria":["session_count","session_count"],"podium":5}`, 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "categories.points.criteria"},
		{name: "points criteria 5 times", complete: true, body: strings.Replace(validBody, `"elimination":{"step_meters":2000}`, `"points":{"criteria":["session_count","longest_duration","longest_distance","weekly_elevation","weekly_elevation"],"podium":5}`, 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "categories.points.criteria"},
		{name: "unknown points criterion", complete: true, body: strings.Replace(validBody, `"elimination":{"step_meters":2000}`, `"points":{"criteria":["fastest"],"podium":5}`, 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "categories.points.criteria.0"},
		{name: "points in a top 3", complete: true, body: strings.Replace(validBody, `"elimination":{"step_meters":2000}`, `"points":{"criteria":["session_count"],"podium":3}`, 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "categories.points.podium"},
		{name: "points without podium", complete: true, body: strings.Replace(validBody, `"elimination":{"step_meters":2000}`, `"points":{"criteria":["session_count"]}`, 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "categories.points.podium"},
		{name: "points session of 0 min", complete: true, body: strings.Replace(validBody, `"elimination":{"step_meters":2000}`, `"points":{"criteria":["session_count"],"podium":5,"min_session_minutes":0}`, 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "categories.points.min_session_minutes"},
		{name: "points session over 180 min", complete: true, body: strings.Replace(validBody, `"elimination":{"step_meters":2000}`, `"points":{"criteria":["session_count"],"podium":5,"min_session_minutes":181}`, 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "categories.points.min_session_minutes"},
		{name: "null points", complete: true, body: strings.Replace(validBody, `"elimination":{"step_meters":2000}`, `"points":null`, 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "categories.points"},
		{name: "factors for points without the longest distance", complete: true, body: strings.Replace(validBody, `"cumulative_distance":{},"elimination":{"step_meters":2000}`, `"points":{"criteria":["session_count"],"podium":5}`, 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "distance_factors"},
		{name: "factors without the cumulative distance", complete: true, body: strings.Replace(validBody, `"cumulative_distance":{},`, "", 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "distance_factors"},
		{name: "former elimination_step_meters", complete: true, body: strings.Replace(validBody, `"weeks":6`, `"weeks":6,"elimination_step_meters":2000`, 1), wantStatus: 400, wantCode: "validation_failed"},
		{name: "factor over 2: refused by the contract, key not echoed", complete: true, body: strings.Replace(validBody, `"ride":0.25`, `"ride":3`, 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "distance_factors"},
		{name: "description over 2000 characters", complete: true, body: strings.Replace(validBody, `"weeks":6`, `"weeks":6,"description":"`+strings.Repeat("é", 2001)+`"`, 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "description"},
		{name: "not JSON", complete: true, body: "title=Spring", wantStatus: 400, wantCode: "validation_failed"},
		{name: "unknown field", complete: true, body: strings.Replace(validBody, `"weeks":6`, `"weeks":6,"created_by":"someone"`, 1), wantStatus: 400, wantCode: "validation_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, repo := newTestService(tt.complete)
			rec, out := post(t, svc, tt.body)
			e, _ := out["error"].(map[string]any)
			var fields string
			if params, ok := e["params"].(map[string]any); ok {
				fields, _ = params["fields"].(string)
			}
			if rec.Code != tt.wantStatus || e["code"] != tt.wantCode || fields != tt.wantFields {
				t.Fatalf("got %d %v fields %q, want %d %s fields %q", rec.Code, e["code"], fields, tt.wantStatus, tt.wantCode, tt.wantFields)
			}
			if repo.created != nil {
				t.Fatal("challenge stored despite the refusal")
			}
		})
	}
}

func TestListChallenges(t *testing.T) {
	desc := "abandoned"
	repo := &fakeRepository{summaries: []Summary{
		{ID: "11111111-1111-1111-1111-111111111111", Title: "A", Visibility: "public", Timezone: "UTC", ReferenceSport: "ride", StartsAt: now, EndsAt: now.AddDate(0, 0, 14), ParticipantCount: 3, MyStatus: &desc, Creator: true},
		{ID: "22222222-2222-2222-2222-222222222222", Title: "B", Visibility: "public", Timezone: "UTC", StartsAt: now.AddDate(0, 0, 1), EndsAt: now.AddDate(0, 0, 15), ParticipantCount: 1},
	}, counts: Counts{Upcoming: 1, Ongoing: 4, Finished: 2}}
	svc := NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now))

	rec, out := serve(t, svc, http.MethodGet, "/api/v1/challenges?scope=mine&status=ongoing&limit=1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body)
	}
	items, _ := out["items"].([]any)
	first, _ := items[0].(map[string]any)
	if len(items) != 1 || first["my_status"] != "abandoned" || first["participant_count"] != 3.0 {
		t.Fatalf("items %v, want 1 item with my status and the count", out["items"])
	}
	if first["my_role"] != "creator" || first["status"] != "ongoing" || first["reference_sport"] != "ride" {
		t.Fatalf("item %v, want my role creator, the status ongoing and the reference sport", first)
	}
	// 14 days from 15:00: the weeks start at midnight, the third holds the
	// last 15 hours.
	if v, ok := first["best_result"]; first["week_count"] != 3.0 || !ok || v != nil {
		t.Fatalf("item %v, want 3 weeks and a null best result", first)
	}
	// Given to the one who abandoned too, though they have no my week.
	if first["current_week"] != 1.0 || first["week_ends_at"] != "2026-03-11T00:00:00Z" {
		t.Fatalf("item %v, want week 1 ending at the second midnight", first)
	}
	if got, _ := json.Marshal(out["counts"]); string(got) != `{"finished":2,"ongoing":4,"upcoming":1}` {
		t.Fatalf("counts %s, want those of the scope", got)
	}
	cursor, _ := out["next_cursor"].(string)
	if cursor == "" {
		t.Fatalf("next_cursor %v, want a cursor for the second page", out["next_cursor"])
	}
	if q := repo.listed; q.Scope != ScopeMine || q.Status != StatusOngoing || q.ActorID != actor {
		t.Fatalf("query %+v, want the parameters and the actor from the token", q)
	}

	// The cursor gives the position of the last item back to the query.
	if rec, _ := serve(t, svc, http.MethodGet, "/api/v1/challenges?cursor="+cursor, ""); rec.Code != http.StatusOK {
		t.Fatalf("second page: status %d", rec.Code)
	}
	if after := repo.listed.After; after == nil || after.ID != "11111111-1111-1111-1111-111111111111" || !after.StartsAt.Equal(now) {
		t.Fatalf("after %+v, want the position of the first item", after)
	}
}

func TestListChallengesMyWeek(t *testing.T) {
	active := string(ParticipantActive)
	weekEnd := now.Add(48 * time.Hour)
	minimum, provisional := 7000, 12
	running := Summary{
		ID: "11111111-1111-1111-1111-111111111111", Title: "A", Visibility: "public", Timezone: "UTC",
		StartsAt: now.AddDate(0, 0, -8), EndsAt: now.AddDate(0, 0, 13), ParticipantCount: 8, MyStatus: &active,
	}
	upcoming := Summary{ID: "22222222-2222-2222-2222-222222222222", Title: "B", Visibility: "public", Timezone: "UTC", StartsAt: now.AddDate(0, 0, 1), EndsAt: now.AddDate(0, 0, 15), MyStatus: &active}
	repo := &fakeRepository{summaries: []Summary{running, upcoming}, categoriesOf: map[string][]Category{running.ID: nil}}
	standings := fakeStandings{standings: map[string]Standing{running.ID: {Week: 2, WeekEndsAt: weekEnd, Rankings: []Ranking{
		{Type: CategoryCumulativeTime, Rank: 1, RankedCount: 7},
		{Type: CategoryCumulativeDistance, Rank: 5, RankedCount: 7},
		{Type: CategoryElimination, Rank: 3, RankedCount: 7, Progress: &scoring.Progress{
			Status: scoring.StatusAtRisk, MinimumMeters: &minimum, DistanceMeters: 6000, WeekEndsAt: weekEnd,
		}},
		{Type: CategoryPoints, Rank: 2, RankedCount: 7, Provisional: &provisional},
		{Type: CategoryElevation, Rank: 4, RankedCount: 7},
	}}}}
	svc := NewService(repo, fakeProfiles{}, fakeCatalogue{}, standings, clock.NewFake(now))

	rec, out := serve(t, svc, http.MethodGet, "/api/v1/challenges?scope=mine", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body)
	}
	items, _ := out["items"].([]any)
	first, _ := items[0].(map[string]any)
	second, _ := items[1].(map[string]any)
	if v, ok := second["my_week"]; !ok || v != nil {
		t.Fatalf("upcoming my_week %v, want null", v)
	}
	got, _ := json.Marshal(first["my_week"])
	want := `{"rankings":[` +
		`{"elimination":null,"provisional_points":null,"rank":1,"ranked_count":7,"type":"cumulative_time"},` +
		`{"elimination":null,"provisional_points":null,"rank":5,"ranked_count":7,"type":"cumulative_distance"},` +
		`{"elimination":{"distance_meters":6000,"eliminated_week":null,"minimum_meters":7000,"status":"at_risk","week_ends_at":"` + weekEnd.UTC().Format(time.RFC3339) + `"},"provisional_points":null,"rank":3,"ranked_count":7,"type":"elimination"},` +
		`{"elimination":null,"provisional_points":12,"rank":2,"ranked_count":7,"type":"points"},` +
		`{"elimination":null,"provisional_points":null,"rank":4,"ranked_count":7,"type":"cumulative_elevation"}` +
		`],"week":2,"week_ends_at":"` + weekEnd.UTC().Format(time.RFC3339) + `"}`
	if string(got) != want {
		t.Fatalf("my_week\n%s\nwant\n%s", got, want)
	}
}

func TestListChallengesBestResult(t *testing.T) {
	active := string(ParticipantActive)
	done := Summary{
		ID: "11111111-1111-1111-1111-111111111111", Title: "A", Visibility: "public", Timezone: "UTC",
		StartsAt: now.AddDate(0, 0, -21), EndsAt: now.AddDate(0, 0, -1), ParticipantCount: 8, MyStatus: &active,
	}
	repo := &fakeRepository{summaries: []Summary{done}, categoriesOf: map[string][]Category{done.ID: nil}}
	standings := fakeStandings{results: map[string][]Ranking{done.ID: {
		{Type: CategoryCumulativeTime, Rank: 3, RankedCount: 7},
		{Type: CategoryCumulativeDistance, Rank: 2, RankedCount: 7},
	}}}
	svc := NewService(repo, fakeProfiles{}, fakeCatalogue{}, standings, clock.NewFake(now))

	rec, out := serve(t, svc, http.MethodGet, "/api/v1/challenges?scope=mine", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body)
	}
	items, _ := out["items"].([]any)
	first, _ := items[0].(map[string]any)
	if got, _ := json.Marshal(first["best_result"]); string(got) != `{"rank":2,"ranked_count":7,"type":"cumulative_distance"}` {
		t.Fatalf("best_result %s, want the distance line", got)
	}
	if v, ok := first["my_week"]; first["week_count"] != 3.0 || !ok || v != nil {
		t.Fatalf("item %v, want 3 weeks and a null my week", first)
	}
	for _, k := range []string{"current_week", "week_ends_at"} {
		if v, ok := first[k]; !ok || v != nil {
			t.Fatalf("%s = %v (present %t), want null once finished", k, v, ok)
		}
	}
}

func TestListChallengesDefaults(t *testing.T) {
	repo := &fakeRepository{}
	rec, out := serve(t, NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)), http.MethodGet, "/api/v1/challenges", "")
	if rec.Code != http.StatusOK || out["next_cursor"] != nil {
		t.Fatalf("got %d %v, want 200 with no next page", rec.Code, out)
	}
	if items, ok := out["items"].([]any); !ok || len(items) != 0 {
		t.Fatalf("items %v, want an empty list, not null", out["items"])
	}
	if q := repo.listed; q.Scope != ScopePublic || q.Status != "" || q.Limit != defaultLimit+1 || q.After != nil {
		t.Fatalf("query %+v, want public, every status, %d per page", q, defaultLimit)
	}
}

func TestListChallengesRefusals(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantFields string
	}{
		{name: "unknown scope", query: "scope=all", wantFields: "scope"},
		{name: "unknown status", query: "status=cancelled", wantFields: "status"},
		{name: "limit 0", query: "limit=0", wantFields: "limit"},
		{name: "limit over 100", query: "limit=101", wantFields: "limit"},
		{name: "cursor not base64", query: "cursor=%21%21", wantFields: "cursor"},
		{name: "cursor with a bad id", query: "cursor=eyJzIjoiMjAyNi0wMy0wNFQwNTowMDowMFoiLCJpIjoieCJ9", wantFields: "cursor"},
		{name: "cursor of JSON null", query: "cursor=bnVsbA", wantFields: "cursor"},
		{name: "cursor without its date", query: "cursor=eyJpIjoiMTExMTExMTEtMTExMS0xMTExLTExMTEtMTExMTExMTExMTExIn0", wantFields: "cursor"},
		{name: "cursor with a bad date", query: "cursor=eyJzIjoibm90IGEgZGF0ZSIsImkiOiIxMTExMTExMS0xMTExLTExMTEtMTExMS0xMTExMTExMTExMTEifQ", wantFields: "cursor"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{}
			rec, out := serve(t, NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)), http.MethodGet, "/api/v1/challenges?"+tt.query, "")
			e, _ := out["error"].(map[string]any)
			params, _ := e["params"].(map[string]any)
			if rec.Code != http.StatusBadRequest || e["code"] != "validation_failed" || params["fields"] != tt.wantFields {
				t.Fatalf("got %d %v, want 400 validation_failed on %s", rec.Code, out, tt.wantFields)
			}
			if repo.listed.ActorID != "" {
				t.Fatal("query run despite the refusal")
			}
		})
	}
}

func TestListChallengesNormalizesTheCursorID(t *testing.T) {
	// A forged cursor whose id is a urn: uuid.Parse accepts it, PostgreSQL
	// would not; the query gets the canonical form.
	cursor, err := httpx.EncodeCursor(Position{StartsAt: now, ID: "urn:uuid:11111111-1111-1111-1111-111111111111"})
	if err != nil {
		t.Fatal(err)
	}
	repo := &fakeRepository{}
	rec, _ := serve(t, NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)), http.MethodGet, "/api/v1/challenges?cursor="+cursor, "")
	if rec.Code != http.StatusOK || repo.listed.After == nil || repo.listed.After.ID != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("got %d, after %+v, want the canonical id", rec.Code, repo.listed.After)
	}
}

const participantsPath = "/api/v1/challenges/11111111-1111-1111-1111-111111111111/participants"

func TestListParticipants(t *testing.T) {
	repo := &fakeRepository{challenge: stored(VisibilityPublic), participants: participants(3)}
	profiles := fakeProfiles{names: map[string]Name{"00000000-0000-0000-0000-000000000001": marie}}
	svc := NewService(repo, profiles, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now))

	rec, out := serve(t, svc, http.MethodGet, participantsPath+"?limit=2", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body)
	}
	items, _ := out["items"].([]any)
	first, _ := items[0].(map[string]any)
	second, _ := items[1].(map[string]any)
	if len(items) != 2 || first["pseudo"] != "marie_92" || first["first_name"] != "Marie-Ève" || first["last_name"] != "Tremblay" ||
		first["status"] != "active" || first["avatar"] != nil || first["is_creator"] != true {
		t.Fatalf("items %v, want 2 items, the first a creator with its pseudo and names", out["items"])
	}
	if second["is_creator"] != false {
		t.Fatalf("second item %v, want is_creator false", second)
	}
	// Never the e-mail of another user.
	if _, ok := first["email"]; ok {
		t.Fatalf("first item %v carries an e-mail", first)
	}
	// No name (account deleted after the listing was read): pseudo and names null.
	for _, field := range []string{"pseudo", "first_name", "last_name"} {
		if v, ok := second[field]; !ok || v != nil || second["status"] != "abandoned" {
			t.Fatalf("second item %v, want abandoned with a null %s", second, field)
		}
	}
	cursor, _ := out["next_cursor"].(string)
	if cursor == "" {
		t.Fatalf("next_cursor %v, want a cursor for the second page", out["next_cursor"])
	}

	// The cursor gives the position of the last item back to the query.
	if rec, _ := serve(t, svc, http.MethodGet, participantsPath+"?cursor="+cursor, ""); rec.Code != http.StatusOK {
		t.Fatalf("second page: status %d", rec.Code)
	}
	after := repo.participantsAsked.after
	if after == nil || after.UserID != "00000000-0000-0000-0000-000000000002" || !after.JoinedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("after %+v, want the position of the second item", after)
	}
	if repo.participantsAsked.limit != defaultLimit+1 {
		t.Fatalf("limit %d, want %d by default", repo.participantsAsked.limit, defaultLimit+1)
	}
}

func TestListParticipantsHidden(t *testing.T) {
	// A private challenge the user does not see answers like a missing one.
	for name, repo := range map[string]*fakeRepository{
		"hidden":  {challenge: stored(VisibilityPrivate), participants: participants(1)},
		"missing": {},
	} {
		t.Run(name, func(t *testing.T) {
			rec, out := serve(t, NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)), http.MethodGet, participantsPath, "")
			e, _ := out["error"].(map[string]any)
			if rec.Code != http.StatusNotFound || e["code"] != "challenge_not_found" {
				t.Fatalf("got %d %v, want 404 challenge_not_found", rec.Code, out)
			}
			if repo.participantsAsked != nil {
				t.Fatal("participants read despite the refusal")
			}
		})
	}
}

// TestAdminReadsAPrivateChallenge: the admin permission of the token lets
// its holder read a private challenge they are not in (detail, with the role
// of their membership, and participants); without it, the same challenge is
// not found. Writes never take the permission: an edit stays hidden.
func TestAdminReadsAPrivateChallenge(t *testing.T) {
	const challengePath = "/api/v1/challenges/11111111-1111-1111-1111-111111111111"
	tests := []struct {
		name   string
		token  string
		method string
		target string
		body   string
		status int
	}{
		{name: "admin reads the detail", token: adminToken, method: http.MethodGet, target: challengePath, status: http.StatusOK},
		{name: "admin lists the participants", token: adminToken, method: http.MethodGet, target: participantsPath, status: http.StatusOK},
		{name: "user: detail hidden", token: "some.access.token", method: http.MethodGet, target: challengePath, status: http.StatusNotFound},
		{name: "user: participants hidden", token: "some.access.token", method: http.MethodGet, target: participantsPath, status: http.StatusNotFound},
		{name: "admin may not edit", token: adminToken, method: http.MethodPatch, target: challengePath, body: `{"title":"Taken over"}`, status: http.StatusNotFound},
		{name: "admin may not join", token: adminToken, method: http.MethodPost, target: challengePath + "/join", status: http.StatusNotFound},
		{name: "admin may not delete", token: adminToken, method: http.MethodDelete, target: challengePath, status: http.StatusNotFound},
		{name: "admin may not exclude", token: adminToken, method: http.MethodDelete, target: participantsPath + "/22222222-2222-2222-2222-222222222222", status: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{challenge: stored(VisibilityPrivate), participants: participants(1)}
			svc := NewService(repo, fakeProfiles{complete: true}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now))
			rec, out := serveAs(t, svc, tt.token, tt.method, tt.target, tt.body)
			if rec.Code != tt.status {
				t.Fatalf("status %d (%s), want %d", rec.Code, rec.Body, tt.status)
			}
			e, _ := out["error"].(map[string]any)
			if tt.status == http.StatusNotFound && e["code"] != "challenge_not_found" {
				t.Fatalf("error %v, want challenge_not_found", out)
			}
			if tt.target == challengePath && tt.method == http.MethodGet && rec.Code == http.StatusOK && out["my_role"] != "outsider" {
				t.Fatalf("my_role %v, want outsider: the role of the membership", out["my_role"])
			}
		})
	}
}

func TestListParticipantsRefusals(t *testing.T) {
	cursor := func(v any) string {
		c, err := httpx.EncodeCursor(v)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	tests := []struct {
		name       string
		target     string
		wantFields string
	}{
		// A bad path parameter answers 400 without naming it, as on every route.
		{name: "bad challenge id", target: "/api/v1/challenges/x/participants"},
		{name: "limit 0", target: participantsPath + "?limit=0", wantFields: "limit"},
		{name: "limit over 100", target: participantsPath + "?limit=101", wantFields: "limit"},
		{name: "cursor not base64", target: participantsPath + "?cursor=%21%21", wantFields: "cursor"},
		{name: "cursor with a bad id", target: participantsPath + "?cursor=" + cursor(map[string]any{"j": now, "u": "x"}), wantFields: "cursor"},
		{name: "cursor without its date", target: participantsPath + "?cursor=" + cursor(map[string]any{"u": "11111111-1111-1111-1111-111111111111"}), wantFields: "cursor"},
		{name: "cursor with a bad date", target: participantsPath + "?cursor=" + cursor(map[string]any{"j": "not a date", "u": "11111111-1111-1111-1111-111111111111"}), wantFields: "cursor"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{challenge: stored(VisibilityPublic)}
			rec, out := serve(t, NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)), http.MethodGet, tt.target, "")
			e, _ := out["error"].(map[string]any)
			params, _ := e["params"].(map[string]any)
			if rec.Code != http.StatusBadRequest || e["code"] != "validation_failed" || (tt.wantFields != "" && params["fields"] != tt.wantFields) {
				t.Fatalf("got %d %v, want 400 validation_failed on %s", rec.Code, out, tt.wantFields)
			}
			if repo.participantsAsked != nil {
				t.Fatal("query run despite the refusal")
			}
		})
	}
}

func TestListParticipantsNormalizesTheCursorID(t *testing.T) {
	cursor, err := httpx.EncodeCursor(ParticipantPosition{JoinedAt: now, UserID: "urn:uuid:11111111-1111-1111-1111-111111111111"})
	if err != nil {
		t.Fatal(err)
	}
	repo := &fakeRepository{challenge: stored(VisibilityPublic)}
	rec, _ := serve(t, NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)), http.MethodGet, participantsPath+"?cursor="+cursor, "")
	if rec.Code != http.StatusOK || repo.participantsAsked == nil || repo.participantsAsked.after.UserID != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("got %d, query %+v, want the canonical id", rec.Code, repo.participantsAsked)
	}
}

func TestGetChallenge(t *testing.T) {
	repo := &fakeRepository{challenge: stored(VisibilityPublic), membership: Membership{Participant: ParticipantAbandoned}}
	repo.challenge.Categories = []Category{{Type: CategoryCumulativeTime, Position: 1}}
	repo.challenge.ParticipantCount = 4
	clk := clock.NewFake(time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC))
	rec, out := serve(t, NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clk), http.MethodGet, "/api/v1/challenges/11111111-1111-1111-1111-111111111111", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body)
	}
	want := map[string]any{
		"my_role": "abandoned", "my_status": "abandoned", "status": "ongoing", "can_join": false,
		"current_week": 1.0, "week_ends_at": "2026-03-09T04:00:00Z", "registration_deadline": "2026-03-09T04:00:00Z",
		"participant_count": 4.0, "week_count": 3.0,
	}
	for k, v := range want {
		if out[k] != v {
			t.Errorf("%s = %v, want %v", k, out[k], v)
		}
	}
	weeks := []any{
		map[string]any{"number": 1.0, "starts_at": "2026-03-02T05:00:00Z", "ends_at": "2026-03-09T04:00:00Z"},
		map[string]any{"number": 2.0, "starts_at": "2026-03-09T04:00:00Z", "ends_at": "2026-03-16T04:00:00Z"},
		map[string]any{"number": 3.0, "starts_at": "2026-03-16T04:00:00Z", "ends_at": "2026-03-23T04:00:00Z"},
	}
	if !reflect.DeepEqual(out["weeks"], weeks) {
		t.Errorf("weeks = %v, want %v", out["weeks"], weeks)
	}
}

func TestGetChallengeNotFoundIsTheSameAnswer(t *testing.T) {
	type answer struct {
		status  int
		headers http.Header
		body    map[string]any
	}
	answers := map[string]answer{}
	// A deleted challenge reaches the service as a missing one (the
	// integration tests check the storage side).
	for name, repo := range map[string]*fakeRepository{
		"missing or deleted": {},
		"private, not mine":  {challenge: stored(VisibilityPrivate)},
	} {
		rec, out := serve(t, NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)), http.MethodGet, "/api/v1/challenges/11111111-1111-1111-1111-111111111111", "")
		headers := rec.Header().Clone()
		headers.Del("X-Request-ID") // differs for every request
		e, _ := out["error"].(map[string]any)
		delete(e, "request_id")
		answers[name] = answer{status: rec.Code, headers: headers, body: out}
	}
	a, b := answers["missing or deleted"], answers["private, not mine"]
	if a.status != http.StatusNotFound || a.status != b.status || !reflect.DeepEqual(a.headers, b.headers) || !reflect.DeepEqual(a.body, b.body) {
		t.Fatalf("answers differ:\n%+v\n%+v", a, b)
	}
	if e, _ := a.body["error"].(map[string]any); e["code"] != "challenge_not_found" {
		t.Fatalf("body %v, want challenge_not_found", a.body)
	}
}

func TestGetChallengeOutsideTheWindowHasNoWeek(t *testing.T) {
	repo := &fakeRepository{challenge: stored(VisibilityPublic)}
	for name, at := range map[string]time.Time{
		"before the start": time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		"after the end":    time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
	} {
		rec, out := serve(t, NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(at)), http.MethodGet, "/api/v1/challenges/11111111-1111-1111-1111-111111111111", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", name, rec.Code)
		}
		if v, ok := out["current_week"]; !ok || v != nil {
			t.Errorf("%s: current_week = %v, want null", name, v)
		}
		if v, ok := out["week_ends_at"]; !ok || v != nil {
			t.Errorf("%s: week_ends_at = %v, want null", name, v)
		}
		if v := out["week_count"]; v != 3.0 {
			t.Errorf("%s: week_count = %v, want 3", name, v)
		}
	}
}

func TestGetChallengeBadID(t *testing.T) {
	rec, out := serve(t, NewService(&fakeRepository{}, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)), http.MethodGet, "/api/v1/challenges/not-a-uuid", "")
	e, _ := out["error"].(map[string]any)
	if rec.Code != http.StatusBadRequest || e["code"] != "validation_failed" {
		t.Fatalf("got %d %v, want 400 validation_failed", rec.Code, out)
	}
}

func TestUpdateChallenge(t *testing.T) {
	const target = "/api/v1/challenges/11111111-1111-1111-1111-111111111111"
	creator := Membership{Creator: true, Participant: ParticipantActive}
	tests := []struct {
		name       string
		membership Membership
		body       string
		wantStatus int
		wantCode   string
		wantFields string
	}{
		{name: "creator", membership: creator, body: `{"title":"New title","description":""}`, wantStatus: 200},
		{name: "participant", membership: Membership{Participant: ParticipantActive}, body: `{"title":"New title"}`, wantStatus: 403, wantCode: "forbidden"},
		{name: "frozen fields", membership: creator, body: `{"title":"New title","weeks":8,"visibility":"private"}`, wantStatus: 422, wantCode: "field_not_editable", wantFields: "visibility,weeks"},
		{name: "frozen reference sport", membership: creator, body: `{"title":"New title","reference_sport":"ride"}`, wantStatus: 422, wantCode: "field_not_editable", wantFields: "reference_sport"},
		{name: "unknown field", membership: creator, body: `{"titel":"New title"}`, wantStatus: 400, wantCode: "validation_failed"},
		{name: "null title", membership: creator, body: `{"title":null}`, wantStatus: 400, wantCode: "validation_failed", wantFields: "title"},
		{name: "null description", membership: creator, body: `{"description":null}`, wantStatus: 400, wantCode: "validation_failed", wantFields: "description"},
		{name: "description over 2000 characters", membership: creator, body: `{"description":"` + strings.Repeat("é", 2001) + `"}`, wantStatus: 400, wantCode: "validation_failed", wantFields: "description"},
		{name: "creator who abandoned", membership: Membership{Creator: true, Participant: ParticipantAbandoned}, body: `{"title":"New title"}`, wantStatus: 200},
		{name: "title too short", membership: creator, body: `{"title":" ab "}`, wantStatus: 400, wantCode: "validation_failed", wantFields: "title"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{challenge: stored(VisibilityPublic), membership: tt.membership}
			rec, out := serve(t, NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)), http.MethodPatch, target, tt.body)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if tt.wantStatus == 200 {
				if out["title"] != "New title" || out["my_role"] != "creator" {
					t.Fatalf("body %v, want the edited detail", out)
				}
				return
			}
			e, _ := out["error"].(map[string]any)
			params, _ := e["params"].(map[string]any)
			fields, _ := params["fields"].(string)
			if e["code"] != tt.wantCode || fields != tt.wantFields {
				t.Fatalf("got %v fields %q, want %s fields %q", e["code"], fields, tt.wantCode, tt.wantFields)
			}
			if repo.updated != nil {
				t.Fatal("challenge written despite the refusal")
			}
		})
	}
}

// TestUpdateChallengeFrozenFieldNull: a frozen field sent as null is refused
// by the contract, and nothing is written.
func TestUpdateChallengeFrozenFieldNull(t *testing.T) {
	const target = "/api/v1/challenges/11111111-1111-1111-1111-111111111111"
	for _, field := range []string{
		"categories", "distance_factors", "elimination_step_meters", "ends_at",
		"members_can_invite", "reference_sport", "start_date", "starts_at",
		"timezone", "visibility", "weeks",
	} {
		t.Run(field, func(t *testing.T) {
			repo := &fakeRepository{challenge: stored(VisibilityPublic), membership: Membership{Creator: true, Participant: ParticipantActive}}
			rec, out := serve(t, NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)), http.MethodPatch, target, `{"title":"New title","`+field+`":null}`)
			e, _ := out["error"].(map[string]any)
			params, _ := e["params"].(map[string]any)
			if rec.Code != http.StatusBadRequest || e["code"] != "validation_failed" || params["fields"] != field {
				t.Fatalf("got %d %v, want 400 validation_failed fields %q", rec.Code, out, field)
			}
			if repo.updated != nil {
				t.Fatal("challenge written despite the refusal")
			}
		})
	}
}

// TestUpdateChallengeNotFoundIsTheSameAnswer: a missing challenge and a
// private one the actor may not see answer the same 404 on edit too.
func TestUpdateChallengeNotFoundIsTheSameAnswer(t *testing.T) {
	const target = "/api/v1/challenges/11111111-1111-1111-1111-111111111111"
	var bodies []map[string]any
	// The private one also gets a frozen field: 404 comes before 422, so the
	// answer still tells nothing.
	for body, repo := range map[string]*fakeRepository{
		`{"title":"New title"}`:           {},
		`{"title":"New title","weeks":8}`: {challenge: stored(VisibilityPrivate)},
	} {
		rec, out := serve(t, NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)), http.MethodPatch, target, body)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status %d, want 404", rec.Code)
		}
		e, _ := out["error"].(map[string]any)
		delete(e, "request_id")
		bodies = append(bodies, out)
		if repo.updated != nil {
			t.Fatal("challenge written")
		}
	}
	if !reflect.DeepEqual(bodies[0], bodies[1]) {
		t.Fatalf("bodies %v and %v, want the same challenge_not_found", bodies[0], bodies[1])
	}
}

func TestJoinChallenge(t *testing.T) {
	const target = "/api/v1/challenges/11111111-1111-1111-1111-111111111111/join"
	before := clock.NewFake(time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC))
	tests := []struct {
		name       string
		repo       *fakeRepository
		profile    bool
		wantStatus int
		wantCode   string
	}{
		{name: "joined", repo: &fakeRepository{challenge: stored(VisibilityPublic)}, profile: true, wantStatus: 201},
		{name: "already in", repo: &fakeRepository{challenge: stored(VisibilityPublic), membership: Membership{Participant: ParticipantActive}}, profile: true, wantStatus: 409, wantCode: "already_participant"},
		{name: "abandoned", repo: &fakeRepository{challenge: stored(VisibilityPublic), membership: Membership{Participant: ParticipantAbandoned}}, profile: true, wantStatus: 422, wantCode: "participation_abandoned"},
		{name: "private", repo: &fakeRepository{challenge: stored(VisibilityPrivate)}, profile: true, wantStatus: 404, wantCode: "challenge_not_found"},
		{name: "incomplete profile", repo: &fakeRepository{challenge: stored(VisibilityPublic)}, wantStatus: 422, wantCode: "profile_incomplete"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, out := serve(t, NewService(tt.repo, fakeProfiles{complete: tt.profile}, fakeCatalogue{}, fakeStandings{}, before), http.MethodPost, target, "")
			if rec.Code != tt.wantStatus {
				t.Fatalf("status %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if tt.wantStatus == 201 {
				if out["my_role"] != "participant" || out["my_status"] != "active" || out["can_join"] != false {
					t.Fatalf("body %v, want the actor now an active participant", out)
				}
				return
			}
			if e, _ := out["error"].(map[string]any); e["code"] != tt.wantCode {
				t.Fatalf("code %v, want %s", e["code"], tt.wantCode)
			}
		})
	}

	// Registration closed: the clock at the exact end of week 1.
	closed := clock.NewFake(time.Date(2026, 3, 9, 4, 0, 0, 0, time.UTC))
	rec, out := serve(t, NewService(&fakeRepository{challenge: stored(VisibilityPublic)}, fakeProfiles{complete: true}, fakeCatalogue{}, fakeStandings{}, closed), http.MethodPost, target, "")
	if e, _ := out["error"].(map[string]any); rec.Code != 422 || e["code"] != "registration_closed" {
		t.Fatalf("got %d %v, want 422 registration_closed", rec.Code, out)
	}
}

// TestJoinChallengeNotFoundIsTheSameAnswer: joining a missing challenge and
// a private one without invitation answer the same 404.
func TestJoinChallengeNotFoundIsTheSameAnswer(t *testing.T) {
	const target = "/api/v1/challenges/11111111-1111-1111-1111-111111111111/join"
	var bodies []map[string]any
	for _, repo := range []*fakeRepository{{}, {challenge: stored(VisibilityPrivate)}} {
		rec, out := serve(t, NewService(repo, fakeProfiles{complete: true}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)), http.MethodPost, target, "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status %d, want 404", rec.Code)
		}
		e, _ := out["error"].(map[string]any)
		delete(e, "request_id")
		bodies = append(bodies, out)
	}
	if !reflect.DeepEqual(bodies[0], bodies[1]) {
		t.Fatalf("bodies %v and %v, want the same challenge_not_found", bodies[0], bodies[1])
	}
}

func TestAbandonChallenge(t *testing.T) {
	const target = "/api/v1/challenges/11111111-1111-1111-1111-111111111111/abandon"
	during := clock.NewFake(time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC))
	tests := []struct {
		name       string
		repo       *fakeRepository
		clk        *clock.FakeClock
		wantStatus int
		wantCode   string
	}{
		{name: "active", repo: &fakeRepository{challenge: stored(VisibilityPublic), membership: Membership{Participant: ParticipantActive}}, clk: during, wantStatus: 200},
		{name: "again", repo: &fakeRepository{challenge: stored(VisibilityPublic), membership: Membership{Participant: ParticipantAbandoned}}, clk: during, wantStatus: 200},
		{name: "at the exact end", repo: &fakeRepository{challenge: stored(VisibilityPublic), membership: Membership{Participant: ParticipantActive}}, clk: clock.NewFake(time.Date(2026, 3, 23, 4, 0, 0, 0, time.UTC)), wantStatus: 422, wantCode: "challenge_ended"},
		{name: "never joined", repo: &fakeRepository{challenge: stored(VisibilityPublic)}, clk: during, wantStatus: 404, wantCode: "not_found"},
		{name: "hidden", repo: &fakeRepository{challenge: stored(VisibilityPrivate)}, clk: during, wantStatus: 404, wantCode: "challenge_not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, out := serve(t, NewService(tt.repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, tt.clk), http.MethodPost, target, "")
			if rec.Code != tt.wantStatus {
				t.Fatalf("status %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if tt.wantStatus == 200 {
				if out["my_status"] != "abandoned" || out["my_role"] != "abandoned" {
					t.Fatalf("body %v, want the actor abandoned", out)
				}
				return
			}
			if e, _ := out["error"].(map[string]any); e["code"] != tt.wantCode {
				t.Fatalf("code %v, want %s", e["code"], tt.wantCode)
			}
		})
	}
}

func TestAbandonChallengeByTheCreator(t *testing.T) {
	repo := &fakeRepository{challenge: stored(VisibilityPublic), membership: Membership{Creator: true, Participant: ParticipantActive}}
	rec, out := serve(t, NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC))), http.MethodPost, "/api/v1/challenges/11111111-1111-1111-1111-111111111111/abandon", "")
	if rec.Code != http.StatusOK || out["my_role"] != "creator" || out["my_status"] != "abandoned" {
		t.Fatalf("got %d %v, want 200, still creator, abandoned", rec.Code, out)
	}
}

func TestAbandonChallengeNotFoundIsTheSameAnswer(t *testing.T) {
	const target = "/api/v1/challenges/11111111-1111-1111-1111-111111111111/abandon"
	var bodies []map[string]any
	for _, repo := range []*fakeRepository{{}, {challenge: stored(VisibilityPrivate)}} {
		rec, out := serve(t, NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)), http.MethodPost, target, "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status %d, want 404", rec.Code)
		}
		e, _ := out["error"].(map[string]any)
		delete(e, "request_id")
		bodies = append(bodies, out)
	}
	if !reflect.DeepEqual(bodies[0], bodies[1]) {
		t.Fatalf("bodies %v and %v, want the same challenge_not_found", bodies[0], bodies[1])
	}
}

func TestExcludeParticipant(t *testing.T) {
	const target = "/api/v1/challenges/11111111-1111-1111-1111-111111111111/participants/22222222-2222-2222-2222-222222222222"
	during := clock.NewFake(time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC))
	creator := Membership{Creator: true, Participant: ParticipantActive}
	tests := []struct {
		name       string
		repo       *fakeRepository
		wantStatus int
		wantCode   string
	}{
		{name: "creator", repo: &fakeRepository{challenge: stored(VisibilityPublic), membership: creator, target: Membership{Participant: ParticipantActive}}, wantStatus: 204},
		{name: "already out", repo: &fakeRepository{challenge: stored(VisibilityPublic), membership: creator, target: Membership{Participant: ParticipantAbandoned}}, wantStatus: 204},
		{name: "participant", repo: &fakeRepository{challenge: stored(VisibilityPublic), membership: Membership{Participant: ParticipantActive}, target: Membership{Participant: ParticipantActive}}, wantStatus: 403, wantCode: "forbidden"},
		{name: "creator target", repo: &fakeRepository{challenge: stored(VisibilityPublic), membership: creator, target: creator}, wantStatus: 422, wantCode: "cannot_exclude_creator"},
		{name: "unknown target", repo: &fakeRepository{challenge: stored(VisibilityPublic), membership: creator}, wantStatus: 404, wantCode: "not_found"},
		{name: "hidden", repo: &fakeRepository{challenge: stored(VisibilityPrivate)}, wantStatus: 404, wantCode: "challenge_not_found"},
	}
	ended := &fakeRepository{challenge: stored(VisibilityPublic), membership: creator, target: Membership{Participant: ParticipantActive}}
	rec, out := serve(t, NewService(ended, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(time.Date(2026, 3, 23, 4, 0, 0, 0, time.UTC))), http.MethodDelete, target, "")
	if e, _ := out["error"].(map[string]any); rec.Code != 422 || e["code"] != "challenge_ended" {
		t.Fatalf("at the exact end: %d %v, want 422 challenge_ended", rec.Code, out)
	}
	rec, out = serve(t, NewService(ended, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, during), http.MethodDelete, "/api/v1/challenges/11111111-1111-1111-1111-111111111111/participants/not-a-uuid", "")
	if e, _ := out["error"].(map[string]any); rec.Code != 400 || e["code"] != "validation_failed" {
		t.Fatalf("bad user id: %d %v, want 400 validation_failed", rec.Code, out)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, out := serve(t, NewService(tt.repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, during), http.MethodDelete, target, "")
			if rec.Code != tt.wantStatus {
				t.Fatalf("status %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if tt.wantStatus == 204 {
				if rec.Body.Len() != 0 {
					t.Fatalf("body %q, want none", rec.Body)
				}
				return
			}
			if e, _ := out["error"].(map[string]any); e["code"] != tt.wantCode {
				t.Fatalf("code %v, want %s", e["code"], tt.wantCode)
			}
		})
	}
}

func TestAddCreator(t *testing.T) {
	const target = "/api/v1/challenges/11111111-1111-1111-1111-111111111111/creators"
	const body = `{"user_id":"22222222-2222-2222-2222-222222222222"}`
	during := clock.NewFake(time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC))
	creator := Membership{Creator: true, Participant: ParticipantActive}
	tests := []struct {
		name       string
		repo       *fakeRepository
		body       string
		wantStatus int
		wantCode   string
	}{
		{name: "creator", repo: &fakeRepository{challenge: stored(VisibilityPublic), membership: creator, target: Membership{Participant: ParticipantActive}}, body: body, wantStatus: 200},
		{name: "already a creator", repo: &fakeRepository{challenge: stored(VisibilityPublic), membership: creator, target: creator}, body: body, wantStatus: 200},
		{name: "participant", repo: &fakeRepository{challenge: stored(VisibilityPublic), membership: Membership{Participant: ParticipantActive}, target: Membership{Participant: ParticipantActive}}, body: body, wantStatus: 403, wantCode: "forbidden"},
		{name: "target not participating", repo: &fakeRepository{challenge: stored(VisibilityPublic), membership: creator}, body: body, wantStatus: 422, wantCode: "not_active_participant"},
		{name: "hidden", repo: &fakeRepository{challenge: stored(VisibilityPrivate)}, body: body, wantStatus: 404, wantCode: "challenge_not_found"},
		{name: "bad user id", repo: &fakeRepository{challenge: stored(VisibilityPublic), membership: creator}, body: `{"user_id":"x"}`, wantStatus: 400, wantCode: "validation_failed"},
		{name: "unknown field", repo: &fakeRepository{challenge: stored(VisibilityPublic), membership: creator}, body: `{"user_id":"22222222-2222-2222-2222-222222222222","role":"admin"}`, wantStatus: 400, wantCode: "validation_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, out := serve(t, NewService(tt.repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, during), http.MethodPost, target, tt.body)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if tt.wantStatus == 200 {
				if out["my_role"] != "creator" {
					t.Fatalf("body %v, want the challenge seen by its creator", out)
				}
				return
			}
			if e, _ := out["error"].(map[string]any); e["code"] != tt.wantCode {
				t.Fatalf("code %v, want %s", e["code"], tt.wantCode)
			}
		})
	}

	ended := &fakeRepository{challenge: stored(VisibilityPublic), membership: creator, target: Membership{Participant: ParticipantActive}}
	rec, out := serve(t, NewService(ended, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(time.Date(2026, 3, 23, 4, 0, 0, 0, time.UTC))), http.MethodPost, target, body)
	if e, _ := out["error"].(map[string]any); rec.Code != 422 || e["code"] != "challenge_ended" {
		t.Fatalf("at the exact end: %d %v, want 422 challenge_ended", rec.Code, out)
	}
}

func TestDeleteChallenge(t *testing.T) {
	const target = "/api/v1/challenges/11111111-1111-1111-1111-111111111111"
	creator := Membership{Creator: true, Participant: ParticipantActive}
	tests := []struct {
		name       string
		repo       *fakeRepository
		wantStatus int
		wantCode   string
	}{
		{name: "creator", repo: &fakeRepository{challenge: stored(VisibilityPublic), membership: creator}, wantStatus: 204},
		{name: "participant", repo: &fakeRepository{challenge: stored(VisibilityPublic), membership: Membership{Participant: ParticipantActive}}, wantStatus: 403, wantCode: "forbidden"},
		{name: "hidden", repo: &fakeRepository{challenge: stored(VisibilityPrivate)}, wantStatus: 404, wantCode: "challenge_not_found"},
		{name: "missing or already deleted", repo: &fakeRepository{}, wantStatus: 404, wantCode: "challenge_not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, out := serve(t, NewService(tt.repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)), http.MethodDelete, target, "")
			if rec.Code != tt.wantStatus {
				t.Fatalf("status %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if tt.wantStatus == 204 {
				return
			}
			if e, _ := out["error"].(map[string]any); e["code"] != tt.wantCode {
				t.Fatalf("code %v, want %s", e["code"], tt.wantCode)
			}
		})
	}
}
