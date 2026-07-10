package engine

import "time"

// Clock is the engine's view of time. Retry backoff goes through it so tests
// can assert the delay sequence without waiting for it.
type Clock interface {
	Now() time.Time
	Sleep(d time.Duration)
}

type realClock struct{}

func (realClock) Now() time.Time        { return time.Now() }
func (realClock) Sleep(d time.Duration) { time.Sleep(d) }

// Default backoff, used when a workflow does not set one.
const (
	defaultBackoffBase   = time.Second
	defaultBackoffFactor = 2.0
	defaultBackoffMax    = 30 * time.Second
)

// backoffFor returns the delay before the retry that follows attempt n
// (zero-based), growing geometrically and capped.
func backoffFor(base time.Duration, factor float64, max time.Duration, attempt int) time.Duration {
	if base <= 0 {
		base = defaultBackoffBase
	}
	if factor <= 0 {
		factor = defaultBackoffFactor
	}
	if max <= 0 {
		max = defaultBackoffMax
	}
	d := float64(base)
	for i := 0; i < attempt; i++ {
		d *= factor
	}
	if time.Duration(d) > max {
		return max
	}
	return time.Duration(d)
}
