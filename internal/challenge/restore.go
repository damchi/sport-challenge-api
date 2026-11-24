package challenge

import (
	"context"
	"time"

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

// AdminRestore clears the deletion of challenge id as actor, an admin
// (AdminRefusal, 404 not_found otherwise), at any time (README §2.12): the
// challenge is found again, its invitation links neither revoked nor
// expired valid again; it returns the instant the challenge was deleted at.
// It writes in the transaction ctx carries and judges nothing: the caller
// (admin.ChallengeCorrections) recomputes the challenge, whose weeks locked
// while it was deleted were judged by no one. A missing challenge, or one
// that is not deleted, answers 404 challenge_not_found.
func (s *Service) AdminRestore(ctx context.Context, actor auth.Actor, id string) (time.Time, error) {
	if err := AdminRefusal(actor.Admin); err != nil {
		return time.Time{}, err
	}
	at, err := s.repo.Restore(ctx, id)
	if err := challengeErr(err); err != nil {
		return time.Time{}, err
	}
	return at, nil
}
