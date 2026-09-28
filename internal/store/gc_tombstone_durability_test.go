package store

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// tombstoneSyncProbe wraps index/roots.jsonl's append handle and records, at every Sync, how many
// bytes the file held and whether the doomed root's tombstone was in them while every one of its
// chunks was still on disk.
type tombstoneSyncProbe struct {
	io.WriteCloser
	t        *testing.T
	rootsPth string
	root     core.Hash
	chunks   []string
	// durableBeforeSweep is what was durable when the sweep began deleting: the file's size at the
	// last Sync that ran while none of the root's chunks had been deleted and the tombstone was in the
	// file, or the size the test's own Flush made durable before the pass when no such Sync ran.
	durableBeforeSweep int64
}

func (p *tombstoneSyncProbe) Sync() error {
	s, ok := p.WriteCloser.(syncer)
	require.True(p.t, ok, "fixture: roots.jsonl's handle can be synced")
	if err := s.Sync(); err != nil {
		return err
	}
	raw, err := os.ReadFile(paths.Long(p.rootsPth))
	require.NoError(p.t, err)
	if !bytes.Contains(raw, []byte(`"op":"gc","root":"`+p.root.String()+`"`)) {
		return nil
	}
	for _, c := range p.chunks {
		if _, err := os.Stat(paths.Long(c)); err != nil {
			return nil
		}
	}
	p.durableBeforeSweep = int64(len(raw))
	return nil
}

// TestGC_TombstonesAreDurableBeforeTheSweepDeletesTheirChunks: a pass retires a dead root by
// appending a tombstone to index/roots.jsonl and then sweeps the root's chunks off disk. A deletion
// cannot be taken back, so the tombstone must be durable first. Otherwise a power cut that keeps the
// deletions and loses the unsynced tombstone reopens an index that serves the retired root with its
// chunks gone: a spurious integrity failure where the content had simply expired. The test models
// that cut: roots.jsonl is cut back to what was durable when the sweep began, and the reopened store
// must still know the root is retired.
func TestGC_TombstonesAreDurableBeforeTheSweepDeletesTheirChunks(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	doomed := gcSeed(t, tp, "src/doomed.txt", "referenced by nothing, collected by this pass\n")
	require.NoError(t, tp.Store.Flush(ctx))

	rootsPath := filepath.Join(paths.Of(tp.Root).Index, rootsFile)
	flushed := fileSize(t, rootsPath)
	probe := &tombstoneSyncProbe{t: t, rootsPth: rootsPath, root: doomed.Hash, durableBeforeSweep: flushed}
	for _, c := range doomed.Chunks {
		probe.chunks = append(probe.chunks, tp.Store.objectPath(c.Hash))
	}
	tp.Store.rootsW.mu.Lock()
	probe.WriteCloser = tp.Store.rootsW.w
	tp.Store.rootsW.w = probe
	tp.Store.rootsW.mu.Unlock()

	rep, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)
	require.Positive(t, rep.DeletedObjects, "fixture: the pass collects the doomed root's chunks")
	for _, c := range probe.chunks {
		_, statErr := os.Stat(paths.Long(c))
		require.ErrorIs(t, statErr, os.ErrNotExist, "fixture: the sweep deleted chunk %s", filepath.Base(c))
	}

	require.NoError(t, tp.Store.Close())
	require.NoError(t, os.Truncate(paths.Long(rootsPath), probe.durableBeforeSweep),
		"the power cut keeps only what was durable when the sweep began deleting")
	re := openOver(t, tp.project)
	_, err = re.Store.GetRoot(ctx, doomed.Hash)
	require.ErrorIs(t, err, core.ErrNotFound,
		"the retired root stays retired: its tombstone was durable before its chunks were deleted")
	require.Greater(t, probe.durableBeforeSweep, flushed, "a sync made the tombstone durable before the sweep")
}
