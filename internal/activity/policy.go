package activity

import (
	"time"

	"github.com/damchi/sport-challenge-api/internal/platform/apperr"
	"github.com/damchi/sport-challenge-api/internal/scoring"
)

// CanChange reports whether actorID may modify or delete an activity of
// authorID: its author only (README §5.7). Creating needs no such check: an
// activity is always recorded for the actor.
func CanChange(actorID, authorID string) bool {
	return actorID != "" && actorID == authorID
}

// AdminRefusal lets an actor with the admin permission of their access
// token record, change or delete the activity of anyone, in any week
// (README §2.12); anyone else gets 404 not_found, as on every admin route.
func AdminRefusal(admin bool) error {
	if !admin {
		return apperr.New(apperr.CodeNotFound, nil)
	}
	return nil
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
	// Reference is the reference sport of the challenge, and Exercise the
	// exercise of its elimination by repetitions, empty without one.
	Reference string
	Exercise  string
	// Active is true when the author is an active participant: read from the
	// participation status, never the role (a creator who abandoned no
	// longer competes).
	Active bool
	// AbandonedAt is when the author left the challenge (abandoned or
	// excluded), nil while active. The weeks locked by then stay locked: the
	// points category keeps the author in them, and their points no longer
	// move (README §2.3).
	AbandonedAt *time.Time
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

// UnknownExerciseRefusal refuses repetitions of an exercise that none of
// challenges offers an activity of sport started at startedAt: challenges
// with an elimination by repetitions of that exercise, sport as their
// reference, where it counts (README §2.5); 422 unknown_exercise. An exercise
// of kept, the repetitions already on the activity a change replaces, is
// kept even when none offers it any more.
func UnknownExerciseRefusal(sport string, startedAt time.Time, reps, kept []Repetitions, challenges []Participation) error {
	offered := map[string]bool{}
	for _, r := range kept {
		offered[r.Exercise] = true
	}
	for _, c := range challenges {
		if c.Exercise != "" && c.Reference == sport && counts(c, startedAt) {
			offered[c.Exercise] = true
		}
	}
	for _, r := range reps {
		if !offered[r.Exercise] {
			return apperr.New(apperr.CodeUnknownExercise, nil)
		}
	}
	return nil
}

// IsLocked reports whether an activity started at startedAt is locked at now
// (from platform/clock, never the worker): its week is locked in at least
// one challenge where it counts (Contributions), from 7 am, local time of
// that challenge, on the first day of the next week (scoring.Window.WeekLocksAt),
// or in a challenge the author left once that week was locked (AbandonedAt,
// the bound scoring.Points keeps them in a week by). An activity across the
// end of a week belongs to the week it starts in.
func IsLocked(now, startedAt time.Time, challenges []Participation) bool {
	for _, c := range challenges {
		// A participation abandoned always has its instant (schema check).
		w := window(c)
		if (!c.Active && c.AbandonedAt == nil) || !w.Contains(startedAt) {
			continue
		}
		// A challenge whose weeks cannot be cut locks rather than lets an
		// activity move: the time zone is validated at creation.
		if c.Location == nil {
			return true
		}
		locksAt := w.WeekLocksAt(w.WeekOf(startedAt))
		switch {
		case c.Active && !now.Before(locksAt):
			return true
		case !c.Active && !c.AbandonedAt.Before(locksAt):
			// Locked by the time they left (never after now).
			return true
		}
	}
	return false
}

// Touched returns the ids of the challenges, in the order of challenges,
// whose rankings an activity of their author started at one of starts is
// part of: those where it counts (Contributions), and those the author left
// once its week was locked, where the points category and the elimination
// still judge them (README §2.3). An admin change of the activity, its start moving from the first
// of starts to the second maybe, recomputes each of them (README §2.12). A
// challenge left whose weeks cannot be cut is touched rather than missed.
func Touched(challenges []Participation, starts ...time.Time) []string {
	var out []string
	for _, c := range challenges {
		for _, at := range starts {
			if counts(c, at) || keptAfterLeaving(c, at) {
				out = append(out, c.ChallengeID)
				break
			}
		}
	}
	return out
}

// keptAfterLeaving reports whether the author left c once the week of an
// activity started at startedAt was locked: the points category and the
// elimination still judge them in that week (README §2.3).
func keptAfterLeaving(c Participation, startedAt time.Time) bool {
	w := window(c)
	if c.Active || c.AbandonedAt == nil || !w.Contains(startedAt) {
		return false
	}
	if c.Location == nil {
		return true
	}
	return !c.AbandonedAt.Before(w.WeekLocksAt(w.WeekOf(startedAt)))
}

// counts reports whether an activity started at startedAt counts in c.
func counts(c Participation, startedAt time.Time) bool {
	return c.Active && window(c).Contains(startedAt)
}

func window(c Participation) scoring.Window {
	return scoring.Window{Start: c.StartsAt, End: c.EndsAt, Location: c.Location}
}
