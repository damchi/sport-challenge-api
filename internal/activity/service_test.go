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
	// listing serves List; listed records its query.
	listing []Activity
	listed  *ListQuery
	// days serve DayTotals (zeros when nil), bounds records its bounds;
	// sports serve SportCounts; weeks serve Weeks, weekQuery records its
	// query and weeksErr fails it.
	days      []Totals
	bounds    []time.Time
	sports    []SportCount
	weeks     []WeekTotals
	weekQuery *WeekQuery
	weeksErr  error
}

func (f *fakeRepository) DayTotals(_ context.Context, _ string, bounds []time.Time) ([]Totals, error) {
	f.bounds = bounds
	if f.err != nil {
		return nil, f.err
	}
	if f.days == nil {
		return make([]Totals, len(bounds)-1), nil
	}
	return f.days, nil
}

func (f *fakeRepository) SportCounts(context.Context, string) ([]SportCount, error) {
	return f.sports, f.err
}

func (f *fakeRepository) Weeks(_ context.Context, q WeekQuery) ([]WeekTotals, error) {
	f.weekQuery = &q
	if f.weeksErr != nil {
		return nil, f.weeksErr
	}
	if len(f.weeks) > q.Limit {
		return f.weeks[:q.Limit], nil
	}
	return f.weeks, nil
}

// EraseUser is covered by the integration tests.
func (f *fakeRepository) EraseUser(context.Context, string) error { return nil }

func (f *fakeRepository) List(_ context.Context, q ListQuery) ([]Activity, error) {
	f.listed = &q
	if f.err != nil {
		return nil, f.err
	}
	if len(f.listing) > q.Limit {
		return f.listing[:q.Limit], nil
	}
	return f.listing, nil
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
	// names serve Names; namesErr fails it, asked records the ids.
	names    map[string]Name
	namesErr error
	asked    *[]string
}

func (f fakeProfiles) Names(_ context.Context, ids []string) (map[string]Name, error) {
	if f.asked != nil {
		*f.asked = ids
	}
	return f.names, f.namesErr
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

var catalogue = fakeCatalogue{modes: map[string]string{"run": distanceRequired, "yoga": distanceNone, "other": distanceOptional, "ski_2": distanceOptional}}

// fakeParticipations gives challenges for user, the actor when empty, only;
// calls counts them.
type fakeParticipations struct {
	t          *testing.T
	user       string
	challenges []Participation
	err        error
	calls      *int
}

func (f fakeParticipations) Participations(_ context.Context, userID string) ([]Participation, error) {
	want := f.user
	if want == "" {
		want = actor
	}
	if userID != want {
		f.t.Errorf("participations of %q, want those of %q", userID, want)
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
	p.ChallengeID, p.Title = id, "Spring run"
	p.Factors = map[string]float64{"run": 1, "ride": 0.3}
	return p
}

func run(startedAt time.Time) Draft {
	d := 5000
	return Draft{SportCode: "run", StartedAt: startedAt, DurationSeconds: 1500, DistanceMeters: &d}
}

func newService(t *testing.T, repo *fakeRepository, ps fakeParticipations) *Service {
	t.Helper()
	ps.t = t
	return NewService(repo, fakeProfiles{complete: true}, catalogue, ps, nil, clock.NewFake(recordNow))
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
		{name: "left before week 1 locked: open, counts nowhere", draft: run(toronto(t, 2026, 6, 3, 7)), challenges: []Participation{leftAt(challengeOf(t, "c3"), toronto(t, 2026, 6, 5, 10))}},
		{name: "sport without distance", draft: Draft{SportCode: "yoga", StartedAt: inWeek2, DurationSeconds: 3600}, challenges: []Participation{c1}, want: []string{"c1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{}
			got, err := newService(t, repo, fakeParticipations{challenges: tt.challenges}).Record(t.Context(), actor, nil, tt.draft)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Created || got.ID == "" || !reflect.DeepEqual(challengeIDs(got.Contributions), tt.want) {
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
		{name: "week 1 locked before leaving", startedAt: toronto(t, 2026, 6, 3, 7), challenges: []Participation{leftAt(c1, toronto(t, 2026, 6, 9, 10))}},
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
	if got.Created || got.ID != existing.ID || got.DurationSeconds != 1200 || !reflect.DeepEqual(challengeIDs(got.Contributions), []string{"c1"}) {
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
			_, err := NewService(repo, profiles, cat, ps, nil, clock.NewFake(recordNow)).Record(t.Context(), actor, nil, tt.draft)
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
			if got.Created || got.ID != activityID || !reflect.DeepEqual(challengeIDs(got.Contributions), tt.want) {
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
		{name: "original week locked before leaving", stored: &lockedRun, ps: fakeParticipations{challenges: []Participation{leftAt(c1, toronto(t, 2026, 6, 9, 10))}}, draft: run(inWeek2), code: apperr.CodeWeekLocked},
		{name: "moved into a week locked before leaving", ps: fakeParticipations{challenges: []Participation{leftAt(c1, toronto(t, 2026, 6, 9, 10))}}, draft: run(inWeek1), code: apperr.CodeWeekLocked},
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
		{name: "week locked before leaving", stored: map[string]Activity{activityID: locked}, ps: fakeParticipations{challenges: []Participation{leftAt(challengeOf(t, "c1"), toronto(t, 2026, 6, 9, 10))}}, code: apperr.CodeWeekLocked},
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

// fakeChallenges gives scope for any challenge, and members for
// AllActivitiesScope, or fails with err; asked records the last call.
type fakeChallenges struct {
	scope   Participation
	members map[string]Participation
	err     error
	asked   *[3]string
}

func (f fakeChallenges) AllActivitiesScope(_ context.Context, actorID, challengeID string) (Participation, map[string]Participation, error) {
	if f.asked != nil {
		*f.asked = [3]string{actorID, challengeID, ""}
	}
	return f.scope, f.members, f.err
}

func (f fakeChallenges) ActivitiesScope(_ context.Context, actorID, challengeID, userID string) (Participation, error) {
	if f.asked != nil {
		*f.asked = [3]string{actorID, challengeID, userID}
	}
	return f.scope, f.err
}

// listing is n activities of userID, most recent first, one a day back from
// Thursday 2026-06-11 (week 2 of challengeOf), 7:00 in Toronto.
func listing(t *testing.T, userID string, n int) []Activity {
	t.Helper()
	out := make([]Activity, 0, n)
	for i := range n {
		out = append(out, Activity{
			ID: fmt.Sprintf("5f1e7c1a-0000-4000-8000-%012d", i), UserID: userID, SportCode: "yoga",
			StartedAt: toronto(t, 2026, 6, 11-i, 7), DurationSeconds: 3600, Source: SourceManual,
		})
	}
	return out
}

func TestServiceMine(t *testing.T) {
	c1 := challengeOf(t, "c1")
	tests := []struct {
		name      string
		available int
		limit     int
		wantItems int
		wantNext  bool
	}{
		{name: "more than a page", available: 5, limit: 2, wantItems: 2, wantNext: true},
		{name: "exactly a page: no next page", available: 2, limit: 2, wantItems: 2},
		{name: "less than a page", available: 1, limit: 20, wantItems: 1},
		{name: "nothing", available: 0, limit: 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{listing: listing(t, actor, tt.available)}
			from, to := toronto(t, 2026, 6, 1, 0), toronto(t, 2026, 6, 15, 0)
			after := &Position{StartedAt: toronto(t, 2026, 6, 12, 0), ID: "5f1e7c1a-0000-4000-8000-000000000009"}
			page, err := newService(t, repo, fakeParticipations{challenges: []Participation{c1}}).Mine(t.Context(), actor, Filter{From: &from, To: &to}, tt.limit, after)
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Items) != tt.wantItems || (page.Next != nil) != tt.wantNext {
				t.Fatalf("%d items, next %v, want %d items, next %v", len(page.Items), page.Next, tt.wantItems, tt.wantNext)
			}
			if tt.wantNext {
				last := page.Items[len(page.Items)-1]
				if page.Next.ID != last.ID || !page.Next.StartedAt.Equal(last.StartedAt) {
					t.Fatalf("next %+v, want the position of the last item %+v", page.Next, last.Activity)
				}
			}
			// The query asks one more item, of the actor, in the period,
			// after the given position.
			want := ListQuery{UserIDs: []string{actor}, From: &from, To: &to, Limit: tt.limit + 1, After: after}
			if q := repo.listed; q == nil || !reflect.DeepEqual(*q, want) {
				t.Fatalf("query %+v, want %+v", q, want)
			}
		})
	}
}

// TestServiceMineCountsAndLocks reads where each activity counts and whether
// it is locked at the clock's now (Thursday of week 2).
func TestServiceMineCountsAndLocks(t *testing.T) {
	c1, c2 := challengeOf(t, "c1"), challengeOf(t, "c2")
	c2.Active = false
	week2 := Activity{ID: "a2", UserID: actor, StartedAt: toronto(t, 2026, 6, 10, 7), DurationSeconds: 60, Source: SourceManual}
	week1 := Activity{ID: "a1", UserID: actor, StartedAt: toronto(t, 2026, 6, 3, 7), DurationSeconds: 60, Source: SourceManual}
	before := Activity{ID: "a0", UserID: actor, StartedAt: toronto(t, 2026, 5, 20, 7), DurationSeconds: 60, Source: SourceManual}
	repo := &fakeRepository{listing: []Activity{week2, week1, before}}
	page, err := newService(t, repo, fakeParticipations{challenges: []Participation{c1, c2}}).Mine(t.Context(), actor, Filter{}, 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		ids    []string
		locked bool
	}{
		{ids: []string{"c1"}},
		{ids: []string{"c1"}, locked: true},
		// Outside every window: counts nowhere, never locked.
		{},
	}
	for i, w := range want {
		got := page.Items[i]
		if !reflect.DeepEqual(challengeIDs(got.Contributions), w.ids) || got.Locked != w.locked {
			t.Errorf("item %d: challenges %v, locked %v, want %v, %v", i, challengeIDs(got.Contributions), got.Locked, w.ids, w.locked)
		}
	}
}

func TestServiceMineRefusals(t *testing.T) {
	from := toronto(t, 2026, 6, 8, 0)
	before := from.Add(-time.Second)
	tests := []struct {
		name   string
		filter Filter
		limit  int
		field  string
	}{
		{name: "empty page", limit: 0, field: "limit"},
		{name: "to before from", filter: Filter{From: &from, To: &before}, limit: 20, field: "to"},
		{name: "to equal to from: empty period", filter: Filter{From: &from, To: &from}, limit: 20, field: "to"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{}
			_, err := newService(t, repo, fakeParticipations{}).Mine(t.Context(), actor, tt.filter, tt.limit, nil)
			var appErr *apperr.Error
			if !errors.As(err, &appErr) || appErr.Code != apperr.CodeValidationFailed || appErr.Params[apperr.FieldsParam] != tt.field {
				t.Fatalf("error = %v, want validation_failed on %s", err, tt.field)
			}
			if repo.listed != nil {
				t.Fatal("activities read despite the refusal")
			}
		})
	}
}

func TestServiceMineFailures(t *testing.T) {
	storageErr := errors.New("connection reset")
	if _, err := newService(t, &fakeRepository{err: storageErr}, fakeParticipations{}).Mine(t.Context(), actor, Filter{}, 20, nil); !errors.Is(err, storageErr) {
		t.Fatalf("listing failure: error = %v, want the cause kept", err)
	}
	if _, err := newService(t, &fakeRepository{listing: listing(t, actor, 1)}, fakeParticipations{err: storageErr}).Mine(t.Context(), actor, Filter{}, 20, nil); !errors.Is(err, storageErr) {
		t.Fatalf("participations failure: error = %v, want the cause kept", err)
	}
}

const participant = "7c6b5a49-3827-4165-9f0e-d1c2b3a4f5e6"

func TestServiceOfParticipant(t *testing.T) {
	c1 := challengeOf(t, "c1")
	// Wednesday 2026-06-10 and Thursday 2026-06-11 in week 2 of c1, not
	// ended; Wednesday 2026-06-03 in week 1, ended on 2026-06-08.
	rows := listing(t, participant, 2)
	week1 := Activity{ID: "5f1e7c1a-0000-4000-8000-000000000099", UserID: participant, SportCode: "yoga", StartedAt: toronto(t, 2026, 6, 3, 7), DurationSeconds: 3600, Source: SourceManual}
	repo := &fakeRepository{listing: append(rows, week1, week1)}
	var asked [3]string
	calls := 0
	ps := fakeParticipations{t: t, user: participant, calls: &calls}
	svc := NewService(repo, fakeProfiles{}, catalogue, ps, fakeChallenges{scope: c1, asked: &asked}, clock.NewFake(recordNow))
	page, err := svc.OfParticipant(t.Context(), actor, "c1", participant, 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	if asked != [3]string{actor, "c1", participant} {
		t.Fatalf("scope asked for %v, want the actor, the challenge and the participant", asked)
	}
	// Activities of the participant, in the window of the challenge.
	want := ListQuery{UserIDs: []string{participant}, From: &c1.StartsAt, To: &c1.EndsAt, Limit: 4}
	if q := repo.listed; q == nil || !reflect.DeepEqual(*q, want) {
		t.Fatalf("query %+v, want %+v", q, want)
	}
	if len(page.Items) != 3 || page.Next == nil {
		t.Fatalf("%d items, next %v, want 3 items and a next page", len(page.Items), page.Next)
	}
	// The other challenges of the participant are never read: they could
	// show through contributions or locked.
	if calls != 0 {
		t.Fatalf("participations of the participant read %d times, want never", calls)
	}
	for i, it := range page.Items {
		if !reflect.DeepEqual(challengeIDs(it.Contributions), []string{"c1"}) {
			t.Errorf("item %d: challenges %v, want [c1]", i, challengeIDs(it.Contributions))
		}
	}
	if page.Items[0].Locked || page.Items[1].Locked || !page.Items[2].Locked {
		t.Fatalf("locked %v, %v, %v, want false, false, true (week 1 of c1 ended)", page.Items[0].Locked, page.Items[1].Locked, page.Items[2].Locked)
	}
}

// TestServiceOfParticipantAbandoned shows the activities of a participant
// who abandoned: they count nowhere in the challenge.
func TestServiceOfParticipantAbandoned(t *testing.T) {
	c1 := challengeOf(t, "c1")
	c1.Active = false
	repo := &fakeRepository{listing: listing(t, participant, 1)}
	ps := fakeParticipations{t: t, user: participant, challenges: []Participation{c1}}
	page, err := NewService(repo, fakeProfiles{}, catalogue, ps, fakeChallenges{scope: c1}, clock.NewFake(recordNow)).OfParticipant(t.Context(), actor, "c1", participant, 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || len(page.Items[0].Contributions) != 0 {
		t.Fatalf("items %+v, want one activity counting nowhere", page.Items)
	}
}

func TestServiceOfParticipantRefusals(t *testing.T) {
	storageErr := errors.New("connection reset")
	tests := []struct {
		name     string
		scopeErr error
		limit    int
		wantErr  error
	}{
		{name: "empty page", limit: 0, wantErr: apperr.New(apperr.CodeValidationFailed, nil)},
		{name: "leaderboard only", scopeErr: apperr.New(apperr.CodeNotFound, nil), limit: 20, wantErr: apperr.New(apperr.CodeNotFound, nil)},
		{name: "hidden challenge", scopeErr: apperr.New(apperr.CodeChallengeNotFound, nil), limit: 20, wantErr: apperr.New(apperr.CodeChallengeNotFound, nil)},
		{name: "scope failure", scopeErr: storageErr, limit: 20, wantErr: storageErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{listing: listing(t, participant, 1)}
			svc := NewService(repo, fakeProfiles{}, catalogue, fakeParticipations{t: t, user: participant}, fakeChallenges{err: tt.scopeErr}, clock.NewFake(recordNow))
			_, err := svc.OfParticipant(t.Context(), actor, "c1", participant, tt.limit, nil)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if repo.listed != nil {
				t.Fatal("activities read despite the refusal")
			}
		})
	}
}

func TestServiceOfChallenge(t *testing.T) {
	const other = "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d"
	const gone = "9f8e7d6c-5b4a-4392-8170-6f5e4d3c2b1a"
	c1 := challengeOf(t, "c1")
	// other left on Wednesday 2026-06-10, once week 1 was locked: their
	// activity of week 1 stays locked, counting nowhere.
	left := leftAt(c1, toronto(t, 2026, 6, 10, 12))
	week2 := Activity{ID: "5f1e7c1a-0000-4000-8000-000000000001", UserID: participant, SportCode: "yoga", StartedAt: toronto(t, 2026, 6, 10, 7), DurationSeconds: 3600, Source: SourceManual}
	week1 := Activity{ID: "5f1e7c1a-0000-4000-8000-000000000002", UserID: other, SportCode: "yoga", StartedAt: toronto(t, 2026, 6, 3, 7), DurationSeconds: 3600, Source: SourceManual}
	// gone deleted their account since the participants were read.
	ofGone := Activity{ID: "5f1e7c1a-0000-4000-8000-000000000003", UserID: gone, SportCode: "yoga", StartedAt: toronto(t, 2026, 6, 2, 7), DurationSeconds: 3600, Source: SourceManual}
	extra := Activity{ID: "5f1e7c1a-0000-4000-8000-000000000004", UserID: participant, SportCode: "yoga", StartedAt: toronto(t, 2026, 6, 1, 7), DurationSeconds: 3600, Source: SourceManual}
	repo := &fakeRepository{listing: []Activity{week2, week1, ofGone, extra}}
	pseudo := "lea"
	var asked [3]string
	var namesAsked []string
	profiles := fakeProfiles{names: map[string]Name{participant: {Pseudo: &pseudo}, other: {}}, asked: &namesAsked}
	members := map[string]Participation{participant: c1, other: left, gone: c1}
	ps := fakeParticipations{t: t, user: "nobody"}
	svc := NewService(repo, profiles, catalogue, ps, fakeChallenges{scope: c1, members: members, asked: &asked}, clock.NewFake(recordNow))
	page, err := svc.OfChallenge(t.Context(), actor, "c1", 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	if asked != [3]string{actor, "c1", ""} {
		t.Fatalf("scope asked for %v, want the actor and the challenge", asked)
	}
	// Activities of every participant, sorted ids, in the window.
	want := ListQuery{UserIDs: []string{other, participant, gone}, From: &c1.StartsAt, To: &c1.EndsAt, Limit: 4}
	if q := repo.listed; q == nil || !reflect.DeepEqual(*q, want) {
		t.Fatalf("query %+v, want %+v", q, want)
	}
	if !reflect.DeepEqual(namesAsked, []string{other, participant, gone}) {
		t.Fatalf("names asked for %v, want the authors of the page once each, sorted", namesAsked)
	}
	// The next page starts after the last activity read, the one of the
	// deleted account left out.
	if page.Next == nil || page.Next.ID != ofGone.ID {
		t.Fatalf("next %+v, want after %s", page.Next, ofGone.ID)
	}
	if len(page.Items) != 2 {
		t.Fatalf("%d items, want 2 (the deleted account left out)", len(page.Items))
	}
	first, second := page.Items[0], page.Items[1]
	if first.ID != week2.ID || first.Author.Pseudo == nil || *first.Author.Pseudo != pseudo ||
		!reflect.DeepEqual(challengeIDs(first.Contributions), []string{"c1"}) || first.Locked {
		t.Fatalf("first %+v, want the activity of the participant counting in c1, not locked", first)
	}
	if second.ID != week1.ID || len(second.Contributions) != 0 || !second.Locked {
		t.Fatalf("second %+v, want the activity of the one who left, counting nowhere, locked", second)
	}
}

func TestServiceOfChallengeNoParticipant(t *testing.T) {
	repo := &fakeRepository{}
	svc := NewService(repo, fakeProfiles{}, catalogue, fakeParticipations{}, fakeChallenges{scope: challengeOf(t, "c1")}, clock.NewFake(recordNow))
	page, err := svc.OfChallenge(t.Context(), actor, "c1", 20, nil)
	if err != nil || page.Items == nil || len(page.Items) != 0 || page.Next != nil {
		t.Fatalf("OfChallenge() = %+v, %v, want an empty page", page, err)
	}
	if repo.listed != nil {
		t.Fatal("activities read without a participant")
	}
}

func TestServiceOfChallengeRefusals(t *testing.T) {
	storageErr := errors.New("connection reset")
	members := map[string]Participation{participant: challengeOf(t, "c1")}
	tests := []struct {
		name     string
		scopeErr error
		limit    int
		wantErr  error
	}{
		{name: "empty page", limit: 0, wantErr: apperr.New(apperr.CodeValidationFailed, nil)},
		{name: "leaderboard only", scopeErr: apperr.New(apperr.CodeNotFound, nil), limit: 20, wantErr: apperr.New(apperr.CodeNotFound, nil)},
		{name: "hidden challenge", scopeErr: apperr.New(apperr.CodeChallengeNotFound, nil), limit: 20, wantErr: apperr.New(apperr.CodeChallengeNotFound, nil)},
		{name: "scope failure", scopeErr: storageErr, limit: 20, wantErr: storageErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{listing: listing(t, participant, 1)}
			svc := NewService(repo, fakeProfiles{}, catalogue, fakeParticipations{}, fakeChallenges{members: members, err: tt.scopeErr}, clock.NewFake(recordNow))
			_, err := svc.OfChallenge(t.Context(), actor, "c1", tt.limit, nil)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if repo.listed != nil {
				t.Fatal("activities read despite the refusal")
			}
		})
	}
}

func TestServiceOfChallengeFailures(t *testing.T) {
	storageErr := errors.New("connection reset")
	c1 := challengeOf(t, "c1")
	scope := fakeChallenges{scope: c1, members: map[string]Participation{participant: c1}}
	if _, err := NewService(&fakeRepository{err: storageErr}, fakeProfiles{}, catalogue, fakeParticipations{}, scope, clock.NewFake(recordNow)).OfChallenge(t.Context(), actor, "c1", 20, nil); !errors.Is(err, storageErr) {
		t.Fatalf("listing failure: error = %v, want the cause kept", err)
	}
	repo := &fakeRepository{listing: listing(t, participant, 1)}
	if _, err := NewService(repo, fakeProfiles{namesErr: storageErr}, catalogue, fakeParticipations{}, scope, clock.NewFake(recordNow)).OfChallenge(t.Context(), actor, "c1", 20, nil); !errors.Is(err, storageErr) {
		t.Fatalf("names failure: error = %v, want the cause kept", err)
	}
}
