package daemon

import (
	"context"
	"errors"
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

// joinLines is parts, one after another, in a slice of its own.
func joinLines(parts ...[]byte) []byte {
	var b []byte
	for _, p := range parts {
		b = append(b, p...)
	}
	return b
}

// TestDrainCountsTheBytesOfAFileItCouldNotSyncAsPending: a file whose sync failed keeps its stat size out
// of the progress the pass persists, and rightly: that progress would otherwise name bytes a machine
// crash can still take. Its unread bytes are pending all the same, and PendingBytes is what the
// SessionEnd flush records in a session's recovery marker, so the pass counts them (review 1, R6). Each
// unread byte is counted once: the bytes the file's recorded size already puts in the total are not
// counted again when the file grows past that size and then fails its sync.
func TestDrainCountsTheBytesOfAFileItCouldNotSyncAsPending(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	spool := paths.Of(root).Spool
	const freshName, resumedName = "client-7171.ndjson", "client-7272.ndjson"
	fresh, resumed := filepath.Join(spool, freshName), filepath.Join(spool, resumedName)
	line := func(ts core.UnixMilli) []byte {
		return wireLine(t, ipc.Request{Op: ipc.OpObserveTool, Session: "sess-pending", TS: ts}).line
	}
	a1, a2, b1, b2, b3 := line(1), line(2), line(3), line(4), line(5)
	half := len(b2) / 2
	require.NoError(t, os.MkdirAll(paths.Long(spool), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(fresh), joinLines(a1, a2), 0o600))
	require.NoError(t, os.WriteFile(paths.Long(resumed), joinLines(b1, b2[:half]), 0o600))

	errSync := errors.New("drain: injected spool sync fault")
	failing := map[string]bool{freshName: true}
	var got []core.UnixMilli
	dr := newDrainer(DrainConfig{
		Root: root, Log: newRecordingLogger(), Clock: newFakeClock(epoch),
		Dispatch: func(_ context.Context, r ipc.Request) ipc.Response {
			got = append(got, r.TS)
			return ipc.Response{OK: true}
		},
	})
	dr.syncFile = func(p string) error {
		if failing[filepath.Base(p)] {
			return errSync
		}
		return syncSpoolFile(p)
	}
	resumedProgress := &drainFileState{Size: int64(len(b1) + half), Offset: int64(len(b1))}

	n, err := dr.Drain(ctx)
	require.ErrorIs(t, err, errSync)
	require.Equal(t, 1, n, "the other file's whole line drains")
	require.Equal(t, int64(len(a1)+len(a2)+half), dr.GapState().PendingBytes,
		"the unsynced file's unread bytes, and the other file's trailing half line")
	st, err := dr.loadState()
	require.NoError(t, err)
	require.Equal(t, &drainFileState{}, st[freshName], "the unsynced file's size is not recorded")
	require.Equal(t, resumedProgress, st[resumedName], "fixture: the other file's progress records its half line")

	// That file grows past its recorded size, and now its sync fails too.
	w, err := paths.AppendOnly(resumed)
	require.NoError(t, err)
	_, err = w.Write(joinLines(b2[half:], b3))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	failing[resumedName] = true
	n, err = dr.Drain(ctx)
	require.ErrorIs(t, err, errSync)
	require.Zero(t, n)
	require.Equal(t, int64(len(a1)+len(a2)+len(b2)+len(b3)), dr.GapState().PendingBytes,
		"every unread byte once: the half line the recorded size counts is not counted again")
	st, err = dr.loadState()
	require.NoError(t, err)
	require.Equal(t, resumedProgress, st[resumedName], "the size the failed pass saw is not recorded")

	clear(failing)
	n, err = dr.Drain(ctx)
	require.NoError(t, err)
	require.Equal(t, 4, n, "once both sync, the next pass takes every line left")
	require.Equal(t, []core.UnixMilli{3, 1, 2, 4, 5}, got, "each line exactly once")
	require.Zero(t, dr.GapState().PendingBytes)
	for _, g := range dr.GapState().Gaps {
		require.Equalf(t, DrainGapUnleased, g.Kind,
			"nothing is left unsynced or pending; these requests carry no nonce, so each is unleased: %+v", g)
	}
}

// TestDrainSyncsTheSpoolDirectoryOncePerPassBeforeItConsumesASyncedFile: on POSIX a file's fsync covers
// its bytes, not the directory entry that names it, and the hook client never syncs the directory it
// creates its spool file in. So a lease made durable after the drain's file sync could still name a
// delivery whose whole file a machine crash takes (review 1, R7; the WAL's own half is the design's
// R16). A pass syncs the spool directory once, after its first file sync and before it consumes
// anything of that file. That one sync covers every file the pass listed, since each existed before
// the listing. A pass that syncs no file, whether it reads a held segment or finds nothing new, syncs
// no directory. A directory sync that fails leaves its file unconsumed, as a failed file sync does,
// and the pass's next file sync tries the directory again.
func TestDrainSyncsTheSpoolDirectoryOncePerPassBeforeItConsumesASyncedFile(t *testing.T) {
	const sess = core.SessionID("sess-dir")
	ctx := context.Background()
	ing, _, root := newWALIngest(t)
	spool := paths.Of(root).Spool
	seg := segName(sess, 0)
	fileOf := map[core.UnixMilli]string{}
	accept := func(ts core.UnixMilli) {
		r := wireLine(t, ipc.Request{Op: ipc.OpObserveTool, Session: sess, TS: ts})
		require.NoError(t, ing.Accept(r.req, r.line))
		fileOf[ts] = seg
	}
	put := func(name string, ts ...core.UnixMilli) {
		reqs := make([]ipc.Request, 0, len(ts))
		for _, x := range ts {
			reqs = append(reqs, ipc.Request{Op: ipc.OpObserveTool, Session: "sess-client", TS: x})
			fileOf[x] = name
		}
		writeRequestLines(t, filepath.Join(spool, name), reqs...)
	}

	var evs []string
	errDir := errors.New("drain: injected directory sync fault")
	failDir := 0 // how many directory syncs are still to fail
	dr := newDrainer(DrainConfig{
		Root: root, Log: newRecordingLogger(), Clock: newFakeClock(epoch),
		Dispatch: func(_ context.Context, r ipc.Request) ipc.Response {
			evs = append(evs, "dispatch "+fileOf[r.TS])
			return ipc.Response{OK: true}
		},
		IsLive:    func(core.SessionID) bool { return true },
		RemoveWAL: ing.removeDrainedWAL,
		HoldsWAL:  ing.holdsWAL,
		SyncedWAL: ing.syncedWAL,
	})
	dr.syncFile = func(p string) error {
		evs = append(evs, "sync "+filepath.Base(p))
		return syncSpoolFile(p)
	}
	dr.syncDir = func(dir string) error {
		require.Equal(t, spool, dir, "the directory the spool files are in")
		evs = append(evs, "sync-dir")
		if failDir > 0 {
			failDir--
			return errDir
		}
		return paths.SyncDir(dir)
	}
	pass := func(wantN int) []string {
		t.Helper()
		evs = nil
		n, err := dr.Drain(ctx)
		require.NoError(t, err)
		require.Equal(t, wantN, n)
		return evs
	}

	accept(1)
	put("client-1.ndjson", 2, 3)
	put("client-2.ndjson", 4)
	require.Equal(t, []string{
		"dispatch " + seg,
		"sync client-1.ndjson", "sync-dir", "dispatch client-1.ndjson", "dispatch client-1.ndjson",
		"sync client-2.ndjson", "dispatch client-2.ndjson",
	}, pass(4), "one directory sync, once the pass has synced a file and before it consumes any of it")

	accept(5)
	require.Equal(t, []string{"dispatch " + seg}, pass(1), "a pass over a held segment alone syncs no directory")
	require.Empty(t, pass(0), "nor does a pass that finds nothing new")

	put("client-3.ndjson", 6)
	require.Equal(t, []string{"sync client-3.ndjson", "sync-dir", "dispatch client-3.ndjson"}, pass(1),
		"every pass that syncs a file syncs the directory again")

	put("client-4.ndjson", 7)
	put("client-5.ndjson", 8)
	failDir = 1
	evs = nil
	n, err := dr.Drain(ctx)
	require.ErrorIs(t, err, errDir, "the pass reports the file whose directory it could not sync")
	require.Equal(t, 1, n)
	require.Equal(t, []string{
		"sync client-4.ndjson", "sync-dir",
		"sync client-5.ndjson", "sync-dir", "dispatch client-5.ndjson",
	}, evs, "a failed directory sync consumes nothing of its file, and the next file's sync retries it")
	require.Equal(t, DrainGap{File: "client-4.ndjson", Kind: DrainGapUnsynced, Count: 1, Reason: "spool bytes could not be made durable"},
		gapOfKind(t, dr.GapState(), DrainGapUnsynced))
	st, err := dr.loadState()
	require.NoError(t, err)
	require.Equal(t, &drainFileState{}, st["client-4.ndjson"], "nothing of the file is consumed")
	require.Equal(t, []string{"sync client-4.ndjson", "sync-dir", "dispatch client-4.ndjson"}, pass(1),
		"the next pass takes it")
}

// kvValueOf returns the value logged under key in kv, a logger's alternating keys and values, or nil.
func kvValueOf(kv []any, key string) any {
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i] == key {
			return kv[i+1]
		}
	}
	return nil
}

// TestDrainAnnouncesAFileItCannotSyncOncePerFailure: a spool file the drain can read but not sync, such
// as one it may not open for writing, used to drain and now never does. Its sync fails on every pass,
// and the SessionEnd flush never clears that session's recovery marker. Drain warns of every failed
// file on every pass, so a hole that permanent is announced Loud, once per file, where it cannot hide
// among the warnings (review 1, R8). A sync that succeeds ends the failure, and a later failure of the
// same file is announced again.
func TestDrainAnnouncesAFileItCannotSyncOncePerFailure(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	spool := paths.Of(root).Spool
	keptPath := filepath.Join(spool, "client-8181.ndjson")
	gonePath := filepath.Join(spool, "client-8282.ndjson")
	line := func(ts core.UnixMilli) []byte {
		return wireLine(t, ipc.Request{Op: ipc.OpObserveTool, Session: "sess-loud", TS: ts}).line
	}
	k1, k2 := line(1), line(2)
	half := len(k2) / 2
	require.NoError(t, os.MkdirAll(paths.Long(spool), 0o700))
	// A trailing half line keeps the first file on disk once it drains.
	require.NoError(t, os.WriteFile(paths.Long(keptPath), joinLines(k1, k2[:half]), 0o600))
	require.NoError(t, os.WriteFile(paths.Long(gonePath), line(3), 0o600))

	errSync := errors.New("drain: injected spool sync fault")
	failing := true
	log := newRecordingLogger()
	dr := newDrainer(DrainConfig{
		Root: root, Log: log, Clock: newFakeClock(epoch),
		Dispatch: func(context.Context, ipc.Request) ipc.Response { return ipc.Response{OK: true} },
	})
	dr.syncFile = func(p string) error {
		if failing {
			return errSync
		}
		return syncSpoolFile(p)
	}
	announced := func() []string {
		var out []string
		for _, e := range log.entries(logLoud) {
			require.Equal(t, "daemon: drain: spool file cannot be made durable; none of it drains until it can", e.Msg)
			path, _ := kvValueOf(e.KV, "path").(string)
			out = append(out, path)
		}
		return out
	}

	for range 3 {
		n, err := dr.Drain(ctx)
		require.ErrorIs(t, err, errSync)
		require.Zero(t, n)
	}
	require.ElementsMatch(t, []string{keptPath, gonePath}, announced(),
		"three failed passes over two files: one announcement for each file")
	warned := 0
	for _, e := range log.entries(logWarn) {
		if e.Msg == "daemon: drain: file error" {
			warned++
		}
	}
	require.Equal(t, 6, warned, "each failed file is still warned of on every pass")

	failing = false
	n, err := dr.Drain(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, n, "once the syncs succeed, both files drain")
	require.NoFileExists(t, gonePath)
	require.FileExists(t, keptPath, "fixture: its trailing half line keeps it on disk")
	require.Len(t, announced(), 2, "a sync that succeeds announces nothing")

	w, err := paths.AppendOnly(keptPath)
	require.NoError(t, err)
	_, err = w.Write(k2[half:])
	require.NoError(t, err)
	require.NoError(t, w.Close())
	failing = true
	for range 2 {
		_, err = dr.Drain(ctx)
		require.ErrorIs(t, err, errSync)
	}
	got := announced()
	require.Len(t, got, 3, "a failure after the file's sync succeeded is a new one, announced once")
	require.Equal(t, keptPath, got[2])
}
