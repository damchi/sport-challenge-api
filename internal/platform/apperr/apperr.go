// Package apperr defines the application errors returned to clients: a stable
// code (that clients translate), an HTTP status, optional parameters and a
// chained cause that is never exposed. Each ticket adds the codes it returns.
package apperr

import (
	"fmt"
	"net/http"
)

// Code is a stable snake_case identifier, part of the API contract.
type Code string

// Codes currently returned by the API.
const (
	CodeUnauthenticated  Code = "unauthenticated"
	CodeInvalidToken     Code = "invalid_token"
	CodeTokenExpired     Code = "token_expired"
	CodeNotFound         Code = "not_found"
	CodeValidationFailed Code = "validation_failed"
	CodeInternal         Code = "internal_error"
)

type definition struct {
	status  int
	message string
}

// definitionOf gives each code its HTTP status and English fallback message.
func definitionOf(code Code) (definition, bool) {
	switch code {
	case CodeUnauthenticated:
		return definition{http.StatusUnauthorized, "Authentication required"}, true
	case CodeInvalidToken:
		return definition{http.StatusUnauthorized, "Invalid access token"}, true
	case CodeTokenExpired:
		return definition{http.StatusUnauthorized, "Access token expired"}, true
	case CodeNotFound:
		return definition{http.StatusNotFound, "Resource not found"}, true
	case CodeValidationFailed:
		return definition{http.StatusBadRequest, "Invalid request"}, true
	case CodeInternal:
		return definition{http.StatusInternalServerError, "Internal server error"}, true
	}
	return definition{}, false
}

// Error is an application error. Only Code, Message and Params reach the
// client; the cause stays on the server side (logs, errors.Is, errors.As).
type Error struct {
	Code    Code
	Status  int
	Message string
	// Params carry values for the client's text (a deadline...); never
	// personal data.
	Params map[string]string
	cause  error
}

// New returns the error for code, wrapping cause (nil if none). An unknown
// code is a programming error: it becomes internal_error.
func New(code Code, cause error) *Error {
	def, ok := definitionOf(code)
	if !ok {
		internal, _ := definitionOf(CodeInternal)
		return &Error{
			Code: CodeInternal, Status: internal.status, Message: internal.message,
			cause: fmt.Errorf("unknown error code %q: %w", code, cause),
		}
	}
	return &Error{Code: code, Status: def.status, Message: def.message, cause: cause}
}

// Error implements error; it includes the cause, for logs only.
func (e *Error) Error() string {
	if e.cause == nil {
		return string(e.Code)
	}
	return string(e.Code) + ": " + e.cause.Error()
}

// Unwrap exposes the cause to errors.Is and errors.As.
func (e *Error) Unwrap() error { return e.cause }

// Is matches any *Error with the same code: errors.Is(err, apperr.New(code, nil)).
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}
