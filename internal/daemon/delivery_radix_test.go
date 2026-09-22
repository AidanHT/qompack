package daemon

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// These tests pin the delivery radix primitive's four load-bearing properties: immutable generations
// (an old root stays usable and exact), sound absence (a missing key is (nil,false,nil) only after a
// complete valid traversal), unavailability that is never absence (missing/corrupt/collision), and
// path-locality (a lookup never opens a page off its own path). They live in package daemon so the
// injected key-hash seam and the on-disk page paths are reachable without widening the surface.

func newTestRadix(t *testing.T) *deliveryRadix {
	t.Helper()
	r, err := openDeliveryRadix(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.close() })
	return r
}

func mustInsert(t *testing.T, r *deliveryRadix, root radixHash, key, value []byte) radixHash {
	t.Helper()
	newRoot, err := r.insert(context.Background(), root, key, value)
	require.NoError(t, err)
	return newRoot
}

// TestDeliveryRadix_EmptyTreeAbsence: the zero root holds nothing, and that is a real absence.
func TestDeliveryRadix_EmptyTreeAbsence(t *testing.T) {
	r := newTestRadix(t)
	v, found, err := r.lookup(context.Background(), radixHash{}, []byte("anything"))
	require.NoError(t, err)
	require.False(t, found)
	require.Nil(t, v)
}

// TestDeliveryRadix_ManyGenerationsOldRootsUsable inserts more than 64 distinct keys, keeping every
// intermediate root, and proves each generation still answers its own keys exactly and reports a
// later key as absent (not unavailable).
func TestDeliveryRadix_ManyGenerationsOldRootsUsable(t *testing.T) {
	r := newTestRadix(t)
	const n = 80
	key := func(i int) []byte { return []byte(fmt.Sprintf("key-%03d", i)) }
	val := func(i int) []byte { return []byte(fmt.Sprintf("value-for-%03d", i)) }

	roots := make([]radixHash, 0, n)
	root := radixHash{}
	for i := 0; i < n; i++ {
		root = mustInsert(t, r, root, key(i), val(i))
		roots = append(roots, root)
		require.False(t, root.isZero())
	}
	require.Greater(t, len(roots), 64)

	for i := 0; i < n; i++ {
		// Every key up to and including i resolves to its exact value in generation i.
		for j := 0; j <= i; j++ {
			v, found, err := r.lookup(context.Background(), roots[i], key(j))
			require.NoError(t, err, "gen %d key %d", i, j)
			require.True(t, found, "gen %d must hold key %d", i, j)
			require.Equal(t, val(j), v)
		}
		// A key inserted only in a later generation is a sound ABSENCE here, not unavailability.
		if i+1 < n {
			v, found, err := r.lookup(context.Background(), roots[i], key(i+1))
			require.NoError(t, err, "gen %d must not error on a future key", i)
			require.False(t, found)
			require.Nil(t, v)
		}
	}
}

// TestDeliveryRadix_UpdateValueSameKey: updating a key forks a new root; the old root keeps the old
// value, immutable.
func TestDeliveryRadix_UpdateValueSameKey(t *testing.T) {
	r := newTestRadix(t)
	r1 := mustInsert(t, r, radixHash{}, []byte("k"), []byte("v1"))
	r2 := mustInsert(t, r, r1, []byte("k"), []byte("v2"))
	require.NotEqual(t, r1, r2)

	v, found, err := r.lookup(context.Background(), r1, []byte("k"))
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []byte("v1"), v)

	v, found, err = r.lookup(context.Background(), r2, []byte("k"))
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []byte("v2"), v)

	// Re-inserting an identical (key,value) is idempotent: the root does not move.
	r3 := mustInsert(t, r, r2, []byte("k"), []byte("v2"))
	require.Equal(t, r2, r3)
}

// TestDeliveryRadix_ReopenPersists: a fresh handle over the same directory reads a prior root.
func TestDeliveryRadix_ReopenPersists(t *testing.T) {
	dir := t.TempDir()
	r, err := openDeliveryRadix(dir)
	require.NoError(t, err)
	root := radixHash{}
	for i := 0; i < 10; i++ {
		root = mustInsert(t, r, root, []byte(fmt.Sprintf("k%d", i)), []byte(fmt.Sprintf("v%d", i)))
	}
	require.NoError(t, r.close())

	r2, err := openDeliveryRadix(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r2.close() })
	for i := 0; i < 10; i++ {
		v, found, err := r2.lookup(context.Background(), root, []byte(fmt.Sprintf("k%d", i)))
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, []byte(fmt.Sprintf("v%d", i)), v)
	}
}

// TestDeliveryRadix_CollisionRefused: two distinct keys forced to the same digest are a collision —
// the second is refused on insert and unavailable on lookup, and the resident key is never touched.
func TestDeliveryRadix_CollisionRefused(t *testing.T) {
	r := newTestRadix(t)
	var same radixHash
	same[0] = 0x42
	r.hashKey = func(k []byte) radixHash {
		if string(k) == "A" || string(k) == "B" {
			return same
		}
		return r.defaultKeyHash(k)
	}

	r1 := mustInsert(t, r, radixHash{}, []byte("A"), []byte("va"))

	_, err := r.insert(context.Background(), r1, []byte("B"), []byte("vb"))
	require.ErrorIs(t, err, errRadixCollision, "a colliding key must be refused, never overwrite")

	// A still resolves to its own value; B is unavailable, never a false match and never absence.
	v, found, err := r.lookup(context.Background(), r1, []byte("A"))
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []byte("va"), v)

	_, found, err = r.lookup(context.Background(), r1, []byte("B"))
	require.ErrorIs(t, err, errRadixCollision)
	require.False(t, found)
}

// TestDeliveryRadix_WrongRootAndCorruptPageAreUnavailable: a root that names no page, and a page whose
// bytes no longer hash to their name, are both unavailable — never read as an empty tree.
func TestDeliveryRadix_WrongRootAndCorruptPageAreUnavailable(t *testing.T) {
	r := newTestRadix(t)

	var bogus radixHash
	bogus[0], bogus[31] = 0xde, 0xad // any non-zero, non-existent root
	_, _, err := r.lookup(context.Background(), bogus, []byte("k"))
	require.ErrorIs(t, err, errRadixUnavailable, "a root with no page on disk is unavailable, not empty")

	root := mustInsert(t, r, radixHash{}, []byte("k"), []byte("v"))
	// Overwrite the root page with bytes that do not hash to its name.
	require.NoError(t, os.WriteFile(paths.Long(r.pagePath(root)), []byte("corrupt"), 0o600))
	_, _, err = r.lookup(context.Background(), root, []byte("k"))
	require.ErrorIs(t, err, errRadixUnavailable, "a page whose content no longer matches its name is unavailable")
}

// TestDeliveryRadix_MissingDescendantIsUnavailableWithoutTouchingTheOther builds a two-leaf branch
// (keys diverging at bit 0), deletes one leaf, and shows the missing branch is unavailable while a
// lookup down the OTHER branch still succeeds — proving path-locality and unavailability-not-absence.
func TestDeliveryRadix_MissingDescendantIsUnavailableWithoutTouchingTheOther(t *testing.T) {
	r := newTestRadix(t)
	var ha, hb radixHash // differ at bit 0
	ha[0] = 0x00
	hb[0] = 0x80
	r.hashKey = func(k []byte) radixHash {
		switch string(k) {
		case "A":
			return ha
		case "B":
			return hb
		default:
			return r.defaultKeyHash(k)
		}
	}
	root := mustInsert(t, r, radixHash{}, []byte("A"), []byte("va"))
	root = mustInsert(t, r, root, []byte("B"), []byte("vb"))

	// Compute and delete B's leaf page directly.
	leafB := radixDigest(radixPageDomain, encodeRadixLeaf(hb, []byte("B"), []byte("vb")))
	require.NoError(t, os.Remove(paths.Long(r.pagePath(leafB))))

	_, _, err := r.lookup(context.Background(), root, []byte("B"))
	require.ErrorIs(t, err, errRadixUnavailable, "a missing descendant page is unavailable, not absent")

	v, found, err := r.lookup(context.Background(), root, []byte("A"))
	require.NoError(t, err, "the intact branch must answer without touching the missing one")
	require.True(t, found)
	require.Equal(t, []byte("va"), v)
}

// TestDeliveryRadix_LookupDoesNotTouchAnIrrelevantCorruptBranch corrupts one leaf and shows a lookup
// down the sibling still returns the right answer, and that a third key sharing the first bit is a
// sound absence — neither opens the corrupt page.
func TestDeliveryRadix_LookupDoesNotTouchAnIrrelevantCorruptBranch(t *testing.T) {
	r := newTestRadix(t)
	var ha, hb, hc radixHash
	ha[0] = 0x00 // bit0 = 0
	hb[0] = 0x80 // bit0 = 1
	hc[0] = 0x20 // bit0 = 0, diverges from A at bit 2
	r.hashKey = func(k []byte) radixHash {
		switch string(k) {
		case "A":
			return ha
		case "B":
			return hb
		case "C":
			return hc
		default:
			return r.defaultKeyHash(k)
		}
	}
	root := mustInsert(t, r, radixHash{}, []byte("A"), []byte("va"))
	root = mustInsert(t, r, root, []byte("B"), []byte("vb"))

	// Garble B's leaf so any read of it would fail.
	leafB := radixDigest(radixPageDomain, encodeRadixLeaf(hb, []byte("B"), []byte("vb")))
	require.NoError(t, os.WriteFile(paths.Long(r.pagePath(leafB)), []byte("garbled"), 0o600))

	v, found, err := r.lookup(context.Background(), root, []byte("A"))
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []byte("va"), v)

	// C shares bit 0 with A, so it descends into A's leaf and is a sound absence there — the corrupt
	// B leaf is never opened.
	_, found, err = r.lookup(context.Background(), root, []byte("C"))
	require.NoError(t, err)
	require.False(t, found)
}

// TestDeliveryRadix_LongCommonPrefixCompressesAndProvesAbsence uses two keys that agree for 200 bits
// and differ at bit 200: the tree stores one compressed branch (not 200 pages), both keys resolve,
// and a key diverging early is a sound absence caught inside the skip.
func TestDeliveryRadix_LongCommonPrefixCompressesAndProvesAbsence(t *testing.T) {
	r := newTestRadix(t)
	var ha, hb, hc radixHash
	hb[25] = 0x80 // bit 200 set; ha is all-zero, so ha and hb share bits [0,200)
	hc[1] = 0x20  // diverges from the common prefix at bit 10
	r.hashKey = func(k []byte) radixHash {
		switch string(k) {
		case "A":
			return ha
		case "B":
			return hb
		case "C":
			return hc
		default:
			return r.defaultKeyHash(k)
		}
	}
	root := mustInsert(t, r, radixHash{}, []byte("A"), []byte("va"))
	root = mustInsert(t, r, root, []byte("B"), []byte("vb"))

	// Compression: the root is a single branch whose skip covers the 200 shared bits.
	node, err := r.readNode(root)
	require.NoError(t, err)
	require.Equal(t, radixKindBranch, node.kind)
	require.Equal(t, 200, node.skipLen, "the shared prefix must be one compressed edge, not 200 pages")

	for _, tc := range []struct {
		key, val string
	}{{"A", "va"}, {"B", "vb"}} {
		v, found, err := r.lookup(context.Background(), root, []byte(tc.key))
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, []byte(tc.val), v)
	}

	// C leaves the common prefix at bit 10 — a sound absence caught by the skip check.
	_, found, err := r.lookup(context.Background(), root, []byte("C"))
	require.NoError(t, err)
	require.False(t, found)
}

// TestDeliveryRadix_Cancellation: a cancelled context stops both operations with the context error.
func TestDeliveryRadix_Cancellation(t *testing.T) {
	r := newTestRadix(t)
	root := mustInsert(t, r, radixHash{}, []byte("k"), []byte("v"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := r.insert(ctx, root, []byte("k2"), []byte("v2"))
	require.ErrorIs(t, err, context.Canceled)

	_, _, err = r.lookup(ctx, root, []byte("k"))
	require.ErrorIs(t, err, context.Canceled)
}

// TestDeliveryRadix_CrashOrphanLeavesOldGenerationsUsable drops a staging orphan (the shape a crash
// between stage and rename leaves) into a shard directory and shows every committed root still reads
// exactly, the orphan is never referenced, and new inserts still succeed alongside it.
func TestDeliveryRadix_CrashOrphanLeavesOldGenerationsUsable(t *testing.T) {
	r := newTestRadix(t)
	roots := make([]radixHash, 0, 6)
	root := radixHash{}
	for i := 0; i < 6; i++ {
		root = mustInsert(t, r, root, []byte(fmt.Sprintf("k%d", i)), []byte(fmt.Sprintf("v%d", i)))
		roots = append(roots, root)
	}

	// A crash orphan: a staging file that never got renamed, in the newest root's shard directory.
	shard := r.shard(root)
	orphan := r.dir + string(os.PathSeparator) + shard + string(os.PathSeparator) + radixTempPrefix + "orphan"
	require.NoError(t, os.WriteFile(paths.Long(orphan), []byte("half-written page bytes"), 0o600))

	for i, rt := range roots {
		for j := 0; j <= i; j++ {
			v, found, err := r.lookup(context.Background(), rt, []byte(fmt.Sprintf("k%d", j)))
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, []byte(fmt.Sprintf("v%d", j)), v)
		}
	}

	// The index is still writable with the orphan present.
	root = mustInsert(t, r, root, []byte("k-after"), []byte("v-after"))
	v, found, err := r.lookup(context.Background(), root, []byte("k-after"))
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []byte("v-after"), v)

	// The orphan was never touched or referenced.
	_, err = os.Stat(paths.Long(orphan))
	require.NoError(t, err, "the crash orphan is left in place, not silently reused")
}

// TestDeliveryRadix_InputBounds: an empty key, an oversize key and an oversize value are all refused.
func TestDeliveryRadix_InputBounds(t *testing.T) {
	r := newTestRadix(t)
	_, err := r.insert(context.Background(), radixHash{}, nil, []byte("v"))
	require.ErrorIs(t, err, errRadixTooLarge, "an empty key is refused")
	_, err = r.insert(context.Background(), radixHash{}, make([]byte, radixMaxKeyBytes+1), []byte("v"))
	require.ErrorIs(t, err, errRadixTooLarge)
	_, err = r.insert(context.Background(), radixHash{}, []byte("k"), make([]byte, radixMaxValueBytes+1))
	require.ErrorIs(t, err, errRadixTooLarge)
}

// TestDeliveryRadix_DecodeRejectsMalformedPages exercises the page validator directly (a forged page
// need not hash to any name to test the parser): short buffers, a bad magic/version/kind, a branch
// with a zero child, an over-long skip, an oversize leaf length, and trailing bytes.
func TestDeliveryRadix_DecodeRejectsMalformedPages(t *testing.T) {
	good := encodeRadixLeaf(radixHash{}, []byte("k"), []byte("v"))
	var oneChild radixHash
	oneChild[0] = 1

	cases := map[string][]byte{
		"empty":              nil,
		"too short":          []byte("QRD"),
		"bad magic":          append([]byte("ZZZZ"), good[4:]...),
		"unknown version":    append([]byte(radixMagic), append([]byte{99, radixKindLeaf}, good[6:]...)...),
		"unknown kind":       append([]byte(radixMagic), []byte{radixVersion, 9}...),
		"branch zero child":  encodeRadixBranch(0, nil, oneChild, radixHash{}),
		"leaf trailing byte": append(append([]byte(nil), good...), 0x00),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := decodeRadixPage(raw)
			require.ErrorIs(t, err, errRadixUnavailable, "a malformed page must be unavailable")
		})
	}

	// A branch whose skipLen claims more bits than the hash holds is rejected.
	over := encodeRadixBranch(0, nil, oneChild, oneChild)
	over[6], over[7] = 0xFF, 0xFF // skipLen = 65535
	_, err := decodeRadixPage(over)
	require.ErrorIs(t, err, errRadixUnavailable)

	// A well-formed page still decodes, so the rejections above are not vacuous.
	_, err = decodeRadixPage(good)
	require.NoError(t, err)
}
