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
	CategoryPoints             = "points"
	CategoryElevation          = "cumulative_elevation"
)

// Challenge is a challenge as the leaderboard needs it: its window and its
// categories, with the settings frozen at creation.
type Challenge struct {
	ID         string
	Timezone   string
	StartsAt   time.Time
	EndsAt     time.Time
	Categories []Category
	// ReadsActivities tells the reader may see the activities of the
	// participants (rule challenge.ReadsActivities): the values behind the
	// points of a breakdown are shown to them alone.
	ReadsActivities bool
}

// Category is a category of a challenge, in position order.
type Category struct {
	Type string
	// Factors is the distance factor by sport code (cumulative_distance,
	// and points with the longest distance criterion).
	Factors map[string]float64
	// Sport and StepMeters, or Exercise and StepRepetitions for an
	// elimination by repetitions, are the elimination settings.
	Sport           string
	StepMeters      int
	Exercise        string
	StepRepetitions int
	// Criteria, Podium and MinSessionSeconds are the points settings.
	Criteria          []string
	Podium            int
	MinSessionSeconds int
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

// CategoryRanking is the ranking of one category, with how many
// participants are ranked in it (abandoned ones are not, deleted accounts
// are not listed).
type CategoryRanking struct {
	Type string
	// Exercise is that of an elimination by repetitions, empty otherwise.
	Exercise    string
	RankedCount int
	Entries     []Ranked
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

// PageRequest is the part of the leaderboard asked for: Category alone when
// set, and with Limit > 0, the podium then Limit lines after it, from After
// (nil for the first page). Limit 0 is every line, without podium.
type PageRequest struct {
	Category string
	Limit    int
	After    *Position
}

// Position is where the next page of a category starts in its lines after
// the podium, and the state of the ranking it was given in (Version): a
// ranking that has changed since refuses it.
type Position struct {
	Category string `json:"c"`
	Offset   int    `json:"o"`
	Version  string `json:"v"`
}

// Page is the part of a leaderboard asked for, in category order, computed
// at ComputedAt.
type Page struct {
	ComputedAt time.Time
	Categories []CategoryPage
}

// CategoryPage is a category of a Page: its podium (nil without a limit)
// and its Entries, the page after it or every line, the line of the actor
// (Me, nil when not a participant), and Next, the position of the next page
// (nil at the end and without a limit).
type CategoryPage struct {
	Type        string
	Exercise    string
	RankedCount int
	Podium      []Ranked
	Entries     []Ranked
	Me          *Ranked
	Next        *Position
}

// Breakdown is where the points of a participant come from in the points
// category of a challenge, computed at ComputedAt.
type Breakdown struct {
	UserID     string
	ComputedAt time.Time
	Abandoned  bool
	// Frozen tells the breakdown is that of the frozen results.
	Frozen bool
	// ValuesHidden tells the reader may not see the activities of the
	// participants: the value of each criterion is left out (zero), the
	// ranks and points stay.
	ValuesHidden bool
	// Acquired is the points of the locked weeks; Provisional those of the
	// weeks started but not locked yet, nil when there is none, for a
	// participant who abandoned and once frozen.
	Acquired    int
	Provisional *int
	Weeks       []BreakdownWeek
}

// BreakdownWeek is a week of a Breakdown: its bounds, whether it is locked,
// its points and the line of each criterion, in the order of the category.
type BreakdownWeek struct {
	Number   int
	StartsAt time.Time
	EndsAt   time.Time
	Locked   bool
	Points   int
	Criteria []scoring.CriterionPoints
}
