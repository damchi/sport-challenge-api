package httpx

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
)

// RecoveryMiddleware turns a panic into a generic 500 internal_error and logs
// the panic value with its stack trace. http.ErrAbortHandler, which net/http
// uses to drop a connection on purpose, only aborts the request.
func RecoveryMiddleware(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			if r == http.ErrAbortHandler {
				c.Abort()
				return
			}
			logger.ErrorContext(c.Request.Context(), "panic recovered",
				slog.String("route", c.FullPath()),
				slog.String("request_id", RequestID(c)),
				slog.String("panic", fmt.Sprint(r)),
				slog.String("stack", string(debug.Stack())),
			)
			if c.Writer.Written() {
				c.Abort()
				return
			}
			WriteError(c, http.StatusInternalServerError, "internal_error", "Internal server error")
		}()
		c.Next()
	}
}
