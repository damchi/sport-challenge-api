package admin

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/damchi/sport-challenge-api/internal/activity"
	"github.com/damchi/sport-challenge-api/internal/api"
	"github.com/damchi/sport-challenge-api/internal/api/apitest"
	"github.com/damchi/sport-challenge-api/internal/leaderboard"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/buildinfo"
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

type adminOnly struct {
	*Handler
	apitest.Others
}

// getStats serves GET /api/v1/admin/stats with token (serve).
func getStats(t *testing.T, svc *Service, token string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	return serve(t, svc, token, "/api/v1/admin/stats")
}

// serve serves GET target as cmd/api does (authentication, contract
// validation, handler) with token, and checks the response against the
// contract.
func serve(t *testing.T, svc *Service, token, target string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	return serveLogged(t, svc, token, http.MethodGet, target, "", io.Discard)
}

// serveLogged is serve for method with body (none when ""), the logs of the
// API, as JSON, written to logs.
func serveLogged(t *testing.T, svc *Service, token, method, target, body string, logs io.Writer) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	return serveWith(t, NewHandler(svc, nil, nil, nil, nil), token, method, target, body, logs)
}

// serveWith is serveLogged on handler h.
func serveWith(t *testing.T, h *Handler, token, method, target, body string, logs io.Writer, headers ...http.Header) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	spec, err := api.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	validate, err := httpx.RequestValidator(spec, logger, HideInvalidRequest)
	if err != nil {
		t.Fatal(err)
	}
	r := httpx.NewRouter(logger)
	r.Use(auth.Protect(nil,
		auth.Authenticate(acceptingVerifier{}, logger),
		auth.CurrentUser(func(context.Context, auth.Claims, string) (string, error) {
			return "11111111-1111-4111-8111-111111111111", nil
		}, logger),
	))
	api.RegisterHandlersWithOptions(r, api.NewStrictHandlerWithOptions(adminOnly{Handler: h}, nil, api.StrictGinServerOptions{
		RequestErrorHandlerFunc:  httpx.BadRequestHandler(logger, HideInvalidRequest),
		HandlerErrorFunc:         httpx.ErrorHandler(logger),
		ResponseErrorHandlerFunc: httpx.ErrorHandler(logger),
	}), api.GinServerOptions{
		ErrorHandler: httpx.InvalidParamHandler(logger, HideInvalidRequest),
		Middlewares:  []api.MiddlewareFunc{api.MiddlewareFunc(validate)},
	})

	newRequest := func() *http.Request {
		if body == "" {
			return httptest.NewRequest(method, target, nil)
		}
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for _, hs := range headers {
			maps.Copy(req.Header, hs)
		}
		return req
	}
	req := newRequest()
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	apitest.ValidateResponse(t, newRequest(), rec)

	var got map[string]any
	if rec.Code == http.StatusNoContent {
		return rec, got
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q is not JSON: %v", rec.Body.String(), err)
	}
	return rec, got
}

func TestGetAdminStatsWithoutThePermission(t *testing.T) {
	repo := &fakeRepo{}
	rec, body := getStats(t, NewService(repo, nil, clock.NewFake(statsAt), buildinfo.Info{}), "some.access.token")
	e, _ := body["error"].(map[string]any)
	if rec.Code != http.StatusNotFound || e["code"] != "not_found" {
		t.Fatalf("got %d %v, want 404 not_found", rec.Code, body)
	}
	if repo.calls != 0 {
		t.Fatalf("repository called %d times for a non-admin", repo.calls)
	}
}

func TestGetAdminStats(t *testing.T) {
	ended := statsAt.Add(-time.Hour)
	failed := "failed"
	repo := &fakeRepo{
		figures: Figures{AccountsTotal: 9, AccountsNew: 2, AccountsActive: 4, AccountsBlocked: 3, Upcoming: 1, Ongoing: 2, Finished: 3, Deleted: 1, Activities: 12, PendingAuth0Deletions: 1, BlockedAuth0Deletions: 1},
		runs: []TaskRun{
			{Task: "close-weeks", Run: WorkerRun{StartedAt: statsAt.Add(-time.Minute)}},
			{Task: "close-weeks", Failure: true, Run: WorkerRun{StartedAt: statsAt.Add(-2 * time.Hour), EndedAt: &ended, Outcome: &failed, ErrorCode: ptr("internal_error")}},
		},
	}
	svc := NewService(repo, nil, clock.NewFake(statsAt), buildinfo.Info{Version: "v1.2.3", Commit: "abc"})

	rec, got := getStats(t, svc, adminToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	want := map[string]any{
		"computed_at":             "2026-11-23T15:30:00Z",
		"accounts":                map[string]any{"total": 9.0, "new_last_7_days": 2.0, "active_last_7_days": 4.0, "blocked": 3.0},
		"challenges":              map[string]any{"upcoming": 1.0, "ongoing": 2.0, "finished": 3.0, "deleted": 1.0},
		"activities_last_7_days":  12.0,
		"pending_auth0_deletions": 1.0,
		"blocked_auth0_deletions": 1.0,
		"worker_tasks": []any{
			map[string]any{
				"task":     "close-weeks",
				"last_run": map[string]any{"started_at": "2026-11-23T15:29:00Z"},
				"last_failure": map[string]any{
					"started_at": "2026-11-23T13:30:00Z", "ended_at": "2026-11-23T14:30:00Z",
					"outcome": "failed", "error_code": "internal_error",
				},
			},
			map[string]any{"task": "finalize-challenges"},
			map[string]any{"task": "retry-auth0-deletions"},
		},
		"build": map[string]any{"version": "v1.2.3", "commit": "abc"},
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("body =\n%s\nwant\n%s", gotJSON, wantJSON)
	}
}

// TestWorkerTasksMatchTheContract: the dashboard lists every task the
// contract knows, and only those, so a task added to the worker is added to
// both.
func TestWorkerTasksMatchTheContract(t *testing.T) {
	contract := []api.AdminWorkerTaskTask{api.CloseWeeks, api.FinalizeChallenges, api.RetryAuth0Deletions}
	if len(workerTasks) != len(contract) {
		t.Fatalf("workerTasks = %v, contract = %v", workerTasks, contract)
	}
	for i, task := range workerTasks {
		if !api.AdminWorkerTaskTask(task).Valid() || string(contract[i]) != task {
			t.Errorf("workerTasks[%d] = %q, contract has %q", i, task, contract[i])
		}
	}
}

// TestInvalidAdminRequestIsHiddenWithoutThePermission: a malformed request to
// an /admin route (body, path parameter, query parameter) answers 404
// not_found without the admin permission, like a valid one, and names no
// field; an admin keeps 400 validation_failed with the fields (README §5.7).
func TestInvalidAdminRequestIsHiddenWithoutThePermission(t *testing.T) {
	requests := []struct {
		name, method, target, body, fields string
	}{
		{name: "empty reason", method: http.MethodPatch, target: "/api/v1/admin/challenges/" + challengeID, body: `{"reason": ""}`, fields: "reason"},
		{name: "null reason", method: http.MethodPatch, target: "/api/v1/admin/challenges/" + challengeID, body: `{"reason": null, "dry_run": true}`, fields: "reason"},
		{name: "unknown field", method: http.MethodPost, target: "/api/v1/admin/users/search", body: `{"x": 1}`},
		{name: "path id not a UUID", method: http.MethodGet, target: "/api/v1/admin/challenges/not-a-uuid"},
		{name: "query out of bounds", method: http.MethodGet, target: "/api/v1/admin/users?limit=0", fields: "limit"},
	}
	for _, rq := range requests {
		t.Run(rq.name, func(t *testing.T) {
			for _, tt := range []struct {
				token      string
				wantStatus int
				wantCode   string
			}{
				{token: "some.access.token", wantStatus: http.StatusNotFound, wantCode: "not_found"},
				{token: adminToken, wantStatus: http.StatusBadRequest, wantCode: "validation_failed"},
			} {
				repo := &fakeRepo{}
				rec, got := serveWith(t, NewHandler(NewService(repo, nil, clock.NewFake(statsAt), buildinfo.Info{}), nil, nil, nil, nil), tt.token, rq.method, rq.target, rq.body, io.Discard)
				e, _ := got["error"].(map[string]any)
				if rec.Code != tt.wantStatus || e["code"] != tt.wantCode {
					t.Fatalf("%s: got %d %v, want %d %s", tt.token, rec.Code, got, tt.wantStatus, tt.wantCode)
				}
				params, _ := e["params"].(map[string]any)
				if tt.wantStatus == http.StatusNotFound && params != nil {
					t.Fatalf("%s: a hidden route sends params back: %v", tt.token, got)
				}
				if tt.wantStatus == http.StatusBadRequest && rq.fields != "" && params["fields"] != rq.fields {
					t.Fatalf("%s: fields = %v, want %q", tt.token, params["fields"], rq.fields)
				}
				if repo.calls != 0 {
					t.Fatalf("%s: repository called %d times for an invalid request", tt.token, repo.calls)
				}
			}
		})
	}
}

// uncategorizedBoards ranks every challenge in no category.
type uncategorizedBoards struct{}

func (uncategorizedBoards) Get(context.Context, auth.Actor, string) (leaderboard.Leaderboard, error) {
	return leaderboard.Leaderboard{Categories: []leaderboard.CategoryRanking{}}, nil
}

func (uncategorizedBoards) Weeks(context.Context, auth.Actor, string) (leaderboard.Weeks, error) {
	return leaderboard.Weeks{}, nil
}

// TestAdminPreviewWithoutAReason: through each kind of body that takes
// dry_run, a preview goes without a reason (README §2.12), writing no
// journal line; a change without one is refused before anything is locked.
func TestAdminPreviewWithoutAReason(t *testing.T) {
	// Each body is open, its fields followed by a comma: dry_run ends it.
	routes := []struct {
		name, method, target, body string
	}{
		{name: "settings", method: http.MethodPatch, target: "/api/v1/admin/challenges/" + challengeID, body: `{"title": "Printemps", `},
		{name: "challenge deletion", method: http.MethodDelete, target: "/api/v1/admin/challenges/" + challengeID, body: `{`},
		{name: "participant", method: http.MethodPost, target: "/api/v1/admin/challenges/" + challengeID + "/participants", body: `{"user_id": "` + bobID + `", `},
		{
			name: "activity", method: http.MethodPost, target: "/api/v1/admin/users/" + bobID + "/activities",
			body: `{"sport_code": "run", "started_at": "2026-06-03T11:00:00Z", "duration_seconds": 1500, `,
		},
	}
	for _, r := range routes {
		t.Run(r.name, func(t *testing.T) {
			for _, tt := range []struct {
				name, dryRun string
				wantStatus   int
			}{
				{name: "preview", dryRun: `"dry_run": true}`, wantStatus: http.StatusOK},
				{name: "change", dryRun: `"dry_run": false}`, wantStatus: http.StatusBadRequest},
				{name: "change, dry_run absent", wantStatus: http.StatusBadRequest},
			} {
				body := r.body + tt.dryRun
				if tt.dryRun == "" {
					body = strings.TrimSuffix(r.body, ", ") + "}"
				}
				log := &correctionLog{}
				journal := &journalLog{log: log}
				corrections := NewCorrections(log, NewJournal(journal, clock.NewFake(statsAt)), log, uncategorizedBoards{})
				settings := &fakeSettings{log: log}
				h := NewHandler(NewService(&fakeRepo{}, nil, clock.NewFake(statsAt), buildinfo.Info{}),
					NewChallengeCorrections(corrections, settings, settings),
					NewMemberCorrections(corrections, &fakeMembers{log: log}),
					NewActivityCorrections(corrections, &fakeActivities{log: log, touched: []string{challengeID}, change: activity.AdminChange{ActivityID: actionID}}),
					nil)
				rec, got := serveWith(t, h, adminToken, r.method, r.target, body, io.Discard)
				if rec.Code != tt.wantStatus {
					t.Fatalf("%s: got %d %v, want %d", tt.name, rec.Code, got, tt.wantStatus)
				}
				if len(journal.records) != 0 {
					t.Fatalf("%s: journal %v, want no line", tt.name, journal.records)
				}
				if tt.wantStatus == http.StatusOK && (got["dry_run"] != true || !log.rolledBack) {
					t.Fatalf("%s: body %v, rolled back %v: want a preview, rolled back", tt.name, got, log.rolledBack)
				}
				if tt.wantStatus == http.StatusBadRequest {
					e, _ := got["error"].(map[string]any)
					params, _ := e["params"].(map[string]any)
					if e["code"] != "validation_failed" || params["fields"] != "reason" || len(log.steps) != 0 {
						t.Fatalf("%s: got %v, steps %v: want validation_failed on reason, nothing locked", tt.name, got, log.steps)
					}
				}
			}
		})
	}
}
