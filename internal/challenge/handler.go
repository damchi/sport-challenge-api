package challenge

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

// defaultLimit is the page size when the client gives none (the contract's
// default).
const defaultLimit = 20

// ListChallenges answers a page of challenges for the authenticated user.
func (h *Handler) ListChallenges(ctx context.Context, req api.ListChallengesRequestObject) (api.ListChallengesResponseObject, error) {
	actorID, ok := auth.UserID(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	p := req.Params
	scope, status, limit := ScopePublic, "", defaultLimit
	if p.Scope != nil {
		scope = string(*p.Scope)
	}
	if p.Status != nil {
		status = string(*p.Status)
	}
	if p.Limit != nil {
		limit = *p.Limit
	}
	var after *Position
	if p.Cursor != nil {
		after = &Position{}
		if err := httpx.DecodeCursor(*p.Cursor, after); err != nil {
			return nil, err
		}
		// Normalized: uuid.Parse also accepts forms PostgreSQL refuses
		// (urn:uuid:..., {...}).
		id, err := uuid.Parse(after.ID)
		if err != nil {
			return nil, apperr.InvalidFields(err, "cursor")
		}
		after.ID = id.String()
		// A position without its date (forged cursor) would restart the list.
		if after.StartsAt.IsZero() {
			return nil, apperr.InvalidFields(nil, "cursor")
		}
	}

	page, err := h.service.List(ctx, actorID, scope, status, limit, after)
	if err != nil {
		return nil, err
	}
	out := api.ChallengePage{Items: make([]api.ChallengeSummary, 0, len(page.Items))}
	for _, c := range page.Items {
		id, err := uuid.Parse(c.ID)
		if err != nil {
			return nil, fmt.Errorf("challenge id: %w", err)
		}
		item := api.ChallengeSummary{
			Id:               id,
			Title:            c.Title,
			Visibility:       api.ChallengeSummaryVisibility(c.Visibility),
			Timezone:         c.Timezone,
			StartsAt:         c.StartsAt.UTC(),
			EndsAt:           c.EndsAt.UTC(),
			ParticipantCount: c.ParticipantCount,
			// Both schemas declare the same my_role enum (openapi.yaml).
			MyRole: api.ChallengeSummaryMyRole(roleName(c.Role)),
			Status: api.ChallengeSummaryStatus(c.Status),
		}
		if c.MyStatus != nil {
			status := api.ChallengeSummaryMyStatus(*c.MyStatus)
			item.MyStatus = &status
		}
		if c.MyWeek != nil {
			item.MyWeek = toMyWeek(*c.MyWeek)
		}
		out.Items = append(out.Items, item)
	}
	if page.Next != nil {
		cursor, err := httpx.EncodeCursor(page.Next)
		if err != nil {
			return nil, err
		}
		out.NextCursor = &cursor
	}
	return api.ListChallenges200JSONResponse(out), nil
}

// toMyWeek turns a standing into its contract form, the elimination
// progress as the leaderboard gives it.
func toMyWeek(st Standing) *api.MyWeek {
	out := &api.MyWeek{Week: st.Week, WeekEndsAt: st.WeekEndsAt.UTC(), Rankings: make([]api.MyRanking, len(st.Rankings))}
	for i, r := range st.Rankings {
		out.Rankings[i] = api.MyRanking{Type: api.MyRankingType(r.Type), Rank: r.Rank, RankedCount: r.RankedCount}
		if r.Progress != nil {
			out.Rankings[i].Elimination = toProgress(*r.Progress)
		}
	}
	return out
}

// toProgress turns the live situation into its contract form, zero values
// being null, as the leaderboard does (leaderboard.toProgress).
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

// GetChallenge answers a challenge as the authenticated user sees it.
func (h *Handler) GetChallenge(ctx context.Context, req api.GetChallengeRequestObject) (api.GetChallengeResponseObject, error) {
	actorID, ok := auth.UserID(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	out, err := h.ChallengeDetail(ctx, actorID, req.ChallengeId.String())
	if err != nil {
		return nil, err
	}
	return api.GetChallenge200JSONResponse(out), nil
}

// ChallengeDetail gives challenge id as actorID sees it, in the shape of
// the contract: for GetChallenge, and for the routes of other modules that
// answer a challenge (accepting an invitation).
func (h *Handler) ChallengeDetail(ctx context.Context, actorID, id string) (api.ChallengeDetail, error) {
	d, err := h.service.Get(ctx, actorID, id)
	if err != nil {
		return api.ChallengeDetail{}, err
	}
	return toDetail(d)
}

// UpdateChallenge edits the title and the description.
func (h *Handler) UpdateChallenge(ctx context.Context, req api.UpdateChallengeRequestObject) (api.UpdateChallengeResponseObject, error) {
	actorID, ok := auth.UserID(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	if req.Body == nil {
		return nil, apperr.InvalidFields(nil)
	}
	b := req.Body
	// The fields frozen at creation, by their contract name.
	var frozen []string
	for name, value := range map[string]any{
		"categories": b.Categories, "distance_factors": b.DistanceFactors,
		"elimination_step_meters": b.EliminationStepMeters, "ends_at": b.EndsAt,
		"members_can_invite": b.MembersCanInvite, "start_date": b.StartDate,
		"starts_at": b.StartsAt, "timezone": b.Timezone,
		"visibility": b.Visibility, "weeks": b.Weeks,
	} {
		if value != nil {
			frozen = append(frozen, name)
		}
	}
	d, err := h.service.UpdateText(ctx, actorID, req.ChallengeId.String(), frozen, TextChanges{Title: b.Title, Description: b.Description})
	if err != nil {
		return nil, err
	}
	out, err := toDetail(d)
	if err != nil {
		return nil, err
	}
	return api.UpdateChallenge200JSONResponse(out), nil
}

func toDetail(d Detail) (api.ChallengeDetail, error) {
	base, err := toAPI(d.Challenge)
	if err != nil {
		return api.ChallengeDetail{}, err
	}
	out := api.ChallengeDetail{
		Id:                   base.Id,
		Title:                base.Title,
		Description:          base.Description,
		Visibility:           api.ChallengeDetailVisibility(base.Visibility),
		MembersCanInvite:     base.MembersCanInvite,
		Timezone:             base.Timezone,
		StartsAt:             base.StartsAt,
		EndsAt:               base.EndsAt,
		Categories:           base.Categories,
		MyRole:               roleName(d.Role),
		Status:               api.ChallengeDetailStatus(d.Status),
		CanJoin:              d.CanJoin,
		RegistrationDeadline: d.RegistrationDeadline.UTC(),
	}
	if d.Participant != "" {
		status := api.ChallengeDetailMyStatus(d.Participant)
		out.MyStatus = &status
	}
	if d.CurrentWeek > 0 {
		week, ends := d.CurrentWeek, d.WeekEndsAt.UTC()
		out.CurrentWeek, out.WeekEndsAt = &week, &ends
	}
	return out, nil
}

func roleName(r Role) api.ChallengeDetailMyRole {
	switch r {
	case RoleCreator:
		return api.ChallengeDetailMyRoleCreator
	case RoleParticipant:
		return api.ChallengeDetailMyRoleParticipant
	case RoleAbandoned:
		return api.ChallengeDetailMyRoleAbandoned
	case RoleInvited:
		return api.ChallengeDetailMyRoleInvited
	default:
		return api.ChallengeDetailMyRoleOutsider
	}
}

// JoinChallenge makes the authenticated user a participant.
func (h *Handler) JoinChallenge(ctx context.Context, req api.JoinChallengeRequestObject) (api.JoinChallengeResponseObject, error) {
	actorID, ok := auth.UserID(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	d, err := h.service.Join(ctx, actorID, req.ChallengeId.String())
	if err != nil {
		return nil, err
	}
	out, err := toDetail(d)
	if err != nil {
		return nil, err
	}
	return api.JoinChallenge201JSONResponse(out), nil
}

// AbandonChallenge ends the authenticated user's participation for good.
func (h *Handler) AbandonChallenge(ctx context.Context, req api.AbandonChallengeRequestObject) (api.AbandonChallengeResponseObject, error) {
	actorID, ok := auth.UserID(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	d, err := h.service.Abandon(ctx, actorID, req.ChallengeId.String())
	if err != nil {
		return nil, err
	}
	out, err := toDetail(d)
	if err != nil {
		return nil, err
	}
	return api.AbandonChallenge200JSONResponse(out), nil
}

// ListParticipants answers a page of the participants of a challenge.
func (h *Handler) ListParticipants(ctx context.Context, req api.ListParticipantsRequestObject) (api.ListParticipantsResponseObject, error) {
	actorID, ok := auth.UserID(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	p := req.Params
	limit := defaultLimit
	if p.Limit != nil {
		limit = *p.Limit
	}
	var after *ParticipantPosition
	if p.Cursor != nil {
		after = &ParticipantPosition{}
		if err := httpx.DecodeCursor(*p.Cursor, after); err != nil {
			return nil, err
		}
		// Normalized, and checked whole, as for ListChallenges.
		id, err := uuid.Parse(after.UserID)
		if err != nil {
			return nil, apperr.InvalidFields(err, "cursor")
		}
		after.UserID = id.String()
		if after.JoinedAt.IsZero() {
			return nil, apperr.InvalidFields(nil, "cursor")
		}
	}

	page, err := h.service.Participants(ctx, actorID, req.ChallengeId.String(), limit, after)
	if err != nil {
		return nil, err
	}
	out := api.ParticipantPage{Items: make([]api.Participant, 0, len(page.Items))}
	for _, participant := range page.Items {
		id, err := uuid.Parse(participant.UserID)
		if err != nil {
			return nil, fmt.Errorf("participant id: %w", err)
		}
		// Avatar stays null in V1: the avatar upload comes with SC-API-023.
		out.Items = append(out.Items, api.Participant{
			UserId:    id,
			Pseudo:    participant.Name.Pseudo,
			FirstName: participant.Name.FirstName,
			LastName:  participant.Name.LastName,
			Status:    api.ParticipantStatus(participant.Status),
			IsCreator: participant.IsCreator,
			JoinedAt:  participant.JoinedAt.UTC(),
		})
	}
	if page.Next != nil {
		cursor, err := httpx.EncodeCursor(page.Next)
		if err != nil {
			return nil, err
		}
		out.NextCursor = &cursor
	}
	return api.ListParticipants200JSONResponse(out), nil
}

// ExcludeParticipant excludes a participant on behalf of a creator.
func (h *Handler) ExcludeParticipant(ctx context.Context, req api.ExcludeParticipantRequestObject) (api.ExcludeParticipantResponseObject, error) {
	actorID, ok := auth.UserID(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	if err := h.service.Exclude(ctx, actorID, req.ChallengeId.String(), req.UserId.String()); err != nil {
		return nil, err
	}
	return api.ExcludeParticipant204Response{}, nil
}

// AddCreator makes a participant a creator, on behalf of a creator.
func (h *Handler) AddCreator(ctx context.Context, req api.AddCreatorRequestObject) (api.AddCreatorResponseObject, error) {
	actorID, ok := auth.UserID(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	if req.Body == nil {
		return nil, apperr.InvalidFields(nil)
	}
	d, err := h.service.AddCreator(ctx, actorID, req.ChallengeId.String(), req.Body.UserId.String())
	if err != nil {
		return nil, err
	}
	out, err := toDetail(d)
	if err != nil {
		return nil, err
	}
	return api.AddCreator200JSONResponse(out), nil
}

// DeleteChallenge soft-deletes a challenge on behalf of a creator.
func (h *Handler) DeleteChallenge(ctx context.Context, req api.DeleteChallengeRequestObject) (api.DeleteChallengeResponseObject, error) {
	actorID, ok := auth.UserID(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	if err := h.service.Delete(ctx, actorID, req.ChallengeId.String()); err != nil {
		return nil, err
	}
	return api.DeleteChallenge204Response{}, nil
}
