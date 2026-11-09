// Package clock gives the current instant to the business code, which never
// calls time.Now() directly: tests use a FakeClock, and staging an offset
// clock (ticket SC-API-073).
package clock

import (
	"sync"
	"time"
)

// Clock returns the current instant.
type Clock interface {
	Now() time.Time
}

// Real is the system clock, in UTC.
type Real struct{}

// Now implements Clock.
func (Real) Now() time.Time { return time.Now().UTC() }

// FakeClock is a clock set by hand, safe for concurrent use.
type FakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// NewFake returns a FakeClock at now.
func NewFake(now time.Time) *FakeClock {
	return &FakeClock{now: now}
}

// Now implements Clock.
func (f *FakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Set moves the clock to now.
func (f *FakeClock) Set(now time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = now
}

// Advance moves the clock forward by d.
func (f *FakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}
