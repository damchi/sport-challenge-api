// Package auth verifies the access tokens sent to the API. The rest of the
// application only sees TokenVerifier and Claims, never the provider.
package auth

import (
	"context"
	"errors"
	"time"
)

// Errors returned by Verify, wrapping the underlying cause. Any other error
// (cancelled request, JWKS timeout) says nothing about the token.
var (
	// ErrTokenExpired means the token was valid but is past its expiration.
	ErrTokenExpired = errors.New("token expired")
	// ErrInvalidToken covers every other rejection: signature, algorithm,
	// issuer, audience, not yet valid, machine-to-machine token...
	ErrInvalidToken = errors.New("invalid token")
)

// Claims are the only token claims exposed to the application.
type Claims struct {
	// Subject identifies the user at the provider (JWT "sub").
	Subject   string
	ExpiresAt time.Time
	Scopes    []string
}

// TokenVerifier checks an access token and returns its claims.
type TokenVerifier interface {
	Verify(ctx context.Context, rawToken string) (Claims, error)
}
