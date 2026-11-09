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

// CanJoin reports whether a user with membership m may join a challenge of
// that visibility at now: JoinRefusal gives no reason.
func CanJoin(m Membership, visibility string, now, deadline time.Time) bool {
	return JoinRefusal(m, visibility, now, deadline) == nil
}

// JoinRefusal is the join rule of README §5.7, with the reason of a refusal:
// a challenge the user may not see is not found (404); a public one is open
// to every logged-in user and a private one on invitation, strictly before
// deadline, the end of week 1 (422 registration_closed); never twice: an
// active participant is already in (409 already_participant), an abandoned
// one is out for good (422 participation_abandoned), creators included. It
// reads the participation itself, not the role: a creator who abandoned
// keeps the creator role but no longer competes. The profile completeness is
// checked apart (profile_incomplete).
func JoinRefusal(m Membership, visibility string, now, deadline time.Time) error {
	if err := RequireView(RoleOf(m), visibility); err != nil {
		return err
	}
	switch {
	case m.Participant == ParticipantActive:
		return apperr.New(apperr.CodeAlreadyParticipant, nil)
	case m.Participant == ParticipantAbandoned:
		return apperr.New(apperr.CodeParticipationAbandoned, nil)
	case m.Creator:
		// Every creator is a participant (tickets 029, 036): none to add.
		return apperr.New(apperr.CodeAlreadyParticipant, nil)
	case !now.Before(deadline):
		return apperr.New(apperr.CodeRegistrationClosed, nil)
	default:
		// Visible and not in: public for everyone, private for the invited.
		return nil
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

// AbandonRefusal is the abandon rule (README §2.2): an active participant,
// creators included, may abandon until the end of the challenge (endsAt
// excluded: 422 challenge_ended from that instant); abandoning is final, so
// abandoning again changes nothing (noop, no error). A challenge the user may
// not see is not found (404 challenge_not_found); a visible one the user
// never joined has no participation to abandon (404 not_found).
func AbandonRefusal(m Membership, visibility string, now, endsAt time.Time) (noop bool, err error) {
	if err := RequireView(RoleOf(m), visibility); err != nil {
		return false, err
	}
	switch m.Participant {
	case ParticipantAbandoned:
		return true, nil
	case ParticipantActive:
		if !now.Before(endsAt) {
			return false, apperr.New(apperr.CodeChallengeEnded, nil)
		}
		return false, nil
	default:
		return false, apperr.New(apperr.CodeNotFound, nil)
	}
}

// CanExclude reports whether role may exclude a participant (README §5.7):
// creators only, even after abandoning.
func CanExclude(role Role) bool {
	return role == RoleCreator
}

// ExcludeRefusal is the exclusion rule. On the actor: a challenge the actor
// may not see is not found (404 challenge_not_found), and only creators may
// exclude (403 forbidden). On the target: creators cannot be excluded (422
// cannot_exclude_creator), someone who never joined has nothing to lose
// (404 not_found), someone already out changes nothing (noop), and a
// finished challenge is frozen (422 challenge_ended from the exact endsAt).
// An excluded participant is abandoned, shown like any abandon.
func ExcludeRefusal(actor, target Membership, visibility string, now, endsAt time.Time) (noop bool, err error) {
	role := RoleOf(actor)
	if err := RequireView(role, visibility); err != nil {
		return false, err
	}
	if !CanExclude(role) {
		return false, apperr.New(apperr.CodeForbidden, nil)
	}
	if target.Creator {
		return false, apperr.New(apperr.CodeCannotExcludeCreator, nil)
	}
	switch target.Participant {
	case ParticipantAbandoned:
		return true, nil
	case ParticipantActive:
		if !now.Before(endsAt) {
			return false, apperr.New(apperr.CodeChallengeEnded, nil)
		}
		return false, nil
	default:
		return false, apperr.New(apperr.CodeNotFound, nil)
	}
}

// CanAddCreator reports whether role may make someone a creator (README
// §5.7): creators only, even after abandoning.
func CanAddCreator(role Role) bool {
	return role == RoleCreator
}

// AddCreatorRefusal is the rule to make target a creator. On the actor: a
// challenge the actor may not see is not found (404 challenge_not_found),
// and only creators may add one (403 forbidden). On the target: a creator
// already changes nothing (noop), only an active participant may become one
// (422 not_active_participant: every creator is a participant), and a
// finished challenge is frozen (422 challenge_ended from the exact endsAt).
func AddCreatorRefusal(actor, target Membership, visibility string, now, endsAt time.Time) (noop bool, err error) {
	role := RoleOf(actor)
	if err := RequireView(role, visibility); err != nil {
		return false, err
	}
	if !CanAddCreator(role) {
		return false, apperr.New(apperr.CodeForbidden, nil)
	}
	switch {
	case target.Creator:
		return true, nil
	case target.Participant != ParticipantActive:
		return false, apperr.New(apperr.CodeNotActiveParticipant, nil)
	case !now.Before(endsAt):
		return false, apperr.New(apperr.CodeChallengeEnded, nil)
	default:
		return false, nil
	}
}
