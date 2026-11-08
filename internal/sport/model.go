// Package sport serves the catalogue of sports: whether each sport has a
// distance and its suggested distance factor.
package sport

// Sport is a row of the sports table (reference data from the migrations).
type Sport struct {
	Code         string `gorm:"primaryKey"`
	DistanceMode string
	// SuggestedFactor is nil for sports without distance.
	SuggestedFactor *float64
}
