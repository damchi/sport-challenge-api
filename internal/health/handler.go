// Package health serves the liveness and readiness probes.
package health

import (
	"context"
	"log/slog"
	"time"

	"github.com/damchi/sport-challenge-api/internal/api"
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
}

// NewHandler returns a Handler whose readiness depends on db.
func NewHandler(db Pinger, logger *slog.Logger) Handler {
	return Handler{db: db, logger: logger}
}

// GetHealthz always answers 200: it only tells that the process serves requests.
func (Handler) GetHealthz(context.Context, api.GetHealthzRequestObject) (api.GetHealthzResponseObject, error) {
	return api.GetHealthz200JSONResponse{Status: api.HealthStatusOk}, nil
}

// GetReadyz answers 200 when the database answers within pingTimeout, 503
// otherwise, and logs why (connection refused, timeout, authentication...).
func (h Handler) GetReadyz(ctx context.Context, _ api.GetReadyzRequestObject) (api.GetReadyzResponseObject, error) {
	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := h.db.Ping(ctx); err != nil {
		h.logger.WarnContext(ctx, "database not ready", slog.String("error", err.Error()))
		return api.GetReadyz503JSONResponse{Status: api.ReadinessStatusUnavailable}, nil
	}
	return api.GetReadyz200JSONResponse{Status: api.ReadinessStatusOk}, nil
}
