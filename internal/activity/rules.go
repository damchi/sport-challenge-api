// Package activity records the activities of the users: one entry point for
// manual entry and, later, imports.
package activity

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
)

// Technical limits of ticket SC-API-042 (README §2.5: "reasonable maximum
// values"). No speed cap in V1 (README §2.5, open question): only the
// duration and the distance are bounded.
const (
	maxDurationSeconds = 24 * 60 * 60
	maxDistanceMeters  = 1_000_000
	minSportLabel      = 2
	maxSportLabel      = 40
	// futureTolerance absorbs the clock drift of the client's device.
	futureTolerance = 5 * time.Minute
	// otherSport is the sport typed by hand, the only one with a label.
	otherSport = "other"
)

// Distance modes of the catalogue (sports.distance_mode).
const (
	distanceRequired = "required"
	distanceNone     = "none"
	distanceOptional = "optional"
)

// Draft is an activity as its author enters it. The speed is never part of
// it: it is computed from the duration and the distance.
type Draft struct {
	SportCode string
	// SportLabel is the free label of the sport "other"; nil otherwise.
	SportLabel      *string
	StartedAt       time.Time
	DurationSeconds int
	// DistanceMeters is nil when no distance is entered.
	DistanceMeters *int
}

// Validate checks draft at the instant now (from platform/clock), with
// distanceMode the distance mode of its sport in the catalogue. It returns
// the draft normalized (label cleaned). An invalid field gives 400
// validation_failed listing the fields, sorted; a valid distance the sport
// does not take, or a missing one it needs, gives 422 distance_not_allowed
// or distance_required.
func Validate(draft Draft, distanceMode string, now time.Time) (Draft, error) {
	if distanceMode != distanceRequired && distanceMode != distanceNone && distanceMode != distanceOptional {
		return Draft{}, apperr.New(apperr.CodeInternal, fmt.Errorf("unknown distance mode %q", distanceMode))
	}

	var fields []string
	if draft.DurationSeconds <= 0 || draft.DurationSeconds > maxDurationSeconds {
		fields = append(fields, "duration_seconds")
	}
	if d := draft.DistanceMeters; d != nil && (*d <= 0 || *d > maxDistanceMeters) {
		fields = append(fields, "distance_meters")
	}
	if draft.StartedAt.IsZero() || draft.StartedAt.After(now.Add(futureTolerance)) {
		fields = append(fields, "started_at")
	}
	label, ok := sportLabel(draft.SportCode, draft.SportLabel)
	if !ok {
		fields = append(fields, "sport_label")
	}
	if len(fields) > 0 {
		slices.Sort(fields)
		return Draft{}, apperr.InvalidFields(nil, fields...)
	}

	switch {
	case distanceMode == distanceRequired && draft.DistanceMeters == nil:
		return Draft{}, apperr.New(apperr.CodeDistanceRequired, nil)
	case distanceMode == distanceNone && draft.DistanceMeters != nil:
		return Draft{}, apperr.New(apperr.CodeDistanceNotAllowed, nil)
	}

	draft.SportLabel = label
	return draft, nil
}

// sportLabel checks the label of a sport: required for "other", refused
// otherwise. It returns it cleaned (surrounding spaces removed, inner runs
// of spaces made one); 2 to 40 characters once cleaned, no control
// character. The label is shown as plain text, never interpreted.
func sportLabel(sportCode string, label *string) (*string, bool) {
	if sportCode != otherSport {
		return nil, label == nil
	}
	if label == nil || strings.ContainsFunc(*label, unicode.IsControl) {
		return nil, false
	}
	cleaned := strings.Join(strings.Fields(*label), " ")
	n := utf8.RuneCountInString(cleaned)
	if n < minSportLabel || n > maxSportLabel {
		return nil, false
	}
	return &cleaned, true
}

// Velocity is the speed of an activity and its pace, its inverse.
type Velocity struct {
	MetersPerSecond     float64
	SecondsPerKilometer float64
}

// Speed computes the velocity of an activity from its distance and its
// duration; the client never sends it. Without a distance or a duration it
// is zero (never Inf nor NaN). It is not capped in V1.
func Speed(distanceMeters, durationSeconds int) Velocity {
	if distanceMeters <= 0 || durationSeconds <= 0 {
		return Velocity{}
	}
	return Velocity{
		MetersPerSecond:     float64(distanceMeters) / float64(durationSeconds),
		SecondsPerKilometer: float64(durationSeconds) * 1000 / float64(distanceMeters),
	}
}
