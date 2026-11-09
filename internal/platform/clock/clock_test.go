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
