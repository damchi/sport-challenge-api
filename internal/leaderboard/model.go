package leaderboard

import (
	"time"

	"github.com/damchi/sport-challenge-api/internal/scoring"
)

// Category types, as the challenge module stores them (categories.type).
const (
	CategoryCumulativeTime     = "cumulative_time"
	CategoryCumulativeDistance = "cumulative_distance"
	CategoryElimination        = "elimination"
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
	// Sport and StepMeters are the elimination settings.
	Sport      string
	StepMeters int
}

// Ranked is a line of a ranking with the public name of the participant
// (never a deleted account: it is not ranked).
type Ranked struct {
	scoring.Entry
	Name Name
}

// Name is what a user shows the others: never the e-mail. Each field is nil
// when the account was deleted between the read of the list and that of the
// names, and for a name not set yet.
type Name struct {
	Pseudo    *string
	FirstName *string
	LastName  *string
}

// CategoryRanking is the ranking of one category.
type CategoryRanking struct {
	Type    string
	Entries []Ranked
}

// Leaderboard is the rankings of a challenge, in category order, computed
// at ComputedAt.
type Leaderboard struct {
	ComputedAt time.Time
	Categories []CategoryRanking
}

// Standing is where a participant stands in a challenge during its weeks:
// the current week, and their line in each category, in category order.
type Standing struct {
	Week       int
	WeekEndsAt time.Time
	Categories []Place
}

// Place is the line of a participant in the ranking of a category, with how
// many participants are ranked in it (abandoned ones are not).
type Place struct {
	Type        string
	Entry       scoring.Entry
	RankedCount int
}
