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

// window is a challenge of weeks weeks from local midnight of day in zone,
// its bounds in UTC as they are stored.
func window(t *testing.T, zone string, day [3]int, weeks int) Window {
	t.Helper()
	loc := load(t, zone)
	start := time.Date(day[0], time.Month(day[1]), day[2], 0, 0, 0, 0, loc)
	return Window{Start: start.UTC(), End: start.AddDate(0, 0, 7*weeks).UTC(), Location: loc}
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := window(t, tt.zone, tt.start, tt.weeks)
			if got := w.Weeks(); got != tt.weeks {
				t.Fatalf("Weeks() = %d, want %d", got, tt.weeks)
			}
			for n := 1; n <= tt.weeks; n++ {
				// Local midnight, built from the calendar, not from durations.
				wantStart := time.Date(tt.start[0], time.Month(tt.start[1]), tt.start[2]+7*(n-1), 0, 0, 0, 0, w.Location)
				wantEnd := time.Date(tt.start[0], time.Month(tt.start[1]), tt.start[2]+7*n, 0, 0, 0, 0, w.Location)
				start, end := w.WeekBounds(n)
				if !start.Equal(wantStart) || !end.Equal(wantEnd) {
					t.Fatalf("week %d: [%s, %s), want [%s, %s)", n, start, end, wantStart, wantEnd)
				}
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
	if got := w.Weeks(); got != 2 {
		t.Fatalf("Weeks() = %d, want 2", got)
	}
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
