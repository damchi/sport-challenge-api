package challenge

import (
	"errors"
	"maps"
	"math"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
	// The time zone database, embedded: the tests do not depend on the
	// machine's (a minimal image or runner may have none).
	_ "time/tzdata"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/scoring"
)

// distanceSports is the catalogue part the rules need: the sports that have
// a distance (required or optional).
var distanceSports = map[string]bool{"run": true, "walk": true, "ride": true, "other": true}

// unknownSport marks a reference sport the catalogue does not have, whose
// distance mode is empty.
const unknownSport = "unknown"

func sportCode(code string) *string { return &code }

// now is 10:00 on Wednesday 2026-03-04 in Toronto (15:00 UTC).
var now = time.Date(2026, 3, 4, 15, 0, 0, 0, time.UTC)

// valid is a draft with every category.
func valid() Draft {
	return Draft{
		Title:       "Spring run",
		Visibility:  "public",
		Timezone:    "America/Toronto",
		StartDate:   "2026-03-04",
		Weeks:       6,
		Distance:    true,
		Elimination: &EliminationDraft{StepMeters: 2000},
	}
}

func minutes(n int) *int { return &n }

// points is a points category with criteria, a top 5 and the default
// minimum session.
func points(criteria ...string) *PointsDraft {
	return &PointsDraft{Criteria: criteria, Podium: 5}
}

func with(change func(*Draft)) Draft {
	d := valid()
	change(&d)
	return d
}

func TestValidate(t *testing.T) {
	desc := func(n int) *string { s := strings.Repeat("é", n); return &s }
	tests := []struct {
		name  string
		draft Draft
		// referenceMode is the distance mode of the reference sport in the
		// catalogue: empty for required.
		referenceMode string
		wantFields    string // empty: valid
	}{
		{name: "valid", draft: valid()},
		{name: "private, members can invite", draft: with(func(d *Draft) { d.Visibility = "private"; d.MembersCanInvite = true })},

		// Title: 3 to 80 characters, counted as characters, not bytes.
		{name: "title of 3 characters", draft: with(func(d *Draft) { d.Title = "Été" })},
		{name: "title of 80 characters", draft: with(func(d *Draft) { d.Title = strings.Repeat("é", 80) })},
		{name: "title of 2 characters", draft: with(func(d *Draft) { d.Title = "Ét" }), wantFields: "title"},
		{name: "title of 81 characters", draft: with(func(d *Draft) { d.Title = strings.Repeat("a", 81) }), wantFields: "title"},
		{name: "title of spaces only", draft: with(func(d *Draft) { d.Title = "     " }), wantFields: "title"},
		{name: "title padded to 3 characters", draft: with(func(d *Draft) { d.Title = "  ab  " }), wantFields: "title"},

		// Control characters: refused (PostgreSQL refuses NUL in text).
		{name: "NUL in the title", draft: with(func(d *Draft) { d.Title = "ab\x00c" }), wantFields: "title"},
		{name: "newline in the title", draft: with(func(d *Draft) { d.Title = "Spring\nrun" }), wantFields: "title"},
		{name: "NUL in the description", draft: with(func(d *Draft) { s := "a\x00b"; d.Description = &s }), wantFields: "description"},
		{name: "escape in the description", draft: with(func(d *Draft) { s := "a\x1bb"; d.Description = &s }), wantFields: "description"},
		{name: "lines and tabs in the description", draft: with(func(d *Draft) { s := "Line 1\r\n\tLine 2"; d.Description = &s })},

		// Format characters: refused, they hide or turn the text around; the
		// zero width joiner of emojis stays.
		{name: "right-to-left override in the title", draft: with(func(d *Draft) { d.Title = "Spring \u202enur" }), wantFields: "title"},
		{name: "zero width space in the title", draft: with(func(d *Draft) { d.Title = "Spring\u200brun" }), wantFields: "title"},
		{name: "title of joiners only", draft: with(func(d *Draft) { d.Title = "\u200d\u200d\u200d\u200d" }), wantFields: "title"},
		{name: "title of 2 letters and marks", draft: with(func(d *Draft) { d.Title = "ab\u200e\u200f" }), wantFields: "title"},
		{name: "line separator in the title", draft: with(func(d *Draft) { d.Title = "Spring\u2028run" }), wantFields: "title"},
		{name: "separators in the description", draft: with(func(d *Draft) { s := "Line 1\u2028Line 2\u2029"; d.Description = &s })},
		{name: "right-to-left override in the description", draft: with(func(d *Draft) { s := "a\u202eb"; d.Description = &s }), wantFields: "description"},
		{name: "zero width space in the description", draft: with(func(d *Draft) { s := "a\u200bb"; d.Description = &s }), wantFields: "description"},
		{name: "emojis and flags in the title and the description", draft: with(func(d *Draft) {
			d.Title = "Team \U0001F468\u200d\U0001F469\u200d\U0001F467 \U0001F3F4\U000E0067\U000E0062\U000E0073\U000E0063\U000E0074\U000E007F"
			s := "Go \U0001F3C3\u200d\u2640\ufe0f"
			d.Description = &s
		})},

		// Description: optional, 2000 characters at most.
		{name: "description of 2000 characters", draft: with(func(d *Draft) { d.Description = desc(2000) })},
		{name: "description of 2001 characters", draft: with(func(d *Draft) { d.Description = desc(2001) }), wantFields: "description"},

		{name: "other visibility", draft: with(func(d *Draft) { d.Visibility = "friends" }), wantFields: "visibility"},

		// Time zone: an IANA name; not the empty name nor Local, which
		// LoadLocation accepts (UTC and the server's zone).
		{name: "UTC", draft: with(func(d *Draft) { d.Timezone = "UTC" })},
		{name: "unknown time zone", draft: with(func(d *Draft) { d.Timezone = "Mars/Olympus" }), wantFields: "timezone"},
		{name: "empty time zone", draft: with(func(d *Draft) { d.Timezone = "" }), wantFields: "timezone"},
		{name: "Local time zone", draft: with(func(d *Draft) { d.Timezone = "Local" }), wantFields: "timezone"},

		// Start: a local day, today or later in the challenge's time zone.
		{name: "starts today", draft: valid()},
		{name: "starts tomorrow", draft: with(func(d *Draft) { d.StartDate = "2026-03-05" })},
		{name: "starts the same day next year", draft: with(func(d *Draft) { d.StartDate = "2027-03-04" })},
		{name: "starts the day after next year", draft: with(func(d *Draft) { d.StartDate = "2027-03-05" }), wantFields: "start_date"},
		{name: "starts in year 9999", draft: with(func(d *Draft) { d.StartDate = "9999-12-31" }), wantFields: "start_date"},
		{name: "started yesterday", draft: with(func(d *Draft) { d.StartDate = "2026-03-03" }), wantFields: "start_date"},
		{name: "not a date", draft: with(func(d *Draft) { d.StartDate = "04/03/2026" }), wantFields: "start_date"},
		{name: "impossible date", draft: with(func(d *Draft) { d.StartDate = "2026-02-30" }), wantFields: "start_date"},
		{name: "date with a time", draft: with(func(d *Draft) { d.StartDate = "2026-03-04T00:00:00Z" }), wantFields: "start_date"},

		// Duration: whole weeks, 2 to 52 (D3).
		{name: "2 weeks", draft: with(func(d *Draft) { d.Weeks = 2 })},
		{name: "52 weeks", draft: with(func(d *Draft) { d.Weeks = 52 })},
		{name: "1 week", draft: with(func(d *Draft) { d.Weeks = 1 }), wantFields: "weeks"},
		{name: "53 weeks", draft: with(func(d *Draft) { d.Weeks = 53 }), wantFields: "weeks"},
		{name: "0 weeks", draft: with(func(d *Draft) { d.Weeks = 0 }), wantFields: "weeks"},

		// Categories: the cumulative time alone, or with any of the others.
		{name: "cumulative time only", draft: with(func(d *Draft) { d.Distance = false; d.Elimination = nil })},
		{name: "elimination without distance", draft: with(func(d *Draft) { d.Distance = false })},
		{name: "distance without elimination", draft: with(func(d *Draft) { d.Elimination = nil })},

		// Elimination step X: meters, > 0 and 100 km at most.
		{name: "step of 1 m", draft: with(func(d *Draft) { d.Elimination.StepMeters = 1 })},
		{name: "step of 100 km", draft: with(func(d *Draft) { d.Elimination.StepMeters = 100_000 })},
		{name: "step of 0", draft: with(func(d *Draft) { d.Elimination.StepMeters = 0 }), wantFields: "categories.elimination.step_meters"},
		{name: "negative step", draft: with(func(d *Draft) { d.Elimination.StepMeters = -1 }), wantFields: "categories.elimination.step_meters"},
		{name: "step over 100 km", draft: with(func(d *Draft) { d.Elimination.StepMeters = 100_001 }), wantFields: "categories.elimination.step_meters"},

		// Distance factors only serve the cumulative distance: without it,
		// even none or invalid ones are refused, and the keys never echoed.
		{name: "factors without distance", draft: with(func(d *Draft) {
			d.Distance = false
			d.DistanceFactors = map[string]float64{"ride": 0.25}
		}), wantFields: "distance_factors"},
		{name: "empty factors without distance", draft: with(func(d *Draft) {
			d.Distance = false
			d.DistanceFactors = map[string]float64{}
		}), wantFields: "distance_factors"},
		{name: "invalid factors without distance: the field only", draft: with(func(d *Draft) {
			d.Distance = false
			d.DistanceFactors = map[string]float64{"ride": 3, "quidditch": 1}
		}), wantFields: "distance_factors"},

		// Points: at least one known criterion, each once; a podium of 5 or
		// 10; a minimum session of 1 to 180 minutes.
		{name: "points with every criterion", draft: with(func(d *Draft) {
			d.Points = points(scoring.CriterionLongestDuration, scoring.CriterionLongestDistance, scoring.CriterionSessionCount, scoring.CriterionWeeklyElevation)
		})},
		{name: "points with the weekly elevation alone", draft: with(func(d *Draft) { d.Points = points(scoring.CriterionWeeklyElevation) })},
		{name: "points in a top 10", draft: with(func(d *Draft) { d.Points = points(scoring.CriterionSessionCount); d.Points.Podium = 10 })},
		{name: "points with a session of 1 min", draft: with(func(d *Draft) {
			d.Points = points(scoring.CriterionSessionCount)
			d.Points.MinSessionMinutes = minutes(1)
		})},
		{name: "points with a session of 180 min", draft: with(func(d *Draft) {
			d.Points = points(scoring.CriterionSessionCount)
			d.Points.MinSessionMinutes = minutes(180)
		})},
		{name: "points without criterion", draft: with(func(d *Draft) { d.Points = points() }), wantFields: "categories.points.criteria"},
		{name: "points with an unknown criterion", draft: with(func(d *Draft) { d.Points = points("fastest") }), wantFields: "categories.points.criteria"},
		{name: "points with a criterion twice", draft: with(func(d *Draft) {
			d.Points = points(scoring.CriterionLongestDuration, scoring.CriterionLongestDuration)
		}), wantFields: "categories.points.criteria"},
		{name: "points with the weekly elevation twice", draft: with(func(d *Draft) {
			d.Points = points(scoring.CriterionWeeklyElevation, scoring.CriterionSessionCount, scoring.CriterionWeeklyElevation)
		}), wantFields: "categories.points.criteria"},
		{name: "points in a top 3", draft: with(func(d *Draft) { d.Points = points(scoring.CriterionSessionCount); d.Points.Podium = 3 }), wantFields: "categories.points.podium"},
		{name: "points with a session of 0 min", draft: with(func(d *Draft) {
			d.Points = points(scoring.CriterionSessionCount)
			d.Points.MinSessionMinutes = minutes(0)
		}), wantFields: "categories.points.min_session_minutes"},
		{name: "points with a session of 181 min", draft: with(func(d *Draft) {
			d.Points = points(scoring.CriterionSessionCount)
			d.Points.MinSessionMinutes = minutes(181)
		}), wantFields: "categories.points.min_session_minutes"},
		{name: "points with every setting invalid", draft: with(func(d *Draft) { d.Points = &PointsDraft{MinSessionMinutes: minutes(0)} }), wantFields: "categories.points.criteria,categories.points.min_session_minutes,categories.points.podium"},

		// The longest distance of the points uses the factors too.
		{name: "factors for the longest distance alone", draft: with(func(d *Draft) {
			d.Distance = false
			d.Points = points(scoring.CriterionLongestDistance)
			d.DistanceFactors = map[string]float64{"ride": 0.25}
		})},
		{name: "invalid factor for the longest distance", draft: with(func(d *Draft) {
			d.Distance = false
			d.Points = points(scoring.CriterionLongestDistance)
			d.DistanceFactors = map[string]float64{"ride": 3}
		}), wantFields: "distance_factors.ride"},
		{name: "factors for points without the longest distance", draft: with(func(d *Draft) {
			d.Distance = false
			d.Points = points(scoring.CriterionLongestDuration, scoring.CriterionSessionCount)
			d.DistanceFactors = map[string]float64{"ride": 0.25}
		}), wantFields: "distance_factors"},

		// Distance factors chosen by the creator: distance sports, 0.1 to 2,
		// two decimals at most.
		{name: "factors at the bounds", draft: with(func(d *Draft) { d.DistanceFactors = map[string]float64{"ride": 0.1, "walk": 2, "other": 0.35} })},
		{name: "factors with float noise", draft: with(func(d *Draft) {
			d.DistanceFactors = map[string]float64{"ride": 0.29, "walk": 0.57, "other": 1.15}
		})},
		{name: "run is the fixed reference", draft: with(func(d *Draft) { d.DistanceFactors = map[string]float64{"run": 1} }), wantFields: "distance_factors.run"},
		{name: "the reference is fixed, whatever the sport", draft: with(func(d *Draft) {
			d.ReferenceSport = sportCode("ride")
			d.DistanceFactors = map[string]float64{"ride": 1}
		}), wantFields: "distance_factors.ride"},
		{name: "run is free under another reference", draft: with(func(d *Draft) {
			d.ReferenceSport = sportCode("ride")
			d.DistanceFactors = map[string]float64{"run": 0.5}
		})},

		// Reference sport: a sport of the catalogue whose distance is
		// required, run when none is given.
		{name: "ride as the reference", draft: with(func(d *Draft) { d.ReferenceSport = sportCode("ride") })},
		{name: "reference without distance", draft: with(func(d *Draft) { d.ReferenceSport = sportCode("yoga") }), referenceMode: "none", wantFields: "reference_sport"},
		{name: "reference with an optional distance", draft: with(func(d *Draft) { d.ReferenceSport = sportCode("other") }), referenceMode: "optional", wantFields: "reference_sport"},
		{name: "unknown reference", draft: with(func(d *Draft) { d.ReferenceSport = sportCode("quidditch") }), referenceMode: unknownSport, wantFields: "reference_sport"},
		{name: "factor under 0.1", draft: with(func(d *Draft) { d.DistanceFactors = map[string]float64{"ride": 0.09} }), wantFields: "distance_factors.ride"},
		{name: "factor over 2", draft: with(func(d *Draft) { d.DistanceFactors = map[string]float64{"ride": 2.01} }), wantFields: "distance_factors.ride"},
		{name: "factor with 3 decimals", draft: with(func(d *Draft) { d.DistanceFactors = map[string]float64{"ride": 0.125} }), wantFields: "distance_factors.ride"},
		{name: "sport without distance", draft: with(func(d *Draft) { d.DistanceFactors = map[string]float64{"yoga": 1} }), wantFields: "distance_factors"},
		{name: "unknown sport", draft: with(func(d *Draft) { d.DistanceFactors = map[string]float64{"quidditch": 1} }), wantFields: "distance_factors"},
		{name: "NaN factor", draft: with(func(d *Draft) { d.DistanceFactors = map[string]float64{"ride": math.NaN()} }), wantFields: "distance_factors.ride"},
		{name: "infinite factor", draft: with(func(d *Draft) { d.DistanceFactors = map[string]float64{"ride": math.Inf(1)} }), wantFields: "distance_factors.ride"},
		{name: "unknown sport and invalid factor", draft: with(func(d *Draft) { d.DistanceFactors = map[string]float64{"quidditch": 1, "ride": 3} }), wantFields: "distance_factors,distance_factors.ride"},
		{name: "invalid time zone with a past date: only the time zone", draft: with(func(d *Draft) { d.Timezone = "Mars/Olympus"; d.StartDate = "2020-01-01" }), wantFields: "timezone"},

		{
			name:       "every invalid field, sorted",
			draft:      Draft{Title: "x", Visibility: "x", Timezone: "America/Toronto", StartDate: "x", Weeks: 99, Elimination: &EliminationDraft{}},
			wantFields: "categories.elimination.step_meters,start_date,title,visibility,weeks",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mode := tt.referenceMode
			switch mode {
			case "":
				mode = distanceRequired
			case unknownSport:
				mode = ""
			}
			_, err := Validate(tt.draft, now, distanceSports, mode)
			if tt.wantFields == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}
			var appErr *apperr.Error
			if !errors.As(err, &appErr) || appErr.Code != apperr.CodeValidationFailed || appErr.Status != http.StatusBadRequest {
				t.Fatalf("Validate() error = %v, want 400 validation_failed", err)
			}
			if got := appErr.Params[apperr.FieldsParam]; got != tt.wantFields {
				t.Fatalf("fields = %q, want %q", got, tt.wantFields)
			}
		})
	}
}

func TestValidateWindow(t *testing.T) {
	toronto, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		draft    Draft
		now      time.Time
		wantFrom time.Time
		wantTo   time.Time
	}{
		{
			name:     "no time change",
			draft:    with(func(d *Draft) { d.StartDate = "2026-01-05"; d.Weeks = 2 }),
			now:      time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
			wantFrom: time.Date(2026, 1, 5, 0, 0, 0, 0, toronto),
			wantTo:   time.Date(2026, 1, 19, 0, 0, 0, 0, toronto),
		},
		{
			// Clocks go forward on 2026-03-08: the window is 1 h shorter
			// than 14 × 24 h but still ends at local midnight.
			name:     "spring forward in March",
			draft:    with(func(d *Draft) { d.StartDate = "2026-03-02"; d.Weeks = 2 }),
			now:      time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
			wantFrom: time.Date(2026, 3, 2, 0, 0, 0, 0, toronto),
			wantTo:   time.Date(2026, 3, 16, 0, 0, 0, 0, toronto),
		},
		{
			// Clocks go back on 2026-11-01: 1 h longer than 14 × 24 h.
			name:     "fall back in November",
			draft:    with(func(d *Draft) { d.StartDate = "2026-10-26"; d.Weeks = 2 }),
			now:      time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
			wantFrom: time.Date(2026, 10, 26, 0, 0, 0, 0, toronto),
			wantTo:   time.Date(2026, 11, 9, 0, 0, 0, 0, toronto),
		},
		{
			// 52 weeks cross both time changes and still end at local midnight.
			name:     "52 weeks across two time changes",
			draft:    with(func(d *Draft) { d.StartDate = "2026-01-05"; d.Weeks = 52 }),
			now:      time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
			wantFrom: time.Date(2026, 1, 5, 0, 0, 0, 0, toronto),
			wantTo:   time.Date(2027, 1, 4, 0, 0, 0, 0, toronto),
		},
		{
			// Starts on the day clocks go forward (at 02:00): midnight exists.
			name:     "starts on the spring time change",
			draft:    with(func(d *Draft) { d.StartDate = "2026-03-08"; d.Weeks = 2 }),
			now:      time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
			wantFrom: time.Date(2026, 3, 8, 0, 0, 0, 0, toronto),
			wantTo:   time.Date(2026, 3, 22, 0, 0, 0, 0, toronto),
		},
		{
			name:     "other time zone",
			draft:    with(func(d *Draft) { d.Timezone = "Europe/Paris"; d.StartDate = "2026-03-23"; d.Weeks = 3 }),
			now:      time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC),
			wantFrom: time.Date(2026, 3, 23, 0, 0, 0, 0, paris),
			wantTo:   time.Date(2026, 4, 13, 0, 0, 0, 0, paris),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, err := Validate(tt.draft, tt.now, distanceSports, distanceRequired)
			if err != nil {
				t.Fatal(err)
			}
			if !w.StartsAt.Equal(tt.wantFrom) || !w.EndsAt.Equal(tt.wantTo) {
				t.Fatalf("window [%s, %s), want [%s, %s)", w.StartsAt, w.EndsAt, tt.wantFrom, tt.wantTo)
			}
			if w.StartsAt.Location() != time.UTC || w.EndsAt.Location() != time.UTC {
				t.Fatalf("window in %s and %s, want UTC (timestamptz)", w.StartsAt.Location(), w.EndsAt.Location())
			}
		})
	}
}

func TestValidateWindowIsNeverFixedDurations(t *testing.T) {
	tests := []struct {
		name  string
		start string
		now   time.Time
		want  time.Duration
	}{
		{name: "spring forward: 2 weeks are 335 h", start: "2026-03-02", now: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC), want: 335 * time.Hour},
		{name: "fall back: 2 weeks are 337 h", start: "2026-10-26", now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC), want: 337 * time.Hour},
		{name: "no change: 2 weeks are 336 h", start: "2026-01-05", now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), want: 336 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, err := Validate(with(func(d *Draft) { d.StartDate = tt.start; d.Weeks = 2 }), tt.now, distanceSports, distanceRequired)
			if err != nil {
				t.Fatal(err)
			}
			if got := w.EndsAt.Sub(w.StartsAt); got != tt.want {
				t.Fatalf("window lasts %s, want %s (local weeks, not 14 × 24 h)", got, tt.want)
			}
		})
	}
}

// TestValidateStartsOnTheChosenLocalDay pins SC-API-079: in Santiago,
// midnight is skipped on 2027-09-05 (the clocks go from 23:59:59 to 01:00).
// The challenge starts at the first instant of that day, 01:00, never at
// 23:00 the day before, and ends at local midnight 2 weeks later.
func TestValidateStartsOnTheChosenLocalDay(t *testing.T) {
	santiago, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Fatal(err)
	}
	if time.Date(2027, 9, 5, 0, 0, 0, 0, santiago).Day() == 5 {
		t.Skip("this zone database does not skip midnight on 2027-09-05 in Santiago")
	}
	draft := with(func(d *Draft) { d.Timezone = "America/Santiago"; d.StartDate = "2027-09-05"; d.Weeks = 2 })
	w, err := Validate(draft, time.Date(2027, 9, 1, 12, 0, 0, 0, time.UTC), distanceSports, distanceRequired)
	if err != nil {
		t.Fatal(err)
	}
	if got := w.StartsAt.In(santiago).Format("2006-01-02 15:04"); got != "2027-09-05 01:00" {
		t.Fatalf("starts at %s local, want 2027-09-05 01:00", got)
	}
	if got := w.EndsAt.In(santiago).Format("2006-01-02 15:04"); got != "2027-09-19 00:00" {
		t.Fatalf("ends at %s local, want 2027-09-19 00:00", got)
	}
	// On that day, starting the same day is still allowed at its first instant.
	if _, err := Validate(draft, w.StartsAt, distanceSports, distanceRequired); err != nil {
		t.Fatalf("start today refused at the first instant of the day: %v", err)
	}
	// And the day before is yesterday from that first instant.
	yesterday := with(func(d *Draft) { d.Timezone = "America/Santiago"; d.StartDate = "2027-09-04"; d.Weeks = 2 })
	if _, err := Validate(yesterday, w.StartsAt, distanceSports, distanceRequired); err == nil {
		t.Fatal("start yesterday accepted at the first instant of the day")
	}
}

func TestValidateAYearAheadIsACalendarYear(t *testing.T) {
	// From 2027-03-01, a year ahead is 2028-03-01: 366 days later, since
	// 2028-02-29 exists. 365 days would stop at 2028-02-29.
	from := time.Date(2027, 3, 1, 17, 0, 0, 0, time.UTC)
	if _, err := Validate(with(func(d *Draft) { d.StartDate = "2028-03-01" }), from, distanceSports, distanceRequired); err != nil {
		t.Fatalf("start the same day next year refused: %v", err)
	}
	if _, err := Validate(with(func(d *Draft) { d.StartDate = "2028-03-02" }), from, distanceSports, distanceRequired); err == nil {
		t.Fatal("start after the same day next year accepted")
	}
}

func TestValidateStartAtTheEdgesOfToday(t *testing.T) {
	toronto, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	draft := with(func(d *Draft) { d.StartDate = "2026-03-04" })
	// Last second of the local 2026-03-04: starting that day is allowed.
	if _, err := Validate(draft, time.Date(2026, 3, 4, 23, 59, 59, 0, toronto), distanceSports, distanceRequired); err != nil {
		t.Fatalf("start today at 23:59:59 refused: %v", err)
	}
	// Local midnight of 2026-03-05: 2026-03-04 is now yesterday.
	if _, err := Validate(draft, time.Date(2026, 3, 5, 0, 0, 0, 0, toronto), distanceSports, distanceRequired); err == nil {
		t.Fatal("start yesterday accepted at local midnight")
	}
}

func TestValidateTodayIsInTheChallengeTimeZone(t *testing.T) {
	// 02:00 UTC on 2026-03-05 is still 2026-03-04 in Toronto (21:00).
	late := time.Date(2026, 3, 5, 2, 0, 0, 0, time.UTC)
	if _, err := Validate(with(func(d *Draft) { d.StartDate = "2026-03-04" }), late, distanceSports, distanceRequired); err != nil {
		t.Fatalf("start on the local today refused: %v", err)
	}
	// In Paris it is already 2026-03-05 (03:00): 2026-03-04 is in the past.
	paris := with(func(d *Draft) { d.Timezone = "Europe/Paris"; d.StartDate = "2026-03-04" })
	if _, err := Validate(paris, late, distanceSports, distanceRequired); err == nil {
		t.Fatal("start on the local yesterday accepted")
	}
}

func TestValidateText(t *testing.T) {
	str := func(s string) *string { return &s }
	tests := []struct {
		name       string
		changes    TextChanges
		wantTitle  *string
		wantFields string
	}{
		{name: "nothing"},
		{name: "title trimmed", changes: TextChanges{Title: str("  New title ")}, wantTitle: str("New title")},
		{name: "description removed", changes: TextChanges{Description: str("")}},
		{name: "title too short", changes: TextChanges{Title: str(" ab ")}, wantFields: "title"},
		{name: "title with a control character", changes: TextChanges{Title: str("ab\x00cd")}, wantFields: "title"},
		{name: "title with a right-to-left override", changes: TextChanges{Title: str("ab\u202ecd")}, wantFields: "title"},
		{name: "description with a zero width space", changes: TextChanges{Description: str("ab\u200bcd")}, wantFields: "description"},
		{name: "description too long", changes: TextChanges{Description: str(strings.Repeat("é", 2001))}, wantFields: "description"},
		{name: "both invalid, sorted", changes: TextChanges{Title: str("x"), Description: str("a\x1bb")}, wantFields: "description,title"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateText(tt.changes)
			if tt.wantFields != "" {
				var appErr *apperr.Error
				if !errors.As(err, &appErr) || appErr.Params[apperr.FieldsParam] != tt.wantFields {
					t.Fatalf("error = %v, want fields %q", err, tt.wantFields)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantTitle != nil && (got.Title == nil || *got.Title != *tt.wantTitle) {
				t.Fatalf("title %v, want %q", got.Title, *tt.wantTitle)
			}
		})
	}
}

func TestReferenceFactors(t *testing.T) {
	suggested := map[string]float64{"run": 1, "walk": 1, "ride": 0.3, "rowing": 0.5, "other": 1}
	tests := []struct {
		name      string
		suggested map[string]float64
		reference string
		want      map[string]float64
	}{
		{name: "run keeps the catalogue", suggested: suggested, reference: "run", want: map[string]float64{"run": 1, "walk": 1, "ride": 0.3, "rowing": 0.5, "other": 1}},
		{name: "ride: the slower sports capped at 1", suggested: suggested, reference: "ride", want: map[string]float64{"run": 1, "walk": 1, "ride": 1, "rowing": 1, "other": 1}},
		{name: "rowing: ride 0.3 / 0.5", suggested: suggested, reference: "rowing", want: map[string]float64{"run": 1, "walk": 1, "ride": 0.6, "rowing": 1, "other": 1}},
		{name: "never under 0.1", suggested: map[string]float64{"fast": 0.1, "slow": 2}, reference: "slow", want: map[string]float64{"fast": 0.1, "slow": 1}},
		{name: "rounded to two decimals", suggested: map[string]float64{"a": 0.3, "b": 0.7}, reference: "b", want: map[string]float64{"a": 0.43, "b": 1}},
		{name: "half rounded up", suggested: map[string]float64{"a": 0.1, "b": 0.8}, reference: "b", want: map[string]float64{"a": 0.13, "b": 1}},
		{name: "reference without a suggested factor: x1", suggested: map[string]float64{"ride": 0.3}, reference: "run", want: map[string]float64{"run": 1, "ride": 0.3}},
		{name: "reference over 1 in the catalogue", suggested: map[string]float64{"a": 1, "b": 1.5}, reference: "b", want: map[string]float64{"a": 0.67, "b": 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := referenceFactors(tt.suggested, tt.reference); !maps.Equal(got, tt.want) {
				t.Fatalf("referenceFactors() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBestResult(t *testing.T) {
	line := func(ty string, rank int) Ranking { return Ranking{Type: ty, Rank: rank, RankedCount: 6} }
	tests := []struct {
		name     string
		rankings []Ranking
		want     *Ranking
	}{
		{"lowest rank", []Ranking{line(CategoryCumulativeTime, 3), line(CategoryCumulativeDistance, 1), line(CategoryPoints, 2)}, &Ranking{Type: CategoryCumulativeDistance, Rank: 1, RankedCount: 6}},
		{"equal ranks: first category", []Ranking{line(CategoryCumulativeTime, 2), line(CategoryElimination, 2)}, &Ranking{Type: CategoryCumulativeTime, Rank: 2, RankedCount: 6}},
		{"unranked lines skipped", []Ranking{line(CategoryCumulativeTime, 0), line(CategoryElimination, 4)}, &Ranking{Type: CategoryElimination, Rank: 4, RankedCount: 6}},
		{"ranked nowhere", []Ranking{line(CategoryCumulativeTime, 0)}, nil},
		{"no category", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BestResult(tt.rankings); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("BestResult() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
