package challenge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
)

const actor = "0b9d4a5e-6a43-4c8e-9d1f-7a2b3c4d5e6f"

type fakeRepository struct {
	created  *Challenge
	joinedAt time.Time
	err      error
	// summaries and listed serve List.
	summaries []Summary
	listed    ListQuery
	// challenge and membership serve Get; a nil challenge is not found.
	challenge      *Challenge
	membership     Membership
	categoriesRead bool
	updated        *TextChanges
	abandonedAt    time.Time
	// target is the membership of the user to exclude or make a creator.
	target       Membership
	creatorAdded bool
	deletedAt    time.Time
	// participants serve Participants; participantsAsked records the call.
	participants      []Participant
	participantsAsked *participantsCall
	// active serves ActiveParticipations; activeErr fails it.
	active    []string
	activeErr error
	// memberships serve Memberships; membershipsErr fails it.
	memberships    []UserChallenge
	membershipsErr error
}

func (f *fakeRepository) ActiveParticipations(context.Context, string, time.Time) ([]string, error) {
	return f.active, f.activeErr
}

func (f *fakeRepository) Memberships(context.Context, string) ([]UserChallenge, error) {
	return f.memberships, f.membershipsErr
}

type participantsCall struct {
	limit int
	after *ParticipantPosition
}

func (f *fakeRepository) Participants(_ context.Context, _ string, limit int, after *ParticipantPosition) ([]Participant, error) {
	f.participantsAsked = &participantsCall{limit: limit, after: after}
	if len(f.participants) > limit {
		return f.participants[:limit], nil
	}
	return f.participants, nil
}

func (f *fakeRepository) Get(context.Context, string, string) (Challenge, Membership, error) {
	if f.err != nil {
		return Challenge{}, Membership{}, f.err
	}
	if f.challenge == nil {
		return Challenge{}, Membership{}, fmt.Errorf("read challenge: %w", errChallengeNotFound)
	}
	return *f.challenge, f.membership, nil
}

func (f *fakeRepository) UpdateText(_ context.Context, _ string, changes TextChanges) error {
	if f.err != nil {
		return f.err
	}
	f.updated = &changes
	if changes.Title != nil {
		f.challenge.Title = *changes.Title
	}
	return nil
}

func (f *fakeRepository) Join(_ context.Context, _, _ string, decide func(Challenge, Membership) (time.Time, error)) error {
	if f.err != nil {
		return f.err
	}
	if f.challenge == nil {
		return fmt.Errorf("join challenge: %w", errChallengeNotFound)
	}
	joinedAt, err := decide(*f.challenge, f.membership)
	if err != nil {
		return fmt.Errorf("join challenge: %w", err)
	}
	f.joinedAt = joinedAt
	f.membership.Participant = ParticipantActive
	return nil
}

func (f *fakeRepository) Abandon(_ context.Context, _, _ string, decide func(Challenge, Membership) (bool, time.Time, error)) error {
	if f.err != nil {
		return f.err
	}
	if f.challenge == nil {
		return fmt.Errorf("abandon challenge: %w", errChallengeNotFound)
	}
	noop, at, err := decide(*f.challenge, f.membership)
	if err != nil {
		return fmt.Errorf("abandon challenge: %w", err)
	}
	if !noop {
		f.abandonedAt = at
		f.membership.Participant = ParticipantAbandoned
	}
	return nil
}

func (f *fakeRepository) Exclude(_ context.Context, _, _, _ string, decide func(Challenge, Membership, Membership) (bool, time.Time, error)) error {
	if f.err != nil {
		return f.err
	}
	if f.challenge == nil {
		return fmt.Errorf("exclude participant: %w", errChallengeNotFound)
	}
	noop, at, err := decide(*f.challenge, f.membership, f.target)
	if err != nil {
		return fmt.Errorf("exclude participant: %w", err)
	}
	if !noop {
		f.abandonedAt = at
	}
	return nil
}

func (f *fakeRepository) AddCreator(_ context.Context, _, _, _ string, decide func(Challenge, Membership, Membership) (bool, error)) error {
	if f.err != nil {
		return f.err
	}
	if f.challenge == nil {
		return fmt.Errorf("add creator: %w", errChallengeNotFound)
	}
	noop, err := decide(*f.challenge, f.membership, f.target)
	if err != nil {
		return fmt.Errorf("add creator: %w", err)
	}
	if !noop {
		f.creatorAdded = true
	}
	return nil
}

func (f *fakeRepository) Delete(_ context.Context, _, _ string, decide func(Challenge, Membership) (time.Time, error)) error {
	if f.err != nil {
		return f.err
	}
	if f.challenge == nil {
		return fmt.Errorf("delete challenge: %w", errChallengeNotFound)
	}
	at, err := decide(*f.challenge, f.membership)
	if err != nil {
		return fmt.Errorf("delete challenge: %w", err)
	}
	f.deletedAt = at
	return nil
}

func (f *fakeRepository) Categories(context.Context, string) ([]Category, error) {
	if f.challenge == nil {
		return nil, nil
	}
	f.categoriesRead = true
	return f.challenge.Categories, nil
}

func (f *fakeRepository) List(_ context.Context, q ListQuery) ([]Summary, error) {
	f.listed = q
	if f.err != nil {
		return nil, f.err
	}
	if len(f.summaries) > q.Limit {
		return f.summaries[:q.Limit], nil
	}
	return f.summaries, nil
}

func (f *fakeRepository) Create(_ context.Context, c Challenge, joinedAt time.Time) (Challenge, error) {
	if f.err != nil {
		return Challenge{}, f.err
	}
	c.ID = "11111111-1111-1111-1111-111111111111"
	f.created, f.joinedAt = &c, joinedAt
	return c, nil
}

type fakeProfiles struct {
	complete  bool
	err       error
	pseudos   map[string]string
	pseudoErr error
}

func (f fakeProfiles) ProfileComplete(context.Context, string) (bool, error) {
	return f.complete, f.err
}

func (f fakeProfiles) Pseudos(context.Context, []string) (map[string]string, error) {
	return f.pseudos, f.pseudoErr
}

type fakeCatalogue struct {
	factors map[string]float64
	err     error
}

func (f fakeCatalogue) SuggestedFactors(context.Context) (map[string]float64, error) {
	return f.factors, f.err
}

func suggested() map[string]float64 {
	return map[string]float64{"run": 1, "walk": 1, "ride": 0.3, "other": 1}
}

func TestServiceCreate(t *testing.T) {
	repo := &fakeRepository{}
	clk := clock.NewFake(now)
	svc := NewService(repo, fakeProfiles{complete: true}, fakeCatalogue{factors: suggested()}, clk)

	draft := with(func(d *Draft) {
		d.Title = "  Spring run  "
		d.DistanceFactors = map[string]float64{"ride": 0.25}
	})
	c, err := svc.Create(t.Context(), actor, draft)
	if err != nil {
		t.Fatal(err)
	}

	if c.Title != "Spring run" {
		t.Errorf("title %q, want it trimmed", c.Title)
	}
	if c.CreatedBy != actor || !repo.joinedAt.Equal(now) {
		t.Errorf("created by %s, joined at %s, want %s at the clock's now", c.CreatedBy, repo.joinedAt, actor)
	}
	wantStart := time.Date(2026, 3, 4, 5, 0, 0, 0, time.UTC) // local midnight in Toronto
	if !c.StartsAt.Equal(wantStart) || !c.EndsAt.Equal(time.Date(2026, 4, 15, 4, 0, 0, 0, time.UTC)) {
		t.Errorf("window [%s, %s), want 6 local weeks from %s", c.StartsAt, c.EndsAt, wantStart)
	}
	if len(c.Categories) != 3 {
		t.Fatalf("%d categories, want 3", len(c.Categories))
	}
	wantTypes := []string{CategoryCumulativeTime, CategoryCumulativeDistance, CategoryRunElimination}
	for i, cat := range c.Categories {
		if cat.Type != wantTypes[i] || cat.Position != i+1 {
			t.Errorf("category %d = %s at %d, want %s at %d", i, cat.Type, cat.Position, wantTypes[i], i+1)
		}
	}
	// The creator's factor replaces the suggestion; the others keep theirs.
	factors := c.Categories[1].Params.Factors
	want := map[string]float64{"run": 1, "walk": 1, "ride": 0.25, "other": 1}
	if len(factors) != len(want) {
		t.Fatalf("factors %v, want %v", factors, want)
	}
	for code, f := range want {
		if factors[code] != f {
			t.Errorf("factor of %s = %v, want %v", code, factors[code], f)
		}
	}
	if p := c.Categories[2].Params; p.Sport != "run" || p.StepMeters != 2000 {
		t.Errorf("elimination params %+v, want run with a 2000 m step", p)
	}
}

func TestServiceCreateFreezesRunAtOne(t *testing.T) {
	// A catalogue that suggests another factor for run, or none at all.
	for name, catalogue := range map[string]map[string]float64{
		"run suggested at 0.8": {"run": 0.8, "ride": 0.3},
		"run missing":          {"ride": 0.3},
	} {
		t.Run(name, func(t *testing.T) {
			svc := NewService(&fakeRepository{}, fakeProfiles{complete: true}, fakeCatalogue{factors: catalogue}, clock.NewFake(now))
			c, err := svc.Create(t.Context(), actor, valid())
			if err != nil {
				t.Fatal(err)
			}
			if f := c.Categories[1].Params.Factors["run"]; f != 1 {
				t.Fatalf("run frozen at %v, want 1", f)
			}
		})
	}
}

func TestServiceCreateEmptyDescriptionIsNone(t *testing.T) {
	repo := &fakeRepository{}
	empty := ""
	if _, err := NewService(repo, fakeProfiles{complete: true}, fakeCatalogue{factors: suggested()}, clock.NewFake(now)).
		Create(t.Context(), actor, with(func(d *Draft) { d.Description = &empty })); err != nil {
		t.Fatal(err)
	}
	if repo.created.Description != nil {
		t.Fatalf("description %q stored, want none", *repo.created.Description)
	}
}

func TestServiceCreateDoesNotChangeTheCatalogue(t *testing.T) {
	catalogue := suggested()
	svc := NewService(&fakeRepository{}, fakeProfiles{complete: true}, fakeCatalogue{factors: catalogue}, clock.NewFake(now))
	if _, err := svc.Create(t.Context(), actor, with(func(d *Draft) { d.DistanceFactors = map[string]float64{"ride": 0.5} })); err != nil {
		t.Fatal(err)
	}
	if catalogue["ride"] != 0.3 {
		t.Fatalf("catalogue changed: ride = %v", catalogue["ride"])
	}
}

func TestServiceCreateRefusals(t *testing.T) {
	storage := errors.New("connection refused")
	tests := []struct {
		name       string
		profiles   fakeProfiles
		catalogue  fakeCatalogue
		repoErr    error
		draft      Draft
		wantCode   apperr.Code
		wantStatus int
		wantFields string
		wantErr    error
	}{
		{name: "incomplete profile", profiles: fakeProfiles{}, catalogue: fakeCatalogue{factors: suggested()}, draft: valid(), wantCode: apperr.CodeProfileIncomplete, wantStatus: http.StatusUnprocessableEntity},
		{name: "invalid draft", profiles: fakeProfiles{complete: true}, catalogue: fakeCatalogue{factors: suggested()}, draft: with(func(d *Draft) { d.Weeks = 1 }), wantCode: apperr.CodeValidationFailed, wantStatus: http.StatusBadRequest, wantFields: "weeks"},
		{name: "factor out of range", profiles: fakeProfiles{complete: true}, catalogue: fakeCatalogue{factors: suggested()}, draft: with(func(d *Draft) { d.DistanceFactors = map[string]float64{"ride": 3} }), wantCode: apperr.CodeValidationFailed, wantStatus: http.StatusBadRequest, wantFields: "distance_factors.ride"},
		{name: "factor of a sport outside the catalogue", profiles: fakeProfiles{complete: true}, catalogue: fakeCatalogue{factors: suggested()}, draft: with(func(d *Draft) { d.DistanceFactors = map[string]float64{"yoga": 1} }), wantCode: apperr.CodeValidationFailed, wantStatus: http.StatusBadRequest, wantFields: "distance_factors"},
		{name: "profile lookup fails", profiles: fakeProfiles{err: storage}, catalogue: fakeCatalogue{factors: suggested()}, draft: valid(), wantErr: storage},
		{name: "catalogue fails", profiles: fakeProfiles{complete: true}, catalogue: fakeCatalogue{err: storage}, draft: valid(), wantErr: storage},
		{name: "storage fails", profiles: fakeProfiles{complete: true}, catalogue: fakeCatalogue{factors: suggested()}, repoErr: storage, draft: valid(), wantErr: storage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{err: tt.repoErr}
			_, err := NewService(repo, tt.profiles, tt.catalogue, clock.NewFake(now)).Create(t.Context(), actor, tt.draft)
			if tt.wantErr != nil {
				var appErr *apperr.Error
				if !errors.Is(err, tt.wantErr) || errors.As(err, &appErr) {
					t.Fatalf("error = %v, want it to wrap %v as an internal error", err, tt.wantErr)
				}
				return
			}
			var appErr *apperr.Error
			if !errors.As(err, &appErr) || appErr.Code != tt.wantCode || appErr.Status != tt.wantStatus || appErr.Params[apperr.FieldsParam] != tt.wantFields {
				t.Fatalf("error = %+v, want %d %s fields %q", appErr, tt.wantStatus, tt.wantCode, tt.wantFields)
			}
			if repo.created != nil {
				t.Fatal("challenge stored despite the refusal")
			}
		})
	}
}

func TestServiceCreateUsesTheClock(t *testing.T) {
	// The draft starts on 2026-03-04: valid at 10:00 local that day, past
	// once the clock moves to the next local day.
	clk := clock.NewFake(now)
	svc := NewService(&fakeRepository{}, fakeProfiles{complete: true}, fakeCatalogue{factors: suggested()}, clk)
	if _, err := svc.Create(t.Context(), actor, valid()); err != nil {
		t.Fatalf("start today refused: %v", err)
	}
	clk.Advance(24 * time.Hour)
	_, err := svc.Create(t.Context(), actor, valid())
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || !strings.Contains(appErr.Params[apperr.FieldsParam], "start_date") {
		t.Fatalf("error = %v, want start_date refused a day later", err)
	}
}

func summaries(n int) []Summary {
	out := make([]Summary, n)
	for i := range out {
		out[i] = Summary{ID: fmt.Sprintf("00000000-0000-0000-0000-%012d", i), StartsAt: now.AddDate(0, 0, i)}
	}
	return out
}

func TestServiceList(t *testing.T) {
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
		{name: "nothing", available: 0, limit: 20, wantItems: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{summaries: summaries(tt.available)}
			clk := clock.NewFake(now)
			page, err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, clk).List(t.Context(), actor, ScopeMine, StatusOngoing, tt.limit, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Items) != tt.wantItems || (page.Next != nil) != tt.wantNext {
				t.Fatalf("%d items, next %v, want %d items, next %v", len(page.Items), page.Next, tt.wantItems, tt.wantNext)
			}
			if tt.wantNext {
				last := page.Items[len(page.Items)-1]
				if page.Next.ID != last.ID || !page.Next.StartsAt.Equal(last.StartsAt) {
					t.Fatalf("next %+v, want the position of the last item %+v", page.Next, last)
				}
			}
			// The query asks one more item, with the actor and the clock's now.
			q := repo.listed
			if q.Limit != tt.limit+1 || q.ActorID != actor || q.Scope != ScopeMine || q.Status != StatusOngoing || !q.Now.Equal(now) {
				t.Fatalf("query %+v, want limit %d for the actor at the clock's now", q, tt.limit+1)
			}
		})
	}
}

func TestServiceListRefusesAnEmptyPage(t *testing.T) {
	_, err := NewService(&fakeRepository{summaries: summaries(3)}, fakeProfiles{}, fakeCatalogue{}, clock.NewFake(now)).List(t.Context(), actor, ScopePublic, "", 0, nil)
	if !errors.Is(err, apperr.New(apperr.CodeValidationFailed, nil)) {
		t.Fatalf("error = %v, want validation_failed on limit", err)
	}
}

func TestServiceListStorageFailure(t *testing.T) {
	storage := errors.New("connection refused")
	_, err := NewService(&fakeRepository{err: storage}, fakeProfiles{}, fakeCatalogue{}, clock.NewFake(now)).List(t.Context(), actor, ScopePublic, "", 20, nil)
	if !errors.Is(err, storage) {
		t.Fatalf("error = %v, want it to wrap %v", err, storage)
	}
}

// stored is a 3-week public challenge in Toronto from 2026-03-02.
func stored(visibility string) *Challenge {
	return &Challenge{
		ID: "11111111-1111-1111-1111-111111111111", Title: "Spring run", Visibility: visibility,
		Timezone: "America/Toronto",
		StartsAt: time.Date(2026, 3, 2, 5, 0, 0, 0, time.UTC),
		EndsAt:   time.Date(2026, 3, 23, 4, 0, 0, 0, time.UTC),
	}
}

func TestServiceGet(t *testing.T) {
	deadline := time.Date(2026, 3, 9, 4, 0, 0, 0, time.UTC) // local midnight, after the time change
	tests := []struct {
		name         string
		visibility   string
		membership   Membership
		now          time.Time
		wantRole     Role
		wantStatus   string
		wantCanJoin  bool
		wantWeek     int
		wantWeekEnds time.Time
	}{
		{name: "upcoming, outsider of a public challenge", visibility: VisibilityPublic, now: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC), wantRole: RoleOutsider, wantStatus: StatusUpcoming, wantCanJoin: true},
		{name: "last second of week 1: can still join", visibility: VisibilityPublic, now: deadline.Add(-time.Second), wantRole: RoleOutsider, wantStatus: StatusOngoing, wantCanJoin: true, wantWeek: 1, wantWeekEnds: deadline},
		{name: "end of week 1: registration closed", visibility: VisibilityPublic, now: deadline, wantRole: RoleOutsider, wantStatus: StatusOngoing, wantWeek: 2, wantWeekEnds: time.Date(2026, 3, 16, 4, 0, 0, 0, time.UTC)},
		{name: "participant", visibility: VisibilityPrivate, membership: Membership{Participant: ParticipantActive}, now: deadline.Add(-time.Hour), wantRole: RoleParticipant, wantStatus: StatusOngoing, wantWeek: 1, wantWeekEnds: deadline},
		{name: "finished, creator", visibility: VisibilityPublic, membership: Membership{Creator: true, Participant: ParticipantActive}, now: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), wantRole: RoleCreator, wantStatus: StatusFinished},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{challenge: stored(tt.visibility), membership: tt.membership}
			d, err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, clock.NewFake(tt.now)).Get(t.Context(), actor, "11111111-1111-1111-1111-111111111111")
			if err != nil {
				t.Fatal(err)
			}
			if d.Role != tt.wantRole || d.Status != tt.wantStatus || d.CanJoin != tt.wantCanJoin || d.CurrentWeek != tt.wantWeek || !d.WeekEndsAt.Equal(tt.wantWeekEnds) {
				t.Fatalf("got role %v, %s, can join %v, week %d ending %s; want %v, %s, %v, %d ending %s",
					d.Role, d.Status, d.CanJoin, d.CurrentWeek, d.WeekEndsAt, tt.wantRole, tt.wantStatus, tt.wantCanJoin, tt.wantWeek, tt.wantWeekEnds)
			}
			if !d.RegistrationDeadline.Equal(deadline) {
				t.Fatalf("deadline %s, want %s", d.RegistrationDeadline, deadline)
			}
		})
	}
}

// TestServiceGetHidesPrivateChallenges: a missing challenge and a private
// one the actor may not see give the very same error.
func TestServiceGetHidesPrivateChallenges(t *testing.T) {
	missing := &fakeRepository{}
	private := &fakeRepository{challenge: stored(VisibilityPrivate)}
	var errs []*apperr.Error
	for _, repo := range []*fakeRepository{missing, private} {
		_, err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, clock.NewFake(now)).Get(t.Context(), actor, "11111111-1111-1111-1111-111111111111")
		if repo.categoriesRead {
			t.Fatal("categories read before the visibility check")
		}
		var appErr *apperr.Error
		if !errors.As(err, &appErr) {
			t.Fatalf("error = %v, want an apperr", err)
		}
		errs = append(errs, appErr)
	}
	for _, e := range errs {
		if e.Code != apperr.CodeChallengeNotFound || e.Status != http.StatusNotFound || e.Message != errs[0].Message || e.Params != nil {
			t.Fatalf("errors %+v and %+v, want the same 404 challenge_not_found", errs[0], errs[1])
		}
	}
}

func TestServiceGetStorageFailure(t *testing.T) {
	storage := errors.New("connection refused")
	_, err := NewService(&fakeRepository{err: storage}, fakeProfiles{}, fakeCatalogue{}, clock.NewFake(now)).Get(t.Context(), actor, "x")
	var appErr *apperr.Error
	if !errors.Is(err, storage) || errors.As(err, &appErr) {
		t.Fatalf("error = %v, want it to wrap %v as an internal error", err, storage)
	}
}

func TestServiceUpdateText(t *testing.T) {
	str := func(s string) *string { return &s }
	creator := Membership{Creator: true, Participant: ParticipantActive}
	tests := []struct {
		name       string
		visibility string
		membership Membership
		frozen     []string
		changes    TextChanges
		wantCode   apperr.Code
		wantFields string
	}{
		{name: "creator edits, even after the end", visibility: VisibilityPublic, membership: creator, changes: TextChanges{Title: str(" New title ")}},
		{name: "creator who abandoned keeps the right", visibility: VisibilityPublic, membership: Membership{Creator: true, Participant: ParticipantAbandoned}, changes: TextChanges{Title: str("New title")}},
		{name: "participant: forbidden", visibility: VisibilityPublic, membership: Membership{Participant: ParticipantActive}, changes: TextChanges{Title: str("New title")}, wantCode: apperr.CodeForbidden},
		{name: "outsider of a public challenge: forbidden", visibility: VisibilityPublic, changes: TextChanges{Title: str("New title")}, wantCode: apperr.CodeForbidden},
		{name: "outsider of a private challenge: not found", visibility: VisibilityPrivate, changes: TextChanges{Title: str("New title")}, wantCode: apperr.CodeChallengeNotFound},
		{name: "frozen fields, sorted", visibility: VisibilityPublic, membership: creator, frozen: []string{"weeks", "categories"}, wantCode: apperr.CodeFieldNotEditable, wantFields: "categories,weeks"},
		{name: "frozen fields on a private challenge not visible: not found first", visibility: VisibilityPrivate, frozen: []string{"weeks"}, wantCode: apperr.CodeChallengeNotFound},
		{name: "frozen fields are checked after the right", visibility: VisibilityPublic, membership: Membership{Participant: ParticipantActive}, frozen: []string{"weeks"}, wantCode: apperr.CodeForbidden},
		{name: "invalid title", visibility: VisibilityPublic, membership: creator, changes: TextChanges{Title: str("ab")}, wantCode: apperr.CodeValidationFailed, wantFields: "title"},
		{name: "invalid description", visibility: VisibilityPublic, membership: creator, changes: TextChanges{Description: str("a\x00b")}, wantCode: apperr.CodeValidationFailed, wantFields: "description"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{challenge: stored(tt.visibility), membership: tt.membership}
			// After the end of the stored challenge (2026-03-23): edits never close.
			d, err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, clock.NewFake(time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))).
				UpdateText(t.Context(), actor, "11111111-1111-1111-1111-111111111111", tt.frozen, tt.changes)
			if tt.wantCode == "" {
				if err != nil {
					t.Fatal(err)
				}
				if repo.updated == nil || *repo.updated.Title != "New title" || d.Title != "New title" {
					t.Fatalf("written %+v, detail title %q, want the trimmed title", repo.updated, d.Title)
				}
				return
			}
			var appErr *apperr.Error
			if !errors.As(err, &appErr) || appErr.Code != tt.wantCode || appErr.Params[apperr.FieldsParam] != tt.wantFields {
				t.Fatalf("error = %+v, want %s fields %q", appErr, tt.wantCode, tt.wantFields)
			}
			if repo.updated != nil {
				t.Fatal("challenge written despite the refusal")
			}
		})
	}
}

func TestServiceUpdateTextMissing(t *testing.T) {
	title := "New title"
	_, err := NewService(&fakeRepository{}, fakeProfiles{}, fakeCatalogue{}, clock.NewFake(now)).
		UpdateText(t.Context(), actor, "x", nil, TextChanges{Title: &title})
	if !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
		t.Fatalf("error = %v, want challenge_not_found", err)
	}
}

func TestServiceUpdateTextNothingOrEmptyDescription(t *testing.T) {
	creator := Membership{Creator: true, Participant: ParticipantActive}
	empty := ""
	for name, changes := range map[string]TextChanges{
		"nothing ({})":      {},
		"empty description": {Description: &empty},
	} {
		t.Run(name, func(t *testing.T) {
			repo := &fakeRepository{challenge: stored(VisibilityPublic), membership: creator}
			if _, err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, clock.NewFake(now)).UpdateText(t.Context(), actor, "x", nil, changes); err != nil {
				t.Fatal(err)
			}
			if repo.updated == nil || (changes.Description != nil && (repo.updated.Description == nil || *repo.updated.Description != "")) {
				t.Fatalf("written %+v, want the changes as given", repo.updated)
			}
		})
	}
}

func TestServiceUpdateTextStorageFailure(t *testing.T) {
	storage := errors.New("connection refused")
	repo := &failingUpdate{fakeRepository: fakeRepository{challenge: stored(VisibilityPublic), membership: Membership{Creator: true}}, err: storage}
	title := "New title"
	_, err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, clock.NewFake(now)).UpdateText(t.Context(), actor, "x", nil, TextChanges{Title: &title})
	var appErr *apperr.Error
	if !errors.Is(err, storage) || errors.As(err, &appErr) {
		t.Fatalf("error = %v, want it to wrap %v as an internal error", err, storage)
	}
}

// failingUpdate reads like the fake but fails the write.
type failingUpdate struct {
	fakeRepository
	err error
}

func (f *failingUpdate) UpdateText(context.Context, string, TextChanges) error { return f.err }

func TestServiceJoin(t *testing.T) {
	deadline := time.Date(2026, 3, 9, 4, 0, 0, 0, time.UTC) // end of week 1 of stored()
	tests := []struct {
		name       string
		visibility string
		membership Membership
		profile    bool
		now        time.Time
		want       apperr.Code // empty: joined
	}{
		{name: "one second before the end of week 1", visibility: VisibilityPublic, profile: true, now: deadline.Add(-time.Second)},
		{name: "at the exact end of week 1", visibility: VisibilityPublic, profile: true, now: deadline, want: apperr.CodeRegistrationClosed},
		{name: "already active", visibility: VisibilityPublic, membership: Membership{Participant: ParticipantActive}, profile: true, now: deadline.Add(-time.Hour), want: apperr.CodeAlreadyParticipant},
		{name: "abandoned, then again", visibility: VisibilityPublic, membership: Membership{Participant: ParticipantAbandoned}, profile: true, now: deadline.Add(-time.Hour), want: apperr.CodeParticipationAbandoned},
		{name: "active creator", visibility: VisibilityPublic, membership: Membership{Creator: true, Participant: ParticipantActive}, profile: true, now: deadline.Add(-time.Hour), want: apperr.CodeAlreadyParticipant},
		{name: "creator who abandoned", visibility: VisibilityPublic, membership: Membership{Creator: true, Participant: ParticipantAbandoned}, profile: true, now: deadline.Add(-time.Hour), want: apperr.CodeParticipationAbandoned},
		{name: "private without invitation", visibility: VisibilityPrivate, profile: true, now: deadline.Add(-time.Hour), want: apperr.CodeChallengeNotFound},
		{name: "incomplete profile, after the rule", visibility: VisibilityPublic, now: deadline.Add(-time.Hour), want: apperr.CodeProfileIncomplete},
		{name: "closed beats an incomplete profile", visibility: VisibilityPublic, now: deadline, want: apperr.CodeRegistrationClosed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{challenge: stored(tt.visibility), membership: tt.membership}
			d, err := NewService(repo, fakeProfiles{complete: tt.profile}, fakeCatalogue{}, clock.NewFake(tt.now)).Join(t.Context(), actor, "11111111-1111-1111-1111-111111111111")
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				if !repo.joinedAt.Equal(tt.now) || d.Role != RoleParticipant {
					t.Fatalf("joined at %s with role %v, want the clock's now and participant", repo.joinedAt, d.Role)
				}
				return
			}
			if !errors.Is(err, apperr.New(tt.want, nil)) {
				t.Fatalf("error = %v, want %s", err, tt.want)
			}
			if !repo.joinedAt.IsZero() {
				t.Fatal("participation written despite the refusal")
			}
		})
	}
}

func TestServiceJoinMissing(t *testing.T) {
	_, err := NewService(&fakeRepository{}, fakeProfiles{complete: true}, fakeCatalogue{}, clock.NewFake(now)).Join(t.Context(), actor, "x")
	if !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
		t.Fatalf("error = %v, want challenge_not_found", err)
	}
}

func TestServiceJoinFailures(t *testing.T) {
	storage := errors.New("connection refused")
	for name, svc := range map[string]*Service{
		"profile lookup fails": NewService(&fakeRepository{challenge: stored(VisibilityPublic)}, fakeProfiles{err: storage}, fakeCatalogue{}, clock.NewFake(now)),
		"storage fails":        NewService(&fakeRepository{err: storage}, fakeProfiles{complete: true}, fakeCatalogue{}, clock.NewFake(now)),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := svc.Join(t.Context(), actor, "x")
			var appErr *apperr.Error
			if !errors.Is(err, storage) || errors.As(err, &appErr) {
				t.Fatalf("error = %v, want it to wrap %v as an internal error", err, storage)
			}
		})
	}
}

func TestServiceAbandon(t *testing.T) {
	endsAt := time.Date(2026, 3, 23, 4, 0, 0, 0, time.UTC) // stored()
	tests := []struct {
		name       string
		visibility string
		membership Membership
		now        time.Time
		wantWrite  bool
		wantRole   Role
		want       apperr.Code
	}{
		{name: "active participant", visibility: VisibilityPublic, membership: Membership{Participant: ParticipantActive}, now: endsAt.Add(-time.Second), wantWrite: true, wantRole: RoleAbandoned},
		{name: "creator keeps the creator role", visibility: VisibilityPublic, membership: Membership{Creator: true, Participant: ParticipantActive}, now: endsAt.Add(-time.Hour), wantWrite: true, wantRole: RoleCreator},
		{name: "again: idempotent, nothing written", visibility: VisibilityPublic, membership: Membership{Participant: ParticipantAbandoned}, now: endsAt.Add(-time.Hour), wantRole: RoleAbandoned},
		{name: "at the exact end", visibility: VisibilityPublic, membership: Membership{Participant: ParticipantActive}, now: endsAt, want: apperr.CodeChallengeEnded},
		{name: "never joined", visibility: VisibilityPublic, now: endsAt.Add(-time.Hour), want: apperr.CodeNotFound},
		{name: "hidden", visibility: VisibilityPrivate, now: endsAt.Add(-time.Hour), want: apperr.CodeChallengeNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{challenge: stored(tt.visibility), membership: tt.membership}
			d, err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, clock.NewFake(tt.now)).Abandon(t.Context(), actor, "11111111-1111-1111-1111-111111111111")
			if tt.want != "" {
				if !errors.Is(err, apperr.New(tt.want, nil)) {
					t.Fatalf("error = %v, want %s", err, tt.want)
				}
				if !repo.abandonedAt.IsZero() {
					t.Fatal("participation written despite the refusal")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if wrote := !repo.abandonedAt.IsZero(); wrote != tt.wantWrite || (wrote && !repo.abandonedAt.Equal(tt.now)) {
				t.Fatalf("abandoned at %s, want written %v at the clock's now", repo.abandonedAt, tt.wantWrite)
			}
			if d.Role != tt.wantRole || d.Participant != ParticipantAbandoned {
				t.Fatalf("detail role %v, participation %s, want %v and abandoned", d.Role, d.Participant, tt.wantRole)
			}
		})
	}
}

func TestServiceJoinDuplicateFromStorage(t *testing.T) {
	// The insert found the participation already there: 409, not a 500.
	repo := &duplicateJoin{fakeRepository: fakeRepository{challenge: stored(VisibilityPublic)}}
	_, err := NewService(repo, fakeProfiles{complete: true}, fakeCatalogue{}, clock.NewFake(now)).Join(t.Context(), actor, "x")
	if !errors.Is(err, apperr.New(apperr.CodeAlreadyParticipant, nil)) {
		t.Fatalf("error = %v, want already_participant", err)
	}
}

type duplicateJoin struct{ fakeRepository }

func (d *duplicateJoin) Join(context.Context, string, string, func(Challenge, Membership) (time.Time, error)) error {
	return fmt.Errorf("join challenge: %w", errAlreadyParticipant)
}

func TestServiceAbandonMissingAndFailure(t *testing.T) {
	_, err := NewService(&fakeRepository{}, fakeProfiles{}, fakeCatalogue{}, clock.NewFake(now)).Abandon(t.Context(), actor, "x")
	if !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
		t.Fatalf("missing: %v, want challenge_not_found", err)
	}
	storage := errors.New("connection refused")
	_, err = NewService(&fakeRepository{err: storage}, fakeProfiles{}, fakeCatalogue{}, clock.NewFake(now)).Abandon(t.Context(), actor, "x")
	var appErr *apperr.Error
	if !errors.Is(err, storage) || errors.As(err, &appErr) {
		t.Fatalf("storage: %v, want it wrapped as an internal error", err)
	}
}

func TestServiceExclude(t *testing.T) {
	endsAt := time.Date(2026, 3, 23, 4, 0, 0, 0, time.UTC)
	creator := Membership{Creator: true, Participant: ParticipantActive}
	tests := []struct {
		name      string
		actor     Membership
		target    Membership
		now       time.Time
		wantWrite bool
		want      apperr.Code
	}{
		{name: "creator excludes a participant", actor: creator, target: Membership{Participant: ParticipantActive}, now: endsAt.Add(-time.Second), wantWrite: true},
		{name: "already out: nothing written", actor: creator, target: Membership{Participant: ParticipantAbandoned}, now: endsAt.Add(-time.Hour)},
		{name: "participant: forbidden", actor: Membership{Participant: ParticipantActive}, target: Membership{Participant: ParticipantActive}, now: endsAt.Add(-time.Hour), want: apperr.CodeForbidden},
		{name: "creator target", actor: creator, target: creator, now: endsAt.Add(-time.Hour), want: apperr.CodeCannotExcludeCreator},
		{name: "unknown target", actor: creator, now: endsAt.Add(-time.Hour), want: apperr.CodeNotFound},
		{name: "finished", actor: creator, target: Membership{Participant: ParticipantActive}, now: endsAt, want: apperr.CodeChallengeEnded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{challenge: stored(VisibilityPublic), membership: tt.actor, target: tt.target}
			err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, clock.NewFake(tt.now)).Exclude(t.Context(), actor, "c", "t")
			if tt.want != "" {
				if !errors.Is(err, apperr.New(tt.want, nil)) {
					t.Fatalf("error = %v, want %s", err, tt.want)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if wrote := !repo.abandonedAt.IsZero(); wrote != tt.wantWrite || (wrote && !repo.abandonedAt.Equal(tt.now)) {
				t.Fatalf("abandoned at %s, want written %v at the clock's now", repo.abandonedAt, tt.wantWrite)
			}
		})
	}
}

func TestServiceExcludeMissingAndFailure(t *testing.T) {
	err := NewService(&fakeRepository{}, fakeProfiles{}, fakeCatalogue{}, clock.NewFake(now)).Exclude(t.Context(), actor, "c", "t")
	if !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
		t.Fatalf("missing: %v, want challenge_not_found", err)
	}
	storage := errors.New("connection refused")
	err = NewService(&fakeRepository{err: storage}, fakeProfiles{}, fakeCatalogue{}, clock.NewFake(now)).Exclude(t.Context(), actor, "c", "t")
	var appErr *apperr.Error
	if !errors.Is(err, storage) || errors.As(err, &appErr) {
		t.Fatalf("storage: %v, want it wrapped as an internal error", err)
	}
}

func TestServiceAddCreator(t *testing.T) {
	endsAt := time.Date(2026, 3, 23, 4, 0, 0, 0, time.UTC)
	creator := Membership{Creator: true, Participant: ParticipantActive}
	tests := []struct {
		name      string
		actor     Membership
		target    Membership
		now       time.Time
		wantWrite bool
		want      apperr.Code
	}{
		{name: "creator adds an active participant", actor: creator, target: Membership{Participant: ParticipantActive}, now: endsAt.Add(-time.Second), wantWrite: true},
		{name: "already a creator: nothing written", actor: creator, target: creator, now: endsAt.Add(-time.Hour)},
		{name: "participant: forbidden", actor: Membership{Participant: ParticipantActive}, target: Membership{Participant: ParticipantActive}, now: endsAt.Add(-time.Hour), want: apperr.CodeForbidden},
		{name: "target abandoned", actor: creator, target: Membership{Participant: ParticipantAbandoned}, now: endsAt.Add(-time.Hour), want: apperr.CodeNotActiveParticipant},
		{name: "finished", actor: creator, target: Membership{Participant: ParticipantActive}, now: endsAt, want: apperr.CodeChallengeEnded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{challenge: stored(VisibilityPublic), membership: tt.actor, target: tt.target}
			_, err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, clock.NewFake(tt.now)).AddCreator(t.Context(), actor, "c", "t")
			if tt.want != "" {
				if !errors.Is(err, apperr.New(tt.want, nil)) {
					t.Fatalf("error = %v, want %s", err, tt.want)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if repo.creatorAdded != tt.wantWrite {
				t.Fatalf("creator written %v, want %v", repo.creatorAdded, tt.wantWrite)
			}
		})
	}
}

func TestServiceAddCreatorMissingAndFailure(t *testing.T) {
	_, err := NewService(&fakeRepository{}, fakeProfiles{}, fakeCatalogue{}, clock.NewFake(now)).AddCreator(t.Context(), actor, "c", "t")
	if !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
		t.Fatalf("missing: %v, want challenge_not_found", err)
	}
	storage := errors.New("connection refused")
	_, err = NewService(&fakeRepository{err: storage}, fakeProfiles{}, fakeCatalogue{}, clock.NewFake(now)).AddCreator(t.Context(), actor, "c", "t")
	var appErr *apperr.Error
	if !errors.Is(err, storage) || errors.As(err, &appErr) {
		t.Fatalf("storage: %v, want it wrapped as an internal error", err)
	}
}

func TestServiceDelete(t *testing.T) {
	tests := []struct {
		name       string
		visibility string
		membership Membership
		now        time.Time
		want       apperr.Code
	}{
		{name: "creator, ongoing", visibility: VisibilityPublic, membership: Membership{Creator: true, Participant: ParticipantActive}, now: time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)},
		{name: "creator, finished", visibility: VisibilityPrivate, membership: Membership{Creator: true, Participant: ParticipantActive}, now: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)},
		{name: "creator, upcoming", visibility: VisibilityPublic, membership: Membership{Creator: true, Participant: ParticipantActive}, now: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)},
		{name: "creator who abandoned", visibility: VisibilityPrivate, membership: Membership{Creator: true, Participant: ParticipantAbandoned}, now: now},
		{name: "participant: forbidden", visibility: VisibilityPublic, membership: Membership{Participant: ParticipantActive}, now: now, want: apperr.CodeForbidden},
		{name: "hidden", visibility: VisibilityPrivate, now: now, want: apperr.CodeChallengeNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{challenge: stored(tt.visibility), membership: tt.membership}
			err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, clock.NewFake(tt.now)).Delete(t.Context(), actor, "c")
			if tt.want != "" {
				if !errors.Is(err, apperr.New(tt.want, nil)) {
					t.Fatalf("error = %v, want %s", err, tt.want)
				}
				if !repo.deletedAt.IsZero() {
					t.Fatal("deleted despite the refusal")
				}
				return
			}
			if err != nil || !repo.deletedAt.Equal(tt.now) {
				t.Fatalf("error %v, deleted at %s, want deleted at the clock's now", err, repo.deletedAt)
			}
		})
	}
}

func TestServiceDeleteMissingAndFailure(t *testing.T) {
	err := NewService(&fakeRepository{}, fakeProfiles{}, fakeCatalogue{}, clock.NewFake(now)).Delete(t.Context(), actor, "c")
	if !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
		t.Fatalf("missing: %v, want challenge_not_found", err)
	}
	storage := errors.New("connection refused")
	err = NewService(&fakeRepository{err: storage}, fakeProfiles{}, fakeCatalogue{}, clock.NewFake(now)).Delete(t.Context(), actor, "c")
	var appErr *apperr.Error
	if !errors.Is(err, storage) || errors.As(err, &appErr) {
		t.Fatalf("storage: %v, want it wrapped as an internal error", err)
	}
}

// participants returns n participations joined one minute apart, the
// second one abandoned.
func participants(n int) []Participant {
	items := make([]Participant, n)
	for i := range items {
		items[i] = Participant{
			UserID:   fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1),
			Status:   ParticipantActive,
			JoinedAt: now.Add(time.Duration(i) * time.Minute),
		}
	}
	if n > 1 {
		items[1].Status = ParticipantAbandoned
	}
	return items
}

func TestServiceParticipants(t *testing.T) {
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{challenge: stored(VisibilityPublic), participants: participants(tt.available)}
			after := &ParticipantPosition{JoinedAt: now, UserID: "00000000-0000-0000-0000-000000000009"}
			profiles := fakeProfiles{pseudos: map[string]string{"00000000-0000-0000-0000-000000000001": "marie_92"}}
			page, err := NewService(repo, profiles, fakeCatalogue{}, clock.NewFake(now)).Participants(t.Context(), actor, "c", tt.limit, after)
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Items) != tt.wantItems || (page.Next != nil) != tt.wantNext {
				t.Fatalf("%d items, next %v, want %d items, next %v", len(page.Items), page.Next, tt.wantItems, tt.wantNext)
			}
			if tt.wantNext {
				last := page.Items[len(page.Items)-1]
				if page.Next.UserID != last.UserID || !page.Next.JoinedAt.Equal(last.JoinedAt) {
					t.Fatalf("next %+v, want the position of the last item %+v", page.Next, last)
				}
			}
			// The query asks one more item, after the given position.
			if q := repo.participantsAsked; q == nil || q.limit != tt.limit+1 || q.after != after {
				t.Fatalf("query %+v, want limit %d after the cursor", q, tt.limit+1)
			}
			if p := page.Items[0].Pseudo; p == nil || *p != "marie_92" {
				t.Fatalf("first pseudo %v, want marie_92", p)
			}
			// No pseudo (anonymized account): left nil, for a neutral name.
			if len(page.Items) > 1 && (page.Items[1].Pseudo != nil || page.Items[1].Status != ParticipantAbandoned) {
				t.Fatalf("second item %+v, want abandoned without pseudo", page.Items[1])
			}
		})
	}
}

func TestServiceParticipantsVisibility(t *testing.T) {
	tests := []struct {
		name       string
		visibility string
		membership Membership
		want       apperr.Code // empty: listed
	}{
		{name: "outsider of a public challenge", visibility: VisibilityPublic},
		{name: "participant of a private challenge", visibility: VisibilityPrivate, membership: Membership{Participant: ParticipantActive}},
		{name: "abandoned participant of a private challenge", visibility: VisibilityPrivate, membership: Membership{Participant: ParticipantAbandoned}},
		{name: "outsider of a private challenge: hidden", visibility: VisibilityPrivate, want: apperr.CodeChallengeNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{challenge: stored(tt.visibility), membership: tt.membership, participants: participants(2)}
			_, err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, clock.NewFake(now)).Participants(t.Context(), actor, "c", 20, nil)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("error = %v, want the list", err)
				}
				return
			}
			if !errors.Is(err, apperr.New(tt.want, nil)) {
				t.Fatalf("error = %v, want %s", err, tt.want)
			}
			if repo.participantsAsked != nil {
				t.Fatal("participants read despite the refusal")
			}
		})
	}
}

func TestServiceParticipantsFailures(t *testing.T) {
	storageErr := errors.New("connection reset")
	tests := []struct {
		name     string
		repo     *fakeRepository
		profiles fakeProfiles
		limit    int
		wantCode apperr.Code
		wantErr  error
	}{
		{name: "empty page", repo: &fakeRepository{challenge: stored(VisibilityPublic)}, limit: 0, wantCode: apperr.CodeValidationFailed},
		{name: "missing challenge", repo: &fakeRepository{}, limit: 20, wantCode: apperr.CodeChallengeNotFound},
		{name: "storage failure", repo: &fakeRepository{err: storageErr}, limit: 20, wantErr: storageErr},
		{name: "pseudo failure", repo: &fakeRepository{challenge: stored(VisibilityPublic), participants: participants(1)}, profiles: fakeProfiles{pseudoErr: storageErr}, limit: 20, wantErr: storageErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewService(tt.repo, tt.profiles, fakeCatalogue{}, clock.NewFake(now)).Participants(t.Context(), actor, "c", tt.limit, nil)
			if tt.wantCode != "" && !errors.Is(err, apperr.New(tt.wantCode, nil)) {
				t.Fatalf("error = %v, want %s", err, tt.wantCode)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want the cause kept", err)
			}
		})
	}
}

func TestServiceAbandonAll(t *testing.T) {
	ongoing := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name          string
		participant   ParticipantStatus
		now           time.Time
		wantAbandoned bool
	}{
		{name: "active in an ongoing challenge: abandoned", participant: ParticipantActive, now: ongoing, wantAbandoned: true},
		{name: "active in an upcoming challenge: abandoned", participant: ParticipantActive, now: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), wantAbandoned: true},
		// Finished meanwhile (at the end instant exactly): the results stay.
		{name: "active in a finished challenge: kept", participant: ParticipantActive, now: time.Date(2026, 3, 23, 4, 0, 0, 0, time.UTC)},
		{name: "already abandoned: unchanged", participant: ParticipantAbandoned, now: ongoing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{challenge: stored(VisibilityPrivate), membership: Membership{Participant: tt.participant}, active: []string{"c"}}
			if err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, clock.NewFake(tt.now)).AbandonAll(t.Context(), actor); err != nil {
				t.Fatal(err)
			}
			if abandoned := !repo.abandonedAt.IsZero(); abandoned != tt.wantAbandoned {
				t.Fatalf("abandoned %v (at %s), want %v", abandoned, repo.abandonedAt, tt.wantAbandoned)
			}
			if tt.wantAbandoned && !repo.abandonedAt.Equal(tt.now) {
				t.Fatalf("abandoned at %s, want the clock's now", repo.abandonedAt)
			}
		})
	}
}

func TestServiceAbandonAllFailures(t *testing.T) {
	storageErr := errors.New("connection reset")
	tests := []struct {
		name    string
		repo    *fakeRepository
		wantErr error
	}{
		{name: "nothing to abandon", repo: &fakeRepository{}},
		// Deleted between the listing and its lock: skipped.
		{name: "challenge deleted meanwhile", repo: &fakeRepository{active: []string{"c"}}},
		{name: "listing failure", repo: &fakeRepository{activeErr: storageErr}, wantErr: storageErr},
		{name: "abandon failure", repo: &fakeRepository{active: []string{"c"}, err: storageErr}, wantErr: storageErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewService(tt.repo, fakeProfiles{}, fakeCatalogue{}, clock.NewFake(now)).AbandonAll(t.Context(), actor)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestServiceMemberships(t *testing.T) {
	want := []UserChallenge{{ID: "c1", Timezone: "America/Toronto", Membership: Membership{Creator: true, Participant: ParticipantAbandoned}}}
	got, err := NewService(&fakeRepository{memberships: want}, nil, nil, clock.NewFake(now)).Memberships(t.Context(), actor)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Memberships() = %+v, %v, want %+v", got, err, want)
	}

	storage := errors.New("connection refused")
	if _, err := NewService(&fakeRepository{membershipsErr: storage}, nil, nil, clock.NewFake(now)).Memberships(t.Context(), actor); !errors.Is(err, storage) {
		t.Fatalf("error = %v, want it to wrap %v", err, storage)
	}
}
