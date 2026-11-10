package leaderboard

import (
	"time"

	"github.com/damchi/sport-challenge-api/internal/scoring"
)

// Category types, as the challenge module stores them (categories.type).
const (
	CategoryCumulativeTime     = "cumulative_time"
	CategoryCumulativeDistance = "cumulative_distance"
	CategoryRunElimination     = "run_elimination"
)

// Challenge is a challenge as the leaderboard needs it: its window and its
// categories, with the settings frozen at creation.
type Challenge struct {
	ID         string
	Timezone   string
	StartsAt   time.Time
	EndsAt     time.Time
	Categories []Category
}

// Category is a category of a challenge, in position order.
type Category struct {
	Type string
	// Factors is the distance factor by sport code (cumulative_distance).
	Factors map[string]float64
	// Sport and StepMeters are the run elimination settings.
	Sport      string
	StepMeters int
}

// CategoryRanking is the ranking of one category.
type CategoryRanking struct {
	Type    string
	Entries []scoring.Entry
}
