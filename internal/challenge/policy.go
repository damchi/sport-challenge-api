package challenge

import (
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
)

// Visibilities of a challenge.
const (
	VisibilityPublic  = "public"
	VisibilityPrivate = "private"
)

// ParticipantStatus is the status of a participation (column
// participants.status); empty when the user never joined.
type ParticipantStatus string

// Participation statuses.
const (
	ParticipantActive    ParticipantStatus = "active"
	ParticipantAbandoned ParticipantStatus = "abandoned"
)

// Membership is what the service knows about a user and a challenge, read in
// the same query as the challenge (Repository.Get).
type Membership struct {
	Creator     bool
	Participant ParticipantStatus
	// Invited is true when the user holds a valid invitation (tickets 065, 066).
	Invited bool
}

// Role is the role of a user in a challenge, from none to the strongest.
type Role int

// Roles, as listed in the rights matrix of README §5.7.
const (
	RoleOutsider Role = iota
	RoleInvited
	RoleAbandoned
	RoleParticipant
	RoleCreator
)

// RoleOf returns the role of a membership. A creator stays a creator even
// after abandoning (they can still delete the challenge); a participation,
// active or abandoned, outranks an invitation.
func RoleOf(m Membership) Role {
	switch {
	case m.Creator:
		return RoleCreator
	case m.Participant == ParticipantActive:
		return RoleParticipant
	case m.Participant == ParticipantAbandoned:
		return RoleAbandoned
	case m.Invited:
		return RoleInvited
	default:
		return RoleOutsider
	}
}

// CanView reports whether role may see a challenge of that visibility and
// its leaderboard (README §5.7): every logged-in user for a public challenge;
// creators, participants (abandoned included) and invited users for a
// private one.
func CanView(role Role, visibility string) bool {
	switch visibility {
	case VisibilityPublic:
		return true
	case VisibilityPrivate:
		// An allowlist: a role this switch does not know is never let in.
		switch role {
		case RoleCreator, RoleParticipant, RoleAbandoned, RoleInvited:
			return true
		default:
			return false
		}
	default:
		return false
	}
}

// RequireView is CanView for a use case: a challenge the user may not see
// answers 404 challenge_not_found, never 403, so a private challenge's
// existence is not revealed.
func RequireView(role Role, visibility string) error {
	if !CanView(role, visibility) {
		return apperr.New(apperr.CodeChallengeNotFound, nil)
	}
	return nil
}

// CanJoin reports whether role may join a challenge of that visibility at
// now (README §5.7): a public one for every logged-in user, a private one on
// invitation, strictly before deadline (the end of week 1), and never twice:
// a participant, active or abandoned, cannot join again. The profile
// completeness is checked apart (profile_incomplete).
func CanJoin(role Role, visibility string, now, deadline time.Time) bool {
	if !now.Before(deadline) {
		return false
	}
	switch role {
	case RoleOutsider:
		return visibility == VisibilityPublic
	case RoleInvited:
		return visibility == VisibilityPublic || visibility == VisibilityPrivate
	default:
		return false
	}
}

// CanEdit reports whether role may edit the title and the description of a
// challenge (README §5.7): its creators only.
func CanEdit(role Role) bool {
	return role == RoleCreator
}

// RequireEdit is CanEdit for a use case: 403 forbidden, as the actor may
// already see the challenge.
func RequireEdit(role Role) error {
	if !CanEdit(role) {
		return apperr.New(apperr.CodeForbidden, nil)
	}
	return nil
}
