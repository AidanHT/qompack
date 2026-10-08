package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
)

// Spool identity (known issue 19, owner decision D78(c), D38's residual). From 0.3.1 every hook's
// spool writer has a file of its own, client-<pid>-<writer id>.ndjson, so a hook that reuses an
// earlier hook's pid no longer appends to that hook's file. A 0.3.0 hook named its file by pid alone,
// client-<pid>.ndjson, and an upgraded project can still hold one: the daemon drains both forms, in
// one host order. These rows write the new form through the real ipc writer, two of them under one
// pid (ipc.NewSpoolForPID stands a writer in for a hook process), beside a legacy file of that pid.

// closeSpoolWriter releases w's file handle, as a hook process's exit does: Windows does not delete a
// file a handle is still open on, so a drained file stays until its writer has closed it.
func closeSpoolWriter(t *testing.T, w ipc.SpoolWriter) {
	t.Helper()
	if c, ok := w.(interface{ Close() error }); ok {
		require.NoError(t, c.Close())
	}
}

// spoolIdentityWrite appends reqs through a new writer standing in for a hook process with pid, closes
// it as the process's exit would, and returns the file it wrote.
func spoolIdentityWrite(t *testing.T, root string, pid int, reqs ...ipc.Request) string {
	t.Helper()
	w := ipc.NewSpoolForPID(paths.Of(root).Spool, pid, logging.Nop(), nil)
	for _, req := range reqs {
		require.NoError(t, w.Append(req))
	}
	closeSpoolWriter(t, w)
	return filepath.Base(w.Path())
}

// TestSpoolIdentity_LegacyAndPerWriterSpoolsDrainInHostOrder is D38's residual, closed for every
// writer from 0.3.1, and the upgrade path. A 0.3.0 hook with pid 9 left the host's first prompt in
// client-9.ndjson, undrained. Two later hooks could not reach the daemon either: one with pid 10
// spooled the host's second prompt, and one that reused pid 9 the third. Under pid naming the third
// joined the first in client-9.ndjson, behind it in that file's record order, so one pass replayed it
// ahead of client-10's second prompt and the third prompt took turn 1. With a file per writer the
// pass places each file by its own first record, and the legacy file among them: turns 0, 1 and 2
// are the host's first, second and third prompts, nothing is counted out of host order, and every
// file is consumed and removed.
func TestSpoolIdentity_LegacyAndPerWriterSpoolsDrainInHostOrder(t *testing.T) {
	root := t.TempDir()
	dd, o := spD3PidReuseDaemon(t, root)

	const sess core.SessionID = "sess-spool-identity"
	first := spD3Prompt(dd, root, sess, testDeliveryToken('d'), "first")
	second := spD3Prompt(dd, root, sess, testDeliveryToken('e'), "second")
	third := spD3Prompt(dd, root, sess, testDeliveryToken('f'), "third")
	second.TS = first.TS + 100
	third.TS = second.TS + 100

	const legacy = "client-9.ndjson"
	writeSpoolRecords(t, root, legacy, first) // what a 0.3.0 hook with pid 9 left behind
	secondFile := spoolIdentityWrite(t, root, 10, second)
	thirdFile := spoolIdentityWrite(t, root, 9, third)

	n, err := dd.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, 3, n, "the legacy file and both writers' files are replayed in one pass")

	require.Equal(t, "first", spD3PromptText(t, o, observer.VerbatimPromptID(sess, 0)), "prompt_<s>_0")
	require.Equal(t, "second", spD3PromptText(t, o, observer.VerbatimPromptID(sess, 1)), "prompt_<s>_1")
	require.Equal(t, "third", spD3PromptText(t, o, observer.VerbatimPromptID(sess, 2)), "prompt_<s>_2")
	require.Equal(t, int64(0), dd.m.Counter(observerPromptOutOfHostOrder).Value(),
		"host order held across the legacy and the per-writer files: nothing is counted")
	require.Empty(t, spD3NoticesFor(t, root, o, sess), "host order held: no substitution notice")
	require.NotEqual(t, legacy, thirdFile, "a writer that reuses pid 9 must not append to the legacy file")
	require.NotEqual(t, secondFile, thirdFile)
	for _, base := range []string{legacy, secondFile, thirdFile} {
		require.True(t, spoolWatchGone(root, base), "%s is consumed and removed", base)
	}
}

// TestSpoolIdentity_DrainedPerWriterSpoolsLeaveNoPerFileState: with a file per writer the spool
// directory holds one file per hook process that spooled, not one per pid, so what the daemon keeps
// per file must go with the file. Forty writers under one pid and a legacy file of the same pid are
// seen by the client-spool watcher, indexed for the PreCompact settle, drained and removed. After the
// drain nothing of them is left: no spool file, no state/drain.json entry, no drain memo, no settle
// index entry and, at the watcher's next look, no watcher entry.
func TestSpoolIdentity_DrainedPerWriterSpoolsLeaveNoPerFileState(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	dr := newDrainer(contentDrainConfig(dd))
	dd.drain.Store(dr)
	ctx := context.Background()

	const sess core.SessionID = "sess-spool-identity-bounded"
	const writers = 40
	for i := range writers {
		spoolIdentityWrite(t, root, 4242, liveOrderTool(dd, root, sess, i))
	}
	writeHookSpool(t, root, "client-4242.ndjson", liveOrderTool(dd, root, sess, writers))
	listed := listClientSpools(root)
	require.Len(t, listed, writers+1, "fixture: every writer has a file of its own beside the legacy one")

	entries := map[string]*spoolWatchEntry{}
	now := time.Now()
	dd.lookAtClientSpools(ctx, entries, true, now) // every file seen for the first time: no pass yet
	require.Len(t, entries, writers+1, "fixture: the watcher has an entry per file")
	all := make(map[string]bool, len(listed))
	for _, l := range listed {
		all[l.base] = true
	}
	dd.indexClientSpools(ctx, all)
	dd.spoolHeads.mu.Lock()
	indexed := len(dd.spoolHeads.files)
	dd.spoolHeads.mu.Unlock()
	require.Equal(t, writers+1, indexed, "fixture: the settle's index has an entry per file")

	n, err := dd.Drain(ctx)
	require.NoError(t, err)
	require.Equal(t, writers+1, n, "every file's record is replayed")

	require.Empty(t, listClientSpools(root), "every drained file is removed")
	st, err := dr.loadState()
	require.NoError(t, err)
	require.Empty(t, st, "state/drain.json keeps no entry for a removed file")
	require.Empty(t, dr.memo, "the drain remembers nothing of a removed file")
	dd.spoolHeads.mu.Lock()
	indexed, unlinking := len(dd.spoolHeads.files), len(dd.spoolHeads.unlinking)
	dd.spoolHeads.mu.Unlock()
	require.Zero(t, indexed, "the settle's index drops every file the drain removed")
	require.Zero(t, unlinking, "every removal's bracket is closed")
	dd.lookAtClientSpools(ctx, entries, false, now.Add(dd.spool.every))
	require.Empty(t, entries, "the watcher forgets every file that is gone")

	entriesLeft, err := os.ReadDir(paths.Long(paths.Of(root).Spool))
	require.NoError(t, err)
	for _, e := range entriesLeft {
		require.NotEqual(t, ipc.SpoolFileClient, ipc.SpoolFileKindOf(e.Name()), "client spool %s left", e.Name())
	}
}
