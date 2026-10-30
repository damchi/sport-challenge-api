package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestNewLoggerIsCloudLoggingCompatible(t *testing.T) {
	tests := []struct {
		level    slog.Level
		severity string
	}{
		{slog.LevelDebug, "DEBUG"},
		{slog.LevelInfo, "INFO"},
		{slog.LevelWarn, "WARNING"},
		{slog.LevelError, "ERROR"},
	}
	for _, tt := range tests {
		t.Run(tt.severity, func(t *testing.T) {
			var out bytes.Buffer
			newLogger(&out, slog.LevelDebug).Log(t.Context(), tt.level, "hello")

			var line map[string]any
			if err := json.Unmarshal(out.Bytes(), &line); err != nil {
				t.Fatalf("not JSON: %q", out.String())
			}
			if line["severity"] != tt.severity || line["message"] != "hello" || line["time"] == nil {
				t.Fatalf("line = %v, want severity %s, message and time", line, tt.severity)
			}
			if ts, _ := line["time"].(string); !strings.HasSuffix(ts, "Z") {
				t.Fatalf("time = %v, want UTC", line["time"])
			}
			if _, ok := line["level"]; ok {
				t.Fatalf("line still has level: %v", line)
			}
		})
	}
}

func TestNewLoggerFiltersBelowLevel(t *testing.T) {
	var out bytes.Buffer
	newLogger(&out, slog.LevelWarn).Info("hidden")
	if out.Len() != 0 {
		t.Fatalf("info written at warn level: %q", out.String())
	}
}

func TestLogRoutesAtDebugLevel(t *testing.T) {
	routes := gin.RoutesInfo{{Method: "GET", Path: "/healthz"}}

	var out bytes.Buffer
	logRoutes(newLogger(&out, slog.LevelDebug), routes)
	var line map[string]any
	if err := json.Unmarshal(out.Bytes(), &line); err != nil {
		t.Fatalf("not JSON: %q", out.String())
	}
	got, _ := line["routes"].([]any)
	if line["message"] != "routes registered" || len(got) != 1 || got[0] != "GET /healthz" {
		t.Fatalf("line = %v, want the GET /healthz route", line)
	}

	out.Reset()
	logRoutes(newLogger(&out, slog.LevelInfo), routes)
	if out.Len() != 0 {
		t.Fatalf("routes logged at info level: %q", out.String())
	}
}

func TestPingDatabase(t *testing.T) {
	tests := []struct {
		name         string
		pingErr      error
		wantSeverity string
		wantMessage  string
	}{
		{name: "available", wantSeverity: "INFO", wantMessage: "database connected"},
		{name: "unavailable", pingErr: errors.New("connection refused"), wantSeverity: "WARNING", wantMessage: "database unavailable at startup"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			pingDatabase(t.Context(), newLogger(&out, slog.LevelDebug), func(ctx context.Context) error {
				if _, ok := ctx.Deadline(); !ok {
					t.Error("ping context has no deadline")
				}
				return tt.pingErr
			})

			var line map[string]any
			if err := json.Unmarshal(out.Bytes(), &line); err != nil {
				t.Fatalf("not JSON: %q", out.String())
			}
			if line["severity"] != tt.wantSeverity || line["message"] != tt.wantMessage {
				t.Fatalf("line = %v, want %s %q", line, tt.wantSeverity, tt.wantMessage)
			}
		})
	}
}
