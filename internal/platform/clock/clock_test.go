package clock

import (
	"sync"
	"testing"
	"time"
)

func TestRealIsUTC(t *testing.T) {
	if loc := (Real{}).Now().Location(); loc != time.UTC {
		t.Fatalf("Real.Now() in %s, want UTC", loc)
	}
}

func TestFakeClock(t *testing.T) {
	start := time.Date(2026, 3, 4, 15, 0, 0, 0, time.UTC)
	c := NewFake(start)
	if got := c.Now(); !got.Equal(start) {
		t.Fatalf("Now() = %s, want %s", got, start)
	}
	c.Advance(90 * time.Minute)
	if got, want := c.Now(), start.Add(90*time.Minute); !got.Equal(want) {
		t.Fatalf("after Advance, Now() = %s, want %s", got, want)
	}
	later := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	c.Set(later)
	if got := c.Now(); !got.Equal(later) {
		t.Fatalf("after Set, Now() = %s, want %s", got, later)
	}
}

func TestFakeClockIsSafeForConcurrentUse(t *testing.T) {
	c := NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			c.Advance(time.Second)
			_ = c.Now()
		})
	}
	wg.Wait()
	if got, want := c.Now(), time.Date(2026, 1, 1, 0, 0, 50, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("Now() = %s, want %s", got, want)
	}
}

func TestNewWithoutOffsetIsTheRealClock(t *testing.T) {
	if _, ok := New(0).(Real); !ok {
		t.Fatalf("New(0) = %T, want Real", New(0))
	}
}

func TestOffsetIsAheadOfTheRealClock(t *testing.T) {
	const offset = 7 * 24 * time.Hour
	c := New(offset)

	before := time.Now()
	got := c.Now()
	after := time.Now()

	if got.Location() != time.UTC {
		t.Fatalf("Now() in %s, want UTC", got.Location())
	}
	if got.Before(before.Add(offset)) || got.After(after.Add(offset)) {
		t.Fatalf("Now() = %s, want between %s and %s", got, before.Add(offset), after.Add(offset))
	}
}
