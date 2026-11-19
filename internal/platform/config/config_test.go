package config

import (
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"
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
			vars: map[string]string{"ENV": "production", "PORT": "8080", "LOG_LEVEL": "warn", "DATABASE_URL": "postgres://db.example/x", "AUTH0_DOMAIN": "tenant.ca.auth0.com", "AUTH0_AUDIENCE": "https://api.test", "AUTH0_MGMT_CLIENT_ID": "mgmt-id", "AUTH0_MGMT_CLIENT_SECRET": "mgmt-secret"},
			want: Config{Env: EnvProduction, Port: 8080, LogLevel: slog.LevelWarn, DatabaseURL: "postgres://db.example/x", Auth0Domain: "tenant.ca.auth0.com", Auth0MgmtDomain: "tenant.ca.auth0.com", Auth0Audience: "https://api.test", Auth0MgmtClientID: "mgmt-id", Auth0MgmtClientSecret: "mgmt-secret", AtRiskThreshold: 24 * time.Hour, RateLimitIP: defaultRateLimitIP, RateLimitUser: defaultRateLimitUser},
		},
		{
			name: "log level is case insensitive",
			vars: map[string]string{"ENV": "local", "PORT": "1", "LOG_LEVEL": "DEBUG", "DATABASE_URL": "postgres://localhost/x", "AUTH0_DOMAIN": "tenant.ca.auth0.com", "AUTH0_AUDIENCE": "https://api.test", "AUTH0_MGMT_CLIENT_ID": "mgmt-id", "AUTH0_MGMT_CLIENT_SECRET": "mgmt-secret"},
			want: Config{Env: EnvLocal, Port: 1, LogLevel: slog.LevelDebug, DatabaseURL: "postgres://localhost/x", Auth0Domain: "tenant.ca.auth0.com", Auth0MgmtDomain: "tenant.ca.auth0.com", Auth0Audience: "https://api.test", Auth0MgmtClientID: "mgmt-id", Auth0MgmtClientSecret: "mgmt-secret", AtRiskThreshold: 24 * time.Hour, RateLimitIP: defaultRateLimitIP, RateLimitUser: defaultRateLimitUser},
		},
		{
			name: "surrounding spaces are ignored",
			vars: map[string]string{"ENV": " local ", "PORT": "8080 ", "LOG_LEVEL": "info   ", "DATABASE_URL": " postgres://localhost/x ", "AUTH0_DOMAIN": " tenant.ca.auth0.com ", "AUTH0_AUDIENCE": " https://api.test ", "AUTH0_MGMT_CLIENT_ID": " mgmt-id ", "AUTH0_MGMT_CLIENT_SECRET": " mgmt-secret "}, //nolint:gosec // test value, not a credential
			want: Config{Env: EnvLocal, Port: 8080, LogLevel: slog.LevelInfo, DatabaseURL: "postgres://localhost/x", Auth0Domain: "tenant.ca.auth0.com", Auth0MgmtDomain: "tenant.ca.auth0.com", Auth0Audience: "https://api.test", Auth0MgmtClientID: "mgmt-id", Auth0MgmtClientSecret: "mgmt-secret", AtRiskThreshold: 24 * time.Hour, RateLimitIP: defaultRateLimitIP, RateLimitUser: defaultRateLimitUser},
		},
		{
			name:    "blank values count as missing",
			vars:    map[string]string{"ENV": " ", "PORT": "\t", "LOG_LEVEL": " ", "DATABASE_URL": " ", "AUTH0_DOMAIN": " ", "AUTH0_AUDIENCE": " ", "AUTH0_MGMT_CLIENT_ID": " ", "AUTH0_MGMT_CLIENT_SECRET": " "},
			wantErr: []string{"ENV is required", "PORT is required", "LOG_LEVEL is required", "DATABASE_URL is required", "AUTH0_DOMAIN is required", "AUTH0_AUDIENCE is required", "AUTH0_MGMT_CLIENT_ID is required", "AUTH0_MGMT_CLIENT_SECRET is required"},
		},
		{
			name:    "all missing are reported together",
			vars:    map[string]string{},
			wantErr: []string{"ENV is required", "PORT is required", "LOG_LEVEL is required", "DATABASE_URL is required", "AUTH0_DOMAIN is required", "AUTH0_AUDIENCE is required", "AUTH0_MGMT_CLIENT_ID is required", "AUTH0_MGMT_CLIENT_SECRET is required"},
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
				"AUTH0_MGMT_CLIENT_ID": "mgmt-id", "AUTH0_MGMT_CLIENT_SECRET": "mgmt-secret",
			},
			wantErr: []string{"AUTH0_DOMAIN must be a host name"},
		},
		{
			name: "auth0 domain with userinfo",
			vars: map[string]string{
				"ENV": "local", "PORT": "8080", "LOG_LEVEL": "info", "DATABASE_URL": "postgres://localhost/x",
				"AUTH0_DOMAIN": "x@evil.com", "AUTH0_AUDIENCE": "https://api.test",
				"AUTH0_MGMT_CLIENT_ID": "mgmt-id", "AUTH0_MGMT_CLIENT_SECRET": "mgmt-secret",
			},
			wantErr: []string{"AUTH0_DOMAIN must be a host name"},
		},
		{
			name: "auth0 domain with a query",
			vars: map[string]string{
				"ENV": "local", "PORT": "8080", "LOG_LEVEL": "info", "DATABASE_URL": "postgres://localhost/x",
				"AUTH0_DOMAIN": "tenant.ca.auth0.com?x=1", "AUTH0_AUDIENCE": "https://api.test",
				"AUTH0_MGMT_CLIENT_ID": "mgmt-id", "AUTH0_MGMT_CLIENT_SECRET": "mgmt-secret",
			},
			wantErr: []string{"AUTH0_DOMAIN must be a host name"},
		},
		{
			name: "auth0 domain without a dot",
			vars: map[string]string{
				"ENV": "local", "PORT": "8080", "LOG_LEVEL": "info", "DATABASE_URL": "postgres://localhost/x",
				"AUTH0_DOMAIN": "localhost", "AUTH0_AUDIENCE": "https://api.test",
				"AUTH0_MGMT_CLIENT_ID": "mgmt-id", "AUTH0_MGMT_CLIENT_SECRET": "mgmt-secret",
			},
			wantErr: []string{"AUTH0_DOMAIN must be a host name"},
		},
		{
			name: "auth0 domain with a space",
			vars: map[string]string{
				"ENV": "local", "PORT": "8080", "LOG_LEVEL": "info", "DATABASE_URL": "postgres://localhost/x",
				"AUTH0_DOMAIN": "tenant .ca.auth0.com", "AUTH0_AUDIENCE": "https://api.test",
				"AUTH0_MGMT_CLIENT_ID": "mgmt-id", "AUTH0_MGMT_CLIENT_SECRET": "mgmt-secret",
			},
			wantErr: []string{"AUTH0_DOMAIN must be a host name"},
		},
		{
			name: "auth0 domain is lowercased",
			vars: map[string]string{
				"ENV": "local", "PORT": "8080", "LOG_LEVEL": "info", "DATABASE_URL": "postgres://localhost/x",
				"AUTH0_DOMAIN": "Tenant.CA.Auth0.com", "AUTH0_AUDIENCE": "https://api.test",
				"AUTH0_MGMT_CLIENT_ID": "mgmt-id", "AUTH0_MGMT_CLIENT_SECRET": "mgmt-secret",
			},
			want: Config{
				Env: EnvLocal, Port: 8080, LogLevel: slog.LevelInfo, DatabaseURL: "postgres://localhost/x",
				Auth0Domain: "tenant.ca.auth0.com", Auth0MgmtDomain: "tenant.ca.auth0.com", Auth0Audience: "https://api.test",
				Auth0MgmtClientID: "mgmt-id", Auth0MgmtClientSecret: "mgmt-secret", AtRiskThreshold: 24 * time.Hour,
				RateLimitIP: defaultRateLimitIP, RateLimitUser: defaultRateLimitUser,
			},
		},
		{
			name:    "port out of range",
			vars:    map[string]string{"ENV": "production", "PORT": "70000", "LOG_LEVEL": "info", "DATABASE_URL": "postgres://localhost/x", "AUTH0_DOMAIN": "tenant.ca.auth0.com", "AUTH0_AUDIENCE": "https://api.test", "AUTH0_MGMT_CLIENT_ID": "mgmt-id", "AUTH0_MGMT_CLIENT_SECRET": "mgmt-secret"},
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
				"AUTH0_MGMT_CLIENT_ID": "mgmt-id", "AUTH0_MGMT_CLIENT_SECRET": "mgmt-secret",
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

func TestLoadAtRiskThreshold(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    time.Duration
		wantErr bool
	}{
		{name: "absent: 24 hours", raw: "", want: 24 * time.Hour},
		{name: "blank: 24 hours", raw: "  ", want: 24 * time.Hour},
		{name: "hours", raw: "12h", want: 12 * time.Hour},
		{name: "spaces ignored", raw: " 90m ", want: 90 * time.Minute},
		{name: "seconds, for a test environment", raw: "30s", want: 30 * time.Second},
		{name: "zero", raw: "0s", wantErr: true},
		{name: "negative", raw: "-1h", wantErr: true},
		{name: "no unit", raw: "24", wantErr: true},
		{name: "not a duration", raw: "a day", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Load(envFrom(map[string]string{
				"ENV": "local", "PORT": "8080", "LOG_LEVEL": "info", "DATABASE_URL": "postgres://localhost/x",
				"AUTH0_DOMAIN": "tenant.ca.auth0.com", "AUTH0_AUDIENCE": "https://api.test",
				"AUTH0_MGMT_CLIENT_ID": "mgmt-id", "AUTH0_MGMT_CLIENT_SECRET": "mgmt-secret",
				"AT_RISK_THRESHOLD": tt.raw,
			}))
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "AT_RISK_THRESHOLD must be") {
					t.Fatalf("Load() error = %v, want an AT_RISK_THRESHOLD error", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if got.AtRiskThreshold != tt.want {
				t.Fatalf("AtRiskThreshold = %v, want %v", got.AtRiskThreshold, tt.want)
			}
		})
	}
}

// TestLoadAuth0MgmtDomain: the Management API keeps the tenant domain when
// AUTH0_DOMAIN is a custom domain; without AUTH0_MGMT_DOMAIN, both are the
// same.
func TestLoadAuth0MgmtDomain(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		want       string
		wantIssuer string
		wantErr    bool
	}{
		{name: "absent: AUTH0_DOMAIN", raw: "", want: "login.example.com", wantIssuer: "login.example.com"},
		{name: "blank: AUTH0_DOMAIN", raw: "  ", want: "login.example.com", wantIssuer: "login.example.com"},
		{name: "tenant domain apart", raw: "tenant.ca.auth0.com", want: "tenant.ca.auth0.com", wantIssuer: "login.example.com"},
		{name: "lowercased and trimmed", raw: " Tenant.CA.Auth0.com ", want: "tenant.ca.auth0.com", wantIssuer: "login.example.com"},
		{name: "with a scheme", raw: "https://tenant.ca.auth0.com", wantErr: true},
		{name: "with a path", raw: "tenant.ca.auth0.com/api/v2", wantErr: true},
		{name: "with a port", raw: "tenant.ca.auth0.com:443", wantErr: true},
		{name: "with credentials", raw: "x@evil.com", wantErr: true},
		{name: "single label", raw: "localhost", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Load(envFrom(map[string]string{
				"ENV": "local", "PORT": "8080", "LOG_LEVEL": "info", "DATABASE_URL": "postgres://localhost/x",
				"AUTH0_DOMAIN": "login.example.com", "AUTH0_AUDIENCE": "https://api.test",
				"AUTH0_MGMT_CLIENT_ID": "mgmt-id", "AUTH0_MGMT_CLIENT_SECRET": "mgmt-secret",
				"AUTH0_MGMT_DOMAIN": tt.raw,
			}))
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "AUTH0_MGMT_DOMAIN must be a host name") {
					t.Fatalf("Load() error = %v, want an AUTH0_MGMT_DOMAIN error", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if got.Auth0MgmtDomain != tt.want || got.Auth0Domain != tt.wantIssuer {
				t.Fatalf("Auth0MgmtDomain = %q, Auth0Domain = %q, want %q and %q", got.Auth0MgmtDomain, got.Auth0Domain, tt.want, tt.wantIssuer)
			}
		})
	}
}

func TestLoadTrustedProxies(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    []string
		wantErr bool
	}{
		{name: "unset: no proxy is trusted"},
		{name: "one address", raw: "169.254.1.1", want: []string{"169.254.1.1"}},
		{name: "ranges and addresses, spaces and empty entries ignored", raw: " 169.254.0.0/16 ,, 2001:db8::/32 ,10.0.0.1", want: []string{"169.254.0.0/16", "2001:db8::/32", "10.0.0.1"}},
		{name: "every IPv4 address", raw: "0.0.0.0/0", wantErr: true},
		{name: "every IPv6 address", raw: "::/0", wantErr: true},
		{name: "every IPv4 address written in IPv6", raw: "::ffff:0.0.0.0/96", wantErr: true},
		{name: "half of IPv4", raw: "0.0.0.0/1,128.0.0.0/1", wantErr: true},
		{name: "IPv4 range wider than /8", raw: "10.0.0.0/7", wantErr: true},
		{name: "IPv6 range wider than /16", raw: "2000::/3", wantErr: true},
		{name: "widest ranges allowed", raw: "10.0.0.0/8,::ffff:10.0.0.0/104,fd00::/16", want: []string{"10.0.0.0/8", "::ffff:10.0.0.0/104", "fd00::/16"}},
		{name: "wildcard", raw: "*", wantErr: true},
		{name: "host name", raw: "proxy.example.com", wantErr: true},
		{name: "invalid range", raw: "10.0.0.0/33", wantErr: true},
		{name: "one invalid entry among valid ones", raw: "10.0.0.1,nope", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Load(envFrom(map[string]string{
				"ENV": "local", "PORT": "8080", "LOG_LEVEL": "info", "DATABASE_URL": "postgres://localhost/x",
				"AUTH0_DOMAIN": "tenant.ca.auth0.com", "AUTH0_AUDIENCE": "https://api.test",
				"AUTH0_MGMT_CLIENT_ID": "mgmt-id", "AUTH0_MGMT_CLIENT_SECRET": "mgmt-secret",
				"TRUSTED_PROXIES": tt.raw,
			}))
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "TRUSTED_PROXIES must be") {
					t.Fatalf("Load() error = %v, want a TRUSTED_PROXIES error", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if !reflect.DeepEqual(got.TrustedProxies, tt.want) {
				t.Fatalf("TrustedProxies = %q, want %q", got.TrustedProxies, tt.want)
			}
		})
	}
}

func TestLoadRateLimits(t *testing.T) {
	tests := []struct {
		name     string
		vars     map[string]string
		wantIP   RateLimit
		wantUser RateLimit
		wantErr  []string
	}{
		{name: "unset: defaults", wantIP: defaultRateLimitIP, wantUser: defaultRateLimitUser},
		{
			name:   "bounds",
			vars:   map[string]string{"RATE_LIMIT_IP_RPS": "0.001", "RATE_LIMIT_IP_BURST": "1000000", "RATE_LIMIT_USER_RPS": "1000000", "RATE_LIMIT_USER_BURST": "1"},
			wantIP: RateLimit{RPS: 0.001, Burst: 1_000_000}, wantUser: RateLimit{RPS: 1_000_000, Burst: 1},
		},
		{
			name:   "all set",
			vars:   map[string]string{"RATE_LIMIT_IP_RPS": "50", "RATE_LIMIT_IP_BURST": "100", "RATE_LIMIT_USER_RPS": "0.5", "RATE_LIMIT_USER_BURST": "3"},
			wantIP: RateLimit{RPS: 50, Burst: 100}, wantUser: RateLimit{RPS: 0.5, Burst: 3},
		},
		{
			name:   "each value falls back on its own",
			vars:   map[string]string{"RATE_LIMIT_IP_BURST": "5", "RATE_LIMIT_USER_RPS": "1"},
			wantIP: RateLimit{RPS: defaultRateLimitIP.RPS, Burst: 5}, wantUser: RateLimit{RPS: 1, Burst: defaultRateLimitUser.Burst},
		},
		{name: "zero rate", vars: map[string]string{"RATE_LIMIT_IP_RPS": "0"}, wantErr: []string{"RATE_LIMIT_IP_RPS must be"}},
		{name: "negative rate", vars: map[string]string{"RATE_LIMIT_USER_RPS": "-1"}, wantErr: []string{"RATE_LIMIT_USER_RPS must be"}},
		{name: "rate is not a number", vars: map[string]string{"RATE_LIMIT_IP_RPS": "fast"}, wantErr: []string{"RATE_LIMIT_IP_RPS must be"}},
		{name: "NaN rate", vars: map[string]string{"RATE_LIMIT_IP_RPS": "NaN"}, wantErr: []string{"RATE_LIMIT_IP_RPS must be"}},
		{name: "infinite rate", vars: map[string]string{"RATE_LIMIT_IP_RPS": "Inf"}, wantErr: []string{"RATE_LIMIT_IP_RPS must be"}},
		{name: "rate too small to refill a bucket", vars: map[string]string{"RATE_LIMIT_IP_RPS": "1e-12"}, wantErr: []string{"RATE_LIMIT_IP_RPS must be"}},
		{name: "rate too large", vars: map[string]string{"RATE_LIMIT_IP_RPS": "1e7"}, wantErr: []string{"RATE_LIMIT_IP_RPS must be"}},
		{name: "burst too large", vars: map[string]string{"RATE_LIMIT_IP_BURST": "1000001"}, wantErr: []string{"RATE_LIMIT_IP_BURST must be"}},
		{name: "zero burst", vars: map[string]string{"RATE_LIMIT_IP_BURST": "0"}, wantErr: []string{"RATE_LIMIT_IP_BURST must be"}},
		{name: "decimal burst", vars: map[string]string{"RATE_LIMIT_USER_BURST": "2.5"}, wantErr: []string{"RATE_LIMIT_USER_BURST must be"}},
		{
			name:    "every problem is reported",
			vars:    map[string]string{"RATE_LIMIT_IP_RPS": "0", "RATE_LIMIT_IP_BURST": "0", "RATE_LIMIT_USER_RPS": "x", "RATE_LIMIT_USER_BURST": "-2"},
			wantErr: []string{"RATE_LIMIT_IP_RPS must be", "RATE_LIMIT_IP_BURST must be", "RATE_LIMIT_USER_RPS must be", "RATE_LIMIT_USER_BURST must be"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vars := map[string]string{
				"ENV": "local", "PORT": "8080", "LOG_LEVEL": "info", "DATABASE_URL": "postgres://localhost/x",
				"AUTH0_DOMAIN": "tenant.ca.auth0.com", "AUTH0_AUDIENCE": "https://api.test",
				"AUTH0_MGMT_CLIENT_ID": "mgmt-id", "AUTH0_MGMT_CLIENT_SECRET": "mgmt-secret",
			}
			for k, v := range tt.vars {
				vars[k] = v
			}
			got, err := Load(envFrom(vars))
			if len(tt.wantErr) > 0 {
				for _, want := range tt.wantErr {
					if err == nil || !strings.Contains(err.Error(), want) {
						t.Fatalf("Load() error = %v, want it to contain %q", err, want)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if got.RateLimitIP != tt.wantIP || got.RateLimitUser != tt.wantUser {
				t.Fatalf("limits = %+v and %+v, want %+v and %+v", got.RateLimitIP, got.RateLimitUser, tt.wantIP, tt.wantUser)
			}
		})
	}
}

func TestLoadClockOffset(t *testing.T) {
	base := func(env, offset string) map[string]string {
		vars := map[string]string{"ENV": env, "PORT": "8080", "LOG_LEVEL": "info", "DATABASE_URL": "postgres://db.example/x", "AUTH0_DOMAIN": "tenant.ca.auth0.com", "AUTH0_AUDIENCE": "https://api.test", "AUTH0_MGMT_CLIENT_ID": "mgmt-id", "AUTH0_MGMT_CLIENT_SECRET": "mgmt-secret"}
		if offset != "" {
			vars["CLOCK_OFFSET"] = offset
		}
		return vars
	}
	tests := []struct {
		name    string
		vars    map[string]string
		want    time.Duration
		wantErr string
	}{
		{name: "no offset by default", vars: base("local", ""), want: 0},
		{name: "a week ahead", vars: base("local", "168h"), want: 168 * time.Hour},
		{name: "local", vars: base("local", "90m"), want: 90 * time.Minute},
		{name: "surrounding spaces are ignored", vars: base("local", " 24h "), want: 24 * time.Hour},
		{name: "production without offset", vars: base("production", ""), want: 0},
		{name: "production refuses an offset", vars: base("production", "1h"), wantErr: "CLOCK_OFFSET is only allowed when ENV=local"},
		{name: "production refuses even a zero offset", vars: base("production", "0s"), wantErr: "CLOCK_OFFSET is only allowed when ENV=local"},
		{name: "production refuses an invalid offset", vars: base("production", "soon"), wantErr: "CLOCK_OFFSET is only allowed when ENV=local"},
		{name: "staging is no longer an environment", vars: base("staging", "1h"), wantErr: "ENV must be one of local, production"},
		{name: "not a duration", vars: base("local", "soon"), wantErr: "CLOCK_OFFSET must be a positive duration"},
		{name: "days are not a duration", vars: base("local", "7d"), wantErr: "CLOCK_OFFSET must be a positive duration"},
		{name: "negative", vars: base("local", "-1h"), wantErr: "CLOCK_OFFSET must be a positive duration"},
		{name: "zero", vars: base("local", "0s"), wantErr: "CLOCK_OFFSET must be a positive duration"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(envFrom(tt.vars))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Load() error = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.ClockOffset != tt.want {
				t.Fatalf("ClockOffset = %v, want %v", cfg.ClockOffset, tt.want)
			}
		})
	}
}
