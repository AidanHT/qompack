package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// The lifecycle seams a caller meets when something is already wrong: the pending-write registry
// that must not fail a hook, the retention-root contract, and the delta record's own parser.

// TestPending_DegradesInsteadOfFailingThePutWhenTheRegistryIsUnusable.
//
// Every producer of a Put is a hook, and §2.3 permits a hook no exit code but 0, so an unwritable
// .qompack/state must not turn a recorded tool result into a failed session. The put SUCCEEDS, the
// freshness guard alone carries the sweep, and store.pending.degraded is what makes the loss
// visible instead of silent.
//
// The registry is made unusable the way a real one breaks: something that is not a directory is
// sitting where the directory has to be.
func TestPending_DegradesInsteadOfFailingThePutWhenTheRegistryIsUnusable(t *testing.T) {
	// Not parallel: newTestStore calls t.Setenv.
	tp := newTestStore(t)
	ctx := context.Background()

	blocked := filepath.Join(tp.Store.l.State, pendingWriteDir)
	require.NoError(t, os.WriteFile(paths.Long(blocked), []byte("not a directory\n"), 0o600))

	res, err := tp.Store.PutBytes(ctx, []byte("package main\n\nfunc main() {}\n"),
		PutOptions{Tool: "Read", Path: "src/main.go"})
	require.NoError(t, err, "a broken pending registry must not fail the put")
	require.False(t, res.Root.Hash.IsZero())
	require.Equal(t, int64(1), tp.counter("store.pending.degraded"),
		"the degradation is counted, not swallowed")
}

// TestAppendRetentionRoot_RefusesAClaimThatNamesNothing.
//
// The file is the producer-side contract for holding an object live against GC, and GC reads it as
// a set. A line with no hash holds nothing, and a line with no class cannot be audited or expired
// by the pass that reads it — both would sit in the file forever looking like a retention claim.
// They are refused as contract errors, and the file is not created by the attempt.
func TestAppendRetentionRoot_RefusesAClaimThatNamesNothing(t *testing.T) {
	// Not parallel: newTestStore calls t.Setenv.
	tp := newTestStore(t)

	err := AppendRetentionRoot(tp.Root, RetentionRoot{Class: "lease", Reason: "no hash"})
	require.ErrorIs(t, err, core.ErrContract)
	require.ErrorContains(t, err, "needs a hash")

	err = AppendRetentionRoot(tp.Root, RetentionRoot{Hash: core.Hash{1}, Reason: "no class"})
	require.ErrorIs(t, err, core.ErrContract)
	require.ErrorContains(t, err, "needs a class")

	_, statErr := os.Stat(paths.Long(RetentionRootsPath(tp.Root)))
	require.ErrorIs(t, statErr, os.ErrNotExist, "a refused claim wrote no line")
}

// TestCompactRetentionRoots_HasNothingToDoWithoutALog. GC runs the compaction at the end of every
// pass, including on projects that have never declared a retention root, so "there is no file" has
// to be an empty report rather than an error that would make the pass look failed.
func TestCompactRetentionRoots_HasNothingToDoWithoutALog(t *testing.T) {
	// Not parallel: newProject calls t.Setenv.
	p := newProject(t)

	rep, err := CompactRetentionRoots(p.Root)
	require.NoError(t, err)
	require.False(t, rep.Compacted)
	require.Zero(t, rep.LinesBefore)
	require.Zero(t, rep.BytesBefore)
}

// TestUnmarshalDeltaRecord_ReadsBothShapesAndCallsTheRestCorrupt.
//
// A recovery record written before SP20-D3 is a bare delta array that names its base only on its
// index line; one written since is an object that names its base inside itself, which is what makes
// its address unique to that base. Both have to read. Everything else is CORRUPT rather than empty:
// a record that carries no delta list, or names no usable base, cannot reconstruct anything, and
// returning an empty delta list for it would hand the caller a silent identity transform wearing an
// exactness claim.
func TestUnmarshalDeltaRecord_ReadsBothShapesAndCallsTheRestCorrupt(t *testing.T) {
	t.Parallel()

	base := core.Hash{7}
	declared, deltas, err := unmarshalDeltaRecord([]byte(`[]`))
	require.NoError(t, err, "the pre-SP20-D3 bare array still reads")
	require.True(t, declared.IsZero(), "the old shape declares its base only on its index line")
	require.Empty(t, deltas)

	declared, deltas, err = unmarshalDeltaRecord([]byte(`{"base":"` + base.String() + `","deltas":[]}`))
	require.NoError(t, err)
	require.Equal(t, base, declared, "the current shape names its base inside the payload")
	require.NotNil(t, deltas)

	for _, c := range []struct {
		name string
		in   string
		why  string
	}{
		{"a truncated array", `[{"o":1`, "unexpected end"},
		{"a truncated object", `{"base":`, "unexpected end"},
		{"an object with no delta list", `{"base":"` + base.String() + `"}`, "carries no delta list"},
		{"an object with no base", `{"base":"","deltas":[]}`, "declares no usable base"},
		{"an object with an unparseable base", `{"base":"sha256:zz","deltas":[]}`, "declares no usable base"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := unmarshalDeltaRecord([]byte(c.in))
			require.Error(t, err)
			require.ErrorContains(t, err, c.why)
		})
	}
}

// TestReadDeltaAndRestoreOriginal_RefuseBeforeTheyReadAnything.
//
// Both are recovery reads, and both carry the same two guards for the same reason: a closed store's
// indices have been released, and a cancelled context means the turn that asked is already over. The
// fidelity they report while refusing is Unavailable — never Corrupt, which is a claim about bytes
// that were actually read, and never Exact, which would be an exactness claim made without reading
// anything at all.
func TestReadDeltaAndRestoreOriginal_RefuseBeforeTheyReadAnything(t *testing.T) {
	// Not parallel: newTestStore calls t.Setenv.
	tp := newTestStore(t)
	ctx := context.Background()
	res, err := tp.Store.PutBytes(ctx, []byte("package main\n"), PutOptions{Tool: "Read", Path: "a.go"})
	require.NoError(t, err)

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, fid, err := tp.Store.ReadDelta(cancelled, res.Root.Hash)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, FidelityUnavailable, fid)
	_, fid, err = tp.Store.RestoreOriginal(cancelled, res.Root.Hash)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, FidelityUnavailable, fid)

	_, fid, err = tp.Store.RestoreOriginal(ctx, core.Hash{9})
	require.ErrorIs(t, err, core.ErrNotFound, "a root the index never held has nothing to restore")
	require.Equal(t, FidelityUnavailable, fid)

	require.NoError(t, tp.Store.Close())
	_, fid, err = tp.Store.ReadDelta(ctx, res.Root.Hash)
	require.ErrorIs(t, err, core.ErrDegraded)
	require.Equal(t, FidelityUnavailable, fid)
	_, fid, err = tp.Store.RestoreOriginal(ctx, res.Root.Hash)
	require.ErrorIs(t, err, core.ErrDegraded)
	require.Equal(t, FidelityUnavailable, fid)
}

// TestFlush_RefusesACancelledContextAndWritesNothing. Flush is the only call that materializes the
// files view, persists the counters and appends session records, so a Flush that half-ran on a
// cancelled turn would leave three files disagreeing about when the session ended. It refuses before
// the first of them.
func TestFlush_RefusesACancelledContextAndWritesNothing(t *testing.T) {
	// Not parallel: newTestStore calls t.Setenv.
	tp := newTestStore(t)
	ctx := context.Background()
	require.NoError(t, tp.Store.AppendFileVersion(ctx, "src/a.go",
		FileVersion{TS: 10, Root: core.Hash{1}, Turn: 1, Bytes: 11}))

	before := treeSnapshot(t, tp.Root)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	require.ErrorIs(t, tp.Store.Flush(cancelled), context.Canceled)
	require.Equal(t, before, treeSnapshot(t, tp.Root), "a refused Flush wrote nothing")

	require.NoError(t, tp.Store.Flush(ctx), "and the same Flush runs once the context is live")
}
