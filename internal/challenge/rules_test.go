package challenge

import (
	"errors"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
)

// distanceSports is the catalogue part the rules need: the sports that have
// a distance (required or optional).
var distanceSports = map[string]bool{"run": true, "walk": true, "ride": true, "other": true}

// now is 10:00 on Wednesday 2026-03-04 in Toronto (15:00 UTC).
var now = time.Date(2026, 3, 4, 15, 0, 0, 0, time.UTC)

func valid() Draft {
	return Draft{
		Title:                 "Spring run",
		Visibility:            "public",
		Timezone:              "America/Toronto",
		StartDate:             "2026-03-04",
		Weeks:                 6,
		EliminationStepMeters: 2000,
	}
}

func with(change func(*Draft)) Draft {
	d := valid()
	change(&d)
	return d
}

func TestValidate(t *testing.T) {
	desc := func(n int) *string { s := strings.Repeat("é", n); return &s }
	tests := []struct {
		name       string
		draft      Draft
		wantFields string // empty: valid
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

		// Elimination step X: meters, > 0 and 100 km at most.
		{name: "step of 1 m", draft: with(func(d *Draft) { d.EliminationStepMeters = 1 })},
		{name: "step of 100 km", draft: with(func(d *Draft) { d.EliminationStepMeters = 100_000 })},
		{name: "step of 0", draft: with(func(d *Draft) { d.EliminationStepMeters = 0 }), wantFields: "elimination_step_meters"},
		{name: "negative step", draft: with(func(d *Draft) { d.EliminationStepMeters = -1 }), wantFields: "elimination_step_meters"},
		{name: "step over 100 km", draft: with(func(d *Draft) { d.EliminationStepMeters = 100_001 }), wantFields: "elimination_step_meters"},

		// Distance factors chosen by the creator: distance sports, 0.1 to 2,
		// two decimals at most.
		{name: "factors at the bounds", draft: with(func(d *Draft) { d.DistanceFactors = map[string]float64{"ride": 0.1, "walk": 2, "other": 0.35} })},
		{name: "factors with float noise", draft: with(func(d *Draft) {
			d.DistanceFactors = map[string]float64{"ride": 0.29, "walk": 0.57, "other": 1.15}
		})},
		{name: "run is the fixed reference", draft: with(func(d *Draft) { d.DistanceFactors = map[string]float64{"run": 1} }), wantFields: "distance_factors.run"},
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
			draft:      Draft{Title: "x", Visibility: "x", Timezone: "America/Toronto", StartDate: "x", Weeks: 99, EliminationStepMeters: 0},
			wantFields: "elimination_step_meters,start_date,title,visibility,weeks",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Validate(tt.draft, now, distanceSports)
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
			w, err := Validate(tt.draft, tt.now, distanceSports)
			if err != nil {
				t.Fatal(err)
			}
			if !w.StartsAt.Equal(tt.wantFrom) || !w.EndsAt.Equal(tt.wantTo) {
				t.Fatalf("window [%s, %s), want [%s, %s)", w.StartsAt, w.EndsAt, tt.wantFrom, tt.wantTo)
			}
			if w.Location.String() != tt.draft.Timezone {
				t.Fatalf("location %s, want %s", w.Location, tt.draft.Timezone)
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
			w, err := Validate(with(func(d *Draft) { d.StartDate = tt.start; d.Weeks = 2 }), tt.now, distanceSports)
			if err != nil {
				t.Fatal(err)
			}
			if got := w.EndsAt.Sub(w.StartsAt); got != tt.want {
				t.Fatalf("window lasts %s, want %s (local weeks, not 14 × 24 h)", got, tt.want)
			}
		})
	}
}

func TestValidateAYearAheadIsACalendarYear(t *testing.T) {
	// From 2027-03-01, a year ahead is 2028-03-01: 366 days later, since
	// 2028-02-29 exists. 365 days would stop at 2028-02-29.
	from := time.Date(2027, 3, 1, 17, 0, 0, 0, time.UTC)
	if _, err := Validate(with(func(d *Draft) { d.StartDate = "2028-03-01" }), from, distanceSports); err != nil {
		t.Fatalf("start the same day next year refused: %v", err)
	}
	if _, err := Validate(with(func(d *Draft) { d.StartDate = "2028-03-02" }), from, distanceSports); err == nil {
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
	if _, err := Validate(draft, time.Date(2026, 3, 4, 23, 59, 59, 0, toronto), distanceSports); err != nil {
		t.Fatalf("start today at 23:59:59 refused: %v", err)
	}
	// Local midnight of 2026-03-05: 2026-03-04 is now yesterday.
	if _, err := Validate(draft, time.Date(2026, 3, 5, 0, 0, 0, 0, toronto), distanceSports); err == nil {
		t.Fatal("start yesterday accepted at local midnight")
	}
}

func TestValidateTodayIsInTheChallengeTimeZone(t *testing.T) {
	// 02:00 UTC on 2026-03-05 is still 2026-03-04 in Toronto (21:00).
	late := time.Date(2026, 3, 5, 2, 0, 0, 0, time.UTC)
	if _, err := Validate(with(func(d *Draft) { d.StartDate = "2026-03-04" }), late, distanceSports); err != nil {
		t.Fatalf("start on the local today refused: %v", err)
	}
	// In Paris it is already 2026-03-05 (03:00): 2026-03-04 is in the past.
	paris := with(func(d *Draft) { d.Timezone = "Europe/Paris"; d.StartDate = "2026-03-04" })
	if _, err := Validate(paris, late, distanceSports); err == nil {
		t.Fatal("start on the local yesterday accepted")
	}
}
