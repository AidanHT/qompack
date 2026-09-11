package daemon

import (
	"cmp"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// diskDrainState reads state/drain.json the way a restarted process does: from the bytes on disk,
// never from a drainer's memory. A missing file reads as empty progress.
func diskDrainState(t *testing.T, root string) drainState {
	t.Helper()
	st, err := newDrainer(DrainConfig{Root: root}).loadState()
	require.NoError(t, err)
	return st
}

// spoolFileSize is path's size on disk.
func spoolFileSize(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(paths.Long(path))
	require.NoError(t, err)
	return fi.Size()
}

// closedTwoDeliverySegment accepts two deliveries for sess and closes its segment, the way
// SessionEnd or a restart leaves it: nothing holds it, so a drain that finds the session ended
// removes it. It returns the segment's path and size.
func closedTwoDeliverySegment(t *testing.T, dd *daemon, clk *fakeClock, sess core.SessionID) (string, int64) {
	t.Helper()
	for k := 0; k < 2; k++ {
		clk.Advance(time.Millisecond)
		liveWALAccept(t, dd, liveWALTool(dd, sess))
	}
	require.NoError(t, dd.ing.CloseSession(sess))
	walFile := walPath(paths.Of(dd.root).Spool, sess, 0)
	return walFile, spoolFileSize(t, walFile)
}

// recreationCases are the three ways a later segment under a drained name compares with the
// drained one's size S. Each failed its own way against a stale {Done, Offset: S, Size: S} entry.
var recreationCases = []struct {
	name     string
	straggle int    // deliveries in the recreated segment; the drained one held two
	size     int    // cmp.Compare(recreated size, S)
	stale    string // what a stale entry did in this case
}{
	{name: "recreated smaller", straggle: 1, size: -1, stale: "failed every Drain for the whole spool"},
	{name: "recreated at the same size", straggle: 2, size: 0, stale: "removed the segment undrained"},
	{name: "recreated larger", straggle: 3, size: +1, stale: "skipped the segment's first S bytes"},
}

// recreateAndRecover recreates sess's drained segment 0 the way the session's own traffic does —
// the ingest reopens the name after a restart mid-session, or a straggler after SessionEnd — with
// straggle ACKed deliveries, checks the recreated size against drained, crashes, and returns the
// TSs that were ACKed and those a restarted drain replays from the spool.
func recreateAndRecover(t *testing.T, dd *daemon, clk *fakeClock, sess core.SessionID, straggle, size int, drained int64) (acked, got []core.UnixMilli, err error) {
	t.Helper()
	walFile := walPath(paths.Of(dd.root).Spool, sess, 0)
	require.NoFileExists(t, walFile, "fixture: the drained segment was unlinked")
	for k := 0; k < straggle; k++ {
		clk.Advance(time.Millisecond)
		r := liveWALTool(dd, sess)
		liveWALAccept(t, dd, r) // Accept returned: the delivery is ACKed, its WAL line durable
		acked = append(acked, r.TS)
	}
	require.Equal(t, size, cmp.Compare(spoolFileSize(t, walFile), drained), "fixture: the recreated segment's size")
	require.NoError(t, dd.ing.Close()) // crash: nothing processed, nothing drained
	_, err = recordingDrainer(dd.root, clk, &got).Drain(context.Background())
	return acked, got, err
}

// TestDrainForgetsAFileDurablyBeforeUnlinkingIt pins the ordering itself. The drain used to unlink a
// finished spool file first and drop its progress entry afterwards, in memory, for the next
// saveState to persist; a crash in between left state/drain.json naming a file that no longer
// existed, and nothing ever retired that entry. The seam is the unlink: when it is issued, the state
// on disk must already have forgotten the file. Both routes into the removal are covered — the pass
// that reads a file to its end and removes it, and the early path that removes a file an earlier
// pass finished (a live session's segment Stop kept, retired by the next daemon's startup drain).
func TestDrainForgetsAFileDurablyBeforeUnlinkingIt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		finishedFirst bool
		dispatched    int
	}{
		{name: "read to its end by the same pass", dispatched: 2},
		{name: "finished by an earlier pass", finishedFirst: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			const sess = core.SessionID("sess-forget-first")
			dd, clk := liveWALDaemon(t, nil)
			ctx := context.Background()
			walFile, size := closedTwoDeliverySegment(t, dd, clk, sess)
			base := filepath.Base(walFile)
			if tc.finishedFirst {
				keep := dd.drainConfig()
				keep.IsLive = func(core.SessionID) bool { return true }
				_, err := newDrainer(keep).Drain(ctx)
				require.NoError(t, err)
				require.Equal(t, &drainFileState{Size: size, Offset: size, Done: true}, diskDrainState(t, dd.root)[base],
					"fixture: an earlier pass finished the segment and kept it")
			}

			cfg := dd.drainConfig()
			var namedAtUnlink []bool
			cfg.RemoveWAL = func(path string, drained int64) (bool, error) {
				_, named := diskDrainState(t, dd.root)[base]
				namedAtUnlink = append(namedAtUnlink, named)
				return dd.ing.removeDrainedWAL(path, drained)
			}
			n, err := newDrainer(cfg).Drain(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.dispatched, n)
			require.Equal(t, []bool{false}, namedAtUnlink,
				"when the unlink is issued, state/drain.json must no longer name the file")
			require.NoFileExists(t, walFile)
			require.NotContains(t, diskDrainState(t, dd.root), base)
		})
	}
}

// TestDrainCrashAtTheUnlinkLeavesNoProgressForARecreatedSegment is the crash image the round-2
// reviewer's probe built (drainwal R2-1). The drain unlinks a finished segment, the process dies
// before anything else reaches the disk, and the session's traffic later recreates the name.
// state/drain.json is captured as the unlink is issued and put back after the drain, which is
// exactly what that crash leaves. With the entry still on disk each size relation failed its own
// way (recreationCases). A restarted daemon's drain, the fresh drainer here, must instead fail
// nothing and replay every ACKed line of the recreated segment exactly once.
func TestDrainCrashAtTheUnlinkLeavesNoProgressForARecreatedSegment(t *testing.T) {
	t.Parallel()
	for _, tc := range recreationCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			const sess = core.SessionID("sess-crash-at-unlink")
			dd, clk := liveWALDaemon(t, nil)
			walFile, drained := closedTwoDeliverySegment(t, dd, clk, sess)
			statePath := paths.Long(drainStatePath(dd.root))

			var atUnlink []byte
			cfg := dd.drainConfig()
			cfg.RemoveWAL = func(path string, n int64) (bool, error) {
				if b, err := os.ReadFile(statePath); err == nil {
					atUnlink = b
				}
				return dd.ing.removeDrainedWAL(path, n)
			}
			n, err := newDrainer(cfg).Drain(context.Background())
			require.NoError(t, err)
			require.Equal(t, 2, n)
			require.NoFileExists(t, walFile)
			require.NotNil(t, atUnlink, "fixture: the unlink was issued")
			require.NoError(t, os.WriteFile(statePath, atUnlink, 0o600)) // the crash, at the unlink

			acked, got, err := recreateAndRecover(t, dd, clk, sess, tc.straggle, tc.size, drained)
			require.NoError(t, err, "a restarted drain must not fail (a stale entry %s)", tc.stale)
			require.Equal(t, acked, got, "every ACKed line of the recreated segment, exactly once (a stale entry %s)", tc.stale)
			require.NoFileExists(t, walFile, "replayed and held by nobody, the recreated segment is cleaned up")
		})
	}
}

// TestDrainRestoresProgressWhenItDoesNotRemoveTheFile pins the other half of forgetting first: a
// removal that does not happen must put the file's progress back on disk before the pass moves on.
// Otherwise a crash later in the pass leaves the file with no entry, and the restarted drain
// replays every line of it again. The removal can not happen in two ways. The ingest can refuse,
// because it holds the segment for a session it serves without the registry counting it live. Or
// the unlink can fail, a Windows sharing violation say, injected here so both platforms see it.
//
// The crash image is taken while the pass is on the NEXT spool file, a client fallback the listing
// orders after every WAL segment, and put back afterwards: the restarted drain must replay only the
// delivery ACKed after the pass.
func TestDrainRestoresProgressWhenItDoesNotRemoveTheFile(t *testing.T) {
	t.Parallel()
	errUnlink := errors.New("injected: the unlink failed")
	cases := []struct {
		name    string
		release bool // close the segment, so only the injected failure keeps it
		remove  func(dd *daemon) func(string, int64) (bool, error)
		wantErr error
	}{
		{
			name:   "the ingest holds the segment",
			remove: func(dd *daemon) func(string, int64) (bool, error) { return dd.ing.removeDrainedWAL },
		},
		{
			name:    "the unlink fails",
			release: true,
			remove: func(*daemon) func(string, int64) (bool, error) {
				return func(string, int64) (bool, error) { return false, errUnlink }
			},
			wantErr: errUnlink,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			const sess = core.SessionID("sess-not-removed")
			dd, clk := liveWALDaemon(t, nil)
			ctx := context.Background()
			clk.Advance(time.Millisecond)
			liveWALAccept(t, dd, liveWALTool(dd, sess)) // not through the registry: the session is not live
			if tc.release {
				require.NoError(t, dd.ing.CloseSession(sess))
			}
			walFile := walPath(paths.Of(dd.root).Spool, sess, 0)
			base := filepath.Base(walFile)
			size := spoolFileSize(t, walFile)
			fallback, err := ipc.EncodeRequest(liveWALTool(dd, "sess-client-fallback"))
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(paths.Long(filepath.Join(paths.Of(dd.root).Spool, "client-4343.ndjson")), fallback, 0o600))
			statePath := paths.Long(drainStatePath(dd.root))

			cfg := dd.drainConfig()
			remove := tc.remove(dd)
			var namedAtUnlink []bool
			cfg.RemoveWAL = func(path string, drained int64) (bool, error) {
				_, named := diskDrainState(t, dd.root)[base]
				namedAtUnlink = append(namedAtUnlink, named)
				return remove(path, drained)
			}
			var image []byte
			dispatch := cfg.Dispatch
			cfg.Dispatch = func(ctx context.Context, r ipc.Request) ipc.Response {
				if r.Session != sess && image == nil {
					if b, err := os.ReadFile(statePath); err == nil {
						image = b
					}
				}
				return dispatch(ctx, r)
			}
			n, err := newDrainer(cfg).Drain(ctx)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, 2, n, "the segment's delivery and the client fallback's")
			require.Equal(t, []bool{false}, namedAtUnlink, "the file was forgotten before its unlink was tried")
			require.FileExists(t, walFile)
			require.Equal(t, &drainFileState{Size: size, Offset: size, Done: true}, diskDrainState(t, dd.root)[base],
				"the pass ends with the file's progress back on disk")
			require.NotNil(t, image, "fixture: the pass reached the client fallback")

			// The crash, while the pass was on the client fallback; one more delivery was ACKed first.
			require.NoError(t, os.WriteFile(statePath, image, 0o600))
			clk.Advance(time.Millisecond)
			after := liveWALTool(dd, sess)
			liveWALAccept(t, dd, after)
			require.NoError(t, dd.ing.Close())

			var got []core.UnixMilli
			_, err = recordingDrainer(dd.root, clk, &got).Drain(ctx)
			require.NoError(t, err)
			require.Equal(t, []core.UnixMilli{after.TS}, got,
				"the restored progress was already on disk: the drained delivery is not replayed")
		})
	}
}

// TestDrainCrashBetweenForgettingAndUnlinkingReplaysOnlyWhatTheFrontierLacks pins the window the
// ordering opens, so its consequence stays what the design claims. A crash after the file is
// forgotten and before it is unlinked leaves the file with no progress entry, and the restarted
// drain reads it again from offset zero. For a leased line that costs nothing: the committed
// frontier names it, and the drain advances past an acknowledged copy without dispatching it
// (TestDrainDoesNotRedeliverAnAcknowledgedClientCopy). Both deliveries here went through the live
// path — leased, published, acknowledged — so the restarted drain must dispatch neither, and then
// release the segment.
func TestDrainCrashBetweenForgettingAndUnlinkingReplaysOnlyWhatTheFrontierLacks(t *testing.T) {
	root := t.TempDir()
	dd, calls := newObservingDaemon(t, root)
	lock := lockFor(t, dd, root)
	defer func() { _ = lock.Release() }()
	ctx := context.Background()

	const sess = "sess-forgotten-not-unlinked"
	tokens := []string{testDeliveryToken('c'), testDeliveryToken('d')}
	for _, token := range tokens {
		require.True(t, dd.dispatchOp(ctx, observeRequest(token, sess, `{"hook_event_name":"PostToolUse"}`)).OK)
	}
	drainRing(t, dd)
	journal, err := dd.deliveryJournal()
	require.NoError(t, err)
	for _, token := range tokens {
		require.True(t, journal.acknowledged(token), "fixture: the live path committed the frontier")
	}
	require.Equal(t, 2, calls(), "fixture: the live path published both")
	require.NoError(t, dd.ing.CloseSession(sess))
	walFile := walPath(paths.Of(root).Spool, sess, 0)
	base := filepath.Base(walFile)
	statePath := paths.Long(drainStatePath(root))

	cfg := dd.drainConfig()
	cfg.IsLive = func(core.SessionID) bool { return false } // the session has ended
	var image []byte
	cfg.RemoveWAL = func(string, int64) (bool, error) {
		if b, err := os.ReadFile(statePath); err == nil {
			image = b
		}
		return false, nil // the process dies here: the unlink is never issued
	}
	_, err = newDrainer(cfg).Drain(ctx)
	require.NoError(t, err)
	require.NotNil(t, image, "fixture: the drain reached the unlink")
	require.NoError(t, os.WriteFile(statePath, image, 0o600))
	require.NotContains(t, diskDrainState(t, root), base, "the crash image: the file is forgotten")
	require.FileExists(t, walFile, "the crash image: the file is still there")

	// The restart: a fresh seen set, the same durable frontier.
	restarted := newDrainer(DrainConfig{
		Root: root, Log: logging.Nop(), Metrics: dd.m, Clock: dd.clk,
		Dispatch: dd.drainDispatch, Seen: newSeenSet(seenCapacity), Admit: dd.admitDelivery,
		Journal: dd.deliveryJournal,
	})
	n, err := restarted.Drain(ctx)
	require.NoError(t, err)
	require.Zero(t, n)
	require.Equal(t, 2, calls(), "a line the frontier already names is read again but not dispatched again")
	require.NoFileExists(t, walFile)
	require.True(t, restarted.GapState().Complete, "reading an acknowledged line again is not a gap")
}
