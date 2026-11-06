package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/httpx"
)

type userIDKey struct{}

// UserIDResolver returns the identifier of the local user of an auth
// subject, creating the user on first login. An *apperr.Error (such as
// account_deleted) is answered as is.
type UserIDResolver func(ctx context.Context, subject string) (string, error)

// UserID returns the identifier of the local user CurrentUser put in the
// request context. It works with the *gin.Context the generated handlers
// receive, like ClaimsFromContext.
func UserID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(userIDKey{}).(string)
	return id, ok && id != ""
}

// CurrentUser runs after Authenticate: it turns the token's subject into the
// local user and puts its identifier in the request context. A deleted
// account answers 401 account_deleted; any other failure is a 500, logged by
// httpx.RespondError. Claims missing from the context (Authenticate not run)
// answer 401 unauthenticated: the route is never served anonymously.
func CurrentUser(resolve UserIDResolver, logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := ClaimsFromContext(c.Request.Context())
		if !ok {
			httpx.RespondError(c, logger, apperr.New(apperr.CodeUnauthenticated, nil))
			return
		}
		id, err := resolve(c.Request.Context(), claims.Subject)
		switch {
		case errors.Is(err, context.Canceled) && c.Request.Context().Err() != nil:
			// Same exception as Authenticate: the client is gone. Any other
			// cancellation is a failure of ours, answered and logged as a 500.
			c.AbortWithStatus(statusClientClosedRequest)
			return
		case err != nil:
			httpx.RespondError(c, logger, err)
			return
		case id == "":
			httpx.RespondError(c, logger, apperr.New(apperr.CodeInternal, errors.New("resolve user: empty identifier")))
			return
		}
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), userIDKey{}, id))
	}
}

// Protect runs the handlers (Authenticate then CurrentUser) on every request
// except OPTIONS requests matching no route (CORS preflights, answered by the
// CORS middleware placed before Protect) and the routes of public, written
// "METHOD /path" as registered ("GET /healthz"). Everything else is protected,
// including routes added later and unknown paths: a route is public only when
// it is listed. The handlers must not call c.Next: they would serve the route
// before the next handler of the list.
func Protect(public []string, handlers ...gin.HandlerFunc) gin.HandlerFunc {
	open := make(map[string]bool, len(public))
	for _, route := range public {
		open[route] = true
	}
	return func(c *gin.Context) {
		preflight := c.Request.Method == http.MethodOptions && c.FullPath() == ""
		if preflight || open[c.Request.Method+" "+c.FullPath()] {
			return
		}
		for _, h := range handlers {
			h(c)
			if c.IsAborted() {
				return
			}
		}
	}
}
