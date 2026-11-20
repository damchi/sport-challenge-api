// Package challenge manages the challenges: creation, listing, participation.
package challenge

import (
	"errors"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/textx"
	"github.com/damchi/sport-challenge-api/internal/scoring"
)

var errInvalidTimezone = errors.New("invalid time zone")

// Limits of README §2.1 and of decisions D2 and D3.
const (
	minTitle            = 3
	maxTitle            = 80
	maxDescription      = 2000
	minWeeks            = 2
	maxWeeks            = 52
	maxEliminationStep  = 100_000 // meters
	minDistanceFactor   = 0.1
	maxDistanceFactor   = 2
	distanceFactorScale = 100 // two decimals at most, like numeric(4,2)
	startDateLayout     = time.DateOnly
	// referenceSport is the ×1 reference of the distance category: every
	// distance is brought back to a running distance, so its factor is fixed.
	referenceSport       = "run"
	factorFieldPrefix    = "distance_factors."
	unknownFactorsFields = "distance_factors"
)

// Draft is a challenge as its creator describes it.
type Draft struct {
	Title       string
	Description *string
	// Visibility is "public" or "private".
	Visibility       string
	MembersCanInvite bool
	// Timezone is the IANA name of the place the challenge is run from; the
	// weeks are cut in it.
	Timezone string
	// StartDate is the first local day, as YYYY-MM-DD: today or later, at
	// most a year ahead.
	StartDate string
	Weeks     int
	// EliminationStepMeters is the increment X of the elimination category.
	EliminationStepMeters int
	// DistanceFactors are the factors the creator chose, by sport code; the
	// other distance sports keep their suggested factor. run is the ×1
	// reference and cannot be changed.
	DistanceFactors map[string]float64
}

// Window is the period of a challenge, [StartsAt, EndsAt) in UTC.
type Window struct {
	StartsAt time.Time
	EndsAt   time.Time
}

// Validate checks draft against the challenge rules, at the instant now
// (from platform/clock), with distanceSports the sports of the catalogue that
// have a distance. It returns the window of the challenge: from the first
// instant of the first local day (midnight, or the end of the skipped hour
// where a time change skips midnight: scoring.StartOfDay), for a whole number
// of weeks counted in local days (a week across a time change lasts 167 or
// 169 hours). A broken rule gives 400 validation_failed listing the fields,
// sorted.
func Validate(draft Draft, now time.Time, distanceSports map[string]bool) (Window, error) {
	var fields []string

	if !validTitle(draft.Title) {
		fields = append(fields, "title")
	}
	if d := draft.Description; d != nil && !validDescription(*d) {
		fields = append(fields, "description")
	}
	if draft.Visibility != VisibilityPublic && draft.Visibility != VisibilityPrivate {
		fields = append(fields, "visibility")
	}
	if draft.Weeks < minWeeks || draft.Weeks > maxWeeks {
		fields = append(fields, "weeks")
	}
	if draft.EliminationStepMeters <= 0 || draft.EliminationStepMeters > maxEliminationStep {
		fields = append(fields, "elimination_step_meters")
	}
	fields = append(fields, invalidFactors(draft.DistanceFactors, distanceSports)...)

	loc, locErr := location(draft.Timezone)
	if locErr != nil {
		fields = append(fields, "timezone")
	}
	day, dayErr := time.Parse(startDateLayout, draft.StartDate)
	if dayErr != nil {
		fields = append(fields, "start_date")
	}

	var window Window
	// "Today or later" needs both the day and the time zone.
	if locErr == nil && dayErr == nil {
		start := scoring.StartOfDay(day.Year(), day.Month(), day.Day(), loc)
		y, m, d := now.In(loc).Date()
		today := scoring.StartOfDay(y, m, d, loc)
		// At most a year ahead: the same day next year, in local days.
		if start.Before(today) || start.After(scoring.StartOfDay(y+1, m, d, loc)) {
			fields = append(fields, "start_date")
		}
		window = Window{
			StartsAt: start,
			EndsAt:   scoring.StartOfDay(day.Year(), day.Month(), day.Day()+7*draft.Weeks, loc),
		}
	}

	if len(fields) > 0 {
		slices.Sort(fields)
		return Window{}, apperr.InvalidFields(nil, fields...)
	}
	return window, nil
}

// validTitle checks a title, at creation and on edit: 3 to 80 characters
// once surrounding spaces are removed, 3 of them visible (textx.VisibleLen),
// no hidden character (textx).
func validTitle(title string) bool {
	trimmed := strings.TrimSpace(title)
	return textx.VisibleLen(trimmed) >= minTitle && utf8.RuneCountInString(trimmed) <= maxTitle &&
		!textx.HasHidden(title, "")
}

// validDescription checks a description: 2000 characters at most, no
// hidden character (textx) but line breaks and tabs.
func validDescription(d string) bool {
	return utf8.RuneCountInString(d) <= maxDescription && !textx.HasHidden(d, "\n\r\t\u2028\u2029")
}

// TextChanges are the fields of a challenge its creators may edit at any
// time; nil leaves a field as is, and an empty description removes it.
type TextChanges struct {
	Title       *string
	Description *string
}

// ValidateText checks changes with the rules of the creation and returns
// them normalized (title trimmed).
func ValidateText(changes TextChanges) (TextChanges, error) {
	var fields []string
	if t := changes.Title; t != nil {
		if !validTitle(*t) {
			fields = append(fields, "title")
		} else {
			trimmed := strings.TrimSpace(*t)
			changes.Title = &trimmed
		}
	}
	if d := changes.Description; d != nil && !validDescription(*d) {
		fields = append(fields, "description")
	}
	if len(fields) > 0 {
		slices.Sort(fields)
		return TextChanges{}, apperr.InvalidFields(nil, fields...)
	}
	return changes, nil
}

// location loads an IANA time zone. It refuses the empty name and "Local",
// which time.LoadLocation turns into UTC and the server's zone.
func location(name string) (*time.Location, error) {
	if name == "" || name == "Local" {
		return nil, errInvalidTimezone
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, errInvalidTimezone
	}
	return loc, nil
}

// invalidFactors returns the fields of factors that break the rules: a known
// distance sport is named ("distance_factors.ride"); a code outside the
// distance sports gives "distance_factors" only, so a value the client chose
// is never sent back.
func invalidFactors(factors map[string]float64, distanceSports map[string]bool) []string {
	var fields []string
	unknown := false
	for code, factor := range factors {
		if !distanceSports[code] {
			unknown = true
			continue
		}
		if code == referenceSport {
			fields = append(fields, factorFieldPrefix+code)
			continue
		}
		// Written in the positive so that NaN, for which every comparison
		// is false, is refused.
		scaled := factor * distanceFactorScale
		if !(factor >= minDistanceFactor && factor <= maxDistanceFactor) || math.Abs(scaled-math.Round(scaled)) > 1e-9 {
			fields = append(fields, factorFieldPrefix+code)
		}
	}
	if unknown {
		fields = append(fields, unknownFactorsFields)
	}
	return fields
}
