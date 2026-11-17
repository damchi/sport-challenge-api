// Package scoring computes the rankings of a challenge from raw activities
// and the rules of its categories. It imports the standard library only: no
// database, no HTTP, no clock (the window carries the current instant).
package scoring

import "time"

// Window is the period of a challenge, [Start, End) in UTC, with the time
// zone that cuts it into weeks and the current instant (from the clock).
// Location must be set: it is the challenge's, validated at creation.
//
// The weeks are counted in local days of the time zone, never as fixed
// 7 × 24 h durations: a week across a time change lasts 167 or 169 hours.
// Each bound is the first instant of a local day (StartOfDay).
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

// WeekBounds returns week n as [start, end) in UTC. n starts at 1 and must
// stay within the window: outside, the bounds mean nothing.
func (w Window) WeekBounds(n int) (time.Time, time.Time) {
	// From the local date, not from the start instant: a challenge that starts
	// at 01:00 because midnight was skipped still has its next weeks at 00:00.
	y, m, d := w.Start.In(w.Location).Date()
	start := StartOfDay(y, m, d+7*(n-1), w.Location)
	end := StartOfDay(y, m, d+7*n, w.Location)
	// A window holds whole weeks, but its end stays the limit.
	if end.After(w.End) {
		end = w.End.UTC()
	}
	return start, end
}

// Weeks returns the number of weeks of the window.
func (w Window) Weeks() int {
	n := 0
	for {
		if start, _ := w.WeekBounds(n + 1); !start.Before(w.End) {
			return n
		}
		n++
	}
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

// EndedWeeks returns how many weeks have ended at Now: a week ends at its
// exact last instant, with no grace period. Weeks end in order, so these
// are weeks 1 to EndedWeeks.
func (w Window) EndedWeeks() int {
	ended := 0
	for n := 1; n <= w.Weeks(); n++ {
		if _, end := w.WeekBounds(n); w.Now.Before(end) {
			break
		}
		ended = n
	}
	return ended
}

// RegistrationDeadline is the end of week 1, when joining closes.
func (w Window) RegistrationDeadline() time.Time {
	_, end := w.WeekBounds(1)
	return end
}

// StartOfDay returns the first instant of the local day year-month-day in
// loc, in UTC. The date is normalized like time.Date (February 31 is March
// 3). It is local midnight, except where a time change skips midnight (Chile,
// Egypt, Lebanon...): the day then starts at the first instant that exists,
// the end of the skipped hour (README §2.1). time.Date picks either side of
// such a gap, sometimes 23:00 the day before. A day skipped as a whole (Apia,
// 2011-12-30) gives the start of the next day that exists.
func StartOfDay(year int, month time.Month, day int, loc *time.Location) time.Time {
	y, m, d := time.Date(year, month, day, 12, 0, 0, 0, time.UTC).Date()
	t := time.Date(y, m, d, 0, 0, 0, 0, loc)
	if ty, tm, td := t.Date(); ty != y || tm != m || td != d {
		// Midnight was skipped and t fell back on the day before: the day
		// starts where the zone in effect at t ends, at the time change.
		_, t = t.ZoneBounds()
	}
	return t.UTC()
}
