package closure

import (
	"encoding/json"
	"time"

	"github.com/damchi/sport-challenge-api/internal/scoring"
)

// Challenge is a challenge whose weeks can be closed: not deleted and
// started, with its run elimination category and the settings frozen at
// creation.
type Challenge struct {
	ID       string
	Timezone string
	StartsAt time.Time
	EndsAt   time.Time
	// CategoryID is the run elimination category, the one weeks are judged in.
	CategoryID string
	Sport      string
	StepMeters int
}

// Result counts what a run of the task did, for the log.
type Result struct {
	// Challenges is the number of challenges that had weeks to close.
	Challenges int
	// Weeks is the number of weeks closed.
	Weeks int
	// Eliminations is the number of eliminations recorded.
	Eliminations int
}

// EndedChallenge is a challenge that is over, not deleted and without a
// result snapshot yet, with the settings of its categories frozen at creation.
type EndedChallenge struct {
	ID         string
	Timezone   string
	StartsAt   time.Time
	EndsAt     time.Time
	Categories []Category
}

// Category is a category of a challenge, in position order.
type Category struct {
	ID   string
	Type string
	// Factors is the distance factor by sport code (cumulative_distance).
	Factors map[string]float64
	// Sport and StepMeters are the run elimination settings.
	Sport      string
	StepMeters int
}

// Snapshot is the final ranking of a category, as JSON.
type Snapshot struct {
	CategoryID string
	Type       string
	Payload    json.RawMessage
}

// LastWeek is the closure of the last week of a challenge, recorded with its
// snapshots: the week ends with the challenge, so close-weeks leaves it to
// the finalization. Eliminated are the eliminations judged that week in
// category CategoryID, the run elimination one.
type LastWeek struct {
	Number     int
	CategoryID string
	Eliminated []scoring.Eliminated
}

// FinalizeResult counts what a run of the finalization did, for the log.
type FinalizeResult struct {
	// Challenges is the number of challenges whose results were frozen.
	Challenges int
}
