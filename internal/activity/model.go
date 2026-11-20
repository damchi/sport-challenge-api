package activity

import "time"

// SourceManual marks an activity entered by hand (the only source in V1).
const SourceManual = "manual"

// Activity is a row of the activities table.
type Activity struct {
	ID         string
	UserID     string
	SportCode  string
	SportLabel *string
	StartedAt  time.Time
	// DurationSeconds is always set; DistanceMeters is nil without distance.
	DurationSeconds int
	DistanceMeters  *int
	Source          string
	// IdempotencyKey is the key the client sent with the creation, if any.
	IdempotencyKey *string
}

// Recorded is an activity once recorded, with what it brings now to each
// challenge where it counts (possibly none).
type Recorded struct {
	Activity
	Contributions []Contribution
	// Created is false when the idempotency key gave an activity recorded
	// before: nothing was written.
	Created bool
	// Locked is true once its week is locked in a challenge where it counts
	// (IsLocked): it can no longer be changed nor deleted.
	Locked bool
}

// ListQuery selects a page of the activities of a user, deleted ones left
// out: those started in [From, To), a nil bound being open.
type ListQuery struct {
	UserID string
	From   *time.Time
	To     *time.Time
	Limit  int
	// After is the position of the last item of the previous page.
	After *Position
}

// Position is the place of an activity in a listing, most recent first: the
// pagination key (started_at, id), encoded in the cursor.
type Position struct {
	StartedAt time.Time `json:"s"`
	ID        string    `json:"i"`
}

// Page is a page of activities; Next is nil on the last page.
type Page struct {
	Items []Recorded
	Next  *Position
}
