package activity

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
)

const actor = "0b9d4a5e-6a43-4c8e-9d1f-7a2b3c4d5e6f"

// fakeRepository records like Repository, without a database: existing is
// the activity a key gives, nil when the key is new; stored holds the
// activities Update and Delete find, by id.
type fakeRepository struct {
	existing *Activity
	stored   map[string]Activity
	err      error
	// recorded, updated and deleted are what was written; decided tells
	// whether decide ran.
	recorded  *Activity
	updated   *Activity
	deleted   string
	deletedAt time.Time
	decided   bool
}

func (f *fakeRepository) Update(ctx context.Context, _, id string, a Activity, decide func(context.Context, Activity) error) (Activity, error) {
	current, err := f.find(ctx, id, decide)
	if err != nil {
		return Activity{}, fmt.Errorf("update activity %s: %w", id, err)
	}
	out := current
	out.SportCode, out.SportLabel, out.StartedAt = a.SportCode, a.SportLabel, a.StartedAt
	out.DurationSeconds, out.DistanceMeters = a.DurationSeconds, a.DistanceMeters
	f.updated = &out
	return out, nil
}

func (f *fakeRepository) Delete(ctx context.Context, _, id string, at time.Time, decide func(context.Context, Activity) error) error {
	if _, err := f.find(ctx, id, decide); err != nil {
		return fmt.Errorf("delete activity %s: %w", id, err)
	}
	f.deleted, f.deletedAt = id, at
	return nil
}

// find gives activity id to decide, as Repository.change does.
func (f *fakeRepository) find(ctx context.Context, id string, decide func(context.Context, Activity) error) (Activity, error) {
	if f.err != nil {
		return Activity{}, f.err
	}
	current, ok := f.stored[id]
	if !ok {
		return Activity{}, errActivityNotFound
	}
	f.decided = true
	return current, decide(ctx, current)
}

func (f *fakeRepository) Record(ctx context.Context, a Activity, decide func(context.Context) error) (Activity, bool, error) {
	if f.err != nil {
		return Activity{}, false, f.err
	}
	if f.existing != nil && a.IdempotencyKey != nil {
		return *f.existing, false, nil
	}
	f.decided = true
	if err := decide(ctx); err != nil {
		return Activity{}, false, fmt.Errorf("record activity: %w", err)
	}
	a.ID = "5f1e7c1a-0000-4000-8000-000000000001"
	f.recorded = &a
	return a, true, nil
}

type fakeProfiles struct {
	complete bool
	err      error
}

func (f fakeProfiles) ProfileComplete(context.Context, string) (bool, error) {
	return f.complete, f.err
}

// fakeCatalogue has the sports of modes, by code.
type fakeCatalogue struct {
	modes map[string]string
	err   error
}

func (f fakeCatalogue) DistanceMode(_ context.Context, code string) (string, bool, error) {
	mode, ok := f.modes[code]
	return mode, ok, f.err
}

var catalogue = fakeCatalogue{modes: map[string]string{"run": distanceRequired, "yoga": distanceNone, "other": distanceOptional}}

// fakeParticipations gives challenges for actor only; calls counts them.
type fakeParticipations struct {
	t          *testing.T
	challenges []Participation
	err        error
	calls      *int
}

func (f fakeParticipations) Participations(_ context.Context, userID string) ([]Participation, error) {
	if userID != actor {
		f.t.Errorf("participations of %q, want the actor's", userID)
	}
	if f.calls != nil {
		*f.calls++
	}
	return f.challenges, f.err
}

// recordNow is a Thursday in week 2 of challengeOf (from Monday 2026-06-01,
// Toronto, 3 weeks): week 1 ended on 2026-06-08.
var recordNow = time.Date(2026, 6, 11, 16, 0, 0, 0, time.UTC)

func challengeOf(t *testing.T, id string) Participation {
	t.Helper()
	p := participation(t, [3]int{2026, 6, 1}, 3)
	p.ChallengeID = id
	return p
}

func run(startedAt time.Time) Draft {
	d := 5000
	return Draft{SportCode: "run", StartedAt: startedAt, DurationSeconds: 1500, DistanceMeters: &d}
}

func newService(t *testing.T, repo *fakeRepository, ps fakeParticipations) *Service {
	t.Helper()
	ps.t = t
	return NewService(repo, fakeProfiles{complete: true}, catalogue, ps, clock.NewFake(recordNow))
}

func TestServiceRecord(t *testing.T) {
	inWeek2 := toronto(t, 2026, 6, 10, 7)
	c1, c2 := challengeOf(t, "c1"), challengeOf(t, "c2")
	notActive := challengeOf(t, "c3")
	notActive.Active = false

	tests := []struct {
		name       string
		draft      Draft
		challenges []Participation
		want       []string
	}{
		{name: "outside every window: recorded, counts nowhere", draft: run(toronto(t, 2026, 5, 20, 7))},
		{name: "no challenge at all", draft: run(inWeek2)},
		{name: "two challenges", draft: run(inWeek2), challenges: []Participation{c1, c2}, want: []string{"c1", "c2"}},
		{name: "not active: counts nowhere, never refused", draft: run(inWeek2), challenges: []Participation{notActive, c1}, want: []string{"c1"}},
		{name: "sport without distance", draft: Draft{SportCode: "yoga", StartedAt: inWeek2, DurationSeconds: 3600}, challenges: []Participation{c1}, want: []string{"c1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{}
			got, err := newService(t, repo, fakeParticipations{challenges: tt.challenges}).Record(t.Context(), actor, nil, tt.draft)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Created || got.ID == "" || !reflect.DeepEqual(got.ChallengeIDs, tt.want) {
				t.Fatalf("Record() = %+v, want created, counting in %v", got, tt.want)
			}
			r := repo.recorded
			if r == nil || r.UserID != actor || r.Source != SourceManual || r.SportCode != tt.draft.SportCode || !r.StartedAt.Equal(tt.draft.StartedAt) || r.IdempotencyKey != nil {
				t.Fatalf("recorded %+v, want the draft for the actor, manual", r)
			}
		})
	}
}

func TestServiceRecordNormalizes(t *testing.T) {
	repo := &fakeRepository{}
	label := "  Stand   up paddle "
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	draft := Draft{SportCode: "other", SportLabel: &label, StartedAt: time.Date(2026, 6, 10, 7, 0, 0, 0, loc), DurationSeconds: 1800}
	got, err := newService(t, repo, fakeParticipations{}).Record(t.Context(), actor, nil, draft)
	if err != nil {
		t.Fatal(err)
	}
	if got.SportLabel == nil || *got.SportLabel != "Stand up paddle" || got.StartedAt.Location() != time.UTC {
		t.Fatalf("Record() = %+v, want the label cleaned and the start in UTC", got)
	}
}

func TestServiceRecordWeekLocked(t *testing.T) {
	c1 := challengeOf(t, "c1")
	finished := participation(t, [3]int{2026, 4, 27}, 2)
	finished.ChallengeID = "finished"
	tests := []struct {
		name       string
		startedAt  time.Time
		challenges []Participation
	}{
		{name: "week 1 ended", startedAt: toronto(t, 2026, 6, 3, 7), challenges: []Participation{c1}},
		{name: "in the window of a finished challenge", startedAt: toronto(t, 2026, 5, 2, 7), challenges: []Participation{finished}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{}
			_, err := newService(t, repo, fakeParticipations{challenges: tt.challenges}).Record(t.Context(), actor, nil, run(tt.startedAt))
			var appErr *apperr.Error
			if !errors.As(err, &appErr) || appErr.Code != apperr.CodeWeekLocked {
				t.Fatalf("error = %v, want week_locked", err)
			}
			if repo.recorded != nil {
				t.Fatal("a locked activity was recorded")
			}
		})
	}
}

func TestServiceRecordIdempotencyKey(t *testing.T) {
	key := "8d3f-key"
	existingStart := toronto(t, 2026, 6, 3, 7) // in week 1, locked now
	existing := &Activity{ID: "5f1e7c1a-0000-4000-8000-000000000009", UserID: actor, SportCode: "run", StartedAt: existingStart, DurationSeconds: 1200, Source: SourceManual, IdempotencyKey: &key}
	repo := &fakeRepository{existing: existing}
	calls := 0
	svc := newService(t, repo, fakeParticipations{challenges: []Participation{challengeOf(t, "c1")}, calls: &calls})

	// Another body with the same key: the activity recorded first, as it
	// is, and where it counts now; its locked week does not refuse it.
	got, err := svc.Record(t.Context(), actor, &key, run(toronto(t, 2026, 6, 10, 7)))
	if err != nil {
		t.Fatal(err)
	}
	if got.Created || got.ID != existing.ID || got.DurationSeconds != 1200 || !reflect.DeepEqual(got.ChallengeIDs, []string{"c1"}) {
		t.Fatalf("Record() = %+v, want the existing activity, not created, counting in c1", got)
	}
	if repo.decided || repo.recorded != nil || calls != 1 {
		t.Fatalf("decided %v, recorded %+v, %d reads: want nothing written, one read", repo.decided, repo.recorded, calls)
	}

	// A new key is recorded with the activity.
	repo = &fakeRepository{}
	other := "other-key"
	if _, err := newService(t, repo, fakeParticipations{}).Record(t.Context(), actor, &other, run(toronto(t, 2026, 6, 10, 7))); err != nil {
		t.Fatal(err)
	}
	if repo.recorded == nil || repo.recorded.IdempotencyKey == nil || *repo.recorded.IdempotencyKey != other {
		t.Fatalf("recorded %+v, want the key stored", repo.recorded)
	}
}

func TestServiceRecordRefusals(t *testing.T) {
	inWeek2 := toronto(t, 2026, 6, 10, 7)
	storage := errors.New("connection refused")
	yogaWithDistance := run(inWeek2)
	yogaWithDistance.SportCode = "yoga"
	tests := []struct {
		name       string
		incomplete bool
		profileErr error
		catalogue  fakeCatalogue
		repo       *fakeRepository
		ps         fakeParticipations
		draft      Draft
		code       apperr.Code
		wantErr    error
		noDecision bool
	}{
		{name: "profile incomplete", incomplete: true, draft: run(inWeek2), code: apperr.CodeProfileIncomplete, noDecision: true},
		{name: "unknown sport", draft: Draft{SportCode: "chess", StartedAt: inWeek2, DurationSeconds: 60}, code: apperr.CodeValidationFailed, noDecision: true},
		{name: "invalid draft", draft: Draft{SportCode: "yoga", StartedAt: recordNow.Add(time.Hour), DurationSeconds: 60}, code: apperr.CodeValidationFailed, noDecision: true},
		{name: "distance not allowed", draft: yogaWithDistance, code: apperr.CodeDistanceNotAllowed, noDecision: true},
		{name: "account deleted meanwhile", repo: &fakeRepository{err: errAccountDeleted}, draft: run(inWeek2), code: apperr.CodeAccountDeleted, noDecision: true},
		{name: "key of a deleted activity", repo: &fakeRepository{err: errActivityNotFound}, draft: run(inWeek2), code: apperr.CodeActivityNotFound, noDecision: true},
		{name: "profile read fails", profileErr: storage, draft: run(inWeek2), wantErr: storage, noDecision: true},
		{name: "catalogue read fails", catalogue: fakeCatalogue{err: storage}, draft: run(inWeek2), wantErr: storage, noDecision: true},
		{name: "participations read fails", ps: fakeParticipations{err: storage}, draft: run(inWeek2), wantErr: storage},
		{name: "storage fails", repo: &fakeRepository{err: storage}, draft: run(inWeek2), wantErr: storage, noDecision: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profiles := fakeProfiles{complete: !tt.incomplete, err: tt.profileErr}
			cat := tt.catalogue
			if cat.modes == nil {
				cat.modes = catalogue.modes
			}
			repo := tt.repo
			if repo == nil {
				repo = &fakeRepository{}
			}
			ps := tt.ps
			ps.t = t
			_, err := NewService(repo, profiles, cat, ps, clock.NewFake(recordNow)).Record(t.Context(), actor, nil, tt.draft)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("error = %v, want it to wrap %v", err, tt.wantErr)
				}
			} else {
				var appErr *apperr.Error
				if !errors.As(err, &appErr) || appErr.Code != tt.code {
					t.Fatalf("error = %v, want %s", err, tt.code)
				}
			}
			if repo.recorded != nil || (tt.noDecision && repo.decided) {
				t.Fatalf("recorded %+v, decided %v: want nothing written", repo.recorded, repo.decided)
			}
		})
	}
}

const activityID = "5f1e7c1a-0000-4000-8000-000000000002"

// storedRun is a run of the actor in week 2 of challengeOf, unlocked at
// recordNow, recorded with a key.
func storedRun(t *testing.T) Activity {
	t.Helper()
	key, d := "key", 5000
	return Activity{
		ID: activityID, UserID: actor, SportCode: "run", StartedAt: toronto(t, 2026, 6, 10, 7),
		DurationSeconds: 1500, DistanceMeters: &d, Source: SourceManual, IdempotencyKey: &key,
	}
}

func TestServiceUpdate(t *testing.T) {
	c1 := challengeOf(t, "c1")
	label := " Paddle  board "
	tests := []struct {
		name  string
		draft Draft
		want  []string
	}{
		{name: "within week 2", draft: run(toronto(t, 2026, 6, 9, 18)), want: []string{"c1"}},
		{name: "moved outside every window: counts nowhere", draft: run(toronto(t, 2026, 5, 20, 7))},
		{name: "another sport: the distance is cleared", draft: Draft{SportCode: "yoga", StartedAt: toronto(t, 2026, 6, 10, 7), DurationSeconds: 3600}, want: []string{"c1"}},
		{name: "sport other: the label is cleaned", draft: Draft{SportCode: "other", SportLabel: &label, StartedAt: toronto(t, 2026, 6, 10, 7), DurationSeconds: 3600}, want: []string{"c1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := storedRun(t)
			repo := &fakeRepository{stored: map[string]Activity{activityID: before}}
			got, err := newService(t, repo, fakeParticipations{challenges: []Participation{c1}}).Update(t.Context(), actor, activityID, tt.draft)
			if err != nil {
				t.Fatal(err)
			}
			if got.Created || got.ID != activityID || !reflect.DeepEqual(got.ChallengeIDs, tt.want) {
				t.Fatalf("Update() = %+v, want activity %s counting in %v", got, activityID, tt.want)
			}
			u := repo.updated
			if u == nil || u.SportCode != tt.draft.SportCode || !u.StartedAt.Equal(tt.draft.StartedAt) || u.StartedAt.Location() != time.UTC ||
				u.DurationSeconds != tt.draft.DurationSeconds || !reflect.DeepEqual(u.DistanceMeters, tt.draft.DistanceMeters) {
				t.Fatalf("updated %+v, want the draft in UTC", u)
			}
			if u.Source != before.Source || u.IdempotencyKey != before.IdempotencyKey || u.UserID != actor {
				t.Fatalf("updated %+v, want the source, the key and the author kept", u)
			}
			if tt.draft.SportCode == "other" && (u.SportLabel == nil || *u.SportLabel != "Paddle board") {
				t.Fatalf("label %v, want it cleaned", u.SportLabel)
			}
		})
	}
}

func TestServiceUpdateRefusals(t *testing.T) {
	c1 := challengeOf(t, "c1")
	inWeek2, inWeek1 := toronto(t, 2026, 6, 10, 7), toronto(t, 2026, 6, 3, 7)
	storage := errors.New("connection refused")
	someoneElse := storedRun(t)
	someoneElse.UserID = "7c6b5a49-3827-4165-9f0e-d1c2b3a4f5e6"
	lockedRun := storedRun(t)
	lockedRun.StartedAt = inWeek1
	// Ownership comes before the lock: never week_locked for someone else.
	someoneElseLocked := lockedRun
	someoneElseLocked.UserID = someoneElse.UserID
	yogaWithDistance := run(inWeek2)
	yogaWithDistance.SportCode = "yoga"
	tests := []struct {
		name       string
		stored     *Activity
		repoErr    error
		ps         fakeParticipations
		draft      Draft
		code       apperr.Code
		wantErr    error
		noDecision bool
	}{
		{name: "activity of someone else", stored: &someoneElse, draft: run(inWeek2), code: apperr.CodeActivityNotFound},
		{name: "activity of someone else in a locked week", stored: &someoneElseLocked, draft: run(inWeek2), code: apperr.CodeActivityNotFound},
		{name: "missing or deleted activity", draft: run(inWeek2), code: apperr.CodeActivityNotFound, noDecision: true},
		{name: "original week locked", stored: &lockedRun, draft: run(inWeek2), code: apperr.CodeWeekLocked},
		{name: "moved into a locked week", draft: run(inWeek1), code: apperr.CodeWeekLocked},
		{name: "unknown sport", draft: Draft{SportCode: "chess", StartedAt: inWeek2, DurationSeconds: 60}, code: apperr.CodeValidationFailed, noDecision: true},
		{name: "in the future", draft: run(recordNow.Add(time.Hour)), code: apperr.CodeValidationFailed, noDecision: true},
		{name: "distance not allowed", draft: yogaWithDistance, code: apperr.CodeDistanceNotAllowed, noDecision: true},
		{name: "distance required", draft: Draft{SportCode: "run", StartedAt: inWeek2, DurationSeconds: 60}, code: apperr.CodeDistanceRequired, noDecision: true},
		{name: "account deleted meanwhile", repoErr: errAccountDeleted, draft: run(inWeek2), code: apperr.CodeAccountDeleted, noDecision: true},
		{name: "participations read fails", ps: fakeParticipations{err: storage}, draft: run(inWeek2), wantErr: storage},
		{name: "storage fails", repoErr: storage, draft: run(inWeek2), wantErr: storage, noDecision: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stored := map[string]Activity{}
			if tt.stored != nil {
				stored[activityID] = *tt.stored
			} else if tt.code != apperr.CodeActivityNotFound {
				stored[activityID] = storedRun(t)
			}
			repo := &fakeRepository{stored: stored, err: tt.repoErr}
			ps := tt.ps
			if ps.challenges == nil {
				ps.challenges = []Participation{c1}
			}
			_, err := newService(t, repo, ps).Update(t.Context(), actor, activityID, tt.draft)
			checkError(t, err, tt.code, tt.wantErr)
			if repo.updated != nil || (tt.noDecision && repo.decided) {
				t.Fatalf("updated %+v, decided %v: want nothing written", repo.updated, repo.decided)
			}
		})
	}
}

func TestServiceDelete(t *testing.T) {
	c1 := challengeOf(t, "c1")
	repo := &fakeRepository{stored: map[string]Activity{activityID: storedRun(t)}}
	if err := newService(t, repo, fakeParticipations{challenges: []Participation{c1}}).Delete(t.Context(), actor, activityID); err != nil {
		t.Fatal(err)
	}
	if repo.deleted != activityID || !repo.deletedAt.Equal(recordNow) {
		t.Fatalf("deleted %q at %v, want %s at the clock's now", repo.deleted, repo.deletedAt, activityID)
	}
}

func TestServiceDeleteRefusals(t *testing.T) {
	storage := errors.New("connection refused")
	someoneElse := storedRun(t)
	someoneElse.UserID = "7c6b5a49-3827-4165-9f0e-d1c2b3a4f5e6"
	locked := storedRun(t)
	locked.StartedAt = toronto(t, 2026, 6, 3, 7)
	someoneElseLocked := locked
	someoneElseLocked.UserID = someoneElse.UserID
	tests := []struct {
		name    string
		stored  map[string]Activity
		repoErr error
		ps      fakeParticipations
		code    apperr.Code
		wantErr error
	}{
		{name: "activity of someone else", stored: map[string]Activity{activityID: someoneElse}, code: apperr.CodeActivityNotFound},
		{name: "activity of someone else in a locked week", stored: map[string]Activity{activityID: someoneElseLocked}, code: apperr.CodeActivityNotFound},
		{name: "missing or deleted activity", code: apperr.CodeActivityNotFound},
		{name: "week locked", stored: map[string]Activity{activityID: locked}, code: apperr.CodeWeekLocked},
		{name: "account deleted meanwhile", repoErr: errAccountDeleted, code: apperr.CodeAccountDeleted},
		{name: "participations read fails", stored: map[string]Activity{activityID: storedRun(t)}, ps: fakeParticipations{err: storage}, wantErr: storage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{stored: tt.stored, err: tt.repoErr}
			ps := tt.ps
			if ps.challenges == nil {
				ps.challenges = []Participation{challengeOf(t, "c1")}
			}
			err := newService(t, repo, ps).Delete(t.Context(), actor, activityID)
			checkError(t, err, tt.code, tt.wantErr)
			if repo.deleted != "" {
				t.Fatalf("deleted %q, want nothing written", repo.deleted)
			}
		})
	}
}

// checkError checks that err wraps wantErr, or else is the error of code.
func checkError(t *testing.T, err error, code apperr.Code, wantErr error) {
	t.Helper()
	if wantErr != nil {
		if !errors.Is(err, wantErr) {
			t.Fatalf("error = %v, want it to wrap %v", err, wantErr)
		}
		return
	}
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != code {
		t.Fatalf("error = %v, want %s", err, code)
	}
}
