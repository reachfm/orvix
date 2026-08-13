package kernel

import "time"

// Clock abstracts time.Now so service-layer state-transition logic
// (trial expiry, suspension timers, retry backoff, purge scheduling) is
// deterministically testable. Every platform service that reasons about
// time takes a Clock, never calling time.Now() directly.
type Clock interface {
	Now() time.Time
}

// SystemClock is the real clock, always returning UTC — every timestamp
// this kernel produces or persists is UTC, per the cross-cutting
// "consistent UTC timestamps" requirement.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

// FixedClock is a test double that always returns the same instant,
// advanceable by tests that need to simulate elapsed time deterministically.
type FixedClock struct {
	t time.Time
}

func NewFixedClock(t time.Time) *FixedClock { return &FixedClock{t: t.UTC()} }

func (c *FixedClock) Now() time.Time { return c.t }

func (c *FixedClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

func (c *FixedClock) Set(t time.Time) { c.t = t.UTC() }
