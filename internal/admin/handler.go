package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/damchi/sport-challenge-api/internal/api"
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/httpx"
)

// Handler implements the /admin operations of the API contract.
type Handler struct {
	service *Service
}

// NewHandler returns a Handler on service.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// GetAdminStats answers the figures of the dashboard.
func (h *Handler) GetAdminStats(ctx context.Context, _ api.GetAdminStatsRequestObject) (api.GetAdminStatsResponseObject, error) {
	actor, ok := auth.ActorFrom(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	s, err := h.service.Stats(ctx, actor)
	if err != nil {
		return nil, err
	}
	tasks := make([]api.AdminWorkerTask, len(s.WorkerTasks))
	for i, t := range s.WorkerTasks {
		tasks[i] = api.AdminWorkerTask{
			Task:        api.AdminWorkerTaskTask(t.Task),
			LastRun:     toWorkerRun(t.LastRun),
			LastFailure: toWorkerRun(t.LastFailure),
		}
	}
	return api.GetAdminStats200JSONResponse{
		ComputedAt: s.ComputedAt.UTC(),
		Accounts: api.AdminAccountCounts{
			Total:           int(s.AccountsTotal),
			NewLast7Days:    int(s.AccountsNew),
			ActiveLast7Days: int(s.AccountsActive),
		},
		Challenges: api.AdminChallengeCounts{
			Upcoming: int(s.Upcoming),
			Ongoing:  int(s.Ongoing),
			Finished: int(s.Finished),
			Deleted:  int(s.Deleted),
		},
		ActivitiesLast7Days:   int(s.Activities),
		PendingAuth0Deletions: int(s.PendingAuth0Deletions),
		WorkerTasks:           tasks,
		Build:                 api.AdminBuild{Version: s.Build.Version, Commit: s.Build.Commit},
	}, nil
}

// toWorkerRun gives a run the shape of the contract, nil for none.
func toWorkerRun(r *WorkerRun) *api.AdminWorkerRun {
	if r == nil {
		return nil
	}
	out := &api.AdminWorkerRun{StartedAt: r.StartedAt.UTC(), ErrorCode: r.ErrorCode}
	if r.EndedAt != nil {
		ended := r.EndedAt.UTC()
		out.EndedAt = &ended
	}
	if r.Outcome != nil {
		outcome := api.AdminWorkerRunOutcome(*r.Outcome)
		out.Outcome = &outcome
	}
	return out
}

// defaultLimit is the page size when the client gives none (the contract's
// default).
const defaultLimit = 20

// ListAdminChallenges answers a page of every challenge, deleted ones
// included.
func (h *Handler) ListAdminChallenges(ctx context.Context, req api.ListAdminChallengesRequestObject) (api.ListAdminChallengesResponseObject, error) {
	actor, ok := auth.ActorFrom(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	p := req.Params
	q := ChallengeQuery{Limit: defaultLimit, Deleted: p.Deleted}
	if p.Q != nil {
		q.Search = *p.Q
	}
	if p.Status != nil {
		q.Status = string(*p.Status)
	}
	if p.Visibility != nil {
		q.Visibility = string(*p.Visibility)
	}
	if p.Limit != nil {
		q.Limit = *p.Limit
	}
	var cursor string
	if p.Cursor != nil {
		cursor = *p.Cursor
	}

	page, err := h.service.Challenges(ctx, actor, q, cursor)
	if err != nil {
		return nil, err
	}
	out := api.AdminChallengePage{Items: make([]api.AdminChallengeSummary, len(page.Items))}
	for i, c := range page.Items {
		id, err := uuid.Parse(c.ID)
		if err != nil {
			return nil, fmt.Errorf("challenge id: %w", err)
		}
		creators, err := toUserRefs(c.Creators)
		if err != nil {
			return nil, err
		}
		out.Items[i] = api.AdminChallengeSummary{
			Id:               id,
			Title:            c.Title,
			Visibility:       api.AdminChallengeSummaryVisibility(c.Visibility),
			Status:           api.AdminChallengeSummaryStatus(c.Status),
			Timezone:         c.Timezone,
			StartsAt:         c.StartsAt.UTC(),
			EndsAt:           c.EndsAt.UTC(),
			CreatedAt:        c.CreatedAt.UTC(),
			DeletedAt:        utc(c.DeletedAt),
			ParticipantCount: c.ParticipantCount,
			Creators:         creators,
		}
	}
	if page.Next != nil {
		cursor, err := httpx.EncodeCursor(page.Next)
		if err != nil {
			return nil, err
		}
		out.NextCursor = &cursor
	}
	return api.ListAdminChallenges200JSONResponse(out), nil
}

// GetAdminChallenge answers what an admin sees of a challenge, deleted or
// not.
func (h *Handler) GetAdminChallenge(ctx context.Context, req api.GetAdminChallengeRequestObject) (api.GetAdminChallengeResponseObject, error) {
	actor, ok := auth.ActorFrom(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	d, err := h.service.Challenge(ctx, actor, req.ChallengeId.String())
	if err != nil {
		return nil, err
	}
	c := d.Challenge
	id, err := uuid.Parse(c.ID)
	if err != nil {
		return nil, fmt.Errorf("challenge id: %w", err)
	}
	creators, err := toUserRefs(d.Creators)
	if err != nil {
		return nil, err
	}
	out := api.AdminChallengeDetail{
		Id:               id,
		Title:            c.Title,
		Description:      c.Description,
		Visibility:       api.AdminChallengeDetailVisibility(c.Visibility),
		MembersCanInvite: c.MembersCanInvite,
		Timezone:         c.Timezone,
		ReferenceSport:   c.ReferenceSport,
		StartsAt:         c.StartsAt.UTC(),
		EndsAt:           c.EndsAt.UTC(),
		Categories:       make([]api.Category, len(d.Categories)),
		Status:           api.AdminChallengeDetailStatus(d.Status),
		CreatedAt:        c.CreatedAt.UTC(),
		DeletedAt:        utc(c.DeletedAt),
		ParticipantCount: c.ParticipantCount,
		Creators:         creators,
		Weeks:            make([]api.AdminChallengeWeek, len(d.Weeks)),
		Eliminations:     make([]api.AdminElimination, len(d.Eliminations)),
		ResultsFrozenAt:  utc(d.ResultsFrozenAt),
		Invitations:      make([]api.AdminInvitation, len(d.Invitations)),
	}
	for i, cat := range d.Categories {
		// categories.params holds the object of the contract's
		// CategoryParams, under the same keys.
		var params api.CategoryParams
		if err := json.Unmarshal(cat.Params, &params); err != nil {
			return nil, fmt.Errorf("category params: %w", err)
		}
		out.Categories[i] = api.Category{Type: api.CategoryType(cat.Type), Position: cat.Position, Params: params}
	}
	for i, w := range d.Weeks {
		out.Weeks[i] = api.AdminChallengeWeek{
			Number: w.Number, StartsAt: w.StartsAt.UTC(), EndsAt: w.EndsAt.UTC(),
			LocksAt: w.LocksAt.UTC(), Locked: w.Locked, ClosedAt: utc(w.ClosedAt),
		}
	}
	for i, e := range d.Eliminations {
		user, err := toUserRef(e.User)
		if err != nil {
			return nil, err
		}
		out.Eliminations[i] = api.AdminElimination{User: user, WeekNumber: e.WeekNumber, DistanceMeters: e.DistanceMeters, RequiredMeters: e.RequiredMeters}
	}
	for i, inv := range d.Invitations {
		invID, err := uuid.Parse(inv.ID)
		if err != nil {
			return nil, fmt.Errorf("invitation id: %w", err)
		}
		by, err := toUserRef(inv.InvitedBy)
		if err != nil {
			return nil, err
		}
		out.Invitations[i] = api.AdminInvitation{
			Id: invID, InvitedBy: by, CreatedAt: inv.CreatedAt.UTC(), ExpiresAt: inv.ExpiresAt.UTC(),
			RevokedAt: utc(inv.RevokedAt), Status: api.AdminInvitationStatus(inv.Status),
		}
	}
	return api.GetAdminChallenge200JSONResponse(out), nil
}

func toUserRef(u UserRef) (api.AdminUserRef, error) {
	id, err := uuid.Parse(u.ID)
	if err != nil {
		return api.AdminUserRef{}, fmt.Errorf("user id: %w", err)
	}
	return api.AdminUserRef{Id: id, Pseudo: u.Pseudo, FirstName: u.FirstName, LastName: u.LastName}, nil
}

func toUserRefs(users []UserRef) ([]api.AdminUserRef, error) {
	out := make([]api.AdminUserRef, len(users))
	for i, u := range users {
		ref, err := toUserRef(u)
		if err != nil {
			return nil, err
		}
		out[i] = ref
	}
	return out, nil
}

// utc gives an optional instant in UTC.
func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
