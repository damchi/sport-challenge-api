package admin

import (
	"context"
	"time"

	"github.com/damchi/sport-challenge-api/internal/challenge"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
)

// ActionChallengeUpdate is the journal code of a change of the settings of
// a challenge.
const ActionChallengeUpdate = "challenge.update"

// ChallengeSettings changes the settings of a challenge as an admin, in the
// transaction ctx carries (module challenge).
type ChallengeSettings interface {
	AdminUpdate(ctx context.Context, actor auth.Actor, id string, changes challenge.SettingsChanges) (challenge.SettingsChange, error)
}

// InvitationExpiries moves the expiry of the invitation links of a
// challenge, in the transaction ctx carries (module invitation).
type InvitationExpiries interface {
	Reschedule(ctx context.Context, challengeID string, expiresAt time.Time) error
}

// ChallengeCorrections are the admin changes of a challenge itself.
type ChallengeCorrections struct {
	corrections *Corrections
	challenges  ChallengeSettings
	invitations InvitationExpiries
}

// NewChallengeCorrections returns ChallengeCorrections.
func NewChallengeCorrections(corrections *Corrections, challenges ChallengeSettings, invitations InvitationExpiries) *ChallengeCorrections {
	return &ChallengeCorrections{corrections: corrections, challenges: challenges, invitations: invitations}
}

// UpdateSettings changes the settings of challenge id as actor, an admin
// (README §2.12): the change (challenge.Service.AdminUpdate), the expiry of
// its invitation links not revoked moved to the new end of week 1 when it
// moved, its journal line and the recompute of the challenge, in one
// transaction (Corrections.Run), rolled back with dryRun.
func (s *ChallengeCorrections) UpdateSettings(ctx context.Context, actor auth.Actor, id string, changes challenge.SettingsChanges, reason string, dryRun bool) ([]ChallengePreview, error) {
	return s.corrections.Run(ctx, actor, Correction{
		Touched: func(context.Context) ([]string, error) { return []string{id}, nil },
		Apply: func(ctx context.Context) (Entry, error) {
			change, err := s.challenges.AdminUpdate(ctx, actor, id, changes)
			if err != nil {
				return Entry{}, err
			}
			if change.Deadline != nil {
				if err := s.invitations.Reschedule(ctx, id, *change.Deadline); err != nil {
					return Entry{}, err
				}
			}
			return Entry{
				Action: ActionChallengeUpdate, TargetType: TargetChallenge, TargetID: id,
				OldValue: change.Old, NewValue: change.New,
			}, nil
		},
		Reason: reason,
		DryRun: dryRun,
	})
}
