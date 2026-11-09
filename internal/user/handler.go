package user

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/damchi/sport-challenge-api/internal/api"
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
)

// Handler implements the /me operations of the API contract.
type Handler struct {
	service *Service
}

// NewHandler returns a Handler on service.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// GetMe answers the profile of the authenticated user.
func (h *Handler) GetMe(ctx context.Context, _ api.GetMeRequestObject) (api.GetMeResponseObject, error) {
	actorID, err := actor(ctx)
	if err != nil {
		return nil, err
	}
	u, err := h.service.Profile(ctx, actorID)
	if err != nil {
		return nil, err
	}
	me, err := toMe(u)
	if err != nil {
		return nil, err
	}
	return api.GetMe200JSONResponse(me), nil
}

// DeleteMe deletes the account of the authenticated user.
func (h *Handler) DeleteMe(ctx context.Context, _ api.DeleteMeRequestObject) (api.DeleteMeResponseObject, error) {
	actorID, err := actor(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.service.Delete(ctx, actorID); err != nil {
		return nil, err
	}
	return api.DeleteMe204Response{}, nil
}

// PatchMe changes the profile of the authenticated user.
func (h *Handler) PatchMe(ctx context.Context, req api.PatchMeRequestObject) (api.PatchMeResponseObject, error) {
	actorID, err := actor(ctx)
	if err != nil {
		return nil, err
	}
	var changes ProfileChanges
	if req.Body != nil {
		changes.Pseudo = req.Body.Pseudo
		if req.Body.Language != nil {
			lang := string(*req.Body.Language)
			changes.Language = &lang
		}
	}
	u, err := h.service.UpdateProfile(ctx, actorID, changes)
	if err != nil {
		return nil, err
	}
	me, err := toMe(u)
	if err != nil {
		return nil, err
	}
	return api.PatchMe200JSONResponse(me), nil
}

// actor returns the local user CurrentUser put in the context. Every /me
// route is protected, so a missing user is never served anonymously.
func actor(ctx context.Context) (string, error) {
	id, ok := auth.UserID(ctx)
	if !ok {
		return "", apperr.New(apperr.CodeUnauthenticated, nil)
	}
	return id, nil
}

func toMe(u User) (api.Me, error) {
	id, err := uuid.Parse(u.ID)
	if err != nil {
		return api.Me{}, fmt.Errorf("user id: %w", err)
	}
	me := api.Me{Id: id, Pseudo: u.Pseudo, ProfileComplete: u.ProfileComplete()}
	if u.Language != nil {
		lang := api.Language(*u.Language)
		me.Language = &lang
	}
	// Avatar stays null in V1: the avatar upload comes with SC-API-023.
	return me, nil
}
