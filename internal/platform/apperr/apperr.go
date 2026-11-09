// Package apperr defines the application errors returned to clients: a stable
// code (that clients translate), an HTTP status, optional parameters and a
// chained cause that is never exposed. Each ticket adds the codes it returns.
package apperr

import (
	"fmt"
	"net/http"
	"strings"
)

// Code is a stable snake_case identifier, part of the API contract.
type Code string

// Codes currently returned by the API.
const (
	CodeUnauthenticated Code = "unauthenticated"
	CodeInvalidToken    Code = "invalid_token"
	CodeTokenExpired    Code = "token_expired"
	CodeAccountDeleted  Code = "account_deleted"
	// CodeProfileIncomplete refuses creating or joining a challenge until
	// the pseudo is set: a condition to meet, not a missing right (422).
	CodeProfileIncomplete Code = "profile_incomplete"
	CodeNotFound          Code = "not_found"
	// CodeForbidden refuses an action on a resource the user may see.
	CodeForbidden Code = "forbidden"
	// CodeRegistrationClosed refuses joining from the end of week 1.
	CodeRegistrationClosed Code = "registration_closed"
	// CodeAlreadyParticipant refuses joining a challenge twice.
	CodeAlreadyParticipant Code = "already_participant"
	// CodeParticipationAbandoned refuses joining again after abandoning:
	// abandoning is final.
	CodeParticipationAbandoned Code = "participation_abandoned"
	// CodeNotActiveParticipant refuses making creator someone who is not an
	// active participant of the challenge.
	CodeNotActiveParticipant Code = "not_active_participant"
	// CodeCannotExcludeCreator refuses excluding a creator of the challenge.
	CodeCannotExcludeCreator Code = "cannot_exclude_creator"
	// CodeChallengeEnded refuses a change to a finished challenge.
	CodeChallengeEnded Code = "challenge_ended"
	// CodeFieldNotEditable refuses a change to a field frozen at creation.
	CodeFieldNotEditable Code = "field_not_editable"
	// CodeChallengeNotFound also answers a private challenge the user may
	// not see: its existence is never revealed.
	CodeChallengeNotFound Code = "challenge_not_found"
	CodeValidationFailed  Code = "validation_failed"
	CodeInternal          Code = "internal_error"
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
	case CodeAccountDeleted:
		return definition{http.StatusUnauthorized, "Account deleted"}, true
	case CodeProfileIncomplete:
		return definition{http.StatusUnprocessableEntity, "Profile incomplete"}, true
	case CodeForbidden:
		return definition{http.StatusForbidden, "Forbidden"}, true
	case CodeRegistrationClosed:
		return definition{http.StatusUnprocessableEntity, "Registration closed"}, true
	case CodeAlreadyParticipant:
		return definition{http.StatusConflict, "Already a participant"}, true
	case CodeParticipationAbandoned:
		// A business rule (abandoning is final), not a data conflict: 422.
		return definition{http.StatusUnprocessableEntity, "Participation abandoned"}, true
	case CodeNotActiveParticipant:
		return definition{http.StatusUnprocessableEntity, "Not an active participant"}, true
	case CodeCannotExcludeCreator:
		return definition{http.StatusUnprocessableEntity, "A creator cannot be excluded"}, true
	case CodeChallengeEnded:
		return definition{http.StatusUnprocessableEntity, "Challenge ended"}, true
	case CodeFieldNotEditable:
		return definition{http.StatusUnprocessableEntity, "Field not editable"}, true
	case CodeChallengeNotFound:
		return definition{http.StatusNotFound, "Challenge not found"}, true
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

// FieldsParam is the parameter of validation_failed listing the invalid
// fields, comma-separated ("pseudo,language").
const FieldsParam = "fields"

// InvalidFields returns validation_failed with fields in params.fields (no
// parameter when fields is empty). Fields are names of the contract, never
// values of the request.
func InvalidFields(cause error, fields ...string) *Error {
	e := New(CodeValidationFailed, cause)
	if len(fields) > 0 {
		e.Params = map[string]string{FieldsParam: strings.Join(fields, ",")}
	}
	return e
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
