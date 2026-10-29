package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

// newTestRouter returns a router whose logs are captured as JSON lines.
func newTestRouter(t *testing.T) (*gin.Engine, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	r := NewRouter(slog.New(slog.NewJSONHandler(&logs, nil)))
	r.GET("/items/:id", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	r.GET("/panic", func(*gin.Context) { panic("boom") })
	r.GET("/abort", func(*gin.Context) { panic(http.ErrAbortHandler) })
	r.GET("/fail-late", func(c *gin.Context) {
		c.String(http.StatusOK, "partial")
		InternalErrorHandler(slog.New(slog.NewJSONHandler(&logs, nil)))(c, errors.New("encode failed"))
	})
	r.GET("/fail", func(c *gin.Context) {
		InternalErrorHandler(slog.New(slog.NewJSONHandler(&logs, nil)))(c, errors.New("db is down"))
	})
	return r, &logs
}

func serve(r http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// logLines decodes every JSON log line.
func logLines(t *testing.T, logs *bytes.Buffer) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var line map[string]any
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("log line is not JSON: %q", raw)
		}
		lines = append(lines, line)
	}
	return lines
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) errorBody {
	t.Helper()
	var body errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not the error format: %q", rec.Body.String())
	}
	return body.Error
}

func TestRequestID(t *testing.T) {
	tests := []struct {
		name     string
		incoming string
		reused   bool
	}{
		{name: "valid id is reused", incoming: "abc-123_DEF.4", reused: true},
		{name: "missing id is generated", incoming: ""},
		{name: "id with forbidden characters is replaced", incoming: "abc\" injected=1"},
		{name: "too long id is replaced", incoming: strings.Repeat("a", maxRequestIDLength+1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := newTestRouter(t)
			req := httptest.NewRequest(http.MethodGet, "/items/1", nil)
			if tt.incoming != "" {
				req.Header.Set(RequestIDHeader, tt.incoming)
			}
			got := serve(r, req).Header().Get(RequestIDHeader)
			if tt.reused {
				if got != tt.incoming {
					t.Fatalf("request id = %q, want %q", got, tt.incoming)
				}
				return
			}
			if got == tt.incoming || !validRequestID(got) {
				t.Fatalf("request id = %q, want a new valid id", got)
			}
		})
	}
}

func TestLoggerWritesOneLinePerRequest(t *testing.T) {
	r, logs := newTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/items/42?email=someone@example.com", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	req.Header.Set(RequestIDHeader, "req-1")
	serve(r, req)

	lines := logLines(t, logs)
	if len(lines) != 1 {
		t.Fatalf("got %d log lines, want 1", len(lines))
	}
	line := lines[0]
	want := map[string]any{"msg": "request", "method": "GET", "route": "/items/:id", "status": float64(200), "request_id": "req-1"}
	for key, value := range want {
		if line[key] != value {
			t.Errorf("log %s = %v, want %v", key, line[key], value)
		}
	}
	if _, ok := line["duration_ms"].(float64); !ok {
		t.Errorf("log duration_ms = %v, want a number", line["duration_ms"])
	}
	for _, leak := range []string{"secret-token", "someone@example.com", "/items/42"} {
		if strings.Contains(logs.String(), leak) {
			t.Errorf("log leaks %q: %s", leak, logs)
		}
	}
}

func TestRecoveryAnswersGenericInternalError(t *testing.T) {
	r, logs := newTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	req.Header.Set(RequestIDHeader, "req-panic")
	rec := serve(r, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	body := decodeError(t, rec)
	if body.Code != "internal_error" || body.RequestID != "req-panic" {
		t.Fatalf("error = %+v, want internal_error with the request id", body)
	}
	if strings.Contains(rec.Body.String(), "boom") {
		t.Fatalf("response leaks the panic value: %s", rec.Body)
	}

	lines := logLines(t, logs)
	if len(lines) != 2 {
		t.Fatalf("got %d log lines, want the panic and the request", len(lines))
	}
	if lines[0]["panic"] != "boom" || !strings.Contains(lines[0]["stack"].(string), "goroutine") {
		t.Errorf("panic log = %v, want the value and the stack", lines[0])
	}
	if lines[1]["status"] != float64(500) || lines[1]["level"] != "ERROR" {
		t.Errorf("request log = %v, want status 500 at ERROR", lines[1])
	}
}

func TestRecoveryAbortHandlerIsNotAnError(t *testing.T) {
	r, logs := newTestRouter(t)
	serve(r, httptest.NewRequest(http.MethodGet, "/abort", nil))

	for _, line := range logLines(t, logs) {
		if line["msg"] == "panic recovered" {
			t.Fatalf("ErrAbortHandler logged as a panic: %v", line)
		}
	}
}

func TestInternalErrorHandlerKeepsAStartedResponse(t *testing.T) {
	r, logs := newTestRouter(t)
	rec := serve(r, httptest.NewRequest(http.MethodGet, "/fail-late", nil))

	if rec.Body.String() != "partial" {
		t.Fatalf("body = %q, want the started response untouched", rec.Body)
	}
	if !strings.Contains(logs.String(), "encode failed") {
		t.Fatalf("the cause is not logged: %s", logs)
	}
}

func TestInternalErrorHandlerHidesTheCause(t *testing.T) {
	r, logs := newTestRouter(t)
	rec := serve(r, httptest.NewRequest(http.MethodGet, "/fail", nil))

	if rec.Code != http.StatusInternalServerError || decodeError(t, rec).Code != "internal_error" {
		t.Fatalf("got %d %s, want 500 internal_error", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "db is down") {
		t.Fatalf("response leaks the cause: %s", rec.Body)
	}
	if !strings.Contains(logs.String(), "db is down") {
		t.Fatalf("the cause is not logged: %s", logs)
	}
}

func TestUnknownRouteAnswersNotFound(t *testing.T) {
	r, _ := newTestRouter(t)
	rec := serve(r, httptest.NewRequest(http.MethodGet, "/nope", nil))

	if rec.Code != http.StatusNotFound || decodeError(t, rec).Code != "not_found" {
		t.Fatalf("got %d %s, want 404 not_found", rec.Code, rec.Body)
	}
}

func TestInvalidParamHandler(t *testing.T) {
	r := gin.New()
	r.GET("/", func(c *gin.Context) {
		InvalidParamHandler(c, errors.New("invalid value someone@example.com for id"), http.StatusBadRequest)
	})
	rec := serve(r, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusBadRequest || decodeError(t, rec).Code != "validation_failed" {
		t.Fatalf("got %d %s, want 400 validation_failed", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "someone@example.com") {
		t.Fatalf("response leaks the binding error: %s", rec.Body)
	}
}

func TestBadRequestHandler(t *testing.T) {
	r := gin.New()
	r.GET("/", func(c *gin.Context) { BadRequestHandler(c, errors.New("json: cannot unmarshal")) })
	rec := serve(r, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusBadRequest || decodeError(t, rec).Code != "validation_failed" {
		t.Fatalf("got %d %s, want 400 validation_failed", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "unmarshal") {
		t.Fatalf("response leaks the decoding error: %s", rec.Body)
	}
}
