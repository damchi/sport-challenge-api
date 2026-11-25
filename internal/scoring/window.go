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
// Week n starts on the local date of the start + 7 × (n-1) days
// (WeekBounds), at the first instant of that day: an instant falls in the
// week of its local date, counted in days from the date of the start. It is
// computed from the dates, without cutting the weeks: the scorers call it
// for every group of activities.
func (w Window) WeekOf(t time.Time) int {
	if !w.Contains(t) {
		return 0
	}
	return daysBetween(w.Start.In(w.Location), t.In(w.Location))/7 + 1
}

// daysBetween is the number of calendar days from the date from shows to
// the date to shows, each in its own location.
func daysBetween(from, to time.Time) int {
	fy, fm, fd := from.Date()
	ty, tm, td := to.Date()
	d := time.Date(ty, tm, td, 0, 0, 0, 0, time.UTC).Sub(time.Date(fy, fm, fd, 0, 0, 0, 0, time.UTC))
	return int(d / (24 * time.Hour))
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

// lockHour is the local hour a week locks at, on the first day of the next
// week (README §2.3): the activities of the week can still be entered,
// changed and deleted until then, and it is judged then.
const lockHour = 7

// WeekLocksAt returns the instant week n locks, in UTC: 7 am, local time,
// on the day its end falls on (the first day of the next week; for the last
// week, the first day after the challenge). The only place of the rule.
// n must stay within the window.
func (w Window) WeekLocksAt(n int) time.Time {
	_, end := w.WeekBounds(n)
	y, m, d := end.In(w.Location).Date()
	return firstInstantAt(y, m, d, lockHour, w.Location)
}

// LockedWeeks returns how many weeks are locked at Now, so judged: a week
// is judged when it locks (WeekLocksAt), never before. Weeks lock in order,
// so these are weeks 1 to LockedWeeks.
func (w Window) LockedWeeks() int {
	locked, weeks := 0, w.Weeks()
	for n := 1; n <= weeks; n++ {
		if w.Now.Before(w.WeekLocksAt(n)) {
			break
		}
		locked = n
	}
	return locked
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
// the end of the skipped hour (README §2.1); where one repeats midnight, it
// is the first of the two. time.Date picks either side in both cases,
// sometimes 23:00 the day before. A day skipped as a whole (Apia,
// 2011-12-30) gives the start of the next day that exists.
func StartOfDay(year int, month time.Month, day int, loc *time.Location) time.Time {
	y, m, d := time.Date(year, month, day, 12, 0, 0, 0, time.UTC).Date()
	return firstInstantAt(y, m, d, 0, loc)
}

// firstInstantAt returns, in UTC, the first instant that shows hour:00 on
// the local day year-month-day in loc. Where a time change skips that hour,
// it is the first instant after the gap; where it repeats it, the first of
// the two (README §2.3). time.Date picks either side in both cases.
func firstInstantAt(year int, month time.Month, day, hour int, loc *time.Location) time.Time {
	want := time.Date(year, month, day, hour, 0, 0, 0, time.UTC)
	t := time.Date(year, month, day, hour, 0, 0, 0, loc)
	switch shown := wallClock(t); {
	case shown.Before(want):
		// In the gap, read before the change: the gap ends with the zone.
		_, t = t.ZoneBounds()
	case shown.After(want):
		// In the gap, read after the change: the gap ends where it starts.
		t, _ = t.ZoneBounds()
	default:
		// Repeated, the zone before the change may show it first.
		if start, _ := t.ZoneBounds(); !start.IsZero() {
			_, offset := start.Add(-time.Nanosecond).Zone()
			first := want.Add(-time.Duration(offset) * time.Second)
			if first.Before(start) && wallClock(first.In(loc)).Equal(want) {
				t = first
			}
		}
	}
	return t.UTC()
}

// wallClock is the local date and time t shows, as if it were UTC.
func wallClock(t time.Time) time.Time {
	y, m, d := t.Date()
	h, mi, sec := t.Clock()
	return time.Date(y, m, d, h, mi, sec, t.Nanosecond(), time.UTC)
}
