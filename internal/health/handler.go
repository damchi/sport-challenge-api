// Package health serves the liveness probe.
package health

import (
	"context"

	"github.com/damchi/sport-challenge-api/internal/api"
)

// Handler implements the health operations of the API contract.
type Handler struct{}

// GetHealthz always answers 200: it only tells that the process serves requests.
func (Handler) GetHealthz(context.Context, api.GetHealthzRequestObject) (api.GetHealthzResponseObject, error) {
	return api.GetHealthz200JSONResponse{Status: api.Ok}, nil
}
