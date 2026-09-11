package daemon

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// The drain must never make a lease durable before the bytes it names are durable (SP20-D1, review
// finding F6). A lease is a durable journal line: taken for a delivery whose WAL line was written but
// not yet synced, a machine crash before the WAL Sync leaves an orphan lease, a permanent open-lease
// GC root and an arrival hole for a delivery whose hook died with the machine. The tests below pin
// the two halves of the rule: a segment the ingest holds is read only up to its synced size, and any
// other file is synced by the drain before the pass consumes any of it. Every ordering comes from a
// held Sync, a seam or the pass's own sequence; the clock only bounds waits a correct build never
// reaches.

// syncedNow asks ing for the synced size of the segment at path on a goroutine of its own and waits
// for the answer, so a syncedWAL that waited for a WAL batch in flight fails the test instead of
// hanging it.
func syncedNow(t *testing.T, ing *ingest, path string) (int64, bool) {
	t.Helper()
	done := make(chan struct{})
	var size int64
	var held bool
	go func() {
		defer close(done)
		size, held = ing.syncedWAL(path)
	}()
	awaitClosed(t, done, "an answer from syncedWAL")
	return size, held
}

// requireSynced requires ing to hold the segment at path with a synced size of want.
func requireSynced(t *testing.T, ing *ingest, path string, want int64, why string) {
	t.Helper()
	got, held := syncedNow(t, ing, path)
	require.Truef(t, held, "%s: the ingest holds the segment", why)
	require.Equalf(t, want, got, "%s", why)
}

// requireNotHeld requires ing not to hold the segment at path.
func requireNotHeld(t *testing.T, ing *ingest, path, why string) {
	t.Helper()
	_, held := syncedNow(t, ing, path)
	require.Falsef(t, held, "%s", why)
}

// drainAside runs one pass of dr on a goroutine of its own and waits for it, so a pass that waited
// for a WAL batch in flight fails the test instead of hanging it.
func drainAside(t *testing.T, dr *drainer) (int, error) {
	t.Helper()
	done := make(chan struct{})
	var n int
	var err error
	go func() {
		defer close(done)
		n, err = dr.Drain(context.Background())
	}()
	awaitClosed(t, done, "a drain pass while a WAL Sync is held")
	return n, err
}

// wireLine is req as Accept receives it and as the WAL must hold it: its wire line, which already
// ends in the one terminator.
func wireLine(t *testing.T, req ipc.Request) walReq {
	t.Helper()
	line, err := ipc.EncodeRequest(req)
	require.NoError(t, err)
	return walReq{req: req, line: line, want: line}
}

// leasedRequest is an admitted observe.tool delivery from sess carrying a fresh nonce, so the drain
// and the ingest can both lease it.
func leasedRequest(t *testing.T, sess core.SessionID) ipc.Request {
	t.Helper()
	nonce, err := ipc.NewDeliveryNonce()
	require.NoError(t, err)
	return observeRequest(nonce, string(sess), `{"hook_event_name":"PostToolUse"}`)
}

// writeRequestLines writes reqs to the file at path exactly as the WAL and the hook client write
// them: each request's wire line, terminator included, and nothing else.
func writeRequestLines(t *testing.T, path string, reqs ...ipc.Request) {
	t.Helper()
	var buf []byte
	for _, r := range reqs {
		buf = append(buf, wireLine(t, r).line...)
	}
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(path)), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(path), buf, 0o600))
}

// opIndex returns the index in log of the first event of kind about what, or -1.
func opIndex(log []walOp, kind, what string) int {
	return slices.IndexFunc(log, func(op walOp) bool { return op.kind == kind && op.what == what })
}

// opCount returns how many events of log are of kind about what.
func opCount(log []walOp, kind, what string) int {
	n := 0
	for _, op := range log {
		if op.kind == kind && op.what == what {
			n++
		}
	}
	return n
}

// logLeaseWrites routes journal's lease Writes through p's log: each Write that carries one of
// nonces is logged as a "journal-write" of it once the Write has returned.
func logLeaseWrites(journal *deliveryJournal, p *walProbe, nonces ...string) {
	journal.writer = leaseFaultWriter{file: journal.file, write: func(b []byte) (int, error) {
		n, err := journal.file.Write(b)
		for _, nonce := range nonces {
			if bytes.Contains(b, []byte(nonce)) {
				p.add(walOp{kind: "journal-write", what: nonce})
			}
		}
		return n, err
	}}
}

// TestIngest_SyncedWALAdvancesOnlyPastASyncThatReturned pins the synced size the ingest publishes
// for each segment it holds: the bound a drain pass reads such a segment to (DrainConfig.SyncedWAL).
func TestIngest_SyncedWALAdvancesOnlyPastASyncThatReturned(t *testing.T) {
	const sess = core.SessionID("synced")

	t.Run("a batch in flight adds nothing until its Sync returns", func(t *testing.T) {
		ing, p, root := newWALIngest(t)
		seg := walPath(paths.Of(root).Spool, sess, 0)
		requireNotHeld(t, ing, seg, "a segment never opened is not held")

		held := newWALGate(t)
		p.onSync = func(call int, _ string, f *os.File) error {
			if call == 2 {
				held.hold()
			}
			return f.Sync()
		}
		first, second := newWALReq(t, sess, 0), newWALReq(t, sess, 1)
		a := goAccept(ing, p, 0, first)
		awaitAccept(t, a)
		require.NoError(t, a.err)
		requireSynced(t, ing, seg, int64(len(first.want)), "the first line's Sync returned")

		b := goAccept(ing, p, 1, second)
		awaitClosed(t, held.entered, "the Sync covering the second line")
		require.Equal(t, string(first.want)+string(second.want), readWAL(t, root, sess, 0), "fixture: the second line is written")
		requireSynced(t, ing, seg, int64(len(first.want)),
			"asked while the batch holds ingest.mu through its Sync: the answer does not wait, and the line is not synced yet")

		held.release()
		awaitAccept(t, b)
		require.NoError(t, b.err)
		requireSynced(t, ing, seg, int64(len(first.want)+len(second.want)), "the second line's Sync returned")
	})

	t.Run("a failed Sync freezes it until a segment is opened", func(t *testing.T) {
		ing, p, root := newWALIngest(t)
		errSync := errors.New("synced: injected WAL sync fault")
		p.onSync = func(call int, _ string, f *os.File) error {
			if call == 2 {
				return errSync
			}
			return f.Sync()
		}
		seg0, seg1 := walPath(paths.Of(root).Spool, sess, 0), walPath(paths.Of(root).Spool, sess, 1)
		reqs := newWALReqs(t, 0, sess, sess, sess)
		accept := func(k int, r walReq) error {
			a := goAccept(ing, p, k, r)
			awaitAccept(t, a)
			require.Nilf(t, a.recovered, "request %d", k)
			return a.err
		}

		require.NoError(t, accept(0, reqs[0]))
		committed := int64(len(reqs[0].want))
		requireSynced(t, ing, seg0, committed, "the first line's Sync returned")
		require.ErrorIs(t, accept(1, reqs[1]), errSync)
		requireSynced(t, ing, seg0, committed, "a Sync that failed does not advance it")
		require.NoError(t, accept(2, reqs[2]), "the ingest goes on appending after a failed Sync, as a sequential append did")
		require.Equal(t, wantsOn(reqs, sess), readWAL(t, root, sess, 0), "fixture: the segment holds all three lines")
		requireSynced(t, ing, seg0, committed,
			"a Sync that returned nil after a failed one does not show the failed one's bytes reached the disk")

		ing.mu.Lock()
		ing.wals[sess].bytes = walRotateBytes - 1 // the next line rolls over to segment 1
		ing.mu.Unlock()
		next := newWALReq(t, sess, len(reqs))
		require.NoError(t, accept(len(reqs), next))
		requireNotHeld(t, ing, seg0, "a segment rotated away is not held")
		requireSynced(t, ing, seg1, int64(len(next.want)), "the segment rotation opened starts with no failed Sync")
	})

	t.Run("a segment closed by CloseSession or Close is not held", func(t *testing.T) {
		const other = core.SessionID("synced-other")
		ing, p, root := newWALIngest(t)
		for k, s := range []core.SessionID{sess, other} {
			a := goAccept(ing, p, k, newWALReq(t, s, k))
			awaitAccept(t, a)
			require.NoError(t, a.err)
		}
		seg, otherSeg := walPath(paths.Of(root).Spool, sess, 0), walPath(paths.Of(root).Spool, other, 0)
		requireSynced(t, ing, seg, int64(walTestLineSize+1), "fixture: the session's line is synced")

		require.NoError(t, ing.CloseSession(sess))
		requireNotHeld(t, ing, seg, "CloseSession closes the segment")
		requireSynced(t, ing, otherSeg, int64(walTestLineSize+1), "another session's segment stays held")
		require.NoError(t, ing.Close())
		requireNotHeld(t, ing, otherSeg, "Close closes every segment")
	})

	t.Run("a segment opened over bytes it did not write counts none of them synced", func(t *testing.T) {
		ing, p, root := newWALIngest(t)
		// A crashed process left a line in the segment, and may never have synced it.
		left := newWALReq(t, sess, 0)
		seg := walPath(paths.Of(root).Spool, sess, 0)
		require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(seg)), 0o700))
		require.NoError(t, os.WriteFile(paths.Long(seg), left.want, 0o600))

		p.gate = newWALGate(t)
		next := newWALReq(t, sess, 1)
		a := goAccept(ing, p, 1, next)
		awaitClosed(t, p.gate.entered, "the Sync covering the new line")
		requireSynced(t, ing, seg, 0, "reopened, the segment counts nothing synced, not even the bytes it found there")
		p.gate.release()
		awaitAccept(t, a)
		require.NoError(t, a.err)
		requireSynced(t, ing, seg, int64(len(left.want)+len(next.want)), "the first Sync that returns covers the bytes it found too")
	})

	t.Run("a Sync that panics freezes it", func(t *testing.T) {
		ing, p, root := newWALIngest(t)
		seg := walPath(paths.Of(root).Spool, sess, 0)
		p.onSync = func(call int, name string, f *os.File) error {
			if call == 2 {
				panic(walTestPanic{seg: name})
			}
			return f.Sync()
		}
		reqs := newWALReqs(t, 0, sess, sess, sess)
		accepts := make([]*walAccept, len(reqs))
		for k := range reqs {
			accepts[k] = goAccept(ing, p, k, reqs[k])
			awaitAccept(t, accepts[k])
		}
		require.NoError(t, accepts[0].err)
		require.Equal(t, walTestPanic{seg: segName(sess, 0)}, accepts[1].recovered, "fixture: the second line's Sync panicked")
		require.NoError(t, accepts[2].err)
		requireSynced(t, ing, seg, int64(len(reqs[0].want)),
			"neither a Sync that panicked nor a Sync that returned nil after it advances it")
	})
}

// TestDrainNeverLeasesAWALLineBeforeItsSyncReturns is review finding F6's reproducer, inverted. The
// review held a WAL batch's Sync and ran a drain pass meanwhile, and the pass leased, published and
// acknowledged the delivery whose line the batch had written: a durable lease for bytes that were
// not durable.
func TestDrainNeverLeasesAWALLineBeforeItsSyncReturns(t *testing.T) {
	ctx := context.Background()

	t.Run("wired as the daemon wires it, a pass takes nothing past the synced size", func(t *testing.T) {
		const sess = core.SessionID("sess-held-sync")
		dd, _ := liveWALDaemon(t, nil)
		lock := lockFor(t, dd, dd.root)
		t.Cleanup(func() { _ = lock.Release() })
		journal, err := dd.deliveryJournal()
		require.NoError(t, err)

		p := newWALProbe(dd.ing)
		held := newWALGate(t)
		p.onSync = func(call int, _ string, f *os.File) error {
			if call == 2 {
				held.hold()
			}
			return f.Sync()
		}
		committed, inFlight := leasedRequest(t, sess), leasedRequest(t, sess)
		logLeaseWrites(journal, p, committed.Nonce, inFlight.Nonce)

		cfg := dd.drainConfig()
		var dispatched []string // appended to by the pass, read once it has returned
		cfg.Dispatch = func(_ context.Context, r ipc.Request) ipc.Response {
			dispatched = append(dispatched, r.Nonce)
			return ipc.Response{OK: true}
		}
		dr := newDrainer(cfg)
		dr.syncFile = func(path string) error {
			p.add(walOp{kind: "drain-sync", what: filepath.Base(path)})
			return syncSpoolFile(path)
		}
		seg := filepath.Base(walPath(paths.Of(dd.root).Spool, sess, 0))

		c := wireLine(t, committed)
		a0 := goAccept(dd.ing, p, 0, c)
		awaitAccept(t, a0)
		require.NoError(t, a0.err)
		n, err := dr.Drain(ctx)
		require.NoError(t, err)
		require.Equal(t, 1, n, "fixture: the committed delivery drains")

		f := wireLine(t, inFlight)
		a1 := goAccept(dd.ing, p, 1, f)
		awaitClosed(t, held.entered, "the WAL Sync covering the in-flight delivery's line")
		require.Equal(t, string(c.want)+string(f.want), readWAL(t, dd.root, sess, 0),
			"fixture: the in-flight line is written, past the segment's synced size")

		n, err = drainAside(t, dr)
		require.NoError(t, err)
		require.Zero(t, n, "a pass while the WAL Sync is held takes nothing past the synced size")
		require.False(t, a1.returned(), "fixture: the Accept still waits for its WAL Sync")
		log := p.log()
		require.Equal(t, -1, opIndex(log, "journal-write", inFlight.Nonce),
			"no lease line for a delivery whose WAL Sync has not returned")
		require.NotContains(t, dispatched, inFlight.Nonce, "and no dispatch")
		st, err := dr.loadState()
		require.NoError(t, err)
		require.Equal(t, int64(len(c.want)), st[seg].Offset, "nothing past the synced size is consumed")
		require.Equal(t, []DrainGap{{File: seg, Kind: DrainGapPending, Count: 1, Reason: "spool bytes not yet replayed"}},
			dr.GapState().Gaps, "the unsynced line is pending, as a trailing incomplete line is")

		held.release()
		awaitAccept(t, a1)
		require.NoError(t, a1.err)
		n, err = dr.Drain(ctx)
		require.NoError(t, err)
		require.Equal(t, 1, n, "once the Sync has returned, the next pass takes the line")
		n, err = dr.Drain(ctx)
		require.NoError(t, err)
		require.Zero(t, n)
		require.Equal(t, []string{committed.Nonce, inFlight.Nonce}, dispatched, "each delivery is dispatched exactly once")

		log = p.log()
		require.Equal(t, 1, opCount(log, "journal-write", inFlight.Nonce), "one lease line for the delivery")
		covered := slices.IndexFunc(log, func(op walOp) bool { return op.kind == "sync" && op.call == 2 && op.err == nil })
		require.GreaterOrEqual(t, covered, 0, "fixture: the Sync covering the in-flight line returned")
		require.Less(t, covered, opIndex(log, "journal-write", inFlight.Nonce),
			"the lease line follows the WAL Sync covering the delivery")
		require.Equal(t, -1, slices.IndexFunc(log, func(op walOp) bool { return op.kind == "drain-sync" }),
			"the drain syncs nothing of a segment the ingest holds: the ingest's Syncs cover it")
	})

	t.Run("a drainer with no ingest behind it syncs the segment before it leases", func(t *testing.T) {
		const sess = core.SessionID("sess-bare-drainer")
		root, lock, journal := newTestDeliveryJournal(t)
		ing := newIngest(root, config.Defaults(), logging.Nop(), nil, newFakeClock(epoch))
		t.Cleanup(func() { _ = ing.Close() })
		ing.journal = lock.openDeliveryJournal
		p := newWALProbe(ing)
		p.gate = newWALGate(t)
		req := leasedRequest(t, sess)
		logLeaseWrites(journal, p, req.Nonce)

		a := goAccept(ing, p, 0, wireLine(t, req))
		awaitClosed(t, p.gate.entered, "the WAL Sync covering the delivery's line")

		dr := newDrainer(DrainConfig{
			Root: root, Log: logging.Nop(), Clock: newFakeClock(epoch),
			Dispatch: func(context.Context, ipc.Request) ipc.Response { return ipc.Response{OK: true} },
			Seen:     ing.seen,
			Journal:  lock.openDeliveryJournal,
			IsLive:   func(core.SessionID) bool { return true },
		})
		dr.syncFile = func(path string) error {
			err := syncSpoolFile(path)
			p.add(walOp{kind: "drain-sync", what: filepath.Base(path), err: err})
			return err
		}
		n, err := drainAside(t, dr)
		require.NoError(t, err)
		require.Equal(t, 1, n, "a drainer that does not know the ingest takes the line")
		require.False(t, a.returned(), "fixture: the Accept still waits for its WAL Sync")

		log := p.log()
		seg := segName(sess, 0)
		synced := slices.IndexFunc(log, func(op walOp) bool { return op.kind == "drain-sync" && op.what == seg && op.err == nil })
		require.GreaterOrEqual(t, synced, 0, "the drain synced the segment itself")
		require.Equal(t, 1, opCount(log, "drain-sync", seg), "once in the pass")
		require.Less(t, synced, opIndex(log, "journal-write", req.Nonce),
			"the lease line follows the drain's own sync, which covered the line")

		p.gate.release()
		awaitAccept(t, a)
		require.NoError(t, a.err)
		require.Equal(t, 1, opCount(p.log(), "journal-write", req.Nonce), "the Accept takes the drain's lease back")
	})
}

// durEvent is one event in a durEvents log: a spool file's sync, or a lease journal Write.
type durEvent struct {
	kind string // "sync" or "lease"
	file string // the base name of the file synced, or of the file holding the delivery leased
	err  error  // sync: its result
}

// durEvents is an ordered log of the syncs and lease Writes of a drain pass, each logged once its
// call has returned.
type durEvents struct {
	mu  sync.Mutex
	evs []durEvent
}

func (l *durEvents) add(ev durEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.evs = append(l.evs, ev)
}

// take returns every event so far and empties the log.
func (l *durEvents) take() []durEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := l.evs
	l.evs = nil
	return out
}

// requireSyncedBeforeLeases requires evs to sync each file of want exactly once and no other, and
// each such file's sync to precede the first lease Write of a delivery the file holds.
func requireSyncedBeforeLeases(t *testing.T, evs []durEvent, want ...string) {
	t.Helper()
	var synced []string
	for _, ev := range evs {
		if ev.kind == "sync" {
			require.NoErrorf(t, ev.err, "the sync of %s", ev.file)
			synced = append(synced, ev.file)
		}
	}
	require.ElementsMatch(t, want, synced, "one sync per file with unread bytes the ingest does not hold, and no other")
	for _, file := range want {
		s := slices.IndexFunc(evs, func(ev durEvent) bool { return ev.kind == "sync" && ev.file == file })
		l := slices.IndexFunc(evs, func(ev durEvent) bool { return ev.kind == "lease" && ev.file == file })
		require.GreaterOrEqualf(t, l, 0, "fixture: a delivery of %s was leased", file)
		require.Lessf(t, s, l, "%s is synced before the first lease Write of a delivery it holds", file)
	}
}

// TestDrainSyncsAFileTheIngestDoesNotHoldBeforeItsFirstLease pins the other half of the rule: a spool
// file the ingest does not hold is made durable by the drain, once per pass, before the pass takes
// its first lease from it. The ingest has no journal, so each lease line below is the drain's.
func TestDrainSyncsAFileTheIngestDoesNotHoldBeforeItsFirstLease(t *testing.T) {
	ctx := context.Background()
	root, lock, journal := newTestDeliveryJournal(t)
	ing := newIngest(root, config.Defaults(), logging.Nop(), nil, newFakeClock(epoch))
	t.Cleanup(func() { _ = ing.Close() })
	spool := paths.Of(root).Spool

	fileOf := map[string]string{} // nonce -> base name of the file holding the delivery
	accept := func(req ipc.Request) {
		require.NoError(t, ing.Accept(req, wireLine(t, req).line))
		fileOf[req.Nonce] = ""
	}
	place := func(file string, reqs ...ipc.Request) {
		for _, r := range reqs {
			fileOf[r.Nonce] = file
		}
	}

	// A segment this daemon closed, one it rotated away and the one rotation opened, which it holds.
	closed := leasedRequest(t, "sess-closed")
	accept(closed)
	require.NoError(t, ing.CloseSession("sess-closed"))
	place("wal-sess-closed.ndjson", closed)
	rot0, rot1 := leasedRequest(t, "sess-rotated"), leasedRequest(t, "sess-rotated")
	accept(rot0)
	ing.mu.Lock()
	ing.wals["sess-rotated"].bytes = walRotateBytes - 1 // the next line rolls over to segment 1
	ing.mu.Unlock()
	accept(rot1)
	place("wal-sess-rotated.ndjson", rot0)
	place("wal-sess-rotated.1.ndjson", rot1)
	// A segment a crashed process left, and a hook client's spool: neither was ever synced.
	crashed := []ipc.Request{leasedRequest(t, "sess-crashed"), leasedRequest(t, "sess-crashed")}
	writeRequestLines(t, filepath.Join(spool, "wal-sess-crashed.ndjson"), crashed...)
	place("wal-sess-crashed.ndjson", crashed...)
	client := []ipc.Request{leasedRequest(t, "sess-client"), leasedRequest(t, "sess-client")}
	writeRequestLines(t, filepath.Join(spool, "client-4242.ndjson"), client...)
	place("client-4242.ndjson", client...)

	var evs durEvents
	journal.writer = leaseFaultWriter{file: journal.file, write: func(b []byte) (int, error) {
		n, err := journal.file.Write(b)
		for nonce, file := range fileOf {
			if bytes.Contains(b, []byte(nonce)) {
				evs.add(durEvent{kind: "lease", file: file})
			}
		}
		return n, err
	}}
	dr := newDrainer(DrainConfig{
		Root: root, Log: logging.Nop(), Clock: newFakeClock(epoch),
		Dispatch:  func(context.Context, ipc.Request) ipc.Response { return ipc.Response{OK: true} },
		Journal:   lock.openDeliveryJournal,
		IsLive:    func(core.SessionID) bool { return true },
		RemoveWAL: ing.removeDrainedWAL,
		HoldsWAL:  ing.holdsWAL,
		SyncedWAL: ing.syncedWAL,
	})
	dr.syncFile = func(path string) error {
		err := syncSpoolFile(path)
		evs.add(durEvent{kind: "sync", file: filepath.Base(path), err: err})
		return err
	}

	n, err := dr.Drain(ctx)
	require.NoError(t, err)
	require.Equal(t, len(fileOf), n, "every delivery drains")
	pass := evs.take()
	requireSyncedBeforeLeases(t, pass, "wal-sess-closed.ndjson", "wal-sess-rotated.ndjson", "wal-sess-crashed.ndjson", "client-4242.ndjson")
	require.True(t, slices.ContainsFunc(pass, func(ev durEvent) bool { return ev.kind == "lease" && ev.file == "wal-sess-rotated.1.ndjson" }),
		"the held segment's line is leased without a sync of the drain's: the ingest's Sync covered it before the pass")
	leases := 0
	for _, ev := range pass {
		if ev.kind == "lease" {
			leases++
		}
	}
	require.Equal(t, len(fileOf), leases, "one lease line per delivery")

	// Another pass syncs only a file with something unread.
	late := leasedRequest(t, "sess-late")
	writeRequestLines(t, filepath.Join(spool, "client-4243.ndjson"), late)
	place("client-4243.ndjson", late)
	n, err = dr.Drain(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	requireSyncedBeforeLeases(t, evs.take(), "client-4243.ndjson")
}

// TestDrainLeasesNothingFromAFileItCouldNotSync: a file whose sync fails is not consumed at all in
// that pass. None of its deliveries is leased or dispatched and its offset stays where it was; the
// failure is a gap and a file error, the other files still drain, and the next pass takes the file's
// deliveries once each.
func TestDrainLeasesNothingFromAFileItCouldNotSync(t *testing.T) {
	ctx := context.Background()
	root, lock, journal := newTestDeliveryJournal(t)
	spool := paths.Of(root).Spool
	const badName, goodName = "client-1.ndjson", "client-2.ndjson"
	bad := []ipc.Request{leasedRequest(t, "sess-bad"), leasedRequest(t, "sess-bad")}
	good := leasedRequest(t, "sess-good")
	writeRequestLines(t, filepath.Join(spool, badName), bad...)
	writeRequestLines(t, filepath.Join(spool, goodName), good)

	leased := map[string]int{}
	journal.writer = leaseFaultWriter{file: journal.file, write: func(b []byte) (int, error) {
		for _, r := range append([]ipc.Request{good}, bad...) {
			if bytes.Contains(b, []byte(r.Nonce)) {
				leased[r.Nonce]++
			}
		}
		return journal.file.Write(b)
	}}
	var dispatched []string
	clk := newFakeClock(epoch)
	m := obs.New(clk)
	dr := newDrainer(DrainConfig{
		Root: root, Log: logging.Nop(), Metrics: m, Clock: clk, Journal: lock.openDeliveryJournal,
		Dispatch: func(_ context.Context, r ipc.Request) ipc.Response {
			dispatched = append(dispatched, r.Nonce)
			return ipc.Response{OK: true}
		},
	})
	errSync := errors.New("drain: injected spool sync fault")
	failing := true
	dr.syncFile = func(path string) error {
		if failing && filepath.Base(path) == badName {
			return errSync
		}
		return syncSpoolFile(path)
	}

	n, err := dr.Drain(ctx)
	require.ErrorIs(t, err, errSync, "the pass reports the file it could not make durable")
	require.Equal(t, 1, n, "the other file drains")
	require.Equal(t, []string{good.Nonce}, dispatched, "nothing of the unsynced file is dispatched")
	require.Equal(t, map[string]int{good.Nonce: 1}, leased, "nothing of the unsynced file is leased")
	st, err := dr.loadState()
	require.NoError(t, err)
	require.Equal(t, &drainFileState{}, st[badName], "nothing of the unsynced file is consumed")
	require.FileExists(t, filepath.Join(spool, badName))
	require.Equal(t, DrainGap{File: badName, Kind: DrainGapUnsynced, Count: 1, Reason: "spool bytes could not be made durable"},
		gapOfKind(t, dr.GapState(), DrainGapUnsynced))
	require.False(t, dr.GapState().Complete)
	require.EqualValues(t, 1, m.Counter(counterDrainFileError).Value(), "the failed sync is a file error")

	failing = false
	n, err = dr.Drain(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, n, "the next pass takes the file's deliveries")
	require.Equal(t, []string{good.Nonce, bad[0].Nonce, bad[1].Nonce}, dispatched, "each delivery is dispatched once")
	require.Equal(t, map[string]int{good.Nonce: 1, bad[0].Nonce: 1, bad[1].Nonce: 1}, leased, "and leased once")
	require.NoFileExists(t, filepath.Join(spool, badName))
	require.True(t, dr.GapState().Complete)
}

// TestDrainSyncsASpoolFileAHookStillHasOpen runs the real sync, with no fault seam, against a client
// spool a hook process still has open for appending, as ipc's spool opens it. FlushFileBuffers needs
// a handle with write access on Windows, so the sync opens one for appending while the hook's own
// handle is open; the two must share the file, and the hook must go on appending afterwards.
func TestDrainSyncsASpoolFileAHookStillHasOpen(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	spool := paths.Of(root).Spool
	require.NoError(t, os.MkdirAll(paths.Long(spool), 0o700))
	path := filepath.Join(spool, "client-5151.ndjson")
	hook, err := paths.AppendOnly(path)
	require.NoError(t, err)
	hookClosed := false
	t.Cleanup(func() {
		if !hookClosed {
			_ = hook.Close()
		}
	})
	first := wireLine(t, ipc.Request{Op: ipc.OpObserveTool, Session: "sess-hook", TS: 1}).line
	second := wireLine(t, ipc.Request{Op: ipc.OpObserveTool, Session: "sess-hook", TS: 2}).line
	half := len(second) / 2
	_, err = hook.Write(first)
	require.NoError(t, err)
	_, err = hook.Write(second[:half]) // the hook is part-way through its next line
	require.NoError(t, err)

	var got []core.UnixMilli
	var syncs []error
	dr := newDrainer(DrainConfig{Root: root, Clock: newFakeClock(epoch), Dispatch: func(_ context.Context, r ipc.Request) ipc.Response {
		got = append(got, r.TS)
		return ipc.Response{OK: true}
	}})
	dr.syncFile = func(p string) error {
		err := syncSpoolFile(p)
		syncs = append(syncs, err)
		return err
	}

	n, err := dr.Drain(ctx)
	require.NoError(t, err, "the drain syncs a spool file a hook still has open for appending")
	require.Equal(t, 1, n)
	require.Equal(t, []error{nil}, syncs, "one real sync, and it succeeded beside the hook's handle")
	_, err = hook.Write(second[half:])
	require.NoError(t, err, "the hook's handle still appends after the drain synced the file")
	hookClosed = true
	require.NoError(t, hook.Close())

	n, err = dr.Drain(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, []error{nil, nil}, syncs, "the next pass syncs the file once more")
	require.Equal(t, []core.UnixMilli{1, 2}, got, "every line exactly once, in order")
	require.NoFileExists(t, path, "fully drained and no longer open, the file is removed")
}
