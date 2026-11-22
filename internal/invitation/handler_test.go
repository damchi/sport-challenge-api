package invitation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/damchi/sport-challenge-api/internal/api"
	"github.com/damchi/sport-challenge-api/internal/api/apitest"
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/platform/httpx"
)

type acceptingVerifier struct{}

func (acceptingVerifier) Verify(context.Context, string) (auth.Claims, error) {
	return auth.Claims{Subject: "email|123"}, nil
}

type invitationsOnly struct {
	*Handler
	apitest.Others
}

// fakeDetails answers the challenge joined, as module challenge would.
type fakeDetails struct{ err error }

func (f fakeDetails) ChallengeDetail(_ context.Context, _, id string) (api.ChallengeDetail, error) {
	if f.err != nil {
		return api.ChallengeDetail{}, f.err
	}
	status := api.ChallengeDetailMyStatusActive
	return api.ChallengeDetail{
		Id: uuid.MustParse(id), Title: "Spring run", Visibility: api.ChallengeDetailVisibilityPrivate,
		Timezone: "America/Toronto", StartsAt: start, EndsAt: end, Categories: []api.Category{},
		MyRole: api.ChallengeDetailMyRoleParticipant, MyStatus: &status,
		Status: api.ChallengeDetailStatusOngoing, RegistrationDeadline: deadline, WeekCount: 6,
	}, nil
}

// serve serves a request as cmd/api does (authentication, contract
// validation, handler) and checks the response against the contract. No
// access token is sent when authenticated is false.
func serve(t *testing.T, svc *Service, method, target, body string, authenticated bool) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	return serveWith(t, NewHandler(svc, fakeDetails{}), method, target, body, authenticated)
}

func serveWith(t *testing.T, h *Handler, method, target, body string, authenticated bool) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	return serveLogged(t, h, io.Discard, method, target, body, authenticated)
}

// serveLogged is serveWith with the server logs written to logs. An empty
// body sends none.
func serveLogged(t *testing.T, h *Handler, logs io.Writer, method, target, body string, authenticated bool) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
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
	api.RegisterHandlersWithOptions(r, api.NewStrictHandlerWithOptions(invitationsOnly{Handler: h}, nil, api.StrictGinServerOptions{
		RequestErrorHandlerFunc:  httpx.BadRequestHandler(logger),
		HandlerErrorFunc:         httpx.ErrorHandler(logger),
		ResponseErrorHandlerFunc: httpx.ErrorHandler(logger),
	}), api.GinServerOptions{
		ErrorHandler: httpx.InvalidParamHandler(logger),
		Middlewares:  []api.MiddlewareFunc{api.MiddlewareFunc(validate)},
	})

	req := httptest.NewRequest(method, target, nil)
	if body != "" {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	if authenticated {
		req.Header.Set("Authorization", "Bearer some.access.token")
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	check := httptest.NewRequest(method, target, nil)
	if body != "" {
		check = httptest.NewRequest(method, target, strings.NewReader(body))
		check.Header.Set("Content-Type", "application/json")
	}
	apitest.ValidateResponse(t, check, rec)

	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("body is not JSON: %s", rec.Body)
	}
	return rec, out
}

// The routes of a token (README §5.7: in the body, never in the URL).
const (
	openTarget   = "/api/v1/invitations/open"
	acceptTarget = "/api/v1/invitations/accept"
)

// tokenBody is the body that carries token.
func tokenBody(token string) string { return `{"token":"` + token + `"}` }

func code(out map[string]any) any {
	e, _ := out["error"].(map[string]any)
	return e["code"]
}

func TestCreateInvitation(t *testing.T) {
	const target = "/api/v1/challenges/" + challengeID + "/invitations"
	tests := []struct {
		name       string
		refusal    error
		wantStatus int
		wantCode   string
	}{
		{name: "created", wantStatus: 201},
		{name: "may not invite", refusal: apperr.New(apperr.CodeForbidden, nil), wantStatus: 403, wantCode: "forbidden"},
		{name: "hidden challenge", refusal: apperr.New(apperr.CodeChallengeNotFound, nil), wantStatus: 404, wantCode: "challenge_not_found"},
		{name: "after week 1", refusal: apperr.New(apperr.CodeRegistrationClosed, nil), wantStatus: 422, wantCode: "registration_closed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{}
			rec, out := serve(t, NewService(fakeChallenges{err: tt.refusal}, repo, clock.NewFake(now)), http.MethodPost, target, "", true)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if tt.wantStatus != 201 {
				if code(out) != tt.wantCode || repo.created != nil {
					t.Fatalf("code %v, created %v, want %s and nothing stored", code(out), repo.created, tt.wantCode)
				}
				return
			}
			token, _ := out["token"].(string)
			if len(token) != 43 || out["expires_at"] != "2026-03-09T04:00:00Z" {
				t.Fatalf("body %v, want a token of 43 characters expiring at the end of week 1", out)
			}
		})
	}
}

func TestOpenInvitation(t *testing.T) {
	token, hash := validToken()
	repo := &fakeRepository{valid: hash}
	rec, out := serve(t, NewService(fakeChallenges{}, repo, clock.NewFake(now)), http.MethodPost, openTarget, tokenBody(token), true)
	want := map[string]any{"challenge_id": challengeID, "title": "Spring run", "starts_at": "2026-03-02T05:00:00Z", "ends_at": "2026-04-13T04:00:00Z"}
	if rec.Code != 200 || !reflect.DeepEqual(out, want) {
		t.Fatalf("got %d %v, want 200 %v", rec.Code, out, want)
	}
	if repo.linkedUser != actor {
		t.Fatal("opening the link did not link the actor")
	}
}

// TestOpenInvitationHiddenAfterTheLink: a challenge deleted right after the
// link answers as any invalid invitation, never about the challenge.
func TestOpenInvitationHiddenAfterTheLink(t *testing.T) {
	token, hash := validToken()
	svc := NewService(fakeChallenges{previewErr: apperr.New(apperr.CodeChallengeNotFound, nil)}, &fakeRepository{valid: hash}, clock.NewFake(now))
	rec, out := serve(t, svc, http.MethodPost, openTarget, tokenBody(token), true)
	if rec.Code != http.StatusNotFound || code(out) != "invitation_invalid" {
		t.Fatalf("got %d %s, want 404 invitation_invalid", rec.Code, rec.Body)
	}
}

// TestTokenIsNeverLoggedNorAnswered: the token is a bearer secret, sent in
// the body. Neither the logs nor the answers carry it, on a success, a 404
// and a 500.
func TestTokenIsNeverLoggedNorAnswered(t *testing.T) {
	token, hash := validToken()
	unknown := "CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAg"
	storage := errors.New("connection refused")
	tests := []struct {
		name       string
		token      string
		repo       *fakeRepository
		details    fakeDetails
		wantStatus int
	}{
		{name: "opened and joined", token: token, repo: &fakeRepository{valid: hash}, wantStatus: 200},
		{name: "unknown token", token: unknown, repo: &fakeRepository{valid: hash}, wantStatus: 404},
		{name: "storage fails", token: token, repo: &fakeRepository{err: storage}, wantStatus: 500},
		{name: "challenge read fails after the join", token: token, repo: &fakeRepository{valid: hash}, details: fakeDetails{err: storage}, wantStatus: 500},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			h := NewHandler(NewService(fakeChallenges{}, tt.repo, clock.NewFake(now)), tt.details)
			get, _ := serveLogged(t, h, &logs, http.MethodPost, openTarget, tokenBody(tt.token), true)
			accept, _ := serveLogged(t, h, &logs, http.MethodPost, acceptTarget, tokenBody(tt.token), true)
			if get.Code != tt.wantStatus && accept.Code != tt.wantStatus {
				t.Fatalf("statuses %d and %d, want a %d", get.Code, accept.Code, tt.wantStatus)
			}
			if logs.Len() == 0 {
				t.Fatal("nothing logged: the test reads no log")
			}
			for where, text := range map[string]string{"logs": logs.String(), "open answer": get.Body.String(), "accept answer": accept.Body.String()} {
				if strings.Contains(text, tt.token) {
					t.Fatalf("the token is in the %s: %s", where, text)
				}
			}
		})
	}
}

// TestInvalidInvitationIsTheSameAnswer: an unknown token and a malformed
// one answer the same 404 invitation_invalid, on both routes of a token.
func TestInvalidInvitationIsTheSameAnswer(t *testing.T) {
	_, hash := validToken()
	unknown := "CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAg"
	var bodies []map[string]any
	for _, target := range []string{openTarget, acceptTarget} {
		for _, token := range []string{unknown, "not-a-token"} {
			var joined [2]string
			rec, out := serve(t, NewService(fakeChallenges{joined: &joined}, &fakeRepository{valid: hash}, clock.NewFake(now)), http.MethodPost, target, tokenBody(token), true)
			if rec.Code != http.StatusNotFound || code(out) != "invitation_invalid" {
				t.Fatalf("%s with %s: got %d %v, want 404 invitation_invalid", target, token, rec.Code, out)
			}
			if joined != [2]string{} {
				t.Fatalf("%s with %s: joined despite the invalid invitation", target, token)
			}
			e, _ := out["error"].(map[string]any)
			delete(e, "request_id")
			bodies = append(bodies, out)
		}
	}
	for _, b := range bodies[1:] {
		if !reflect.DeepEqual(bodies[0], b) {
			t.Fatalf("bodies %v and %v, want the same invitation_invalid", bodies[0], b)
		}
	}
}

func TestAcceptInvitation(t *testing.T) {
	token, hash := validToken()
	tests := []struct {
		name       string
		refusal    error
		wantStatus int
		wantCode   string
	}{
		{name: "joined", wantStatus: 201},
		{name: "already in", refusal: apperr.New(apperr.CodeAlreadyParticipant, nil), wantStatus: 409, wantCode: "already_participant"},
		{name: "abandoned before", refusal: apperr.New(apperr.CodeParticipationAbandoned, nil), wantStatus: 422, wantCode: "participation_abandoned"},
		{name: "incomplete profile", refusal: apperr.New(apperr.CodeProfileIncomplete, nil), wantStatus: 422, wantCode: "profile_incomplete"},
		{name: "invitation invalid at the join", refusal: apperr.New(apperr.CodeChallengeNotFound, nil), wantStatus: 404, wantCode: "invitation_invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var joined [2]string
			svc := NewService(fakeChallenges{joinErr: tt.refusal, joined: &joined}, &fakeRepository{valid: hash}, clock.NewFake(now))
			rec, out := serve(t, svc, http.MethodPost, acceptTarget, tokenBody(token), true)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if tt.wantStatus != 201 {
				if code(out) != tt.wantCode {
					t.Fatalf("code %v, want %s", code(out), tt.wantCode)
				}
				return
			}
			if joined != [2]string{actor, challengeID} || out["id"] != challengeID || out["my_role"] != "participant" {
				t.Fatalf("joined %v, body %v, want the actor a participant of %s", joined, out, challengeID)
			}
		})
	}
}

// TestAcceptInvitationDetailFails: the challenge is read after the join. A
// failure of that read is a 500 without its cause, the join stays.
func TestAcceptInvitationDetailFails(t *testing.T) {
	token, hash := validToken()
	var joined [2]string
	svc := NewService(fakeChallenges{joined: &joined}, &fakeRepository{valid: hash}, clock.NewFake(now))
	h := NewHandler(svc, fakeDetails{err: errors.New("connection refused")})
	rec, out := serveWith(t, h, http.MethodPost, acceptTarget, tokenBody(token), true)
	if rec.Code != http.StatusInternalServerError || code(out) != "internal_error" || strings.Contains(rec.Body.String(), "connection refused") {
		t.Fatalf("got %d %s, want 500 internal_error without the cause", rec.Code, rec.Body)
	}
	if joined != [2]string{actor, challengeID} {
		t.Fatalf("joined %v, want the join made before the read", joined)
	}

	// The challenge deleted right after the join: never an answer about a
	// challenge on a route of an invitation.
	h = NewHandler(svc, fakeDetails{err: apperr.New(apperr.CodeChallengeNotFound, nil)})
	rec, out = serveWith(t, h, http.MethodPost, acceptTarget, tokenBody(token), true)
	if rec.Code != http.StatusNotFound || code(out) != "invitation_invalid" {
		t.Fatalf("got %d %s, want 404 invitation_invalid", rec.Code, rec.Body)
	}
}

// TestInvitationRoutesRequireAuthentication: a token is not a credential,
// every route of an invitation needs a logged-in user.
func TestInvitationRoutesRequireAuthentication(t *testing.T) {
	token, hash := validToken()
	routes := map[string]string{
		"/api/v1/challenges/" + challengeID + "/invitations": "",
		openTarget:   tokenBody(token),
		acceptTarget: tokenBody(token),
	}
	for target, body := range routes {
		var joined [2]string
		repo := &fakeRepository{valid: hash}
		rec, out := serve(t, NewService(fakeChallenges{joined: &joined}, repo, clock.NewFake(now)), http.MethodPost, target, body, false)
		if rec.Code != http.StatusUnauthorized || code(out) != "unauthenticated" {
			t.Fatalf("POST %s: got %d %v, want 401 unauthenticated", target, rec.Code, out)
		}
		if repo.created != nil || repo.linkedUser != "" || joined != [2]string{} {
			t.Fatalf("POST %s: something was written without authentication", target)
		}
	}
}

// TestInvitationTokenMissing: the body must carry a token, and only it (400
// validation_failed); a token in the query string is never read; no answer
// carries the token.
func TestInvitationTokenMissing(t *testing.T) {
	token, hash := validToken()
	tests := []struct {
		name, query, body string
	}{
		{name: "no body", body: ""},
		{name: "empty object", body: `{}`},
		{name: "empty token", body: `{"token":""}`},
		{name: "malformed JSON", body: `{"token":`},
		{name: "unknown property", body: `{"token":"` + token + `","extra":1}`},
		{name: "token in the query string only", query: "?token=" + token, body: ""},
	}
	for _, target := range []string{openTarget, acceptTarget} {
		for _, tt := range tests {
			t.Run(target+" "+tt.name, func(t *testing.T) {
				repo := &fakeRepository{valid: hash}
				rec, out := serve(t, NewService(fakeChallenges{}, repo, clock.NewFake(now)), http.MethodPost, target+tt.query, tt.body, true)
				if rec.Code != http.StatusBadRequest || code(out) != "validation_failed" {
					t.Fatalf("got %d %v, want 400 validation_failed", rec.Code, out)
				}
				if repo.linkedUser != "" || strings.Contains(rec.Body.String(), token) {
					t.Fatalf("linked %q, answer %s: want nothing linked and no token in the answer", repo.linkedUser, rec.Body)
				}
			})
		}
	}
}
