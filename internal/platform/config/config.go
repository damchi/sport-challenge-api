// Package config reads and validates the configuration from environment variables.
package config

import (
	"errors"
	"log/slog"
	"net/netip"
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
	// TrustedProxies are the addresses or CIDR ranges of the proxies in front
	// of the API (the peer the container sees behind Cloud Run): only from
	// them is X-Forwarded-For read to find the client IP. Empty: no proxy is
	// trusted and the client IP is the peer address.
	TrustedProxies []string
	// ClockOffset moves the clock of the process forward, to see a week end in
	// minutes. Zero (the default) is the real time; it is refused in production.
	ClockOffset time.Duration
	// RateLimitIP bounds the requests of one IP address, RateLimitUser the
	// write requests of one user.
	RateLimitIP   RateLimit
	RateLimitUser RateLimit
}

// RateLimit is a token bucket: RPS tokens come back per second, up to Burst.
type RateLimit struct {
	RPS   float64
	Burst int
}

// defaultAtRiskThreshold is the "at risk" threshold when none is configured.
const defaultAtRiskThreshold = 24 * time.Hour

// Rate limits when none is configured: the IP limit leaves room for several
// users behind one address, the user limit for a burst of entries.
var (
	defaultRateLimitIP   = RateLimit{RPS: 20, Burst: 40}
	defaultRateLimitUser = RateLimit{RPS: 2, Burst: 10}
)

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

	// The clock is never moved in production.
	if raw := get("CLOCK_OFFSET"); raw != "" {
		if cfg.Env == EnvProduction {
			problems = append(problems, "CLOCK_OFFSET must not be set when ENV=production")
		} else if d, err := time.ParseDuration(raw); err != nil || d <= 0 {
			problems = append(problems, "CLOCK_OFFSET must be a positive duration such as 168h")
		} else {
			cfg.ClockOffset = d
		}
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

	if proxies, ok := parseProxies(get("TRUSTED_PROXIES")); !ok {
		problems = append(problems, "TRUSTED_PROXIES must be a comma-separated list of IP addresses or CIDR ranges, never wider than /8 (IPv4) or /16 (IPv6)")
	} else {
		cfg.TrustedProxies = proxies
	}

	cfg.RateLimitIP = rateLimit(get, "RATE_LIMIT_IP", defaultRateLimitIP, &problems)
	cfg.RateLimitUser = rateLimit(get, "RATE_LIMIT_USER", defaultRateLimitUser, &problems)

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

// parseProxies reads a comma-separated list of proxy addresses or CIDR
// ranges such as "169.254.0.0/16,10.0.0.1". Too wide a range is refused
// (tooWide): a trusted client could forge its IP with X-Forwarded-For. Empty
// entries are ignored.
func parseProxies(raw string) ([]string, bool) {
	var proxies []string
	for entry := range strings.SplitSeq(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(entry); err == nil {
			if tooWide(prefix) {
				return nil, false
			}
		} else if _, err := netip.ParseAddr(entry); err != nil {
			return nil, false
		}
		proxies = append(proxies, entry)
	}
	return proxies, true
}

// tooWide refuses a range of proxies wider than a /8 in IPv4 or a /16 in
// IPv6: no proxy fleet is that large, and a range of (nearly) every address
// would trust the clients themselves. An IPv4 range written in IPv6
// (::ffff:0.0.0.0/96) is measured as the IPv4 range it matches.
func tooWide(prefix netip.Prefix) bool {
	bits := prefix.Bits()
	switch {
	case prefix.Addr().Is4():
		return bits < 8
	case prefix.Addr().Is4In6() && bits >= 96:
		return bits-96 < 8
	default:
		return bits < 16
	}
}

// RPS and Burst are bounded, far beyond any real setting: the time a bucket
// takes to refill (Burst / RPS) must fit in a time.Duration.
const (
	minRateLimitRPS = 0.001
	maxRateLimit    = 1_000_000
)

// rateLimit reads <prefix>_RPS (a number, decimals allowed) and
// <prefix>_BURST (an integer); each one falls back to def when unset.
func rateLimit(get func(string) string, prefix string, def RateLimit, problems *[]string) RateLimit {
	limit := def
	if raw := get(prefix + "_RPS"); raw != "" {
		// "!(rps >= min)" also refuses NaN.
		if rps, err := strconv.ParseFloat(raw, 64); err != nil || !(rps >= minRateLimitRPS) || rps > maxRateLimit {
			*problems = append(*problems, prefix+"_RPS must be a number between 0.001 and 1000000")
		} else {
			limit.RPS = rps
		}
	}
	if raw := get(prefix + "_BURST"); raw != "" {
		if burst, err := strconv.Atoi(raw); err != nil || burst < 1 || burst > maxRateLimit {
			*problems = append(*problems, prefix+"_BURST must be an integer between 1 and 1000000")
		} else {
			limit.Burst = burst
		}
	}
	return limit
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
