package activity

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/damchi/sport-challenge-api/internal/api"
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
)

// Handler implements the activity operations of the API contract.
type Handler struct {
	service *Service
}

// NewHandler returns a Handler on service.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// CreateActivity records an activity of the authenticated user: 201 when
// created, 200 when its idempotency key gave one recorded before.
func (h *Handler) CreateActivity(ctx context.Context, req api.CreateActivityRequestObject) (api.CreateActivityResponseObject, error) {
	actorID, ok := auth.UserID(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	if req.Body == nil {
		return nil, apperr.InvalidFields(nil)
	}
	b := req.Body
	rec, err := h.service.Record(ctx, actorID, req.Params.IdempotencyKey, Draft{
		SportCode:       b.SportCode,
		SportLabel:      b.SportLabel,
		StartedAt:       b.StartedAt,
		DurationSeconds: b.DurationSeconds,
		DistanceMeters:  b.DistanceMeters,
	})
	if err != nil {
		return nil, err
	}
	out, err := toAPI(rec)
	if err != nil {
		return nil, err
	}
	if !rec.Created {
		return api.CreateActivity200JSONResponse(out), nil
	}
	return api.CreateActivity201JSONResponse(out), nil
}

func toAPI(rec Recorded) (api.Activity, error) {
	id, err := uuid.Parse(rec.ID)
	if err != nil {
		return api.Activity{}, fmt.Errorf("activity id: %w", err)
	}
	out := api.Activity{
		Id:              id,
		SportCode:       rec.SportCode,
		SportLabel:      rec.SportLabel,
		StartedAt:       rec.StartedAt.UTC(),
		DurationSeconds: rec.DurationSeconds,
		DistanceMeters:  rec.DistanceMeters,
		Source:          api.ActivitySource(rec.Source),
		ChallengeIds:    make([]uuid.UUID, 0, len(rec.ChallengeIDs)),
	}
	if rec.DistanceMeters != nil {
		v := Speed(*rec.DistanceMeters, rec.DurationSeconds)
		out.SpeedMps, out.PaceSecondsPerKm = &v.MetersPerSecond, &v.SecondsPerKilometer
	}
	for _, c := range rec.ChallengeIDs {
		cid, err := uuid.Parse(c)
		if err != nil {
			return api.Activity{}, fmt.Errorf("challenge id: %w", err)
		}
		out.ChallengeIds = append(out.ChallengeIds, cid)
	}
	return out, nil
}
