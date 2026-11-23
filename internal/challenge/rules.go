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
	// defaultReferenceSport is the reference sport of a challenge whose
	// creator chose none.
	defaultReferenceSport = "run"
	// distanceRequired is the distance mode a reference sport needs.
	distanceRequired     = "required"
	factorFieldPrefix    = "distance_factors."
	distanceFactorsField = "distance_factors"
	// defaultMinSessionMinutes is the minimum session of the points
	// category when the creator gives none.
	defaultMinSessionMinutes = 20
	minSessionMinutes        = 1
	maxSessionMinutes        = 180
)

// pointsPodiums are the sizes of the top that scores in the points
// category, those scoring.Points has a scale for.
var pointsPodiums = []int{5, 10}

// pointsCriteria are the criteria of the points category.
var pointsCriteria = []string{
	scoring.CriterionLongestDuration, scoring.CriterionLongestDistance,
	scoring.CriterionSessionCount, scoring.CriterionWeeklyElevation,
}

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
	// Distance is whether the challenge has the cumulative distance
	// category; the cumulative time is always there.
	Distance bool
	// Elimination is the elimination category, nil when not chosen.
	Elimination *EliminationDraft
	// Points is the points category, nil when not chosen.
	Points *PointsDraft
	// Elevation is whether the challenge has the cumulative elevation gain
	// category; it has no settings.
	Elevation bool
	// ReferenceSport is the sport the distances are brought back to and the
	// elimination is run in; nil for run (an empty code is refused).
	ReferenceSport *string
	// DistanceFactors are the factors the creator chose, by sport code; the
	// other distance sports keep their suggested factor, brought back to the
	// reference (referenceFactors). The reference is ×1 and cannot be
	// changed.
	DistanceFactors map[string]float64
}

// EliminationDraft is the setting of the elimination category.
type EliminationDraft struct {
	// StepMeters is the increment X, in meters.
	StepMeters int
}

// PointsDraft is the setting of the points category.
type PointsDraft struct {
	// Criteria are those chosen, at least one, each once.
	Criteria []string
	// Podium is the size of the top that scores: 5 or 10.
	Podium int
	// MinSessionMinutes is the minimum duration of a session counted by
	// the session count, 1 to 180 minutes; nil for the default, 20.
	MinSessionMinutes *int
}

// minSessionMinutes is the minimum session chosen, or the default.
func (p PointsDraft) minSessionMinutes() int {
	if p.MinSessionMinutes == nil {
		return defaultMinSessionMinutes
	}
	return *p.MinSessionMinutes
}

// longestDistance reports whether the points category ranks the longest
// session by equivalent distance, which needs the distance factors.
func (p *PointsDraft) longestDistance() bool {
	return p != nil && slices.Contains(p.Criteria, scoring.CriterionLongestDistance)
}

// usesFactors reports whether a category of the draft uses the distance
// factors: the cumulative distance, or the longest distance of the points.
func (d Draft) usesFactors() bool {
	return d.Distance || d.Points.longestDistance()
}

// reference is the reference sport of the draft, run when none is given.
func (d Draft) reference() string {
	if d.ReferenceSport == nil {
		return defaultReferenceSport
	}
	return *d.ReferenceSport
}

// Window is the period of a challenge, [StartsAt, EndsAt) in UTC.
type Window struct {
	StartsAt time.Time
	EndsAt   time.Time
}

// Validate checks draft against the challenge rules, at the instant now
// (from platform/clock), with distanceSports the sports of the catalogue that
// have a distance and referenceMode the distance mode of the reference sport
// (empty when the catalogue has no such sport). The distance factors are
// refused without a category that uses them (usesFactors).
// It returns the window of the challenge: from the first instant of the
// first local day (midnight, or the end of the skipped hour where a time
// change skips midnight: scoring.StartOfDay), for a whole number of weeks
// counted in local days (a week across a time change lasts 167 or 169
// hours). A broken rule gives 400 validation_failed listing the fields,
// sorted.
func Validate(draft Draft, now time.Time, distanceSports map[string]bool, referenceMode string) (Window, error) {
	return validate(draft, now, distanceSports, referenceMode, false)
}

// ValidateSettings checks the settings an admin gives a challenge (README
// §2.12) with the rules of the creation (Validate), but the first day, which
// may be in the past: the admin moves a challenge in progress or finished.
// It is still at most a year ahead.
func ValidateSettings(draft Draft, now time.Time, distanceSports map[string]bool, referenceMode string) (Window, error) {
	return validate(draft, now, distanceSports, referenceMode, true)
}

// validate is Validate, the first day allowed in the past with pastAllowed.
func validate(draft Draft, now time.Time, distanceSports map[string]bool, referenceMode string, pastAllowed bool) (Window, error) {
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
	if e := draft.Elimination; e != nil && (e.StepMeters <= 0 || e.StepMeters > maxEliminationStep) {
		fields = append(fields, "categories.elimination.step_meters")
	}
	if p := draft.Points; p != nil {
		fields = append(fields, invalidPoints(*p)...)
	}
	if referenceMode != distanceRequired {
		fields = append(fields, "reference_sport")
	}
	if !draft.usesFactors() && draft.DistanceFactors != nil {
		// No category uses them: the field alone, its keys never echoed.
		fields = append(fields, distanceFactorsField)
	} else {
		fields = append(fields, invalidFactors(draft.DistanceFactors, distanceSports, draft.reference())...)
	}

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
		if (!pastAllowed && start.Before(today)) || start.After(scoring.StartOfDay(y+1, m, d, loc)) {
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

// invalidPoints returns the fields of the points settings that break the
// rules.
func invalidPoints(p PointsDraft) []string {
	var fields []string
	seen := make(map[string]bool, len(p.Criteria))
	ok := len(p.Criteria) > 0
	for _, c := range p.Criteria {
		if seen[c] || !slices.Contains(pointsCriteria, c) {
			ok = false
		}
		seen[c] = true
	}
	if !ok {
		fields = append(fields, "categories.points.criteria")
	}
	if !slices.Contains(pointsPodiums, p.Podium) {
		fields = append(fields, "categories.points.podium")
	}
	if m := p.minSessionMinutes(); m < minSessionMinutes || m > maxSessionMinutes {
		fields = append(fields, "categories.points.min_session_minutes")
	}
	return fields
}

// invalidFactors returns the fields of factors that break the rules: a known
// distance sport is named ("distance_factors.ride"), the reference among
// them; a code outside the distance sports gives "distance_factors" only, so
// a value the client chose is never sent back.
func invalidFactors(factors map[string]float64, distanceSports map[string]bool, reference string) []string {
	var fields []string
	unknown := false
	for code, factor := range factors {
		if !distanceSports[code] {
			unknown = true
			continue
		}
		if code == reference {
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
		fields = append(fields, distanceFactorsField)
	}
	return fields
}

// referenceFactors brings the suggested factors of the catalogue, made for
// run, back to reference (README §2.4): min(1, f / f_reference), rounded to
// two decimals (half up) and never under 0.1, the reference itself ×1. The
// sports slower than the reference count 1 for 1. Computed in hundredths, as
// the catalogue stores them, so the rounding is exact. A reference the
// catalogue gives no factor counts as ×1.
func referenceFactors(suggested map[string]float64, reference string) map[string]float64 {
	ref := int64(math.Round(suggested[reference] * distanceFactorScale))
	if ref <= 0 {
		ref = distanceFactorScale
	}
	factors := make(map[string]float64, len(suggested))
	for code, f := range suggested {
		hundredths := int64(math.Round(f * distanceFactorScale))
		// Half up: floor(100 f / ref + 1/2), in integers.
		ratio := (2*distanceFactorScale*hundredths + ref) / (2 * ref)
		ratio = min(max(ratio, minDistanceFactor*distanceFactorScale), distanceFactorScale)
		factors[code] = float64(ratio) / distanceFactorScale
	}
	factors[reference] = 1
	return factors
}

// BestResult is the best ranked line of rankings, in category order: the
// lowest rank, the first category of the challenge between equal ranks; nil
// when the participant is ranked in none.
func BestResult(rankings []Ranking) *Ranking {
	var best *Ranking
	for i := range rankings {
		r := &rankings[i]
		if r.Rank > 0 && (best == nil || r.Rank < best.Rank) {
			best = r
		}
	}
	if best == nil {
		return nil
	}
	out := *best
	return &out
}
