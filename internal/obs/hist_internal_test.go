package obs

import (
	"math"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// TestHistogram_BucketMonotone asserts bucketFor is non-decreasing in its input across the entire
// uint64 domain: clamping beyond the last bucket preserves monotonicity because everything past
// the threshold maps to the same maximal index.
func TestHistogram_BucketMonotone(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		a := rapid.Uint64Range(0, math.MaxUint64).Draw(rt, "a")
		b := rapid.Uint64Range(0, math.MaxUint64).Draw(rt, "b")
		if a > b {
			a, b = b, a
		}
		if bucketFor(a) > bucketFor(b) {
			rt.Fatalf("bucketFor not monotone: bucketFor(%d)=%d > bucketFor(%d)=%d",
				a, bucketFor(a), b, bucketFor(b))
		}
	})
}

// TestBucketUpper_ContainsInput asserts bucketUpper(bucketFor(u)) >= u for every u the histogram
// can actually represent (up to the last bucket's own upper bound, derived from bucketUpper
// itself rather than hardcoded, so the test stays correct if the bucket math ever changes).
// Beyond that ceiling, u saturates into the last bucket by construction and this property is not
// claimed to hold — real callers never observe latencies anywhere near the ~1.2h ceiling.
func TestBucketUpper_ContainsInput(t *testing.T) {
	maxMicros := uint64(bucketUpper(nBuckets-1) / time.Microsecond)
	rapid.Check(t, func(rt *rapid.T) {
		u := rapid.Uint64Range(0, maxMicros).Draw(rt, "u")
		upperMicros := uint64(bucketUpper(bucketFor(u)) / time.Microsecond)
		if upperMicros < u {
			rt.Fatalf("bucketUpper(bucketFor(%d)) = %dus, want >= %dus", u, upperMicros, u)
		}
	})
}

// TestBucketFor_Zero pins the boundary case bucketFor documents explicitly: a zero duration must
// land in bucket 0, not underflow bits.Len64(0)-1 = -1.
func TestBucketFor_Zero(t *testing.T) {
	if got := bucketFor(0); got != 0 {
		t.Fatalf("bucketFor(0) = %d, want 0", got)
	}
}

// TestPercentileOfSnapshot_DefaultsToP99 exercises percentileOfSnapshot's defensive default
// branch: every Budget in this package uses pct 95 or 99, so no production call site reaches it,
// but the fallback itself must still behave as documented.
func TestPercentileOfSnapshot_DefaultsToP99(t *testing.T) {
	snap := HistSnapshot{P99: 42 * time.Millisecond}
	if got := percentileOfSnapshot(snap, 50); got != snap.P99 {
		t.Fatalf("percentileOfSnapshot(_, 50) = %v, want P99 %v", got, snap.P99)
	}
}
