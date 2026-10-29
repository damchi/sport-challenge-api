package config

import (
	"log/slog"
	"strings"
	"testing"
)

func envFrom(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		vars    map[string]string
		want    Config
		wantErr []string
	}{
		{
			name: "valid",
			vars: map[string]string{"ENV": "staging", "PORT": "8080", "LOG_LEVEL": "warn"},
			want: Config{Env: EnvStaging, Port: 8080, LogLevel: slog.LevelWarn},
		},
		{
			name: "log level is case insensitive",
			vars: map[string]string{"ENV": "local", "PORT": "1", "LOG_LEVEL": "DEBUG"},
			want: Config{Env: EnvLocal, Port: 1, LogLevel: slog.LevelDebug},
		},
		{
			name: "surrounding spaces are ignored",
			vars: map[string]string{"ENV": " local ", "PORT": "8080 ", "LOG_LEVEL": "info   "},
			want: Config{Env: EnvLocal, Port: 8080, LogLevel: slog.LevelInfo},
		},
		{
			name:    "blank values count as missing",
			vars:    map[string]string{"ENV": " ", "PORT": "\t", "LOG_LEVEL": " "},
			wantErr: []string{"ENV is required", "PORT is required", "LOG_LEVEL is required"},
		},
		{
			name:    "all missing are reported together",
			vars:    map[string]string{},
			wantErr: []string{"ENV is required", "PORT is required", "LOG_LEVEL is required"},
		},
		{
			name: "all invalid are reported together",
			vars: map[string]string{"ENV": "prod", "PORT": "http", "LOG_LEVEL": "verbose"},
			wantErr: []string{
				"ENV must be one of",
				"PORT must be an integer",
				"LOG_LEVEL must be one of",
			},
		},
		{
			name:    "port out of range",
			vars:    map[string]string{"ENV": "production", "PORT": "70000", "LOG_LEVEL": "info"},
			wantErr: []string{"PORT must be an integer between 1 and 65535"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Load(envFrom(tt.vars))
			if len(tt.wantErr) == 0 {
				if err != nil {
					t.Fatalf("Load() error = %v", err)
				}
				if got != tt.want {
					t.Fatalf("Load() = %+v, want %+v", got, tt.want)
				}
				return
			}
			if err == nil {
				t.Fatal("Load() error = nil, want an error")
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

func TestLoadNeverEchoesValues(t *testing.T) {
	const secret = "s3cr3t-value"
	_, err := Load(envFrom(map[string]string{"ENV": secret, "PORT": secret, "LOG_LEVEL": secret}))
	if err == nil {
		t.Fatal("Load() error = nil, want an error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaks the value: %q", err)
	}
}
