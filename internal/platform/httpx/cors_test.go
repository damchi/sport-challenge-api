package httpx

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/gin-gonic/gin"
)

const allowedOrigin = "https://app.example.com"

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
	}{
		{name: "no origin (mobile app, curl)", method: http.MethodGet, wantStatus: http.StatusNoContent, wantServed: true},
		{name: "allowed origin", method: http.MethodGet, origin: allowedOrigin, wantStatus: http.StatusNoContent, wantAllowOrigin: allowedOrigin, wantServed: true},
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
		{name: "OPTIONS without preflight header", method: http.MethodOptions, origin: allowedOrigin, wantStatus: http.StatusNotFound, wantAllowOrigin: allowedOrigin},
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
