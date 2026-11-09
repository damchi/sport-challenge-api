package challenge

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
	api.RegisterHandlersWithOptions(r, api.NewStrictHandlerWithOptions(challengesOnly{Handler: NewHandler(svc)}, nil, api.StrictGinServerOptions{
		RequestErrorHandlerFunc:  httpx.BadRequestHandler(logger),
		HandlerErrorFunc:         httpx.ErrorHandler(logger),
		ResponseErrorHandlerFunc: httpx.ErrorHandler(logger),
	}), api.GinServerOptions{
		ErrorHandler: httpx.InvalidParamHandler(logger),
		Middlewares:  []api.MiddlewareFunc{api.MiddlewareFunc(validate)},
	})

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Authorization", "Bearer some.access.token")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	apitest.ValidateResponse(t, httptest.NewRequest(method, target, nil), rec)

	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("body is not JSON: %s", rec.Body)
	}
	return rec, out
}

func newTestService(profileComplete bool) (*Service, *fakeRepository) {
	repo := &fakeRepository{}
	return NewService(repo, fakeProfiles{complete: profileComplete}, fakeCatalogue{factors: suggested()}, clock.NewFake(now)), repo
}

const validBody = `{"title":"Spring run","visibility":"public","timezone":"America/Toronto","start_date":"2026-03-04","weeks":6,"elimination_step_meters":2000,"distance_factors":{"ride":0.25}}`

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
		{name: "factor of an unknown sport: the key is not echoed", complete: true, body: strings.Replace(validBody, `"ride":0.25`, `"<script>":"x"`, 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "distance_factors"},
		{name: "factor out of range", complete: true, body: strings.Replace(validBody, `"ride":0.25`, `"ride":0.123`, 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "distance_factors.ride"},
		{name: "missing title", complete: true, body: strings.Replace(validBody, `"title":"Spring run",`, "", 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "title"},
		{name: "NUL in the title: 400, not a 500 from the database", complete: true, body: strings.Replace(validBody, `"Spring run"`, `"Spring\u0000run"`, 1), wantStatus: 400, wantCode: "validation_failed", wantFields: "title"},
		{name: "categories chosen by the client (D2)", complete: true, body: strings.Replace(validBody, `"weeks":6`, `"weeks":6,"categories":[]`, 1), wantStatus: 400, wantCode: "validation_failed"},
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
		{ID: "11111111-1111-1111-1111-111111111111", Title: "A", Visibility: "public", Timezone: "UTC", StartsAt: now, EndsAt: now.AddDate(0, 0, 14), ParticipantCount: 3, MyStatus: &desc},
		{ID: "22222222-2222-2222-2222-222222222222", Title: "B", Visibility: "public", Timezone: "UTC", StartsAt: now.AddDate(0, 0, 1), EndsAt: now.AddDate(0, 0, 15), ParticipantCount: 1},
	}}
	svc := NewService(repo, fakeProfiles{}, fakeCatalogue{}, clock.NewFake(now))

	rec, out := serve(t, svc, http.MethodGet, "/api/v1/challenges?scope=mine&status=ongoing&limit=1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body)
	}
	items, _ := out["items"].([]any)
	first, _ := items[0].(map[string]any)
	if len(items) != 1 || first["my_status"] != "abandoned" || first["participant_count"] != 3.0 {
		t.Fatalf("items %v, want 1 item with my status and the count", out["items"])
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

func TestListChallengesDefaults(t *testing.T) {
	repo := &fakeRepository{}
	rec, out := serve(t, NewService(repo, fakeProfiles{}, fakeCatalogue{}, clock.NewFake(now)), http.MethodGet, "/api/v1/challenges", "")
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
		{name: "cursor with a bad date", query: "cursor=eyJzIjoibm90IGEgZGF0ZSIsImkiOiIxMTExMTExMS0xMTExLTExMTEtMTExMS0xMTExMTExMTExMTEifQ", wantFields: "cursor"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{}
			rec, out := serve(t, NewService(repo, fakeProfiles{}, fakeCatalogue{}, clock.NewFake(now)), http.MethodGet, "/api/v1/challenges?"+tt.query, "")
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
	rec, _ := serve(t, NewService(repo, fakeProfiles{}, fakeCatalogue{}, clock.NewFake(now)), http.MethodGet, "/api/v1/challenges?cursor="+cursor, "")
	if rec.Code != http.StatusOK || repo.listed.After == nil || repo.listed.After.ID != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("got %d, after %+v, want the canonical id", rec.Code, repo.listed.After)
	}
}
