package scoring

import "time"

// Activity is an activity as the scorers see it, its start in UTC.
type Activity struct {
	UserID          string
	SportCode       string
	StartedAt       time.Time
	DurationSeconds int
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
}
