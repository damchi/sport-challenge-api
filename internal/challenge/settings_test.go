package challenge

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/auth"
	"github.com/damchi/sport-challenge-api/internal/platform/clock"
)

func (f *fakeRepository) UpdateSettings(_ context.Context, _, _ string, _ time.Time, decide func(Challenge, []Category, int) (Challenge, error)) error {
	if f.challenge == nil {
		return errChallengeNotFound
	}
	next, err := decide(*f.challenge, f.challenge.Categories, f.creators())
	if err != nil {
		return err
	}
	f.settings = &next
	return nil
}

// finished is a challenge of 4 weeks in Toronto, from Monday 2026-02-02,
// over at now (2026-03-04), with the cumulative distance (the factors of
// the catalogue, ride changed to 0.5) and the elimination.
func finished(t *testing.T) *Challenge {
	t.Helper()
	toronto, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	return &Challenge{
		ID: "c1", Title: "Winter run", Visibility: VisibilityPrivate, Timezone: "America/Toronto", ReferenceSport: "run",
		StartsAt: time.Date(2026, 2, 2, 0, 0, 0, 0, toronto).UTC(), EndsAt: time.Date(2026, 3, 2, 0, 0, 0, 0, toronto).UTC(),
		Categories: []Category{
			{ID: "k1", Type: CategoryCumulativeTime, Position: positionTime},
			{ID: "k2", Type: CategoryCumulativeDistance, Position: positionDistance, Params: Params{Factors: map[string]float64{"run": 1, "walk": 1, "ride": 0.5, "other": 1}}},
			{ID: "k3", Type: CategoryElimination, Position: positionElimination, Params: Params{Sport: "run", StepMeters: 2000}},
		},
	}
}

func adminActor() auth.Actor { return auth.Actor{ID: actor, Admin: true} }

func TestServiceAdminUpdate(t *testing.T) {
	toronto, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	weeks := func(n int) *int { return &n }
	text := func(s string) *string { return &s }
	tests := []struct {
		name    string
		changes SettingsChanges
		// check looks at what was written and the change returned.
		check func(t *testing.T, written Challenge, change SettingsChange)
	}{
		{
			name:    "a finished challenge made longer ends later, its first week as it was",
			changes: SettingsChanges{Weeks: weeks(6)},
			check: func(t *testing.T, written Challenge, change SettingsChange) {
				if want := time.Date(2026, 3, 16, 0, 0, 0, 0, toronto); !written.EndsAt.Equal(want) {
					t.Fatalf("ends at %v, want %v", written.EndsAt, want)
				}
				if !reflect.DeepEqual(change.Old, map[string]any{"weeks": 4}) || !reflect.DeepEqual(change.New, map[string]any{"weeks": 6}) {
					t.Fatalf("change %v -> %v, want the weeks only", change.Old, change.New)
				}
				if change.Deadline != nil {
					t.Fatalf("deadline %v, want none: week 1 did not move", change.Deadline)
				}
				if !reflect.DeepEqual(written.Categories, categoriesOf(Draft{Distance: true, Elimination: &EliminationDraft{StepMeters: 2000}},
					map[string]float64{"run": 1, "walk": 1, "ride": 0.5, "other": 1})) {
					t.Fatalf("categories %+v, want them as they were", written.Categories)
				}
			},
		},
		{
			name: "crossfit with an elimination by repetitions of wall balls",
			changes: SettingsChanges{ReferenceSport: text("crossfit"), Categories: &CategoryChoice{
				Elimination: &EliminationDraft{Exercise: "wall_ball", StepRepetitions: 10},
			}},
			check: func(t *testing.T, written Challenge, _ SettingsChange) {
				want := Params{Sport: "crossfit", Exercise: "wall_ball", StepRepetitions: 10}
				if len(written.Categories) != 2 || !reflect.DeepEqual(written.Categories[1].Params, want) {
					t.Fatalf("categories %+v, want the time and the elimination %+v", written.Categories, want)
				}
			},
		},
		{
			name:    "the first day moved into the past moves the end of week 1",
			changes: SettingsChanges{StartDate: text("2026-01-26")},
			check: func(t *testing.T, written Challenge, change SettingsChange) {
				if want := time.Date(2026, 1, 26, 0, 0, 0, 0, toronto); !written.StartsAt.Equal(want) {
					t.Fatalf("starts at %v, want %v", written.StartsAt, want)
				}
				if want := time.Date(2026, 2, 2, 0, 0, 0, 0, toronto); change.Deadline == nil || !change.Deadline.Equal(want) {
					t.Fatalf("deadline %v, want %v", change.Deadline, want)
				}
			},
		},
		{
			name:    "a new time zone keeps the local first day",
			changes: SettingsChanges{Timezone: text("Europe/Paris")},
			check: func(t *testing.T, written Challenge, change SettingsChange) {
				paris, err := time.LoadLocation("Europe/Paris")
				if err != nil {
					t.Fatal(err)
				}
				if want := time.Date(2026, 2, 2, 0, 0, 0, 0, paris); !written.StartsAt.Equal(want) {
					t.Fatalf("starts at %v, want %v", written.StartsAt, want)
				}
				if change.Deadline == nil {
					t.Fatal("no deadline, want the end of week 1 in Paris")
				}
			},
		},
		{
			name:    "a new reference starts again from the suggested factors",
			changes: SettingsChanges{ReferenceSport: text("ride")},
			check: func(t *testing.T, written Challenge, _ SettingsChange) {
				want := map[string]float64{"run": 1, "walk": 1, "ride": 1, "other": 1}
				if got := written.Categories[1].Params.Factors; !reflect.DeepEqual(got, want) {
					t.Fatalf("factors %v, want %v", got, want)
				}
				if written.ReferenceSport != "ride" || written.Categories[2].Params.Sport != "ride" {
					t.Fatalf("reference %q, elimination in %q, want ride", written.ReferenceSport, written.Categories[2].Params.Sport)
				}
			},
		},
		{
			name:    "run to crossfit without the categories that need a distance",
			changes: SettingsChanges{ReferenceSport: text("crossfit"), Categories: &CategoryChoice{}},
			check: func(t *testing.T, written Challenge, _ SettingsChange) {
				if written.ReferenceSport != "crossfit" || len(written.Categories) != 1 || written.Categories[0].Params.Factors != nil {
					t.Fatalf("reference %q, categories %+v, want crossfit with the cumulative time only, no factor", written.ReferenceSport, written.Categories)
				}
			},
		},
		{
			name:    "factors sent replace those they name, the others kept",
			changes: SettingsChanges{DistanceFactors: map[string]float64{"walk": 0.8}},
			check: func(t *testing.T, written Challenge, change SettingsChange) {
				want := map[string]float64{"run": 1, "walk": 0.8, "ride": 0.5, "other": 1}
				if got := written.Categories[1].Params.Factors; !reflect.DeepEqual(got, want) {
					t.Fatalf("factors %v, want %v", got, want)
				}
				if _, ok := change.New["categories"]; !ok || len(change.New) != 1 {
					t.Fatalf("new value %v, want the categories only", change.New)
				}
			},
		},
		{
			name: "categories replace the optional ones, the cumulative time kept",
			changes: SettingsChanges{Categories: &CategoryChoice{
				Points: &PointsDraft{Criteria: []string{"session_count"}, Podium: 10}, Elevation: true,
			}},
			check: func(t *testing.T, written Challenge, _ SettingsChange) {
				var types []string
				for _, c := range written.Categories {
					types = append(types, c.Type)
				}
				if want := []string{CategoryCumulativeTime, CategoryPoints, CategoryElevation}; !reflect.DeepEqual(types, want) {
					t.Fatalf("categories %v, want %v", types, want)
				}
				if p := written.Categories[1].Params; p.MinSessionSeconds != 20*60 || p.Factors != nil {
					t.Fatalf("points %+v, want the default minimum session and no factors", p)
				}
			},
		},
		{
			name:    "title trimmed, empty description removed, visibility and invitations",
			changes: SettingsChanges{Title: text("  Spring run  "), Description: text(""), Visibility: text(VisibilityPublic), MembersCanInvite: new(true)},
			check: func(t *testing.T, written Challenge, change SettingsChange) {
				if written.Title != "Spring run" || written.Description != nil || written.Visibility != VisibilityPublic || !written.MembersCanInvite {
					t.Fatalf("written %+v", written)
				}
				if change.New["title"] != "Spring run" || change.Old["title"] != "Winter run" {
					t.Fatalf("change %v -> %v", change.Old, change.New)
				}
				if _, ok := change.New["description"]; ok {
					t.Fatalf("description in the change %v, want it left out (none before, none after)", change.New)
				}
			},
		},
		{
			name: "the distance removed, the points by distance keep the factors of the challenge",
			changes: SettingsChanges{Categories: &CategoryChoice{
				Points: &PointsDraft{Criteria: []string{"longest_distance"}, Podium: 5},
			}},
			check: func(t *testing.T, written Challenge, _ SettingsChange) {
				want := map[string]float64{"run": 1, "walk": 1, "ride": 0.5, "other": 1}
				if got := written.Categories[1]; got.Type != CategoryPoints || !reflect.DeepEqual(got.Params.Factors, want) {
					t.Fatalf("points %+v, want the factors %v", got, want)
				}
			},
		},
		{
			name:    "a new reference with factors: suggested ones brought back to it, then those sent",
			changes: SettingsChanges{ReferenceSport: text("ride"), DistanceFactors: map[string]float64{"walk": 0.4}},
			check: func(t *testing.T, written Challenge, _ SettingsChange) {
				want := map[string]float64{"run": 1, "walk": 0.4, "ride": 1, "other": 1}
				if got := written.Categories[1].Params.Factors; !reflect.DeepEqual(got, want) {
					t.Fatalf("factors %v, want %v", got, want)
				}
			},
		},
		{
			name:    "nothing changed: nothing in the journal values",
			changes: SettingsChanges{Weeks: weeks(4)},
			check: func(t *testing.T, _ Challenge, change SettingsChange) {
				if len(change.Old) != 0 || len(change.New) != 0 || change.Deadline != nil {
					t.Fatalf("change %+v, want none", change)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{challenge: finished(t)}
			svc := NewService(repo, fakeProfiles{}, fakeCatalogue{factors: suggested()}, fakeStandings{}, clock.NewFake(now))
			change, err := svc.AdminUpdate(t.Context(), adminActor(), "c1", tt.changes)
			if err != nil {
				t.Fatal(err)
			}
			if repo.settings == nil {
				t.Fatal("nothing written")
			}
			tt.check(t, *repo.settings, change)
		})
	}
}

// TestServiceAdminUpdateAddsFactors: a challenge without a category that
// uses factors gets, with the cumulative distance, the suggested ones, as
// at creation.
func TestServiceAdminUpdateAddsFactors(t *testing.T) {
	c := finished(t)
	c.Categories = c.Categories[:1]
	repo := &fakeRepository{challenge: c}
	svc := NewService(repo, fakeProfiles{}, fakeCatalogue{factors: suggested()}, fakeStandings{}, clock.NewFake(now))
	if _, err := svc.AdminUpdate(t.Context(), adminActor(), "c1", SettingsChanges{Categories: &CategoryChoice{Distance: true}}); err != nil {
		t.Fatal(err)
	}
	if got := repo.settings.Categories[1].Params.Factors; !reflect.DeepEqual(got, suggested()) {
		t.Fatalf("factors %v, want the suggested ones %v", got, suggested())
	}
}

// TestServiceAdminUpdateRefusals: the rules of the creation, the admin
// permission and a missing challenge refuse the change, nothing written.
func TestServiceAdminUpdateRefusals(t *testing.T) {
	weeks := func(n int) *int { return &n }
	text := func(s string) *string { return &s }
	tests := []struct {
		name       string
		actor      auth.Actor
		missing    bool
		changes    SettingsChanges
		wantCode   apperr.Code
		wantStatus int
		wantFields string
	}{
		{name: "not an admin", actor: auth.Actor{ID: actor}, changes: SettingsChanges{Weeks: weeks(6)}, wantCode: apperr.CodeNotFound, wantStatus: http.StatusNotFound},
		{name: "missing challenge", actor: adminActor(), missing: true, changes: SettingsChanges{Weeks: weeks(6)}, wantCode: apperr.CodeChallengeNotFound, wantStatus: http.StatusNotFound},
		{name: "one week", actor: adminActor(), changes: SettingsChanges{Weeks: weeks(1)}, wantCode: apperr.CodeValidationFailed, wantStatus: http.StatusBadRequest, wantFields: "weeks"},
		{name: "53 weeks", actor: adminActor(), changes: SettingsChanges{Weeks: weeks(53)}, wantCode: apperr.CodeValidationFailed, wantStatus: http.StatusBadRequest, wantFields: "weeks"},
		{name: "more than a year ahead", actor: adminActor(), changes: SettingsChanges{StartDate: text("2027-03-05")}, wantCode: apperr.CodeValidationFailed, wantStatus: http.StatusBadRequest, wantFields: "start_date"},
		{name: "unknown time zone", actor: adminActor(), changes: SettingsChanges{Timezone: text("Mars/Olympus")}, wantCode: apperr.CodeValidationFailed, wantStatus: http.StatusBadRequest, wantFields: "timezone"},
		{name: "reference with an optional distance", actor: adminActor(), changes: SettingsChanges{ReferenceSport: text("other")}, wantCode: apperr.CodeValidationFailed, wantStatus: http.StatusBadRequest, wantFields: "reference_sport"},
		// SC-API-212: the categories kept must still be allowed without distance.
		{
			name: "run to crossfit with the cumulative distance", actor: adminActor(),
			changes:  SettingsChanges{ReferenceSport: text("crossfit"), Categories: &CategoryChoice{Distance: true}},
			wantCode: apperr.CodeValidationFailed, wantStatus: http.StatusBadRequest, wantFields: "categories.cumulative_distance",
		},
		{
			name: "run to crossfit keeping the categories", actor: adminActor(),
			changes:  SettingsChanges{ReferenceSport: text("crossfit")},
			wantCode: apperr.CodeValidationFailed, wantStatus: http.StatusBadRequest, wantFields: "categories.cumulative_distance,categories.elimination.exercise,categories.elimination.step_meters,categories.elimination.step_repetitions",
		},
		{
			name: "crossfit with an exercise of another sport", actor: adminActor(),
			changes: SettingsChanges{ReferenceSport: text("crossfit"), Categories: &CategoryChoice{
				Elimination: &EliminationDraft{Exercise: "squat", StepRepetitions: 10},
			}},
			wantCode: apperr.CodeValidationFailed, wantStatus: http.StatusBadRequest, wantFields: "categories.elimination.exercise",
		},
		{name: "factor of the reference", actor: adminActor(), changes: SettingsChanges{DistanceFactors: map[string]float64{"run": 1}}, wantCode: apperr.CodeValidationFailed, wantStatus: http.StatusBadRequest, wantFields: "distance_factors.run"},
		{name: "factor of the new reference", actor: adminActor(), changes: SettingsChanges{ReferenceSport: text("ride"), DistanceFactors: map[string]float64{"ride": 0.5}}, wantCode: apperr.CodeValidationFailed, wantStatus: http.StatusBadRequest, wantFields: "distance_factors.ride"},
		{name: "factor over 2", actor: adminActor(), changes: SettingsChanges{DistanceFactors: map[string]float64{"ride": 2.5}}, wantCode: apperr.CodeValidationFailed, wantStatus: http.StatusBadRequest, wantFields: "distance_factors.ride"},
		{
			name: "factors without a category that uses them", actor: adminActor(),
			changes:  SettingsChanges{Categories: &CategoryChoice{Elevation: true}, DistanceFactors: map[string]float64{"ride": 0.5}},
			wantCode: apperr.CodeValidationFailed, wantStatus: http.StatusBadRequest, wantFields: "distance_factors",
		},
		{
			name: "step of the elimination out of bounds", actor: adminActor(),
			changes:  SettingsChanges{Categories: &CategoryChoice{Elimination: &EliminationDraft{StepMeters: 100_001}}},
			wantCode: apperr.CodeValidationFailed, wantStatus: http.StatusBadRequest, wantFields: "categories.elimination.step_meters",
		},
		{
			name: "points without criteria", actor: adminActor(),
			changes:  SettingsChanges{Categories: &CategoryChoice{Points: &PointsDraft{Podium: 5}}},
			wantCode: apperr.CodeValidationFailed, wantStatus: http.StatusBadRequest, wantFields: "categories.points.criteria",
		},
		{name: "title too short", actor: adminActor(), changes: SettingsChanges{Title: text("ab")}, wantCode: apperr.CodeValidationFailed, wantStatus: http.StatusBadRequest, wantFields: "title"},
		{name: "unknown visibility", actor: adminActor(), changes: SettingsChanges{Visibility: text("unlisted")}, wantCode: apperr.CodeValidationFailed, wantStatus: http.StatusBadRequest, wantFields: "visibility"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{challenge: finished(t)}
			if tt.missing {
				repo.challenge = nil
			}
			svc := NewService(repo, fakeProfiles{}, fakeCatalogue{factors: suggested()}, fakeStandings{}, clock.NewFake(now))
			_, err := svc.AdminUpdate(t.Context(), tt.actor, "c1", tt.changes)
			var appErr *apperr.Error
			if !errors.As(err, &appErr) || appErr.Code != tt.wantCode || appErr.Status != tt.wantStatus {
				t.Fatalf("AdminUpdate() = %v, want %d %s", err, tt.wantStatus, tt.wantCode)
			}
			if got := appErr.Params[apperr.FieldsParam]; got != tt.wantFields {
				t.Fatalf("fields %q, want %q", got, tt.wantFields)
			}
			if repo.settings != nil {
				t.Fatalf("written %+v, want nothing", repo.settings)
			}
		})
	}
}

// TestServiceAdminUpdateWithoutCreator: a challenge always keeps a creator
// (README §2.12): a change that brings a finished challenge without any
// creator back ongoing, or upcoming, is refused, nothing written; the same
// challenge changed but left finished goes through, as one with a creator
// made ongoing again.
func TestServiceAdminUpdateWithoutCreator(t *testing.T) {
	weeks := func(n int) *int { return &n }
	text := func(s string) *string { return &s }
	tests := []struct {
		name        string
		creatorless bool
		changes     SettingsChanges
		want        apperr.Code
	}{
		{name: "made longer, ongoing again", creatorless: true, changes: SettingsChanges{Weeks: weeks(6)}, want: apperr.CodeNoCreator},
		{name: "moved ahead, upcoming", creatorless: true, changes: SettingsChanges{StartDate: text("2026-04-06")}, want: apperr.CodeNoCreator},
		{name: "made shorter, still finished", creatorless: true, changes: SettingsChanges{Weeks: weeks(3)}},
		{name: "title only, still finished", creatorless: true, changes: SettingsChanges{Title: text("Winter run again")}},
		{name: "with a creator, ongoing again", changes: SettingsChanges{Weeks: weeks(6)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{challenge: finished(t), creatorless: tt.creatorless}
			svc := NewService(repo, fakeProfiles{}, fakeCatalogue{factors: suggested()}, fakeStandings{}, clock.NewFake(now))
			_, err := svc.AdminUpdate(t.Context(), adminActor(), "c1", tt.changes)
			if tt.want != "" {
				var appErr *apperr.Error
				if !errors.As(err, &appErr) || appErr.Code != tt.want || appErr.Status != http.StatusUnprocessableEntity || repo.settings != nil {
					t.Fatalf("AdminUpdate() = %v, written %v, want 422 %s and nothing written", err, repo.settings != nil, tt.want)
				}
				return
			}
			if err != nil || repo.settings == nil {
				t.Fatalf("AdminUpdate() = %v, written %v, want the change written", err, repo.settings != nil)
			}
		})
	}
}

// TestServiceAdminUpdateWithoutEffect: the categories as the database gives
// them (decoded from JSON), the points among them, show no false change
// when nothing changes: the journal values stay empty.
func TestServiceAdminUpdateWithoutEffect(t *testing.T) {
	c := finished(t)
	for i, raw := range []string{
		`{}`,
		`{"factors": {"run": 1, "walk": 1, "ride": 0.5, "other": 1}}`,
		`{"sport": "run", "step_meters": 2000}`,
	} {
		var p Params
		if err := p.Scan(raw); err != nil {
			t.Fatal(err)
		}
		c.Categories[i].Params = p
	}
	var points, elevation Params
	if err := points.Scan(`{"criteria": ["longest_duration", "longest_distance"], "podium": 10, "min_session_seconds": 1800,
		"factors": {"run": 1, "walk": 1, "ride": 0.5, "other": 1}}`); err != nil {
		t.Fatal(err)
	}
	if err := elevation.Scan(`{}`); err != nil {
		t.Fatal(err)
	}
	c.Categories = append(c.Categories,
		Category{ID: "k4", Type: CategoryPoints, Position: positionPoints, Params: points},
		Category{ID: "k5", Type: CategoryElevation, Position: positionElevation, Params: elevation})
	repo := &fakeRepository{challenge: c}
	svc := NewService(repo, fakeProfiles{}, fakeCatalogue{factors: suggested()}, fakeStandings{}, clock.NewFake(now))
	four := 4
	change, err := svc.AdminUpdate(t.Context(), adminActor(), "c1", SettingsChanges{Weeks: &four})
	if err != nil {
		t.Fatal(err)
	}
	if len(change.Old) != 0 || len(change.New) != 0 {
		t.Fatalf("change %v -> %v, want none", change.Old, change.New)
	}
}
