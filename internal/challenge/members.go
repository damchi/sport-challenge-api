package challenge

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
)

// MemberChange is what an admin change of a member wrote, for the journal:
// the changed fields only, before (Old, nil when the participation was
// created) and after (New); both empty when nothing changed.
type MemberChange struct {
	Old map[string]any
	New map[string]any
}

// The journal fields of a member.
const (
	memberStatus  = "status"
	memberCreator = "creator"
)

// noChange is the MemberChange of an admin change that wrote nothing.
func noChange() MemberChange {
	return MemberChange{Old: map[string]any{}, New: map[string]any{}}
}

// The admin use cases below change the members of a challenge as actor, an
// admin (AdminRefusal, 404 not_found otherwise), at any time (README
// §2.12). Each one runs in the transaction ctx carries (admin.Corrections,
// which took the week lock, then the accounts of the participants, first)
// and judges nothing: the caller records the change and recomputes the
// challenge in the same transaction. Each one first locks the row of the
// account it touches (LockAccount), before the challenge, in the order of
// an account deletion. A missing or deleted challenge answers 404
// challenge_not_found.

// AdminAddParticipant makes userID an active participant of challenge id,
// registration closed or challenge finished (AdminAddParticipantRefusal):
// an unknown or deleted account answers 404 user_not_found.
func (s *Service) AdminAddParticipant(ctx context.Context, actor auth.Actor, id, userID string) (MemberChange, error) {
	if err := s.lockAccount(ctx, actor, userID, apperr.CodeUserNotFound); err != nil {
		return MemberChange{}, err
	}
	// Read once the account is locked: its profile cannot change meanwhile.
	complete, err := s.profiles.ProfileComplete(ctx, userID)
	if err != nil {
		return MemberChange{}, fmt.Errorf("check profile: %w", err)
	}
	// Join takes the account FOR SHARE again: weaker than the lock this
	// transaction holds, it does not wait. The instant of the join is read
	// once the challenge is locked, as for a join by its user.
	err = s.repo.Join(ctx, id, userID, s.clock.Now(), func(_ Challenge, m Membership) (time.Time, error) {
		if err := AdminAddParticipantRefusal(m, complete); err != nil {
			return time.Time{}, err
		}
		return s.clock.Now(), nil
	})
	switch {
	case errors.Is(err, errAlreadyParticipant):
		return MemberChange{}, apperr.New(apperr.CodeAlreadyParticipant, nil)
	case errors.Is(err, errAccountDeleted):
		return MemberChange{}, apperr.New(apperr.CodeUserNotFound, nil)
	}
	if err := challengeErr(err); err != nil {
		return MemberChange{}, err
	}
	return MemberChange{New: map[string]any{memberStatus: ParticipantActive}}, nil
}

// AdminReinstateParticipant cancels the abandon or the exclusion of userID
// in challenge id (AdminReinstateRefusal): the participation is active
// again, its abandon forgotten. An active participant changes nothing.
func (s *Service) AdminReinstateParticipant(ctx context.Context, actor auth.Actor, id, userID string) (MemberChange, error) {
	if err := s.lockAccount(ctx, actor, userID, apperr.CodeNotFound); err != nil {
		return MemberChange{}, err
	}
	out := noChange()
	err := s.repo.Reinstate(ctx, id, userID, s.clock.Now(), func(_ Challenge, m Membership) (bool, error) {
		noop, err := AdminReinstateRefusal(m)
		if err == nil && !noop {
			out = MemberChange{Old: map[string]any{memberStatus: ParticipantAbandoned}, New: map[string]any{memberStatus: ParticipantActive}}
		}
		return noop, err
	})
	if err := challengeErr(err); err != nil {
		return MemberChange{}, err
	}
	return out, nil
}

// AdminExcludeParticipant makes userID abandon challenge id
// (AdminExcludeRefusal), a creator included, who keeps the creator role as
// one who abandons does. Someone already out changes nothing.
func (s *Service) AdminExcludeParticipant(ctx context.Context, actor auth.Actor, id, userID string) (MemberChange, error) {
	if err := s.lockAccount(ctx, actor, userID, apperr.CodeNotFound); err != nil {
		return MemberChange{}, err
	}
	out := noChange()
	err := s.repo.Abandon(ctx, id, userID, s.clock.Now(), func(_ Challenge, m Membership) (bool, time.Time, error) {
		noop, err := AdminExcludeRefusal(m)
		if err == nil && !noop {
			out = MemberChange{Old: map[string]any{memberStatus: ParticipantActive}, New: map[string]any{memberStatus: ParticipantAbandoned}}
		}
		return noop, s.clock.Now(), err
	})
	if err := challengeErr(err); err != nil {
		return MemberChange{}, err
	}
	return out, nil
}

// AdminAddCreator makes userID, a participant active or not, a creator of
// challenge id, the challenge finished included (AdminAddCreatorRefusal):
// someone who never joined, or a deleted account, answers 422
// not_active_participant. A creator already changes nothing.
func (s *Service) AdminAddCreator(ctx context.Context, actor auth.Actor, id, userID string) (MemberChange, error) {
	if err := s.lockAccount(ctx, actor, userID, apperr.CodeNotActiveParticipant); err != nil {
		return MemberChange{}, err
	}
	out := noChange()
	// The membership of the admin is of no use: the rule reads the target's.
	err := s.repo.AddCreator(ctx, id, actor.ID, userID, s.clock.Now(), func(_ Challenge, _, target Membership) (bool, error) {
		noop, err := AdminAddCreatorRefusal(target)
		if err == nil && !noop {
			out = MemberChange{Old: map[string]any{memberCreator: false}, New: map[string]any{memberCreator: true}}
		}
		return noop, err
	})
	if err := challengeErr(err); err != nil {
		return MemberChange{}, err
	}
	return out, nil
}

// AdminRemoveCreator removes the creator role of userID in challenge id
// (AdminRemoveCreatorRefusal), the participation left as it is; the last
// creator answers 422 last_creator. A participant who is no creator
// changes nothing.
func (s *Service) AdminRemoveCreator(ctx context.Context, actor auth.Actor, id, userID string) (MemberChange, error) {
	if err := s.lockAccount(ctx, actor, userID, apperr.CodeNotFound); err != nil {
		return MemberChange{}, err
	}
	out := noChange()
	err := s.repo.RemoveCreator(ctx, id, userID, s.clock.Now(), func(c Challenge, m Membership, others int) (bool, error) {
		noop, err := AdminRemoveCreatorRefusal(m, others, c.ID)
		if err == nil && !noop {
			out = MemberChange{Old: map[string]any{memberCreator: true}, New: map[string]any{memberCreator: false}}
		}
		return noop, err
	})
	if err := challengeErr(err); err != nil {
		return MemberChange{}, err
	}
	return out, nil
}

// lockAccount refuses actor unless an admin (AdminRefusal), then locks the
// row of account userID (Repository.LockAccount): an unknown or deleted
// account answers missing, with no other detail.
func (s *Service) lockAccount(ctx context.Context, actor auth.Actor, userID string, missing apperr.Code) error {
	if err := AdminRefusal(actor.Admin); err != nil {
		return err
	}
	err := s.repo.LockAccount(ctx, userID)
	if errors.Is(err, errAccountDeleted) {
		return apperr.New(missing, nil)
	}
	return err
}

// challengeErr turns errChallengeNotFound into 404 challenge_not_found;
// any other error, nil included, is returned as is.
func challengeErr(err error) error {
	if errors.Is(err, errChallengeNotFound) {
		return apperr.New(apperr.CodeChallengeNotFound, nil)
	}
	return err
}
