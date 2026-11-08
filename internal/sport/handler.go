package sport

import (
	"context"

	"github.com/damchi/sport-challenge-api/internal/api"
)

// cacheControl lets clients reuse the catalogue for 5 minutes. Private: the
// request is authenticated, so shared caches must not keep the response.
const cacheControl = "private, max-age=300"

// Handler implements the /sports operations of the API contract.
type Handler struct {
	service *Service
}

// NewHandler returns a Handler on service.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// GetSports answers the catalogue.
func (h *Handler) GetSports(ctx context.Context, _ api.GetSportsRequestObject) (api.GetSportsResponseObject, error) {
	sports, err := h.service.List(ctx)
	if err != nil {
		return nil, err
	}
	list := api.SportList{Sports: make([]api.Sport, 0, len(sports))}
	for _, s := range sports {
		list.Sports = append(list.Sports, api.Sport{
			Code:            s.Code,
			DistanceMode:    api.SportDistanceMode(s.DistanceMode),
			SuggestedFactor: s.SuggestedFactor,
		})
	}
	return api.GetSports200JSONResponse{
		Body:    list,
		Headers: api.GetSports200ResponseHeaders{CacheControl: cacheControl},
	}, nil
}
