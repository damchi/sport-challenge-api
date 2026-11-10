// Package config reads and validates the configuration from environment variables.
package config

import (
	"errors"
	"log/slog"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Environment is the deployment environment the process runs in.
type Environment string

// Supported environments.
const (
	EnvLocal      Environment = "local"
	EnvStaging    Environment = "staging"
	EnvProduction Environment = "production"
)

// Config holds the settings read from the environment.
type Config struct {
	Env      Environment
	Port     int
	LogLevel slog.Level
	// DatabaseURL contains the database password: never log it.
	DatabaseURL string
	// Auth0Domain is the tenant host name, without scheme or path
	// (sport-challenge-dev.ca.auth0.com); Auth0Audience is the API identifier.
	Auth0Domain   string
	Auth0Audience string
	// Auth0MgmtClientID and Auth0MgmtClientSecret identify the machine to
	// machine application that deletes users at Auth0 (Management API,
	// delete:users only). Secrets: never log them.
	Auth0MgmtClientID     string
	Auth0MgmtClientSecret string
	// CORSAllowedOrigins are the web origins allowed to call the API from a
	// browser (scheme://host[:port], lowercase). Empty: no browser origin.
	CORSAllowedOrigins []string
	// AtRiskThreshold is how long before the end of a week a participant
	// under the minimum of the elimination category is "at risk". 24 hours
	// unless AT_RISK_THRESHOLD says otherwise.
	AtRiskThreshold time.Duration
}

// defaultAtRiskThreshold is the "at risk" threshold when none is configured.
const defaultAtRiskThreshold = 24 * time.Hour

// Load reads the configuration through getenv (os.Getenv in production).
// Surrounding spaces are ignored. Every missing or invalid variable is
// reported in a single error. Values are never included in the error, so a
// misplaced secret cannot leak through it.
func Load(getenv func(string) string) (Config, error) {
	get := func(key string) string { return strings.TrimSpace(getenv(key)) }
	var cfg Config
	var problems []string

	switch env := Environment(get("ENV")); env {
	case "":
		problems = append(problems, "ENV is required")
	case EnvLocal, EnvStaging, EnvProduction:
		cfg.Env = env
	default:
		problems = append(problems, "ENV must be one of local, staging, production")
	}

	if raw := get("PORT"); raw == "" {
		problems = append(problems, "PORT is required")
	} else if port, err := strconv.Atoi(raw); err != nil || port < 1 || port > 65535 {
		problems = append(problems, "PORT must be an integer between 1 and 65535")
	} else {
		cfg.Port = port
	}

	if raw := get("LOG_LEVEL"); raw == "" {
		problems = append(problems, "LOG_LEVEL is required")
	} else if level, ok := logLevels[strings.ToLower(raw)]; !ok {
		problems = append(problems, "LOG_LEVEL must be one of debug, info, warn, error")
	} else {
		cfg.LogLevel = level
	}

	if cfg.DatabaseURL = get("DATABASE_URL"); cfg.DatabaseURL == "" {
		problems = append(problems, "DATABASE_URL is required")
	}

	// Lowercase: the expected issuer must match the "iss" Auth0 writes.
	if cfg.Auth0Domain = strings.ToLower(get("AUTH0_DOMAIN")); cfg.Auth0Domain == "" {
		problems = append(problems, "AUTH0_DOMAIN is required")
	} else if !hostName.MatchString(cfg.Auth0Domain) {
		problems = append(problems, "AUTH0_DOMAIN must be a host name (letters, digits, hyphens and dots), without scheme, port or path")
	}

	if cfg.Auth0Audience = get("AUTH0_AUDIENCE"); cfg.Auth0Audience == "" {
		problems = append(problems, "AUTH0_AUDIENCE is required")
	}

	if cfg.Auth0MgmtClientID = get("AUTH0_MGMT_CLIENT_ID"); cfg.Auth0MgmtClientID == "" {
		problems = append(problems, "AUTH0_MGMT_CLIENT_ID is required")
	}
	if cfg.Auth0MgmtClientSecret = get("AUTH0_MGMT_CLIENT_SECRET"); cfg.Auth0MgmtClientSecret == "" {
		problems = append(problems, "AUTH0_MGMT_CLIENT_SECRET is required")
	}

	if origins, ok := parseOrigins(get("CORS_ALLOWED_ORIGINS")); !ok {
		problems = append(problems, "CORS_ALLOWED_ORIGINS must be a comma-separated list of origins (http or https scheme, host, optional port, nothing else), never *")
	} else {
		cfg.CORSAllowedOrigins = origins
	}

	cfg.AtRiskThreshold = defaultAtRiskThreshold
	if raw := get("AT_RISK_THRESHOLD"); raw != "" {
		if d, err := time.ParseDuration(raw); err != nil || d <= 0 {
			problems = append(problems, "AT_RISK_THRESHOLD must be a positive duration such as 24h")
		} else {
			cfg.AtRiskThreshold = d
		}
	}

	if len(problems) > 0 {
		return Config{}, errors.New("invalid configuration: " + strings.Join(problems, "; "))
	}
	return cfg, nil
}

// parseOrigins reads a comma-separated list of origins such as
// "https://app.example.com,http://localhost:5173". Each one must be exactly
// what a browser sends in the Origin header: no wildcard, path, query,
// fragment, credentials, default port (a browser never sends
// https://host:443), trailing dot or non-ASCII host (browsers send the
// punycode form). Empty entries are ignored.
func parseOrigins(raw string) ([]string, bool) {
	var origins []string
	for entry := range strings.SplitSeq(raw, ",") {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		u, err := url.Parse(entry)
		// Rebuilt from scheme and host only, the origin must give back the
		// entry: anything else (path, query, fragment, credentials) differs.
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
			strings.Contains(u.Host, "*") || u.Scheme+"://"+u.Host != entry ||
			strings.HasSuffix(u.Host, ":") || u.Port() == defaultPorts[u.Scheme] ||
			strings.HasSuffix(u.Hostname(), ".") || !isASCII(u.Host) {
			return nil, false
		}
		origins = append(origins, entry)
	}
	return origins, true
}

func isASCII(v string) bool {
	for i := range len(v) {
		if v[i] >= 0x80 {
			return false
		}
	}
	return true
}

var defaultPorts = map[string]string{"http": "80", "https": "443"}

// hostName accepts a DNS host name such as sport-challenge-dev.ca.auth0.com.
// It rejects anything that would change the JWKS URL built from it (@, ?, #...).
var hostName = regexp.MustCompile(`^(?i)[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

var logLevels = map[string]slog.Level{
	"debug": slog.LevelDebug,
	"info":  slog.LevelInfo,
	"warn":  slog.LevelWarn,
	"error": slog.LevelError,
}
