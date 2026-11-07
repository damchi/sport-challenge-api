package config

import (
	"log/slog"
	"reflect"
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
				if !reflect.DeepEqual(got, tt.want) {
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

func TestLoadCORSAllowedOrigins(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    []string
		wantErr bool
	}{
		{name: "absent: no browser origin", raw: ""},
		{name: "one origin", raw: "https://app.example.com", want: []string{"https://app.example.com"}},
		{name: "list with spaces, port and empty entries", raw: " https://app.example.com , ,http://localhost:5173,", want: []string{"https://app.example.com", "http://localhost:5173"}},
		{name: "lowercased", raw: "HTTPS://App.Example.com", want: []string{"https://app.example.com"}},
		{name: "wildcard", raw: "*", wantErr: true},
		{name: "wildcard subdomain", raw: "https://*.example.com", wantErr: true},
		{name: "trailing slash", raw: "https://app.example.com/", wantErr: true},
		{name: "path", raw: "https://app.example.com/app", wantErr: true},
		{name: "query", raw: "https://app.example.com?x=1", wantErr: true},
		{name: "empty query", raw: "https://app.example.com?", wantErr: true},
		{name: "fragment", raw: "https://app.example.com#x", wantErr: true},
		{name: "credentials", raw: "https://user@app.example.com", wantErr: true},
		{name: "no scheme", raw: "app.example.com", wantErr: true},
		{name: "other scheme", raw: "ftp://app.example.com", wantErr: true},
		{name: "null origin", raw: "null", wantErr: true},
		{name: "default https port", raw: "https://app.example.com:443", wantErr: true},
		{name: "default http port", raw: "http://localhost:80", wantErr: true},
		{name: "empty port", raw: "https://app.example.com:", wantErr: true},
		{name: "port without host", raw: "https://:443", wantErr: true},
		{name: "trailing dot", raw: "https://app.example.com.", wantErr: true},
		{name: "unicode host", raw: "https://café.example.com", wantErr: true},
		{name: "punycode host", raw: "https://xn--caf-dma.example.com", want: []string{"https://xn--caf-dma.example.com"}},
		{name: "explicit other port", raw: "https://app.example.com:8443", want: []string{"https://app.example.com:8443"}},
		{name: "one bad entry fails the list", raw: "https://app.example.com,https://other.example.com/", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Load(envFrom(map[string]string{
				"ENV": "local", "PORT": "8080", "LOG_LEVEL": "info", "DATABASE_URL": "postgres://localhost/x",
				"AUTH0_DOMAIN": "tenant.ca.auth0.com", "AUTH0_AUDIENCE": "https://api.test",
				"CORS_ALLOWED_ORIGINS": tt.raw,
			}))
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "CORS_ALLOWED_ORIGINS must be") {
					t.Fatalf("Load() error = %v, want a CORS_ALLOWED_ORIGINS error", err)
				}
				if strings.Contains(err.Error(), "example.com") {
					t.Fatalf("error echoes the value: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if !reflect.DeepEqual(got.CORSAllowedOrigins, tt.want) {
				t.Fatalf("CORSAllowedOrigins = %q, want %q", got.CORSAllowedOrigins, tt.want)
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
