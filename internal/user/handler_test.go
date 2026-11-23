package user

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
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/httpx"
)

const meID = "0b9d4a5e-6a43-4c8e-9d1f-7a2b3c4d5e6f"

// acceptingVerifier accepts any token; admin sets the admin permission of
// its claims.
type acceptingVerifier struct{ admin bool }

func (v acceptingVerifier) Verify(context.Context, string) (auth.Claims, error) {
	return auth.Claims{Subject: "email|123", Admin: v.admin}, nil
}

// newMeRouter serves the /me operations as cmd/api does: authentication,
// contract validation, then the handler on repo.
func newMeRouter(t *testing.T, repo repository) *gin.Engine {
	t.Helper()
	return newMeRouterFor(t, NewService(repo, nil, nil, nil, nil, discard))
}

// newMeRouterFor serves the /me operations of svc.
func newMeRouterFor(t *testing.T, svc *Service) *gin.Engine {
	t.Helper()
	return newMeRouterWith(t, svc, acceptingVerifier{})
}

// newMeRouterWith serves the /me operations of svc, with tokens verified by
// verifier.
func newMeRouterWith(t *testing.T, svc *Service, verifier auth.TokenVerifier) *gin.Engine {
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
		auth.Authenticate(verifier, logger),
		auth.CurrentUser(func(context.Context, auth.Claims) (string, error) { return meID, nil }, logger),
	))
	strict := api.NewStrictHandlerWithOptions(meHandlers{Handler: NewHandler(svc)}, nil, api.StrictGinServerOptions{
		RequestErrorHandlerFunc:  httpx.BadRequestHandler(logger),
		HandlerErrorFunc:         httpx.ErrorHandler(logger),
		ResponseErrorHandlerFunc: httpx.ErrorHandler(logger),
	})
	api.RegisterHandlersWithOptions(r, strict, api.GinServerOptions{
		ErrorHandler: httpx.InvalidParamHandler(logger),
		Middlewares:  []api.MiddlewareFunc{api.MiddlewareFunc(validate)},
	})
	return r
}

// meHandlers completes the contract with probes that are never called.
type meHandlers struct {
	*Handler
	apitest.Others
}

type response struct {
	status int
	body   map[string]any
	code   string
	fields string
}

func do(t *testing.T, r *gin.Engine, method, body string) response {
	t.Helper()
	return doWithType(t, r, method, body, "application/json")
}

func doWithType(t *testing.T, r *gin.Engine, method, body, contentType string) response {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, "/api/v1/me", reader)
	req.Header.Set("Authorization", "Bearer some.access.token")
	if body != "" && contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	// The request body was read: give the validator a fresh copy.
	check := httptest.NewRequest(method, "/api/v1/me", nil)
	apitest.ValidateResponse(t, check, rec)

	res := response{status: rec.Code}
	if rec.Body.Len() == 0 { // 204
		return res
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res.body); err != nil {
		t.Fatalf("body is not JSON: %s", rec.Body)
	}
	if e, ok := res.body["error"].(map[string]any); ok {
		res.code, _ = e["code"].(string)
		if params, ok := e["params"].(map[string]any); ok {
			res.fields, _ = params["fields"].(string)
		}
	}
	return res
}

func TestGetMe(t *testing.T) {
	tests := []struct {
		name         string
		user         User
		wantComplete bool
	}{
		{name: "new user, empty profile", user: User{ID: meID}},
		{name: "pseudo without names", user: User{ID: meID, Pseudo: ptr("marie_92"), Language: ptr("fr")}},
		{
			name:         "names without a pseudo",
			user:         User{ID: meID, FirstName: ptr("Marie-Ève"), LastName: ptr("Tremblay")},
			wantComplete: true,
		},
		{
			name:         "complete profile",
			user:         User{ID: meID, Pseudo: ptr("marie_92"), FirstName: ptr("Marie-Ève"), LastName: ptr("Tremblay"), Email: ptr("marie@example.com"), Language: ptr("fr")},
			wantComplete: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := do(t, newMeRouter(t, fakeRepository{user: tt.user}), http.MethodGet, "")
			if res.status != http.StatusOK || res.body["id"] != meID || res.body["profile_complete"] != tt.wantComplete || res.body["avatar"] != nil {
				t.Fatalf("got %d %v, want 200 for %s with profile_complete %v", res.status, res.body, meID, tt.wantComplete)
			}
			// The owner reads their own names and e-mail.
			for field, want := range map[string]*string{"first_name": tt.user.FirstName, "last_name": tt.user.LastName, "email": tt.user.Email} {
				got, ok := res.body[field]
				if !ok || (want == nil && got != nil) || (want != nil && got != *want) {
					t.Fatalf("%s = %v, want %v", field, got, want)
				}
			}
		})
	}
}

// TestMeIsAdmin: is_admin comes from the admin permission of the request's
// token, on every route that answers the profile.
func TestMeIsAdmin(t *testing.T) {
	user := User{ID: meID, FirstName: ptr("Marie-Ève"), LastName: ptr("Tremblay")}
	tests := []struct {
		name   string
		method string
		body   string
		admin  bool
	}{
		{name: "GET without the permission", method: http.MethodGet},
		{name: "GET with the admin permission", method: http.MethodGet, admin: true},
		{name: "PATCH without the permission", method: http.MethodPatch, body: `{"language":"fr"}`},
		{name: "PATCH with the admin permission", method: http.MethodPatch, body: `{"language":"fr"}`, admin: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(fakeRepository{user: user, changes: &ProfileChanges{}}, nil, nil, nil, nil, discard)
			res := do(t, newMeRouterWith(t, svc, acceptingVerifier{admin: tt.admin}), tt.method, tt.body)
			if res.status != http.StatusOK || res.body["is_admin"] != tt.admin {
				t.Fatalf("got %d is_admin = %v, want 200 with is_admin = %v", res.status, res.body["is_admin"], tt.admin)
			}
		})
	}
}

func TestPatchMe(t *testing.T) {
	stored := User{ID: meID, Pseudo: ptr("marie_92"), Language: ptr("en")}
	tests := []struct {
		name       string
		body       string
		repoErr    error
		wantStatus int
		wantCode   string
		wantFields string
		wantPseudo *string
		wantRemove bool
		wantFirst  *string
		wantLast   *string
	}{
		{name: "pseudo and language", body: `{"pseudo":"marie_92","language":"en"}`, wantStatus: 200, wantPseudo: ptr("marie_92")},
		{name: "first and last name", body: `{"first_name":"Marie-Ève","last_name":"D'Amours"}`, wantStatus: 200, wantFirst: ptr("Marie-Ève"), wantLast: ptr("D'Amours")},
		{name: "null first name refused", body: `{"first_name":null}`, wantStatus: 400, wantCode: "validation_failed", wantFields: "first_name"},
		{name: "null last name refused", body: `{"last_name":null}`, wantStatus: 400, wantCode: "validation_failed", wantFields: "last_name"},
		{name: "empty last name refused", body: `{"last_name":""}`, wantStatus: 400, wantCode: "validation_failed", wantFields: "last_name"},
		{name: "first name with a leading space", body: `{"first_name":" Marie"}`, wantStatus: 400, wantCode: "validation_failed", wantFields: "first_name"},
		{name: "last name with a digit", body: `{"last_name":"Tremblay2"}`, wantStatus: 400, wantCode: "validation_failed", wantFields: "last_name"},
		{name: "last name over 50 characters", body: `{"last_name":"` + strings.Repeat("é", 51) + `"}`, wantStatus: 400, wantCode: "validation_failed", wantFields: "last_name"},
		{name: "e-mail not editable", body: `{"email":"other@example.com"}`, wantStatus: 400, wantCode: "validation_failed"},
		{name: "empty object changes nothing", body: `{}`, wantStatus: 200},
		{name: "null pseudo removes it", body: `{"pseudo":null}`, wantStatus: 200, wantRemove: true},
		{name: "null pseudo with a first name", body: `{"pseudo":null,"first_name":"Ana","last_name":"Lee"}`, wantStatus: 200, wantRemove: true, wantFirst: ptr("Ana"), wantLast: ptr("Lee")},
		{name: "null language refused", body: `{"language":null}`, wantStatus: 400, wantCode: "validation_failed", wantFields: "language"},
		{name: "invalid pseudo and language", body: `{"pseudo":"a b","language":"de"}`, wantStatus: 400, wantCode: "validation_failed", wantFields: "language,pseudo"},
		{name: "pseudo with an accent", body: `{"pseudo":"élodie"}`, wantStatus: 400, wantCode: "validation_failed", wantFields: "pseudo"},
		{name: "unknown field", body: `{"pseudo":"marie_92","avatar":"x"}`, wantStatus: 400, wantCode: "validation_failed"},
		{name: "field with another case", body: `{"Pseudo":"marie_92"}`, wantStatus: 400, wantCode: "validation_failed"},
		{name: "not JSON", body: `pseudo=marie`, wantStatus: 400, wantCode: "validation_failed"},
		{name: "no body", wantStatus: 400, wantCode: "validation_failed"},
		{name: "body over 64 KiB", body: `{"pseudo":"` + strings.Repeat("a", 64<<10) + `"}`, wantStatus: 400, wantCode: "validation_failed"},
		{name: "duplicate key", body: `{"pseudo":"marie_92","pseudo":null}`, wantStatus: 400, wantCode: "validation_failed"},
		{name: "identifier in the body", body: `{"id":"11111111-1111-1111-1111-111111111111"}`, wantStatus: 400, wantCode: "validation_failed"},
		{name: "pseudo taken", body: `{"pseudo":"Marie_92"}`, repoErr: errPseudoTaken, wantStatus: 409, wantCode: "validation_failed", wantFields: "pseudo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var written ProfileChanges
			repo := fakeRepository{user: stored, err: tt.repoErr, changes: &written}
			res := do(t, newMeRouter(t, repo), http.MethodPatch, tt.body)

			if res.status != tt.wantStatus || res.code != tt.wantCode || res.fields != tt.wantFields {
				t.Fatalf("got %d %q fields %q, want %d %q fields %q", res.status, res.code, res.fields, tt.wantStatus, tt.wantCode, tt.wantFields)
			}
			if tt.wantPseudo != nil && (written.Pseudo == nil || *written.Pseudo != *tt.wantPseudo) {
				t.Fatalf("pseudo written = %v, want %q", written.Pseudo, *tt.wantPseudo)
			}
			if written.RemovePseudo != tt.wantRemove || (tt.wantRemove && written.Pseudo != nil) {
				t.Fatalf("pseudo removed = %v (written %v), want removed = %v", written.RemovePseudo, written.Pseudo, tt.wantRemove)
			}
			if tt.wantFirst != nil && (written.FirstName == nil || *written.FirstName != *tt.wantFirst || written.LastName == nil || *written.LastName != *tt.wantLast) {
				t.Fatalf("names written = %v %v, want %q %q", written.FirstName, written.LastName, *tt.wantFirst, *tt.wantLast)
			}
		})
	}
}

func TestPatchMeRequiresAJSONBody(t *testing.T) {
	for _, contentType := range []string{"text/plain", ""} {
		t.Run("content type "+contentType, func(t *testing.T) {
			var written ProfileChanges
			repo := fakeRepository{user: User{ID: meID}, changes: &written}
			res := doWithType(t, newMeRouter(t, repo), http.MethodPatch, `{"pseudo":"marie_92"}`, contentType)
			if res.status != http.StatusBadRequest || res.code != "validation_failed" || written.Pseudo != nil {
				t.Fatalf("got %d %q, pseudo written %v, want 400 validation_failed and nothing written", res.status, res.code, written.Pseudo)
			}
		})
	}
}

func TestDeleteMe(t *testing.T) {
	repo := fakeRepository{user: User{ID: meID, AuthSubject: "email|123"}, deleted: new(string)}
	identities := &fakeIdentities{}
	res := do(t, newMeRouterFor(t, NewService(repo, &fakeParticipations{}, nil, identities, clock.Real{}, discard)), http.MethodDelete, "")
	if res.status != http.StatusNoContent || *repo.deleted != meID {
		t.Fatalf("got %d, deleted %q, want 204 for the user of the token", res.status, *repo.deleted)
	}
}

// TestDeleteMeLastCreator checks the refusal of the rule of the last creator
// (README §2.2) as the contract describes it: 422 last_creator, the account
// left untouched and nothing deleted at the auth provider.
func TestDeleteMeLastCreator(t *testing.T) {
	refusal := apperr.New(apperr.CodeLastCreator, nil)
	refusal.Params = map[string]string{apperr.ChallengesParam: "c1,c2"}
	repo := fakeRepository{user: User{ID: meID, AuthSubject: "email|123"}, deleted: new(string)}
	identities := &fakeIdentities{}
	res := do(t, newMeRouterFor(t, NewService(repo, &fakeParticipations{err: refusal}, nil, identities, clock.Real{}, discard)), http.MethodDelete, "")
	if res.status != http.StatusUnprocessableEntity || res.code != "last_creator" {
		t.Fatalf("got %d %s, want 422 last_creator", res.status, res.code)
	}
	params, _ := res.body["error"].(map[string]any)["params"].(map[string]any)
	if params[apperr.ChallengesParam] != "c1,c2" {
		t.Fatalf("params = %v, want challenges c1,c2", params)
	}
	if *repo.deleted != "" || identities.deleted != "" {
		t.Fatalf("deleted %q, at the auth provider %q, want nothing", *repo.deleted, identities.deleted)
	}
}

func TestDeleteMeConcurrentlyDeleted(t *testing.T) {
	repo := fakeRepository{deleteErr: errUserNotFound}
	res := do(t, newMeRouterFor(t, NewService(repo, &fakeParticipations{}, nil, &fakeIdentities{}, clock.Real{}, discard)), http.MethodDelete, "")
	if res.status != http.StatusUnauthorized || res.code != "account_deleted" {
		t.Fatalf("got %d %s, want 401 account_deleted", res.status, res.code)
	}
}
