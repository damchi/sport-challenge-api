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
	q := ChallengeQuery{Limit: limitOf(p.Limit), Deleted: p.Deleted}
	if p.Q != nil {
		q.Search = *p.Q
	}
	if p.Status != nil {
		q.Status = string(*p.Status)
	}
	if p.Visibility != nil {
		q.Visibility = string(*p.Visibility)
	}
	page, err := h.service.Challenges(ctx, actor, q, cursorOf(p.Cursor))
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
	if out.NextCursor, err = nextCursor(page.Next); err != nil {
		return nil, err
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
		RecentActions:    make([]api.AdminAction, len(d.RecentActions)),
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
	for i, a := range d.RecentActions {
		if out.RecentActions[i], err = toAdminAction(a); err != nil {
			return nil, err
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

// ListAdminUsers answers a page of every account, deleted ones included,
// with their e-mail.
func (h *Handler) ListAdminUsers(ctx context.Context, req api.ListAdminUsersRequestObject) (api.ListAdminUsersResponseObject, error) {
	out, err := h.users(ctx, UserQuery{Limit: limitOf(req.Params.Limit)}, cursorOf(req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	return api.ListAdminUsers200JSONResponse(out), nil
}

// SearchAdminUsers answers a page of the accounts the search of the body
// finds; the search, maybe an e-mail, never travels in the URL.
func (h *Handler) SearchAdminUsers(ctx context.Context, req api.SearchAdminUsersRequestObject) (api.SearchAdminUsersResponseObject, error) {
	q := UserQuery{Limit: defaultLimit}
	var cursor string
	if b := req.Body; b != nil {
		if b.Q != nil {
			q.Search = *b.Q
		}
		q.Limit, cursor = limitOf(b.Limit), cursorOf(b.Cursor)
	}
	out, err := h.users(ctx, q, cursor)
	if err != nil {
		return nil, err
	}
	return api.SearchAdminUsers200JSONResponse(out), nil
}

// users answers a page of the accounts of q.
func (h *Handler) users(ctx context.Context, q UserQuery, cursor string) (api.AdminUserPage, error) {
	actor, ok := auth.ActorFrom(ctx)
	if !ok {
		return api.AdminUserPage{}, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	page, err := h.service.Users(ctx, actor, q, cursor)
	if err != nil {
		return api.AdminUserPage{}, err
	}
	out := api.AdminUserPage{Items: make([]api.AdminUser, len(page.Items))}
	for i, u := range page.Items {
		if out.Items[i], err = toAdminUser(u); err != nil {
			return api.AdminUserPage{}, err
		}
	}
	if out.NextCursor, err = nextCursor(page.Next); err != nil {
		return api.AdminUserPage{}, err
	}
	return out, nil
}

// GetAdminUser answers an account, deleted or not, with its e-mail.
func (h *Handler) GetAdminUser(ctx context.Context, req api.GetAdminUserRequestObject) (api.GetAdminUserResponseObject, error) {
	actor, ok := auth.ActorFrom(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	u, err := h.service.User(ctx, actor, req.UserId.String())
	if err != nil {
		return nil, err
	}
	out, err := toAdminUser(u)
	if err != nil {
		return nil, err
	}
	return api.GetAdminUser200JSONResponse(out), nil
}

// ListAdminUserChallenges answers a page of the challenges of an account.
func (h *Handler) ListAdminUserChallenges(ctx context.Context, req api.ListAdminUserChallengesRequestObject) (api.ListAdminUserChallengesResponseObject, error) {
	actor, ok := auth.ActorFrom(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	p := req.Params
	page, err := h.service.UserChallenges(ctx, actor, req.UserId.String(), limitOf(p.Limit), cursorOf(p.Cursor))
	if err != nil {
		return nil, err
	}
	out := api.AdminUserChallengePage{Items: make([]api.AdminUserChallenge, len(page.Items))}
	for i, c := range page.Items {
		id, err := uuid.Parse(c.ID)
		if err != nil {
			return nil, fmt.Errorf("challenge id: %w", err)
		}
		item := api.AdminUserChallenge{
			Id:          id,
			Title:       c.Title,
			Visibility:  api.AdminUserChallengeVisibility(c.Visibility),
			Status:      api.AdminUserChallengeStatus(c.Status),
			StartsAt:    c.StartsAt.UTC(),
			EndsAt:      c.EndsAt.UTC(),
			CreatedAt:   c.CreatedAt.UTC(),
			DeletedAt:   utc(c.DeletedAt),
			IsCreator:   c.IsCreator,
			JoinedAt:    utc(c.JoinedAt),
			AbandonedAt: utc(c.AbandonedAt),
		}
		if c.ParticipantStatus != nil {
			status := api.AdminUserChallengeParticipantStatus(*c.ParticipantStatus)
			item.ParticipantStatus = &status
		}
		out.Items[i] = item
	}
	if out.NextCursor, err = nextCursor(page.Next); err != nil {
		return nil, err
	}
	return api.ListAdminUserChallenges200JSONResponse(out), nil
}

// ListAdminUserActivities answers a page of the activities of an account.
func (h *Handler) ListAdminUserActivities(ctx context.Context, req api.ListAdminUserActivitiesRequestObject) (api.ListAdminUserActivitiesResponseObject, error) {
	actor, ok := auth.ActorFrom(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	p := req.Params
	page, err := h.service.UserActivities(ctx, actor, req.UserId.String(), limitOf(p.Limit), cursorOf(p.Cursor))
	if err != nil {
		return nil, err
	}
	out := api.AdminActivityPage{Items: make([]api.AdminActivity, len(page.Items))}
	for i, a := range page.Items {
		id, err := uuid.Parse(a.ID)
		if err != nil {
			return nil, fmt.Errorf("activity id: %w", err)
		}
		out.Items[i] = api.AdminActivity{
			Id:                  id,
			SportCode:           a.SportCode,
			SportLabel:          a.SportLabel,
			StartedAt:           a.StartedAt.UTC(),
			DurationSeconds:     a.DurationSeconds,
			DistanceMeters:      a.DistanceMeters,
			ElevationGainMeters: a.ElevationGainMeters,
			Source:              api.AdminActivitySource(a.Source),
			CreatedAt:           a.CreatedAt.UTC(),
		}
	}
	if out.NextCursor, err = nextCursor(page.Next); err != nil {
		return nil, err
	}
	return api.ListAdminUserActivities200JSONResponse(out), nil
}

func toAdminUser(u UserRow) (api.AdminUser, error) {
	id, err := uuid.Parse(u.ID)
	if err != nil {
		return api.AdminUser{}, fmt.Errorf("user id: %w", err)
	}
	return api.AdminUser{
		Id:             id,
		Pseudo:         u.Pseudo,
		FirstName:      u.FirstName,
		LastName:       u.LastName,
		Email:          u.Email,
		State:          api.AdminUserState(u.State),
		CreatedAt:      u.CreatedAt.UTC(),
		DeletedAt:      utc(u.DeletedAt),
		LastActivityAt: utc(u.LastActivityAt),
		ChallengeCount: u.ChallengeCount,
	}, nil
}

// limitOf is the page size asked, defaultLimit without one.
func limitOf(limit *int) int {
	if limit == nil {
		return defaultLimit
	}
	return *limit
}

// cursorOf is the cursor sent, "" for the first page.
func cursorOf(cursor *string) string {
	if cursor == nil {
		return ""
	}
	return *cursor
}

// nextCursor encodes the position of the next page, nil on the last page.
func nextCursor[P any](next *P) (*string, error) {
	if next == nil {
		return nil, nil
	}
	cursor, err := httpx.EncodeCursor(next)
	if err != nil {
		return nil, err
	}
	return &cursor, nil
}

// ListAdminActions answers a page of the journal of the admin changes.
func (h *Handler) ListAdminActions(ctx context.Context, req api.ListAdminActionsRequestObject) (api.ListAdminActionsResponseObject, error) {
	actor, ok := auth.ActorFrom(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	p := req.Params
	q := ActionQuery{Limit: limitOf(p.Limit)}
	if p.TargetType != nil {
		q.TargetType = string(*p.TargetType)
	}
	if p.TargetId != nil {
		q.TargetID = p.TargetId.String()
	}
	if p.AdminId != nil {
		q.AdminID = p.AdminId.String()
	}
	if p.ChallengeId != nil {
		q.ChallengeID = p.ChallengeId.String()
	}
	page, err := h.service.Actions(ctx, actor, q, cursorOf(p.Cursor))
	if err != nil {
		return nil, err
	}
	out := api.AdminActionPage{Items: make([]api.AdminAction, len(page.Items))}
	for i, a := range page.Items {
		if out.Items[i], err = toAdminAction(a); err != nil {
			return nil, err
		}
	}
	if out.NextCursor, err = nextCursor(page.Next); err != nil {
		return nil, err
	}
	return api.ListAdminActions200JSONResponse(out), nil
}

func toAdminAction(a Action) (api.AdminAction, error) {
	id, err := uuid.Parse(a.ID)
	if err != nil {
		return api.AdminAction{}, fmt.Errorf("admin action id: %w", err)
	}
	admin, err := toUserRef(a.Admin)
	if err != nil {
		return api.AdminAction{}, err
	}
	target, err := uuid.Parse(a.TargetID)
	if err != nil {
		return api.AdminAction{}, fmt.Errorf("admin action target id: %w", err)
	}
	challenges := make([]uuid.UUID, len(a.ChallengeIDs))
	for i, c := range a.ChallengeIDs {
		if challenges[i], err = uuid.Parse(c); err != nil {
			return api.AdminAction{}, fmt.Errorf("admin action challenge id: %w", err)
		}
	}
	oldValue, err := jsonMap(a.OldValue)
	if err != nil {
		return api.AdminAction{}, fmt.Errorf("admin action old value: %w", err)
	}
	newValue, err := jsonMap(a.NewValue)
	if err != nil {
		return api.AdminAction{}, fmt.Errorf("admin action new value: %w", err)
	}
	return api.AdminAction{
		Id:           id,
		Admin:        admin,
		CreatedAt:    a.CreatedAt.UTC(),
		Action:       a.Action,
		TargetType:   api.AdminActionTargetType(a.TargetType),
		TargetId:     target,
		ChallengeIds: challenges,
		OldValue:     oldValue,
		NewValue:     newValue,
		Reason:       a.Reason,
	}, nil
}

// jsonMap decodes a JSON object of the journal, nil for none.
func jsonMap(v []byte) (*map[string]any, error) {
	if v == nil {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal(v, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
