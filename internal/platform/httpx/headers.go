package httpx

import "github.com/gin-gonic/gin"

// SecurityHeaders sets the headers every response carries, before the
// handler runs:
//   - X-Content-Type-Options: nosniff, so a browser never reads a JSON
//     response as another type;
//   - Cache-Control: no-store, as the responses hold personal data (pseudo,
//     activities) that no cache keeps. A handler whose response can be kept
//     sets its own Cache-Control, which replaces this one (GET /sports).
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		c.Next()
	}
}
