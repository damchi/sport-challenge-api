package admin

import (
	"context"

	"github.com/damchi/sport-challenge-api/internal/activity"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
)

// The journal codes of the admin changes of an activity.
const (
	ActionActivityCreate = "activity.create"
	ActionActivityUpdate = "activity.update"
	ActionActivityDelete = "activity.delete"
)

// ActivityWriter prepares the admin changes of an activity, in the
// transaction ctx carries, each one first locking the row of the account of
// the activity (module activity).
type ActivityWriter interface {
	AdminRecord(ctx context.Context, actor auth.Actor, userID string, draft activity.Draft) (activity.AdminWrite, error)
	AdminUpdate(ctx context.Context, actor auth.Actor, id string, draft activity.Draft) (activity.AdminWrite, error)
	AdminDelete(ctx context.Context, actor auth.Actor, id string) (activity.AdminWrite, error)
}

// ActivityCorrections are the admin changes of the activity of anyone, in
// any week (README §2.12): each one, its journal line (the activity as the
// target, every challenge where it counts before or after the change in
// challenge_ids) and the recompute of those challenges, in one transaction
// (Corrections.Run), rolled back with dryRun.
type ActivityCorrections struct {
	corrections *Corrections
	activities  ActivityWriter
}

// NewActivityCorrections returns ActivityCorrections.
func NewActivityCorrections(corrections *Corrections, activities ActivityWriter) *ActivityCorrections {
	return &ActivityCorrections{corrections: corrections, activities: activities}
}

// Record records draft for account userID.
func (s *ActivityCorrections) Record(ctx context.Context, actor auth.Actor, userID string, draft activity.Draft, reason string, dryRun bool) ([]ChallengePreview, error) {
	return s.run(ctx, actor, ActionActivityCreate, func(ctx context.Context) (activity.AdminWrite, error) {
		return s.activities.AdminRecord(ctx, actor, userID, draft)
	}, reason, dryRun)
}

// Update replaces activity id with draft.
func (s *ActivityCorrections) Update(ctx context.Context, actor auth.Actor, id string, draft activity.Draft, reason string, dryRun bool) ([]ChallengePreview, error) {
	return s.run(ctx, actor, ActionActivityUpdate, func(ctx context.Context) (activity.AdminWrite, error) {
		return s.activities.AdminUpdate(ctx, actor, id, draft)
	}, reason, dryRun)
}

// Delete deletes activity id.
func (s *ActivityCorrections) Delete(ctx context.Context, actor auth.Actor, id, reason string, dryRun bool) ([]ChallengePreview, error) {
	return s.run(ctx, actor, ActionActivityDelete, func(ctx context.Context) (activity.AdminWrite, error) {
		return s.activities.AdminDelete(ctx, actor, id)
	}, reason, dryRun)
}

// run prepares the change with prepare, which gives the challenges it
// touches before anything is written, then writes it, recorded under
// action, in Corrections.Run.
func (s *ActivityCorrections) run(ctx context.Context, actor auth.Actor, action string, prepare func(ctx context.Context) (activity.AdminWrite, error), reason string, dryRun bool) ([]ChallengePreview, error) {
	var w activity.AdminWrite
	return s.corrections.Run(ctx, actor, Correction{
		Touched: func(ctx context.Context) ([]string, error) {
			var err error
			w, err = prepare(ctx)
			return w.Touched, err
		},
		Apply: func(ctx context.Context) (Entry, error) {
			c, err := w.Write(ctx)
			if err != nil {
				return Entry{}, err
			}
			return Entry{
				Action: action, TargetType: TargetActivity, TargetID: c.ActivityID,
				OldValue: c.Old, NewValue: c.New,
			}, nil
		},
		Reason: reason,
		DryRun: dryRun,
	})
}
