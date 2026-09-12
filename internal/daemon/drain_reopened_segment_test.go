package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// The third case of the rule drain_durable_progress_test.go's two tests pin (SP20-D1, F6 review round
// 1, F1/F3): the bound a pass records is floored at what the drain has already consumed, because that
// bound can FALL between passes.
//
// Both tests beside it move a held segment's synced size only upward, so neither could see the
// converse failure. ingest.holdSynced enters EVERY segment the ingest opens into ingest.synced at 0,
// including one that already holds bytes an earlier pass consumed and recorded, and publishSynced
// freezes it there for the life of a handle whose Sync failed. Recording that 0 over an offset of N
// persisted {Size: 0, Offset: N} — a shape loadState refuses outright — so every later Drain of the
// WHOLE spool failed before it read a single file, permanently, with no machine crash needed. This
// test drives exactly that: a real ingest, its own SyncedWAL/HoldsWAL/RemoveWAL, and a straggler that
// reopens a drained segment after its session ended.

// TestDrainKeepsItsProgressLoadableWhenAStragglerReopensADrainedSegment runs the whole sequence
// through the production seams: no faked SyncedWAL, no faked drain bound. A session's line is
// accepted and synced, SessionEnd releases the segment, a pass drains it to its stat and keeps it
// (the registry still reports the session live), and then a straggler request reopens that exact
// file. The reopened handle counts nothing synced, and its own Sync fails, so it stays at 0 — below
// the offset the pass already consumed and recorded. The progress the next pass persists must still
// be progress a later process can load, and the spool must go on draining.
func TestDrainKeepsItsProgressLoadableWhenAStragglerReopensADrainedSegment(t *testing.T) {
	const sess = core.SessionID("sess-straggler")
	ing, p, root := newWALIngest(t)
	spool := paths.Of(root).Spool
	seg := walPath(spool, sess, 0)
	segBase := filepath.Base(seg)

	// The straggler's Sync fails, so the handle its Accept reopens never advances past the 0
	// holdSynced entered it with. The hook is set once, before any Accept, and keyed on the call
	// index: nothing here waits on a clock or on another goroutine's timing.
	errStraggler := errors.New("drain progress: injected straggler WAL sync fault")
	p.onSync = func(call int, _ string, f *os.File) error {
		if call >= 2 {
			return errStraggler
		}
		return f.Sync()
	}

	first := wireLine(t, ipc.Request{Op: ipc.OpObserveTool, Session: sess, TS: 1})
	a := goAccept(ing, p, 0, first)
	awaitAccept(t, a)
	require.NoError(t, a.err)
	drained := int64(len(first.want))
	requireSynced(t, ing, seg, drained, "fixture: the session's line is synced")

	// SessionEnd. The segment is released, so the pass below syncs it itself and reads it to its stat.
	require.NoError(t, ing.CloseSession(sess))
	requireNotHeld(t, ing, seg, "fixture: CloseSession released the segment")

	var got []core.UnixMilli
	dr := newDrainer(DrainConfig{
		Root: root, Log: newRecordingLogger(), Clock: newFakeClock(epoch),
		Dispatch: func(_ context.Context, r ipc.Request) ipc.Response {
			got = append(got, r.TS)
			return ipc.Response{OK: true}
		},
		IsLive:    func(core.SessionID) bool { return true }, // the registry still reports the session live
		RemoveWAL: ing.removeDrainedWAL,
		HoldsWAL:  ing.holdsWAL,
		SyncedWAL: ing.syncedWAL,
	})

	n, err := drainAside(t, dr)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, []core.UnixMilli{1}, got)
	require.Equal(t, &drainFileState{Size: drained, Offset: drained, Done: true}, diskDrainState(t, root)[segBase],
		"the pass consumed the released segment to its stat, and kept it: its session is still live")

	// The straggler, the case removeDrainedWAL's own comment names. Its Accept reopens the very
	// segment the pass drained, and its Sync fails, so the segment's synced size stays at 0 while its
	// bytes are in the file all the same.
	straggler := wireLine(t, ipc.Request{Op: ipc.OpObserveTool, Session: sess, TS: 2})
	b := goAccept(ing, p, 1, straggler)
	awaitAccept(t, b)
	require.ErrorIs(t, b.err, errStraggler, "fixture: the straggler's WAL Sync failed")
	requireSynced(t, ing, seg, 0,
		"fixture: the reopened segment counts nothing synced — below the offset the first pass consumed")
	require.Equal(t, drained+int64(len(straggler.want)), spoolFileSize(t, seg),
		"fixture: the straggler's bytes are in the file, past the bound the progress records")

	// A brand-new healthy spool file, whose line measures the blast radius: a wedge takes it down too.
	const healthyName = "client-7777.ndjson"
	healthyPath := filepath.Join(spool, healthyName)
	healthy := wireLine(t, ipc.Request{Op: ipc.OpObserveTool, Session: "sess-healthy", TS: 3})
	require.NoError(t, os.WriteFile(paths.Long(healthyPath), healthy.want, 0o600))

	n, err = drainAside(t, dr)
	require.NoError(t, err)
	require.Equal(t, 1, n, "the healthy file's line")
	require.Equal(t, []core.UnixMilli{1, 3}, got)
	// diskDrainState loads the record the way a restarted process does, so a recorded size that had
	// fallen below the consumed offset fails right here: loadState refuses that shape outright.
	require.Equal(t, &drainFileState{Size: drained, Offset: drained}, diskDrainState(t, root)[segBase],
		"the record keeps the durable bound an earlier pass consumed to; it never falls to the reopened 0")
	require.Equal(t, int64(len(straggler.want)), dr.GapState().PendingBytes,
		"the straggler's unsynced bytes are pending, though no progress names them")
	require.NoFileExists(t, healthyPath, "the healthy file drained fully and was removed")

	// And the pass after that still runs at all: the wedge was a progress record no later Drain could
	// load, for the whole spool, with no operator recovery.
	n, err = drainAside(t, dr)
	require.NoError(t, err, "progress floored at the consumed offset stays loadable across the reopen")
	require.Zero(t, n)
	require.Equal(t, []core.UnixMilli{1, 3}, got, "and nothing is delivered twice")
}
