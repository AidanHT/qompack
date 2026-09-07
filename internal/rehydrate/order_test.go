package rehydrate

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRenderOrder_IsIotaOrder is the assertion that keeps §8.6's importance order from drifting.
//
// renderOrder is what Build fills in, and budget truncation drops from the tail, so the order
// decides what survives a small budget. types.go declares the ten ItemKind constants in rendered
// order, which makes renderOrder the iota sequence by construction — and makes the inherited
// conformance case runItemOrderCase ("items must be emitted in ascending ItemKind") hold for
// free. This test is what proves the two are still the same list.
func TestRenderOrder_IsIotaOrder(t *testing.T) {
	require.Len(t, renderOrder, int(ItemAffordance)+1,
		"renderOrder must name every ItemKind; ItemAffordance is the last constant")

	for i, k := range renderOrder {
		require.Equal(t, ItemKind(i), k,
			"renderOrder[%d] must be ItemKind(%d): the render order IS the iota order", i, i)
	}
}

// TestItemKind_String covers every declared kind, so a kind added without a String case shows up
// here rather than as a bare number in a log line or an ItemStat key.
func TestItemKind_String(t *testing.T) {
	want := []string{
		"invariants", "user_intent", "eliminations", "decisions", "current_work",
		"pointers", "restored_instructions", "skill_index", "drop_report", "affordance",
	}
	require.Len(t, want, len(renderOrder), "one name per kind")

	for i, k := range renderOrder {
		require.Equal(t, want[i], k.String())
	}
}

// TestItemKind_StringOnUnknownKind pins the fallback: an out-of-range kind renders as a stable,
// greppable token rather than panicking or returning "".
func TestItemKind_StringOnUnknownKind(t *testing.T) {
	got := ItemKind(int(ItemAffordance) + 1).String()

	require.NotEmpty(t, got)
	require.Contains(t, got, "unknown")
}

// TestSectionHeading_CoversEveryKind asserts every kind has a real heading. A missing case would
// otherwise render a section with no heading at all, which is invisible in a diff of the payload.
func TestSectionHeading_CoversEveryKind(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range renderOrder {
		h := sectionHeading(k)
		require.NotEmpty(t, h, "kind %s has no heading", k)
		require.True(t, len(h) > 3 && h[:3] == "## ", "heading %q must be a level-2 markdown heading", h)
		require.NotContains(t, h, "\n", "a heading is one line")
		require.False(t, seen[h], "two kinds share the heading %q", h)
		seen[h] = true
	}
}
