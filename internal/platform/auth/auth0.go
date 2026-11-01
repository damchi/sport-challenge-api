package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/time/rate"
)

const (
	// leeway tolerates clock differences on exp and nbf.
	leeway = 60 * time.Second
	// jwksRefreshInterval refreshes the keys in the background.
	jwksRefreshInterval = time.Hour
	// unknownKeyRefreshEvery limits the refreshes triggered by a token signed
	// with an unknown key, so an attacker cannot make the API download the
	// JWKS in a loop.
	unknownKeyRefreshEvery = 5 * time.Minute
	// jwksHTTPTimeout bounds each JWKS download. jwkset also uses it as the
	// deadline of an unknown-key refresh, HTTP call included, so it must leave
	// time for that call. A token arriving while the limiter is exhausted still
	// fails at once: the limiter refuses a wait longer than the deadline.
	jwksHTTPTimeout = 5 * time.Second
	// machineGrantType marks Auth0 machine-to-machine (client credentials) tokens.
	machineGrantType = "client-credentials"
	machineSubSuffix = "@clients"
)

// Auth0Verifier verifies Auth0 access tokens: RS256 signature checked against
// the tenant's cached JWKS, issuer, audience, exp and nbf. ID tokens fail the
// audience check (their audience is the client ID) and machine-to-machine
// tokens are rejected: only users call the API.
type Auth0Verifier struct {
	keys   keyfunc.Keyfunc
	parser *jwt.Parser
}

// NewAuth0Verifier returns a verifier for the tenant domain (host name only)
// and the API audience. ctx ends the background JWKS refresh. The JWKS is
// fetched in the background: an unreachable tenant does not fail startup.
func NewAuth0Verifier(ctx context.Context, domain, audience string) (*Auth0Verifier, error) {
	return newVerifier(ctx, tenantOptions(domain, audience))
}

// tenantOptions builds the JWKS URL and the issuer Auth0 uses for domain. The
// issuer ends with a slash: Auth0 puts it in every token's "iss".
func tenantOptions(domain, audience string) verifierOptions {
	return verifierOptions{
		jwksURL:                "https://" + domain + "/.well-known/jwks.json",
		issuer:                 "https://" + domain + "/",
		audience:               audience,
		unknownKeyRefreshEvery: unknownKeyRefreshEvery,
	}
}

type verifierOptions struct {
	jwksURL                string
	issuer                 string
	audience               string
	unknownKeyRefreshEvery time.Duration
}

func newVerifier(ctx context.Context, opts verifierOptions) (*Auth0Verifier, error) {
	keys, err := keyfunc.NewDefaultOverrideCtx(ctx, []string{opts.jwksURL}, keyfunc.Override{
		Client:            jwksClient(),
		HTTPTimeout:       jwksHTTPTimeout,
		RateLimitWaitMax:  jwksHTTPTimeout,
		RefreshInterval:   jwksRefreshInterval,
		RefreshUnknownKID: rate.NewLimiter(rate.Every(opts.unknownKeyRefreshEvery), 1),
	})
	if err != nil {
		return nil, fmt.Errorf("create JWKS client: %w", err)
	}
	return &Auth0Verifier{
		keys: keys,
		parser: jwt.NewParser(
			jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
			jwt.WithIssuer(opts.issuer),
			jwt.WithAudience(opts.audience),
			jwt.WithLeeway(leeway),
			jwt.WithExpirationRequired(),
			// exp and nbf are checked against the real time on purpose, never
			// against platform/clock: Auth0 issues them in real time, and an
			// offset clock (staging) would reject valid tokens.
		),
	}, nil
}

// jwksClient is dedicated to the JWKS download: bounded in time and never
// following a redirect away from the tenant.
func jwksClient() *http.Client {
	return &http.Client{
		Timeout: jwksHTTPTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// tokenClaims are the claims read from an Auth0 access token.
type tokenClaims struct {
	jwt.RegisteredClaims
	Scope     string `json:"scope"`
	GrantType string `json:"gty"`
}

// Verify implements TokenVerifier.
func (v *Auth0Verifier) Verify(ctx context.Context, rawToken string) (Claims, error) {
	var c tokenClaims
	_, err := v.parser.ParseWithClaims(rawToken, &c, v.keys.KeyfuncCtx(ctx))
	switch {
	case errors.Is(err, jwt.ErrTokenExpired):
		return Claims{}, fmt.Errorf("%w: %w", ErrTokenExpired, err)
	case err != nil:
		return Claims{}, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	case c.Subject == "":
		return Claims{}, fmt.Errorf("%w: missing subject", ErrInvalidToken)
	case c.GrantType == machineGrantType || strings.HasSuffix(c.Subject, machineSubSuffix):
		return Claims{}, fmt.Errorf("%w: machine-to-machine token", ErrInvalidToken)
	}
	return Claims{
		Subject:   c.Subject,
		ExpiresAt: c.ExpiresAt.Time,
		Scopes:    strings.Fields(c.Scope),
	}, nil
}
