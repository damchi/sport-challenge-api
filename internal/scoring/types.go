package scoring

// Participant is a member of a challenge as the scorers see it. An
// abandoned participant (who left or was excluded) is listed but not ranked.
type Participant struct {
	UserID    string
	Abandoned bool
}

// Entry is the line of a participant in the ranking of a category. Value is
// in the unit of the category (seconds, meters). Rank starts at 1, equal
// values share it (1, 1, 3); it is 0 for an abandoned participant.
type Entry struct {
	UserID    string
	Rank      int
	Value     int
	Abandoned bool
}
