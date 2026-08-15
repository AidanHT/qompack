package obs

import "sync/atomic"

// Counter is a monotonically-adjustable integer instrument (loud.total, hits, evictions, …). Add
// may be called with a negative n; callers that want a strictly monotone counter simply never do.
type Counter interface {
	Add(n int64)
	Value() int64
}

// Gauge is a point-in-time integer instrument (queue depth, live sessions, …): it supports both an
// absolute Set and a relative Add.
type Gauge interface {
	Set(v int64)
	Add(d int64)
	Value() int64
}

type counter struct{ v int64 }

func newCounter() *counter { return &counter{} }

func (c *counter) Add(n int64)  { atomic.AddInt64(&c.v, n) }
func (c *counter) Value() int64 { return atomic.LoadInt64(&c.v) }

type gauge struct{ v int64 }

func newGauge() *gauge { return &gauge{} }

func (g *gauge) Set(v int64)  { atomic.StoreInt64(&g.v, v) }
func (g *gauge) Add(d int64)  { atomic.AddInt64(&g.v, d) }
func (g *gauge) Value() int64 { return atomic.LoadInt64(&g.v) }
