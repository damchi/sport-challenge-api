// Package sport serves the catalogue of sports: whether each sport has a
// distance and its suggested distance factor, and the exercises of the
// elimination by repetitions.
package sport

// Sport is a row of the sports table (reference data from the migrations).
type Sport struct {
	Code         string `gorm:"primaryKey"`
	DistanceMode string
	// SuggestedFactor is nil for sports without distance.
	SuggestedFactor *float64
	// ElevationAllowed tells whether an activity of the sport may have an
	// elevation gain; never for a sport without distance.
	ElevationAllowed bool
}

// Exercise is a row of the exercises table (reference data from the
// migrations): an exercise of the elimination by repetitions and its sport.
type Exercise struct {
	Code      string `gorm:"primaryKey"`
	SportCode string
	// Position is the display order within the sport.
	Position int
}
