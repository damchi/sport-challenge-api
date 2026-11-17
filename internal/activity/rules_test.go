package activity

import (
	"errors"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
)

// now is the instant of the validation (from platform/clock in the service).
var now = time.Date(2026, 3, 4, 15, 0, 0, 0, time.UTC)

func ptr[T any](v T) *T { return &v }

func valid() Draft {
	return Draft{
		SportCode:       "run",
		StartedAt:       now.Add(-time.Hour),
		DurationSeconds: 3600,
		DistanceMeters:  ptr(10_000),
	}
}

func with(change func(*Draft)) Draft {
	d := valid()
	change(&d)
	return d
}

// other is a draft of the sport typed by hand, without distance.
func other(label string) Draft {
	return with(func(d *Draft) { d.SportCode = "other"; d.SportLabel = &label; d.DistanceMeters = nil })
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name       string
		draft      Draft
		mode       string // distance mode of the sport in the catalogue
		wantCode   apperr.Code
		wantFields string // validation_failed only
		wantLabel  string // normalized label of a valid "other" draft
	}{
		{name: "valid", draft: valid(), mode: "required"},

		// Duration: required, strictly positive, 24 h at most.
		{name: "duration of 1 second", draft: with(func(d *Draft) { d.DurationSeconds = 1 }), mode: "required"},
		{name: "duration of 24 h", draft: with(func(d *Draft) { d.DurationSeconds = 86_400 }), mode: "required"},
		{name: "duration missing", draft: with(func(d *Draft) { d.DurationSeconds = 0 }), mode: "required", wantCode: apperr.CodeValidationFailed, wantFields: "duration_seconds"},
		{name: "negative duration", draft: with(func(d *Draft) { d.DurationSeconds = -1 }), mode: "required", wantCode: apperr.CodeValidationFailed, wantFields: "duration_seconds"},
		{name: "duration of 24 h and 1 second", draft: with(func(d *Draft) { d.DurationSeconds = 86_401 }), mode: "required", wantCode: apperr.CodeValidationFailed, wantFields: "duration_seconds"},

		// Distance: strictly positive, 1000 km at most.
		{name: "distance of 1 meter", draft: with(func(d *Draft) { d.DistanceMeters = ptr(1) }), mode: "required"},
		{name: "distance of 1000 km", draft: with(func(d *Draft) { d.DistanceMeters = ptr(1_000_000) }), mode: "required"},
		{name: "distance of zero", draft: with(func(d *Draft) { d.DistanceMeters = ptr(0) }), mode: "required", wantCode: apperr.CodeValidationFailed, wantFields: "distance_meters"},
		{name: "negative distance", draft: with(func(d *Draft) { d.DistanceMeters = ptr(-1) }), mode: "required", wantCode: apperr.CodeValidationFailed, wantFields: "distance_meters"},
		{name: "distance of 1000 km and 1 meter", draft: with(func(d *Draft) { d.DistanceMeters = ptr(1_000_001) }), mode: "required", wantCode: apperr.CodeValidationFailed, wantFields: "distance_meters"},

		// Distance by sport: required, not allowed, optional for "other" (D9).
		{name: "distance missing for a distance sport", draft: with(func(d *Draft) { d.DistanceMeters = nil }), mode: "required", wantCode: apperr.CodeDistanceRequired},
		{name: "sport without distance", draft: with(func(d *Draft) { d.SportCode = "yoga"; d.DistanceMeters = nil }), mode: "none"},
		{name: "distance for a sport without distance", draft: with(func(d *Draft) { d.SportCode = "yoga" }), mode: "none", wantCode: apperr.CodeDistanceNotAllowed},
		{name: "other without distance", draft: other("Padel"), mode: "optional", wantLabel: "Padel"},
		{name: "other with distance", draft: with(func(d *Draft) { d.SportCode = "other"; d.SportLabel = ptr("Kayak") }), mode: "optional", wantLabel: "Kayak"},
		{name: "other with a distance of zero", draft: with(func(d *Draft) { d.SportCode = "other"; d.SportLabel = ptr("Kayak"); d.DistanceMeters = ptr(0) }), mode: "optional", wantCode: apperr.CodeValidationFailed, wantFields: "distance_meters"},
		{name: "other with a distance of 1000 km and 1 meter", draft: with(func(d *Draft) { d.SportCode = "other"; d.SportLabel = ptr("Kayak"); d.DistanceMeters = ptr(1_000_001) }), mode: "optional", wantCode: apperr.CodeValidationFailed, wantFields: "distance_meters"},
		{name: "invalid distance comes before the sport rule", draft: with(func(d *Draft) { d.SportCode = "yoga"; d.DistanceMeters = ptr(0) }), mode: "none", wantCode: apperr.CodeValidationFailed, wantFields: "distance_meters"},

		// Label of "other": required, trimmed, 2 to 40 characters, no control character.
		{name: "label of 2 characters", draft: other("Ét"), mode: "optional", wantLabel: "Ét"},
		{name: "label of 40 characters", draft: other(strings.Repeat("é", 40)), mode: "optional", wantLabel: strings.Repeat("é", 40)},
		{name: "label with extra spaces", draft: other("  Ski   de  fond "), mode: "optional", wantLabel: "Ski de fond"},
		{name: "label of 40 characters once cleaned", draft: other("  " + strings.Repeat("a", 20) + "    " + strings.Repeat("b", 19) + " "), mode: "optional", wantLabel: strings.Repeat("a", 20) + " " + strings.Repeat("b", 19)},
		{name: "label missing", draft: with(func(d *Draft) { d.SportCode = "other"; d.DistanceMeters = nil }), mode: "optional", wantCode: apperr.CodeValidationFailed, wantFields: "sport_label"},
		{name: "empty label", draft: other(""), mode: "optional", wantCode: apperr.CodeValidationFailed, wantFields: "sport_label"},
		{name: "label of 1 character", draft: other("é"), mode: "optional", wantCode: apperr.CodeValidationFailed, wantFields: "sport_label"},
		{name: "label of 41 characters", draft: other(strings.Repeat("a", 41)), mode: "optional", wantCode: apperr.CodeValidationFailed, wantFields: "sport_label"},
		{name: "label of spaces only", draft: other("     "), mode: "optional", wantCode: apperr.CodeValidationFailed, wantFields: "sport_label"},
		{name: "label padded to 2 characters", draft: other("  a  "), mode: "optional", wantCode: apperr.CodeValidationFailed, wantFields: "sport_label"},
		{name: "NUL in the label", draft: other("Pa\x00del"), mode: "optional", wantCode: apperr.CodeValidationFailed, wantFields: "sport_label"},
		{name: "newline in the label", draft: other("Ski\nde fond"), mode: "optional", wantCode: apperr.CodeValidationFailed, wantFields: "sport_label"},
		{name: "tab in the label", draft: other("Ski\tde fond"), mode: "optional", wantCode: apperr.CodeValidationFailed, wantFields: "sport_label"},
		{name: "right-to-left override in the label", draft: other("Pa\u202edel"), mode: "optional", wantCode: apperr.CodeValidationFailed, wantFields: "sport_label"},
		{name: "zero width space in the label", draft: other("Pa\u200bdel"), mode: "optional", wantCode: apperr.CodeValidationFailed, wantFields: "sport_label"},
		{name: "emoji joined in the label", draft: other("Yoga \U0001F9D8\u200d\u2640\ufe0f"), mode: "optional", wantLabel: "Yoga \U0001F9D8\u200d\u2640\ufe0f"},
		{name: "label for a sport of the catalogue", draft: with(func(d *Draft) { d.SportLabel = ptr("Trail") }), mode: "required", wantCode: apperr.CodeValidationFailed, wantFields: "sport_label"},

		// Start: never in the future, 5 minutes of tolerance.
		{name: "starts now", draft: with(func(d *Draft) { d.StartedAt = now }), mode: "required"},
		{name: "starts in 5 minutes", draft: with(func(d *Draft) { d.StartedAt = now.Add(5 * time.Minute) }), mode: "required"},
		{name: "starts in 5 minutes and 1 second", draft: with(func(d *Draft) { d.StartedAt = now.Add(5*time.Minute + time.Second) }), mode: "required", wantCode: apperr.CodeValidationFailed, wantFields: "started_at"},
		{name: "start missing", draft: with(func(d *Draft) { d.StartedAt = time.Time{} }), mode: "required", wantCode: apperr.CodeValidationFailed, wantFields: "started_at"},

		{
			name:     "every invalid field, sorted",
			draft:    Draft{SportCode: "other", StartedAt: now.Add(time.Hour), DistanceMeters: ptr(0)},
			mode:     "optional",
			wantCode: apperr.CodeValidationFailed, wantFields: "distance_meters,duration_seconds,sport_label,started_at",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Validate(tt.draft, tt.mode, now)
			if tt.wantCode == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				gotLabel := ""
				if got.SportLabel != nil {
					gotLabel = *got.SportLabel
				}
				if gotLabel != tt.wantLabel {
					t.Fatalf("label = %q, want %q", gotLabel, tt.wantLabel)
				}
				return
			}
			var appErr *apperr.Error
			if !errors.As(err, &appErr) || appErr.Code != tt.wantCode {
				t.Fatalf("Validate() error = %v, want %s", err, tt.wantCode)
			}
			if appErr.Params[apperr.FieldsParam] != tt.wantFields {
				t.Fatalf("fields = %q, want %q", appErr.Params[apperr.FieldsParam], tt.wantFields)
			}
		})
	}
}

func TestValidateDistanceRuleStatus(t *testing.T) {
	// The distance rules refuse a valid input: 422, not 400.
	for _, tt := range []struct {
		draft Draft
		mode  string
	}{
		{with(func(d *Draft) { d.DistanceMeters = nil }), "required"},
		{valid(), "none"},
	} {
		_, err := Validate(tt.draft, tt.mode, now)
		var appErr *apperr.Error
		if !errors.As(err, &appErr) || appErr.Status != http.StatusUnprocessableEntity {
			t.Fatalf("Validate(mode %s) error = %v, want 422", tt.mode, err)
		}
	}
}

func TestValidateUnknownDistanceMode(t *testing.T) {
	// The mode comes from the catalogue: an unknown one is a programming error.
	_, err := Validate(valid(), "sometimes", now)
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != apperr.CodeInternal {
		t.Fatalf("Validate() error = %v, want internal_error", err)
	}
}

func TestSpeed(t *testing.T) {
	tests := []struct {
		name     string
		meters   int
		seconds  int
		wantMPS  float64
		wantPace float64 // seconds per kilometer
	}{
		{name: "10 km in 50 min", meters: 10_000, seconds: 3000, wantMPS: 10_000.0 / 3000, wantPace: 300},
		{name: "1 m in 24 h", meters: 1, seconds: 86_400, wantMPS: 1.0 / 86_400, wantPace: 86_400_000},
		// No speed cap in V1: an implausible speed is still computed.
		{name: "1000 km in 1 s", meters: 1_000_000, seconds: 1, wantMPS: 1_000_000, wantPace: 0.001},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Speed(tt.meters, tt.seconds)
			if math.Abs(got.MetersPerSecond-tt.wantMPS) > 1e-9*tt.wantMPS {
				t.Fatalf("MetersPerSecond = %v, want %v", got.MetersPerSecond, tt.wantMPS)
			}
			if math.Abs(got.SecondsPerKilometer-tt.wantPace) > 1e-9*tt.wantPace {
				t.Fatalf("SecondsPerKilometer = %v, want %v", got.SecondsPerKilometer, tt.wantPace)
			}
		})
	}
}

func TestSpeedWithoutDistanceOrDuration(t *testing.T) {
	// No distance (a sport without km, "other" without distance) or no
	// duration: no speed, never Inf nor NaN.
	for _, tt := range []struct{ meters, seconds int }{{0, 3600}, {1000, 0}, {-1, 3600}, {1000, -1}} {
		if got := Speed(tt.meters, tt.seconds); got != (Velocity{}) {
			t.Fatalf("Speed(%d, %d) = %+v, want zero", tt.meters, tt.seconds, got)
		}
	}
}
