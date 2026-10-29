package httpx

import (
	"crypto/rand"

	"github.com/gin-gonic/gin"
)

// RequestIDHeader carries the request identifier in both directions.
const RequestIDHeader = "X-Request-ID"

const (
	requestIDKey       = "request_id"
	maxRequestIDLength = 128
)

// RequestIDMiddleware reuses a valid incoming X-Request-ID, generates one
// otherwise, stores it in the context and returns it in the response.
func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(RequestIDHeader)
		if !validRequestID(id) {
			id = rand.Text()
		}
		c.Set(requestIDKey, id)
		c.Header(RequestIDHeader, id)
		c.Next()
	}
}

// RequestID returns the identifier set by RequestIDMiddleware, or "".
func RequestID(c *gin.Context) string {
	return c.GetString(requestIDKey)
}

// validRequestID accepts 1 to 128 characters among letters, digits, '-', '_'
// and '.', so a client cannot inject anything into logs or headers.
func validRequestID(id string) bool {
	if id == "" || len(id) > maxRequestIDLength {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}
