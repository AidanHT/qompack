// This is an INTERNAL test package (package observer, not observer_test) for one reason:
// humanBytes is unexported, and its rounding is the part of Tombstone most likely to drift
// silently — a 1000-based divisor would render Qompack.md §8.1's own example as "2.5KB" and no
// external assertion on Tombstone alone would say why. Testing it directly makes the failure name
// itself.
package observer

import (
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

// section81Hash is a hash whose Short() form is the "a3f2…" of Qompack.md §8.1's example
// tombstone, so the assertion below can be byte-for-byte against the design document.
const section81Hash = "sha256:a3f2c9e14b70d5183c6a94f27b0e5d8a1c4f7b0e3a6d9c2f5b8e1a4d7c0f3b6e"

// TestTombstone_RendersTheSection81Form asserts the exact marker Qompack.md §8.1 item 2 shows.
// Every character is load-bearing: the "sha256:" prefix and the 12-character short hash make the
// marker a real store address that `expand` can resolve, and the U+00B7 separators are what the
// /qompack:status renderer and the docs both reproduce.
func TestTombstone_RendersTheSection81Form(t *testing.T) {
	root, err := core.ParseHash(section81Hash)
	require.NoError(t, err)

	rec := store.ToolUseRecord{
		Root:  root,
		Bytes: 2457,
		Tool:  "FileRead",
		Path:  "src/auth.ts",
	}

	require.Equal(t,
		"[cleared: sha256:a3f2c9e14b70 · 2.4KB · FileRead src/auth.ts · re-expandable]",
		Tombstone(rec))
}

// TestTombstone_IsAddressable asserts the property that makes a tombstone worth writing at all: a
// cleared result stays fetchable, because the marker carries the same 12 hex characters
// core.Hash.Short produces and can therefore be pasted straight back into a retrieval call.
func TestTombstone_IsAddressable(t *testing.T) {
	root, err := core.ParseHash(section81Hash)
	require.NoError(t, err)

	got := Tombstone(store.ToolUseRecord{Root: root, Bytes: 1, Tool: "Grep", Path: "src/pool.ts"})
	require.Contains(t, got, root.Short(), "the marker must carry a resolvable store address")
	require.Contains(t, got, "re-expandable", "the marker must say the content can be fetched back")
}

// TestTombstone_HandlesAnEmptyRecord asserts the renderer never panics on a zero record: a
// tombstone is written on the hot path, and a nil-ish record must degrade to a useless-but-valid
// line rather than take a hook down (§12.3).
func TestTombstone_HandlesAnEmptyRecord(t *testing.T) {
	got := Tombstone(store.ToolUseRecord{})
	require.Contains(t, got, "[cleared: sha256:")
	require.Contains(t, got, "0B")
	require.Contains(t, got, "re-expandable]")
}

// TestHumanBytes_UsesTheBinaryDivisor is the assertion the whole golden rests on: 1 KB is 1024
// bytes. Each case names why it is here rather than being an arbitrary number.
func TestHumanBytes_UsesTheBinaryDivisor(t *testing.T) {
	const kb = 1024
	cases := []struct {
		name string
		in   int64
		want string
	}{
		{name: "zero", in: 0, want: "0B"},
		{name: "one byte", in: 1, want: "1B"},
		{name: "one below the KB boundary stays in bytes", in: kb - 1, want: "1023B"},
		{name: "exactly one KB", in: kb, want: "1.0KB"},
		{name: "the §8.1 example rounds to 2.4, not 2.5", in: 2457, want: "2.4KB"},
		{name: "one below the MB boundary stays in KB", in: kb*kb - 1, want: "1024.0KB"},
		{name: "exactly one MB", in: kb * kb, want: "1.0MB"},
		{name: "exactly one GB", in: kb * kb * kb, want: "1.0GB"},
		{name: "beyond the largest unit stays in GB", in: 3 * kb * kb * kb, want: "3.0GB"},
		{name: "a negative size is not a panic", in: -5, want: "-5B"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, humanBytes(tc.in))
		})
	}
}

// TestHumanBytes_NeverEmitsASpaceBeforeTheUnit pins the format detail that would otherwise be
// invisible until a golden diff: the unit is glued to the number.
func TestHumanBytes_NeverEmitsASpaceBeforeTheUnit(t *testing.T) {
	for _, n := range []int64{0, 1, 1023, 1024, 2457, 1 << 20, 1 << 30} {
		require.NotContains(t, humanBytes(n), " ", "humanBytes(%d) must not contain a space", n)
	}
}
