package activity

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
)

// AdminWrite is an admin change of an activity, checked and ready to write
// in the transaction that prepared it (AdminRecord, AdminUpdate,
// AdminDelete).
type AdminWrite struct {
	// Touched are the challenges whose rankings the activity is part of
	// before the change and after it (Touched), in the order of the
	// participations of its author: those the caller recomputes.
	Touched []string
	// Write makes the change, with no week lock nor end of challenge.
	Write func(ctx context.Context) (AdminChange, error)
}

// AdminChange is what an admin change of an activity wrote, for the
// journal: its fields before (Old, nil when created) and after (New, nil
// when deleted); the changed ones only for a replacement.
type AdminChange struct {
	ActivityID string
	Old        map[string]any
	New        map[string]any
}

// The admin use cases below change the activity of anyone as actor, an
// admin (AdminRefusal, 404 not_found otherwise), in any week, locked ones
// and those of a finished challenge included (README §2.12), with the
// validations of an entry (Validate, overlap). Each one runs in the
// transaction ctx carries (admin.Corrections, which took the week lock
// first): it checks the change and locks the row of the account of the
// activity, before reading where it counts, then gives the write to make.
// The caller records the change and recomputes the challenges touched in
// the same transaction.

// AdminRecord prepares the recording of draft for userID, whose profile
// must be complete (422 profile_incomplete): an unknown or deleted account
// answers 404 user_not_found. The source is manual, as an entry by the
// account: the journal tells them apart.
func (s *Service) AdminRecord(ctx context.Context, actor auth.Actor, userID string, draft Draft) (AdminWrite, error) {
	draft, err := s.adminDraft(ctx, actor, draft)
	if err != nil {
		return AdminWrite{}, err
	}
	if err := s.lockAccount(ctx, userID, apperr.CodeUserNotFound); err != nil {
		return AdminWrite{}, err
	}
	complete, err := s.profiles.ProfileComplete(ctx, userID)
	if err != nil {
		return AdminWrite{}, fmt.Errorf("check profile: %w", err)
	}
	if !complete {
		return AdminWrite{}, apperr.New(apperr.CodeProfileIncomplete, nil)
	}
	ps, err := s.participations.Participations(ctx, userID)
	if err != nil {
		return AdminWrite{}, fmt.Errorf("read participations: %w", err)
	}
	a := draftActivity(draft)
	a.UserID, a.Source = userID, SourceManual
	return AdminWrite{
		Touched: Touched(ps, a.StartedAt),
		Write: func(ctx context.Context) (AdminChange, error) {
			stored, _, err := s.repo.Record(ctx, a, anyTime)
			if err != nil {
				return AdminChange{}, storageError(err)
			}
			return AdminChange{ActivityID: stored.ID, New: journalFields(stored)}, nil
		},
	}, nil
}

// AdminUpdate prepares the replacement of activity id with draft, as Update
// replaces it: a missing or deleted activity, or that of a deleted account,
// answers 404 activity_not_found. The challenges touched are those of its
// current start and of the new one.
func (s *Service) AdminUpdate(ctx context.Context, actor auth.Actor, id string, draft Draft) (AdminWrite, error) {
	draft, err := s.adminDraft(ctx, actor, draft)
	if err != nil {
		return AdminWrite{}, err
	}
	current, ps, err := s.adminTarget(ctx, id)
	if err != nil {
		return AdminWrite{}, err
	}
	a := draftActivity(draft)
	return AdminWrite{
		Touched: Touched(ps, current.StartedAt, a.StartedAt),
		Write: func(ctx context.Context) (AdminChange, error) {
			stored, err := s.repo.Update(ctx, current.UserID, id, a, anyActivity)
			if err != nil {
				return AdminChange{}, storageError(err)
			}
			old, changed := changedFields(journalFields(current), journalFields(stored))
			return AdminChange{ActivityID: id, Old: old, New: changed}, nil
		},
	}, nil
}

// AdminDelete prepares the deletion of activity id, as Delete marks it
// deleted: a missing or deleted activity, or that of a deleted account,
// answers 404 activity_not_found.
func (s *Service) AdminDelete(ctx context.Context, actor auth.Actor, id string) (AdminWrite, error) {
	if err := AdminRefusal(actor.Admin); err != nil {
		return AdminWrite{}, err
	}
	current, ps, err := s.adminTarget(ctx, id)
	if err != nil {
		return AdminWrite{}, err
	}
	return AdminWrite{
		Touched: Touched(ps, current.StartedAt),
		Write: func(ctx context.Context) (AdminChange, error) {
			if err := s.repo.Delete(ctx, current.UserID, id, s.clock.Now(), anyActivity); err != nil {
				return AdminChange{}, storageError(err)
			}
			return AdminChange{ActivityID: id, Old: journalFields(current)}, nil
		},
	}, nil
}

// adminDraft refuses actor unless an admin (AdminRefusal), then validates
// draft as an entry (validate).
func (s *Service) adminDraft(ctx context.Context, actor auth.Actor, draft Draft) (Draft, error) {
	if err := AdminRefusal(actor.Admin); err != nil {
		return Draft{}, err
	}
	return s.validate(ctx, draft)
}

// adminTarget reads activity id, locks the row of its account, then reads
// the participations of that account. The activity cannot change meanwhile:
// the transaction holds the week lock alone, which every activity write
// takes shared, and the account deletion that would erase it is held back
// by the lock of its row.
func (s *Service) adminTarget(ctx context.Context, id string) (Activity, []Participation, error) {
	current, err := s.repo.Get(ctx, id)
	if err != nil {
		return Activity{}, nil, storageError(err)
	}
	if err := s.lockAccount(ctx, current.UserID, apperr.CodeActivityNotFound); err != nil {
		return Activity{}, nil, err
	}
	ps, err := s.participations.Participations(ctx, current.UserID)
	if err != nil {
		return Activity{}, nil, fmt.Errorf("read participations: %w", err)
	}
	return current, ps, nil
}

// lockAccount locks the row of account userID (Repository.LockAccount),
// before any read of where its activities count, in the order of an account
// deletion: an unknown or deleted account answers missing, with no other
// detail.
func (s *Service) lockAccount(ctx context.Context, userID string, missing apperr.Code) error {
	err := s.repo.LockAccount(ctx, userID)
	if errors.Is(err, errAccountDeleted) {
		return apperr.New(missing, nil)
	}
	if err != nil {
		return fmt.Errorf("lock account: %w", err)
	}
	return nil
}

// anyTime and anyActivity decide an admin write: no week lock applies.
func anyTime(context.Context) error               { return nil }
func anyActivity(context.Context, Activity) error { return nil }

// draftActivity is the activity of draft, its fields entered only.
func draftActivity(d Draft) Activity {
	return Activity{
		SportCode:           d.SportCode,
		SportLabel:          d.SportLabel,
		StartedAt:           d.StartedAt.UTC(),
		DurationSeconds:     d.DurationSeconds,
		DistanceMeters:      d.DistanceMeters,
		ElevationGainMeters: d.ElevationGainMeters,
	}
}

// journalFields are the fields entered of a, as the journal keeps them: a
// field not entered is nil.
func journalFields(a Activity) map[string]any {
	return map[string]any{
		"sport_code":            a.SportCode,
		"sport_label":           deref(a.SportLabel),
		"started_at":            a.StartedAt.UTC().Format(time.RFC3339Nano),
		"duration_seconds":      a.DurationSeconds,
		"distance_meters":       deref(a.DistanceMeters),
		"elevation_gain_meters": deref(a.ElevationGainMeters),
	}
}

// changedFields keeps the fields of before and after whose values differ.
func changedFields(before, after map[string]any) (map[string]any, map[string]any) {
	old, changed := map[string]any{}, map[string]any{}
	for k := range after {
		if !reflect.DeepEqual(before[k], after[k]) {
			old[k], changed[k] = before[k], after[k]
		}
	}
	return old, changed
}

// deref is the value of p, nil for none.
func deref[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}
