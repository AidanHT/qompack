package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

// SP08-D1 (owner decision D20): a SyncPublication pass keeps its whole re-proof and is priced here in
// barriers, the unit host load cannot move. CounterPublicationSyncFile counts the pass's file fsyncs,
// CounterPublicationSyncDir its directory fsyncs.

// barrierFixtureLines is how many lines putBarrierFixture writes: about 190 KB of body, which the
// production chunker (1/4/16 KiB) cuts into dozens of objects, so the objects share the objects/
// root and some share a first-level fanout directory.
const barrierFixtureLines = 4096

// putBarrierFixture puts a multi-chunk body whose CRLF line ends canonicalization rewrites, with
// KeepRaw, so its recovery closure spans the content root and its delta root. It returns the root
// and that closure's objects.
func putBarrierFixture(t *testing.T, tp *testProject) (core.Hash, map[core.Hash]bool) {
	t.Helper()
	ctx := context.Background()
	var b strings.Builder
	for i := range barrierFixtureLines {
		fmt.Fprintf(&b, "publication barrier fixture line %d of %d, with its own words\r\n", i, barrierFixtureLines)
	}
	res, err := tp.Store.PutBytes(ctx, []byte(b.String()), PutOptions{Tool: "Bash", KeepRaw: true})
	require.NoError(t, err)
	objects, err := tp.Store.publicationObjects(ctx, res.Root.Hash)
	require.NoError(t, err)
	require.Greater(t, len(objects), len(res.Root.Chunks), "fixture: the closure spans more than the content root")
	return res.Root.Hash, objects
}

// TestSyncPublication_CountsAFileBarrierPerObjectAndIndex pins the pass's file fsyncs: one for each
// object of the root's recovery closure, one for index/roots.jsonl and one for index/tool_use.jsonl,
// on every pass, because each pass re-proves the whole closure.
func TestSyncPublication_CountsAFileBarrierPerObjectAndIndex(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	root, objects := putBarrierFixture(t, tp)
	require.Zero(t, tp.counter(CounterPublicationSyncFile), "a put alone issues no publication barrier")

	for pass := 1; pass <= 2; pass++ {
		before := tp.counter(CounterPublicationSyncFile)
		require.NoError(t, tp.Store.SyncPublication(ctx, root))
		require.Equal(t, int64(len(objects)+2), tp.counter(CounterPublicationSyncFile)-before,
			"pass %d: one fsync per closure object and one per index file", pass)
	}
}

// TestSyncPublication_SyncsEachDirectoryOncePerPass pins the pass's directory fsyncs at one per
// distinct directory: each fanout leaf (objects/ab/cd) and first-level fanout directory (objects/ab)
// holding a closure object, the objects/ root, and index/. A directory fsync makes durable every
// entry created in that directory before it, and every entry the pass proves exists before the
// pass's directory fsyncs, so a second fsync of a directory within the pass makes nothing more
// durable. The count is derived from the closure's hashes, not from the store's own walk.
func TestSyncPublication_SyncsEachDirectoryOncePerPass(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	root, objects := putBarrierFixture(t, tp)
	leaves, fanouts := map[string]bool{}, map[string]bool{}
	for h := range objects {
		hx := hexOf(h)
		fanouts[hx[:fanoutWidth]] = true
		leaves[hx[:2*fanoutWidth]] = true
	}
	require.Less(t, len(fanouts), len(objects), "fixture: some objects share a first-level fanout directory")
	want := int64(len(leaves) + len(fanouts) + 2) // + the objects/ root + index/
	perObject := int64(3*len(objects) + 1)        // a leaf, a fanout and the root per object, + index/
	require.Less(t, want, perObject, "fixture: one fsync per directory is fewer than three per object")
	t.Logf("closure: %d objects in %d leaves under %d fanouts; %d directory fsyncs per pass (%d at three per object)",
		len(objects), len(leaves), len(fanouts), want, perObject)

	for pass := 1; pass <= 2; pass++ {
		before := tp.counter(CounterPublicationSyncDir)
		require.NoError(t, tp.Store.SyncPublication(ctx, root))
		require.Equal(t, want, tp.counter(CounterPublicationSyncDir)-before,
			"pass %d: each directory holding a closure object is fsynced once, then index/", pass)
	}
}

// TestPublicationDirs_KeepsEachDirectoryOnceInTheOrderFirstReached pins the pass's directory set on
// the case no fixture root reaches by chance: two objects in one fanout leaf. Their leaf is fsynced
// once, as are a first-level fanout directory two leaves share and the objects/ root every object
// shares, and a directory outside the objects/ root ends its walk at the volume root.
func TestPublicationDirs_KeepsEachDirectoryOnceInTheOrderFirstReached(t *testing.T) {
	top := filepath.Join(t.TempDir(), "objects")
	leaf := func(parts ...string) string { return filepath.Join(append([]string{top}, parts...)...) }
	d := newPublicationDirs(top, 4)
	d.add(leaf("ab", "cd")) // first object
	d.add(leaf("ab", "cd")) // a second object in the same leaf
	d.add(leaf("ab", "ef")) // a sibling leaf under the same first-level directory
	d.add(leaf("12", "34")) // another first-level directory
	require.Equal(t, []string{
		leaf("ab", "cd"), leaf("ab"), top,
		leaf("ab", "ef"),
		leaf("12", "34"), leaf("12"),
	}, d.order)

	outside := filepath.Join(filepath.Dir(top), "elsewhere")
	d.add(outside)
	require.Equal(t, outside, d.order[6], "a directory outside the objects/ root is still fsynced")
	volume := filepath.VolumeName(outside) + string(filepath.Separator)
	require.Equal(t, volume, d.order[len(d.order)-1], "and its walk ends at the volume root")
}

// TestSyncPublication_FsyncsEveryClosureDirectoryExactlyOnce pins WHICH directories a pass fsyncs,
// where the count above pins only how many: each closure object's fanout leaf and first-level
// fanout directory, the objects/ root and index/, each exactly once, with index/ last, after its two
// index files. A pass that dropped a directory an object needs and fsynced another twice would keep
// the count and fail here. Both passes fsync the same set: each is a whole re-proof (D20).
func TestSyncPublication_FsyncsEveryClosureDirectoryExactlyOnce(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	root, objects := putBarrierFixture(t, tp)
	objectsDir, indexDir := tp.Store.l.Objects, tp.Store.l.Index
	want := map[string]bool{objectsDir: true, indexDir: true}
	for h := range objects {
		hx := hexOf(h)
		fanout := filepath.Join(objectsDir, hx[:fanoutWidth])
		want[fanout] = true
		want[filepath.Join(fanout, hx[fanoutWidth:2*fanoutWidth])] = true
	}

	var synced []string
	tp.Store.pubSyncDir = func(dir string) error {
		synced = append(synced, dir)
		return nil
	}
	for pass := 1; pass <= 2; pass++ {
		synced = synced[:0]
		require.NoError(t, tp.Store.SyncPublication(ctx, root))
		got := make(map[string]bool, len(synced))
		for _, dir := range synced {
			require.False(t, got[dir], "pass %d: %s is fsynced twice", pass, dir)
			got[dir] = true
		}
		require.Equal(t, want, got, "pass %d: every directory a closure object needs, and index/", pass)
		require.Equal(t, indexDir, synced[len(synced)-1], "pass %d: index/ is fsynced last", pass)
	}
}
