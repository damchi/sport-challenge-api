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
	// CodeAccountBlocked refuses every request of an account an admin
	// blocked but the deletion of its own account (README §2.12):
	// authenticated, but not allowed in (403).
	CodeAccountBlocked Code = "account_blocked"
	// CodeProfileIncomplete refuses creating or joining a challenge and
	// recording an activity until the first and last names are set: a
	// condition to meet, not a missing right (422).
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
	// CodeLastCreator refuses the deletion of an account that is the last
	// creator of challenges not finished (ChallengesParam lists them).
	CodeLastCreator Code = "last_creator"
	// CodeNoCreator refuses an admin change that would bring a challenge
	// without any creator who still has an account back upcoming or ongoing
	// (README §2.12): the admin names a creator first.
	CodeNoCreator Code = "no_creator"
	// CodeChallengeEnded refuses a change to a finished challenge.
	CodeChallengeEnded Code = "challenge_ended"
	// CodeDistanceRequired refuses an activity without distance for a sport
	// that has one.
	CodeDistanceRequired Code = "distance_required"
	// CodeDistanceNotAllowed refuses a distance for a sport without one.
	CodeDistanceNotAllowed Code = "distance_not_allowed"
	// CodeElevationNotAllowed refuses an elevation gain for a sport that
	// takes none, or for an activity without distance.
	CodeElevationNotAllowed Code = "elevation_not_allowed"
	// CodeWeekLocked refuses writing an activity whose week is locked (7 am
	// on the first day of the next week) in a challenge where it counts, or
	// was when its author left one (ADR 0001).
	CodeWeekLocked Code = "week_locked"
	// CodeActivityOverlap refuses an activity that overlaps another of its
	// author (README §2.5).
	CodeActivityOverlap Code = "activity_overlap"
	// CodeFieldNotEditable refuses a change to a field frozen at creation.
	CodeFieldNotEditable Code = "field_not_editable"
	// CodeChallengeNotFound also answers a private challenge the user may
	// not see: its existence is never revealed.
	CodeChallengeNotFound Code = "challenge_not_found"
	// CodeActivityNotFound also answers an activity of another user.
	CodeActivityNotFound Code = "activity_not_found"
	// CodeUserNotFound answers an admin read of an unknown account.
	CodeUserNotFound Code = "user_not_found"
	// CodeInvitationInvalid answers an unknown, expired or revoked
	// invitation token alike.
	CodeInvitationInvalid Code = "invitation_invalid"
	CodeValidationFailed  Code = "validation_failed"
	// CodeCursorInvalid refuses the cursor of a page of a ranking that has
	// changed since: the client starts again from the first page.
	CodeCursorInvalid Code = "cursor_invalid"
	// CodeRateLimited refuses a request beyond the rate limit of its IP
	// address or of its user.
	CodeRateLimited Code = "rate_limited"
	// CodeBusy refuses an admin change that waited too long for the writes
	// in progress, or that a deadlock stopped: nothing was written, it can
	// be tried again.
	CodeBusy     Code = "busy"
	CodeInternal Code = "internal_error"
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
	case CodeAccountBlocked:
		return definition{http.StatusForbidden, "Account blocked"}, true
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
	case CodeLastCreator:
		return definition{http.StatusUnprocessableEntity, "Last creator of a challenge"}, true
	case CodeNoCreator:
		return definition{http.StatusUnprocessableEntity, "Challenge without a creator"}, true
	case CodeChallengeEnded:
		return definition{http.StatusUnprocessableEntity, "Challenge ended"}, true
	case CodeDistanceRequired:
		return definition{http.StatusUnprocessableEntity, "Distance required"}, true
	case CodeDistanceNotAllowed:
		return definition{http.StatusUnprocessableEntity, "Distance not allowed"}, true
	case CodeElevationNotAllowed:
		return definition{http.StatusUnprocessableEntity, "Elevation gain not allowed"}, true
	case CodeWeekLocked:
		return definition{http.StatusUnprocessableEntity, "Week locked"}, true
	case CodeActivityOverlap:
		return definition{http.StatusUnprocessableEntity, "Activity overlaps another"}, true
	case CodeFieldNotEditable:
		return definition{http.StatusUnprocessableEntity, "Field not editable"}, true
	case CodeChallengeNotFound:
		return definition{http.StatusNotFound, "Challenge not found"}, true
	case CodeActivityNotFound:
		return definition{http.StatusNotFound, "Activity not found"}, true
	case CodeUserNotFound:
		return definition{http.StatusNotFound, "User not found"}, true
	case CodeInvitationInvalid:
		return definition{http.StatusNotFound, "Invitation invalid"}, true
	case CodeNotFound:
		return definition{http.StatusNotFound, "Resource not found"}, true
	case CodeValidationFailed:
		return definition{http.StatusBadRequest, "Invalid request"}, true
	case CodeCursorInvalid:
		return definition{http.StatusBadRequest, "Ranking changed since the cursor"}, true
	case CodeRateLimited:
		return definition{http.StatusTooManyRequests, "Too many requests"}, true
	case CodeBusy:
		return definition{http.StatusServiceUnavailable, "Busy, try again"}, true
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

// ChallengesParam is the parameter of last_creator listing the challenges,
// comma-separated ids.
const ChallengesParam = "challenges"

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
