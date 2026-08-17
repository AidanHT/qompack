package canon_test

import (
	"testing"

	"github.com/qompack/qompack/internal/canon"
	"github.com/stretchr/testify/require"
)

// FuzzRestore asserts Restore never panics on adversarial input.
//
// Restore is the one function in this package that a corrupt on-disk side record reaches directly:
// SP-06 reads deltas back out of a stored object and replays them, so a truncated or garbled
// record arrives here as an arbitrary Delta list over an arbitrary buffer. A panic on that path
// costs the user their turn (§12.3), so the contract is "report or succeed, never crash" — and
// when it succeeds, the result must be at least as long as the canonical form, because every
// Delta replaces a token with an Original at least as long.
//
// The corpus encodes a Delta list positionally rather than as a struct, because go-fuzz can only
// mutate scalars: n triples of (offset, length, original) are drawn from the seeds and mutated
// independently, which reaches malformed lists far faster than a hand-written table.
func FuzzRestore(f *testing.F) {
	f.Add([]byte("hello <ts> world"), 1, 6, 4, "2024-01-15T10:32:07Z")
	f.Add([]byte(""), 0, 0, 0, "")
	f.Add([]byte("abc"), 3, 0, 1, "x")
	f.Add([]byte("<a><b>"), 2, 0, 3, "AAAA")
	f.Add([]byte("0123456789"), 2, 9, 5, "overrun")
	f.Add([]byte("0123456789"), 1, -1, -1, "negative")

	f.Fuzz(func(t *testing.T, canonical []byte, n, off, length int, original string) {
		deltas := buildDeltas(n, off, length, original, len(canonical))

		got, err := canon.Restore(canonical, deltas)
		if err != nil {
			require.Nil(t, got, "a failing Restore must not also return bytes")
			return
		}
		require.GreaterOrEqual(t, len(got), len(canonical)-totalDeltaLen(deltas),
			"a successful Restore must account for every byte it replaced")
	})
}

// maxFuzzDeltas bounds how many deltas one fuzz case builds, so a huge n cannot turn a fuzz
// iteration into an allocation benchmark.
const maxFuzzDeltas = 16

// buildDeltas derives an ascending-ish Delta list from four fuzzed scalars. It deliberately does
// NOT guarantee validity: producing lists that violate ordering and bounds is the point.
func buildDeltas(n, off, length int, original string, canonLen int) []canon.Delta {
	if n < 0 {
		n = -n
	}
	n %= maxFuzzDeltas + 1

	classes := canon.KnownClasses()
	deltas := make([]canon.Delta, 0, n)
	for i := 0; i < n; i++ {
		deltas = append(deltas, canon.Delta{
			Offset:   off + i*length,
			Len:      length,
			Original: original,
			Class:    classes[i%len(classes)],
		})
	}
	return deltas
}

// totalDeltaLen sums the token lengths a Delta list claims to have replaced.
func totalDeltaLen(deltas []canon.Delta) int {
	total := 0
	for _, d := range deltas {
		if d.Len > 0 {
			total += d.Len
		}
	}
	return total
}
