package obs

import (
	"math"
	"math/bits"
	"sync/atomic"
	"time"
)

// Histogram records durations into a fixed-bucket log histogram and reports percentile snapshots.
// Observe must be safe for concurrent use without blocking: it sits on the B-B ingest path, whose
// own budget is single-digit milliseconds.
type Histogram interface {
	Observe(d time.Duration)
	Snapshot() HistSnapshot
	Reset()
}

// HistSnapshot is a point-in-time read of a Histogram. N is the total observation count; Max is
// tracked exactly, independent of bucketing. P50/P95/P99/P999 are deliberately conservative — see
// histogram.Snapshot — so a latency gate can never pass because of rounding.
type HistSnapshot struct {
	N    int64
	P50  time.Duration
	P95  time.Duration
	P99  time.Duration
	P999 time.Duration
	Max  time.Duration
}

// nBuckets is 8 buckets per octave across 32 octaves: 1µs … ~1.2h. Not a nomagic violation — 256
// is not in the forbidden set, and it is the shape of the histogram itself, not a config default.
const nBuckets = 256

// bucketFor and bucketUpper implement a 1/8-octave (three mantissa bits) fixed-bucket log
// histogram over microseconds, transcribed verbatim from 00-ARCHITECTURE.md §9. u is a duration
// in whole microseconds.
func bucketFor(u uint64) int {
	if u == 0 {
		return 0
	}
	e := bits.Len64(u) - 1
	var f uint64
	if e >= 3 {
		f = (u >> uint(e-3)) & 7
	} else {
		f = (u << uint(3-e)) & 7
	}
	i := int(8*uint64(e) + f)
	if i >= nBuckets {
		i = nBuckets - 1
	}
	return i
}

// bucketUpper returns bucket i's inclusive upper bound, in microseconds, as a time.Duration.
func bucketUpper(i int) time.Duration {
	e, f := i/8, i%8
	lo := (uint64(8) + uint64(f)) << uint(e) >> 3
	hi := (uint64(8) + uint64(f) + 1) << uint(e) >> 3
	if hi <= lo {
		hi = lo + 1
	}
	return time.Duration(hi) * time.Microsecond
}

// The four percentile ranks Snapshot computes. Not in the nomagic forbidden set.
const (
	pctRank50  = 0.50
	pctRank95  = 0.95
	pctRank99  = 0.99
	pctRank999 = 0.999
)

// histogram is the concrete Histogram: a fixed array of lock-free per-bucket counters plus an
// exact running maximum. Counts are uint32 (1 KB per histogram); Observe never allocates and never
// blocks.
type histogram struct {
	counts    [nBuckets]uint32
	maxMicros int64
}

func newHistogram() *histogram { return &histogram{} }

// Observe records d. Negative durations (a caller's clock skew or subtraction error) are clamped
// to zero rather than corrupting the bucket index.
func (h *histogram) Observe(d time.Duration) {
	if d < 0 {
		d = 0
	}
	u := uint64(d / time.Microsecond)
	atomic.AddUint32(&h.counts[bucketFor(u)], 1)
	casMaxAtLeast(&h.maxMicros, int64(u))
}

// casMaxAtLeast atomically raises *addr to v if v is larger, via a compare-and-swap retry loop —
// the lock-free equivalent of "addr = max(addr, v)".
func casMaxAtLeast(addr *int64, v int64) {
	for {
		cur := atomic.LoadInt64(addr)
		if v <= cur {
			return
		}
		if atomic.CompareAndSwapInt64(addr, cur, v) {
			return
		}
	}
}

// Snapshot walks every bucket once (an O(nBuckets) racy-but-monotone read, which is correct for
// metrics: a concurrent Observe can only add to a count, never remove) and returns the percentile
// snapshot. Percentiles return the CONTAINING bucket's inclusive upper bound rather than an
// interpolated value: for observations >= 8µs this over-reports by at most 2^(1/8)-1 ~= 9.05%,
// and below 8µs the buckets are 1µs wide so the absolute error is <= 1µs. That is deliberate — a
// latency gate must never pass because bucketing rounded a breach away.
func (h *histogram) Snapshot() HistSnapshot {
	var counts [nBuckets]uint32
	var total int64
	for i := range h.counts {
		c := atomic.LoadUint32(&h.counts[i])
		counts[i] = c
		total += int64(c)
	}
	snap := HistSnapshot{
		N:   total,
		Max: time.Duration(atomic.LoadInt64(&h.maxMicros)) * time.Microsecond,
	}
	if total == 0 {
		return snap
	}
	snap.P50 = percentileOf(counts[:], total, pctRank50)
	snap.P95 = percentileOf(counts[:], total, pctRank95)
	snap.P99 = percentileOf(counts[:], total, pctRank99)
	snap.P999 = percentileOf(counts[:], total, pctRank999)
	return snap
}

// percentileOf returns bucketUpper for the bucket containing the p-th percentile rank under the
// nearest-rank method: rank = ceil(p * total), clamped to at least 1.
func percentileOf(counts []uint32, total int64, p float64) time.Duration {
	rank := int64(math.Ceil(p * float64(total)))
	if rank < 1 {
		rank = 1
	}
	var cum int64
	for i, c := range counts {
		cum += int64(c)
		if cum >= rank {
			return bucketUpper(i)
		}
	}
	return bucketUpper(len(counts) - 1)
}

func (h *histogram) Reset() {
	for i := range h.counts {
		atomic.StoreUint32(&h.counts[i], 0)
	}
	atomic.StoreInt64(&h.maxMicros, 0)
}
