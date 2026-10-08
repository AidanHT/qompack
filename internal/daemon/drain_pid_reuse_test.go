package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/rehydrate"
)

// SP08-D3 under pid reuse, for the legacy name. A 0.3.0 hook named its client spool
// client-<pid>.ndjson and opened it for append, and a file stays until a drain consumes it, so a later
// 0.3.0 hook that reused the pid appended to an earlier hook's file. D35 orders client spools by file
// and keeps record order within a file, so one file can then carry prompts that belong on either side
// of another file's. From 0.3.1 every writer has a file of its own (drain_spool_identity_test.go), so
// these rows pin what the daemon still does with a legacy file an upgraded project holds (D38).

// spD3PidReuseDaemon wires a daemon with a drainer over root, as the SP08-D3 evidence test does.
func spD3PidReuseDaemon(t *testing.T, root string) (*daemon, *Options) {
	t.Helper()
	_, dd, o := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })
	spD3Drainer(dd, root)
	t.Cleanup(func() {
		grace, cancel := context.WithTimeout(context.Background(), promptRecordWait)
		defer cancel()
		dd.stopPromptRecordings(grace)
	})
	return dd, o
}

// spD3PidReusePrompts returns a prompt of another session and the host's first and second prompts
// of sess, stamped in that host order 100 ms apart, so no two of them can tie.
func spD3PidReusePrompts(dd *daemon, root string, sess core.SessionID) (other, first, second ipc.Request) {
	other = spD3Prompt(dd, root, "sess-sp08d3-reuse-other", testDeliveryToken('a'), "other session")
	first = spD3Prompt(dd, root, sess, testDeliveryToken('b'), "first")
	second = spD3Prompt(dd, root, sess, testDeliveryToken('c'), "second")
	first.TS = other.TS + 100
	second.TS = first.TS + 100
	return other, first, second
}

// writeSpoolRecords writes reqs into one spool file exactly as a hook's appends leave them: each
// encoded line (EncodeRequest ends it with its one newline) back to back. It returns each record's
// encoded length, so a test can place a consumed offset on a record boundary.
func writeSpoolRecords(t *testing.T, root, name string, reqs ...ipc.Request) []int64 {
	t.Helper()
	var buf []byte
	var lens []int64
	for _, req := range reqs {
		line, err := ipc.EncodeRequest(req)
		require.NoError(t, err)
		buf = append(buf, line...)
		lens = append(lens, int64(len(line)))
	}
	spool := paths.Of(root).Spool
	require.NoError(t, os.MkdirAll(paths.Long(spool), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(spool, name)), buf, 0o600))
	return lens
}

// spD3NoticesFor returns the rehydrator's host_order substitution entries for sess.
func spD3NoticesFor(t *testing.T, root string, o *Options, sess core.SessionID) []rehydrate.DropEntry {
	t.Helper()
	var notices []rehydrate.DropEntry
	for _, de := range spD3Rehydrate(t, root, o, sess).Dropped {
		if de.Kind == "user_intent_source" && de.ID == "host_order" {
			notices = append(notices, de)
		}
	}
	return notices
}

// TestSP08D3_PartlyConsumedReusedSpoolReplaysInHostOrder: pid 9's hook spooled a prompt of another
// session, which an earlier pass consumed; the file stayed, and a later hook with pid 9 appended the
// host's second prompt of this session to it. The host's first prompt sits in client-10. The next
// pass places client-9 by the record it still has to replay, so the host-first prompt takes turn 0
// and nothing is flagged.
func TestSP08D3_PartlyConsumedReusedSpoolReplaysInHostOrder(t *testing.T) {
	root := t.TempDir()
	dd, o := spD3PidReuseDaemon(t, root)

	const sess core.SessionID = "sess-sp08d3-reuse"
	other, first, second := spD3PidReusePrompts(dd, root, sess)
	lens := writeSpoolRecords(t, root, "client-9.ndjson", other, second)
	writeSpoolRecords(t, root, "client-10.ndjson", first)
	// An earlier pass consumed client-9's first record; the reused pid's append came after it.
	require.NoError(t, newDrainer(DrainConfig{Root: root}).saveState(drainState{
		"client-9.ndjson": {Size: lens[0], Offset: lens[0]},
	}))

	n, err := dd.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, n, "the two unconsumed prompts are replayed; the consumed record is not")

	require.Equal(t, "first", spD3PromptText(t, o, observer.VerbatimPromptID(sess, 0)), "prompt_<s>_0")
	require.Equal(t, "second", spD3PromptText(t, o, observer.VerbatimPromptID(sess, 1)), "prompt_<s>_1")
	require.Equal(t, int64(0), dd.m.Counter(observerPromptOutOfHostOrder).Value(),
		"host order held: nothing is counted")
	require.Empty(t, spD3NoticesFor(t, root, o, sess), "host order held: no substitution notice")
}

// TestSP08D3_ReusedSpoolWithinOnePassIsNamed pins the residual D35's file order leaves under pid
// reuse, and shows the product names it rather than claiming provenance. Pid 9's hook spooled a
// prompt of another session; a later hook with pid 9 appended this session's host-second prompt
// before any drain ran; client-10 holds the host-first one. One pass keeps record order within
// client-9, so the host-second prompt is replayed first and takes turn 0. No live prompt is
// involved, yet the host-first prompt's capture is counted and the rehydrator reports the
// substitution: the counter and the notice fire for any capture behind a later-stamped turn.
func TestSP08D3_ReusedSpoolWithinOnePassIsNamed(t *testing.T) {
	root := t.TempDir()
	dd, o := spD3PidReuseDaemon(t, root)

	const sess core.SessionID = "sess-sp08d3-reuse"
	other, first, second := spD3PidReusePrompts(dd, root, sess)
	writeSpoolRecords(t, root, "client-9.ndjson", other, second)
	writeSpoolRecords(t, root, "client-10.ndjson", first)

	n, err := dd.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, 3, n, "every spooled prompt is replayed in one pass")

	id0, id1 := observer.VerbatimPromptID(sess, 0), observer.VerbatimPromptID(sess, 1)
	require.Equal(t, "second", spD3PromptText(t, o, id0),
		"residual: record order within the reused file puts the host-second prompt at turn 0")
	require.Equal(t, "first", spD3PromptText(t, o, id1), "the host-first prompt is captured at turn 1")
	require.Equal(t, int64(1), dd.m.Counter(observerPromptOutOfHostOrder).Value(),
		"the capture behind a later-stamped turn is counted without any live prompt")
	notices := spD3NoticesFor(t, root, o, sess)
	require.Len(t, notices, 1, "the rehydrator names the substitution")
	require.Contains(t, notices[0].Detail, string(id0), "the notice names the substituted turn")
	require.Contains(t, notices[0].Detail, string(id1), "the notice names the host-first prompt")
}
