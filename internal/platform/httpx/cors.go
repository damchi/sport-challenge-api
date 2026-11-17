package httpx

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

const (
	// corsAllowedMethods are the methods of the API routes.
	corsAllowedMethods = "GET, POST, PUT, PATCH, DELETE"
	// corsAllowedHeaders are the request headers a web client may send.
	corsAllowedHeaders = "Authorization, Content-Type, Idempotency-Key, " + RequestIDHeader
	// corsExposedHeaders are the response headers a web client may read
	// besides the safelisted ones: the delay of a 429, the challenge of a 401
	// and the request identifier of every response.
	corsExposedHeaders = "Retry-After, WWW-Authenticate, " + RequestIDHeader
	// corsMaxAge lets the browser reuse a preflight answer for 10 minutes.
	corsMaxAge = 600
)

// CORS lets the web origins of allowedOrigins call the API from a browser.
// Origins are compared exactly (they come lowercased from the configuration,
// as browsers send them); there is no wildcard. The API uses bearer tokens,
// never cookies, so credentials are not allowed.
//
// A preflight (OPTIONS with Access-Control-Request-Method) is answered here
// with 204, without authentication: it must sit before Authenticate. A
// refused origin gets the 204 without any CORS header, so the browser blocks
// the call. Other requests go on; an allowed origin gets its
// Access-Control-Allow-Origin and Access-Control-Expose-Headers, set before
// the next middlewares so a 429 of the rate limit carries them too; a refused
// one gets none and the browser hides the response.
func CORS(allowedOrigins []string) gin.HandlerFunc {
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[o] = true
	}
	return func(c *gin.Context) {
		h := c.Writer.Header()
		// The answer depends on the origin, even when there is none: caches
		// must not share it between origins.
		h.Add("Vary", "Origin")
		origin := c.GetHeader("Origin")
		if origin == "" {
			return
		}
		ok := allowed[origin]
		if ok {
			h.Set("Access-Control-Allow-Origin", origin)
		}

		if c.Request.Method != http.MethodOptions || c.GetHeader("Access-Control-Request-Method") == "" {
			if ok {
				h.Set("Access-Control-Expose-Headers", corsExposedHeaders)
			}
			return
		}
		h.Add("Vary", "Access-Control-Request-Method, Access-Control-Request-Headers")
		if ok {
			h.Set("Access-Control-Allow-Methods", corsAllowedMethods)
			h.Set("Access-Control-Allow-Headers", corsAllowedHeaders)
			h.Set("Access-Control-Max-Age", strconv.Itoa(corsMaxAge))
		}
		c.AbortWithStatus(http.StatusNoContent)
	}
}
