package challenge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
	complete bool
	err      error
}

func (f fakeProfiles) ProfileComplete(context.Context, string) (bool, error) {
	return f.complete, f.err
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
