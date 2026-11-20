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
	Title       string
	StartsAt    time.Time
	EndsAt      time.Time
	Location    *time.Location
	// Factors are those of the cumulative distance category of the
	// challenge, frozen at its creation.
	Factors map[string]float64
	// Active is true when the author is an active participant: read from the
	// participation status, never the role (a creator who abandoned no
	// longer competes).
	Active bool
}

// Contribution is what an activity brings to a challenge where it counts:
// its duration to the cumulative time, and its distance weighted by the
// factor of its sport to the cumulative distance, nil when it brings none.
type Contribution struct {
	ChallengeID     string
	ChallengeTitle  string
	DistanceMeters  *int
	DurationSeconds int
}

// Contributions returns what a brings to each challenge, in the order of
// challenges, where it counts (ADR 0001): finished ones included, whose
// window holds its start and where the author is an active participant.
// The weighted distance is the one of the ranking (scoring.Distance). Never
// a reason to refuse an activity: it may count nowhere.
func Contributions(a Activity, challenges []Participation) []Contribution {
	var out []Contribution
	for _, c := range challenges {
		if !counts(c, a.StartedAt) {
			continue
		}
		out = append(out, Contribution{
			ChallengeID:     c.ChallengeID,
			ChallengeTitle:  c.Title,
			DistanceMeters:  scoring.Distance{Factors: c.Factors}.Weighted(a.SportCode, a.DistanceMeters),
			DurationSeconds: a.DurationSeconds,
		})
	}
	return out
}

// IsLocked reports whether an activity started at startedAt is locked at now
// (from platform/clock, never the worker): its week is locked in at least
// one challenge where it counts (Contributions), from 7 am, local time of
// that challenge, on the first day of the next week (scoring.Window.WeekLocksAt).
// An activity across the end of a week belongs to the week it starts in.
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
		if !now.Before(w.WeekLocksAt(w.WeekOf(startedAt))) {
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
