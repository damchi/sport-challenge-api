package apperr

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestNew(t *testing.T) {
	tests := []struct {
		code       Code
		wantStatus int
	}{
		{CodeUnauthenticated, http.StatusUnauthorized},
		{CodeInvalidToken, http.StatusUnauthorized},
		{CodeTokenExpired, http.StatusUnauthorized},
		{CodeAccountDeleted, http.StatusUnauthorized},
		{CodeNotFound, http.StatusNotFound},
		{CodeValidationFailed, http.StatusBadRequest},
		{CodeInternal, http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(string(tt.code), func(t *testing.T) {
			e := New(tt.code, nil)
			if e.Code != tt.code || e.Status != tt.wantStatus || e.Message == "" {
				t.Fatalf("New(%s) = %+v, want status %d and a message", tt.code, e, tt.wantStatus)
			}
		})
	}
}

func TestNewUnknownCodeIsInternal(t *testing.T) {
	e := New("no_such_code", nil)
	if e.Code != CodeInternal || e.Status != http.StatusInternalServerError {
		t.Fatalf("New(unknown) = %+v, want internal_error", e)
	}
}

func TestChaining(t *testing.T) {
	cause := errors.New("signature is invalid")
	err := fmt.Errorf("authenticate: %w", New(CodeInvalidToken, cause))

	if !errors.Is(err, cause) {
		t.Error("errors.Is(err, cause) = false: the cause is not chained")
	}
	if !errors.Is(err, New(CodeInvalidToken, nil)) {
		t.Error("errors.Is(err, invalid_token) = false")
	}
	if errors.Is(err, New(CodeTokenExpired, nil)) {
		t.Error("errors.Is(err, token_expired) = true for an invalid_token error")
	}

	var appErr *Error
	if !errors.As(err, &appErr) {
		t.Fatal("errors.As(err, *Error) = false")
	}
	if appErr.Code != CodeInvalidToken || appErr.Status != http.StatusUnauthorized {
		t.Fatalf("errors.As gave %+v", appErr)
	}
}

func TestErrorKeepsTheCauseOutOfTheClientFields(t *testing.T) {
	e := New(CodeInvalidToken, errors.New("kid \"attacker\" not found"))
	if e.Message != "Invalid access token" {
		t.Fatalf("Message = %q: it must be the fixed fallback text", e.Message)
	}
	if e.Error() != `invalid_token: kid "attacker" not found` {
		t.Fatalf("Error() = %q, want the code and the cause (for logs)", e.Error())
	}
}
