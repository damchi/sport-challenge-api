package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestTimeout(t *testing.T) {
	const d = time.Minute
	r := gin.New()
	r.ContextWithFallback = true
	r.Use(Timeout(d))
	var remaining time.Duration
	var hasDeadline bool
	r.GET("/", func(c *gin.Context) {
		// The handlers receive the *gin.Context as their context.Context.
		var deadline time.Time
		deadline, hasDeadline = c.Deadline()
		remaining = time.Until(deadline)
		c.Status(http.StatusNoContent)
	})

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if !hasDeadline {
		t.Fatal("request context has no deadline")
	}
	if remaining <= 0 || remaining > d {
		t.Fatalf("deadline in %v, want within %v", remaining, d)
	}
}
