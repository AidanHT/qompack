package core

import "time"

// Clock is the seam that keeps time out of every algorithm. Every package that observes time
// takes a Clock; testutil.FakeClock implements it, and §6.1 bans wall-clock sleeps outright so
// that no test depends on real elapsed time.
type Clock interface {
	Now() time.Time
	Since(time.Time) time.Duration
}

type systemClock struct{}

func (systemClock) Now() time.Time                  { return time.Now() }
func (systemClock) Since(t time.Time) time.Duration { return time.Since(t) }

// SystemClock returns the real clock. It is the only production implementation.
func SystemClock() Clock { return systemClock{} }

// NowMilli reads c and truncates to milliseconds, the resolution every on-disk timestamp uses.
func NowMilli(c Clock) UnixMilli { return UnixMilli(c.Now().UnixMilli()) }

// Time converts back to a time.Time in UTC.
func (t UnixMilli) Time() time.Time { return time.UnixMilli(int64(t)).UTC() }
