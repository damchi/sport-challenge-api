//go:build integration

package sport

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"testing"

	"github.com/damchi/sport-challenge-api/internal/platform/db"
	"github.com/damchi/sport-challenge-api/internal/platform/db/testdb"
)

// TestListReturnsTheReferenceData reads the catalogue seeded by the
// migrations (README §2.4, decision D9).
func TestListReturnsTheReferenceData(t *testing.T) {
	gdb, err := db.NewGorm(testdb.New(t), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	sports, err := NewRepository(gdb).List(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	type row struct {
		mode      string
		factor    float64 // 0 when null
		elevation bool    // SC-API-149
	}
	want := map[string]row{
		"climbing": {"none", 0, false}, "hike": {"required", 1, true}, "other": {"optional", 1, true},
		"ride": {"required", 0.3, true}, "roller": {"required", 0.5, true}, "rowing": {"required", 0.5, false},
		"run": {"required", 1, true}, "strength": {"none", 0, false}, "swim": {"required", 1, false},
		"walk": {"required", 1, true}, "xc_ski": {"required", 0.5, true}, "yoga": {"none", 0, false},
		// SC-API-174.
		"crossfit": {"none", 0, false}, "hyrox": {"required", 1, false}, "paragliding": {"required", 0.3, false},
	}
	if len(sports) != len(want) {
		t.Fatalf("%d sports, want %d", len(sports), len(want))
	}
	codes := make([]string, 0, len(sports))
	for _, s := range sports {
		codes = append(codes, s.Code)
		w, ok := want[s.Code]
		if !ok {
			t.Fatalf("unexpected sport %q", s.Code)
		}
		var got float64
		if s.SuggestedFactor != nil {
			got = *s.SuggestedFactor
		}
		if s.DistanceMode != w.mode || got != w.factor || (w.mode == "none") != (s.SuggestedFactor == nil) || s.ElevationAllowed != w.elevation {
			t.Errorf("%s = %s %v %v, want %s %v %v", s.Code, s.DistanceMode, s.SuggestedFactor, s.ElevationAllowed, w.mode, w.factor, w.elevation)
		}
	}
	if !slices.IsSorted(codes) {
		t.Fatalf("codes %v, want them sorted", codes)
	}
}

// TestGetSportsServesTheCatalogue: GET /sports on the catalogue of the
// migrations gives the sports added by SC-API-174, as the contract says.
func TestGetSportsServesTheCatalogue(t *testing.T) {
	gdb, err := db.NewGorm(testdb.New(t), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	rec := getSports(t, NewRepository(gdb))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var body struct {
		Sports []map[string]any `json:"sports"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]any{
		"crossfit":    {"distance_mode": "none", "suggested_factor": nil, "elevation_allowed": false},
		"hyrox":       {"distance_mode": "required", "suggested_factor": 1.0, "elevation_allowed": false},
		"paragliding": {"distance_mode": "required", "suggested_factor": 0.3, "elevation_allowed": false},
	}
	for _, s := range body.Sports {
		code, _ := s["code"].(string)
		w, ok := want[code]
		if !ok {
			continue
		}
		for k, v := range w {
			if got, ok := s[k]; !ok || got != v {
				t.Errorf("%s.%s = %v, want %v", code, k, got, v)
			}
		}
		delete(want, code)
	}
	if len(want) > 0 {
		t.Fatalf("sports %v missing from %v", want, body.Sports)
	}

	// SC-API-211: the CrossFit exercises in display order, none elsewhere.
	crossfit := []any{"burpee", "wall_ball", "air_squat", "push_up", "pull_up", "box_jump", "double_under", "kettlebell_swing", "thruster", "sit_up"}
	for _, s := range body.Sports {
		wantExercises := []any{}
		if s["code"] == "crossfit" {
			wantExercises = crossfit
		}
		if got, ok := s["exercises"].([]any); !ok || !slices.Equal(got, wantExercises) {
			t.Errorf("%v exercises = %#v, want %v", s["code"], s["exercises"], wantExercises)
		}
	}
}

// TestExercisesOfTheCatalogue reads the exercises seeded by the migrations
// (SC-API-210, README §2.5): the ten CrossFit ones in display order, none
// for a sport without exercises, and membership limited to their sport.
func TestExercisesOfTheCatalogue(t *testing.T) {
	gdb, err := db.NewGorm(testdb.New(t), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(NewRepository(gdb))

	exercises, err := svc.Exercises(t.Context(), "crossfit")
	if err != nil {
		t.Fatal(err)
	}
	codes := make([]string, 0, len(exercises))
	for _, e := range exercises {
		if e.SportCode != "crossfit" {
			t.Errorf("%s belongs to %s, want crossfit", e.Code, e.SportCode)
		}
		codes = append(codes, e.Code)
	}
	want := []string{"burpee", "wall_ball", "air_squat", "push_up", "pull_up", "box_jump", "double_under", "kettlebell_swing", "thruster", "sit_up"}
	if !slices.Equal(codes, want) {
		t.Fatalf("crossfit exercises = %v, want %v", codes, want)
	}

	yoga, err := svc.Exercises(t.Context(), "yoga")
	if err != nil {
		t.Fatal(err)
	}
	if len(yoga) != 0 {
		t.Fatalf("yoga exercises = %v, want none", yoga)
	}

	tests := []struct {
		sport, exercise string
		want            bool
	}{
		{"crossfit", "wall_ball", true},
		{"crossfit", "deadlift", false},
		{"yoga", "burpee", false},
		{"unknown", "burpee", false},
	}
	for _, tt := range tests {
		got, err := svc.HasExercise(t.Context(), tt.sport, tt.exercise)
		if err != nil {
			t.Fatal(err)
		}
		if got != tt.want {
			t.Errorf("HasExercise(%s, %s) = %v, want %v", tt.sport, tt.exercise, got, tt.want)
		}
	}
}
