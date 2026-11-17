// Package health serves the liveness and readiness probes.
package health

import (
	"context"
	"log/slog"
	"time"

	"github.com/damchi/sport-challenge-api/internal/api"
	"github.com/damchi/sport-challenge-api/internal/platform/buildinfo"
)

// pingTimeout keeps the readiness probe fast when the database hangs.
const pingTimeout = 2 * time.Second

// Pinger checks that the database answers (*pgxpool.Pool implements it).
type Pinger interface {
	Ping(ctx context.Context) error
}

// Handler implements the health operations of the API contract.
type Handler struct {
	db     Pinger
	logger *slog.Logger
	build  buildinfo.Info
}

// NewHandler returns a Handler whose readiness depends on db, and whose
// liveness probe announces build.
func NewHandler(db Pinger, logger *slog.Logger, build buildinfo.Info) Handler {
	return Handler{db: db, logger: logger, build: build}
}

// GetHealth always answers 200: it only tells that the process serves
// requests, and which build it is (the version and the commit, nothing more).
func (h Handler) GetHealth(context.Context, api.GetHealthRequestObject) (api.GetHealthResponseObject, error) {
	return api.GetHealth200JSONResponse{Status: api.HealthStatusOk, Version: h.build.Version, Commit: h.build.Commit}, nil
}

// GetReady answers 200 when the database answers within pingTimeout, 503
// otherwise, and logs why (connection refused, timeout, authentication...).
func (h Handler) GetReady(ctx context.Context, _ api.GetReadyRequestObject) (api.GetReadyResponseObject, error) {
	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := h.db.Ping(ctx); err != nil {
		h.logger.WarnContext(ctx, "database not ready", slog.String("error", err.Error()))
		return api.GetReady503JSONResponse{Status: api.ReadinessStatusUnavailable}, nil
	}
	return api.GetReady200JSONResponse{Status: api.ReadinessStatusOk}, nil
}
