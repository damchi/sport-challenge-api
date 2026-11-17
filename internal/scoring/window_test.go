package scoring

import (
	"testing"
	"time"
)

func load(t *testing.T, zone string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

// window is a challenge of weeks weeks from the start of day in zone, its
// bounds in UTC as they are stored.
func window(t *testing.T, zone string, day [3]int, weeks int) Window {
	t.Helper()
	loc := load(t, zone)
	start := StartOfDay(day[0], time.Month(day[1]), day[2], loc)
	end := StartOfDay(day[0], time.Month(day[1]), day[2]+7*weeks, loc)
	return Window{Start: start, End: end, Location: loc}
}

// checkStartOfDay fails unless got is the first instant of the local day
// year-month-day (normalized like time.Date) in loc: it falls on that day,
// and the second before falls on the day before. These hold whatever the
// time zone database says about time changes.
func checkStartOfDay(t *testing.T, got time.Time, year int, month time.Month, day int, loc *time.Location) {
	t.Helper()
	want := time.Date(year, month, day, 12, 0, 0, 0, time.UTC)
	if y, m, d := got.In(loc).Date(); y != want.Year() || m != want.Month() || d != want.Day() {
		t.Fatalf("%s falls on %d-%02d-%02d, want %s", got.In(loc), y, m, d, want.Format(time.DateOnly))
	}
	before := want.AddDate(0, 0, -1)
	if y, m, d := got.Add(-time.Second).In(loc).Date(); y != before.Year() || m != before.Month() || d != before.Day() {
		t.Fatalf("the second before %s falls on %d-%02d-%02d, want %s", got.In(loc), y, m, d, before.Format(time.DateOnly))
	}
}

// TestStartOfDay checks the first instant of local days, with and without a
// time change, in zones that change at midnight (the day then starts at the
// end of the skipped hour) and at 02:00 (midnight exists).
func TestStartOfDay(t *testing.T) {
	tests := []struct {
		name  string
		zone  string
		day   [3]int
		local string // the start in local time, when the zone database is known
	}{
		{name: "Santiago, spring forward at midnight", zone: "America/Santiago", day: [3]int{2027, 9, 5}},
		{name: "Havana, spring forward", zone: "America/Havana", day: [3]int{2027, 3, 14}},
		{name: "Cairo, spring forward at midnight", zone: "Africa/Cairo", day: [3]int{2027, 4, 30}},
		{name: "Beirut, spring forward at midnight", zone: "Asia/Beirut", day: [3]int{2027, 3, 28}},
		{name: "Toronto, spring forward at 02:00: midnight exists", zone: "America/Toronto", day: [3]int{2027, 3, 14}, local: "00:00"},
		{name: "Toronto, fall back at 02:00", zone: "America/Toronto", day: [3]int{2026, 11, 1}, local: "00:00"},
		{name: "Havana, fall back at midnight: the first 00:00", zone: "America/Havana", day: [3]int{2027, 11, 7}, local: "00:00"},
		{name: "Havana, fall back at midnight in 2026", zone: "America/Havana", day: [3]int{2026, 11, 1}, local: "00:00"},
		{name: "UTC", zone: "UTC", day: [3]int{2027, 1, 1}, local: "00:00"},
		{name: "day 31 of February is March 3", zone: "UTC", day: [3]int{2027, 2, 31}, local: "00:00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc := load(t, tt.zone)
			got := StartOfDay(tt.day[0], time.Month(tt.day[1]), tt.day[2], loc)
			if got.Location() != time.UTC {
				t.Fatalf("%s not in UTC", got)
			}
			checkStartOfDay(t, got, tt.day[0], time.Month(tt.day[1]), tt.day[2], loc)
			if tt.local != "" {
				if l := got.In(loc).Format("15:04"); l != tt.local {
					t.Fatalf("starts at %s local, want %s", l, tt.local)
				}
			}
		})
	}
}

// TestStartOfDaySkippedMidnight pins the bug of the audit (SC-API-079): where
// midnight does not exist, time.Date gives 23:00 the day before, and the day
// must start at 01:00 instead. Santiago skips midnight on the first Sunday of
// September. If the zone database ever stops doing so, this test fails rather
// than skip (the other tests built on this day skip, and the property tests
// pass either way): pick another day that skips midnight.
func TestStartOfDaySkippedMidnight(t *testing.T) {
	loc := load(t, "America/Santiago")
	if d := time.Date(2027, 9, 5, 0, 0, 0, 0, loc).Day(); d == 5 {
		t.Fatal("this zone database no longer skips midnight on 2027-09-05 in Santiago: pin another such day")
	}
	got := StartOfDay(2027, 9, 5, loc)
	if l := got.In(loc).Format("2006-01-02 15:04"); l != "2027-09-05 01:00" {
		t.Fatalf("starts at %s local, want 2027-09-05 01:00", l)
	}
}

func TestContains(t *testing.T) {
	w := window(t, "America/Toronto", [3]int{2026, 3, 2}, 2)
	tests := []struct {
		name string
		at   time.Time
		want bool
	}{
		{name: "last second before the start", at: w.Start.Add(-time.Second)},
		{name: "start (included)", at: w.Start, want: true},
		{name: "last second", at: w.End.Add(-time.Second), want: true},
		{name: "end (excluded)", at: w.End},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := w.Contains(tt.at); got != tt.want {
				t.Fatalf("Contains(%s) = %v, want %v", tt.at, got, tt.want)
			}
		})
	}
}

// TestWeeksAcrossTimeChanges checks every week of challenges in several
// zones: week n starts at local midnight 7 × (n-1) local days after the
// start and ends 7 local days later, the last one exactly at the end,
// whatever the time changes; its first and last seconds belong to it.
func TestWeeksAcrossTimeChanges(t *testing.T) {
	tests := []struct {
		name  string
		zone  string
		start [3]int // year, month, day
		weeks int
	}{
		{name: "Toronto, 2 weeks, spring forward on March 8 in week 1", zone: "America/Toronto", start: [3]int{2026, 3, 2}, weeks: 2},
		{name: "Toronto, 2 weeks, spring forward on March 8 in week 2", zone: "America/Toronto", start: [3]int{2026, 3, 1}, weeks: 2},
		{name: "Toronto, 3 weeks, fall back on November 1 in week 1", zone: "America/Toronto", start: [3]int{2026, 10, 26}, weeks: 3},
		{name: "Toronto, 52 weeks across both changes", zone: "America/Toronto", start: [3]int{2026, 1, 5}, weeks: 52},
		{name: "Sydney, daylight saving ends in April", zone: "Australia/Sydney", start: [3]int{2026, 3, 30}, weeks: 2},
		{name: "UTC, 52 weeks", zone: "UTC", start: [3]int{2026, 1, 5}, weeks: 52},
		{name: "Santiago, midnight skipped at the start of week 2", zone: "America/Santiago", start: [3]int{2027, 8, 29}, weeks: 3},
		{name: "Santiago, starts on the day midnight is skipped", zone: "America/Santiago", start: [3]int{2027, 9, 5}, weeks: 3},
		{name: "Havana, 52 weeks", zone: "America/Havana", start: [3]int{2027, 1, 3}, weeks: 52},
		{name: "Cairo, 52 weeks", zone: "Africa/Cairo", start: [3]int{2027, 1, 1}, weeks: 52},
		{name: "Beirut, 52 weeks", zone: "Asia/Beirut", start: [3]int{2027, 1, 3}, weeks: 52},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := window(t, tt.zone, tt.start, tt.weeks)
			if got := w.Weeks(); got != tt.weeks {
				t.Fatalf("Weeks() = %d, want %d", got, tt.weeks)
			}
			for n := 1; n <= tt.weeks; n++ {
				// The first instant of local days, built from the calendar, not
				// from durations.
				start, end := w.WeekBounds(n)
				checkStartOfDay(t, start, tt.start[0], time.Month(tt.start[1]), tt.start[2]+7*(n-1), w.Location)
				checkStartOfDay(t, end, tt.start[0], time.Month(tt.start[1]), tt.start[2]+7*n, w.Location)
				if start.Location() != time.UTC || end.Location() != time.UTC {
					t.Fatalf("week %d: bounds not in UTC", n)
				}
				if got := w.WeekOf(start); got != n {
					t.Fatalf("first second of week %d: WeekOf = %d", n, got)
				}
				if got := w.WeekOf(end.Add(-time.Second)); got != n {
					t.Fatalf("last second of week %d: WeekOf = %d", n, got)
				}
			}
			if _, end := w.WeekBounds(tt.weeks); !end.Equal(w.End) {
				t.Fatalf("last week ends at %s, want the end %s", end, w.End)
			}
			if got := w.WeekOf(w.Start.Add(-time.Second)); got != 0 {
				t.Fatalf("before the start: WeekOf = %d, want 0", got)
			}
			if got := w.WeekOf(w.End); got != 0 {
				t.Fatalf("at the end: WeekOf = %d, want 0", got)
			}
		})
	}
}

// TestWeekLengthsAtTimeChanges checks that a week holding a time change is
// 7 local days, not 7 × 24 h.
func TestWeekLengthsAtTimeChanges(t *testing.T) {
	tests := []struct {
		name  string
		start [3]int
		want  time.Duration
	}{
		{name: "spring forward on 2026-03-08", start: [3]int{2026, 3, 2}, want: 167 * time.Hour},
		{name: "fall back on 2026-11-01", start: [3]int{2026, 10, 26}, want: 169 * time.Hour},
		{name: "no change", start: [3]int{2026, 6, 1}, want: 168 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := window(t, "America/Toronto", tt.start, 2)
			start, end := w.WeekBounds(1)
			if got := end.Sub(start); got != tt.want {
				t.Fatalf("week 1 lasts %s, want %s", got, tt.want)
			}
		})
	}
}

// TestWindowEndIsTheLimit checks a window that does not hold whole weeks:
// the creation rules forbid it, but a stored row must never give a week
// ending after the challenge.
func TestWindowEndIsTheLimit(t *testing.T) {
	w := window(t, "America/Toronto", [3]int{2026, 3, 2}, 2)
	w.End = w.End.Add(-24 * time.Hour)
	if _, end := w.WeekBounds(2); !end.Equal(w.End) {
		t.Fatalf("last week ends at %s, want the end %s", end, w.End)
	}
	w.Now = w.End.Add(-time.Second)
	if week, ends := w.CurrentWeek(); week != 2 || !ends.Equal(w.End) {
		t.Fatalf("last second: week %d ending %s, want 2 ending %s", week, ends, w.End)
	}
}

func TestRegistrationDeadline(t *testing.T) {
	tests := []struct {
		name  string
		start [3]int
		want  [3]int
	}{
		{name: "spring forward in week 1 (167 h)", start: [3]int{2026, 3, 2}, want: [3]int{2026, 3, 9}},
		{name: "fall back in week 1 (169 h)", start: [3]int{2026, 10, 26}, want: [3]int{2026, 11, 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := window(t, "America/Toronto", tt.start, 3)
			want := time.Date(tt.want[0], time.Month(tt.want[1]), tt.want[2], 0, 0, 0, 0, w.Location)
			if got := w.RegistrationDeadline(); !got.Equal(want) {
				t.Fatalf("deadline %s, want %s", got, want)
			}
		})
	}
}

func TestCurrentWeek(t *testing.T) {
	// 3 weeks in Toronto from 2026-03-02: clocks go forward in week 1.
	w := window(t, "America/Toronto", [3]int{2026, 3, 2}, 3)
	weekEnd := func(n int) time.Time { return time.Date(2026, 3, 2+7*n, 0, 0, 0, 0, w.Location) }
	tests := []struct {
		name     string
		now      time.Time
		wantWeek int // 0: outside the window
		wantEnd  time.Time
	}{
		{name: "before the start", now: w.Start.Add(-time.Second)},
		{name: "first instant", now: w.Start, wantWeek: 1, wantEnd: weekEnd(1)},
		{name: "last second of week 1", now: weekEnd(1).Add(-time.Second), wantWeek: 1, wantEnd: weekEnd(1)},
		{name: "first instant of week 2", now: weekEnd(1), wantWeek: 2, wantEnd: weekEnd(2)},
		{name: "last week", now: weekEnd(2).Add(time.Hour), wantWeek: 3, wantEnd: weekEnd(3)},
		{name: "last second", now: w.End.Add(-time.Second), wantWeek: 3, wantEnd: w.End},
		{name: "end (excluded)", now: w.End},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := w
			w.Now = tt.now.UTC()
			week, ends := w.CurrentWeek()
			if week != tt.wantWeek || !ends.Equal(tt.wantEnd) {
				t.Fatalf("week %d ending %s, want %d ending %s", week, ends, tt.wantWeek, tt.wantEnd)
			}
		})
	}
}

func TestWeeks(t *testing.T) {
	tests := []struct {
		name  string
		zone  string
		start [3]int
		weeks int
	}{
		{name: "2 weeks", zone: "America/Toronto", start: [3]int{2026, 3, 2}, weeks: 2},
		{name: "3 weeks across the fall back of November 1", zone: "America/Toronto", start: [3]int{2026, 10, 26}, weeks: 3},
		{name: "52 weeks", zone: "America/Toronto", start: [3]int{2026, 3, 2}, weeks: 52},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := window(t, tt.zone, tt.start, tt.weeks).Weeks(); got != tt.weeks {
				t.Fatalf("Weeks() = %d, want %d", got, tt.weeks)
			}
		})
	}
}

func TestEndedWeeks(t *testing.T) {
	// 3 weeks across the time change of March 8: week 1 lasts 168 h, week 2 167 h.
	w := window(t, "America/Toronto", [3]int{2026, 3, 2}, 3)
	_, end1 := w.WeekBounds(1)
	_, end2 := w.WeekBounds(2)
	tests := []struct {
		name string
		now  time.Time
		want int
	}{
		{name: "before the start", now: w.Start.Add(-time.Hour)},
		{name: "at the start", now: w.Start},
		{name: "last instant of week 1", now: end1.Add(-time.Nanosecond)},
		{name: "exact end of week 1, no grace", now: end1, want: 1},
		{name: "an hour after the end of week 1", now: end1.Add(time.Hour), want: 1},
		{name: "last instant of week 2", now: end2.Add(-time.Nanosecond), want: 1},
		{name: "exact end of week 2", now: end2, want: 2},
		{name: "last instant of the challenge", now: w.End.Add(-time.Nanosecond), want: 2},
		{name: "end of the challenge", now: w.End, want: 3},
		{name: "long after the end", now: w.End.AddDate(1, 0, 0), want: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := w
			w.Now = tt.now
			if got := w.EndedWeeks(); got != tt.want {
				t.Fatalf("EndedWeeks() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestStartOfDayOfASkippedDay checks a day skipped as a whole: Samoa jumped
// from 2011-12-29 to 2011-12-31. Its start is the start of the next day that
// exists.
func TestStartOfDayOfASkippedDay(t *testing.T) {
	loc := load(t, "Pacific/Apia")
	got := StartOfDay(2011, 12, 30, loc)
	if l := got.In(loc).Format("2006-01-02 15:04"); l != "2011-12-31 00:00" {
		t.Fatalf("starts at %s local, want 2011-12-31 00:00", l)
	}
}
