package httpx

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/damchi/sport-challenge-api/internal/platform/clock"
)

const allowedOrigin = "https://app.example.com"

// wantExposeHeaders is the Access-Control-Expose-Headers of an allowed
// origin: part of the contract with the web client.
const wantExposeHeaders = "Retry-After, WWW-Authenticate, X-Request-ID"

func TestCORS(t *testing.T) {
	tests := []struct {
		name            string
		method          string
		origin          string
		requestMethod   string
		wantStatus      int
		wantAllowOrigin string
		wantPreflight   bool
		wantServed      bool
		// wantExposed: the response headers a web client may read are exposed.
		wantExposed bool
	}{
		{name: "no origin (mobile app, curl)", method: http.MethodGet, wantStatus: http.StatusNoContent, wantServed: true},
		{name: "allowed origin", method: http.MethodGet, origin: allowedOrigin, wantStatus: http.StatusNoContent, wantAllowOrigin: allowedOrigin, wantServed: true, wantExposed: true},
		{name: "refused origin", method: http.MethodGet, origin: "https://evil.example.com", wantStatus: http.StatusNoContent, wantServed: true},
		{name: "origin differing by case", method: http.MethodGet, origin: "https://App.example.com", wantStatus: http.StatusNoContent, wantServed: true},
		{name: "origin with another port", method: http.MethodGet, origin: allowedOrigin + ":8443", wantStatus: http.StatusNoContent, wantServed: true},
		{name: "null origin", method: http.MethodGet, origin: "null", wantStatus: http.StatusNoContent, wantServed: true},
		{name: "allowed origin as a prefix", method: http.MethodGet, origin: allowedOrigin + ".evil.com", wantStatus: http.StatusNoContent, wantServed: true},
		{name: "allowed origin as a suffix", method: http.MethodGet, origin: "https://evil-app.example.com", wantStatus: http.StatusNoContent, wantServed: true},
		{name: "trailing dot", method: http.MethodGet, origin: allowedOrigin + ".", wantStatus: http.StatusNoContent, wantServed: true},
		{name: "same host over http", method: http.MethodGet, origin: "http://app.example.com", wantStatus: http.StatusNoContent, wantServed: true},
		{name: "preflight from an allowed origin", method: http.MethodOptions, origin: allowedOrigin, requestMethod: http.MethodPost, wantStatus: http.StatusNoContent, wantAllowOrigin: allowedOrigin, wantPreflight: true},
		{name: "preflight from a refused origin", method: http.MethodOptions, origin: "https://evil.example.com", requestMethod: http.MethodPost, wantStatus: http.StatusNoContent},
		{name: "preflight without origin", method: http.MethodOptions, requestMethod: http.MethodPost, wantStatus: http.StatusNotFound},
		{name: "OPTIONS without preflight header", method: http.MethodOptions, origin: allowedOrigin, wantStatus: http.StatusNotFound, wantAllowOrigin: allowedOrigin, wantExposed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := NewRouter(slog.New(slog.NewJSONHandler(io.Discard, nil)))
			r.Use(CORS([]string{allowedOrigin}))
			var served bool
			r.GET("/items", func(c *gin.Context) {
				served = true
				c.Status(http.StatusNoContent)
			})

			req := httptest.NewRequest(tt.method, "/items", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.requestMethod != "" {
				req.Header.Set("Access-Control-Request-Method", tt.requestMethod)
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			h := rec.Header()

			if rec.Code != tt.wantStatus || served != tt.wantServed {
				t.Fatalf("status %d, served %v, want %d, %v", rec.Code, served, tt.wantStatus, tt.wantServed)
			}
			if got := h.Get("Access-Control-Allow-Origin"); got != tt.wantAllowOrigin {
				t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, tt.wantAllowOrigin)
			}
			if !slices.Contains(h.Values("Vary"), "Origin") {
				t.Fatalf("Vary = %q, want Origin", h.Values("Vary"))
			}
			if h.Get("Access-Control-Allow-Credentials") != "" {
				t.Fatal("credentials allowed: the API uses bearer tokens only")
			}
			wantMethods, wantHeaders := "", ""
			if tt.wantPreflight {
				wantMethods, wantHeaders = corsAllowedMethods, "Authorization, Content-Type, Idempotency-Key, X-Request-ID"
			}
			if h.Get("Access-Control-Allow-Methods") != wantMethods || h.Get("Access-Control-Allow-Headers") != wantHeaders {
				t.Fatalf("allow methods %q, headers %q, want %q, %q",
					h.Get("Access-Control-Allow-Methods"), h.Get("Access-Control-Allow-Headers"), wantMethods, wantHeaders)
			}
			if tt.wantPreflight && h.Get("Access-Control-Max-Age") == "" {
				t.Fatal("preflight without Access-Control-Max-Age")
			}
			wantExpose := ""
			if tt.wantExposed {
				wantExpose = wantExposeHeaders
			}
			if got := h.Get("Access-Control-Expose-Headers"); got != wantExpose {
				t.Fatalf("Access-Control-Expose-Headers = %q, want %q", got, wantExpose)
			}
		})
	}
}

func TestCORSWithoutAllowedOrigins(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := NewRouter(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	r.Use(CORS(nil))
	r.GET("/items", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	req := httptest.NewRequest(http.MethodOptions, "/items", nil)
	req.Header.Set("Origin", allowedOrigin)
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q with no allowed origin", got)
	}
}

// A web client must read Retry-After on a 429: CORS runs before the rate
// limit, so the refused request carries the exposed headers too, and the
// headers it exposes are really there.
func TestCORSExposesRetryAfterOnRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	r := NewRouter(logger)
	r.Use(CORS([]string{allowedOrigin}))
	// One token, never refilled: the clock does not move.
	r.Use(RateLimitIP(NewRateLimiter(1, 1, clock.NewFake(time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC))), logger))
	r.GET("/items", func(c *gin.Context) { c.Status(http.StatusOK) })

	// The cases run in order on the same router: the second one finds the
	// bucket empty.
	tests := []struct {
		name           string
		wantStatus     int
		wantRetryAfter bool
	}{
		{name: "allowed request", wantStatus: http.StatusOK},
		{name: "rate limited request", wantStatus: http.StatusTooManyRequests, wantRetryAfter: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/items", nil)
			req.Header.Set("Origin", allowedOrigin)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			h := rec.Header()

			if rec.Code != tt.wantStatus {
				t.Fatalf("status %d, want %d", rec.Code, tt.wantStatus)
			}
			if got := h.Get("Access-Control-Expose-Headers"); got != wantExposeHeaders {
				t.Fatalf("Access-Control-Expose-Headers = %q, want %q", got, wantExposeHeaders)
			}
			if h.Get(RequestIDHeader) == "" {
				t.Fatalf("no %s to read", RequestIDHeader)
			}
			if got := h.Get("Retry-After") != ""; got != tt.wantRetryAfter {
				t.Fatalf("Retry-After present: %v, want %v", got, tt.wantRetryAfter)
			}
		})
	}
}
