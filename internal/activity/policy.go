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
// zone (required: loaded from the challenge's). A deleted challenge is never
// one: the memberships are read without them.
type Participation struct {
	ChallengeID string
	StartsAt    time.Time
	EndsAt      time.Time
	Location    *time.Location
	// Active is true when the author is an active participant: read from the
	// participation status, never the role (a creator who abandoned no
	// longer competes).
	Active bool
}

// CountsIn returns the challenges, in the order of challenges, where an
// activity started at startedAt counts (ADR 0001): finished ones included,
// whose window holds startedAt and where the author is an active
// participant. Never a reason to refuse an activity: it may count nowhere.
func CountsIn(startedAt time.Time, challenges []Participation) []string {
	var ids []string
	for _, c := range challenges {
		if counts(c, startedAt) {
			ids = append(ids, c.ChallengeID)
		}
	}
	return ids
}

// IsLocked reports whether an activity started at startedAt is locked at now
// (from platform/clock, never the worker): its week has ended in at least
// one challenge where it counts (CountsIn).
func IsLocked(now, startedAt time.Time, challenges []Participation) bool {
	for _, c := range challenges {
		if !counts(c, startedAt) {
			continue
		}
		// A challenge whose weeks cannot be cut locks rather than lets an
		// activity move: the time zone is validated at creation.
		if c.Location == nil {
			return true
		}
		w := window(c)
		if _, end := w.WeekBounds(w.WeekOf(startedAt)); !now.Before(end) {
			return true
		}
	}
	return false
}

// counts reports whether an activity started at startedAt counts in c.
func counts(c Participation, startedAt time.Time) bool {
	return c.Active && window(c).Contains(startedAt)
}

func window(c Participation) scoring.Window {
	return scoring.Window{Start: c.StartsAt, End: c.EndsAt, Location: c.Location}
}
