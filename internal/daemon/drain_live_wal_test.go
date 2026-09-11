package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// liveWALDaemon builds a daemon over a fresh project, on a fake clock, and never calls Run: no
// ingest worker ever starts, so an accepted delivery is never processed and its WAL line is the
// only copy of it — exactly what a crash leaves behind.
func liveWALDaemon(t *testing.T, mutate func(*Options)) (*daemon, *fakeClock) {
	t.Helper()
	clk := newFakeClock(epoch)
	o := Options{ProjectRoot: t.TempDir(), Cfg: testConfig(), Log: logging.Nop(), Clock: clk}
	if mutate != nil {
		mutate(&o)
	}
	d, err := New(o)
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })
	return dd, clk
}

// liveWALTool is one observe.tool delivery from sess, stamped with the fake clock's current time;
// tests that need to tell deliveries apart advance the clock between them and compare TS.
func liveWALTool(dd *daemon, sess core.SessionID) ipc.Request {
	return ipc.Request{
		Op: ipc.OpObserveTool, Session: sess, TS: core.NowMilli(dd.clk),
		Event: &hookio.Event{HookEventName: "PostToolUse", SessionID: sess, CWD: dd.root},
	}
}

// liveWALSessionStart and liveWALSessionEnd are the session.start and flush (SessionEnd) requests
// the hook client sends for sess.
func liveWALSessionStart(dd *daemon, sess core.SessionID) ipc.Request {
	return ipc.Request{
		Op: ipc.OpSessionStart, Session: sess, Reply: true, TS: core.NowMilli(dd.clk),
		Event: &hookio.Event{
			HookEventName: "SessionStart", SessionID: sess, CWD: dd.root,
			TranscriptPath: filepath.Join(dd.root, "t.jsonl"),
		},
	}
}

func liveWALSessionEnd(dd *daemon, sess core.SessionID) ipc.Request {
	return ipc.Request{
		Op: ipc.OpFlush, Session: sess, Reply: true, TS: core.NowMilli(dd.clk),
		Event: &hookio.Event{HookEventName: "SessionEnd", SessionID: sess, CWD: dd.root},
	}
}

// liveWALAccept appends req straight through the ingest, the way the hot-path route does, without
// touching the session registry — so the drain's liveness answer for req's session stays "unknown"
// and only what the ingest itself holds can decide the segment's fate.
func liveWALAccept(t *testing.T, dd *daemon, req ipc.Request) {
	t.Helper()
	line, err := ipc.EncodeRequest(req)
	require.NoError(t, err)
	require.NoError(t, dd.ing.Accept(req, line))
}

// recordingDrainer is a bare drainer — no ingest, no registry, no seen set — over root, as a
// freshly restarted daemon's startup drain sees the spool: it records the TS of every delivery it
// dispatches.
func recordingDrainer(root string, clk core.Clock, got *[]core.UnixMilli) *drainer {
	return newDrainer(DrainConfig{Root: root, Clock: clk, Dispatch: func(_ context.Context, r ipc.Request) ipc.Response {
		*got = append(*got, r.TS)
		return ipc.Response{OK: true}
	}})
}

// TestDrainKeepsTheWALTheIngestHoldsForAnUnregisteredSession pins the live case of the drain
// deleting a WAL segment the ingest still holds. The daemon never saw this session's SessionStart —
// it was restarted mid-session — so the registry did not count it live, the drain took it for an
// ended session, and removed the segment the ingest had open for appending: on Windows that failed
// with a sharing violation on every pass (drain_file_error, and Drain returned an error each time);
// on POSIX the unlink succeeded and every later append, fsynced and ACKed as durable, went into an
// unlinked inode. The crash is simulated by closing the ingest's handles — what process death does —
// with no Stop and no worker ever having run: a fresh drainer over the same spool directory must
// still find the delivery accepted after the drain.
//
// The deliveries go straight through the ingest (liveWALAccept), not through the hot-path route,
// so the registry never learns of the session and the drain's liveness answer stays "not live": the
// ingest's own held check is the only guard left, and this test fails without it. Its twin,
// TestDrainKeepsTheWALOfASessionLiveByTrafficAlone, drives the real route and pins the liveness half.
func TestDrainKeepsTheWALTheIngestHoldsForAnUnregisteredSession(t *testing.T) {
	t.Parallel()
	const sess = core.SessionID("sess-restarted-mid-session")
	dd, clk := liveWALDaemon(t, nil)
	ctx := context.Background()

	liveWALAccept(t, dd, liveWALTool(dd, sess))
	walFile := walPath(paths.Of(dd.root).Spool, sess, 0)
	require.FileExists(t, walFile)
	require.False(t, dd.registry.IsLive(sess), "the drain must see the session as not live")

	n, err := newDrainer(dd.drainConfig()).Drain(ctx)
	require.NoError(t, err, "a segment the ingest is still appending to is not a file error")
	require.Equal(t, 1, n)
	require.Zero(t, dd.m.Counter(counterDrainFileError).Value(), "no drain_file_error for a held segment")
	require.FileExists(t, walFile, "the segment the ingest holds must survive the drain")

	clk.Advance(time.Millisecond)
	after := liveWALTool(dd, sess)
	liveWALAccept(t, dd, after) // Accept returned: the hot-path route ACKs now, its WAL line durable
	require.False(t, dd.registry.IsLive(sess))

	// Crash: the OS closes every handle; nothing is flushed, drained or processed.
	require.NoError(t, dd.ing.Close())

	var got []core.UnixMilli
	_, err = recordingDrainer(dd.root, clk, &got).Drain(ctx)
	require.NoError(t, err)
	require.Equal(t, []core.UnixMilli{after.TS}, got,
		"the delivery ACKed after the drain must be recoverable from the spool after a crash")
}

// TestDrainKeepsTheWALOfASessionLiveByTrafficAlone is the same scenario driven through the real
// hot-path route (dispatchOp), which records the traffic in the registry. A session this daemon
// knows only from its traffic must count as live — the drain keeps a live session's segments
// wholesale — and the delivery ACKed after the drain must still survive a crash.
func TestDrainKeepsTheWALOfASessionLiveByTrafficAlone(t *testing.T) {
	t.Parallel()
	const sess = core.SessionID("sess-live-by-traffic")
	dd, clk := liveWALDaemon(t, nil)
	ctx := context.Background()

	require.True(t, dd.dispatchOp(ctx, liveWALTool(dd, sess)).OK)
	walFile := walPath(paths.Of(dd.root).Spool, sess, 0)
	require.FileExists(t, walFile)
	require.True(t, dd.registry.IsLive(sess), "hot-path traffic with no SessionStart proves the session live")

	n, err := newDrainer(dd.drainConfig()).Drain(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Zero(t, dd.m.Counter(counterDrainFileError).Value())
	require.FileExists(t, walFile, "a live session's segment must survive the drain")

	clk.Advance(time.Millisecond)
	after := liveWALTool(dd, sess)
	require.True(t, dd.dispatchOp(ctx, after).OK, "the delivery is ACKed: its WAL line is durable")

	require.NoError(t, dd.ing.Close()) // crash

	var got []core.UnixMilli
	_, err = recordingDrainer(dd.root, clk, &got).Drain(ctx)
	require.NoError(t, err)
	require.Equal(t, []core.UnixMilli{after.TS}, got,
		"the delivery ACKed after the drain must be recoverable from the spool after a crash")
}

// TestDrainDoesNotRemoveAWALThatGrewBeforeTheRemovalDecision pins the removal TOCTOU: the size a
// pass read to is not the size the file has when the removal is decided. Nothing holds the segment
// (its session was closed), a straggler lands in it between the pass reaching EOF and the removal
// decision, and the session is closed again. Removing on the pass's own size lost the straggler.
func TestDrainDoesNotRemoveAWALThatGrewBeforeTheRemovalDecision(t *testing.T) {
	t.Parallel()
	const sess = core.SessionID("sess-grows-before-removal")
	dd, clk := liveWALDaemon(t, nil)
	ctx := context.Background()

	liveWALAccept(t, dd, liveWALTool(dd, sess))
	require.NoError(t, dd.ing.CloseSession(sess))
	walFile := walPath(paths.Of(dd.root).Spool, sess, 0)

	cfg := dd.drainConfig()
	var straggler ipc.Request
	grown := false
	// IsLive is consulted as part of the removal decision, after the pass has read to EOF: the
	// last point at which the file can still grow before it is removed.
	cfg.IsLive = func(s core.SessionID) bool {
		if !grown {
			grown = true
			clk.Advance(time.Millisecond)
			straggler = liveWALTool(dd, s)
			liveWALAccept(t, dd, straggler)
			require.NoError(t, dd.ing.CloseSession(s))
		}
		return false
	}
	n, err := newDrainer(cfg).Drain(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.True(t, grown, "the removal decision must have been reached")
	require.Zero(t, dd.m.Counter(counterDrainFileError).Value())
	require.FileExists(t, walFile, "a segment that grew past the drained offset must not be removed")

	var got []core.UnixMilli
	_, err = recordingDrainer(dd.root, clk, &got).Drain(ctx)
	require.NoError(t, err)
	require.Equal(t, []core.UnixMilli{straggler.TS}, got, "the next pass delivers the straggler")
	require.NoFileExists(t, walFile, "fully drained and held by nobody, the segment is then cleaned up")
}

// TestDrainPassIsBoundedByItsOwnSnapshotOfAGrowingSpoolFile pins the other half of the same
// TOCTOU: bytes appended after drainFile's stat but BEFORE the pass reaches EOF. The pass used to
// read them too, so its consumed offset overtook the size it had recorded — and loadState refuses
// progress whose offset exceeds its size, so every later Drain failed before reading a single spool
// file. A client-<pid>.ndjson file grows exactly like this when a hook process appends to its own
// spool mid-pass (or a reused pid reopens the name).
func TestDrainPassIsBoundedByItsOwnSnapshotOfAGrowingSpoolFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	p := writeSpoolFile(t, root, "client-4242.ndjson", 2) // TS 0 and 1
	clk := newFakeClock(epoch)

	var got []core.UnixMilli
	appended := false
	dr := newDrainer(DrainConfig{Root: root, Clock: clk, Dispatch: func(_ context.Context, r ipc.Request) ipc.Response {
		got = append(got, r.TS)
		if !appended {
			appended = true
			line, err := ipc.EncodeRequest(ipc.Request{Op: ipc.OpObserveTool, Session: "sess-1", TS: 2})
			require.NoError(t, err)
			f, err := os.OpenFile(paths.Long(p), os.O_WRONLY|os.O_APPEND, 0o600)
			require.NoError(t, err)
			_, err = f.Write(line)
			require.NoError(t, err)
			require.NoError(t, f.Close())
		}
		return ipc.Response{OK: true}
	}})

	n1, err := dr.Drain(context.Background())
	require.NoError(t, err)
	require.FileExists(t, p, "a file that grew past its drained offset must not be removed")

	n2, err := dr.Drain(context.Background())
	require.NoError(t, err, "the first pass must leave progress the next pass can trust")
	require.Equal(t, []core.UnixMilli{0, 1, 2}, got, "every line exactly once, in order")
	require.Equal(t, 2, n1, "a pass drains the file as it stood when the pass began")
	require.Equal(t, 1, n2, "the bytes appended mid-pass belong to the next pass")
	require.NoFileExists(t, p)
}

// TestDrainRemovesClosedRotatedSegmentsAndKeepsTheHeldOne: rotation closes a segment for good, so
// once drained it is cleaned up even though the ingest is still appending to the session's current
// segment — which the same pass must leave alone. Once SessionEnd closes the handle, the next pass
// cleans that one up too.
func TestDrainRemovesClosedRotatedSegmentsAndKeepsTheHeldOne(t *testing.T) {
	t.Parallel()
	const sess = core.SessionID("sess-rotated")
	dd, clk := liveWALDaemon(t, nil)
	ctx := context.Background()

	liveWALAccept(t, dd, liveWALTool(dd, sess))
	dd.ing.mu.Lock()
	dd.ing.wals[sess].bytes = walRotateBytes - 1 // the next append rolls over to segment 1
	dd.ing.mu.Unlock()
	clk.Advance(time.Millisecond) // a distinct delivery: identical unleased lines dedup to one
	liveWALAccept(t, dd, liveWALTool(dd, sess))

	seg0 := walPath(paths.Of(dd.root).Spool, sess, 0)
	seg1 := walPath(paths.Of(dd.root).Spool, sess, 1)
	require.FileExists(t, seg0)
	require.FileExists(t, seg1)

	n, err := newDrainer(dd.drainConfig()).Drain(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.Zero(t, dd.m.Counter(counterDrainFileError).Value())
	require.NoFileExists(t, seg0, "a rotated-away segment is closed for good; drained, it is cleaned up")
	require.FileExists(t, seg1, "the segment the ingest is appending to must be kept")

	require.NoError(t, dd.ing.CloseSession(sess))
	n, err = newDrainer(dd.drainConfig()).Drain(ctx)
	require.NoError(t, err)
	require.Zero(t, n)
	require.NoFileExists(t, seg1, "an ended session's drained segment is cleaned up")
}

// TestStragglerAfterSessionEndIsNotLostWithItsReopenedSegment: SessionEnd closes the ingest's
// handle and its flush drain removes the drained segment. A hot-path straggler that arrives after
// that reopens segment 0, and the ingest holds it again — while the session stays ended. A drain
// pass must not remove the reopened segment, and a delivery appended after that pass must survive a
// crash.
func TestStragglerAfterSessionEndIsNotLostWithItsReopenedSegment(t *testing.T) {
	t.Parallel()
	const sess = core.SessionID("sess-straggler")
	dd, clk := liveWALDaemon(t, nil)
	ctx := context.Background()
	dd.drain.Store(newDrainer(dd.drainConfig()))
	walFile := walPath(paths.Of(dd.root).Spool, sess, 0)

	require.True(t, dd.dispatchOp(ctx, liveWALSessionStart(dd, sess)).OK)
	require.True(t, dd.dispatchOp(ctx, liveWALTool(dd, sess)).OK)
	require.True(t, dd.dispatchOp(ctx, liveWALSessionEnd(dd, sess)).OK)
	require.NoFileExists(t, walFile, "SessionEnd's flush drain cleans up the ended session's segment")

	clk.Advance(time.Millisecond)
	require.True(t, dd.dispatchOp(ctx, liveWALTool(dd, sess)).OK)
	require.FileExists(t, walFile, "the straggler reopened segment 0")
	require.False(t, dd.registry.IsLive(sess), "a straggler after SessionEnd does not revive the session")

	n, err := dd.Drain(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Zero(t, dd.m.Counter(counterDrainFileError).Value())
	require.FileExists(t, walFile, "the reopened segment is held by the ingest and must be kept")

	clk.Advance(time.Millisecond)
	late := liveWALTool(dd, sess)
	require.True(t, dd.dispatchOp(ctx, late).OK)
	require.NoError(t, dd.ing.Close()) // crash

	var got []core.UnixMilli
	_, err = recordingDrainer(dd.root, clk, &got).Drain(ctx)
	require.NoError(t, err)
	require.Equal(t, []core.UnixMilli{late.TS}, got, "nothing appended after SessionEnd may be lost")
}

// TestIngestRemoveDrainedWALRefusesHeldAndGrownSegments pins removeDrainedWAL's answers directly: a
// segment the ingest holds is refused, a closed segment that runs past the drained offset is
// refused, a closed segment that ends exactly at it is removed, and one already gone reports
// not-exist, which the drainer ignores. No refusal is an error.
func TestIngestRemoveDrainedWALRefusesHeldAndGrownSegments(t *testing.T) {
	t.Parallel()
	const sess = core.SessionID("sess-remove")
	dd, _ := liveWALDaemon(t, nil)
	liveWALAccept(t, dd, liveWALTool(dd, sess))
	seg := walPath(paths.Of(dd.root).Spool, sess, 0)
	fi, err := os.Stat(seg)
	require.NoError(t, err)
	size := fi.Size()

	removed, err := dd.ing.removeDrainedWAL(seg, size)
	require.NoError(t, err)
	require.False(t, removed, "the segment the ingest holds must be refused")
	require.FileExists(t, seg)

	require.NoError(t, dd.ing.CloseSession(sess))
	removed, err = dd.ing.removeDrainedWAL(seg, size-1)
	require.NoError(t, err)
	require.False(t, removed, "a segment that runs past the drained offset must be refused")
	require.FileExists(t, seg)

	removed, err = dd.ing.removeDrainedWAL(seg, size)
	require.NoError(t, err)
	require.True(t, removed, "closed and ending exactly at the drained offset, the segment is removed")
	require.NoFileExists(t, seg)

	_, err = dd.ing.removeDrainedWAL(seg, size)
	require.True(t, os.IsNotExist(err), "a segment already gone reports not-exist")
}

// TestIngestHoldsWALAnswersForTheCurrentSegmentOnly pins holdsWAL, the question the drainer asks
// before it forgets a finished segment's progress. It shares removeDrainedWAL's held check, so the
// two answer alike. The segment a session appends to is held. A segment rotation closed is not,
// though its session is still open. None is once the session's handle is closed.
func TestIngestHoldsWALAnswersForTheCurrentSegmentOnly(t *testing.T) {
	t.Parallel()
	const sess = core.SessionID("sess-holds")
	dd, clk := liveWALDaemon(t, nil)
	seg0 := walPath(paths.Of(dd.root).Spool, sess, 0)
	seg1 := walPath(paths.Of(dd.root).Spool, sess, 1)
	require.False(t, dd.ing.holdsWAL(seg0), "a session never opened holds nothing")

	liveWALAccept(t, dd, liveWALTool(dd, sess))
	require.True(t, dd.ing.holdsWAL(seg0), "the segment the session appends to is held")

	dd.ing.mu.Lock()
	dd.ing.wals[sess].bytes = walRotateBytes - 1 // the next append rolls over to segment 1
	dd.ing.mu.Unlock()
	clk.Advance(time.Millisecond)
	liveWALAccept(t, dd, liveWALTool(dd, sess))
	require.False(t, dd.ing.holdsWAL(seg0), "a segment rotation closed is not held")
	require.True(t, dd.ing.holdsWAL(seg1), "the segment rotation opened is held")

	require.NoError(t, dd.ing.CloseSession(sess))
	require.False(t, dd.ing.holdsWAL(seg1), "a closed session holds nothing")
}
