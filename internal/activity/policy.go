package activity

import (
	"time"

	"github.com/damchi/sport-challenge-api/internal/scoring"
)

// CanChange reports whether actorID may modify or delete an activity of
// authorID: its author only (README §5.7). Creating needs no such check: an
// activity is always recorded for the actor.
func CanChange(actorID, authorID string) bool {
	return actorID != "" && actorID == authorID
}

// Participation is a challenge of the author of an activity, as the lock
// reads it: its window [StartsAt, EndsAt) in UTC, cut into weeks in its time
// zone (required: loaded from the challenge's).
type Participation struct {
	StartsAt time.Time
	EndsAt   time.Time
	Location *time.Location
	Deleted  bool
	// Active is true when the author is an active participant: read from the
	// participation status, never the role (a creator who abandoned no
	// longer competes).
	Active bool
}

// IsLocked reports whether an activity started at startedAt is locked at now
// (from platform/clock, never the worker): its week has ended in at least
// one challenge where it counts (ADR 0001). It counts in a challenge not
// deleted, finished ones included, whose window holds startedAt and where
// the author is an active participant.
func IsLocked(now, startedAt time.Time, challenges []Participation) bool {
	for _, c := range challenges {
		if c.Deleted || !c.Active {
			continue
		}
		w := scoring.Window{Start: c.StartsAt, End: c.EndsAt, Location: c.Location}
		if !w.Contains(startedAt) {
			continue
		}
		// A challenge whose weeks cannot be cut locks rather than lets an
		// activity move: the time zone is validated at creation.
		if c.Location == nil {
			return true
		}
		if _, end := w.WeekBounds(w.WeekOf(startedAt)); !now.Before(end) {
			return true
		}
	}
	return false
}
