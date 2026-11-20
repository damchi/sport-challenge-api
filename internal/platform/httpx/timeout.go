package httpx

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
)

// Timeout gives the context of each request a deadline d away. The database
// calls and the wait for a connection of the pool stop there instead of
// waiting without limit when the pool is exhausted; the error answers as any
// other (500 internal_error).
func Timeout(d time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), d)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
