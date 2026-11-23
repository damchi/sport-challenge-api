package activity

import (
	"errors"
	"testing"
	"time"
)

func zone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func TestWeekOf(t *testing.T) {
	toronto, tokyo := zone(t, "America/Toronto"), zone(t, "Asia/Tokyo")
	tests := []struct {
		name       string
		now        time.Time
		loc        *time.Location
		wantMonday time.Time
		wantStart  time.Time
		wantLength time.Duration
	}{
		{
			name: "wednesday", now: time.Date(2026, 11, 18, 15, 0, 0, 0, time.UTC), loc: toronto,
			wantMonday: date(2026, 11, 16), wantStart: time.Date(2026, 11, 16, 5, 0, 0, 0, time.UTC), wantLength: 168 * time.Hour,
		},
		{
			name: "sunday just before midnight", now: time.Date(2026, 11, 23, 4, 59, 59, 0, time.UTC), loc: toronto,
			wantMonday: date(2026, 11, 16), wantStart: time.Date(2026, 11, 16, 5, 0, 0, 0, time.UTC), wantLength: 168 * time.Hour,
		},
		{
			name: "monday at midnight", now: time.Date(2026, 11, 23, 5, 0, 0, 0, time.UTC), loc: toronto,
			wantMonday: date(2026, 11, 23), wantStart: time.Date(2026, 11, 23, 5, 0, 0, 0, time.UTC), wantLength: 168 * time.Hour,
		},
		{
			// The same instant is already Monday in Tokyo.
			name: "same instant in another zone", now: time.Date(2026, 11, 22, 20, 0, 0, 0, time.UTC), loc: tokyo,
			wantMonday: date(2026, 11, 23), wantStart: time.Date(2026, 11, 22, 15, 0, 0, 0, time.UTC), wantLength: 168 * time.Hour,
		},
		{
			// Clocks go forward on Sunday March 8, 2026.
			name: "spring forward", now: time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC), loc: toronto,
			wantMonday: date(2026, 3, 2), wantStart: time.Date(2026, 3, 2, 5, 0, 0, 0, time.UTC), wantLength: 167 * time.Hour,
		},
		{
			// Clocks go back on Sunday November 1, 2026.
			name: "fall back", now: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC), loc: toronto,
			wantMonday: date(2026, 10, 26), wantStart: time.Date(2026, 10, 26, 4, 0, 0, 0, time.UTC), wantLength: 169 * time.Hour,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := weekOf(tt.now, tt.loc)
			if !w.Monday.Equal(tt.wantMonday) || !w.Start().Equal(tt.wantStart) || w.End().Sub(w.Start()) != tt.wantLength {
				t.Fatalf("weekOf() = Monday %s, [%s, %s), want Monday %s, from %s, lasting %s",
					w.Monday, w.Start(), w.End(), tt.wantMonday, tt.wantStart, tt.wantLength)
			}
			if w.Start().After(tt.now) || !w.End().After(tt.now) {
				t.Fatalf("week [%s, %s) does not hold %s", w.Start(), w.End(), tt.now)
			}
			for i := range 7 {
				if !w.Bounds[i].Before(w.Bounds[i+1]) {
					t.Fatalf("day %d [%s, %s), want it to last", i, w.Bounds[i], w.Bounds[i+1])
				}
			}
		})
	}
}

// TestWeekStartingSkippedMidnight cuts the days where a time change skips
// midnight: the day starts at the first instant it has (scoring.StartOfDay).
func TestWeekStartingSkippedMidnight(t *testing.T) {
	// Chile moves from -04 to -03 at 00:00 on Sunday September 6, 2026.
	w := weekStarting(date(2026, 8, 31), zone(t, "America/Santiago"))
	if got, want := w.Bounds[6], time.Date(2026, 9, 6, 4, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("Sunday starts at %s, want %s (01:00 -03)", got, want)
	}
	if got := w.End().Sub(w.Start()); got != 167*time.Hour {
		t.Fatalf("week lasts %s, want 167h", got)
	}
	if got := w.Day(6); !got.Equal(date(2026, 9, 6)) {
		t.Fatalf("day 6 is %s, want September 6", got)
	}
}

func TestLoadZone(t *testing.T) {
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "America/Toronto", ok: true},
		{name: "UTC", ok: true},
		{name: ""},
		{name: "Local"},
		{name: "Mars/Olympus_Mons"},
		{name: "../etc/passwd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc, err := loadZone(tt.name)
			if tt.ok != (err == nil) {
				t.Fatalf("loadZone(%q) = %v, %v; want ok %v", tt.name, loc, err, tt.ok)
			}
			if err != nil && !errors.Is(err, errUnknownZone) {
				t.Fatalf("error %v, want errUnknownZone", err)
			}
		})
	}
}
