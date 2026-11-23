// Package activity records the activities of the users: one entry point for
// manual entry and, later, imports.
package activity

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/platform/textx"
)

// Technical limits of ticket SC-API-042 (README §2.5: "reasonable maximum
// values"). No speed cap in V1 (README §2.5, open question): only the
// duration and the distance are bounded.
const (
	maxDurationSeconds = 24 * 60 * 60
	maxDistanceMeters  = 1_000_000
	// maxElevationGainMeters bounds the elevation gain (SC-API-149).
	maxElevationGainMeters = 20_000
	minSportLabel          = 2
	maxSportLabel          = 40
	// futureTolerance absorbs the clock drift of the client's device, for
	// the start and the end of an activity.
	futureTolerance = 5 * time.Minute
	// otherSport is the sport typed by hand, the only one with a label.
	otherSport = "other"
)

// earliestStart is the technical lower bound of a start (SC-API-084): no
// real activity is older, so an earlier date is an input error.
var earliestStart = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

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
	// ElevationGainMeters is nil when no elevation gain is entered. It is
	// shown only: no category nor badge counts it (README §2.5).
	ElevationGainMeters *int
}

// SportRules is what the catalogue says of a sport for its activities: its
// distance mode (required, none or optional) and whether it takes an
// elevation gain.
type SportRules struct {
	DistanceMode     string
	ElevationAllowed bool
}

// End is the instant the activity of draft ends: its start plus its duration.
func (d Draft) End() time.Time {
	return d.StartedAt.Add(time.Duration(d.DurationSeconds) * time.Second)
}

// Validate checks draft at the instant now (from platform/clock), with sport
// the rules of its sport in the catalogue. It returns the draft normalized
// (label cleaned). An invalid field gives 400 validation_failed listing the
// fields, sorted; a valid distance the sport does not take, or a missing one
// it needs, gives 422 distance_not_allowed or distance_required; a valid
// elevation gain for a sport that takes none, or for an activity without
// distance, 422 elevation_not_allowed.
func Validate(draft Draft, sport SportRules, now time.Time) (Draft, error) {
	distanceMode := sport.DistanceMode
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
	if e := draft.ElevationGainMeters; e != nil && (*e < 0 || *e > maxElevationGainMeters) {
		fields = append(fields, "elevation_gain_meters")
	}
	if draft.StartedAt.IsZero() || draft.StartedAt.Before(earliestStart) || draft.StartedAt.After(now.Add(futureTolerance)) {
		fields = append(fields, "started_at")
	}
	// An activity is entered once done: its end is never in the future
	// either (README §2.5). Both fields make it, unless the start alone is
	// already wrong.
	if !slices.Contains(fields, "started_at") && draft.DurationSeconds > 0 && draft.End().After(now.Add(futureTolerance)) {
		fields = append(fields, "duration_seconds", "started_at")
		fields = slices.Compact(slices.Sorted(slices.Values(fields)))
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
	case draft.ElevationGainMeters != nil && (!sport.ElevationAllowed || draft.DistanceMeters == nil):
		return Draft{}, apperr.New(apperr.CodeElevationNotAllowed, nil)
	}

	draft.SportLabel = label
	return draft, nil
}

// sportLabel checks the label of a sport: required for "other", refused
// otherwise. It returns it cleaned (surrounding spaces removed, inner runs
// of spaces made one); 2 to 40 characters once cleaned, 2 of them visible
// (textx.VisibleLen), no hidden character (textx). The label is shown as plain text, never interpreted.
func sportLabel(sportCode string, label *string) (*string, bool) {
	if sportCode != otherSport {
		return nil, label == nil
	}
	if label == nil || textx.HasHidden(*label, "") {
		return nil, false
	}
	cleaned := strings.Join(strings.Fields(*label), " ")
	if textx.VisibleLen(cleaned) < minSportLabel || utf8.RuneCountInString(cleaned) > maxSportLabel {
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
