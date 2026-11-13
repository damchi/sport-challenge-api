package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestNewIsCloudLoggingCompatible(t *testing.T) {
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
			New(&out, slog.LevelDebug, "api").Log(t.Context(), tt.level, "hello")

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

func TestNewFiltersBelowLevel(t *testing.T) {
	var out bytes.Buffer
	New(&out, slog.LevelWarn, "api").Info("hidden")
	if out.Len() != 0 {
		t.Fatalf("info written at warn level: %q", out.String())
	}
}

func TestNewReportsErrorsToErrorReporting(t *testing.T) {
	tests := []struct {
		level    slog.Level
		reported bool
	}{
		{slog.LevelDebug, false},
		{slog.LevelInfo, false},
		{slog.LevelWarn, false},
		{slog.LevelError, true},
	}
	for _, tt := range tests {
		t.Run(tt.level.String(), func(t *testing.T) {
			var out bytes.Buffer
			New(&out, slog.LevelDebug, "worker").With("request_id", "r1").Log(t.Context(), tt.level, "boom", "challenge_id", "c1")

			var line map[string]any
			if err := json.Unmarshal(out.Bytes(), &line); err != nil {
				t.Fatalf("not JSON: %q", out.String())
			}
			marker, hasMarker := line["@type"]
			context, _ := line["serviceContext"].(map[string]any)
			if hasMarker != tt.reported || (context["service"] == "worker") != tt.reported {
				t.Fatalf("line = %v, want reported %v", line, tt.reported)
			}
			if tt.reported && marker != errorEventType {
				t.Fatalf("@type = %v, want %s", marker, errorEventType)
			}
			// The other attributes stay where they were.
			if line["request_id"] != "r1" || line["challenge_id"] != "c1" || line["message"] != "boom" {
				t.Fatalf("line = %v lost an attribute", line)
			}
		})
	}
}

func TestNewKeepsTheErrorReportingFieldsAtTheRootInsideAGroup(t *testing.T) {
	var out bytes.Buffer
	New(&out, slog.LevelDebug, "api").WithGroup("g").Error("boom", "k", "v")

	var line map[string]any
	if err := json.Unmarshal(out.Bytes(), &line); err != nil {
		t.Fatalf("not JSON: %q", out.String())
	}
	context, _ := line["serviceContext"].(map[string]any)
	if line["@type"] != errorEventType || context["service"] != "api" {
		t.Fatalf("line = %v, want @type and serviceContext at the root", line)
	}
	if group, _ := line["g"].(map[string]any); group["k"] != "v" {
		t.Fatalf("line = %v, want the attribute k inside the group g", line)
	}
}
