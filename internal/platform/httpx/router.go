// Package httpx holds the HTTP plumbing shared by every route: router,
// middlewares and the single error response format.
package httpx

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// NewRouter returns a Gin engine with the base middlewares, outermost first:
// request identifier, request log, then panic recovery (so a recovered panic
// is logged as a 500 with its request identifier). Unknown routes answer
// 404 not_found in the API error format.
func NewRouter(logger *slog.Logger) *gin.Engine {
	r := gin.New()
	r.Use(
		RequestIDMiddleware(),
		LoggerMiddleware(logger),
		RecoveryMiddleware(logger),
	)
	r.NoRoute(func(c *gin.Context) {
		WriteError(c, http.StatusNotFound, "not_found", "Resource not found")
	})
	return r
}
