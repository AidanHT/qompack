package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// Edge rows for the GC mark and its retention-source listing that the main GC tests leave unexecuted:
// the session clause over sessions persisted by an earlier process, a file version as the reason a
// root is in the age window, entries in a retention directory that are not sources, and a pass that
// reaches the store only after it closed or its caller gave up (w16b-cover, C3.6).

// TestGC_KeepsTheMostRecentSessionsPersistedByAnEarlierProcess: RetainSessions counts sessions the
// index persisted as well as the live ones, newest first, and keeps what those sessions used; an older
// session's content is collected.
func TestGC_KeepsTheMostRecentSessionsPersistedByAnEarlierProcess(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	roots := map[core.SessionID]Root{}
	for i, sess := range []core.SessionID{"s-old", "s-mid", "s-new"} {
		roots[sess] = gcSeed(t, tp, "src/"+string(sess)+".txt", "content of "+string(sess)+"\n")
		ts := core.UnixMilli(1_000 + 1_000*int64(min(i, 1))) // s-mid and s-new end together
		require.NoError(t, tp.Store.RecordToolUse(ctx, ToolUseRecord{
			ID: core.ToolUseID("toolu_" + string(sess)), Session: sess, Turn: 1, TS: ts, Tool: "FileRead",
			Root: roots[sess].Hash, Path: "src/" + string(sess) + ".txt",
		}))
	}
	require.NoError(t, tp.Store.Flush(ctx))
	require.NoError(t, tp.Store.Close())
	reopened := openOver(t, tp.project)

	_, err := reopened.Store.GC(ctx, GCPolicy{RetainDays: -1, RetainSessions: 2})
	require.NoError(t, err)
	for sess, keep := range map[core.SessionID]bool{"s-old": false, "s-mid": true, "s-new": true} {
		_, gerr := reopened.Store.GetRoot(ctx, roots[sess].Hash)
		if keep {
			require.NoError(t, gerr, "%s is one of the two most recent sessions", sess)
		} else {
			require.ErrorIs(t, gerr, core.ErrNotFound, "%s is older than the retained sessions", sess)
		}
	}
}

// TestGC_AFileVersionInsideTheWindowKeepsItsRoot: a root whose own record is past the age window is
// still in it while a file version naming it is, and that is the reason the pass reports.
func TestGC_AFileVersionInsideTheWindowKeepsItsRoot(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	versioned := gcSeed(t, tp, "src/versioned.txt", "a file version still in the window\n")
	stale := gcSeed(t, tp, "src/stale.txt", "nothing keeps this one\n")
	tp.Clock.Advance(30 * 24 * time.Hour)
	require.NoError(t, tp.Store.AppendFileVersion(ctx, "src/versioned.txt", FileVersion{
		TS: core.UnixMilli(tp.Clock.Now().UnixMilli()), Root: versioned.Hash, Turn: 1, Bytes: 10,
	}))

	rep, err := tp.Store.GC(ctx, GCPolicy{RetainDays: 7, RetainSessions: -1, MaxOutcomes: 100})
	require.NoError(t, err)
	_, err = tp.Store.GetRoot(ctx, versioned.Hash)
	require.NoError(t, err)
	_, err = tp.Store.GetRoot(ctx, stale.Hash)
	require.ErrorIs(t, err, core.ErrNotFound)
	var reason string
	for _, o := range rep.Outcomes {
		if o.Root == versioned.Hash {
			reason = o.Reason
		}
	}
	require.Equal(t, "a file version inside the retention age window", reason)
}

// TestGC_PassesOverRetentionDirectoryEntriesThatAreNotSources: checkpoints/ and the pending registry
// may hold entries that are not retention sources — a subdirectory, a file of another kind. They are
// passed over: not read, not a halt, and not counted as pending writes. A marker younger than the
// window is counted and left in place.
func TestGC_PassesOverRetentionDirectoryEntriesThatAreNotSources(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	l := paths.Of(tp.Root)
	require.NoError(t, os.Mkdir(paths.Long(filepath.Join(l.Checkpoints, "drafts.json")), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(l.Checkpoints, "README.txt")), []byte("x"), 0o600))
	marker := plantPending(t, tp, "young"+pendingWriteSuffix, []byte(`{"v":1,"root":"","chunks":[]}`))
	plantPending(t, tp, "notes.txt", []byte("not a marker"))
	require.NoError(t, os.Mkdir(paths.Long(filepath.Join(l.State, pendingWriteDir, "sub"+pendingWriteSuffix)), 0o700))

	rep, err := tp.Store.GC(ctx, GCPolicy{RetainDays: 7, RetainSessions: -1})
	require.NoError(t, err)
	require.False(t, rep.RetentionRootsError, "a stray entry is not an unreadable retention source")
	require.Equal(t, 1, rep.PendingWrites)
	require.Zero(t, rep.PendingExpired)
	require.FileExists(t, marker)
}

// TestGCPass_RechecksTheStoreAndTheCallerWhenItFinallyRuns: a pass that waited behind another reaches
// gcPass after GC's own checks, so it checks again: a store closed meanwhile, or a caller that gave up,
// runs nothing.
func TestGCPass_RechecksTheStoreAndTheCallerWhenItFinallyRuns(t *testing.T) {
	tp := newTestStore(t)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := tp.Store.gcPass(cancelled, forceCollect)
	require.ErrorIs(t, err, context.Canceled)

	require.NoError(t, tp.Store.Close())
	_, err = tp.Store.gcPass(context.Background(), forceCollect)
	require.ErrorIs(t, err, core.ErrDegraded)
}
