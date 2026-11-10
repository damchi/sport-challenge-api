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

// Recorded is an activity once recorded, with the challenges where it
// counts now (possibly none).
type Recorded struct {
	Activity
	ChallengeIDs []string
	// Created is false when the idempotency key gave an activity recorded
	// before: nothing was written.
	Created bool
}
