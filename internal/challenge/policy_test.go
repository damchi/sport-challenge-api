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

// TestCanJoin follows README §5.7: public challenges to every logged-in
// user, private ones on invitation, until the end of week 1 (refused at the
// exact instant), never twice (abandoning is final).
func TestCanJoin(t *testing.T) {
	deadline := time.Date(2026, 3, 9, 5, 0, 0, 0, time.UTC)
	before, at := deadline.Add(-time.Second), deadline
	tests := []struct {
		name       string
		role       Role
		visibility string
		now        time.Time
		want       bool
	}{
		{name: "outsider, public, before the deadline", role: RoleOutsider, visibility: VisibilityPublic, now: before, want: true},
		{name: "invited, public", role: RoleInvited, visibility: VisibilityPublic, now: before, want: true},
		{name: "invited, private", role: RoleInvited, visibility: VisibilityPrivate, now: before, want: true},
		{name: "outsider, private: not invited", role: RoleOutsider, visibility: VisibilityPrivate, now: before},
		{name: "at the exact deadline", role: RoleOutsider, visibility: VisibilityPublic, now: at},
		{name: "after the deadline", role: RoleInvited, visibility: VisibilityPrivate, now: at.Add(time.Hour)},
		{name: "already a participant", role: RoleParticipant, visibility: VisibilityPublic, now: before},
		{name: "abandoned: final", role: RoleAbandoned, visibility: VisibilityPublic, now: before},
		{name: "creator: already a participant", role: RoleCreator, visibility: VisibilityPublic, now: before},
		{name: "unknown visibility", role: RoleOutsider, visibility: "unlisted", now: before},
		{name: "unknown role", role: Role(99), visibility: VisibilityPublic, now: before},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanJoin(tt.role, tt.visibility, tt.now, deadline); got != tt.want {
				t.Fatalf("CanJoin() = %v, want %v", got, tt.want)
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
