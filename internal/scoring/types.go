// Package scoring computes the rankings of a challenge from raw activities
// and the rules of its categories. It imports the standard library only: no
// database, no HTTP, no clock (the window carries the current instant).
package scoring

import "time"

// Activity is an activity of a participant, as the scorers read it.
type Activity struct {
	UserID          string
	SportCode       string
	StartedAt       time.Time
	DurationSeconds int
	// DistanceMeters is nil when no distance is entered.
	DistanceMeters *int
}

// Participant is a participant of the challenge. An abandoned one (left,
// excluded or deleted account) is listed but never ranked.
type Participant struct {
	UserID    string
	Abandoned bool
}

// Entry is the line of a participant in the ranking of a category.
type Entry struct {
	UserID string
	// Rank starts at 1, ties sharing it (1, 1, 3); 0 for an abandoned
	// participant, who is not ranked.
	Rank int
	// Value is the total of the category, in its unit (seconds, meters).
	Value     int64
	Abandoned bool
}

// Scorer ranks the participants of a category. It lists every participant,
// abandoned ones included, and counts only the activities of the window
// (Window.Contains).
type Scorer interface {
	Score(w Window, participants []Participant, activities []Activity) []Entry
}
