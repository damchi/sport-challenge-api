package leaderboard

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/damchi/sport-challenge-api/internal/api"
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/httpx"
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

// GetLeaderboard answers the leaderboard of a challenge, whole or the part
// asked for (category, limit, cursor).
func (h *Handler) GetLeaderboard(ctx context.Context, req api.GetLeaderboardRequestObject) (api.GetLeaderboardResponseObject, error) {
	actor, ok := auth.ActorFrom(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	pageReq, err := pageRequest(req.Params)
	if err != nil {
		return nil, err
	}
	page, err := h.service.Page(ctx, actor, req.ChallengeId.String(), pageReq)
	if err != nil {
		return nil, err
	}
	out := api.Leaderboard{
		ComputedAt: page.ComputedAt.UTC(),
		Categories: make([]api.CategoryRanking, 0, len(page.Categories)),
	}
	for _, category := range page.Categories {
		ranking, err := toRanking(category)
		if err != nil {
			return nil, err
		}
		out.Categories = append(out.Categories, ranking)
	}
	return api.GetLeaderboard200JSONResponse(out), nil
}

// Contract turns the whole rankings of a challenge (Service.Get) into their
// contract form, as GetLeaderboard answers them without limit: no podium,
// no line of the reader, no next page.
func Contract(b Leaderboard) (api.Leaderboard, error) {
	out := api.Leaderboard{ComputedAt: b.ComputedAt.UTC(), Categories: make([]api.CategoryRanking, 0, len(b.Categories))}
	for _, c := range b.Categories {
		entries, err := toEntries(c.Entries)
		if err != nil {
			return api.Leaderboard{}, err
		}
		out.Categories = append(out.Categories, api.CategoryRanking{
			Type: api.CategoryRankingType(c.Type), RankedCount: c.RankedCount, Entries: entries,
		})
	}
	return out, nil
}

// pageRequest reads the part of the leaderboard asked for. A cursor needs
// the limit and the category it was given for; one that does not decode, or
// decodes to no position, answers 400 validation_failed on cursor.
func pageRequest(p api.GetLeaderboardParams) (PageRequest, error) {
	var req PageRequest
	if p.Category != nil {
		req.Category = string(*p.Category)
	}
	if p.Limit != nil {
		req.Limit = *p.Limit
	}
	if p.Cursor == nil {
		return req, nil
	}
	var missing []string
	if req.Category == "" {
		missing = append(missing, "category")
	}
	if req.Limit == 0 {
		missing = append(missing, "limit")
	}
	if missing != nil {
		return PageRequest{}, apperr.InvalidFields(nil, missing...)
	}
	after := &Position{}
	if err := httpx.DecodeCursor(*p.Cursor, after); err != nil {
		return PageRequest{}, err
	}
	if after.Category == "" || after.Version == "" || after.Offset <= 0 {
		return PageRequest{}, apperr.InvalidFields(nil, "cursor")
	}
	req.After = after
	return req, nil
}

// toRanking turns a category of a page into its contract form, the
// position of its next page as an opaque cursor.
func toRanking(c CategoryPage) (api.CategoryRanking, error) {
	out := api.CategoryRanking{Type: api.CategoryRankingType(c.Type), RankedCount: c.RankedCount}
	var err error
	if out.Entries, err = toEntries(c.Entries); err != nil {
		return api.CategoryRanking{}, err
	}
	if c.Podium != nil {
		podium, err := toEntries(c.Podium)
		if err != nil {
			return api.CategoryRanking{}, err
		}
		out.Podium = &podium
	}
	if c.Me != nil {
		me, err := toEntry(*c.Me)
		if err != nil {
			return api.CategoryRanking{}, err
		}
		out.Me = &me
	}
	if c.Next != nil {
		cursor, err := httpx.EncodeCursor(c.Next)
		if err != nil {
			return api.CategoryRanking{}, err
		}
		out.NextCursor = &cursor
	}
	return out, nil
}

func toEntries(lines []Ranked) ([]api.LeaderboardEntry, error) {
	entries := make([]api.LeaderboardEntry, 0, len(lines))
	for _, r := range lines {
		entry, err := toEntry(r)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// toEntry turns a line of a ranking into its contract form: an abandoned
// participant has no rank, no value, no elimination progress and no
// provisional points.
func toEntry(r Ranked) (api.LeaderboardEntry, error) {
	id, err := uuid.Parse(r.UserID)
	if err != nil {
		return api.LeaderboardEntry{}, fmt.Errorf("participant id: %w", err)
	}
	// Avatar stays null in V1: the avatar upload comes with SC-API-023.
	entry := api.LeaderboardEntry{
		User:      api.LeaderboardUser{Id: id, Pseudo: r.Name.Pseudo, FirstName: r.Name.FirstName, LastName: r.Name.LastName},
		Abandoned: r.Abandoned,
	}
	if r.Abandoned {
		return entry, nil
	}
	entry.Rank, entry.Value, entry.ProvisionalPoints = &r.Rank, &r.Value, r.Provisional
	if r.Progress != nil {
		entry.Elimination = toProgress(*r.Progress)
	}
	return entry, nil
}

// toProgress turns the live situation into its contract form, zero values
// being null: no status, no eliminated week, no minimum, no week end.
func toProgress(p scoring.Progress) *api.EliminationProgress {
	out := &api.EliminationProgress{MinimumMeters: p.MinimumMeters, DistanceMeters: p.DistanceMeters}
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
