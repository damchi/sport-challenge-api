package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testIssuer   = "https://tenant.test/"
	testAudience = "https://api.test"
)

type signingKey struct {
	kid string
	key *rsa.PrivateKey
}

func newSigningKey(t *testing.T, kid string) signingKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return signingKey{kid: kid, key: key}
}

// jwksServer serves the public part of its keys and counts the requests.
type jwksServer struct {
	mu   sync.Mutex
	keys []signingKey
	hits atomic.Int32
	srv  *httptest.Server
}

func newJWKSServer(t *testing.T, keys ...signingKey) *jwksServer {
	t.Helper()
	s := &jwksServer{keys: keys}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.hits.Add(1)
		// Like a real tenant, answer after a few milliseconds: the refresh on an
		// unknown key must leave time for the HTTP call.
		time.Sleep(20 * time.Millisecond)
		s.mu.Lock()
		defer s.mu.Unlock()
		set := map[string][]map[string]string{"keys": {}}
		for _, k := range s.keys {
			set["keys"] = append(set["keys"], map[string]string{
				"kty": "RSA",
				"use": "sig",
				"alg": "RS256",
				"kid": k.kid,
				"n":   base64.RawURLEncoding.EncodeToString(k.key.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.key.E)).Bytes()),
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(set)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *jwksServer) publish(keys ...signingKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys = keys
}

func newTestVerifier(t *testing.T, s *jwksServer, unknownKeyRefreshEvery time.Duration) *Auth0Verifier {
	t.Helper()
	v, err := newVerifier(t.Context(), verifierOptions{
		jwksURL:                s.srv.URL,
		issuer:                 testIssuer,
		audience:               testAudience,
		unknownKeyRefreshEvery: unknownKeyRefreshEvery,
	})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// validClaims returns the claims of a valid user access token.
func validClaims() jwt.MapClaims {
	now := time.Now()
	return jwt.MapClaims{
		"iss":   testIssuer,
		"aud":   []string{testAudience, testIssuer + "userinfo"},
		"sub":   "email|123",
		"iat":   now.Unix(),
		"exp":   now.Add(15 * time.Minute).Unix(),
		"scope": "openid profile email",
	}
}

func sign(t *testing.T, k signingKey, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = k.kid
	raw, err := tok.SignedString(k.key)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func with(changes map[string]any) jwt.MapClaims {
	c := validClaims()
	for k, v := range changes {
		if v == nil {
			delete(c, k)
			continue
		}
		c[k] = v
	}
	return c
}

func TestVerify(t *testing.T) {
	key := newSigningKey(t, "key-1")
	other := newSigningKey(t, "key-1") // same kid, different key: bad signature
	v := newTestVerifier(t, newJWKSServer(t, key), time.Minute)
	now := time.Now()

	noneToken, err := jwt.NewWithClaims(jwt.SigningMethodNone, validClaims()).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	hmac := jwt.NewWithClaims(jwt.SigningMethodHS256, validClaims())
	hmac.Header["kid"] = key.kid
	hmacToken, err := hmac.SignedString([]byte("shared-secret"))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		raw     string
		wantErr error
	}{
		{name: "valid", raw: sign(t, key, validClaims())},
		{name: "expired within the leeway", raw: sign(t, key, with(map[string]any{"exp": now.Add(-30 * time.Second).Unix()}))},
		{name: "expired", raw: sign(t, key, with(map[string]any{"exp": now.Add(-2 * time.Minute).Unix()})), wantErr: ErrTokenExpired},
		{name: "no expiration", raw: sign(t, key, with(map[string]any{"exp": nil})), wantErr: ErrInvalidToken},
		{name: "not yet valid", raw: sign(t, key, with(map[string]any{"nbf": now.Add(2 * time.Minute).Unix()})), wantErr: ErrInvalidToken},
		{name: "not yet valid within the leeway", raw: sign(t, key, with(map[string]any{"nbf": now.Add(30 * time.Second).Unix()}))},
		{name: "wrong audience", raw: sign(t, key, with(map[string]any{"aud": "https://other.api"})), wantErr: ErrInvalidToken},
		{name: "ID token (audience is the client ID)", raw: sign(t, key, with(map[string]any{"aud": "spa-client-id"})), wantErr: ErrInvalidToken},
		{name: "wrong issuer", raw: sign(t, key, with(map[string]any{"iss": "https://evil.test/"})), wantErr: ErrInvalidToken},
		{name: "wrong signature", raw: sign(t, other, validClaims()), wantErr: ErrInvalidToken},
		{name: "algorithm none", raw: noneToken, wantErr: ErrInvalidToken},
		{name: "HMAC algorithm", raw: hmacToken, wantErr: ErrInvalidToken},
		{name: "missing subject", raw: sign(t, key, with(map[string]any{"sub": nil})), wantErr: ErrInvalidToken},
		{name: "machine-to-machine grant", raw: sign(t, key, with(map[string]any{"gty": "client-credentials"})), wantErr: ErrInvalidToken},
		{name: "machine-to-machine subject", raw: sign(t, key, with(map[string]any{"sub": "abc123@clients"})), wantErr: ErrInvalidToken},
		{name: "garbage", raw: "not-a-jwt", wantErr: ErrInvalidToken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := v.Verify(t.Context(), tt.raw)
			if tt.wantErr == nil && err != nil {
				t.Fatalf("Verify() error = %v, want nil", err)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("Verify() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestVerifyCancelledContextIsNotAnInvalidToken(t *testing.T) {
	key := newSigningKey(t, "key-1")
	v := newTestVerifier(t, newJWKSServer(t), time.Minute)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	// Unknown key: the refresh runs with the cancelled context.
	_, err := v.Verify(ctx, sign(t, key, validClaims()))
	if errors.Is(err, ErrInvalidToken) || errors.Is(err, ErrTokenExpired) || !errors.Is(err, context.Canceled) {
		t.Fatalf("Verify() error = %v, want context.Canceled, not an invalid token", err)
	}
}

func TestVerifyExposesOnlySubjectExpirationAndScopes(t *testing.T) {
	key := newSigningKey(t, "key-1")
	v := newTestVerifier(t, newJWKSServer(t, key), time.Minute)
	exp := time.Now().Add(10 * time.Minute).Truncate(time.Second)

	got, err := v.Verify(t.Context(), sign(t, key, with(map[string]any{"exp": exp.Unix(), "email": "someone@example.com"})))
	if err != nil {
		t.Fatal(err)
	}
	want := Claims{Subject: "email|123", ExpiresAt: exp, Scopes: []string{"openid", "profile", "email"}}
	if got.Subject != want.Subject || !got.ExpiresAt.Equal(want.ExpiresAt) || !slices.Equal(got.Scopes, want.Scopes) {
		t.Fatalf("Verify() = %+v, want %+v", got, want)
	}
}

func TestVerifyRefreshesTheKeysOnUnknownKey(t *testing.T) {
	oldKey, newKey := newSigningKey(t, "key-1"), newSigningKey(t, "key-2")
	s := newJWKSServer(t, oldKey)
	v := newTestVerifier(t, s, time.Minute)

	if _, err := v.Verify(t.Context(), sign(t, oldKey, validClaims())); err != nil {
		t.Fatalf("token signed with the current key: %v", err)
	}
	before := s.hits.Load()

	// Auth0 rotates its signing key: the new key appears in the JWKS.
	s.publish(oldKey, newKey)
	if _, err := v.Verify(t.Context(), sign(t, newKey, validClaims())); err != nil {
		t.Fatalf("token signed with the rotated key: %v", err)
	}
	if got := s.hits.Load(); got != before+1 {
		t.Fatalf("JWKS fetched %d times for the rotation, want 1", got-before)
	}
	if _, err := v.Verify(t.Context(), sign(t, oldKey, validClaims())); err != nil {
		t.Fatalf("old key still published, token rejected: %v", err)
	}
}

func TestVerifyLimitsRefreshesOnUnknownKeys(t *testing.T) {
	key := newSigningKey(t, "key-1")
	s := newJWKSServer(t, key)
	v := newTestVerifier(t, s, time.Hour)

	if _, err := v.Verify(t.Context(), sign(t, key, validClaims())); err != nil {
		t.Fatal(err)
	}
	before := s.hits.Load()

	// An attacker sends tokens with made-up key IDs.
	for i := range 5 {
		unknown := newSigningKey(t, "unknown-"+string(rune('a'+i)))
		start := time.Now()
		if _, err := v.Verify(t.Context(), sign(t, unknown, validClaims())); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("token with an unknown key: error = %v, want ErrInvalidToken", err)
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("rejection took %v: requests must not wait for the rate limiter", elapsed)
		}
	}
	if got := s.hits.Load() - before; got > 1 {
		t.Fatalf("JWKS fetched %d times for 5 unknown keys, want at most 1", got)
	}
}

func TestTenantOptions(t *testing.T) {
	got := tenantOptions("sport-challenge-dev.ca.auth0.com", "https://api.dev.sport-challenge")
	want := verifierOptions{
		jwksURL:                "https://sport-challenge-dev.ca.auth0.com/.well-known/jwks.json",
		issuer:                 "https://sport-challenge-dev.ca.auth0.com/",
		audience:               "https://api.dev.sport-challenge",
		unknownKeyRefreshEvery: unknownKeyRefreshEvery,
	}
	if got != want {
		t.Fatalf("tenantOptions() = %+v, want %+v", got, want)
	}
}

func TestNewAuth0VerifierDoesNotFailOnUnreachableTenant(t *testing.T) {
	// The JWKS is fetched in the background: creation must not fail or block
	// even though this domain is unreachable from the test.
	v, err := NewAuth0Verifier(t.Context(), "tenant.invalid", testAudience)
	if err != nil {
		t.Fatalf("NewAuth0Verifier() error = %v", err)
	}
	if v == nil {
		t.Fatal("NewAuth0Verifier() = nil")
	}
}
