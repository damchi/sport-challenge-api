package auth

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/httpx"
)

// statusClientClosedRequest is nginx's convention for a client that left
// before the response.
const statusClientClosedRequest = 499

// maxTokenLength bounds the token parsed: Auth0 access tokens are far smaller.
const maxTokenLength = 8 << 10

type claimsKey struct{}

// ClaimsFromContext returns the claims Authenticate put in the request
// context. It works with the request context and with the *gin.Context the
// generated handlers receive (the router enables ContextWithFallback).
func ClaimsFromContext(ctx context.Context) (Claims, bool) {
	claims, ok := ctx.Value(claimsKey{}).(Claims)
	return claims, ok
}

// Authenticate requires "Authorization: Bearer <access token>" and verifies
// the token. Missing, repeated or malformed header: 401 unauthenticated;
// invalid token: 401 invalid_token; expired: 401 token_expired, always with a
// WWW-Authenticate header. A verification that could not run (JWKS timeout)
// is a 500, logged: it must not look like a bad token to the client. On
// success the claims go in the request context. It never logs the token, nor
// the verification error (it echoes values the client chose, such as the key
// ID). It never calls c.Next, so Protect can chain CurrentUser after it: keep
// it that way.
func Authenticate(verifier TokenVerifier, logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		headers := c.Request.Header.Values("Authorization")
		raw, ok := "", len(headers) == 1
		if ok {
			raw, ok = bearerToken(headers[0])
		}
		if !ok {
			c.Header("WWW-Authenticate", "Bearer")
			httpx.RespondError(c, logger, apperr.New(apperr.CodeUnauthenticated, nil))
			return
		}

		claims, err := verifier.Verify(c.Request.Context(), raw)
		switch {
		case err == nil:
		case errors.Is(err, context.Canceled):
			// The client is gone: nobody reads the response. 499 (client
			// closed request) keeps the request log from showing a success.
			// Deliberate exception to httpx.RespondError: no client receives
			// this response, so there is no error body to format.
			c.AbortWithStatus(statusClientClosedRequest)
			return
		case !errors.Is(err, ErrInvalidToken) && !errors.Is(err, ErrTokenExpired):
			logger.ErrorContext(c.Request.Context(), "access token verification unavailable",
				slog.Bool("deadline_exceeded", errors.Is(err, context.DeadlineExceeded)),
				slog.String("request_id", httpx.RequestID(c)),
			)
			httpx.RespondError(c, logger, apperr.New(apperr.CodeInternal, nil))
			return
		default:
			// A JWKS that cannot be downloaded shows as an unknown key, hence
			// invalid_token; the refresh failure itself is logged by the JWKS
			// client.
			code := apperr.CodeInvalidToken
			if errors.Is(err, ErrTokenExpired) {
				code = apperr.CodeTokenExpired
			}
			logger.DebugContext(c.Request.Context(), "access token rejected",
				slog.String("code", string(code)),
				slog.String("request_id", httpx.RequestID(c)),
			)
			c.Header("WWW-Authenticate", `Bearer error="invalid_token"`)
			httpx.RespondError(c, logger, apperr.New(code, err))
			return
		}

		// No c.Next(): Gin runs the next handler on return, and Protect can
		// then chain CurrentUser after this middleware.
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), claimsKey{}, claims))
	}
}

// bearerToken extracts the token of an "Authorization: Bearer <token>"
// header. The scheme is case-insensitive (RFC 6750); anything else is refused.
func bearerToken(header string) (string, bool) {
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" || len(token) > maxTokenLength ||
		strings.ContainsAny(token, " \t") {
		return "", false
	}
	return token, true
}
