package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// The tests below pin the edges of the bound drain_durable_test.go's tests pin in the middle (SP20-D1,
// F6 review 1): a held segment with nothing synced yet, a synced size past the pass's own stat, and
// bytes appended after the drain's own sync. Each one is a boundary a mutant crossed while the whole
// package still passed.

// TestDrainTakesNothingOfAFreshSegmentBeforeItsFirstSyncReturns: a segment's synced size is 0 from the
// moment the ingest opens it until the first Sync of its handle returns. That is every session's first
// delivery, and the first batch after every rotation. A drain wired to the ingest takes nothing of such
// a segment: no lease, no dispatch, and no sync of its own. The next pass after that Sync returns takes
// the line. The daemon-wired subtest of TestDrainNeverLeasesAWALLineBeforeItsSyncReturns holds a Sync
// only once the segment's synced size is already positive, so reading a never-synced held segment to
// its stat size, which leases before the WAL Sync returns, passed it (review 1, R1).
func TestDrainTakesNothingOfAFreshSegmentBeforeItsFirstSyncReturns(t *testing.T) {
	const sess = core.SessionID("sess-fresh")
	root, lock, journal := newTestDeliveryJournal(t)
	ing := newIngest(root, config.Defaults(), logging.Nop(), nil, newFakeClock(epoch))
	t.Cleanup(func() { _ = ing.Close() })
	ing.journal = lock.openDeliveryJournal
	p := newWALProbe(ing)
	p.gate = newWALGate(t)
	req := leasedRequest(t, sess)
	logLeaseWrites(journal, p, req.Nonce)

	a := goAccept(ing, p, 0, wireLine(t, req))
	awaitClosed(t, p.gate.entered, "the first Sync of a fresh segment")
	seg := walPath(paths.Of(root).Spool, sess, 0)
	requireSynced(t, ing, seg, 0, "fixture: the ingest holds the segment and nothing of it is synced yet")

	var dispatched []string // appended to by a pass, read once it has returned
	dr := newDrainer(DrainConfig{
		Root: root, Log: logging.Nop(), Clock: newFakeClock(epoch),
		Dispatch: func(_ context.Context, r ipc.Request) ipc.Response {
			dispatched = append(dispatched, r.Nonce)
			return ipc.Response{OK: true}
		},
		Seen:      ing.seen,
		Journal:   lock.openDeliveryJournal,
		IsLive:    func(core.SessionID) bool { return true },
		RemoveWAL: ing.removeDrainedWAL,
		HoldsWAL:  ing.holdsWAL,
		SyncedWAL: ing.syncedWAL,
	})
	syncs := 0
	dr.syncFile = func(path string) error {
		syncs++
		return syncSpoolFile(path)
	}

	n, err := drainAside(t, dr)
	require.NoError(t, err)
	require.Zero(t, n, "a held segment with nothing synced has nothing durable to take")
	require.False(t, a.returned(), "fixture: the Accept still waits for its WAL Sync")
	require.Equal(t, -1, opIndex(p.log(), "journal-write", req.Nonce), "no lease line before the segment's first Sync returns")
	require.Empty(t, dispatched, "and no dispatch")
	require.Zero(t, syncs, "and no sync of the drain's own: the ingest's Syncs cover a segment it holds")
	require.Equal(t, []DrainGap{{File: filepath.Base(seg), Kind: DrainGapPending, Count: 1, Reason: "spool bytes not yet replayed"}},
		dr.GapState().Gaps, "the unsynced line is pending, as a trailing incomplete line is")

	p.gate.release()
	awaitAccept(t, a)
	require.NoError(t, a.err)
	n, err = dr.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n, "once the first Sync has returned, the next pass takes the line")
	require.Equal(t, []string{req.Nonce}, dispatched)
	require.Equal(t, 1, opCount(p.log(), "journal-write", req.Nonce), "one lease line for the delivery")
	require.Zero(t, syncs, "the drain never syncs a segment the ingest holds")
}

// TestDrainReadsAHeldSegmentNoFurtherThanItsStat: a WAL batch that commits between a pass's stat of a
// held segment and its SyncedWAL question leaves the segment's synced size past the size the stat saw.
// A live session does that whenever it accepts during a pass over its segment. The pass still reads
// only what its stat counted. Reading on to the synced size carried the consumed offset past the size
// the pass records, which is progress loadState refuses, so every later Drain failed for the whole
// spool (review 1, R3). The next pass loads the first pass's progress and takes the rest.
func TestDrainReadsAHeldSegmentNoFurtherThanItsStat(t *testing.T) {
	const sess = core.SessionID("sess-past-stat")
	ing, _, root := newWALIngest(t)
	first := wireLine(t, ipc.Request{Op: ipc.OpObserveTool, Session: sess, TS: 1})
	second := wireLine(t, ipc.Request{Op: ipc.OpObserveTool, Session: sess, TS: 2})
	require.NoError(t, ing.Accept(first.req, first.line))
	seg := walPath(paths.Of(root).Spool, sess, 0)

	asked := false
	var got []core.UnixMilli
	dr := newDrainer(DrainConfig{
		Root: root, Log: logging.Nop(), Clock: newFakeClock(epoch),
		Dispatch: func(_ context.Context, r ipc.Request) ipc.Response {
			got = append(got, r.TS)
			return ipc.Response{OK: true}
		},
		IsLive:    func(core.SessionID) bool { return true },
		RemoveWAL: ing.removeDrainedWAL,
		HoldsWAL:  ing.holdsWAL,
		SyncedWAL: func(path string) (int64, bool) {
			if !asked { // a batch commits after the pass's stat and before its question
				asked = true
				require.NoError(t, ing.Accept(second.req, second.line))
				requireSynced(t, ing, seg, int64(len(first.want)+len(second.want)),
					"fixture: the synced size is past the size the pass's stat saw")
			}
			return ing.syncedWAL(path)
		},
	})

	n, err := dr.Drain(context.Background())
	require.NoError(t, err)
	require.True(t, asked, "fixture: the pass asked for the segment's synced size")
	require.Equal(t, 1, n, "the pass reads the segment as it stood at its stat")
	require.Equal(t, []core.UnixMilli{1}, got)
	n, err = dr.Drain(context.Background())
	require.NoError(t, err, "the progress the first pass persisted is consistent")
	require.Equal(t, 1, n, "the next pass takes the line the stat did not count")
	require.Equal(t, []core.UnixMilli{1, 2}, got, "every line exactly once, in order")
}

// TestDrainLeavesBytesAppendedAfterItsSyncForTheNextPass: the drain's sync of a file the ingest does not
// hold covers what the file held when the sync was issued, so the pass reads no further than its stat,
// which comes before the sync. A line a hook appends once that sync has returned is not covered, and
// waits for the next pass, which syncs the file again. The growing-file test appends during Dispatch,
// after both the stat and the sync, so it cannot tell a size taken before the sync from one taken after
// it, and a pass that re-statted after its sync, reading and leasing unsynced bytes, passed it (review
// 1, R4).
func TestDrainLeavesBytesAppendedAfterItsSyncForTheNextPass(t *testing.T) {
	root := t.TempDir()
	spool := paths.Of(root).Spool
	path := filepath.Join(spool, "client-6161.ndjson")
	first := wireLine(t, ipc.Request{Op: ipc.OpObserveTool, Session: "sess-late-append", TS: 1}).line
	second := wireLine(t, ipc.Request{Op: ipc.OpObserveTool, Session: "sess-late-append", TS: 2}).line
	require.NoError(t, os.MkdirAll(paths.Long(spool), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(path), first, 0o600))

	var got []core.UnixMilli
	dr := newDrainer(DrainConfig{Root: root, Clock: newFakeClock(epoch), Dispatch: func(_ context.Context, r ipc.Request) ipc.Response {
		got = append(got, r.TS)
		return ipc.Response{OK: true}
	}})
	syncs := 0
	dr.syncFile = func(p string) error {
		err := syncSpoolFile(p)
		syncs++
		if syncs == 1 { // a hook appends a whole line the moment the pass's sync has returned
			w, werr := paths.AppendOnly(p)
			require.NoError(t, werr)
			_, werr = w.Write(second)
			require.NoError(t, werr)
			require.NoError(t, w.Close())
		}
		return err
	}

	n, err := dr.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n, "the line appended after the sync is left for the next pass")
	require.Equal(t, []core.UnixMilli{1}, got)
	n, err = dr.Drain(context.Background())
	require.NoError(t, err, "the progress the first pass persisted is consistent")
	require.Equal(t, 1, n)
	require.Equal(t, []core.UnixMilli{1, 2}, got, "every line exactly once, in order")
	require.Equal(t, 2, syncs, "the next pass syncs the file again before it reads the appended line")
	require.NoFileExists(t, path, "fully drained, the file is removed")
}

// TestDrainFsyncsASpoolFileOnAHandleOfItsOwnBeforeItsFirstDispatch pins the call the other half of the
// rule rests on (review 1, R2). With nothing in place of the drain's sync, a pass over a spool file the
// ingest does not hold issues one fsync, on a handle of that file, before it dispatches anything of
// the file, and the fsync succeeds beside the handle a hook still has open for appending. Every other
// test replaces the sync wholesale or checks only what it returned, so a sync that opened and closed
// its handle with no fsync in between passed them all.
func TestDrainFsyncsASpoolFileOnAHandleOfItsOwnBeforeItsFirstDispatch(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	spool := paths.Of(root).Spool
	require.NoError(t, os.MkdirAll(paths.Long(spool), 0o700))
	path := filepath.Join(spool, "client-5252.ndjson")
	hook, err := paths.AppendOnly(path)
	require.NoError(t, err)
	hookClosed := false
	t.Cleanup(func() {
		if !hookClosed {
			_ = hook.Close()
		}
	})
	first := wireLine(t, ipc.Request{Op: ipc.OpObserveTool, Session: "sess-fsync", TS: 1}).line
	second := wireLine(t, ipc.Request{Op: ipc.OpObserveTool, Session: "sess-fsync", TS: 2}).line
	half := len(second) / 2
	_, err = hook.Write(first)
	require.NoError(t, err)
	_, err = hook.Write(second[:half]) // the hook is part-way through its next line
	require.NoError(t, err)

	var evs []string
	dr := newDrainer(DrainConfig{Root: root, Clock: newFakeClock(epoch), Dispatch: func(_ context.Context, r ipc.Request) ipc.Response {
		evs = append(evs, fmt.Sprint("dispatch ", r.TS))
		return ipc.Response{OK: true}
	}})
	dr.syncHandle = func(f *os.File) error {
		err := f.Sync()
		require.NoError(t, err, "the fsync on the drain's own handle, beside the hook's")
		evs = append(evs, "fsync "+f.Name())
		return err
	}
	fsync := "fsync " + paths.Long(path)

	n, err := dr.Drain(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, []string{fsync, "dispatch 1"}, evs,
		"one fsync, on a handle of the spool file, before the pass dispatches anything of it")

	_, err = hook.Write(second[half:])
	require.NoError(t, err, "the hook's handle still appends after the drain's fsync")
	hookClosed = true
	require.NoError(t, hook.Close())
	n, err = dr.Drain(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, []string{fsync, "dispatch 1", fsync, "dispatch 2"}, evs,
		"the next pass issues an fsync of its own before it reads on")
	require.NoFileExists(t, path, "fully drained and no longer open, the file is removed")
}
