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
}

// Scorer ranks the participants of a category from the raw activities.
// Only the activities started in the window count; those of users who are
// not participants are ignored.
type Scorer interface {
	Score(w Window, participants []Participant, activities []Activity) []Entry
}

// Participant is a member of a challenge as the scorers see it. An
// abandoned participant (who left or was excluded) is listed but not ranked.
type Participant struct {
	UserID    string
	Abandoned bool
}

// Entry is the line of a participant in the ranking of a category. Value is
// in the unit of the category (seconds, meters). Rank starts at 1, equal
// values share it (1, 1, 3). An abandoned participant has rank and value 0.
type Entry struct {
	UserID    string
	Rank      int
	Value     int
	Abandoned bool
	// Progress is the live situation in the elimination category; nil for
	// the other categories and for an abandoned participant.
	Progress *Progress
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
// the numbers of the current week: the distance already run and the instant
// it ends; the minimum and a status only from week 2, which has a reference.
// Outside the weeks of the challenge there is nothing but the elimination.
type Progress struct {
	Status         string
	EliminatedWeek int
	MinimumMeters  *int
	RunMeters      int
	WeekEndsAt     time.Time
}
