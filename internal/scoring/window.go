package scoring

import "time"

// Window is the period of a challenge, [Start, End) in UTC, with the time
// zone that cuts it into weeks and the current instant (from the clock).
// Location must be set: it is the challenge's, validated at creation.
//
// The weeks are counted in local days of the time zone (AddDate), never as
// fixed 7 × 24 h durations: a week across a time change lasts 167 or 169
// hours.
type Window struct {
	Start    time.Time
	End      time.Time
	Location *time.Location
	Now      time.Time
}

// Contains reports whether an activity started at t counts in the window.
func (w Window) Contains(t time.Time) bool {
	return !t.Before(w.Start) && t.Before(w.End)
}

// Weeks returns the number of weeks of the window.
func (w Window) Weeks() int {
	n := 0
	for end := w.Start; end.Before(w.End); n++ {
		_, end = w.WeekBounds(n + 1)
	}
	return n
}

// WeekBounds returns week n as [start, end) in UTC. n must be from 1 to
// Weeks(): outside, the bounds mean nothing.
func (w Window) WeekBounds(n int) (time.Time, time.Time) {
	local := w.Start.In(w.Location)
	return local.AddDate(0, 0, 7*(n-1)).UTC(), local.AddDate(0, 0, 7*n).UTC()
}

// WeekOf returns the number of the week holding t, or 0 outside the window.
func (w Window) WeekOf(t time.Time) int {
	if !w.Contains(t) {
		return 0
	}
	for n := 1; ; n++ {
		if _, end := w.WeekBounds(n); t.Before(end) {
			return n
		}
	}
}

// CurrentWeek returns the number of the week holding Now and the instant it
// ends, or 0 and the zero time outside the window.
func (w Window) CurrentWeek() (int, time.Time) {
	n := w.WeekOf(w.Now)
	if n == 0 {
		return 0, time.Time{}
	}
	_, end := w.WeekBounds(n)
	return n, end
}

// RegistrationDeadline is the end of week 1, when joining closes.
func (w Window) RegistrationDeadline() time.Time {
	_, end := w.WeekBounds(1)
	return end
}
