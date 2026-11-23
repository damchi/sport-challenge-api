package activity

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/damchi/sport-challenge-api/internal/api"
	"github.com/damchi/sport-challenge-api/internal/api/apitest"
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/httpx"
)

// adminToken is the access token acceptingVerifier grants the admin
// permission to; it accepts any other one without it.
const adminToken = "admin.access.token"

type acceptingVerifier struct{}

func (acceptingVerifier) Verify(_ context.Context, raw string) (auth.Claims, error) {
	return auth.Claims{Subject: "email|123", Admin: raw == adminToken}, nil
}

type activitiesOnly struct {
	*Handler
	apitest.Others
}

// postActivity serves POST /api/v1/activities (serve).
func postActivity(t *testing.T, svc *Service, body string, headers map[string]string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	return serve(t, svc, http.MethodPost, "/api/v1/activities", body, headers)
}

// serve serves a request as cmd/api does (authentication, contract
// validation, handler) and checks the response against the contract; the
// body is nil when the response has none. headers are added to the request;
// an Authorization of "" leaves the request without a token.
func serve(t *testing.T, svc *Service, method, target, body string, headers map[string]string) (*httptest.ResponseRecorder, map[string]any) {
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
		auth.CurrentUser(func(context.Context, auth.Claims) (string, error) { return actor, nil }, logger),
	))
	api.RegisterHandlersWithOptions(r, api.NewStrictHandlerWithOptions(activitiesOnly{Handler: NewHandler(svc)}, nil, api.StrictGinServerOptions{
		RequestErrorHandlerFunc:  httpx.BadRequestHandler(logger),
		HandlerErrorFunc:         httpx.ErrorHandler(logger),
		ResponseErrorHandlerFunc: httpx.ErrorHandler(logger),
	}), api.GinServerOptions{
		ErrorHandler: httpx.InvalidParamHandler(logger),
		Middlewares:  []api.MiddlewareFunc{api.MiddlewareFunc(validate)},
	})

	req := httptest.NewRequest(method, target, strings.NewReader(body))
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
	apitest.ValidateResponse(t, httptest.NewRequest(method, target, nil), rec)

	if rec.Body.Len() == 0 {
		return rec, nil
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q is not JSON: %v", rec.Body.String(), err)
	}
	return rec, got
}

// contributionIDs are the challenge ids of the contributions of an activity
// in a response.
func contributionIDs(activity map[string]any) []any {
	cs, _ := activity["contributions"].([]any)
	ids := make([]any, 0, len(cs))
	for _, c := range cs {
		m, _ := c.(map[string]any)
		ids = append(ids, m["challenge_id"])
	}
	return ids
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
	// 5 km of run, factor 1 in c1, and its 1500 s.
	want := map[string]any{"challenge_id": c1.ChallengeID, "challenge_title": "Spring run", "distance_meters": 5000.0, "duration_seconds": 1500.0}
	if cs, _ := body["contributions"].([]any); len(cs) != 1 || !reflect.DeepEqual(cs[0], want) {
		t.Fatalf("contributions %v, want [%v]", body["contributions"], want)
	}
}

// The elevation gain is shown as entered; null when not entered
// (SC-API-149).
func TestCreateActivityElevationGain(t *testing.T) {
	repo := &fakeRepository{}
	svc := newService(t, repo, fakeParticipations{})
	rec, body := postActivity(t, svc, `{"sport_code":"run","started_at":"2026-06-10T11:00:00Z","duration_seconds":1500,"distance_meters":5000,"elevation_gain_meters":320}`, nil)
	if rec.Code != http.StatusCreated || body["elevation_gain_meters"] != 320.0 {
		t.Fatalf("got %d %v, want 201 with elevation_gain_meters 320", rec.Code, body)
	}
	if repo.recorded == nil || repo.recorded.ElevationGainMeters == nil || *repo.recorded.ElevationGainMeters != 320 {
		t.Fatalf("recorded %+v, want the elevation gain stored", repo.recorded)
	}

	rec, body = postActivity(t, newService(t, &fakeRepository{}, fakeParticipations{}), runBody, nil)
	if v, present := body["elevation_gain_meters"]; rec.Code != http.StatusCreated || !present || v != nil {
		t.Fatalf("got %d %v, want 201 with elevation_gain_meters null", rec.Code, body)
	}
}

// A code of the catalogue may hold digits (constraint sports_code_check):
// the contract lets it through.
func TestCreateActivityCodeWithADigit(t *testing.T) {
	svc := newService(t, &fakeRepository{}, fakeParticipations{})
	rec, body := postActivity(t, svc, `{"sport_code":"ski_2","started_at":"2026-06-10T11:00:00Z","duration_seconds":3600}`, nil)
	if rec.Code != http.StatusCreated || body["sport_code"] != "ski_2" {
		t.Fatalf("status %d (%s), want 201 for ski_2", rec.Code, rec.Body)
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
	if cs, ok := body["contributions"].([]any); !ok || len(cs) != 0 {
		t.Fatalf("contributions %v, want []", body["contributions"])
	}
}

// A sport without a factor in the challenge brings its duration only: the
// distance of the contribution is null, never left out.
func TestCreateActivityContributionWithoutDistance(t *testing.T) {
	c1 := challengeOf(t, "8a0c2f4e-1b3d-4e5f-9a7b-6c8d0e2f4a6b")
	svc := newService(t, &fakeRepository{}, fakeParticipations{challenges: []Participation{c1}})
	rec, body := postActivity(t, svc, `{"sport_code":"yoga","started_at":"2026-06-10T11:00:00Z","duration_seconds":3600}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d (%s), want 201", rec.Code, rec.Body)
	}
	cs, _ := body["contributions"].([]any)
	if len(cs) != 1 {
		t.Fatalf("contributions %v, want one", body["contributions"])
	}
	c, _ := cs[0].(map[string]any)
	if d, present := c["distance_meters"]; !present || d != nil || c["duration_seconds"] != 3600.0 {
		t.Fatalf("contribution %v, want distance_meters null and the duration", c)
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
		{name: "elevation gain for swimming", body: `{"sport_code":"swim","started_at":"2026-06-10T11:00:00Z","duration_seconds":1500,"distance_meters":1500,"elevation_gain_meters":10}`, status: http.StatusUnprocessableEntity, code: "elevation_not_allowed"},
		{name: "elevation gain without distance", body: `{"sport_code":"other","sport_label":"Trail","started_at":"2026-06-10T11:00:00Z","duration_seconds":1500,"elevation_gain_meters":10}`, status: http.StatusUnprocessableEntity, code: "elevation_not_allowed"},
		{name: "elevation gain over 20000 m", body: `{"sport_code":"run","started_at":"2026-06-10T11:00:00Z","duration_seconds":1500,"distance_meters":5000,"elevation_gain_meters":20001}`, status: http.StatusBadRequest, code: "validation_failed"},
		{name: "negative elevation gain", body: `{"sport_code":"run","started_at":"2026-06-10T11:00:00Z","duration_seconds":1500,"distance_meters":5000,"elevation_gain_meters":-1}`, status: http.StatusBadRequest, code: "validation_failed"},
		{name: "code starting with a digit", body: `{"sport_code":"2ski","started_at":"2026-06-10T11:00:00Z","duration_seconds":1500}`, status: http.StatusBadRequest, code: "validation_failed"},
		{name: "code of 33 characters", body: `{"sport_code":"a` + strings.Repeat("b", 32) + `","started_at":"2026-06-10T11:00:00Z","duration_seconds":1500}`, status: http.StatusBadRequest, code: "validation_failed"},
		{name: "unknown sport", body: `{"sport_code":"chess","started_at":"2026-06-10T11:00:00Z","duration_seconds":1500}`, status: http.StatusBadRequest, code: "validation_failed"},
		{name: "speed sent", body: `{"sport_code":"run","started_at":"2026-06-10T11:00:00Z","duration_seconds":1500,"distance_meters":5000,"speed_mps":3}`, status: http.StatusBadRequest, code: "validation_failed"},
		{name: "source sent", body: `{"sport_code":"run","started_at":"2026-06-10T11:00:00Z","duration_seconds":1500,"distance_meters":5000,"source":"strava"}`, status: http.StatusBadRequest, code: "validation_failed"},
		{name: "duration missing", body: `{"sport_code":"run","started_at":"2026-06-10T11:00:00Z","distance_meters":5000}`, status: http.StatusBadRequest, code: "validation_failed"},
		{name: "duration zero", body: `{"sport_code":"run","started_at":"2026-06-10T11:00:00Z","duration_seconds":0,"distance_meters":5000}`, status: http.StatusBadRequest, code: "validation_failed"},
		{name: "ends in the future", body: `{"sport_code":"run","started_at":"2026-06-11T15:50:00Z","duration_seconds":1500,"distance_meters":5000}`, status: http.StatusBadRequest, code: "validation_failed"},
		{name: "starts before 2000", body: `{"sport_code":"run","started_at":"1999-12-31T23:59:59Z","duration_seconds":1500,"distance_meters":5000}`, status: http.StatusBadRequest, code: "validation_failed"},
		{name: "key with a space", body: runBody, headers: map[string]string{"Idempotency-Key": "a key"}, status: http.StatusBadRequest, code: "validation_failed"},
		{name: "key too long", body: runBody, headers: map[string]string{"Idempotency-Key": strings.Repeat("k", 129)}, status: http.StatusBadRequest, code: "validation_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{}
			svc := NewService(repo, fakeProfiles{complete: true}, catalogue, fakeParticipations{t: t, challenges: locked}, nil, clock.NewFake(recordNow))
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

func TestCreateActivityKeyOfDeletedActivity(t *testing.T) {
	repo := &fakeRepository{err: errActivityNotFound}
	rec, body := postActivity(t, newService(t, repo, fakeParticipations{}), runBody, map[string]string{"Idempotency-Key": "deleted-key"})
	if rec.Code != http.StatusNotFound || errorCode(body) != "activity_not_found" {
		t.Fatalf("got %d %v, want 404 activity_not_found", rec.Code, body)
	}
}

const activityTarget = "/api/v1/activities/" + activityID

// The body replaces every field entered: an elevation gain sent is set, one
// left out is removed (SC-API-149).
func TestUpdateActivityElevationGain(t *testing.T) {
	withGain := storedRun(t)
	withGain.ElevationGainMeters = new(int)
	*withGain.ElevationGainMeters = 150
	for _, tt := range []struct {
		name string
		body string
		want any
	}{
		{name: "set", body: `{"sport_code":"run","started_at":"2026-06-10T11:00:00Z","duration_seconds":1500,"distance_meters":5000,"elevation_gain_meters":420}`, want: 420.0},
		{name: "left out", body: runBody, want: nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{stored: map[string]Activity{activityID: withGain}}
			rec, body := serve(t, newService(t, repo, fakeParticipations{}), http.MethodPatch, activityTarget, tt.body, nil)
			if v, present := body["elevation_gain_meters"]; rec.Code != http.StatusOK || !present || v != tt.want {
				t.Fatalf("got %d %v, want 200 with elevation_gain_meters %v", rec.Code, body, tt.want)
			}
		})
	}
}

func TestUpdateActivityReplaces(t *testing.T) {
	repo := &fakeRepository{stored: map[string]Activity{activityID: storedRun(t)}}
	c1 := challengeOf(t, "8a0c2f4e-1b3d-4e5f-9a7b-6c8d0e2f4a6b")
	svc := newService(t, repo, fakeParticipations{challenges: []Participation{c1}})
	rec, body := serve(t, svc, http.MethodPatch, activityTarget, `{"sport_code":"yoga","started_at":"2026-06-10T12:00:00Z","duration_seconds":3600}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d (%s), want 200", rec.Code, rec.Body)
	}
	if body["id"] != activityID || body["sport_code"] != "yoga" || body["distance_meters"] != nil || body["speed_mps"] != nil || body["source"] != "manual" {
		t.Fatalf("body %v, want the activity replaced, its distance cleared", body)
	}
	if ids := contributionIDs(body); len(ids) != 1 || ids[0] != c1.ChallengeID {
		t.Fatalf("contributions %v, want [%s]", body["contributions"], c1.ChallengeID)
	}
}

func TestDeleteActivityDeletes(t *testing.T) {
	repo := &fakeRepository{stored: map[string]Activity{activityID: storedRun(t)}}
	rec, _ := serve(t, newService(t, repo, fakeParticipations{}), http.MethodDelete, activityTarget, "", nil)
	if rec.Code != http.StatusNoContent || repo.deleted != activityID {
		t.Fatalf("status %d (%s), deleted %q, want 204 and the activity deleted", rec.Code, rec.Body, repo.deleted)
	}
}

// TestActivityOverlap checks the 422 activity_overlap of the storage on POST
// and PATCH, as the contract describes it.
func TestActivityOverlap(t *testing.T) {
	overlap := fmt.Errorf("record activity: %w", errActivityOverlap)
	svc := NewService(&fakeRepository{err: overlap}, fakeProfiles{complete: true}, catalogue, fakeParticipations{t: t}, nil, clock.NewFake(recordNow))
	rec, body := postActivity(t, svc, runBody, nil)
	if rec.Code != http.StatusUnprocessableEntity || errorCode(body) != "activity_overlap" {
		t.Fatalf("POST: got %d %v, want 422 activity_overlap", rec.Code, body)
	}
	repo := &fakeRepository{stored: map[string]Activity{activityID: storedRun(t)}, err: overlap}
	rec, body = serve(t, newService(t, repo, fakeParticipations{}), http.MethodPatch, activityTarget, runBody, nil)
	if rec.Code != http.StatusUnprocessableEntity || errorCode(body) != "activity_overlap" {
		t.Fatalf("PATCH: got %d %v, want 422 activity_overlap", rec.Code, body)
	}
}

func TestChangeActivityRefusals(t *testing.T) {
	someoneElse := storedRun(t)
	someoneElse.UserID = "7c6b5a49-3827-4165-9f0e-d1c2b3a4f5e6"
	locked := storedRun(t)
	locked.StartedAt = toronto(t, 2026, 6, 3, 7)
	tests := []struct {
		name    string
		method  string
		target  string
		body    string
		headers map[string]string
		stored  *Activity
		status  int
		code    string
	}{
		{name: "update without token", method: http.MethodPatch, body: runBody, headers: map[string]string{"Authorization": ""}, status: http.StatusUnauthorized, code: "unauthenticated"},
		{name: "update of someone else", method: http.MethodPatch, body: runBody, stored: &someoneElse, status: http.StatusNotFound, code: "activity_not_found"},
		{name: "update of a missing activity", method: http.MethodPatch, body: runBody, status: http.StatusNotFound, code: "activity_not_found"},
		{name: "update in a locked week", method: http.MethodPatch, body: runBody, stored: &locked, status: http.StatusUnprocessableEntity, code: "week_locked"},
		{name: "moved into a locked week", method: http.MethodPatch, body: `{"sport_code":"run","started_at":"2026-06-03T11:00:00Z","duration_seconds":1500,"distance_meters":5000}`, status: http.StatusUnprocessableEntity, code: "week_locked"},
		{name: "update without duration", method: http.MethodPatch, body: `{"sport_code":"run","started_at":"2026-06-10T11:00:00Z","distance_meters":5000}`, status: http.StatusBadRequest, code: "validation_failed"},
		{name: "update with a source", method: http.MethodPatch, body: `{"sport_code":"run","started_at":"2026-06-10T11:00:00Z","duration_seconds":1500,"distance_meters":5000,"source":"strava"}`, status: http.StatusBadRequest, code: "validation_failed"},
		{name: "update with a malformed id", method: http.MethodPatch, target: "/api/v1/activities/not-a-uuid", body: runBody, status: http.StatusBadRequest, code: "validation_failed"},
		{name: "delete without token", method: http.MethodDelete, headers: map[string]string{"Authorization": ""}, status: http.StatusUnauthorized, code: "unauthenticated"},
		{name: "delete of someone else", method: http.MethodDelete, stored: &someoneElse, status: http.StatusNotFound, code: "activity_not_found"},
		{name: "delete of a missing activity", method: http.MethodDelete, status: http.StatusNotFound, code: "activity_not_found"},
		{name: "delete in a locked week", method: http.MethodDelete, stored: &locked, status: http.StatusUnprocessableEntity, code: "week_locked"},
		{name: "delete with a malformed id", method: http.MethodDelete, target: "/api/v1/activities/not-a-uuid", status: http.StatusBadRequest, code: "validation_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stored := map[string]Activity{}
			if tt.stored != nil {
				stored[activityID] = *tt.stored
			} else if tt.code != "activity_not_found" {
				stored[activityID] = storedRun(t)
			}
			target := tt.target
			if target == "" {
				target = activityTarget
			}
			repo := &fakeRepository{stored: stored}
			svc := newService(t, repo, fakeParticipations{challenges: []Participation{challengeOf(t, "8a0c2f4e-1b3d-4e5f-9a7b-6c8d0e2f4a6b")}})
			rec, body := serve(t, svc, tt.method, target, tt.body, tt.headers)
			if rec.Code != tt.status || errorCode(body) != tt.code {
				t.Fatalf("got %d %v, want %d %s", rec.Code, body, tt.status, tt.code)
			}
			if repo.updated != nil || repo.deleted != "" {
				t.Fatalf("updated %+v, deleted %q: want nothing written", repo.updated, repo.deleted)
			}
		})
	}
}

func TestListMyActivities(t *testing.T) {
	c1 := challengeOf(t, "8a0c2f4e-1b3d-4e5f-9a7b-6c8d0e2f4a6b")
	week1 := storedRun(t)
	week1.ID, week1.StartedAt = "5f1e7c1a-0000-4000-8000-000000000003", toronto(t, 2026, 6, 3, 7)
	repo := &fakeRepository{listing: []Activity{storedRun(t), week1}}
	svc := newService(t, repo, fakeParticipations{challenges: []Participation{c1}})
	rec, body := serve(t, svc, http.MethodGet, "/api/v1/me/activities?limit=1&from=2026-06-01T04:00:00Z&to=2026-06-22T04:00:00Z", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d (%s), want 200", rec.Code, rec.Body)
	}
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items %v, want one", body["items"])
	}
	first, _ := items[0].(map[string]any)
	if first["id"] != activityID || first["locked"] != false || first["speed_mps"] == nil || first["source"] != "manual" {
		t.Fatalf("item %v, want the run of week 2, unlocked, with its speed", first)
	}
	if q := repo.listed; q == nil || q.From == nil || q.To == nil || !q.From.Equal(toronto(t, 2026, 6, 1, 0)) || !reflect.DeepEqual(q.UserIDs, []string{actor}) {
		t.Fatalf("query %+v, want the actor's activities in the period", q)
	}

	// The next page starts after the cursor.
	cursor, _ := body["next_cursor"].(string)
	if cursor == "" {
		t.Fatalf("next_cursor %v, want one", body["next_cursor"])
	}
	repo.listing = []Activity{week1}
	rec, body = serve(t, svc, http.MethodGet, "/api/v1/me/activities?limit=1&cursor="+cursor, "", nil)
	if rec.Code != http.StatusOK || body["next_cursor"] != nil {
		t.Fatalf("status %d, body %v, want the last page", rec.Code, body)
	}
	if after := repo.listed.After; after == nil || after.ID != activityID || !after.StartedAt.Equal(storedRun(t).StartedAt) {
		t.Fatalf("after %+v, want the position of the first page's last item", after)
	}
	items, _ = body["items"].([]any)
	if second, _ := items[0].(map[string]any); second["locked"] != true {
		t.Fatalf("item %v, want the run of week 1 locked", second)
	}
}

func TestListMyActivitiesRefusals(t *testing.T) {
	noDate, err := httpx.EncodeCursor(Position{ID: activityID})
	if err != nil {
		t.Fatal(err)
	}
	badID, err := httpx.EncodeCursor(map[string]string{"s": "2026-06-10T11:00:00Z", "i": "nope"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		query   string
		headers map[string]string
		status  int
		code    string
	}{
		{name: "no token", headers: map[string]string{"Authorization": ""}, status: http.StatusUnauthorized, code: "unauthenticated"},
		{name: "to before from", query: "?from=2026-06-10T00:00:00Z&to=2026-06-01T00:00:00Z", status: http.StatusBadRequest, code: "validation_failed"},
		{name: "malformed from", query: "?from=yesterday", status: http.StatusBadRequest, code: "validation_failed"},
		{name: "limit too high", query: "?limit=101", status: http.StatusBadRequest, code: "validation_failed"},
		{name: "cursor not base64", query: "?cursor=%25%25", status: http.StatusBadRequest, code: "validation_failed"},
		{name: "cursor without its date", query: "?cursor=" + noDate, status: http.StatusBadRequest, code: "validation_failed"},
		{name: "cursor with a bad id", query: "?cursor=" + badID, status: http.StatusBadRequest, code: "validation_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{}
			rec, body := serve(t, newService(t, repo, fakeParticipations{}), http.MethodGet, "/api/v1/me/activities"+tt.query, "", tt.headers)
			if rec.Code != tt.status || errorCode(body) != tt.code {
				t.Fatalf("got %d %v, want %d %s", rec.Code, body, tt.status, tt.code)
			}
			if repo.listed != nil {
				t.Fatal("activities read despite the refusal")
			}
		})
	}
}

const participantActivities = "/api/v1/challenges/8a0c2f4e-1b3d-4e5f-9a7b-6c8d0e2f4a6b/participants/" + participant + "/activities"

func TestListParticipantActivities(t *testing.T) {
	c1 := challengeOf(t, "8a0c2f4e-1b3d-4e5f-9a7b-6c8d0e2f4a6b")
	other := challengeOf(t, "1d2c3b4a-5f6e-4d7c-8b9a-0f1e2d3c4b5a")
	run := storedRun(t)
	run.UserID = participant
	repo := &fakeRepository{listing: []Activity{run}}
	var asked scopeCall
	ps := fakeParticipations{t: t, user: participant, challenges: []Participation{c1, other}}
	svc := NewService(repo, fakeProfiles{}, catalogue, ps, fakeChallenges{scope: c1, asked: &asked}, clock.NewFake(recordNow))
	rec, body := serve(t, svc, http.MethodGet, participantActivities, "", nil)
	if rec.Code != http.StatusOK || body["next_cursor"] != nil {
		t.Fatalf("status %d (%s), want 200 and a single page", rec.Code, rec.Body)
	}
	if asked != (scopeCall{actor: auth.Actor{ID: actor}, challengeID: c1.ChallengeID, userID: participant}) {
		t.Fatalf("scope asked for %v", asked)
	}
	items, _ := body["items"].([]any)
	first, _ := items[0].(map[string]any)
	// The other challenge of the participant is never shown.
	if ids := contributionIDs(first); len(ids) != 1 || ids[0] != c1.ChallengeID {
		t.Fatalf("contributions %v, want [%s] only", first["contributions"], c1.ChallengeID)
	}
}

// TestListActivitiesPassTheAdminPermission: the actor asked for the scope
// carries the admin permission of the token, on both lists.
func TestListActivitiesPassTheAdminPermission(t *testing.T) {
	c1 := challengeOf(t, "8a0c2f4e-1b3d-4e5f-9a7b-6c8d0e2f4a6b")
	admin := map[string]string{"Authorization": "Bearer " + adminToken}
	for _, target := range []string{participantActivities, challengeActivities} {
		var asked scopeCall
		scope := fakeChallenges{scope: c1, members: map[string]Participation{}, asked: &asked}
		svc := NewService(&fakeRepository{}, fakeProfiles{}, catalogue, fakeParticipations{t: t, user: participant}, scope, clock.NewFake(recordNow))
		rec, _ := serve(t, svc, http.MethodGet, target, "", admin)
		if rec.Code != http.StatusOK || asked.actor != (auth.Actor{ID: actor, Admin: true}) {
			t.Fatalf("%s: status %d (%s), actor %+v, want 200 and the admin permission", target, rec.Code, rec.Body, asked.actor)
		}
	}
}

func TestListParticipantActivitiesRefusals(t *testing.T) {
	tests := []struct {
		name     string
		target   string
		headers  map[string]string
		scopeErr error
		status   int
		code     string
	}{
		{name: "no token", headers: map[string]string{"Authorization": ""}, status: http.StatusUnauthorized, code: "unauthenticated"},
		{name: "non-participant of a public challenge", scopeErr: apperr.New(apperr.CodeNotFound, nil), status: http.StatusNotFound, code: "not_found"},
		{name: "private challenge hidden", scopeErr: apperr.New(apperr.CodeChallengeNotFound, nil), status: http.StatusNotFound, code: "challenge_not_found"},
		{name: "malformed user id", target: "/api/v1/challenges/8a0c2f4e-1b3d-4e5f-9a7b-6c8d0e2f4a6b/participants/nope/activities", status: http.StatusBadRequest, code: "validation_failed"},
		{name: "limit zero", target: participantActivities + "?limit=0", status: http.StatusBadRequest, code: "validation_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := tt.target
			if target == "" {
				target = participantActivities
			}
			repo := &fakeRepository{}
			svc := NewService(repo, fakeProfiles{}, catalogue, fakeParticipations{t: t, user: participant}, fakeChallenges{err: tt.scopeErr}, clock.NewFake(recordNow))
			rec, body := serve(t, svc, http.MethodGet, target, "", tt.headers)
			if rec.Code != tt.status || errorCode(body) != tt.code {
				t.Fatalf("got %d %v, want %d %s", rec.Code, body, tt.status, tt.code)
			}
			if repo.listed != nil {
				t.Fatal("activities read despite the refusal")
			}
		})
	}
}

const challengeActivities = "/api/v1/challenges/8a0c2f4e-1b3d-4e5f-9a7b-6c8d0e2f4a6b/activities"

func TestListChallengeActivities(t *testing.T) {
	c1 := challengeOf(t, "8a0c2f4e-1b3d-4e5f-9a7b-6c8d0e2f4a6b")
	run := storedRun(t)
	run.UserID = participant
	repo := &fakeRepository{listing: []Activity{run, run}}
	pseudo := "lea"
	profiles := fakeProfiles{names: map[string]Name{participant: {Pseudo: &pseudo}}}
	var asked scopeCall
	scope := fakeChallenges{scope: c1, members: map[string]Participation{participant: c1}, asked: &asked}
	svc := NewService(repo, profiles, catalogue, fakeParticipations{t: t, user: "nobody"}, scope, clock.NewFake(recordNow))
	rec, body := serve(t, svc, http.MethodGet, challengeActivities+"?limit=1", "", nil)
	if rec.Code != http.StatusOK || body["next_cursor"] == nil {
		t.Fatalf("status %d (%s), want 200 and a next page", rec.Code, rec.Body)
	}
	if asked != (scopeCall{actor: auth.Actor{ID: actor}, challengeID: c1.ChallengeID}) {
		t.Fatalf("scope asked for %v", asked)
	}
	items, _ := body["items"].([]any)
	first, _ := items[0].(map[string]any)
	if ids := contributionIDs(first); len(ids) != 1 || ids[0] != c1.ChallengeID || first["locked"] != false || first["speed_mps"] == nil {
		t.Fatalf("item %v, want the activity counting in %s, not locked, with its speed", first, c1.ChallengeID)
	}
	want := map[string]any{"id": participant, "pseudo": pseudo, "first_name": nil, "last_name": nil, "avatar": nil}
	if !reflect.DeepEqual(first["user"], want) {
		t.Fatalf("user %v, want %v", first["user"], want)
	}
}

func TestListChallengeActivitiesRefusals(t *testing.T) {
	tests := []struct {
		name     string
		target   string
		headers  map[string]string
		scopeErr error
		status   int
		code     string
	}{
		{name: "no token", headers: map[string]string{"Authorization": ""}, status: http.StatusUnauthorized, code: "unauthenticated"},
		{name: "non-participant of a public challenge", scopeErr: apperr.New(apperr.CodeNotFound, nil), status: http.StatusNotFound, code: "not_found"},
		{name: "private challenge hidden", scopeErr: apperr.New(apperr.CodeChallengeNotFound, nil), status: http.StatusNotFound, code: "challenge_not_found"},
		{name: "malformed challenge id", target: "/api/v1/challenges/nope/activities", status: http.StatusBadRequest, code: "validation_failed"},
		{name: "limit zero", target: challengeActivities + "?limit=0", status: http.StatusBadRequest, code: "validation_failed"},
		{name: "forged cursor", target: challengeActivities + "?cursor=nope", status: http.StatusBadRequest, code: "validation_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := tt.target
			if target == "" {
				target = challengeActivities
			}
			repo := &fakeRepository{}
			scope := fakeChallenges{members: map[string]Participation{participant: challengeOf(t, "c1")}, err: tt.scopeErr}
			svc := NewService(repo, fakeProfiles{}, catalogue, fakeParticipations{}, scope, clock.NewFake(recordNow))
			rec, body := serve(t, svc, http.MethodGet, target, "", tt.headers)
			if rec.Code != tt.status || errorCode(body) != tt.code {
				t.Fatalf("got %d %v, want %d %s", rec.Code, body, tt.status, tt.code)
			}
			if repo.listed != nil {
				t.Fatal("activities read despite the refusal")
			}
		})
	}
}
