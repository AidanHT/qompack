package sketch_test

import (
	"testing"

	"github.com/qompack/qompack/internal/sketch"
	"github.com/stretchr/testify/require"
)

// TestHeaderMagic pins sketch.HeaderMagic to the exact 4-byte "QPKS" wire-format prefix
// 00-ARCHITECTURE.md §5.7 specifies. SP-03's real Save/Load must stamp and verify exactly this
// value.
func TestHeaderMagic(t *testing.T) {
	require.Equal(t, [4]byte{'Q', 'P', 'K', 'S'}, sketch.HeaderMagic)
	require.Equal(t, "QPKS", string(sketch.HeaderMagic[:]))
}

// TestKind_ZeroValueIsUnset asserts the Kind enum starts at 1, so a zero-value Header{} (as
// returned by every stub Header() method in this package) never accidentally looks like a real
// KindBloom identification.
func TestKind_ZeroValueIsUnset(t *testing.T) {
	require.NotEqual(t, sketch.Kind(0), sketch.KindBloom)
	require.NotEqual(t, sketch.Kind(0), sketch.KindCMS)
	require.NotEqual(t, sketch.Kind(0), sketch.KindHLL)
	require.NotEqual(t, sketch.Kind(0), sketch.KindMisraGries)
	require.NotEqual(t, sketch.Kind(0), sketch.KindMinHash)
}
