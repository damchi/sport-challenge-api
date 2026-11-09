package challenge

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/damchi/sport-challenge-api/internal/api"
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
)

// Handler implements the challenge operations of the API contract.
type Handler struct {
	service *Service
}

// NewHandler returns a Handler on service.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// CreateChallenge creates a challenge for the authenticated user.
func (h *Handler) CreateChallenge(ctx context.Context, req api.CreateChallengeRequestObject) (api.CreateChallengeResponseObject, error) {
	actorID, ok := auth.UserID(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	if req.Body == nil {
		return nil, apperr.InvalidFields(nil)
	}
	b := req.Body
	draft := Draft{
		Title:                 b.Title,
		Description:           b.Description,
		Visibility:            string(b.Visibility),
		Timezone:              b.Timezone,
		StartDate:             b.StartDate.String(),
		Weeks:                 b.Weeks,
		EliminationStepMeters: b.EliminationStepMeters,
	}
	if b.MembersCanInvite != nil {
		draft.MembersCanInvite = *b.MembersCanInvite
	}
	if b.DistanceFactors != nil {
		draft.DistanceFactors = *b.DistanceFactors
	}

	c, err := h.service.Create(ctx, actorID, draft)
	if err != nil {
		return nil, err
	}
	out, err := toAPI(c)
	if err != nil {
		return nil, err
	}
	return api.CreateChallenge201JSONResponse(out), nil
}

func toAPI(c Challenge) (api.Challenge, error) {
	id, err := uuid.Parse(c.ID)
	if err != nil {
		return api.Challenge{}, fmt.Errorf("challenge id: %w", err)
	}
	out := api.Challenge{
		Id:               id,
		Title:            c.Title,
		Description:      c.Description,
		Visibility:       api.ChallengeVisibility(c.Visibility),
		MembersCanInvite: c.MembersCanInvite,
		Timezone:         c.Timezone,
		StartsAt:         c.StartsAt.UTC(),
		EndsAt:           c.EndsAt.UTC(),
		Categories:       make([]api.Category, 0, len(c.Categories)),
	}
	for _, cat := range c.Categories {
		var params api.CategoryParams
		if cat.Params.Factors != nil {
			factors := cat.Params.Factors
			params.Factors = &factors
		}
		if cat.Params.Sport != "" {
			sport := cat.Params.Sport
			params.Sport = &sport
		}
		if cat.Params.StepMeters != 0 {
			step := cat.Params.StepMeters
			params.StepMeters = &step
		}
		out.Categories = append(out.Categories, api.Category{
			Type:     api.CategoryType(cat.Type),
			Position: cat.Position,
			Params:   params,
		})
	}
	return out, nil
}
