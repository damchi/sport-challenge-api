package admin

import (
	"context"
	"maps"
	"time"

	"github.com/damchi/sport-challenge-api/internal/challenge"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
)

// The journal codes of the admin changes of a challenge itself.
const (
	ActionChallengeUpdate  = "challenge.update"
	ActionChallengeDelete  = "challenge.delete"
	ActionChallengeRestore = "challenge.restore"
)

// journalDeletedAt is the journal field of the deletion of a challenge.
const journalDeletedAt = "deleted_at"

// journalNamedCreator is the journal field of the creator a restore named:
// the account by its id alone, with what the restore made of it.
const journalNamedCreator = "named_creator"

// ChallengeChanges changes a challenge itself as an admin, in the
// transaction ctx carries (module challenge): its settings, its deletion
// and its restore, each of the last two returning the instant of the
// deletion.
type ChallengeChanges interface {
	AdminUpdate(ctx context.Context, actor auth.Actor, id string, changes challenge.SettingsChanges) (challenge.SettingsChange, error)
	AdminDelete(ctx context.Context, actor auth.Actor, id string) (time.Time, error)
	AdminRestore(ctx context.Context, actor auth.Actor, id string, creatorID *string) (challenge.Restoration, error)
}

// InvitationExpiries moves the expiry of the invitation links of a
// challenge, in the transaction ctx carries (module invitation).
type InvitationExpiries interface {
	Reschedule(ctx context.Context, challengeID string, expiresAt time.Time) error
}

// ChallengeCorrections are the admin changes of a challenge itself.
type ChallengeCorrections struct {
	corrections *Corrections
	challenges  ChallengeChanges
	invitations InvitationExpiries
}

// NewChallengeCorrections returns ChallengeCorrections.
func NewChallengeCorrections(corrections *Corrections, challenges ChallengeChanges, invitations InvitationExpiries) *ChallengeCorrections {
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

// Delete soft-deletes challenge id as actor, an admin, with the effect of a
// deletion by one of its creators (README §2.12): the deletion
// (challenge.Service.AdminDelete) and its journal line, in one transaction
// (Corrections.Run), rolled back with dryRun. The challenge is ranked
// before the deletion only, and not recomputed: the worker judges no
// deleted challenge, and its eliminations and frozen results stay for a
// restore.
func (s *ChallengeCorrections) Delete(ctx context.Context, actor auth.Actor, id, reason string, dryRun bool) ([]ChallengePreview, error) {
	return s.corrections.Run(ctx, actor, Correction{
		Touched: func(context.Context) ([]string, error) { return []string{id}, nil },
		Apply: func(ctx context.Context) (Entry, error) {
			at, err := s.challenges.AdminDelete(ctx, actor, id)
			if err != nil {
				return Entry{}, err
			}
			return Entry{
				Action: ActionChallengeDelete, TargetType: TargetChallenge, TargetID: id,
				OldValue: map[string]any{journalDeletedAt: nil}, NewValue: map[string]any{journalDeletedAt: at},
			}, nil
		},
		Reason:    reason,
		DryRun:    dryRun,
		lifecycle: deletes,
	})
}

// Restore clears the deletion of challenge id as actor, an admin (README
// §2.12): the restore (challenge.Service.AdminRestore), which makes its
// invitation links neither revoked nor expired valid again, its journal
// line and the recompute of the challenge, whose weeks locked while it was
// deleted were judged by no one, in one transaction (Corrections.Run),
// rolled back with dryRun. The challenge is ranked after the restore only.
// creatorID, when given, names a creator in the restore, who is in its
// journal line (journalNamedCreator): required when the challenge would
// come back upcoming or ongoing without one (README §2.12).
func (s *ChallengeCorrections) Restore(ctx context.Context, actor auth.Actor, id string, creatorID *string, reason string, dryRun bool) ([]ChallengePreview, error) {
	return s.corrections.Run(ctx, actor, Correction{
		Touched: func(context.Context) ([]string, error) { return []string{id}, nil },
		Apply: func(ctx context.Context) (Entry, error) {
			r, err := s.challenges.AdminRestore(ctx, actor, id, creatorID)
			if err != nil {
				return Entry{}, err
			}
			entry := Entry{
				Action: ActionChallengeRestore, TargetType: TargetChallenge, TargetID: id,
				OldValue: map[string]any{journalDeletedAt: r.DeletedAt}, NewValue: map[string]any{journalDeletedAt: nil},
			}
			if creatorID != nil {
				entry.OldValue[journalNamedCreator] = namedCreator(*creatorID, r.Creator.Old)
				entry.NewValue[journalNamedCreator] = namedCreator(*creatorID, r.Creator.New)
			}
			return entry, nil
		},
		Reason:    reason,
		DryRun:    dryRun,
		lifecycle: restores,
	})
}

// namedCreator is the journal value of the account userID a restore named:
// its id and the fields of its membership in fields (status, creator).
func namedCreator(userID string, fields map[string]any) map[string]any {
	out := make(map[string]any, len(fields)+1)
	maps.Copy(out, fields)
	out["user_id"] = userID
	return out
}
