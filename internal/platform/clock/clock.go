// Package clock gives the current instant to the business code, which never
// calls time.Now() directly: tests use a FakeClock, and a local run an offset
// clock to see a week end in minutes.
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

// Offset is the system clock moved forward by a fixed duration, in UTC. Only
// a local run uses it, to test the end of the weeks without waiting for them
// (config refuses it outside ENV=local).
type Offset struct {
	offset time.Duration
}

// Now implements Clock.
func (o Offset) Now() time.Time { return time.Now().UTC().Add(o.offset) }

// New returns the clock of a process: the real one, or the real one moved
// forward by offset when it is not zero.
func New(offset time.Duration) Clock {
	if offset == 0 {
		return Real{}
	}
	return Offset{offset: offset}
}

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
