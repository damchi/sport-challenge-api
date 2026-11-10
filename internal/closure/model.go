package closure

import "time"

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
