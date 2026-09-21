package store

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

// TestTruncateRunes_NeverExceedsItsBoundAndNeverSplitsARune.
//
// This shortens the snippets a search result carries, and both halves of its contract are the kind
// that fail silently. A cut inside a multi-byte rune produces invalid UTF-8 in a document every
// downstream reader parses as JSON, and a truncation that appended an ellipsis past the bound would
// return MORE bytes than the caller asked for — the one thing a truncation must never do, and the
// reason a bound too small to hold the ellipsis drops the ellipsis instead of overflowing.
func TestTruncateRunes_NeverExceedsItsBoundAndNeverSplitsARune(t *testing.T) {
	t.Parallel()

	require.Equal(t, "short", truncateRunes("short", 10), "a string inside the bound is untouched")
	require.Equal(t, "exactly", truncateRunes("exactly", len("exactly")))

	for _, max := range []int{0, 1, 2, 3, 4, 8, 16, 40} {
		got := truncateRunes(strings.Repeat("é", 40), max)
		require.LessOrEqual(t, len(got), max, "max=%d overflowed its bound", max)
		require.True(t, utf8.ValidString(got), "max=%d split a rune", max)
	}

	cut := truncateRunes(strings.Repeat("a", 40), 10)
	require.True(t, strings.HasSuffix(cut, previewEllipsis), "a cut string says it was cut")
	require.LessOrEqual(t, len(cut), 10)
}

// TestRuneSafeCut_LandsOnARuneBoundaryWhateverItIsAsked.
//
// It answers with an OFFSET, so every caller slices with whatever it returns; a bound larger than
// the string, or a non-positive one, has to come back as a usable offset rather than a panic on the
// caller's slice expression.
func TestRuneSafeCut_LandsOnARuneBoundaryWhateverItIsAsked(t *testing.T) {
	t.Parallel()

	s := strings.Repeat("é", 4) // 8 bytes, boundaries at 0, 2, 4, 6

	require.Equal(t, 0, runeSafeCut(s, 0))
	require.Equal(t, 0, runeSafeCut(s, -1), "a negative bound is no bound at all, not a panic")
	require.Equal(t, len(s), runeSafeCut(s, len(s)+10), "a bound past the end is the end")
	require.Equal(t, 0, runeSafeCut("", 5))

	for max := 0; max <= len(s); max++ {
		cut := runeSafeCut(s, max)
		require.LessOrEqual(t, cut, max)
		require.True(t, utf8.ValidString(s[:cut]), "max=%d cut inside a rune", max)
	}
}
