package invitation

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/damchi/sport-challenge-api/internal/api"
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
)

// Details gives a challenge as actorID sees it, in the shape of the
// contract (module challenge, which applies the rights matrix).
type Details interface {
	ChallengeDetail(ctx context.Context, actorID, challengeID string) (api.ChallengeDetail, error)
}

// Handler implements the invitation operations of the API contract.
type Handler struct {
	service *Service
	details Details
}

// NewHandler returns a Handler on service; details answers the challenge
// joined by accepting an invitation.
func NewHandler(service *Service, details Details) *Handler {
	return &Handler{service: service, details: details}
}

// CreateInvitation creates an invitation link of the authenticated user to
// a challenge.
func (h *Handler) CreateInvitation(ctx context.Context, req api.CreateInvitationRequestObject) (api.CreateInvitationResponseObject, error) {
	actorID, ok := auth.UserID(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	inv, token, err := h.service.Create(ctx, actorID, req.ChallengeId.String())
	if err != nil {
		return nil, err
	}
	return api.CreateInvitation201JSONResponse{Token: token, ExpiresAt: inv.ExpiresAt.UTC()}, nil
}

// OpenInvitation opens an invitation link for the authenticated user and
// answers the preview of its challenge. The token comes in the body, never
// in the URL (README §5.7).
func (h *Handler) OpenInvitation(ctx context.Context, req api.OpenInvitationRequestObject) (api.OpenInvitationResponseObject, error) {
	actorID, ok := auth.UserID(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	c, err := h.service.Preview(ctx, actorID, req.Body.Token)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(c.ID)
	if err != nil {
		return nil, fmt.Errorf("challenge id %q: %w", c.ID, err)
	}
	return api.OpenInvitation200JSONResponse{ChallengeId: id, Title: c.Title, StartsAt: c.StartsAt.UTC(), EndsAt: c.EndsAt.UTC()}, nil
}

// AcceptInvitation makes the authenticated user a participant of the
// challenge of an invitation link. The challenge is read after the join: if
// that read fails, the answer is a 500 although the user has joined, and
// accepting again answers 409 already_participant.
func (h *Handler) AcceptInvitation(ctx context.Context, req api.AcceptInvitationRequestObject) (api.AcceptInvitationResponseObject, error) {
	actorID, ok := auth.UserID(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	challengeID, err := h.service.Accept(ctx, actorID, req.Body.Token)
	if err != nil {
		return nil, err
	}
	out, err := h.details.ChallengeDetail(ctx, actorID, challengeID)
	if err != nil {
		// A challenge deleted right after the join: the same answer as
		// any invitation to a deleted challenge.
		return nil, invalidIfHidden(err)
	}
	return api.AcceptInvitation201JSONResponse(out), nil
}
