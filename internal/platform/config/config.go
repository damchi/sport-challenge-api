// Package config reads and validates the configuration from environment variables.
package config

import (
	"errors"
	"log/slog"
	"strconv"
	"strings"
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
}

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

	if len(problems) > 0 {
		return Config{}, errors.New("invalid configuration: " + strings.Join(problems, "; "))
	}
	return cfg, nil
}

var logLevels = map[string]slog.Level{
	"debug": slog.LevelDebug,
	"info":  slog.LevelInfo,
	"warn":  slog.LevelWarn,
	"error": slog.LevelError,
}
