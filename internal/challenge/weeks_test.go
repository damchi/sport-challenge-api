package challenge

import (
	"testing"
	"time"
)

// A 3-week challenge in Toronto from 2026-03-02 (local midnight, 05:00 UTC):
// clocks go forward on 2026-03-08, inside week 1.
func torontoWindow(t *testing.T) (*time.Location, time.Time, time.Time) {
	t.Helper()
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 3, 2, 0, 0, 0, 0, loc)
	return loc, start, start.AddDate(0, 0, 21)
}

func TestRegistrationDeadline(t *testing.T) {
	loc, start, _ := torontoWindow(t)
	// End of week 1: local midnight 7 days later, 1 h less than 7 × 24 h.
	got := RegistrationDeadline(start.UTC(), loc)
	if want := time.Date(2026, 3, 9, 0, 0, 0, 0, loc); !got.Equal(want) {
		t.Fatalf("deadline %s, want %s", got, want)
	}
	if got.Sub(start) != 7*24*time.Hour-time.Hour {
		t.Fatalf("week 1 lasts %s, want 167h (local days)", got.Sub(start))
	}
}

func TestCurrentWeek(t *testing.T) {
	loc, start, end := torontoWindow(t)
	weekEnd := func(n int) time.Time { return time.Date(2026, 3, 2+7*n, 0, 0, 0, 0, loc) }
	tests := []struct {
		name     string
		now      time.Time
		wantWeek int // 0: outside the window
		wantEnd  time.Time
	}{
		{name: "before the start", now: start.Add(-time.Second)},
		{name: "first instant", now: start, wantWeek: 1, wantEnd: weekEnd(1)},
		{name: "last second of week 1", now: weekEnd(1).Add(-time.Second), wantWeek: 1, wantEnd: weekEnd(1)},
		{name: "first instant of week 2", now: weekEnd(1), wantWeek: 2, wantEnd: weekEnd(2)},
		{name: "last week", now: weekEnd(2).Add(time.Hour), wantWeek: 3, wantEnd: weekEnd(3)},
		{name: "last second", now: end.Add(-time.Second), wantWeek: 3, wantEnd: end},
		{name: "end (excluded)", now: end},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			week, weekEndsAt := CurrentWeek(tt.now.UTC(), start.UTC(), end.UTC(), loc)
			if week != tt.wantWeek || !weekEndsAt.Equal(tt.wantEnd) {
				t.Fatalf("week %d ending %s, want %d ending %s", week, weekEndsAt, tt.wantWeek, tt.wantEnd)
			}
		})
	}
}

func TestStatusAt(t *testing.T) {
	_, start, end := torontoWindow(t)
	tests := []struct {
		now  time.Time
		want string
	}{
		{start.Add(-time.Second), StatusUpcoming},
		{start, StatusOngoing},
		{end.Add(-time.Second), StatusOngoing},
		{end, StatusFinished},
	}
	for _, tt := range tests {
		if got := StatusAt(tt.now, start, end); got != tt.want {
			t.Errorf("StatusAt(%s) = %s, want %s", tt.now, got, tt.want)
		}
	}
}

// TestWeeksAcrossTimeChanges checks every week boundary of challenges in
// several zones: each week ends at local midnight 7 local days later, the
// last one exactly at ends_at, whatever the time changes.
func TestWeeksAcrossTimeChanges(t *testing.T) {
	tests := []struct {
		name  string
		zone  string
		start [3]int // year, month, day
		weeks int
	}{
		{name: "Toronto, fall back in week 2 (169 h)", zone: "America/Toronto", start: [3]int{2026, 10, 26}, weeks: 3},
		{name: "Toronto, spring forward in week 2", zone: "America/Toronto", start: [3]int{2026, 3, 1}, weeks: 2},
		{name: "Sydney, daylight saving ends in April", zone: "Australia/Sydney", start: [3]int{2026, 3, 30}, weeks: 2},
		{name: "UTC, 52 weeks", zone: "UTC", start: [3]int{2026, 1, 5}, weeks: 52},
		{name: "Toronto, 52 weeks across both changes", zone: "America/Toronto", start: [3]int{2026, 1, 5}, weeks: 52},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc, err := time.LoadLocation(tt.zone)
			if err != nil {
				t.Fatal(err)
			}
			start := time.Date(tt.start[0], time.Month(tt.start[1]), tt.start[2], 0, 0, 0, 0, loc)
			end := start.AddDate(0, 0, 7*tt.weeks)
			for n := 1; n <= tt.weeks; n++ {
				wantEnd := time.Date(tt.start[0], time.Month(tt.start[1]), tt.start[2]+7*n, 0, 0, 0, 0, loc)
				if h, m, s := wantEnd.Clock(); h != 0 || m != 0 || s != 0 {
					t.Fatalf("week %d ends at %s, not local midnight", n, wantEnd)
				}
				// Last second of week n, then the first instant of week n+1.
				week, ends := CurrentWeek(wantEnd.Add(-time.Second).UTC(), start.UTC(), end.UTC(), loc)
				if week != n || !ends.Equal(wantEnd) {
					t.Fatalf("last second of week %d: week %d ending %s, want %d ending %s", n, week, ends, n, wantEnd)
				}
				if n < tt.weeks {
					if week, _ := CurrentWeek(wantEnd.UTC(), start.UTC(), end.UTC(), loc); week != n+1 {
						t.Fatalf("first instant after week %d: week %d", n, week)
					}
				}
			}
			if week, ends := CurrentWeek(end.Add(-time.Second).UTC(), start.UTC(), end.UTC(), loc); week != tt.weeks || !ends.Equal(end) {
				t.Fatalf("last week %d ending %s, want %d ending at ends_at %s", week, ends, tt.weeks, end)
			}
		})
	}
}

func TestRegistrationDeadlineFallBack(t *testing.T) {
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	// Clocks go back on 2026-11-01, inside week 1: 169 hours.
	start := time.Date(2026, 10, 26, 0, 0, 0, 0, loc)
	got := RegistrationDeadline(start.UTC(), loc)
	if want := time.Date(2026, 11, 2, 0, 0, 0, 0, loc); !got.Equal(want) || got.Sub(start) != 169*time.Hour {
		t.Fatalf("deadline %s (%s after the start), want %s, 169h", got, got.Sub(start), want)
	}
}
