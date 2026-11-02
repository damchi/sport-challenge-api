package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/damchi/sport-challenge-api/internal/platform/httpx"
)

const goodToken = "header.payload.signature-of-a-valid-token"

// fakeVerifier accepts goodToken and fails every other token with err.
type fakeVerifier struct {
	err      error
	received string
}

func (f *fakeVerifier) Verify(_ context.Context, raw string) (Claims, error) {
	f.received = raw
	if raw != goodToken {
		return Claims{}, f.err
	}
	return Claims{Subject: "email|123", ExpiresAt: time.Now().Add(time.Hour), Scopes: []string{"openid"}}, nil
}

type result struct {
	status   int
	code     string
	wwwAuth  string
	subject  string
	logs     string
	received string
}

func call(t *testing.T, verifierErr error, authorization string) result {
	t.Helper()
	gin.SetMode(gin.TestMode)
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	verifier := &fakeVerifier{err: verifierErr}

	r := httpx.NewRouter(logger)
	var subject string
	r.GET("/private", Authenticate(verifier, logger), func(c *gin.Context) {
		// The generated handlers receive the *gin.Context as context.Context.
		var ctx context.Context = c
		claims, ok := ClaimsFromContext(ctx)
		if !ok {
			t.Error("claims not in the context")
		}
		subject = claims.Subject
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/private", nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	res := result{status: rec.Code, wwwAuth: rec.Header().Get("WWW-Authenticate"), subject: subject, logs: logs.String(), received: verifier.received}
	if rec.Code >= 400 && rec.Body.Len() > 0 {
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("body is not the error format: %s", rec.Body)
		}
		res.code = body.Error.Code
	}
	return res
}

func TestAuthenticate(t *testing.T) {
	invalid := fmt.Errorf("%w: kid %q not found", ErrInvalidToken, "attacker-kid")
	expired := fmt.Errorf("%w: token is expired", ErrTokenExpired)

	tests := []struct {
		name          string
		authorization string
		verifierErr   error
		wantStatus    int
		wantCode      string
		wantWWWAuth   string
	}{
		{name: "valid token", authorization: "Bearer " + goodToken, wantStatus: http.StatusNoContent},
		{name: "scheme is case-insensitive", authorization: "bearer " + goodToken, wantStatus: http.StatusNoContent},
		{name: "no header", wantStatus: 401, wantCode: "unauthenticated", wantWWWAuth: "Bearer"},
		{name: "other scheme", authorization: "Basic dXNlcjpwYXNz", wantStatus: 401, wantCode: "unauthenticated", wantWWWAuth: "Bearer"},
		{name: "scheme without token", authorization: "Bearer", wantStatus: 401, wantCode: "unauthenticated", wantWWWAuth: "Bearer"},
		{name: "empty token", authorization: "Bearer ", wantStatus: 401, wantCode: "unauthenticated", wantWWWAuth: "Bearer"},
		{name: "extra space", authorization: "Bearer  " + goodToken, wantStatus: 401, wantCode: "unauthenticated", wantWWWAuth: "Bearer"},
		{name: "token with a space", authorization: "Bearer abc def", wantStatus: 401, wantCode: "unauthenticated", wantWWWAuth: "Bearer"},
		{name: "invalid token", authorization: "Bearer forged.token.value", verifierErr: invalid, wantStatus: 401, wantCode: "invalid_token", wantWWWAuth: `Bearer error="invalid_token"`},
		{name: "expired token", authorization: "Bearer expired.token.value", verifierErr: expired, wantStatus: 401, wantCode: "token_expired", wantWWWAuth: `Bearer error="invalid_token"`},
		{name: "token too long", authorization: "Bearer " + strings.Repeat("a", maxTokenLength+1), wantStatus: 401, wantCode: "unauthenticated", wantWWWAuth: "Bearer"},
		{name: "verification unavailable", authorization: "Bearer other.token.value", verifierErr: fmt.Errorf("verify token: %w", context.DeadlineExceeded), wantStatus: 500, wantCode: "internal_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := call(t, tt.verifierErr, tt.authorization)

			if got.status != tt.wantStatus || got.code != tt.wantCode || got.wwwAuth != tt.wantWWWAuth {
				t.Fatalf("got %d %q WWW-Authenticate=%q, want %d %q %q",
					got.status, got.code, got.wwwAuth, tt.wantStatus, tt.wantCode, tt.wantWWWAuth)
			}
			if tt.wantStatus == http.StatusNoContent && got.subject != "email|123" {
				t.Fatalf("subject in context = %q, want email|123", got.subject)
			}
		})
	}
}

func TestAuthenticatePassesTheExactToken(t *testing.T) {
	got := call(t, nil, "Bearer "+goodToken)
	if got.received != goodToken {
		t.Fatalf("verifier received %q, want %q", got.received, goodToken)
	}
}

func TestAuthenticateNeverLogsTheToken(t *testing.T) {
	for _, raw := range []string{goodToken, "forged.token.value"} {
		got := call(t, fmt.Errorf("%w: kid %q not found", ErrInvalidToken, "attacker-kid"), "Bearer "+raw)
		for _, leak := range []string{raw, "attacker-kid"} {
			if strings.Contains(got.logs, leak) {
				t.Fatalf("logs contain %q: %s", leak, got.logs)
			}
		}
	}
}

func TestAuthenticateRejectsRepeatedAuthorizationHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	r := httpx.NewRouter(logger)
	r.GET("/private", Authenticate(&fakeVerifier{}, logger), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	req := httptest.NewRequest(http.MethodGet, "/private", nil)
	req.Header.Add("Authorization", "Bearer "+goodToken)
	req.Header.Add("Authorization", "Bearer other.token.value")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "unauthenticated") {
		t.Fatalf("got %d %s, want 401 unauthenticated", rec.Code, rec.Body)
	}
}

func TestAuthenticateStopsWhenTheClientIsGone(t *testing.T) {
	got := call(t, fmt.Errorf("verify token: %w", context.Canceled), "Bearer other.token.value")
	if got.subject != "" || strings.Contains(got.logs, "unavailable") {
		t.Fatalf("got %+v, want the request aborted without an error log", got)
	}
	if got.status != statusClientClosedRequest || !strings.Contains(got.logs, `"status":499`) {
		t.Fatalf("status = %d, logs = %s: want 499 in the response and the request log", got.status, got.logs)
	}
}

func TestAuthenticateLogsUnavailableVerification(t *testing.T) {
	got := call(t, fmt.Errorf("verify token: kid %q: %w", "attacker-kid", context.DeadlineExceeded), "Bearer other.token.value")
	if !strings.Contains(got.logs, "access token verification unavailable") {
		t.Fatalf("logs = %s, want the outage logged", got.logs)
	}
	if strings.Contains(got.logs, "attacker-kid") || strings.Contains(got.logs, "other.token.value") {
		t.Fatalf("logs leak client values: %s", got.logs)
	}
}
