package admin

import (
	"context"

	"github.com/damchi/sport-challenge-api/internal/challenge"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
)

// The journal codes of the admin changes of the members of a challenge.
const (
	ActionParticipantAdd       = "participant.add"
	ActionParticipantReinstate = "participant.reinstate"
	ActionParticipantExclude   = "participant.exclude"
	ActionCreatorAdd           = "creator.add"
	ActionCreatorRemove        = "creator.remove"
)

// ChallengeMembers changes the members of a challenge as an admin, in the
// transaction ctx carries, each change first locking the row of the
// account it touches (module challenge).
type ChallengeMembers interface {
	AdminAddParticipant(ctx context.Context, actor auth.Actor, id, userID string) (challenge.MemberChange, error)
	AdminReinstateParticipant(ctx context.Context, actor auth.Actor, id, userID string) (challenge.MemberChange, error)
	AdminExcludeParticipant(ctx context.Context, actor auth.Actor, id, userID string) (challenge.MemberChange, error)
	AdminAddCreator(ctx context.Context, actor auth.Actor, id, userID string) (challenge.MemberChange, error)
	AdminRemoveCreator(ctx context.Context, actor auth.Actor, id, userID string) (challenge.MemberChange, error)
}

// MemberCorrections are the admin changes of the members of a challenge
// (README §2.12): each one, its journal line (the account as a
// participant, the challenge in challenge_ids) and the recompute of the
// challenge, in one transaction (Corrections.Run), rolled back with dryRun.
type MemberCorrections struct {
	corrections *Corrections
	members     ChallengeMembers
}

// NewMemberCorrections returns MemberCorrections.
func NewMemberCorrections(corrections *Corrections, members ChallengeMembers) *MemberCorrections {
	return &MemberCorrections{corrections: corrections, members: members}
}

// memberChange is one of the use cases of ChallengeMembers.
type memberChange func(ctx context.Context, actor auth.Actor, id, userID string) (challenge.MemberChange, error)

// AddParticipant makes userID an active participant of challenge id, at
// any time.
func (s *MemberCorrections) AddParticipant(ctx context.Context, actor auth.Actor, id, userID, reason string, dryRun bool) ([]ChallengePreview, error) {
	return s.run(ctx, actor, id, userID, ActionParticipantAdd, s.members.AdminAddParticipant, reason, dryRun)
}

// ReinstateParticipant cancels the abandon or the exclusion of userID in
// challenge id.
func (s *MemberCorrections) ReinstateParticipant(ctx context.Context, actor auth.Actor, id, userID, reason string, dryRun bool) ([]ChallengePreview, error) {
	return s.run(ctx, actor, id, userID, ActionParticipantReinstate, s.members.AdminReinstateParticipant, reason, dryRun)
}

// ExcludeParticipant excludes userID from challenge id, a creator included.
func (s *MemberCorrections) ExcludeParticipant(ctx context.Context, actor auth.Actor, id, userID, reason string, dryRun bool) ([]ChallengePreview, error) {
	return s.run(ctx, actor, id, userID, ActionParticipantExclude, s.members.AdminExcludeParticipant, reason, dryRun)
}

// AddCreator makes userID, a participant, a creator of challenge id.
func (s *MemberCorrections) AddCreator(ctx context.Context, actor auth.Actor, id, userID, reason string, dryRun bool) ([]ChallengePreview, error) {
	return s.run(ctx, actor, id, userID, ActionCreatorAdd, s.members.AdminAddCreator, reason, dryRun)
}

// RemoveCreator removes the creator role of userID in challenge id.
func (s *MemberCorrections) RemoveCreator(ctx context.Context, actor auth.Actor, id, userID, reason string, dryRun bool) ([]ChallengePreview, error) {
	return s.run(ctx, actor, id, userID, ActionCreatorRemove, s.members.AdminRemoveCreator, reason, dryRun)
}

// run makes change on userID in challenge id as actor, in Corrections.Run,
// recorded under action.
func (s *MemberCorrections) run(ctx context.Context, actor auth.Actor, id, userID, action string, change memberChange, reason string, dryRun bool) ([]ChallengePreview, error) {
	return s.corrections.Run(ctx, actor, Correction{
		Touched: func(context.Context) ([]string, error) { return []string{id}, nil },
		Apply: func(ctx context.Context) (Entry, error) {
			c, err := change(ctx, actor, id, userID)
			if err != nil {
				return Entry{}, err
			}
			return Entry{
				Action: action, TargetType: TargetParticipant, TargetID: userID,
				OldValue: c.Old, NewValue: c.New,
			}, nil
		},
		Reason: reason,
		DryRun: dryRun,
	})
}
