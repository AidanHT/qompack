package daemon

import (
	"context"
	"encoding/binary"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// SP20-D4 resource regression (V6 close-out C1.10). The generation store used to write every radix
// page as its own content-addressed file with a file fsync and a directory fsync, and to path-copy on
// every key, so one committed lease cost dozens of file creations and fsyncs: the recorded probe
// (plans/sdd/V6-closeout/rollover/runs/00-baseline-seam-cost-probe-*.log) measured 1.85 s per lease on
// Windows and 1.70 s on Linux with the seam on, and 49 files per acknowledged delivery. These tests pin
// the physical storage contract that replaces it: one committed generation publishes exactly one pack
// file and one root pointer, whatever its size, and the index still answers every key exactly.
//
// The two file suffixes are spelled literally, as the on-disk names they are, so a test run on the
// file-per-page layout fails on the count rather than failing to compile.

// radixStoreFiles counts the regular files under a generation store's page directory, split into
// pack files, root pointers and anything else (which must be nothing).
func radixStoreFiles(t *testing.T, pagesDir string) (packs, roots, other int) {
	t.Helper()
	err := filepath.WalkDir(pagesDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		switch {
		case strings.HasSuffix(p, ".pack"):
			packs++
		case strings.HasSuffix(p, ".root"):
			roots++
		default:
			other++
		}
		return nil
	})
	require.NoError(t, err)
	return packs, roots, other
}

func TestDeliveryGeneration_CommitPublishesOnePackPerGenerationWhateverItsSize(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		leases int
	}{{"one-lease", 1}, {"four-hundred-leases", 400}} {
		t.Run(tc.name, func(t *testing.T) {
			g := newTestGenerations(t)
			leases := make([]deliveryLease, tc.leases)
			for i := range leases {
				leases[i] = mkGenLease(t, genNonce(i), core.SessionID("sess-pack"), uint64(i+1))
			}
			_, err := g.commit(ctx, leases)
			require.NoError(t, err)
			require.Equal(t, int64(1), g.generationCount())

			packs, roots, other := radixStoreFiles(t, filepath.Join(g.dir, genPagesDir))
			require.Equal(t, 1, packs, "one generation publishes exactly one pack")
			require.Equal(t, 1, roots, "one generation publishes exactly one root pointer")
			require.Zero(t, other, "no page is published as a file of its own")

			for i := range leases {
				got, found, err := g.resolveLease(ctx, leases[i].Delivery)
				require.NoError(t, err)
				require.True(t, found, "lease %d resolves", i)
				require.Equal(t, leases[i], got)
			}
		})
	}
}

// rootPackOf returns the pack id a committed root's pointer names.
func rootPackOf(t *testing.T, r *deliveryRadix, root radixHash) uint64 {
	t.Helper()
	loc, err := r.readRootPointer(root)
	require.NoError(t, err)
	return loc.pack
}

// flipPackByte inverts one byte of a pack file in place.
func flipPackByte(t *testing.T, path string, at int64) {
	t.Helper()
	raw, err := os.ReadFile(paths.Long(path))
	require.NoError(t, err)
	require.Less(t, at, int64(len(raw)))
	raw[at] ^= 0xFF
	require.NoError(t, os.WriteFile(paths.Long(path), raw, 0o600))
}

// TestDeliveryRadix_TxnHoldsOnlyLivePages: a transaction drops every page a later insert replaced, so
// n keys inserted into an empty tree leave exactly the n leaves and n-1 branches of the final tree.
func TestDeliveryRadix_TxnHoldsOnlyLivePages(t *testing.T) {
	r := newTestRadix(t)
	tx := r.begin()
	root := radixHash{}
	const n = 300
	for i := 0; i < n; i++ {
		var err error
		root, err = tx.insert(context.Background(), root, []byte(fmt.Sprintf("key-%04d", i)), []byte("v"))
		require.NoError(t, err)
	}
	require.Equal(t, 2*n-1, tx.held(), "only the final tree's pages are held")
	require.NoError(t, tx.commit(context.Background(), root))
	for i := 0; i < n; i++ {
		v, found, err := r.lookup(context.Background(), root, []byte(fmt.Sprintf("key-%04d", i)))
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, []byte("v"), v)
	}
}

// TestDeliveryRadix_UncommittedRootIsUnavailableToCommittedReaders: a transaction's pages are visible to
// the transaction and to nobody else until commit; a committed reader of the new root is unavailable
// before it, never answered from memory it cannot trust across a crash.
func TestDeliveryRadix_UncommittedRootIsUnavailableToCommittedReaders(t *testing.T) {
	r := newTestRadix(t)
	base := mustInsert(t, r, radixHash{}, []byte("committed"), []byte("c"))
	tx := r.begin()
	next, err := tx.insert(context.Background(), base, []byte("held"), []byte("h"))
	require.NoError(t, err)

	v, found, err := tx.lookup(context.Background(), next, []byte("held"))
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []byte("h"), v)
	_, _, err = r.lookup(context.Background(), next, []byte("held"))
	require.ErrorIs(t, err, errRadixUnavailable, "an uncommitted root is not readable outside its transaction")

	require.NoError(t, tx.commit(context.Background(), next))
	v, found, err = r.lookup(context.Background(), next, []byte("held"))
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []byte("h"), v)
	v, found, err = r.lookup(context.Background(), next, []byte("committed"))
	require.NoError(t, err)
	require.True(t, found, "the committed subtree the transaction reused is still reached")
	require.Equal(t, []byte("c"), v)
}

// TestDeliveryRadix_TamperedLocationIsUnavailableNotAnotherPage: a branch's child location is outside
// the page hash, so pointing it at a different, perfectly valid record must read as unavailable — the
// record found there does not hash to the child the parent names.
func TestDeliveryRadix_TamperedLocationIsUnavailableNotAnotherPage(t *testing.T) {
	r := newTestRadix(t)
	var ha, hb radixHash // differ at bit 0: the root is one branch over two leaves
	ha[0], hb[0] = 0x00, 0x80
	r.hashKey = func(k []byte) radixHash {
		if string(k) == "A" {
			return ha
		}
		return hb
	}
	tx := r.begin()
	root, err := tx.insert(context.Background(), radixHash{}, []byte("A"), []byte("va"))
	require.NoError(t, err)
	root, err = tx.insert(context.Background(), root, []byte("B"), []byte("vb"))
	require.NoError(t, err)
	require.NoError(t, tx.commit(context.Background(), root))

	loc, err := r.readRootPointer(root)
	require.NoError(t, err)
	node, err := r.readRecord(root, loc)
	require.NoError(t, err)
	require.Equal(t, radixKindBranch, node.kind)
	page, _, err := r.readRecordBytes(root, loc)
	require.NoError(t, err)
	r2 := reopenRadix(t, r) // a fresh handle: nothing cached from before the edit
	// Rewrite child0's offset (the trailer field after child0's pack id) to child1's record.
	raw, err := os.ReadFile(paths.Long(r2.packPath(loc.pack)))
	require.NoError(t, err)
	trailer := int64(loc.off) + radixRecordHeaderLen + int64(len(page))
	binary.BigEndian.PutUint64(raw[trailer+8:], node.loc1.off)
	require.NoError(t, os.WriteFile(paths.Long(r2.packPath(loc.pack)), raw, 0o600))

	_, _, err = r2.lookup(context.Background(), root, []byte("A"))
	require.ErrorIs(t, err, errRadixUnavailable, "a location naming the wrong record is unavailable")
	v, found, err := r2.lookup(context.Background(), root, []byte("B"))
	require.NoError(t, err, "the untouched side still answers")
	require.True(t, found)
	require.Equal(t, []byte("vb"), v)
}

// TestDeliveryRadix_TruncatedPackIsUnavailable: a pack cut short under a committed root is unavailable.
func TestDeliveryRadix_TruncatedPackIsUnavailable(t *testing.T) {
	r := newTestRadix(t)
	tx := r.begin()
	root := radixHash{}
	for i := 0; i < 20; i++ {
		var err error
		root, err = tx.insert(context.Background(), root, []byte(fmt.Sprintf("k%02d", i)), []byte("v"))
		require.NoError(t, err)
	}
	require.NoError(t, tx.commit(context.Background(), root))
	pack := r.packPath(rootPackOf(t, r, root))
	r2 := reopenRadix(t, r) // a fresh handle: nothing cached from before the truncation
	info, err := os.Stat(paths.Long(pack))
	require.NoError(t, err)
	require.NoError(t, os.Truncate(paths.Long(pack), info.Size()/2))
	unavailable := 0
	for i := 0; i < 20; i++ {
		_, found, err := r2.lookup(context.Background(), root, []byte(fmt.Sprintf("k%02d", i)))
		if err != nil {
			require.ErrorIs(t, err, errRadixUnavailable)
			unavailable++
			continue
		}
		require.True(t, found, "a key that still reads resolves; it is never a false absence")
	}
	require.Positive(t, unavailable, "keys whose pages were cut away are unavailable")
}

// TestDeliveryRadix_OrphanPackAndStagingAreIgnoredAndPreserved: a crash can leave a published pack no
// root names, or a staging file that was never linked. Neither is read, reused or removed.
func TestDeliveryRadix_OrphanPackAndStagingAreIgnoredAndPreserved(t *testing.T) {
	r := newTestRadix(t)
	root := mustInsert(t, r, radixHash{}, []byte("k0"), []byte("v0"))
	orphanPack := r.packPath(0xabcdef)
	require.NoError(t, os.WriteFile(paths.Long(orphanPack), []byte(radixPackMagic+"unreferenced"), 0o600))
	staging := filepath.Join(r.dir, radixPacksDir, radixTempPrefix+"interrupted")
	require.NoError(t, os.WriteFile(paths.Long(staging), []byte("half a pack"), 0o600))

	root = mustInsert(t, r, root, []byte("k1"), []byte("v1"))
	for i := 0; i < 2; i++ {
		v, found, err := r.lookup(context.Background(), root, []byte(fmt.Sprintf("k%d", i)))
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, []byte(fmt.Sprintf("v%d", i)), v)
	}
	for _, p := range []string{orphanPack, staging} {
		_, err := os.Stat(paths.Long(p))
		require.NoError(t, err, "crash leftovers are preserved for diagnosis: %s", p)
	}
}

// TestDeliveryRadix_ConflictingRootPointerIsPreserved: a pointer already at a root's address that does
// not resolve to that root refuses the commit and is left exactly as found.
func TestDeliveryRadix_ConflictingRootPointerIsPreserved(t *testing.T) {
	r := newTestRadix(t)
	tx := r.begin()
	root, err := tx.insert(context.Background(), radixHash{}, []byte("k"), []byte("v"))
	require.NoError(t, err)
	_, err = r.ensureShard(r.shard(root))
	require.NoError(t, err)
	conflict := make([]byte, radixRootFileLen)
	copy(conflict, radixRootMagic)
	binary.BigEndian.PutUint64(conflict[len(radixRootMagic):], 0x1234)
	binary.BigEndian.PutUint64(conflict[len(radixRootMagic)+8:], uint64(radixPackHeaderLen))
	require.NoError(t, os.WriteFile(paths.Long(r.pagePath(root)), conflict, 0o600))

	require.ErrorIs(t, tx.commit(context.Background(), root), errRadixUnavailable)
	got, err := os.ReadFile(paths.Long(r.pagePath(root)))
	require.NoError(t, err)
	require.Equal(t, conflict, got, "the conflicting pointer is preserved, never overwritten")
}
