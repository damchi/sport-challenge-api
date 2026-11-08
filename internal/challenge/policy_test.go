package challenge

import (
	"errors"
	"net/http"
	"testing"

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
		role       Role
		visibility string
		want       bool
	}{
		{RoleCreator, VisibilityPublic, true},
		{RoleParticipant, VisibilityPublic, true},
		{RoleAbandoned, VisibilityPublic, true},
		{RoleInvited, VisibilityPublic, true},
		{RoleOutsider, VisibilityPublic, true},

		{RoleCreator, VisibilityPrivate, true},
		{RoleParticipant, VisibilityPrivate, true},
		{RoleAbandoned, VisibilityPrivate, true},
		{RoleInvited, VisibilityPrivate, true},
		{RoleOutsider, VisibilityPrivate, false},

		// An unknown visibility is never visible, whatever the role.
		{RoleCreator, "unlisted", false},
		{RoleParticipant, "unlisted", false},
		{RoleAbandoned, "unlisted", false},
		{RoleInvited, "unlisted", false},
		{RoleOutsider, "unlisted", false},
		{RoleCreator, "", false},
		{RoleOutsider, "", false},

		// A role value RoleOf never returns is never let in.
		{Role(99), VisibilityPrivate, false},
		{Role(-1), VisibilityPrivate, false},
	}
	for _, tt := range tests {
		t.Run(tt.role.String()+" "+tt.visibility, func(t *testing.T) {
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
