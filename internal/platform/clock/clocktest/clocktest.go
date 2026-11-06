// Package clocktest provides a controllable clock for tests in any package.
package clocktest

import (
	"sync"
	"time"
)

// Fake is a clock.Clock whose time only changes when the test says so. It is safe for
// concurrent use and always returns UTC.
type Fake struct {
	mu  sync.Mutex
	now time.Time
}

// New returns a Fake set to t.
func New(t time.Time) *Fake {
	return &Fake{now: t.UTC()}
}

// Now returns the current fake time.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Set moves the clock to t.
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = t.UTC()
}

// Advance moves the clock forward by d (backward if d is negative).
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}
