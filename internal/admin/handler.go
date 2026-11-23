package admin

import (
	"context"

	"github.com/damchi/sport-challenge-api/internal/api"
	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
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
