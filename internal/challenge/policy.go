package challenge

import (
	"slices"
	"strings"
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
	// Invited is true when an invitation of the challenge linked to the user
	// (opened while logged in) is valid: not expired, not revoked. The link is
	// a bearer token: anyone logged in who holds it may open it, and only the
	// opening, recorded, makes them invited.
	Invited bool
	// AccountDeleted is true when the account of the user whose membership
	// this is (the target, when read for one) was deleted (anonymized): it is
	// then shown nowhere, its participation included.
	AccountDeleted bool
	// AbandonedAt is when the participation was abandoned (left or
	// excluded), nil otherwise. No rule here reads it: module activity keeps
	// locked the weeks locked by then.
	AbandonedAt *time.Time
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
	// RoleAdmin is the role an admin reads a challenge with when they are
	// not one of its creators (ReaderRole): a creator's reading, of every
	// challenge, and no write at all (README §2.12). It is not a membership:
	// RoleOf never returns it, and no write rule grants it anything. It
	// comes after RoleCreator only by declaration order: never compare roles
	// with < or >, list them (an allowlist), or RoleAdmin would write.
	RoleAdmin
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

// ReaderRole returns the role a user with membership m reads a challenge
// with, admin telling whether their token carries the admin permission: an
// admin reads as a creator does (RoleAdmin), unless they are one; anyone
// else reads with RoleOf(m). Only the read rules take it: the write rules
// take RoleOf, so the admin permission grants no write through them.
func ReaderRole(m Membership, admin bool) Role {
	role := RoleOf(m)
	if admin && role != RoleCreator {
		return RoleAdmin
	}
	return role
}

// CanView reports whether role may see a challenge of that visibility and
// its leaderboard (README §5.7): every logged-in user for a public challenge;
// creators, participants (abandoned included), invited users and admins
// for a private one.
func CanView(role Role, visibility string) bool {
	switch visibility {
	case VisibilityPublic:
		return true
	case VisibilityPrivate:
		// An allowlist: a role this switch does not know is never let in.
		switch role {
		case RoleCreator, RoleParticipant, RoleAbandoned, RoleInvited, RoleAdmin:
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

// ParticipantsRefusal tells why a user with membership m, an admin or not,
// may not list the participants of a challenge of that visibility, or nil
// if they may: the list is visible to whoever sees the challenge, and hidden
// (404) otherwise.
func ParticipantsRefusal(m Membership, admin bool, visibility string) error {
	return RequireView(ReaderRole(m, admin), visibility)
}

// CanViewActivities reports whether role may see the activities of a
// participant (README §5.7): the participants of the challenge, abandoned or
// excluded ones included, its creators and the admins. An invited user or an
// outsider sees the leaderboard only.
func CanViewActivities(role Role) bool {
	// An allowlist: a role this switch does not know is never let in.
	switch role {
	case RoleCreator, RoleParticipant, RoleAbandoned, RoleAdmin:
		return true
	default:
		return false
	}
}

// ReadsActivities reports whether a reader of a challenge with role, an
// admin or not, may see the activities of its participants (CanViewActivities,
// an admin reading as RoleAdmin): what the leaderboard needs to show the
// values behind the points of the points category.
func ReadsActivities(role Role, admin bool) bool {
	return admin || CanViewActivities(role)
}

// ActivitiesRefusal tells why a user with membership actor, an admin or
// not, may not see the activities of target in a challenge of that
// visibility, or nil if they may. A challenge the actor may not see is not
// found (404 challenge_not_found); a visible one shows its leaderboard only
// to anyone CanViewActivities lets out (404 not_found); a target who never
// joined, or whose account was deleted, has no activities there (404
// not_found).
func ActivitiesRefusal(actor Membership, admin bool, target Membership, visibility string) error {
	if err := AllActivitiesRefusal(actor, admin, visibility); err != nil {
		return err
	}
	if target.AccountDeleted || (target.Participant != ParticipantActive && target.Participant != ParticipantAbandoned) {
		return apperr.New(apperr.CodeNotFound, nil)
	}
	return nil
}

// AllActivitiesRefusal tells why a user with membership actor, an admin or
// not, may not see the activities of the participants of a challenge of
// that visibility, or nil if they may: a challenge the actor may not see is
// not found (404 challenge_not_found); anyone else CanViewActivities
// excludes sees its leaderboard only (404 not_found).
func AllActivitiesRefusal(actor Membership, admin bool, visibility string) error {
	role := ReaderRole(actor, admin)
	if err := RequireView(role, visibility); err != nil {
		return err
	}
	if !CanViewActivities(role) {
		return apperr.New(apperr.CodeNotFound, nil)
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

// CanInvite reports whether role may invite people to a challenge (README
// §5.7): its creators, even after abandoning; its active participants only
// when the creator allowed it at creation (membersCanInvite).
func CanInvite(role Role, membersCanInvite bool) bool {
	// An allowlist: a role this switch does not know is never let in.
	switch role {
	case RoleCreator:
		return true
	case RoleParticipant:
		return membersCanInvite
	default:
		return false
	}
}

// InviteRefusal is the rule to create an invitation: a challenge the user
// may not see is not found (404 challenge_not_found), only CanInvite may
// invite (403 forbidden), and none from deadline, the end of week 1 (422
// registration_closed): an invitation expires then, as nobody may join any
// more.
func InviteRefusal(m Membership, visibility string, membersCanInvite bool, now, deadline time.Time) error {
	role := RoleOf(m)
	if err := RequireView(role, visibility); err != nil {
		return err
	}
	if !CanInvite(role, membersCanInvite) {
		return apperr.New(apperr.CodeForbidden, nil)
	}
	if !now.Before(deadline) {
		return apperr.New(apperr.CodeRegistrationClosed, nil)
	}
	return nil
}

// CanEdit reports whether role may edit the title and the description of a
// challenge (README §5.7): its creators only.
func CanEdit(role Role) bool {
	return role == RoleCreator
}

// AdminRefusal lets an actor with the admin permission of their access
// token change any setting of a challenge (README §2.12); anyone else gets
// 404 not_found, as on every admin route.
func AdminRefusal(admin bool) error {
	if !admin {
		return apperr.New(apperr.CodeNotFound, nil)
	}
	return nil
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

// DeletionAbandons reports whether deleting the account of a user with
// membership m makes them abandon a challenge ending at endsAt: an active
// participation in a challenge not finished at now. In a finished challenge
// it is not abandoned: like every other row of the account, the deletion
// erases it, and every view leaves the deleted account out (AccountDeleted).
func DeletionAbandons(m Membership, now, endsAt time.Time) bool {
	return m.Participant == ParticipantActive && now.Before(endsAt)
}

// DeletionRefusal is the rule of the last creator (README §2.2): an account
// may not be deleted while it is the last creator with an account of
// challenges not finished, lastCreatorOf (their ids), or nobody could manage
// them. The user first adds another creator or deletes the challenge: 422
// last_creator, listing the ids sorted. Nil when lastCreatorOf is empty.
func DeletionRefusal(lastCreatorOf []string) error {
	if len(lastCreatorOf) == 0 {
		return nil
	}
	ids := slices.Sorted(slices.Values(lastCreatorOf))
	err := apperr.New(apperr.CodeLastCreator, nil)
	err.Params = map[string]string{apperr.ChallengesParam: strings.Join(ids, ",")}
	return err
}

// CanExclude reports whether role may exclude a participant (README §5.7):
// creators only, even after abandoning.
func CanExclude(role Role) bool {
	return role == RoleCreator
}

// ExcludeRefusal is the exclusion rule. On the actor: a challenge the actor
// may not see is not found (404 challenge_not_found), and only creators may
// exclude (403 forbidden). On the target: creators cannot be excluded (422
// cannot_exclude_creator), someone who never joined, or whose account was
// deleted, has nothing to lose (404 not_found), someone already out changes
// nothing (noop), and a finished challenge is frozen (422 challenge_ended
// from the exact endsAt).
// An excluded participant is abandoned, shown like any abandon.
func ExcludeRefusal(actor, target Membership, visibility string, now, endsAt time.Time) (noop bool, err error) {
	role := RoleOf(actor)
	if err := RequireView(role, visibility); err != nil {
		return false, err
	}
	if !CanExclude(role) {
		return false, apperr.New(apperr.CodeForbidden, nil)
	}
	if target.AccountDeleted {
		return false, apperr.New(apperr.CodeNotFound, nil)
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
// (422 not_active_participant: every creator is a participant; a deleted
// account is none, creator or not), and a finished challenge is frozen (422
// challenge_ended from the exact endsAt).
func AddCreatorRefusal(actor, target Membership, visibility string, now, endsAt time.Time) (noop bool, err error) {
	role := RoleOf(actor)
	if err := RequireView(role, visibility); err != nil {
		return false, err
	}
	if !CanAddCreator(role) {
		return false, apperr.New(apperr.CodeForbidden, nil)
	}
	switch {
	case target.AccountDeleted:
		return false, apperr.New(apperr.CodeNotActiveParticipant, nil)
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

// The admin rules below judge the target of an admin change of the members
// of a challenge (README §2.12), once AdminRefusal let the actor in: they
// leave aside every time lock (registration closed, challenge finished) and
// every role lock (a creator excluded), never the data (a profile
// complete, a challenge with a creator). A deleted account is none, as on
// the routes of the creators.

// AdminAddParticipantRefusal is the rule for an admin to make target an
// active participant: an unknown or deleted account answers 404
// user_not_found; someone already in, 409 already_participant (a creator
// is a participant); someone who abandoned or was excluded, 422
// participation_abandoned (the admin reinstates them instead); an
// incomplete profile, 422 profile_incomplete, as to join.
func AdminAddParticipantRefusal(target Membership, profileComplete bool) error {
	switch {
	case target.AccountDeleted:
		return apperr.New(apperr.CodeUserNotFound, nil)
	case target.Participant == ParticipantActive:
		return apperr.New(apperr.CodeAlreadyParticipant, nil)
	case target.Participant == ParticipantAbandoned:
		return apperr.New(apperr.CodeParticipationAbandoned, nil)
	case target.Creator:
		return apperr.New(apperr.CodeAlreadyParticipant, nil)
	case !profileComplete:
		return apperr.New(apperr.CodeProfileIncomplete, nil)
	default:
		return nil
	}
}

// AdminReinstateRefusal is the rule for an admin to cancel the abandon or
// the exclusion of target: someone who never joined, or a deleted
// account, has nothing to cancel (404 not_found); an active participant
// changes nothing (noop).
func AdminReinstateRefusal(target Membership) (noop bool, err error) {
	if target.AccountDeleted {
		return false, apperr.New(apperr.CodeNotFound, nil)
	}
	switch target.Participant {
	case ParticipantAbandoned:
		return false, nil
	case ParticipantActive:
		return true, nil
	default:
		return false, apperr.New(apperr.CodeNotFound, nil)
	}
}

// AdminExcludeRefusal is the rule for an admin to exclude target, a
// creator included (who keeps the creator role, as one who abandons):
// someone who never joined, or a deleted account, has nothing to lose (404
// not_found); someone already out changes nothing (noop).
func AdminExcludeRefusal(target Membership) (noop bool, err error) {
	if target.AccountDeleted {
		return false, apperr.New(apperr.CodeNotFound, nil)
	}
	switch target.Participant {
	case ParticipantActive:
		return false, nil
	case ParticipantAbandoned:
		return true, nil
	default:
		return false, apperr.New(apperr.CodeNotFound, nil)
	}
}

// AdminAddCreatorRefusal is the rule for an admin to make target a
// creator: any participant, active or not (a creator who abandoned keeps
// the role); someone who never joined, or a deleted account, is none (422
// not_active_participant: every creator is a participant, the admin adds
// them first); a creator already changes nothing (noop).
func AdminAddCreatorRefusal(target Membership) (noop bool, err error) {
	switch {
	case target.AccountDeleted:
		return false, apperr.New(apperr.CodeNotActiveParticipant, nil)
	case target.Creator:
		return true, nil
	case target.Participant == ParticipantActive, target.Participant == ParticipantAbandoned:
		return false, nil
	default:
		return false, apperr.New(apperr.CodeNotActiveParticipant, nil)
	}
}

// AdminRemoveCreatorRefusal is the rule for an admin to remove the creator
// role of target in challenge challengeID, otherCreators being the number
// of its other creators with an account: someone who never joined, or a
// deleted account, answers 404 not_found; a participant who is no creator
// changes nothing (noop); a challenge always keeps a creator (422
// last_creator, the challenge in params, as for an account deletion).
func AdminRemoveCreatorRefusal(target Membership, otherCreators int, challengeID string) (noop bool, err error) {
	switch {
	case target.AccountDeleted:
		return false, apperr.New(apperr.CodeNotFound, nil)
	case target.Creator:
		if otherCreators == 0 {
			return false, DeletionRefusal([]string{challengeID})
		}
		return false, nil
	case target.Participant == ParticipantActive, target.Participant == ParticipantAbandoned:
		return true, nil
	default:
		return false, apperr.New(apperr.CodeNotFound, nil)
	}
}

// CanDelete reports whether role may delete a challenge (README §5.7): any
// creator, at any time, even after abandoning.
func CanDelete(role Role) bool {
	return role == RoleCreator
}

// DeleteRefusal is the deletion rule: a challenge the user may not see is
// not found (404 challenge_not_found), and only creators may delete it (403
// forbidden), whether it is upcoming, ongoing or finished.
func DeleteRefusal(m Membership, visibility string) error {
	role := RoleOf(m)
	if err := RequireView(role, visibility); err != nil {
		return err
	}
	if !CanDelete(role) {
		return apperr.New(apperr.CodeForbidden, nil)
	}
	return nil
}
