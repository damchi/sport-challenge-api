package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/damchi/sport-challenge-api/internal/platform/config"
)

func TestParseTask(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    string
		wantErr error
	}{
		{name: "close-weeks", args: []string{"close-weeks"}, want: "close-weeks"},
		{name: "finalize-challenges", args: []string{"finalize-challenges"}, want: "finalize-challenges"},
		{name: "retry-auth0-deletions", args: []string{"retry-auth0-deletions"}, want: "retry-auth0-deletions"},
		{name: "no task", wantErr: errUsage},
		{name: "unknown task", args: []string{"sync"}, wantErr: errUsage},
		{name: "two tasks", args: []string{"close-weeks", "close-weeks"}, wantErr: errUsage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTask(tt.args)
			if !errors.Is(err, tt.wantErr) || got != tt.want {
				t.Fatalf("parseTask(%v) = %q, %v; want %q, %v", tt.args, got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestWorkerEnv(t *testing.T) {
	tests := []struct {
		name string
		key  string
		set  string
		want string
	}{
		{name: "PORT empty falls back to the API default", key: "PORT", set: "", want: "8080"},
		{name: "PORT of spaces falls back too", key: "PORT", set: "  ", want: "8080"},
		{name: "PORT set is kept", key: "PORT", set: "9090", want: "9090"},
		{name: "other keys are passed through", key: "LOG_LEVEL", set: "warn", want: "warn"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.key, tt.set)
			if got := workerEnv(tt.key); got != tt.want {
				t.Fatalf("workerEnv(%s) with %q = %q; want %q", tt.key, tt.set, got, tt.want)
			}
		})
	}
}

// A job has every variable of the configuration but PORT, which Cloud Run does
// not let it set: the configuration must load, with the API default port.
func TestWorkerEnvSatisfiesTheConfiguration(t *testing.T) {
	for key, value := range map[string]string{
		"ENV":                      "production",
		"LOG_LEVEL":                "info",
		"DATABASE_URL":             "postgres://db.example/x",
		"AUTH0_DOMAIN":             "tenant.ca.auth0.com",
		"AUTH0_AUDIENCE":           "https://api.test",
		"AUTH0_MGMT_CLIENT_ID":     "mgmt-id",
		"AUTH0_MGMT_CLIENT_SECRET": "mgmt-secret", //nolint:gosec // test value, not a credential
		"PORT":                     "",
	} {
		t.Setenv(key, value)
	}
	cfg, err := config.Load(workerEnv)
	if err != nil {
		t.Fatalf("config.Load(workerEnv) = %v, want a valid configuration", err)
	}
	if cfg.Port != 8080 {
		t.Fatalf("Port = %d, want 8080", cfg.Port)
	}
}

// The end of a task is logged as a failure when the task returns an error,
// with its partial counters, and as done otherwise. The error text is not
// logged again: it may carry identifiers, and main already prints it.
func TestLogTaskResult(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantMsg   string
		wantLevel string
	}{
		{name: "success", wantMsg: "task done", wantLevel: "INFO"},
		{name: "failure", err: errors.New("challenge c-1: boom"), wantMsg: "task failed", wantLevel: "ERROR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&buf, nil))
			logTaskResult(context.Background(), logger, taskCloseWeeks, tt.err, slog.Int("challenges", 3))

			lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
			if len(lines) != 1 {
				t.Fatalf("logged %d lines, want 1: %q", len(lines), buf.String())
			}
			var entry struct {
				Msg        string `json:"msg"`
				Level      string `json:"level"`
				Task       string `json:"task"`
				Challenges int    `json:"challenges"`
			}
			if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
				t.Fatalf("decode %q: %v", lines[0], err)
			}
			if entry.Msg != tt.wantMsg || entry.Level != tt.wantLevel {
				t.Fatalf("logged %q at %s, want %q at %s", entry.Msg, entry.Level, tt.wantMsg, tt.wantLevel)
			}
			if entry.Task != taskCloseWeeks || entry.Challenges != 3 {
				t.Fatalf("attributes = task %q, challenges %d; want %q, 3", entry.Task, entry.Challenges, taskCloseWeeks)
			}
			if strings.Contains(lines[0], "c-1") {
				t.Fatalf("logged the error text: %s", lines[0])
			}
		})
	}
}
