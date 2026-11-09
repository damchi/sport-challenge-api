package challenge

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
)

func TestRoleOf(t *testing.T) {
	tests := []struct {
		name string
		m    Membership
		want Role
	}{
		{name: "nothing: outsider", m: Membership{}, want: RoleOutsider},
		{name: "invited", m: Membership{Invited: true}, want: RoleInvited},
		{name: "active participant", m: Membership{Participant: ParticipantActive}, want: RoleParticipant},
		{name: "abandoned participant", m: Membership{Participant: ParticipantAbandoned}, want: RoleAbandoned},
		{name: "creator", m: Membership{Creator: true, Participant: ParticipantActive}, want: RoleCreator},
		// A creator stays a creator even after abandoning: they can still
		// delete the challenge.
		{name: "creator who abandoned", m: Membership{Creator: true, Participant: ParticipantAbandoned}, want: RoleCreator},
		// An invitation does not outrank a participation.
		{name: "invited and participant", m: Membership{Invited: true, Participant: ParticipantActive}, want: RoleParticipant},
		{name: "invited after abandoning", m: Membership{Invited: true, Participant: ParticipantAbandoned}, want: RoleAbandoned},
		// A status the policy does not know grants nothing (the database
		// CHECK only allows active and abandoned).
		{name: "unknown status", m: Membership{Participant: "excluded"}, want: RoleOutsider},
		{name: "unknown status and invited", m: Membership{Participant: "bogus", Invited: true}, want: RoleInvited},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RoleOf(tt.m); got != tt.want {
				t.Fatalf("RoleOf(%+v) = %v, want %v", tt.m, got, tt.want)
			}
		})
	}
}

// TestCanView follows the rights matrix of README §5.7: a public challenge
// is visible to every logged-in user; a private one to its creators,
// participants (abandoned included) and invited users only.
func TestCanView(t *testing.T) {
	tests := []struct {
		name       string
		role       Role
		visibility string
		want       bool
	}{
		{name: "creator, public", role: RoleCreator, visibility: VisibilityPublic, want: true},
		{name: "participant, public", role: RoleParticipant, visibility: VisibilityPublic, want: true},
		{name: "abandoned, public", role: RoleAbandoned, visibility: VisibilityPublic, want: true},
		{name: "invited, public", role: RoleInvited, visibility: VisibilityPublic, want: true},
		{name: "outsider, public", role: RoleOutsider, visibility: VisibilityPublic, want: true},
		{name: "creator, private", role: RoleCreator, visibility: VisibilityPrivate, want: true},
		{name: "participant, private", role: RoleParticipant, visibility: VisibilityPrivate, want: true},
		{name: "abandoned, private", role: RoleAbandoned, visibility: VisibilityPrivate, want: true},
		{name: "invited, private", role: RoleInvited, visibility: VisibilityPrivate, want: true},
		{name: "outsider, private", role: RoleOutsider, visibility: VisibilityPrivate, want: false},
		{name: "creator, unlisted", role: RoleCreator, visibility: "unlisted", want: false},
		{name: "participant, unlisted", role: RoleParticipant, visibility: "unlisted", want: false},
		{name: "abandoned, unlisted", role: RoleAbandoned, visibility: "unlisted", want: false},
		{name: "invited, unlisted", role: RoleInvited, visibility: "unlisted", want: false},
		{name: "outsider, unlisted", role: RoleOutsider, visibility: "unlisted", want: false},
		{name: "creator, empty", role: RoleCreator, visibility: "", want: false},
		{name: "outsider, empty", role: RoleOutsider, visibility: "", want: false},
		{name: "role 99, private", role: Role(99), visibility: VisibilityPrivate, want: false},
		{name: "role -1, private", role: Role(-1), visibility: VisibilityPrivate, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanView(tt.role, tt.visibility); got != tt.want {
				t.Fatalf("CanView(%v, %q) = %v, want %v", tt.role, tt.visibility, got, tt.want)
			}
		})
	}
}

func TestRequireView(t *testing.T) {
	tests := []struct {
		name       string
		role       Role
		visibility string
		wantFound  bool
	}{
		{name: "outsider, public", role: RoleOutsider, visibility: VisibilityPublic, wantFound: true},
		{name: "invited, private", role: RoleInvited, visibility: VisibilityPrivate, wantFound: true},
		{name: "creator, private", role: RoleCreator, visibility: VisibilityPrivate, wantFound: true},
		{name: "abandoned, private (still reads it)", role: RoleAbandoned, visibility: VisibilityPrivate, wantFound: true},
		// Forbidden: not found, never forbidden, so the existence of the
		// challenge is not revealed.
		{name: "outsider, private", role: RoleOutsider, visibility: VisibilityPrivate},
		{name: "creator, unknown visibility", role: RoleCreator, visibility: "unlisted"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RequireView(tt.role, tt.visibility)
			if tt.wantFound {
				if err != nil {
					t.Fatalf("RequireView() = %v, want nil", err)
				}
				return
			}
			var appErr *apperr.Error
			if !errors.As(err, &appErr) || appErr.Code != apperr.CodeChallengeNotFound || appErr.Status != http.StatusNotFound {
				t.Fatalf("RequireView() = %v, want 404 challenge_not_found", err)
			}
		})
	}
}

// TestCanEdit: creators only (README §5.7), every other role forbidden.
func TestCanEdit(t *testing.T) {
	for role, want := range map[Role]bool{
		RoleCreator:     true,
		RoleParticipant: false,
		RoleAbandoned:   false,
		RoleInvited:     false,
		RoleOutsider:    false,
		Role(99):        false,
	} {
		if got := CanEdit(role); got != want {
			t.Errorf("CanEdit(%d) = %v, want %v", role, got, want)
		}
	}
}

func TestRequireEdit(t *testing.T) {
	if err := RequireEdit(RoleCreator); err != nil {
		t.Fatalf("creator refused: %v", err)
	}
	err := RequireEdit(RoleParticipant)
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != apperr.CodeForbidden || appErr.Status != http.StatusForbidden {
		t.Fatalf("RequireEdit(participant) = %v, want 403 forbidden", err)
	}
}

var (
	outsider         = Membership{}
	invited          = Membership{Invited: true}
	active           = Membership{Participant: ParticipantActive}
	abandoned        = Membership{Participant: ParticipantAbandoned}
	creatorActive    = Membership{Creator: true, Participant: ParticipantActive}
	creatorAbandoned = Membership{Creator: true, Participant: ParticipantAbandoned}
	creatorNotJoined = Membership{Creator: true}
	unknownStatus    = Membership{Participant: "bogus"}
)

// TestJoinRefusal gives the reason of every refusal, in order: not visible
// (404), already in (409), abandoned for good (422), closed from the exact
// end of week 1 (422). CanJoin is the same rule without the reason.
func TestJoinRefusal(t *testing.T) {
	deadline := time.Date(2026, 3, 9, 4, 0, 0, 0, time.UTC)
	before, at := deadline.Add(-time.Second), deadline
	tests := []struct {
		name       string
		m          Membership
		visibility string
		now        time.Time
		want       apperr.Code // empty: may join
	}{
		{name: "outsider, public, one second before", m: outsider, visibility: VisibilityPublic, now: before},
		{name: "invited, public", m: invited, visibility: VisibilityPublic, now: before},
		{name: "invited, private", m: invited, visibility: VisibilityPrivate, now: before},
		{name: "unknown status counts as outsider", m: unknownStatus, visibility: VisibilityPublic, now: before},
		{name: "outsider, private: not found", m: outsider, visibility: VisibilityPrivate, now: before, want: apperr.CodeChallengeNotFound},
		{name: "outsider, private, after the deadline: still not found", m: outsider, visibility: VisibilityPrivate, now: at, want: apperr.CodeChallengeNotFound},
		{name: "unknown visibility", m: outsider, visibility: "unlisted", now: before, want: apperr.CodeChallengeNotFound},
		{name: "active participant", m: active, visibility: VisibilityPublic, now: before, want: apperr.CodeAlreadyParticipant},
		{name: "active, after the deadline", m: active, visibility: VisibilityPublic, now: at, want: apperr.CodeAlreadyParticipant},
		{name: "active creator", m: creatorActive, visibility: VisibilityPrivate, now: before, want: apperr.CodeAlreadyParticipant},
		{name: "creator without participation", m: creatorNotJoined, visibility: VisibilityPublic, now: before, want: apperr.CodeAlreadyParticipant},
		{name: "abandoned: final", m: abandoned, visibility: VisibilityPublic, now: before, want: apperr.CodeParticipationAbandoned},
		{name: "abandoned, private, after the deadline", m: abandoned, visibility: VisibilityPrivate, now: at, want: apperr.CodeParticipationAbandoned},
		{name: "creator who abandoned: final too", m: creatorAbandoned, visibility: VisibilityPublic, now: before, want: apperr.CodeParticipationAbandoned},
		{name: "at the exact deadline", m: outsider, visibility: VisibilityPublic, now: at, want: apperr.CodeRegistrationClosed},
		{name: "invited, private, at the deadline", m: invited, visibility: VisibilityPrivate, now: at, want: apperr.CodeRegistrationClosed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := JoinRefusal(tt.m, tt.visibility, tt.now, deadline)
			if can := CanJoin(tt.m, tt.visibility, tt.now, deadline); can != (tt.want == "") {
				t.Fatalf("CanJoin() = %v, want %v", can, tt.want == "")
			}
			if tt.want == "" {
				if err != nil {
					t.Fatalf("JoinRefusal() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, apperr.New(tt.want, nil)) {
				t.Fatalf("JoinRefusal() = %v, want %s", err, tt.want)
			}
		})
	}
}

// TestAbandonRefusal: an active participant may abandon until the end of
// the challenge; abandoning again changes nothing; a hidden challenge is not
// found, a visible one without participation has nothing to abandon.
func TestAbandonRefusal(t *testing.T) {
	endsAt := time.Date(2026, 3, 23, 4, 0, 0, 0, time.UTC)
	before, at := endsAt.Add(-time.Second), endsAt
	tests := []struct {
		name       string
		m          Membership
		visibility string
		now        time.Time
		wantNoop   bool
		want       apperr.Code // empty: abandon
	}{
		{name: "active, one second before the end", m: active, visibility: VisibilityPublic, now: before},
		{name: "active creator: keeps the creator role", m: creatorActive, visibility: VisibilityPrivate, now: before},
		{name: "already abandoned: nothing to do", m: abandoned, visibility: VisibilityPublic, now: before, wantNoop: true},
		{name: "already abandoned, after the end", m: abandoned, visibility: VisibilityPublic, now: at, wantNoop: true},
		{name: "already abandoned a private challenge: still visible, nothing to do", m: abandoned, visibility: VisibilityPrivate, now: before, wantNoop: true},
		{name: "creator who abandoned: nothing to do", m: creatorAbandoned, visibility: VisibilityPublic, now: before, wantNoop: true},
		{name: "at the exact end", m: active, visibility: VisibilityPublic, now: at, want: apperr.CodeChallengeEnded},
		{name: "outsider of a public challenge", m: outsider, visibility: VisibilityPublic, now: before, want: apperr.CodeNotFound},
		{name: "invited, never joined", m: invited, visibility: VisibilityPrivate, now: before, want: apperr.CodeNotFound},
		{name: "creator without participation", m: creatorNotJoined, visibility: VisibilityPublic, now: before, want: apperr.CodeNotFound},
		{name: "outsider of a private challenge: hidden", m: outsider, visibility: VisibilityPrivate, now: before, want: apperr.CodeChallengeNotFound},
		{name: "unknown status: no participation", m: unknownStatus, visibility: VisibilityPublic, now: before, want: apperr.CodeNotFound},
		{name: "unknown visibility: hidden", m: active, visibility: "unlisted", now: before, want: apperr.CodeChallengeNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			noop, err := AbandonRefusal(tt.m, tt.visibility, tt.now, endsAt)
			if noop != tt.wantNoop {
				t.Fatalf("noop = %v, want %v", noop, tt.wantNoop)
			}
			if tt.want == "" {
				if err != nil {
					t.Fatalf("AbandonRefusal() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, apperr.New(tt.want, nil)) {
				t.Fatalf("AbandonRefusal() = %v, want %s", err, tt.want)
			}
		})
	}
}

// TestCanExclude: creators only (README §5.7), every other role forbidden.
func TestCanExclude(t *testing.T) {
	for role, want := range map[Role]bool{
		RoleCreator: true, RoleParticipant: false, RoleAbandoned: false,
		RoleInvited: false, RoleOutsider: false, Role(99): false,
	} {
		if got := CanExclude(role); got != want {
			t.Errorf("CanExclude(%d) = %v, want %v", role, got, want)
		}
	}
}

// TestExcludeRefusal checks the actor, then the target: hidden challenge
// (404), not a creator (403), creator target (422), no participation (404),
// already out (nothing to do), finished (422).
func TestExcludeRefusal(t *testing.T) {
	endsAt := time.Date(2026, 3, 23, 4, 0, 0, 0, time.UTC)
	before, at := endsAt.Add(-time.Second), endsAt
	tests := []struct {
		name       string
		actor      Membership
		target     Membership
		visibility string
		now        time.Time
		wantNoop   bool
		want       apperr.Code // empty: exclude
	}{
		{name: "creator excludes an active participant", actor: creatorActive, target: active, visibility: VisibilityPublic, now: before},
		{name: "creator who abandoned keeps the right", actor: creatorAbandoned, target: active, visibility: VisibilityPrivate, now: before},
		{name: "target already abandoned: nothing to do", actor: creatorActive, target: abandoned, visibility: VisibilityPublic, now: before, wantNoop: true},
		{name: "target already abandoned, after the end", actor: creatorActive, target: abandoned, visibility: VisibilityPublic, now: at, wantNoop: true},
		{name: "at the exact end", actor: creatorActive, target: active, visibility: VisibilityPublic, now: at, want: apperr.CodeChallengeEnded},
		{name: "target is a creator", actor: creatorActive, target: creatorActive, visibility: VisibilityPublic, now: before, want: apperr.CodeCannotExcludeCreator},
		{name: "target is a creator who abandoned", actor: creatorActive, target: creatorAbandoned, visibility: VisibilityPublic, now: before, want: apperr.CodeCannotExcludeCreator},
		{name: "target never joined", actor: creatorActive, target: outsider, visibility: VisibilityPublic, now: before, want: apperr.CodeNotFound},
		{name: "participant: forbidden", actor: active, target: active, visibility: VisibilityPublic, now: before, want: apperr.CodeForbidden},
		{name: "outsider of a public challenge: forbidden", actor: outsider, target: active, visibility: VisibilityPublic, now: before, want: apperr.CodeForbidden},
		{name: "outsider of a private challenge: hidden", actor: outsider, target: active, visibility: VisibilityPrivate, now: before, want: apperr.CodeChallengeNotFound},
		{name: "abandoned participant: forbidden", actor: abandoned, target: active, visibility: VisibilityPublic, now: before, want: apperr.CodeForbidden},
		{name: "invited, private: forbidden", actor: invited, target: active, visibility: VisibilityPrivate, now: before, want: apperr.CodeForbidden},
		{name: "participant, private: forbidden", actor: active, target: active, visibility: VisibilityPrivate, now: before, want: apperr.CodeForbidden},
		{name: "target only invited", actor: creatorActive, target: invited, visibility: VisibilityPrivate, now: before, want: apperr.CodeNotFound},
		{name: "target with an unknown status", actor: creatorActive, target: unknownStatus, visibility: VisibilityPublic, now: before, want: apperr.CodeNotFound},
		{name: "unknown visibility: hidden", actor: creatorActive, target: active, visibility: "unlisted", now: before, want: apperr.CodeChallengeNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			noop, err := ExcludeRefusal(tt.actor, tt.target, tt.visibility, tt.now, endsAt)
			if noop != tt.wantNoop {
				t.Fatalf("noop = %v, want %v", noop, tt.wantNoop)
			}
			if tt.want == "" {
				if err != nil {
					t.Fatalf("ExcludeRefusal() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, apperr.New(tt.want, nil)) {
				t.Fatalf("ExcludeRefusal() = %v, want %s", err, tt.want)
			}
		})
	}
}

// TestCanAddCreator: creators only (README §5.7), every other role forbidden.
func TestCanAddCreator(t *testing.T) {
	for role, want := range map[Role]bool{
		RoleCreator: true, RoleParticipant: false, RoleAbandoned: false,
		RoleInvited: false, RoleOutsider: false, Role(99): false,
	} {
		if got := CanAddCreator(role); got != want {
			t.Errorf("CanAddCreator(%d) = %v, want %v", role, got, want)
		}
	}
}

// TestAddCreatorRefusal checks the actor, then the target: hidden (404), not
// a creator (403), already a creator (nothing to do), not an active
// participant (422), finished (422).
func TestAddCreatorRefusal(t *testing.T) {
	endsAt := time.Date(2026, 3, 23, 4, 0, 0, 0, time.UTC)
	before, at := endsAt.Add(-time.Second), endsAt
	tests := []struct {
		name       string
		actor      Membership
		target     Membership
		visibility string
		now        time.Time
		wantNoop   bool
		want       apperr.Code // empty: add
	}{
		{name: "creator adds an active participant", actor: creatorActive, target: active, visibility: VisibilityPublic, now: before},
		{name: "creator who abandoned keeps the right", actor: creatorAbandoned, target: active, visibility: VisibilityPrivate, now: before},
		{name: "already a creator: nothing to do", actor: creatorActive, target: creatorActive, visibility: VisibilityPublic, now: before, wantNoop: true},
		{name: "already a creator, after the end", actor: creatorActive, target: creatorAbandoned, visibility: VisibilityPublic, now: at, wantNoop: true},
		{name: "target abandoned", actor: creatorActive, target: abandoned, visibility: VisibilityPublic, now: before, want: apperr.CodeNotActiveParticipant},
		{name: "target never joined", actor: creatorActive, target: outsider, visibility: VisibilityPublic, now: before, want: apperr.CodeNotActiveParticipant},
		{name: "target only invited", actor: creatorActive, target: invited, visibility: VisibilityPrivate, now: before, want: apperr.CodeNotActiveParticipant},
		{name: "at the exact end", actor: creatorActive, target: active, visibility: VisibilityPublic, now: at, want: apperr.CodeChallengeEnded},
		{name: "participant: forbidden", actor: active, target: active, visibility: VisibilityPublic, now: before, want: apperr.CodeForbidden},
		{name: "abandoned: forbidden", actor: abandoned, target: active, visibility: VisibilityPrivate, now: before, want: apperr.CodeForbidden},
		{name: "outsider of a public challenge: forbidden", actor: outsider, target: active, visibility: VisibilityPublic, now: before, want: apperr.CodeForbidden},
		{name: "outsider of a private challenge: hidden", actor: outsider, target: active, visibility: VisibilityPrivate, now: before, want: apperr.CodeChallengeNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			noop, err := AddCreatorRefusal(tt.actor, tt.target, tt.visibility, tt.now, endsAt)
			if noop != tt.wantNoop {
				t.Fatalf("noop = %v, want %v", noop, tt.wantNoop)
			}
			if tt.want == "" {
				if err != nil {
					t.Fatalf("AddCreatorRefusal() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, apperr.New(tt.want, nil)) {
				t.Fatalf("AddCreatorRefusal() = %v, want %s", err, tt.want)
			}
		})
	}
}

// TestDeleteRefusal: any creator may delete, at any time (README §2.1),
// even after abandoning; a visible non-creator is forbidden, a hidden
// challenge not found.
func TestDeleteRefusal(t *testing.T) {
	tests := []struct {
		name       string
		m          Membership
		visibility string
		want       apperr.Code // empty: delete
	}{
		{name: "creator", m: creatorActive, visibility: VisibilityPublic},
		{name: "creator who abandoned", m: creatorAbandoned, visibility: VisibilityPrivate},
		{name: "participant: forbidden", m: active, visibility: VisibilityPublic, want: apperr.CodeForbidden},
		{name: "abandoned: forbidden", m: abandoned, visibility: VisibilityPrivate, want: apperr.CodeForbidden},
		{name: "invited: forbidden", m: invited, visibility: VisibilityPrivate, want: apperr.CodeForbidden},
		{name: "outsider of a public challenge: forbidden", m: outsider, visibility: VisibilityPublic, want: apperr.CodeForbidden},
		{name: "outsider of a private challenge: hidden", m: outsider, visibility: VisibilityPrivate, want: apperr.CodeChallengeNotFound},
		{name: "unknown visibility: hidden", m: creatorActive, visibility: "unlisted", want: apperr.CodeChallengeNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := DeleteRefusal(tt.m, tt.visibility)
			can := CanDelete(RoleOf(tt.m))
			if tt.want == "" && !can {
				t.Fatal("CanDelete() = false for an allowed delete")
			}
			if tt.want == apperr.CodeForbidden && can {
				t.Fatal("CanDelete() = true for a forbidden delete")
			}
			if tt.want == "" {
				if err != nil {
					t.Fatalf("DeleteRefusal() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, apperr.New(tt.want, nil)) {
				t.Fatalf("DeleteRefusal() = %v, want %s", err, tt.want)
			}
		})
	}
}

func TestParticipantsRefusal(t *testing.T) {
	tests := []struct {
		name       string
		m          Membership
		visibility string
		want       apperr.Code // empty: listed
	}{
		{name: "outsider of a public challenge", m: outsider, visibility: VisibilityPublic},
		{name: "creator of a private challenge", m: creatorActive, visibility: VisibilityPrivate},
		{name: "creator who abandoned", m: creatorAbandoned, visibility: VisibilityPrivate},
		{name: "participant of a private challenge", m: active, visibility: VisibilityPrivate},
		{name: "abandoned participant of a private challenge", m: abandoned, visibility: VisibilityPrivate},
		{name: "invited to a private challenge", m: invited, visibility: VisibilityPrivate},
		{name: "outsider of a private challenge: hidden", m: outsider, visibility: VisibilityPrivate, want: apperr.CodeChallengeNotFound},
		{name: "unknown visibility: hidden", m: creatorActive, visibility: "unlisted", want: apperr.CodeChallengeNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ParticipantsRefusal(tt.m, tt.visibility)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("ParticipantsRefusal() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, apperr.New(tt.want, nil)) {
				t.Fatalf("ParticipantsRefusal() = %v, want %s", err, tt.want)
			}
		})
	}
}
