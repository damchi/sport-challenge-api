package scoring

import "time"

// Activity is an activity as the scorers see it, its start in UTC.
// DistanceMeters is nil without distance.
type Activity struct {
	UserID          string
	SportCode       string
	StartedAt       time.Time
	DurationSeconds int
	DistanceMeters  *int
	// For the points category, an Activity is a group of sessions of a
	// participant, week and sport, as the repository sums them (a single
	// session in tests), StartedAt an instant of its week (its first
	// session). LongestSeconds and LongestMeters are its longest
	// session by duration and by distance (nil without distance); Sessions
	// counts those lasting at least the category's minimum. Zero outside
	// the points category.
	LongestSeconds int
	LongestMeters  *int
	Sessions       int
}

// Scorer ranks the participants of a category from the raw activities.
// Only the activities started in the window count; those of users who are
// not participants are ignored.
type Scorer interface {
	Score(w Window, participants []Participant, activities []Activity) []Entry
}

// Participant is a member of a challenge as the scorers see it. An
// abandoned participant (who left or was excluded) is listed but not ranked.
// AbandonedAt is when they left (nil otherwise): the points category keeps
// them in the weeks locked by then.
type Participant struct {
	UserID      string
	Abandoned   bool
	AbandonedAt *time.Time
}

// Entry is the line of a participant in the ranking of a category. Value is
// in the unit of the category (seconds, meters, points). Rank starts at 1,
// equal values share it (1, 1, 3). An abandoned participant has rank and
// value 0.
type Entry struct {
	UserID    string
	Rank      int
	Value     int
	Abandoned bool
	// Progress is the live situation in the elimination category; nil for
	// the other categories and for an abandoned participant.
	Progress *Progress
	// Provisional is, in the points category only, the points of the weeks
	// started but not locked yet; nil for the other categories, for an
	// abandoned participant, and when every started week is locked.
	Provisional *int
}

// Live statuses of a participant in the elimination category.
const (
	StatusReached    = "reached"
	StatusAtRisk     = "at_risk"
	StatusInProgress = "in_progress"
	StatusEliminated = "eliminated"
)

// Progress is where a participant stands in the elimination category at the
// instant of the window. An eliminated participant has the status
// eliminated and the week of the elimination, nothing else. The others have
// the numbers of the current week: the distance already covered and the
// instant it ends; the minimum and a status only from week 2, which has a
// reference. Outside the weeks of the challenge there is nothing but the elimination.
type Progress struct {
	Status         string
	EliminatedWeek int
	MinimumMeters  *int
	DistanceMeters int
	WeekEndsAt     time.Time
}
