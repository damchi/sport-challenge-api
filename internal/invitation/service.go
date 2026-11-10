// Package invitation holds the invitations to a challenge: links reusable
// until the end of week 1, the registration deadline. A link is a bearer
// token: any logged-in user who holds it may open it. Opening it links the
// user to the invitation, and that recorded link, while the invitation is
// valid, lets them read a private challenge (challenge.Membership.Invited).
package invitation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
)

// tokenBytes is the size of a token: 256 random bits, not guessable.
const tokenBytes = 32

// repository is what the service needs from the storage.
type repository interface {
	Create(ctx context.Context, inv Invitation) (Invitation, error)
	// Link links userID, at now, to the invitation of tokenHash if it is
	// valid at now (not expired, not revoked, its challenge not deleted),
	// and returns its challenge; errInvalid otherwise. Linking twice
	// changes nothing.
	Link(ctx context.Context, tokenHash []byte, userID string, now time.Time) (string, error)
}

// Challenge is what an invitation shows of its challenge: a limited
// preview.
type Challenge struct {
	ID       string
	Title    string
	StartsAt time.Time
	EndsAt   time.Time
}

// Challenges is what the service needs from module challenge, which applies
// its own rules to actorID each time.
type Challenges interface {
	// InvitationDeadline gives the end of week 1 of challenge challengeID
	// once actorID may invite to it, and refuses otherwise (rule
	// challenge.InviteRefusal).
	InvitationDeadline(ctx context.Context, actorID, challengeID string) (time.Time, error)
	// Preview gives the preview of a challenge actorID may see; 404
	// challenge_not_found otherwise.
	Preview(ctx context.Context, actorID, challengeID string) (Challenge, error)
	// Join makes actorID a participant of the challenge, under the join
	// rule (challenge.JoinRefusal).
	Join(ctx context.Context, actorID, challengeID string) error
}

// Service holds the use cases of the invitations.
type Service struct {
	challenges Challenges
	repo       repository
	clock      clock.Clock
}

// NewService returns a Service.
func NewService(challenges Challenges, repo repository, c clock.Clock) *Service {
	return &Service{challenges: challenges, repo: repo, clock: c}
}

// Create creates an invitation of actorID to challenge challengeID, valid
// until the end of week 1, and returns it with its token: the only time the
// token exists in clear, as only its hash is stored. The rule is the
// challenge module's: nothing is written before it passes. A challenge
// deleted between the rule and the insert keeps a harmless invitation:
// Link refuses any invitation to a deleted challenge.
func (s *Service) Create(ctx context.Context, actorID, challengeID string) (Invitation, string, error) {
	deadline, err := s.challenges.InvitationDeadline(ctx, actorID, challengeID)
	if err != nil {
		return Invitation{}, "", err
	}
	raw := make([]byte, tokenBytes)
	// crypto/rand never fails: it aborts the program instead.
	_, _ = rand.Read(raw)
	hash := sha256.Sum256(raw)
	inv, err := s.repo.Create(ctx, Invitation{
		ChallengeID: challengeID,
		InvitedBy:   actorID,
		TokenHash:   hash[:],
		ExpiresAt:   deadline,
		CreatedAt:   s.clock.Now(),
	})
	if err != nil {
		return Invitation{}, "", fmt.Errorf("create invitation: %w", err)
	}
	return inv, base64.RawURLEncoding.EncodeToString(raw), nil
}

// Link links actorID to the invitation of token, at the clock's now, and
// returns its challenge: from then on, and while the invitation is valid,
// actorID is invited there. An unknown token, a malformed one, an expired
// or revoked invitation, and one to a deleted challenge all answer the same
// 404 invitation_invalid.
func (s *Service) Link(ctx context.Context, actorID, token string) (string, error) {
	// Strict: one spelling per token, its unused trailing bits zero.
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(raw) != tokenBytes {
		return "", apperr.New(apperr.CodeInvitationInvalid, nil)
	}
	hash := sha256.Sum256(raw)
	challengeID, err := s.repo.Link(ctx, hash[:], actorID, s.clock.Now())
	if errors.Is(err, errInvalid) {
		return "", apperr.New(apperr.CodeInvitationInvalid, nil)
	}
	if err != nil {
		return "", fmt.Errorf("link invitation: %w", err)
	}
	return challengeID, nil
}

// Preview opens the link of token for actorID (Link) and returns the
// preview of its challenge.
func (s *Service) Preview(ctx context.Context, actorID, token string) (Challenge, error) {
	challengeID, err := s.Link(ctx, actorID, token)
	if err != nil {
		return Challenge{}, err
	}
	c, err := s.challenges.Preview(ctx, actorID, challengeID)
	if err != nil {
		return Challenge{}, invalidIfHidden(err)
	}
	return c, nil
}

// Accept opens the link of token for actorID (Link), then joins its
// challenge under the join rule of module challenge, and returns the
// challenge joined. A refused join keeps the link: actorID stays invited
// while the invitation is valid.
func (s *Service) Accept(ctx context.Context, actorID, token string) (string, error) {
	challengeID, err := s.Link(ctx, actorID, token)
	if err != nil {
		return "", err
	}
	if err := s.challenges.Join(ctx, actorID, challengeID); err != nil {
		return "", invalidIfHidden(err)
	}
	return challengeID, nil
}

// invalidIfHidden turns a 404 challenge_not_found into a 404
// invitation_invalid: the invitation stopped being valid, or its challenge
// was deleted, between the link and the read of the challenge. The routes
// of an invitation never answer about a challenge.
func invalidIfHidden(err error) error {
	if errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
		return apperr.New(apperr.CodeInvitationInvalid, nil)
	}
	return err
}
