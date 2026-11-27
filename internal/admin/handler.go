package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/damchi/sport-challenge-api/internal/activity"
	"github.com/damchi/sport-challenge-api/internal/api"
	"github.com/damchi/sport-challenge-api/internal/challenge"
	"github.com/damchi/sport-challenge-api/internal/leaderboard"
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/httpx"
	"github.com/damchi/sport-challenge-api/internal/user"
)

// routePrefix starts the path of every /admin route of the contract.
// TestEveryAdminRouteIsHiddenWithoutThePermission (cmd/api) walks the routes
// under it with malformed requests.
const routePrefix = "/api/v1/admin/"

// HideInvalidRequest is the httpx.Gate of the API: an invalid request to an
// /admin route answers as RequireAdmin refuses its actor, 404 not_found
// without the admin permission, before its validation error could reveal
// that the route exists (README §5.7). An admin, and any other route, keep
// the validation error. The decision is RequireAdmin's: only the shape of
// the refusal of an invalid request changes.
func HideInvalidRequest(c *gin.Context) error {
	if !strings.HasPrefix(c.FullPath(), routePrefix) {
		return nil
	}
	// No local user (never behind Protect): the zero actor, not an admin.
	actor, _ := auth.ActorFrom(c.Request.Context())
	return RequireAdmin(actor)
}

// Handler implements the /admin operations of the API contract.
type Handler struct {
	service    *Service
	settings   challengeUpdater
	members    memberUpdater
	activities activityUpdater
	accounts   accountUpdater
}

// challengeUpdater changes a challenge itself as an admin: its settings,
// its deletion and its restore (ChallengeCorrections).
type challengeUpdater interface {
	UpdateSettings(ctx context.Context, actor auth.Actor, id string, changes challenge.SettingsChanges, reason string, dryRun bool) ([]ChallengePreview, error)
	Delete(ctx context.Context, actor auth.Actor, id, reason string, dryRun bool) ([]ChallengePreview, error)
	Restore(ctx context.Context, actor auth.Actor, id string, creatorID *string, reason string, dryRun bool) ([]ChallengePreview, error)
}

// memberUpdater changes the members of a challenge as an admin
// (MemberCorrections).
type memberUpdater interface {
	AddParticipant(ctx context.Context, actor auth.Actor, id, userID, reason string, dryRun bool) ([]ChallengePreview, error)
	ReinstateParticipant(ctx context.Context, actor auth.Actor, id, userID, reason string, dryRun bool) ([]ChallengePreview, error)
	ExcludeParticipant(ctx context.Context, actor auth.Actor, id, userID, reason string, dryRun bool) ([]ChallengePreview, error)
	AddCreator(ctx context.Context, actor auth.Actor, id, userID, reason string, dryRun bool) ([]ChallengePreview, error)
	RemoveCreator(ctx context.Context, actor auth.Actor, id, userID, reason string, dryRun bool) ([]ChallengePreview, error)
}

// activityUpdater changes the activity of anyone as an admin
// (ActivityCorrections).
type activityUpdater interface {
	Record(ctx context.Context, actor auth.Actor, userID string, idempotencyKey *string, draft activity.Draft, reason string, dryRun bool) ([]ChallengePreview, error)
	Update(ctx context.Context, actor auth.Actor, id string, draft activity.Draft, reason string, dryRun bool) ([]ChallengePreview, error)
	Delete(ctx context.Context, actor auth.Actor, id, reason string, dryRun bool) ([]ChallengePreview, error)
}

// accountUpdater changes any account as an admin (AccountCorrections).
type accountUpdater interface {
	UpdateProfile(ctx context.Context, actor auth.Actor, id string, changes user.ProfileChanges, reason string) error
	Block(ctx context.Context, actor auth.Actor, id, reason string) error
	Unblock(ctx context.Context, actor auth.Actor, id, reason string) error
	Delete(ctx context.Context, actor auth.Actor, id, reason string) error
}

// NewHandler returns a Handler on service for the reads, settings for the
// changes of a challenge itself (settings, deletion, restore), members for those of its
// members, activities for those of an activity, accounts for those of an
// account.
func NewHandler(service *Service, settings challengeUpdater, members memberUpdater, activities activityUpdater, accounts accountUpdater) *Handler {
	return &Handler{service: service, settings: settings, members: members, activities: activities, accounts: accounts}
}

// CreateAdminActivity records an activity for any account, in any week, or
// previews it with dry_run.
func (h *Handler) CreateAdminActivity(ctx context.Context, req api.CreateAdminActivityRequestObject) (api.CreateAdminActivityResponseObject, error) {
	actor, ok := auth.ActorFrom(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	// The contract requires the body: an empty one is refused there, and
	// here as one without its reason, once the permission is checked.
	b := activityBody(req.Body)
	previews, err := h.activities.Record(ctx, actor, req.UserId.String(), req.Params.IdempotencyKey, activityDraft(b), b.Reason, isDryRun(b.DryRun))
	if err != nil {
		return nil, err
	}
	out, err := toCorrection(previews, isDryRun(b.DryRun))
	if err != nil {
		return nil, err
	}
	return api.CreateAdminActivity200JSONResponse(out), nil
}

// UpdateAdminActivity replaces the activity of any account, in any week, or
// previews it with dry_run.
func (h *Handler) UpdateAdminActivity(ctx context.Context, req api.UpdateAdminActivityRequestObject) (api.UpdateAdminActivityResponseObject, error) {
	actor, ok := auth.ActorFrom(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	b := activityBody(req.Body)
	previews, err := h.activities.Update(ctx, actor, req.ActivityId.String(), activityDraft(b), b.Reason, isDryRun(b.DryRun))
	if err != nil {
		return nil, err
	}
	out, err := toCorrection(previews, isDryRun(b.DryRun))
	if err != nil {
		return nil, err
	}
	return api.UpdateAdminActivity200JSONResponse(out), nil
}

// DeleteAdminActivity deletes the activity of any account, in any week, or
// previews it with dry_run.
func (h *Handler) DeleteAdminActivity(ctx context.Context, req api.DeleteAdminActivityRequestObject) (api.DeleteAdminActivityResponseObject, error) {
	actor, ok := auth.ActorFrom(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	b := reasonBody(req.Body)
	previews, err := h.activities.Delete(ctx, actor, req.ActivityId.String(), b.Reason, isDryRun(b.DryRun))
	if err != nil {
		return nil, err
	}
	out, err := toCorrection(previews, isDryRun(b.DryRun))
	if err != nil {
		return nil, err
	}
	return api.DeleteAdminActivity200JSONResponse(out), nil
}

// reasonBody is b, or an empty body for none: the request is then refused
// as one without its reason, once the permission is checked
// (Corrections.Run).
func reasonBody(b *api.AdminCorrectionReason) *api.AdminCorrectionReason {
	if b == nil {
		return &api.AdminCorrectionReason{}
	}
	return b
}

// activityBody is b, or an empty body for none.
func activityBody(b *api.AdminActivityWrite) *api.AdminActivityWrite {
	if b == nil {
		return &api.AdminActivityWrite{}
	}
	return b
}

// activityDraft is the activity b carries, as an entry.
func activityDraft(b *api.AdminActivityWrite) activity.Draft {
	return activity.Draft{
		SportCode:           b.SportCode,
		SportLabel:          b.SportLabel,
		StartedAt:           b.StartedAt,
		DurationSeconds:     b.DurationSeconds,
		DistanceMeters:      b.DistanceMeters,
		ElevationGainMeters: b.ElevationGainMeters,
		Repetitions:         activity.RepetitionsOf(b.Repetitions),
	}
}

// isDryRun tells whether dry_run asks a preview, false when absent.
func isDryRun(dryRun *bool) bool {
	return dryRun != nil && *dryRun
}

// AddAdminParticipant makes any account a participant of a challenge, or
// previews it with dry_run.
func (h *Handler) AddAdminParticipant(ctx context.Context, req api.AddAdminParticipantRequestObject) (api.AddAdminParticipantResponseObject, error) {
	// The contract requires the body: an empty one is refused there.
	b := req.Body
	if b == nil {
		b = &api.AdminParticipantAdd{}
	}
	out, err := memberCorrection(ctx, h.members.AddParticipant, req.ChallengeId, b.UserId, &api.AdminCorrectionReason{Reason: b.Reason, DryRun: b.DryRun})
	if err != nil {
		return nil, err
	}
	return api.AddAdminParticipant200JSONResponse(out), nil
}

// ReinstateAdminParticipant cancels an abandon or an exclusion, or previews
// it with dry_run.
func (h *Handler) ReinstateAdminParticipant(ctx context.Context, req api.ReinstateAdminParticipantRequestObject) (api.ReinstateAdminParticipantResponseObject, error) {
	out, err := memberCorrection(ctx, h.members.ReinstateParticipant, req.ChallengeId, req.UserId, req.Body)
	if err != nil {
		return nil, err
	}
	return api.ReinstateAdminParticipant200JSONResponse(out), nil
}

// ExcludeAdminParticipant excludes any participant, a creator included, or
// previews it with dry_run.
func (h *Handler) ExcludeAdminParticipant(ctx context.Context, req api.ExcludeAdminParticipantRequestObject) (api.ExcludeAdminParticipantResponseObject, error) {
	out, err := memberCorrection(ctx, h.members.ExcludeParticipant, req.ChallengeId, req.UserId, req.Body)
	if err != nil {
		return nil, err
	}
	return api.ExcludeAdminParticipant200JSONResponse(out), nil
}

// AddAdminCreator makes a participant a creator, or previews it with
// dry_run.
func (h *Handler) AddAdminCreator(ctx context.Context, req api.AddAdminCreatorRequestObject) (api.AddAdminCreatorResponseObject, error) {
	out, err := memberCorrection(ctx, h.members.AddCreator, req.ChallengeId, req.UserId, req.Body)
	if err != nil {
		return nil, err
	}
	return api.AddAdminCreator200JSONResponse(out), nil
}

// RemoveAdminCreator removes a creator role, or previews it with dry_run.
func (h *Handler) RemoveAdminCreator(ctx context.Context, req api.RemoveAdminCreatorRequestObject) (api.RemoveAdminCreatorResponseObject, error) {
	out, err := memberCorrection(ctx, h.members.RemoveCreator, req.ChallengeId, req.UserId, req.Body)
	if err != nil {
		return nil, err
	}
	return api.RemoveAdminCreator200JSONResponse(out), nil
}

// memberCorrection runs change, one of the methods of memberUpdater, on
// userID in challengeID as the actor of ctx, with the reason and dry_run of
// body, and gives its previews in their contract form. Without a body, the
// request is refused as one without its reason, once the permission is
// checked (Corrections.Run).
func memberCorrection(ctx context.Context, change func(ctx context.Context, actor auth.Actor, id, userID, reason string, dryRun bool) ([]ChallengePreview, error),
	challengeID, userID uuid.UUID, body *api.AdminCorrectionReason,
) (api.AdminCorrection, error) {
	actor, ok := auth.ActorFrom(ctx)
	if !ok {
		return api.AdminCorrection{}, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	body = reasonBody(body)
	dryRun := isDryRun(body.DryRun)
	previews, err := change(ctx, actor, challengeID.String(), userID.String(), body.Reason, dryRun)
	if err != nil {
		return api.AdminCorrection{}, err
	}
	return toCorrection(previews, dryRun)
}

// UpdateAdminChallenge changes the settings of a challenge, or previews
// the change with dry_run.
func (h *Handler) UpdateAdminChallenge(ctx context.Context, req api.UpdateAdminChallengeRequestObject) (api.UpdateAdminChallengeResponseObject, error) {
	actor, ok := auth.ActorFrom(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	// Without a body, the request is refused as one without its reason,
	// once the permission is checked (ChallengeCorrections).
	b := req.Body
	if b == nil {
		b = &api.AdminChallengeUpdate{}
	}
	changes := challenge.SettingsChanges{
		Title: b.Title, Description: b.Description, MembersCanInvite: b.MembersCanInvite,
		Timezone: b.Timezone, Weeks: b.Weeks, ReferenceSport: b.ReferenceSport,
	}
	if b.Visibility != nil {
		v := string(*b.Visibility)
		changes.Visibility = &v
	}
	if b.StartDate != nil {
		d := b.StartDate.String()
		changes.StartDate = &d
	}
	if b.Categories != nil {
		choice := challenge.CategoryChoiceOf(*b.Categories)
		changes.Categories = &choice
	}
	if b.DistanceFactors != nil {
		changes.DistanceFactors = *b.DistanceFactors
	}
	dryRun := b.DryRun != nil && *b.DryRun
	previews, err := h.settings.UpdateSettings(ctx, actor, req.ChallengeId.String(), changes, b.Reason, dryRun)
	if err != nil {
		return nil, err
	}
	out, err := toCorrection(previews, dryRun)
	if err != nil {
		return nil, err
	}
	return api.UpdateAdminChallenge200JSONResponse(out), nil
}

// DeleteAdminChallenge deletes a challenge, or previews the deletion with
// dry_run.
func (h *Handler) DeleteAdminChallenge(ctx context.Context, req api.DeleteAdminChallengeRequestObject) (api.DeleteAdminChallengeResponseObject, error) {
	actor, ok := auth.ActorFrom(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	b := reasonBody(req.Body)
	previews, err := h.settings.Delete(ctx, actor, req.ChallengeId.String(), b.Reason, isDryRun(b.DryRun))
	if err != nil {
		return nil, err
	}
	out, err := toCorrection(previews, isDryRun(b.DryRun))
	if err != nil {
		return nil, err
	}
	return api.DeleteAdminChallenge200JSONResponse(out), nil
}

// RestoreAdminChallenge restores a deleted challenge, or previews the
// restore with dry_run.
func (h *Handler) RestoreAdminChallenge(ctx context.Context, req api.RestoreAdminChallengeRequestObject) (api.RestoreAdminChallengeResponseObject, error) {
	actor, ok := auth.ActorFrom(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	b := req.Body
	if b == nil {
		b = &api.RestoreAdminChallengeJSONRequestBody{}
	}
	var creatorID *string
	if b.CreatorUserId != nil {
		id := b.CreatorUserId.String()
		creatorID = &id
	}
	previews, err := h.settings.Restore(ctx, actor, req.ChallengeId.String(), creatorID, b.Reason, isDryRun(b.DryRun))
	if err != nil {
		return nil, err
	}
	out, err := toCorrection(previews, isDryRun(b.DryRun))
	if err != nil {
		return nil, err
	}
	return api.RestoreAdminChallenge200JSONResponse(out), nil
}

// toCorrection turns the previews of an admin change into their contract
// form.
func toCorrection(previews []ChallengePreview, dryRun bool) (api.AdminCorrection, error) {
	out := api.AdminCorrection{DryRun: dryRun, Challenges: make([]api.AdminChallengePreview, len(previews))}
	for i, p := range previews {
		id, err := uuid.Parse(p.ChallengeID)
		if err != nil {
			return api.AdminCorrection{}, fmt.Errorf("challenge id: %w", err)
		}
		before, err := toBoard(p.Before)
		if err != nil {
			return api.AdminCorrection{}, err
		}
		after, err := toBoard(p.After)
		if err != nil {
			return api.AdminCorrection{}, err
		}
		changes := make([]api.AdminEliminationChange, len(p.Eliminations))
		for j, e := range p.Eliminations {
			user, err := toUserRef(e.User)
			if err != nil {
				return api.AdminCorrection{}, err
			}
			changes[j] = api.AdminEliminationChange{User: user, Before: toJudgement(e.Before), After: toJudgement(e.After)}
		}
		weeks := make([]api.AdminWeekChange, len(p.Weeks))
		for j, w := range p.Weeks {
			change := api.Recomputed
			if w.Locked {
				change = api.JudgedAgain
			}
			weeks[j] = api.AdminWeekChange{WeekNumber: w.Number, Change: change}
		}
		points := make([]api.AdminPointsChange, len(p.Points))
		for j, pc := range p.Points {
			user, err := toUserRef(pc.User)
			if err != nil {
				return api.AdminCorrection{}, err
			}
			points[j] = api.AdminPointsChange{User: user, WeekNumber: pc.Week, Before: pc.Before, After: pc.After, Provisional: pc.Provisional}
		}
		var results *api.AdminChallengePreviewResults
		if p.Results != "" {
			r := api.AdminChallengePreviewResults(p.Results)
			results = &r
		}
		out.Challenges[i] = api.AdminChallengePreview{
			ChallengeId: id, Title: p.Title, Status: api.AdminChallengePreviewStatus(p.Status),
			ReferenceSport: api.AdminReferenceSportChange{Before: p.ReferenceSport.Before, After: p.ReferenceSport.After},
			Results:        results, Weeks: weeks, Points: points,
			Before: before, After: after, Eliminations: changes,
		}
	}
	return out, nil
}

// toBoard turns rankings into their contract form, nil for none.
func toBoard(b *leaderboard.Leaderboard) (*api.Leaderboard, error) {
	if b == nil {
		return nil, nil
	}
	out, err := leaderboard.Contract(*b)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// toJudgement turns an elimination into its contract form, nil for none.
func toJudgement(e *EliminationRow) *api.AdminEliminationJudgement {
	if e == nil {
		return nil
	}
	out := &api.AdminEliminationJudgement{WeekNumber: e.WeekNumber}
	out.DistanceMeters, out.RequiredMeters, out.Repetitions, out.RequiredRepetitions = eliminationMeasures(*e)
	return out
}

// eliminationMeasures gives the distance and minimum of e in meters, or,
// in an elimination by repetitions, 0 and both in repetitions.
func eliminationMeasures(e EliminationRow) (meters, requiredMeters int, repetitions, requiredRepetitions *int) {
	if e.InRepetitions {
		return 0, 0, &e.DistanceMeters, &e.RequiredMeters
	}
	return e.DistanceMeters, e.RequiredMeters, nil, nil
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
			Blocked:         int(s.AccountsBlocked),
		},
		Challenges: api.AdminChallengeCounts{
			Upcoming: int(s.Upcoming),
			Ongoing:  int(s.Ongoing),
			Finished: int(s.Finished),
			Deleted:  int(s.Deleted),
		},
		ActivitiesLast7Days:   int(s.Activities),
		PendingAuth0Deletions: int(s.PendingAuth0Deletions),
		BlockedAuth0Deletions: int(s.BlockedAuth0Deletions),
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
		el := api.AdminElimination{User: user, WeekNumber: e.WeekNumber}
		el.DistanceMeters, el.RequiredMeters, el.Repetitions, el.RequiredRepetitions = eliminationMeasures(e)
		out.Eliminations[i] = el
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
	out, err := h.adminUser(ctx, actor, req.UserId.String())
	if err != nil {
		return nil, err
	}
	return api.GetAdminUser200JSONResponse(out), nil
}

// UpdateAdminUser changes the pseudo and the names of any account, then
// answers it.
func (h *Handler) UpdateAdminUser(ctx context.Context, req api.UpdateAdminUserRequestObject) (api.UpdateAdminUserResponseObject, error) {
	actor, ok := auth.ActorFrom(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	// The contract requires the body: without one, the request is refused
	// as one without its reason, once the permission is checked
	// (Corrections.Run).
	b := req.Body
	if b == nil {
		b = &api.AdminUserUpdate{}
	}
	changes := user.ProfileChanges{FirstName: b.FirstName, LastName: b.LastName}
	if b.Pseudo.IsSpecified() {
		if b.Pseudo.IsNull() {
			changes.RemovePseudo = true
		} else {
			pseudo := b.Pseudo.MustGet()
			changes.Pseudo = &pseudo
		}
	}
	id := req.UserId.String()
	if err := h.accounts.UpdateProfile(ctx, actor, id, changes, b.Reason); err != nil {
		return nil, err
	}
	out, err := h.adminUser(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	return api.UpdateAdminUser200JSONResponse(out), nil
}

// BlockAdminUser blocks any account but that of the admin, then answers it.
func (h *Handler) BlockAdminUser(ctx context.Context, req api.BlockAdminUserRequestObject) (api.BlockAdminUserResponseObject, error) {
	out, err := h.accountChange(ctx, h.accounts.Block, req.UserId, req.Body)
	if err != nil {
		return nil, err
	}
	return api.BlockAdminUser200JSONResponse(out), nil
}

// UnblockAdminUser unblocks any account, then answers it.
func (h *Handler) UnblockAdminUser(ctx context.Context, req api.UnblockAdminUserRequestObject) (api.UnblockAdminUserResponseObject, error) {
	out, err := h.accountChange(ctx, h.accounts.Unblock, req.UserId, req.Body)
	if err != nil {
		return nil, err
	}
	return api.UnblockAdminUser200JSONResponse(out), nil
}

// accountChange runs change, Block or Unblock of accountUpdater, on account
// userID as the actor of ctx, with the reason of body, and answers the
// account after it. Without a body, the request is refused as one without
// its reason, once the permission is checked (Corrections.Run).
func (h *Handler) accountChange(ctx context.Context, change func(ctx context.Context, actor auth.Actor, id, reason string) error,
	userID uuid.UUID, body *api.AdminAccountReason,
) (api.AdminUser, error) {
	actor, ok := auth.ActorFrom(ctx)
	if !ok {
		return api.AdminUser{}, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	if body == nil {
		body = &api.AdminAccountReason{}
	}
	id := userID.String()
	if err := change(ctx, actor, id, body.Reason); err != nil {
		return api.AdminUser{}, err
	}
	return h.adminUser(ctx, actor, id)
}

// DeleteAdminUser deletes any account, as DELETE /me would.
func (h *Handler) DeleteAdminUser(ctx context.Context, req api.DeleteAdminUserRequestObject) (api.DeleteAdminUserResponseObject, error) {
	actor, ok := auth.ActorFrom(ctx)
	if !ok {
		return nil, apperr.New(apperr.CodeUnauthenticated, nil)
	}
	b := req.Body
	if b == nil {
		b = &api.AdminAccountReason{}
	}
	if err := h.accounts.Delete(ctx, actor, req.UserId.String(), b.Reason); err != nil {
		return nil, err
	}
	return api.DeleteAdminUser204Response{}, nil
}

// adminUser reads account id once changed, in its contract form.
func (h *Handler) adminUser(ctx context.Context, actor auth.Actor, id string) (api.AdminUser, error) {
	u, err := h.service.User(ctx, actor, id)
	if err != nil {
		return api.AdminUser{}, err
	}
	return toAdminUser(u)
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
			Timezone:    c.Timezone,
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
			Contributions:       make([]api.ActivityContribution, len(a.Contributions)),
			Locked:              a.Locked,
			Repetitions:         activity.APIRepetitions(a.Repetitions),
		}
		if a.DistanceMeters != nil {
			v := activity.Speed(*a.DistanceMeters, a.DurationSeconds)
			out.Items[i].SpeedMps, out.Items[i].PaceSecondsPerKm = &v.MetersPerSecond, &v.SecondsPerKilometer
		}
		if c := a.AdminChange; c != nil {
			adminID, err := uuid.Parse(c.AdminID)
			if err != nil {
				return nil, fmt.Errorf("admin id: %w", err)
			}
			out.Items[i].AdminChange = &api.AdminActivityChange{Action: api.AdminActivityChangeAction(c.Action), At: c.At.UTC(), AdminId: adminID}
		}
		for j, c := range a.Contributions {
			cid, err := uuid.Parse(c.ChallengeID)
			if err != nil {
				return nil, fmt.Errorf("challenge id: %w", err)
			}
			out.Items[i].Contributions[j] = api.ActivityContribution{
				ChallengeId: cid, ChallengeTitle: c.ChallengeTitle, DistanceMeters: c.DistanceMeters, DurationSeconds: c.DurationSeconds,
			}
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
