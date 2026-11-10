package leaderboard

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/damchi/sport-challenge-api/internal/api"
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/scoring"
)

// Handler implements the leaderboard operations of the API contract.
type Handler struct {
	service *Service
}

// NewHandler returns a Handler on service.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// GetLeaderboard answers the leaderboard of a challenge.
func (h *Handler) GetLeaderboard(ctx context.Context, req api.GetLeaderboardRequestObject) (api.GetLeaderboardResponseObject, error) {
	actorID, ok := auth.UserID(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	board, err := h.service.Get(ctx, actorID, req.ChallengeId.String())
	if err != nil {
		return nil, err
	}
	out := api.Leaderboard{
		ComputedAt: board.ComputedAt.UTC(),
		Categories: make([]api.CategoryRanking, 0, len(board.Categories)),
	}
	for _, category := range board.Categories {
		entries := make([]api.LeaderboardEntry, 0, len(category.Entries))
		for _, r := range category.Entries {
			entry, err := toEntry(r)
			if err != nil {
				return nil, err
			}
			entries = append(entries, entry)
		}
		out.Categories = append(out.Categories, api.CategoryRanking{Type: api.CategoryRankingType(category.Type), Entries: entries})
	}
	return api.GetLeaderboard200JSONResponse(out), nil
}

// toEntry turns a line of a ranking into its contract form: an abandoned
// participant has no rank, no value and no elimination progress.
func toEntry(r Ranked) (api.LeaderboardEntry, error) {
	id, err := uuid.Parse(r.UserID)
	if err != nil {
		return api.LeaderboardEntry{}, fmt.Errorf("participant id: %w", err)
	}
	// Avatar stays null in V1: the avatar upload comes with SC-API-023.
	entry := api.LeaderboardEntry{
		User:      api.LeaderboardUser{Id: id, Pseudo: r.Pseudo},
		Abandoned: r.Abandoned,
	}
	if r.Abandoned {
		return entry, nil
	}
	entry.Rank, entry.Value = &r.Rank, &r.Value
	if r.Progress != nil {
		entry.Elimination = toProgress(*r.Progress)
	}
	return entry, nil
}

// toProgress turns the live situation into its contract form, zero values
// being null: no status, no eliminated week, no minimum, no week end.
func toProgress(p scoring.Progress) *api.EliminationProgress {
	out := &api.EliminationProgress{MinimumMeters: p.MinimumMeters, RunMeters: p.RunMeters}
	if p.Status != "" {
		status := api.EliminationProgressStatus(p.Status)
		out.Status = &status
	}
	if p.EliminatedWeek > 0 {
		out.EliminatedWeek = &p.EliminatedWeek
	}
	if !p.WeekEndsAt.IsZero() {
		end := p.WeekEndsAt.UTC()
		out.WeekEndsAt = &end
	}
	return out
}
