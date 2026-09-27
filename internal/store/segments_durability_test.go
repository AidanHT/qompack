package store

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// segSyncProbe records every Sync of index/segments.jsonl's append handle into a shared step log.
type segSyncProbe struct {
	io.WriteCloser
	t     *testing.T
	steps *[]string
}

func (p segSyncProbe) Sync() error {
	s, ok := p.WriteCloser.(syncer)
	require.True(p.t, ok, "fixture: the segment log's handle can be synced")
	*p.steps = append(*p.steps, "file")
	return s.Sync()
}

// TestSegmentLog_SyncMakesTheLogsNameDurableOnce pins the segment log's durability half as
// checkpoint.Finalize depends on it before a seal: Sync makes the appended records durable (the
// file's sync) AND, the first time in the log's lifetime, the log's own name (index/'s sync, after
// the file's), because the store's Open creates index/segments.jsonl without syncing index/ and no
// publication pass need have run since. Later Syncs pay only the file sync; a Sync whose directory
// barrier failed is retried by the next one; and a closed log refuses rather than claim durability.
func TestSegmentLog_SyncMakesTheLogsNameDurableOnce(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	seg := tp.Store.seg

	var steps []string
	seg.f.mu.Lock()
	seg.f.w = segSyncProbe{WriteCloser: seg.f.w, t: t, steps: &steps}
	seg.f.mu.Unlock()
	failDir := true
	seg.syncDir = func(dir string) error {
		steps = append(steps, "dir:"+filepath.Base(dir))
		require.Equal(t, filepath.Clean(paths.Of(tp.Root).Index), filepath.Clean(dir),
			"the directory that names index/segments.jsonl")
		if failDir {
			return errInjectedSegDirSync
		}
		return paths.SyncDir(dir)
	}

	id, err := seg.Open(ctx, Segment{Session: "seg-durability", StartTurn: 0})
	require.NoError(t, err)
	require.NoError(t, seg.Close(ctx, id, 3, map[string]float64{segTokensFeature: 10}))
	require.NoError(t, seg.MarkEncoded(ctx, []core.SegmentID{id}, 1))

	require.ErrorIs(t, seg.Sync(ctx), errInjectedSegDirSync, "a failed directory barrier fails the Sync")
	require.Equal(t, []string{"file", "dir:index"}, steps, "the records first, then the log's name")

	failDir = false
	steps = nil
	require.NoError(t, seg.Sync(ctx))
	require.Equal(t, []string{"file", "dir:index"}, steps, "an unsucceeded name barrier is retried")

	steps = nil
	require.NoError(t, seg.Sync(ctx))
	require.Equal(t, []string{"file"}, steps, "once the name is durable, a Sync is the file's sync alone")

	require.NoError(t, tp.Store.Close())
	require.ErrorIs(t, seg.Sync(ctx), core.ErrDegraded, "a closed log claims no durability")
}

var errInjectedSegDirSync = errors.New("injected index/ sync failure")
