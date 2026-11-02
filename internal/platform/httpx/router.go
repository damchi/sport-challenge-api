// Package httpx holds the HTTP plumbing shared by every route: router,
// middlewares and the single error response format.
package httpx

import (
	"log/slog"

	"github.com/gin-gonic/gin"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
)

// NewRouter returns a Gin engine with the base middlewares, outermost first:
// request identifier, request log, then panic recovery (so a recovered panic
// is logged as a 500 with its request identifier). Unknown routes answer
// 404 not_found in the API error format.
//
// ContextWithFallback makes the *gin.Context, which the generated handlers
// receive as their context.Context, expose the request context: its values
// (the authenticated claims) and its cancellation. Gin reuses its contexts:
// never pass a request's ctx to a goroutine that outlives the handler (it
// could read another request's claims); use context.WithoutCancel on
// c.Request.Context() or copy the values instead.
func NewRouter(logger *slog.Logger) *gin.Engine {
	r := gin.New()
	r.ContextWithFallback = true
	r.Use(
		RequestIDMiddleware(),
		LoggerMiddleware(logger),
		RecoveryMiddleware(logger),
	)
	r.NoRoute(func(c *gin.Context) {
		RespondError(c, logger, apperr.New(apperr.CodeNotFound, nil))
	})
	return r
}
