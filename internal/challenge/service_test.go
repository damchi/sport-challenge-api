package challenge

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
	"github.com/damchi/sport-challenge-api/internal/scoring"
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
	// lastCreator serves LastCreatorChallenges; lastCreatorErr fails it.
	lastCreator    []string
	lastCreatorErr error
	// memberships serve Memberships; membershipsErr fails it.
	memberships    []UserChallenge
	membershipsErr error
	// others are the memberships Get gives for users other than actor.
	others map[string]Membership
	// members serve ParticipantMemberships; membersErr fails it.
	members    map[string]Membership
	membersErr error
	// invitationsAt is the instant of the last read, when its invitations
	// are judged.
	invitationsAt time.Time
	// categoriesOf serves CategoriesOf; categoriesAsked records the ids
	// asked for, categoriesErr fails it.
	categoriesOf    map[string][]Category
	categoriesAsked []string
	categoriesErr   error
}

func (f *fakeRepository) CategoriesOf(_ context.Context, ids []string) (map[string][]Category, error) {
	f.categoriesAsked = ids
	out := make(map[string][]Category, len(ids))
	for _, id := range ids {
		if cats, ok := f.categoriesOf[id]; ok {
			out[id] = cats
		}
	}
	return out, f.categoriesErr
}

// fakeStandings gives the standings it holds, by challenge ID, of the
// challenges asked for; asked records them and askedAt the instant, err
// fails the call.
type fakeStandings struct {
	standings map[string]Standing
	asked     *[]Challenge
	askedAt   *time.Time
	err       error
}

func (f fakeStandings) Standings(_ context.Context, _ string, challenges []Challenge, now time.Time) (map[string]Standing, error) {
	if f.asked != nil {
		*f.asked = challenges
	}
	if f.askedAt != nil {
		*f.askedAt = now
	}
	out := make(map[string]Standing)
	for _, c := range challenges {
		if st, ok := f.standings[c.ID]; ok {
			out[c.ID] = st
		}
	}
	return out, f.err
}

func (f *fakeRepository) ActiveParticipations(context.Context, string, time.Time) ([]string, error) {
	return f.active, f.activeErr
}

func (f *fakeRepository) LastCreatorChallenges(context.Context, string, time.Time) ([]string, error) {
	return f.lastCreator, f.lastCreatorErr
}

func (f *fakeRepository) Memberships(context.Context, string) ([]UserChallenge, error) {
	return f.memberships, f.membershipsErr
}

// EraseUser is covered by the integration tests.
func (f *fakeRepository) EraseUser(context.Context, string) error { return nil }

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

func (f *fakeRepository) ParticipantMemberships(context.Context, string) (map[string]Membership, error) {
	return f.members, f.membersErr
}

func (f *fakeRepository) Get(_ context.Context, _, actorID string, now time.Time) (Challenge, Membership, error) {
	f.invitationsAt = now
	if f.err != nil {
		return Challenge{}, Membership{}, f.err
	}
	if f.challenge == nil {
		return Challenge{}, Membership{}, fmt.Errorf("read challenge: %w", errChallengeNotFound)
	}
	if f.others != nil && actorID != actor {
		return *f.challenge, f.others[actorID], nil
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

func (f *fakeRepository) Join(_ context.Context, _, _ string, _ time.Time, decide func(Challenge, Membership) (time.Time, error)) error {
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

func (f *fakeRepository) Abandon(_ context.Context, _, _ string, _ time.Time, decide func(Challenge, Membership) (bool, time.Time, error)) error {
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

func (f *fakeRepository) Exclude(_ context.Context, _, _, _ string, _ time.Time, decide func(Challenge, Membership, Membership) (bool, time.Time, error)) error {
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

func (f *fakeRepository) AddCreator(_ context.Context, _, _, _ string, _ time.Time, decide func(Challenge, Membership, Membership) (bool, error)) error {
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

func (f *fakeRepository) Delete(_ context.Context, _, _ string, _ time.Time, decide func(Challenge, Membership) (time.Time, error)) error {
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
	names     map[string]Name
	pseudoErr error
}

// marie is the public name of a participant in the listings.
var marie = Name{Pseudo: ptrTo("marie_92"), FirstName: ptrTo("Marie-Ève"), LastName: ptrTo("Tremblay")}

func ptrTo(s string) *string { return &s }

func (f fakeProfiles) ProfileComplete(context.Context, string) (bool, error) {
	return f.complete, f.err
}

func (f fakeProfiles) Names(context.Context, []string) (map[string]Name, error) {
	return f.names, f.pseudoErr
}

type fakeCatalogue struct {
	factors map[string]float64
	err     error
}

func (f fakeCatalogue) SuggestedFactors(context.Context) (map[string]float64, error) {
	return f.factors, f.err
}

// catalogueModes are the distance modes of the fake catalogue.
var catalogueModes = map[string]string{"run": "required", "walk": "required", "ride": "required", "rowing": "required", "yoga": "none", "other": "optional"}

func (f fakeCatalogue) DistanceMode(_ context.Context, code string) (string, bool, error) {
	mode, ok := catalogueModes[code]
	return mode, ok, f.err
}

func suggested() map[string]float64 {
	return map[string]float64{"run": 1, "walk": 1, "ride": 0.3, "other": 1}
}

func TestServiceCreate(t *testing.T) {
	repo := &fakeRepository{}
	clk := clock.NewFake(now)
	svc := NewService(repo, fakeProfiles{complete: true}, fakeCatalogue{factors: suggested()}, fakeStandings{}, clk)

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
	wantTypes := []string{CategoryCumulativeTime, CategoryCumulativeDistance, CategoryElimination}
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
	if c.ReferenceSport != "run" {
		t.Errorf("reference %q, want run by default", c.ReferenceSport)
	}
	if p := c.Categories[2].Params; p.Sport != "run" || p.StepMeters != 2000 {
		t.Errorf("elimination params %+v, want run with a 2000 m step", p)
	}
}

// TestServiceCreateOptionalCategories: the cumulative time is always there,
// the others only when chosen, each at the position of its type.
func TestServiceCreateOptionalCategories(t *testing.T) {
	type placed struct {
		Type     string
		Position int
	}
	tests := []struct {
		name  string
		draft Draft
		want  []placed
	}{
		{"cumulative time only", with(func(d *Draft) { d.Distance = false; d.Elimination = nil }), []placed{{CategoryCumulativeTime, 1}}},
		{"elimination only", with(func(d *Draft) { d.Distance = false }), []placed{{CategoryCumulativeTime, 1}, {CategoryElimination, 3}}},
		{"distance only", with(func(d *Draft) { d.Elimination = nil }), []placed{{CategoryCumulativeTime, 1}, {CategoryCumulativeDistance, 2}}},
		{"points only", with(func(d *Draft) {
			d.Distance = false
			d.Elimination = nil
			d.Points = points(scoring.CriterionSessionCount)
		}), []placed{{CategoryCumulativeTime, 1}, {CategoryPoints, 4}}},
		{"every category", with(func(d *Draft) { d.Points = points(scoring.CriterionSessionCount) }), []placed{
			{CategoryCumulativeTime, 1}, {CategoryCumulativeDistance, 2}, {CategoryElimination, 3}, {CategoryPoints, 4},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{}
			svc := NewService(repo, fakeProfiles{complete: true}, fakeCatalogue{factors: suggested()}, fakeStandings{}, clock.NewFake(now))
			c, err := svc.Create(t.Context(), actor, tt.draft)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]placed, len(repo.created.Categories))
			for i, cat := range repo.created.Categories {
				got[i] = placed{cat.Type, cat.Position}
			}
			if !slices.Equal(got, tt.want) || len(c.Categories) != len(tt.want) {
				t.Fatalf("stored categories %v, want %v", got, tt.want)
			}
		})
	}
}

// TestServiceCreatePoints: the settings of the points category are frozen,
// the minimum session in seconds, and the factors only with the longest
// distance, the same as those of the cumulative distance.
func TestServiceCreatePoints(t *testing.T) {
	ridden := map[string]float64{"run": 1, "walk": 1, "ride": 0.25, "other": 1}
	tests := []struct {
		name        string
		draft       Draft
		wantFactors map[string]float64
	}{
		{
			name: "longest distance without the cumulative distance",
			draft: with(func(d *Draft) {
				d.Distance = false
				d.Points = &PointsDraft{Criteria: []string{scoring.CriterionLongestDistance, scoring.CriterionSessionCount}, Podium: 10, MinSessionMinutes: minutes(30)}
				d.DistanceFactors = map[string]float64{"ride": 0.25}
			}),
			wantFactors: ridden,
		},
		{
			name: "longest distance with the cumulative distance",
			draft: with(func(d *Draft) {
				d.Points = &PointsDraft{Criteria: []string{scoring.CriterionLongestDistance, scoring.CriterionSessionCount}, Podium: 10, MinSessionMinutes: minutes(30)}
				d.DistanceFactors = map[string]float64{"ride": 0.25}
			}),
			wantFactors: ridden,
		},
		{
			name: "no factors without the longest distance",
			draft: with(func(d *Draft) {
				d.Points = &PointsDraft{Criteria: []string{scoring.CriterionLongestDuration, scoring.CriterionSessionCount}, Podium: 10, MinSessionMinutes: minutes(30)}
			}),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{}
			svc := NewService(repo, fakeProfiles{complete: true}, fakeCatalogue{factors: suggested()}, fakeStandings{}, clock.NewFake(now))
			if _, err := svc.Create(t.Context(), actor, tt.draft); err != nil {
				t.Fatal(err)
			}
			cats := repo.created.Categories
			p := cats[len(cats)-1].Params
			if cats[len(cats)-1].Type != CategoryPoints || !slices.Equal(p.Criteria, tt.draft.Points.Criteria) || p.Podium != 10 || p.MinSessionSeconds != 1800 {
				t.Fatalf("points category %+v, want the criteria, a top 10 and 1800 s", cats[len(cats)-1])
			}
			if !maps.Equal(p.Factors, tt.wantFactors) {
				t.Fatalf("points factors %v, want %v", p.Factors, tt.wantFactors)
			}
			if tt.wantFactors != nil && tt.draft.Distance && !maps.Equal(cats[1].Params.Factors, p.Factors) {
				t.Fatalf("distance factors %v and points factors %v, want the same", cats[1].Params.Factors, p.Factors)
			}
		})
	}
}

// TestServiceCreatePointsDefaultSession: without a minimum session, the
// points category counts the sessions of 20 min.
func TestServiceCreatePointsDefaultSession(t *testing.T) {
	repo := &fakeRepository{}
	svc := NewService(repo, fakeProfiles{complete: true}, fakeCatalogue{factors: suggested()}, fakeStandings{}, clock.NewFake(now))
	if _, err := svc.Create(t.Context(), actor, with(func(d *Draft) { d.Points = points(scoring.CriterionSessionCount) })); err != nil {
		t.Fatal(err)
	}
	cats := repo.created.Categories
	if p := cats[len(cats)-1].Params; p.MinSessionSeconds != 1200 {
		t.Fatalf("minimum session %d s, want 1200 by default", p.MinSessionSeconds)
	}
}

// TestServiceCreateWithAReferenceSport: the suggestions are brought back to
// the reference, the creator's factors replace them, and the elimination is
// run in the reference.
func TestServiceCreateWithAReferenceSport(t *testing.T) {
	catalogue := map[string]float64{"run": 1, "walk": 1, "ride": 0.3, "rowing": 0.5, "other": 1}
	tests := []struct {
		name      string
		reference string
		factors   map[string]float64
		want      map[string]float64
	}{
		{name: "ride", reference: "ride", want: map[string]float64{"run": 1, "walk": 1, "ride": 1, "rowing": 1, "other": 1}},
		{name: "rowing", reference: "rowing", want: map[string]float64{"run": 1, "walk": 1, "ride": 0.6, "rowing": 1, "other": 1}},
		{name: "run changed under ride", reference: "ride", factors: map[string]float64{"run": 0.5}, want: map[string]float64{"run": 0.5, "walk": 1, "ride": 1, "rowing": 1, "other": 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{}
			svc := NewService(repo, fakeProfiles{complete: true}, fakeCatalogue{factors: catalogue}, fakeStandings{}, clock.NewFake(now))
			c, err := svc.Create(t.Context(), actor, with(func(d *Draft) { d.ReferenceSport = sportCode(tt.reference); d.DistanceFactors = tt.factors }))
			if err != nil {
				t.Fatal(err)
			}
			if c.ReferenceSport != tt.reference || repo.created.ReferenceSport != tt.reference {
				t.Errorf("reference %q, stored %q, want %s", c.ReferenceSport, repo.created.ReferenceSport, tt.reference)
			}
			if got := c.Categories[1].Params.Factors; !maps.Equal(got, tt.want) {
				t.Errorf("factors %v, want %v", got, tt.want)
			}
			if p := c.Categories[2].Params; p.Sport != tt.reference {
				t.Errorf("elimination in %s, want %s", p.Sport, tt.reference)
			}
		})
	}
}

// TestServiceCreateRefusesTheReference: a reference without a required
// distance is refused before anything is written.
func TestServiceCreateRefusesTheReference(t *testing.T) {
	for _, reference := range []string{"yoga", "other", "quidditch", ""} {
		t.Run(reference, func(t *testing.T) {
			repo := &fakeRepository{}
			svc := NewService(repo, fakeProfiles{complete: true}, fakeCatalogue{factors: suggested()}, fakeStandings{}, clock.NewFake(now))
			_, err := svc.Create(t.Context(), actor, with(func(d *Draft) { d.ReferenceSport = sportCode(reference) }))
			var appErr *apperr.Error
			if !errors.As(err, &appErr) || appErr.Code != apperr.CodeValidationFailed || appErr.Params[apperr.FieldsParam] != "reference_sport" {
				t.Fatalf("error %v, want 400 validation_failed on reference_sport", err)
			}
			if repo.created != nil {
				t.Fatal("challenge stored despite the refusal")
			}
		})
	}
}

func TestServiceCreateFreezesRunAtOne(t *testing.T) {
	// A catalogue that suggests another factor for run, or none at all.
	for name, catalogue := range map[string]map[string]float64{
		"run suggested at 0.8": {"run": 0.8, "ride": 0.3},
		"run missing":          {"ride": 0.3},
	} {
		t.Run(name, func(t *testing.T) {
			svc := NewService(&fakeRepository{}, fakeProfiles{complete: true}, fakeCatalogue{factors: catalogue}, fakeStandings{}, clock.NewFake(now))
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
	if _, err := NewService(repo, fakeProfiles{complete: true}, fakeCatalogue{factors: suggested()}, fakeStandings{}, clock.NewFake(now)).
		Create(t.Context(), actor, with(func(d *Draft) { d.Description = &empty })); err != nil {
		t.Fatal(err)
	}
	if repo.created.Description != nil {
		t.Fatalf("description %q stored, want none", *repo.created.Description)
	}
}

func TestServiceCreateDoesNotChangeTheCatalogue(t *testing.T) {
	catalogue := suggested()
	svc := NewService(&fakeRepository{}, fakeProfiles{complete: true}, fakeCatalogue{factors: catalogue}, fakeStandings{}, clock.NewFake(now))
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
			_, err := NewService(repo, tt.profiles, tt.catalogue, fakeStandings{}, clock.NewFake(now)).Create(t.Context(), actor, tt.draft)
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
	svc := NewService(&fakeRepository{}, fakeProfiles{complete: true}, fakeCatalogue{factors: suggested()}, fakeStandings{}, clk)
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
			page, err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clk).List(t.Context(), actor, ScopeMine, StatusOngoing, tt.limit, nil)
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

// TestServiceListRoleAndStatus checks that each item gets the role of its
// membership and its status at the clock's now.
func TestServiceListRoleAndStatus(t *testing.T) {
	active, abandoned := string(ParticipantActive), string(ParticipantAbandoned)
	day := 24 * time.Hour
	tests := []struct {
		name       string
		summary    Summary
		wantRole   Role
		wantStatus string
	}{
		{name: "creator, upcoming", summary: Summary{Creator: true, MyStatus: &active, StartsAt: now.Add(day), EndsAt: now.Add(15 * day)}, wantRole: RoleCreator, wantStatus: StatusUpcoming},
		{name: "creator who abandoned stays a creator", summary: Summary{Creator: true, MyStatus: &abandoned, StartsAt: now.Add(-day), EndsAt: now.Add(13 * day)}, wantRole: RoleCreator, wantStatus: StatusOngoing},
		{name: "participant, ongoing from its first instant", summary: Summary{MyStatus: &active, StartsAt: now, EndsAt: now.Add(14 * day)}, wantRole: RoleParticipant, wantStatus: StatusOngoing},
		{name: "abandoned, finished at its last instant", summary: Summary{MyStatus: &abandoned, StartsAt: now.Add(-14 * day), EndsAt: now}, wantRole: RoleAbandoned, wantStatus: StatusFinished},
		{name: "invited", summary: Summary{Invited: true, StartsAt: now.Add(day), EndsAt: now.Add(15 * day)}, wantRole: RoleInvited, wantStatus: StatusUpcoming},
		{name: "non participant", summary: Summary{StartsAt: now.Add(-15 * day), EndsAt: now.Add(-day)}, wantRole: RoleOutsider, wantStatus: StatusFinished},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{summaries: []Summary{tt.summary}}
			page, err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)).List(t.Context(), actor, ScopePublic, "", 20, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := page.Items[0]; got.Role != tt.wantRole || got.Status != tt.wantStatus {
				t.Fatalf("role %v, status %q, want %v, %q", got.Role, got.Status, tt.wantRole, tt.wantStatus)
			}
		})
	}
}

// TestServiceListStandings checks which challenges of the actor's own list
// get a standing: ongoing ones they are an active participant of, read for
// the whole page at once with their categories.
func TestServiceListStandings(t *testing.T) {
	active, abandoned := string(ParticipantActive), string(ParticipantAbandoned)
	day := 24 * time.Hour
	ongoing := func(id string, status *string) Summary {
		return Summary{ID: id, Timezone: "America/Toronto", MyStatus: status, StartsAt: now.Add(-day), EndsAt: now.Add(13 * day)}
	}
	items := []Summary{
		ongoing("running", &active),
		ongoing("abandoned", &abandoned),
		ongoing("creator only", nil),
		{ID: "upcoming", MyStatus: &active, StartsAt: now.Add(day), EndsAt: now.Add(15 * day)},
		{ID: "finished", MyStatus: &active, StartsAt: now.Add(-14 * day), EndsAt: now},
		ongoing("also running", &active),
	}
	cats := []Category{{Type: CategoryCumulativeTime, Position: 1}, {Type: CategoryElimination, Position: 3, Params: Params{Sport: "run", StepMeters: 2000}}}
	standing := Standing{Week: 1, WeekEndsAt: now.Add(6 * day), Rankings: []Ranking{{Type: CategoryCumulativeTime, Rank: 2, RankedCount: 5}}}
	repo := &fakeRepository{summaries: items, categoriesOf: map[string][]Category{"running": cats, "also running": cats}}
	var asked []Challenge
	var askedAt time.Time
	standings := fakeStandings{standings: map[string]Standing{"running": standing}, asked: &asked, askedAt: &askedAt}

	page, err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, standings, clock.NewFake(now)).List(t.Context(), actor, ScopeMine, "", 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"running", "also running"}; !reflect.DeepEqual(repo.categoriesAsked, want) {
		t.Fatalf("categories read for %v, want %v", repo.categoriesAsked, want)
	}
	want := []Challenge{
		{ID: "running", Timezone: "America/Toronto", StartsAt: items[0].StartsAt, EndsAt: items[0].EndsAt, Categories: cats},
		{ID: "also running", Timezone: "America/Toronto", StartsAt: items[5].StartsAt, EndsAt: items[5].EndsAt, Categories: cats},
	}
	if !reflect.DeepEqual(asked, want) {
		t.Fatalf("standings asked for %+v, want %+v", asked, want)
	}
	// The instant of the statuses: an ongoing challenge is judged at it.
	if !askedAt.Equal(now) {
		t.Fatalf("standings asked at %s, want the clock's now %s", askedAt, now)
	}
	for _, it := range page.Items {
		switch {
		case it.ID == "running":
			if it.MyWeek == nil || !reflect.DeepEqual(*it.MyWeek, standing) {
				t.Fatalf("%s: my week %+v, want %+v", it.ID, it.MyWeek, standing)
			}
		case it.MyWeek != nil:
			// "also running" is asked for, but the leaderboard leaves it out.
			t.Fatalf("%s: my week %+v, want none", it.ID, it.MyWeek)
		}
	}
}

func TestServiceListStandingsOnlyInMine(t *testing.T) {
	active := string(ParticipantActive)
	repo := &fakeRepository{summaries: []Summary{{ID: "running", MyStatus: &active, StartsAt: now.Add(-time.Hour), EndsAt: now.AddDate(0, 0, 14)}}}
	var asked []Challenge
	page, err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{asked: &asked}, clock.NewFake(now)).List(t.Context(), actor, ScopePublic, "", 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	if repo.categoriesAsked != nil || asked != nil || page.Items[0].MyWeek != nil {
		t.Fatalf("public list read categories %v and standings %v, want nothing", repo.categoriesAsked, asked)
	}
}

func TestServiceListStandingsFailures(t *testing.T) {
	boom := errors.New("boom")
	active := string(ParticipantActive)
	running := []Summary{{ID: "running", MyStatus: &active, StartsAt: now.Add(-time.Hour), EndsAt: now.AddDate(0, 0, 14)}}
	tests := []struct {
		name      string
		repo      *fakeRepository
		standings fakeStandings
	}{
		{name: "categories", repo: &fakeRepository{summaries: running, categoriesErr: boom}},
		{name: "standings", repo: &fakeRepository{summaries: running, categoriesOf: map[string][]Category{"running": nil}}, standings: fakeStandings{err: boom}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewService(tt.repo, fakeProfiles{}, fakeCatalogue{}, tt.standings, clock.NewFake(now)).List(t.Context(), actor, ScopeMine, "", 20, nil)
			if !errors.Is(err, boom) {
				t.Fatalf("error = %v, want it to wrap %v", err, boom)
			}
		})
	}
}

func TestServiceListRefusesAnEmptyPage(t *testing.T) {
	_, err := NewService(&fakeRepository{summaries: summaries(3)}, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)).List(t.Context(), actor, ScopePublic, "", 0, nil)
	if !errors.Is(err, apperr.New(apperr.CodeValidationFailed, nil)) {
		t.Fatalf("error = %v, want validation_failed on limit", err)
	}
}

func TestServiceListStorageFailure(t *testing.T) {
	storage := errors.New("connection refused")
	_, err := NewService(&fakeRepository{err: storage}, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)).List(t.Context(), actor, ScopePublic, "", 20, nil)
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
			d, err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(tt.now)).Get(t.Context(), actor, "11111111-1111-1111-1111-111111111111")
			if err != nil {
				t.Fatal(err)
			}
			if d.Role != tt.wantRole || d.Status != tt.wantStatus || d.CanJoin != tt.wantCanJoin || d.CurrentWeek != tt.wantWeek || !d.WeekEndsAt.Equal(tt.wantWeekEnds) {
				t.Fatalf("got role %v, %s, can join %v, week %d ending %s; want %v, %s, %v, %d ending %s",
					d.Role, d.Status, d.CanJoin, d.CurrentWeek, d.WeekEndsAt, tt.wantRole, tt.wantStatus, tt.wantCanJoin, tt.wantWeek, tt.wantWeekEnds)
			}
			// 3 weeks whatever the status: the last value current_week takes.
			if d.WeekCount != 3 {
				t.Fatalf("week count %d, want 3", d.WeekCount)
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
		_, err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)).Get(t.Context(), actor, "11111111-1111-1111-1111-111111111111")
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
	_, err := NewService(&fakeRepository{err: storage}, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)).Get(t.Context(), actor, "x")
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
			d, err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))).
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
	_, err := NewService(&fakeRepository{}, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)).
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
			if _, err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)).UpdateText(t.Context(), actor, "x", nil, changes); err != nil {
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
	_, err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)).UpdateText(t.Context(), actor, "x", nil, TextChanges{Title: &title})
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
		{name: "private with a valid invitation", visibility: VisibilityPrivate, membership: Membership{Invited: true}, profile: true, now: deadline.Add(-time.Hour)},
		{name: "incomplete profile, after the rule", visibility: VisibilityPublic, now: deadline.Add(-time.Hour), want: apperr.CodeProfileIncomplete},
		{name: "closed beats an incomplete profile", visibility: VisibilityPublic, now: deadline, want: apperr.CodeRegistrationClosed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{challenge: stored(tt.visibility), membership: tt.membership}
			d, err := NewService(repo, fakeProfiles{complete: tt.profile}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(tt.now)).Join(t.Context(), actor, "11111111-1111-1111-1111-111111111111")
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
	_, err := NewService(&fakeRepository{}, fakeProfiles{complete: true}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)).Join(t.Context(), actor, "x")
	if !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
		t.Fatalf("error = %v, want challenge_not_found", err)
	}
}

func TestServiceJoinFailures(t *testing.T) {
	storage := errors.New("connection refused")
	for name, svc := range map[string]*Service{
		"profile lookup fails": NewService(&fakeRepository{challenge: stored(VisibilityPublic)}, fakeProfiles{err: storage}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)),
		"storage fails":        NewService(&fakeRepository{err: storage}, fakeProfiles{complete: true}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)),
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
			d, err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(tt.now)).Abandon(t.Context(), actor, "11111111-1111-1111-1111-111111111111")
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
	_, err := NewService(repo, fakeProfiles{complete: true}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)).Join(t.Context(), actor, "x")
	if !errors.Is(err, apperr.New(apperr.CodeAlreadyParticipant, nil)) {
		t.Fatalf("error = %v, want already_participant", err)
	}
}

type duplicateJoin struct{ fakeRepository }

func (d *duplicateJoin) Join(context.Context, string, string, time.Time, func(Challenge, Membership) (time.Time, error)) error {
	return fmt.Errorf("join challenge: %w", errAlreadyParticipant)
}

// TestServiceInvitationDeadline: the end of week 1 of stored(), once the
// rule lets the actor invite; the invitations are judged at the clock's now.
func TestServiceInvitationDeadline(t *testing.T) {
	deadline := time.Date(2026, 3, 9, 4, 0, 0, 0, time.UTC) // end of week 1 of stored()
	tests := []struct {
		name             string
		visibility       string
		membersCanInvite bool
		membership       Membership
		now              time.Time
		want             apperr.Code // empty: may invite
	}{
		{name: "creator", visibility: VisibilityPrivate, membership: Membership{Creator: true, Participant: ParticipantActive}, now: deadline.Add(-time.Second)},
		{name: "participant when members may invite", visibility: VisibilityPrivate, membersCanInvite: true, membership: Membership{Participant: ParticipantActive}, now: deadline.Add(-time.Hour)},
		{name: "participant: forbidden", visibility: VisibilityPrivate, membership: Membership{Participant: ParticipantActive}, now: deadline.Add(-time.Hour), want: apperr.CodeForbidden},
		{name: "invited: forbidden", visibility: VisibilityPrivate, membersCanInvite: true, membership: Membership{Invited: true}, now: deadline.Add(-time.Hour), want: apperr.CodeForbidden},
		{name: "outsider of a private challenge: hidden", visibility: VisibilityPrivate, membersCanInvite: true, now: deadline.Add(-time.Hour), want: apperr.CodeChallengeNotFound},
		{name: "creator at the exact end of week 1", visibility: VisibilityPrivate, membership: Membership{Creator: true, Participant: ParticipantActive}, now: deadline, want: apperr.CodeRegistrationClosed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := stored(tt.visibility)
			c.MembersCanInvite = tt.membersCanInvite
			repo := &fakeRepository{challenge: c, membership: tt.membership}
			got, err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(tt.now)).InvitationDeadline(t.Context(), actor, c.ID)
			if !repo.invitationsAt.Equal(tt.now) {
				t.Fatalf("invitations judged at %s, want the clock's now %s", repo.invitationsAt, tt.now)
			}
			if tt.want == "" {
				if err != nil || !got.Equal(deadline) {
					t.Fatalf("InvitationDeadline() = %s, %v, want %s", got, err, deadline)
				}
				return
			}
			if !errors.Is(err, apperr.New(tt.want, nil)) {
				t.Fatalf("error = %v, want %s", err, tt.want)
			}
		})
	}
}

func TestServiceInvitationDeadlineMissingAndFailure(t *testing.T) {
	_, err := NewService(&fakeRepository{}, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)).InvitationDeadline(t.Context(), actor, "x")
	if !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
		t.Fatalf("missing: %v, want challenge_not_found", err)
	}
	storage := errors.New("connection refused")
	_, err = NewService(&fakeRepository{err: storage}, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)).InvitationDeadline(t.Context(), actor, "x")
	var appErr *apperr.Error
	if !errors.Is(err, storage) || errors.As(err, &appErr) {
		t.Fatalf("error = %v, want it to wrap %v as an internal error", err, storage)
	}
}

func TestServiceAbandonMissingAndFailure(t *testing.T) {
	_, err := NewService(&fakeRepository{}, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)).Abandon(t.Context(), actor, "x")
	if !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
		t.Fatalf("missing: %v, want challenge_not_found", err)
	}
	storage := errors.New("connection refused")
	_, err = NewService(&fakeRepository{err: storage}, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)).Abandon(t.Context(), actor, "x")
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
			err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(tt.now)).Exclude(t.Context(), actor, "c", "t")
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
	err := NewService(&fakeRepository{}, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)).Exclude(t.Context(), actor, "c", "t")
	if !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
		t.Fatalf("missing: %v, want challenge_not_found", err)
	}
	storage := errors.New("connection refused")
	err = NewService(&fakeRepository{err: storage}, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)).Exclude(t.Context(), actor, "c", "t")
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
			_, err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(tt.now)).AddCreator(t.Context(), actor, "c", "t")
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
	_, err := NewService(&fakeRepository{}, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)).AddCreator(t.Context(), actor, "c", "t")
	if !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
		t.Fatalf("missing: %v, want challenge_not_found", err)
	}
	storage := errors.New("connection refused")
	_, err = NewService(&fakeRepository{err: storage}, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)).AddCreator(t.Context(), actor, "c", "t")
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
			err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(tt.now)).Delete(t.Context(), actor, "c")
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
	err := NewService(&fakeRepository{}, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)).Delete(t.Context(), actor, "c")
	if !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
		t.Fatalf("missing: %v, want challenge_not_found", err)
	}
	storage := errors.New("connection refused")
	err = NewService(&fakeRepository{err: storage}, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)).Delete(t.Context(), actor, "c")
	var appErr *apperr.Error
	if !errors.Is(err, storage) || errors.As(err, &appErr) {
		t.Fatalf("storage: %v, want it wrapped as an internal error", err)
	}
}

// participants returns n participations joined one minute apart, the
// first one a creator and the second one abandoned.
func participants(n int) []Participant {
	items := make([]Participant, n)
	for i := range items {
		items[i] = Participant{
			UserID:   fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1),
			Status:   ParticipantActive,
			JoinedAt: now.Add(time.Duration(i) * time.Minute),
		}
	}
	if n > 0 {
		items[0].IsCreator = true
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
			profiles := fakeProfiles{names: map[string]Name{"00000000-0000-0000-0000-000000000001": marie}}
			page, err := NewService(repo, profiles, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)).Participants(t.Context(), actor, "c", tt.limit, after)
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
			if got := page.Items[0].Name; !reflect.DeepEqual(got, marie) {
				t.Fatalf("first name %+v, want %+v", got, marie)
			}
			// No name (account deleted after the listing was read): left empty.
			if len(page.Items) > 1 && (page.Items[1].Name != Name{} || page.Items[1].Status != ParticipantAbandoned) {
				t.Fatalf("second item %+v, want abandoned without name", page.Items[1])
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
			_, err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)).Participants(t.Context(), actor, "c", 20, nil)
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
			_, err := NewService(tt.repo, tt.profiles, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)).Participants(t.Context(), actor, "c", tt.limit, nil)
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
			if err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(tt.now)).AbandonAll(t.Context(), actor); err != nil {
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
		{name: "last creator check failure", repo: &fakeRepository{lastCreatorErr: storageErr}, wantErr: storageErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewService(tt.repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)).AbandonAll(t.Context(), actor)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// TestServiceAbandonAllRefusesTheLastCreator checks the rule of the last
// creator (README §2.2): the deletion is refused before anything is abandoned.
func TestServiceAbandonAllRefusesTheLastCreator(t *testing.T) {
	repo := &fakeRepository{challenge: stored(VisibilityPrivate), membership: Membership{Participant: ParticipantActive}, active: []string{"c"}, lastCreator: []string{"c"}}
	err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)).AbandonAll(t.Context(), actor)
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != apperr.CodeLastCreator || appErr.Params[apperr.ChallengesParam] != "c" {
		t.Fatalf("error = %v, want last_creator listing c", err)
	}
	if !repo.abandonedAt.IsZero() {
		t.Fatalf("abandoned at %s, want nothing abandoned", repo.abandonedAt)
	}
}

func TestServiceMemberships(t *testing.T) {
	want := []UserChallenge{{ID: "c1", Timezone: "America/Toronto", Membership: Membership{Creator: true, Participant: ParticipantAbandoned}}}
	got, err := NewService(&fakeRepository{memberships: want}, nil, nil, fakeStandings{}, clock.NewFake(now)).Memberships(t.Context(), actor)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Memberships() = %+v, %v, want %+v", got, err, want)
	}

	storage := errors.New("connection refused")
	if _, err := NewService(&fakeRepository{membershipsErr: storage}, nil, nil, fakeStandings{}, clock.NewFake(now)).Memberships(t.Context(), actor); !errors.Is(err, storage) {
		t.Fatalf("error = %v, want it to wrap %v", err, storage)
	}
}

// TestServiceActivitiesScopeWithoutDistanceCategory: no factors, never an
// error, so an activity there brings its duration only.
func TestServiceActivitiesScopeWithoutDistanceCategory(t *testing.T) {
	c := stored(VisibilityPublic)
	c.Categories = []Category{{Type: CategoryCumulativeTime, Position: 1}}
	repo := &fakeRepository{challenge: c, membership: active}
	got, err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)).ActivitiesScope(t.Context(), actor, "c", actor)
	if err != nil || got.ID != c.ID || got.Factors != nil {
		t.Fatalf("ActivitiesScope() = %+v, %v, want the challenge with no factors", got, err)
	}
}

func TestServiceActivitiesScope(t *testing.T) {
	const target = "22222222-2222-2222-2222-222222222222"
	tests := []struct {
		name       string
		visibility string
		actor      Membership
		others     map[string]Membership
		userID     string
		want       apperr.Code // empty: the scope
		wantMember Membership
	}{
		{name: "participant reads another", visibility: VisibilityPrivate, actor: active, others: map[string]Membership{target: abandoned}, userID: target, wantMember: abandoned},
		{name: "creator reads another", visibility: VisibilityPublic, actor: creatorActive, others: map[string]Membership{target: active}, userID: target, wantMember: active},
		{name: "own activities", visibility: VisibilityPrivate, actor: abandoned, userID: actor, wantMember: abandoned},
		{name: "outsider of a public challenge", visibility: VisibilityPublic, actor: outsider, others: map[string]Membership{target: active}, userID: target, want: apperr.CodeNotFound},
		{name: "outsider of a private challenge", visibility: VisibilityPrivate, actor: outsider, others: map[string]Membership{target: active}, userID: target, want: apperr.CodeChallengeNotFound},
		{name: "target never joined", visibility: VisibilityPublic, actor: active, others: map[string]Membership{}, userID: target, want: apperr.CodeNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			factors := map[string]float64{"run": 1, "ride": 0.3}
			c := stored(tt.visibility)
			c.Categories = []Category{
				{Type: CategoryCumulativeTime, Position: 1},
				{Type: CategoryCumulativeDistance, Position: 2, Params: Params{Factors: factors}},
				{Type: CategoryElimination, Position: 3, Params: Params{Sport: "run", StepMeters: 1000}},
			}
			repo := &fakeRepository{challenge: c, membership: tt.actor, others: tt.others}
			got, err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)).ActivitiesScope(t.Context(), actor, "c", tt.userID)
			if tt.want != "" {
				if !errors.Is(err, apperr.New(tt.want, nil)) {
					t.Fatalf("error = %v, want %s", err, tt.want)
				}
				return
			}
			want := UserChallenge{ID: c.ID, Title: c.Title, Timezone: c.Timezone, StartsAt: c.StartsAt, EndsAt: c.EndsAt, Factors: factors, Membership: tt.wantMember}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("ActivitiesScope() = %+v, %v, want %+v", got, err, want)
			}
		})
	}
}

func TestServiceActivitiesScopeFailures(t *testing.T) {
	storageErr := errors.New("connection reset")
	if _, err := NewService(&fakeRepository{}, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)).ActivitiesScope(t.Context(), actor, "c", actor); !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
		t.Fatalf("missing challenge: error = %v, want challenge_not_found", err)
	}
	if _, err := NewService(&fakeRepository{err: storageErr}, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)).ActivitiesScope(t.Context(), actor, "c", actor); !errors.Is(err, storageErr) {
		t.Fatalf("storage failure: error = %v, want the cause kept", err)
	}
}

func TestServiceAllActivitiesScope(t *testing.T) {
	const target = "22222222-2222-2222-2222-222222222222"
	members := map[string]Membership{actor: active, target: abandoned}
	tests := []struct {
		name       string
		visibility string
		actor      Membership
		want       apperr.Code // empty: the scope
	}{
		{name: "participant, private", visibility: VisibilityPrivate, actor: active},
		{name: "abandoned participant", visibility: VisibilityPrivate, actor: abandoned},
		{name: "creator, public", visibility: VisibilityPublic, actor: creatorActive},
		{name: "outsider of a public challenge", visibility: VisibilityPublic, actor: outsider, want: apperr.CodeNotFound},
		{name: "invited to a private challenge", visibility: VisibilityPrivate, actor: invited, want: apperr.CodeNotFound},
		{name: "outsider of a private challenge", visibility: VisibilityPrivate, actor: outsider, want: apperr.CodeChallengeNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			factors := map[string]float64{"run": 1, "ride": 0.3}
			c := stored(tt.visibility)
			c.Categories = []Category{
				{Type: CategoryCumulativeTime, Position: 1},
				{Type: CategoryCumulativeDistance, Position: 2, Params: Params{Factors: factors}},
			}
			repo := &fakeRepository{challenge: c, membership: tt.actor, members: members}
			got, gotMembers, err := NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now)).AllActivitiesScope(t.Context(), actor, "c")
			if tt.want != "" {
				if !errors.Is(err, apperr.New(tt.want, nil)) {
					t.Fatalf("error = %v, want %s", err, tt.want)
				}
				return
			}
			want := UserChallenge{ID: c.ID, Title: c.Title, Timezone: c.Timezone, StartsAt: c.StartsAt, EndsAt: c.EndsAt, Factors: factors}
			if err != nil || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(gotMembers, members) {
				t.Fatalf("AllActivitiesScope() = %+v, %+v, %v, want %+v, %+v", got, gotMembers, err, want, members)
			}
		})
	}
}

func TestServiceAllActivitiesScopeFailures(t *testing.T) {
	storageErr := errors.New("connection reset")
	svc := func(repo *fakeRepository) *Service {
		return NewService(repo, fakeProfiles{}, fakeCatalogue{}, fakeStandings{}, clock.NewFake(now))
	}
	if _, _, err := svc(&fakeRepository{}).AllActivitiesScope(t.Context(), actor, "c"); !errors.Is(err, apperr.New(apperr.CodeChallengeNotFound, nil)) {
		t.Fatalf("missing challenge: error = %v, want challenge_not_found", err)
	}
	if _, _, err := svc(&fakeRepository{err: storageErr}).AllActivitiesScope(t.Context(), actor, "c"); !errors.Is(err, storageErr) {
		t.Fatalf("storage failure: error = %v, want the cause kept", err)
	}
	repo := &fakeRepository{challenge: stored(VisibilityPublic), membership: active, membersErr: storageErr}
	if _, _, err := svc(repo).AllActivitiesScope(t.Context(), actor, "c"); !errors.Is(err, storageErr) {
		t.Fatalf("members failure: error = %v, want the cause kept", err)
	}
}
