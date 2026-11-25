package challenge

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
)

// AdminDelete soft-deletes challenge id as actor, an admin (AdminRefusal,
// 404 not_found otherwise), at any time and whatever their role, with the
// effect of Delete; it returns the instant of the deletion. It writes in
// the transaction ctx carries (admin.ChallengeCorrections, which took the
// week lock first) and judges nothing: its eliminations and frozen results
// stay for a restore. A missing or deleted challenge answers 404
// challenge_not_found.
func (s *Service) AdminDelete(ctx context.Context, actor auth.Actor, id string) (time.Time, error) {
	if err := AdminRefusal(actor.Admin); err != nil {
		return time.Time{}, err
	}
	// One instant: that of the read of the challenge and of its deletion.
	// The membership of the admin is of no use: no role is required.
	at := s.clock.Now()
	err := s.repo.Delete(ctx, id, actor.ID, at, func(Challenge, Membership) (time.Time, error) {
		return at, nil
	})
	if err := challengeErr(err); err != nil {
		return time.Time{}, err
	}
	return at, nil
}

// Restoration is what AdminRestore did: the instant the challenge had been
// deleted at, and, when it named a creator, what it wrote of them, for the
// journal (MemberChange; both maps nil when no creator was named).
type Restoration struct {
	DeletedAt time.Time
	Creator   MemberChange
}

// AdminRestore clears the deletion of challenge id as actor, an admin
// (AdminRefusal, 404 not_found otherwise), at any time (README §2.12): the
// challenge is found again, its invitation links neither revoked nor
// expired valid again. A challenge always keeps a creator: one that would
// come back upcoming or ongoing without any creator with an account answers
// 422 no_creator (AdminNoCreatorRefusal) unless creatorID names one; a
// finished one is restored without (README §2.2). The account creatorID,
// when given, becomes a creator in the same transaction, whether the
// challenge has creators left or not (nameCreator). It writes in the
// transaction ctx carries and judges nothing: the caller
// (admin.ChallengeCorrections) recomputes the challenge, whose weeks locked
// while it was deleted were judged by no one. A missing challenge, or one
// that is not deleted, answers 404 challenge_not_found.
func (s *Service) AdminRestore(ctx context.Context, actor auth.Actor, id string, creatorID *string) (Restoration, error) {
	if err := AdminRefusal(actor.Admin); err != nil {
		return Restoration{}, err
	}
	var complete bool
	if creatorID != nil {
		// The account first, before the challenge, in the order of an
		// account deletion (lockAccount); its profile read once locked.
		if err := s.lockAccount(ctx, actor, *creatorID, apperr.CodeUserNotFound); err != nil {
			return Restoration{}, err
		}
		var err error
		if complete, err = s.profiles.ProfileComplete(ctx, *creatorID); err != nil {
			return Restoration{}, fmt.Errorf("check profile: %w", err)
		}
	}
	now := s.clock.Now()
	at, err := s.repo.Restore(ctx, id, func(c Challenge, creators int) error {
		if creatorID != nil {
			return nil
		}
		return AdminNoCreatorRefusal(StatusAt(now, c.StartsAt, c.EndsAt), creators)
	})
	if err := challengeErr(err); err != nil {
		return Restoration{}, err
	}
	out := Restoration{DeletedAt: at}
	if creatorID == nil {
		return out, nil
	}
	if out.Creator, err = s.nameCreator(ctx, actor, id, *creatorID, complete, now); err != nil {
		return Restoration{}, err
	}
	return out, nil
}

// nameCreator makes userID, whose account the transaction ctx carries holds
// locked, a creator of challenge id, restored in it (its row locked too):
// a participant, active or one who abandoned (not reinstated), becomes a
// creator as by AdminAddCreator; an account that never joined is first
// added as an active participant, by the rule of AdminAddParticipant (422
// profile_incomplete unless profileComplete), joined at now, the instant
// of the restore. It returns what it wrote, both changes merged.
func (s *Service) nameCreator(ctx context.Context, actor auth.Actor, id, userID string, profileComplete bool, now time.Time) (MemberChange, error) {
	_, m, err := s.repo.Get(ctx, id, userID, now)
	if err := challengeErr(err); err != nil {
		return MemberChange{}, err
	}
	out := noChange()
	if m.Participant == "" {
		if err := AdminAddParticipantRefusal(m, profileComplete); err != nil {
			return MemberChange{}, err
		}
		err := s.repo.Join(ctx, id, userID, now, func(Challenge, Membership) (time.Time, error) { return now, nil })
		if errors.Is(err, errAccountDeleted) {
			return MemberChange{}, apperr.New(apperr.CodeUserNotFound, nil)
		}
		if err := challengeErr(err); err != nil {
			return MemberChange{}, err
		}
		out.New[memberStatus] = ParticipantActive
	}
	err = s.repo.AddCreator(ctx, id, actor.ID, userID, now, func(_ Challenge, _, target Membership) (bool, error) {
		noop, err := AdminAddCreatorRefusal(target)
		if err == nil && !noop {
			out.Old[memberCreator] = false
			out.New[memberCreator] = true
		}
		return noop, err
	})
	if err := challengeErr(err); err != nil {
		return MemberChange{}, err
	}
	return out, nil
}
