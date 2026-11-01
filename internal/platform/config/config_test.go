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
			vars: map[string]string{"ENV": "staging", "PORT": "8080", "LOG_LEVEL": "warn", "DATABASE_URL": "postgres://db.example/x", "AUTH0_DOMAIN": "tenant.ca.auth0.com", "AUTH0_AUDIENCE": "https://api.test"},
			want: Config{Env: EnvStaging, Port: 8080, LogLevel: slog.LevelWarn, DatabaseURL: "postgres://db.example/x", Auth0Domain: "tenant.ca.auth0.com", Auth0Audience: "https://api.test"},
		},
		{
			name: "log level is case insensitive",
			vars: map[string]string{"ENV": "local", "PORT": "1", "LOG_LEVEL": "DEBUG", "DATABASE_URL": "postgres://localhost/x", "AUTH0_DOMAIN": "tenant.ca.auth0.com", "AUTH0_AUDIENCE": "https://api.test"},
			want: Config{Env: EnvLocal, Port: 1, LogLevel: slog.LevelDebug, DatabaseURL: "postgres://localhost/x", Auth0Domain: "tenant.ca.auth0.com", Auth0Audience: "https://api.test"},
		},
		{
			name: "surrounding spaces are ignored",
			vars: map[string]string{"ENV": " local ", "PORT": "8080 ", "LOG_LEVEL": "info   ", "DATABASE_URL": " postgres://localhost/x ", "AUTH0_DOMAIN": " tenant.ca.auth0.com ", "AUTH0_AUDIENCE": " https://api.test "},
			want: Config{Env: EnvLocal, Port: 8080, LogLevel: slog.LevelInfo, DatabaseURL: "postgres://localhost/x", Auth0Domain: "tenant.ca.auth0.com", Auth0Audience: "https://api.test"},
		},
		{
			name:    "blank values count as missing",
			vars:    map[string]string{"ENV": " ", "PORT": "\t", "LOG_LEVEL": " ", "DATABASE_URL": " ", "AUTH0_DOMAIN": " ", "AUTH0_AUDIENCE": " "},
			wantErr: []string{"ENV is required", "PORT is required", "LOG_LEVEL is required", "DATABASE_URL is required", "AUTH0_DOMAIN is required", "AUTH0_AUDIENCE is required"},
		},
		{
			name:    "all missing are reported together",
			vars:    map[string]string{},
			wantErr: []string{"ENV is required", "PORT is required", "LOG_LEVEL is required", "DATABASE_URL is required", "AUTH0_DOMAIN is required", "AUTH0_AUDIENCE is required"},
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
			name: "auth0 domain with a scheme",
			vars: map[string]string{
				"ENV": "local", "PORT": "8080", "LOG_LEVEL": "info", "DATABASE_URL": "postgres://localhost/x",
				"AUTH0_DOMAIN": "https://tenant.ca.auth0.com/", "AUTH0_AUDIENCE": "https://api.test",
			},
			wantErr: []string{"AUTH0_DOMAIN must be a host name"},
		},
		{
			name: "auth0 domain with userinfo",
			vars: map[string]string{
				"ENV": "local", "PORT": "8080", "LOG_LEVEL": "info", "DATABASE_URL": "postgres://localhost/x",
				"AUTH0_DOMAIN": "x@evil.com", "AUTH0_AUDIENCE": "https://api.test",
			},
			wantErr: []string{"AUTH0_DOMAIN must be a host name"},
		},
		{
			name: "auth0 domain with a query",
			vars: map[string]string{
				"ENV": "local", "PORT": "8080", "LOG_LEVEL": "info", "DATABASE_URL": "postgres://localhost/x",
				"AUTH0_DOMAIN": "tenant.ca.auth0.com?x=1", "AUTH0_AUDIENCE": "https://api.test",
			},
			wantErr: []string{"AUTH0_DOMAIN must be a host name"},
		},
		{
			name: "auth0 domain without a dot",
			vars: map[string]string{
				"ENV": "local", "PORT": "8080", "LOG_LEVEL": "info", "DATABASE_URL": "postgres://localhost/x",
				"AUTH0_DOMAIN": "localhost", "AUTH0_AUDIENCE": "https://api.test",
			},
			wantErr: []string{"AUTH0_DOMAIN must be a host name"},
		},
		{
			name: "auth0 domain with a space",
			vars: map[string]string{
				"ENV": "local", "PORT": "8080", "LOG_LEVEL": "info", "DATABASE_URL": "postgres://localhost/x",
				"AUTH0_DOMAIN": "tenant .ca.auth0.com", "AUTH0_AUDIENCE": "https://api.test",
			},
			wantErr: []string{"AUTH0_DOMAIN must be a host name"},
		},
		{
			name: "auth0 domain is lowercased",
			vars: map[string]string{
				"ENV": "local", "PORT": "8080", "LOG_LEVEL": "info", "DATABASE_URL": "postgres://localhost/x",
				"AUTH0_DOMAIN": "Tenant.CA.Auth0.com", "AUTH0_AUDIENCE": "https://api.test",
			},
			want: Config{
				Env: EnvLocal, Port: 8080, LogLevel: slog.LevelInfo, DatabaseURL: "postgres://localhost/x",
				Auth0Domain: "tenant.ca.auth0.com", Auth0Audience: "https://api.test",
			},
		},
		{
			name:    "port out of range",
			vars:    map[string]string{"ENV": "production", "PORT": "70000", "LOG_LEVEL": "info", "DATABASE_URL": "postgres://localhost/x", "AUTH0_DOMAIN": "tenant.ca.auth0.com", "AUTH0_AUDIENCE": "https://api.test"},
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
	_, err := Load(envFrom(map[string]string{"ENV": secret, "PORT": secret, "LOG_LEVEL": secret, "DATABASE_URL": "postgres://u:" + secret + "@db/x"}))
	if err == nil {
		t.Fatal("Load() error = nil, want an error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaks the value: %q", err)
	}
}
