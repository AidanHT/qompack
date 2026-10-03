package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// V6 close-out C1.13, wave 20: what a drainer remembers between passes about a spool behind a waiting
// head (spoolMemo), and what a pass that skips such lines must still get right. The cost rows are in
// drain_pass_cost_test.go.

// logCount is how many entries at level carry msg.
func logCount(log *recordingLogger, level, msg string) int {
	n := 0
	for _, e := range log.entries(level) {
		if e.Msg == msg {
			n++
		}
	}
	return n
}

// TestDrain_ACorruptLineAheadOfAFrontHoldsBackOnlyTheBlobsItNames: a corrupt line behind a waiting head
// stays ahead of its file's consumed front, and each pass checks the spool from the fronts on for
// references to the blobs whose cleanup intents wait (cleanupAcknowledged). That check failed on a line
// that does not decode, so while any intent waited, every pass returned an error, counted a file error
// and logged one for each file it finished, and no blob was ever collected: the corrupt line, announced
// once, was reported again on every pass by another route. A line that does not decode is never
// dispatched; it holds back the blobs it names, in case a binary that can decode it replays it, and no
// others.
func TestDrain_ACorruptLineAheadOfAFrontHoldsBackOnlyTheBlobsItNames(t *testing.T) {
	for _, names := range []bool{false, true} {
		t.Run(fmt.Sprintf("names the blob=%v", names), func(t *testing.T) {
			dd, _, root := laneTestDaemon(t)
			ctx := context.Background()
			cfg := dd.drainConfig()
			log := newRecordingLogger()
			cfg.Log = log
			dr := newDrainer(cfg)
			dd.drain.Store(dr)
			const blob, done = "blob-8891-1.bin", "client-8891.ndjson"
			corrupt := []byte("not a request\n")
			if names {
				corrupt = []byte(`not a request, though it names "` + blob + "\"\n")
			}
			head := blockedSpoolHead(t, dd, root, "sess-refs-stuck", 0)
			writeRawSpool(t, root, "client-8890.ndjson", hookSpoolLine(t, head), corrupt)
			published := blobSpoolLine(t, root, liveOrderTool(dd, root, "sess-refs-blob", 7), blob)
			writeHookSpool(t, root, done, published)

			for pass := 1; pass <= 3; pass++ {
				_, err := dr.Drain(ctx)
				require.NoError(t, err, "pass %d", pass)
			}
			require.True(t, spoolWatchPublished(dd, published.Nonce), "fixture: the blob line was published")
			require.Equal(t, int64(1), dd.m.Counter(counterDrainFileError).Value(),
				"the corrupt line is counted once, and nothing else is a file error")
			require.Zero(t, logCount(log, logWarn, "daemon: drain: file error"))
			blobPath := filepath.Join(paths.Of(root).Spool, blob)
			if names {
				require.FileExists(t, blobPath, "a blob a line ahead of a front names stays, though the line does not decode")
				return
			}
			require.NoFileExists(t, blobPath, "a corrupt line that names no blob holds none back")
			require.True(t, spoolWatchGone(root, done), "and the spool whose intent it was is released")
		})
	}
}

// TestDrainClientSpools_ADeniedLineNamingABlobOutsideTheSpoolLeavesNoCleanupIntent: a line consumed
// without being published leaves its blob's name as a cleanup intent, and the drain names that blob
// from the line's descriptor (pendingBlobOf), which a hostile spool line writes. A name that leaves the
// spool directory must leave no intent: the progress refuses an unsafe intent, so every later pass would
// fail before reading a spool file, and nothing outside the spool may be removed.
func TestDrainClientSpools_ADeniedLineNamingABlobOutsideTheSpoolLeavesNoCleanupIntent(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	ctx := context.Background()
	const base = "client-8981.ndjson"
	hostile := liveOrderTool(dd, root, "sess-outside", 9)
	cfg := dd.drainConfig()
	cfg.Admit = denyNonce(cfg.Admit, hostile.Nonce)
	dr := newDrainer(cfg)
	dd.drain.Store(dr)
	outside := filepath.Join(paths.Of(root).Spool, "..", "outside.bin")
	body := []byte("hostile")
	require.NoError(t, os.MkdirAll(paths.Long(paths.Of(root).Spool), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(outside), body, 0o600))
	ref, err := json.Marshal(blobRef{Blob: "../outside.bin", Bytes: len(body), Field: drainBlobToolResponse, X: hostile.Raw})
	require.NoError(t, err)
	ev := *hostile.Event
	ev.ToolResponse = nil
	hostile.Event, hostile.Raw = &ev, ref
	head := blockedSpoolHead(t, dd, root, "sess-outside-stuck", 0)
	writeHookSpool(t, root, base, head, hostile)

	for pass := 1; pass <= 2; pass++ {
		_, err = dr.DrainClientSpools(ctx)
		require.NoError(t, err, "pass %d loads its progress", pass)
	}
	st, err := dr.loadState()
	require.NoError(t, err)
	require.Empty(t, st[base].PendingBlobs, "a name outside the spool directory is no cleanup intent")
	require.FileExists(t, outside)
}

// TestDrainClientSpools_AFailedProgressWriteForgetsWhatThePassRemembered: a pass remembers the lines it
// consumed behind a waiting head (spoolMemo), whose cleanup intents its progress write carries. When
// that write fails, the progress on disk is the drainer's own earlier write, and a later pass that found
// it there and kept the memo would skip the blob line whose intent never reached disk: the blob would
// stay for the daemon's lifetime. A failed write forgets every memo, so the later pass consumes the line
// again and records its intent.
func TestDrainClientSpools_AFailedProgressWriteForgetsWhatThePassRemembered(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	ctx := context.Background()
	const base, blob = "client-8951.ndjson", "blob-8951-1.bin"
	head := blockedSpoolHead(t, dd, root, "sess-savefail-stuck", 0)
	other := blobSpoolLine(t, root, liveOrderTool(dd, root, "sess-savefail-other", 7), blob)
	tmp := paths.Long(paths.Of(root).Tmp)
	var broke atomic.Bool
	var breakErr error
	cfg := dd.drainConfig()
	dispatch := cfg.Dispatch
	cfg.Dispatch = func(c context.Context, req ipc.Request) ipc.Response {
		resp := dispatch(c, req)
		if req.Nonce == other.Nonce && broke.CompareAndSwap(false, true) {
			// Until the test puts it back, nothing can stage a write under .qompack/tmp, so the pass's
			// progress write fails once the line is published.
			breakErr = errors.Join(os.RemoveAll(tmp), os.WriteFile(tmp, []byte("not a directory"), 0o600))
		}
		return resp
	}
	dr := newDrainer(cfg)
	dd.drain.Store(dr)
	writeHookSpool(t, root, base, head)
	_, err := dr.DrainClientSpools(ctx)
	require.NoError(t, err, "fixture: the first pass writes the drainer's own progress")

	w, err := paths.AppendOnly(filepath.Join(paths.Of(root).Spool, base))
	require.NoError(t, err)
	_, err = w.Write(hookSpoolLine(t, other))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	_, err = dr.DrainClientSpools(ctx)
	require.True(t, broke.Load(), "fixture: the blob line was dispatched")
	require.NoError(t, breakErr)
	require.NoError(t, os.Remove(tmp))
	require.Error(t, err, "fixture: the second pass's progress write failed")
	require.True(t, spoolWatchPublished(dd, other.Nonce), "fixture: the second pass published the blob line")

	_, err = dr.DrainClientSpools(ctx)
	require.NoError(t, err)
	st, err := dr.loadState()
	require.NoError(t, err)
	require.Equal(t, []string{blob}, st[base].PendingBlobs, "the blob's intent reaches the progress after the failed write")
}

// TestDrainClientSpools_LinesPastTheMemoCapBehindAWaitingHeadAreAnnouncedOnce: a drainer remembers at most
// orderingProcessedCap lines consumed behind a waiting head per file (spoolMemo), the bound drainFile
// keeps on the lines its front may roll over. A line past that bound is consumed again in full by every
// later pass: admitted again, as this row pins, since the drain documents that bound. It is not counted
// or announced again: every pass counted and announced each such line, Loud for one admission could not
// decide, so a waiting head ahead of a large backlog put the same lines into LOUD.log on every pass.
// That holds after a pass whose spent budget stopped it early, too, which read only the start of the
// spool: it does not shorten what the passes before it had read and announced.
func TestDrainClientSpools_LinesPastTheMemoCapBehindAWaitingHeadAreAnnouncedOnce(t *testing.T) {
	const pastCap = 3
	dd, _, root := laneTestDaemon(t)
	ctx := context.Background()
	cfg := dd.drainConfig()
	log := newRecordingLogger()
	cfg.Log = log
	const undecided, released core.SessionID = "sess-cap-undecided", "sess-cap-released"
	admit := cfg.Admit
	cfg.Admit = func(req ipc.Request) admissionVerdict {
		if req.Session == undecided {
			return admissionVerdict{Request: req, Failed: true, Reason: "test policy unavailable"}
		}
		return admit(req)
	}
	meter := meterDrainCost(&cfg)
	dr := newDrainer(cfg)
	dd.drain.Store(dr)
	head := blockedSpoolHead(t, dd, root, "sess-cap-stuck", 20)
	p0 := spD3Prompt(dd, root, released, orderNonce(10), "p0")
	acceptPrompt(t, dd, p0) // leased; its job waits on the ring for a worker, and none runs
	next := liveOrderTool(dd, root, released, 1)
	lines := [][]byte{hookSpoolLine(t, head), hookSpoolLine(t, next)}
	for range orderingProcessedCap {
		lines = append(lines, []byte("not a request\n"))
	}
	for i := range pastCap {
		lines = append(lines, hookSpoolLine(t, liveOrderTool(dd, root, undecided, 60+i)))
	}
	writeRawSpool(t, root, "client-8831.ndjson", lines...)

	announcedOnce := func(when string) {
		t.Helper()
		require.Equal(t, int64(orderingProcessedCap), dd.m.Counter(counterDrainFileError).Value(),
			"%s each corrupt line is counted once", when)
		require.Equal(t, int64(pastCap), dd.m.Counter(counterDrainUnadmitted).Value(),
			"%s each line past the cap is counted once", when)
		require.Equal(t, orderingProcessedCap, logCount(log, logWarn, "daemon: drain: corrupt line"),
			"%s each corrupt line is announced once", when)
		require.Equal(t, pastCap, logCount(log, logLoud, "daemon: drain: capture not admitted; record skipped"),
			"%s each line past the cap is announced once", when)
	}
	for pass := 1; pass <= 3; pass++ {
		_, err := dr.DrainClientSpools(ctx)
		require.NoError(t, err)
		announcedOnce(fmt.Sprintf("after pass %d", pass))
		require.Equal(t, pastCap, meter.take().admitted[undecided],
			"pass %d admits each line past the cap: a drainer remembers orderingProcessedCap lines per file", pass)
	}

	// A worker publishes p0, which releases next; a pass whose budget is spent publishes next, which is
	// progress, and stops there, having read nothing past it.
	select {
	case job := <-dd.ing.ring:
		dd.ing.dispatch(context.Background(), dd.runIngested, job)
	default:
		t.Fatal("fixture: p0's job is not queued")
	}
	_, err := dr.DrainClientSpools(withPassBudget(ctx, 0))
	require.ErrorIs(t, err, errPassBudgetSpent, "fixture: the pass made progress, and its spent budget stopped it")
	require.True(t, spoolWatchPublished(dd, next.Nonce), "fixture: the stopped pass published the line p0 released")
	require.Zero(t, meter.take().admitted[undecided], "fixture: the stopped pass read nothing past that line")
	_, err = dr.DrainClientSpools(ctx)
	require.NoError(t, err)
	announcedOnce("after a pass that stopped early and the pass after it,")
}

// TestDrainClientSpools_APassThatStopsEarlyKeepsWhatItRemembersPastItsStop: a pass whose budget is spent
// stops after the line that made its progress (D31), short of the end of a spool. The lines past its
// stop that an earlier pass remembered consuming are no less consumed: a pass that kept only what it had
// read forgot them, and the next pass consumed them again in full. Here the line that makes the progress
// is one whose predecessor a live worker published between the passes.
func TestDrainClientSpools_APassThatStopsEarlyKeepsWhatItRemembersPastItsStop(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	ctx := context.Background()
	cfg := dd.drainConfig()
	meter := meterDrainCost(&cfg)
	dr := newDrainer(cfg)
	dd.drain.Store(dr)
	const stuck, released, other core.SessionID = "sess-early-stuck", "sess-early-released", "sess-early-other"
	head := blockedSpoolHead(t, dd, root, stuck, 20)
	p0 := spD3Prompt(dd, root, released, orderNonce(10), "p0")
	acceptPrompt(t, dd, p0) // leased; its job waits on the ring for a worker, and none runs
	next := liveOrderTool(dd, root, released, 1)
	reqs := []ipc.Request{head, next}
	for i := range 3 {
		reqs = append(reqs, liveOrderTool(dd, root, other, 30+i))
	}
	writeHookSpool(t, root, "client-8841.ndjson", reqs...)

	n, err := dr.DrainClientSpools(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, n, "fixture: the first pass published the other session's lines and nothing else")
	select {
	case job := <-dd.ing.ring:
		dd.ing.dispatch(context.Background(), dd.runIngested, job)
	default:
		t.Fatal("fixture: p0's job is not queued")
	}
	require.True(t, spoolWatchPublished(dd, p0.Nonce), "fixture: a worker published p0")
	meter.take()

	n, err = dr.DrainClientSpools(withPassBudget(ctx, 0))
	require.ErrorIs(t, err, errPassBudgetSpent, "fixture: the pass made progress, and its spent budget stopped it")
	require.Equal(t, 1, n)
	require.True(t, spoolWatchPublished(dd, next.Nonce), "fixture: the pass published the line p0 released")
	require.Zero(t, meter.take().admitted[other], "fixture: the pass stopped before the lines it remembered")

	n, err = dr.DrainClientSpools(ctx)
	require.NoError(t, err)
	require.Zero(t, n)
	cost := meter.take()
	require.Zero(t, cost.admitted[other], "the lines past the early stop are still remembered as consumed")
	require.Zero(t, cost.admitted[released], "and so is the line the stopped pass published")
	require.Equal(t, 1, cost.admitted[stuck], "only the waiting head is admitted again")
}

// TestDrain_ALineAbsorbedBeforeItsAcknowledgementIsNotReportedUnacknowledgedOnceItLands: a line whose
// delivery this daemon has handled but whose acknowledgement is not on the committed frontier yet is
// absorbed, and the pass reports the gap ("in-memory completion has no frontier record"). A drainer that
// remembered the line skipped it on every later pass and reported that gap again for the rest of its
// life, after the acknowledgement had landed. Such a line is not remembered: the next pass consumes it
// again, finds it on the frontier, and reports no gap for it.
func TestDrain_ALineAbsorbedBeforeItsAcknowledgementIsNotReportedUnacknowledgedOnceItLands(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	ctx := context.Background()
	dr := newDrainer(dd.drainConfig())
	dd.drain.Store(dr)
	head := blockedSpoolHead(t, dd, root, "sess-unack-stuck", 0)
	line := liveOrderTool(dd, root, "sess-unack-done", 7)
	lease, ok := dd.ing.leaseDelivery(ctx, line)
	require.True(t, ok)
	key := deliveryIdentityKey(lease, true, nil)
	_, acquired := dd.ing.seen.begin(key)
	require.True(t, acquired)
	dd.ing.seen.finish(key, true) // handled in this daemon's lifetime; its acknowledgement is not written yet
	writeHookSpool(t, root, "client-8811.ndjson", head, line)

	_, err := dr.Drain(ctx)
	require.NoError(t, err)
	gapOfKind(t, dr.GapState(), DrainGapUnacknowledged)

	j, err := dd.deliveryJournal()
	require.NoError(t, err)
	require.NoError(t, j.acknowledge(ctx, lease.Delivery, lease.ObservationID, core.Hash{}))
	_, err = dr.Drain(ctx)
	require.NoError(t, err)
	for _, g := range dr.GapState().Gaps {
		require.NotEqual(t, DrainGapUnacknowledged, g.Kind, "the acknowledgement has landed: %+v", g)
	}
}

// TestDrainClientSpools_ASpoolThatGrowsBehindAWaitingHeadIsNotConsumedAgain: a hook keeps appending to
// its client spool while the daemon is slow to answer it, and a head waiting on an earlier arrival of
// its session keeps every pass reading the spool. A drainer forgot what it had consumed there whenever
// the file changed at all, so each append made the next pass consume every line behind the head again
// in full. Lines already consumed do not change when the file grows (each is checked by its own bytes
// as it is read: spoolMemo), so the pass consumes only the appended line. The file's new bytes are not
// durable yet, so it is synced again.
func TestDrainClientSpools_ASpoolThatGrowsBehindAWaitingHeadIsNotConsumedAgain(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	ctx := context.Background()
	cfg := dd.drainConfig()
	meter := meterDrainCost(&cfg)
	dr := newDrainer(cfg)
	meter.meterDrainer(dr)
	dd.drain.Store(dr)
	const stuck, other core.SessionID = "sess-grow-stuck", "sess-grow-other"
	const base = "client-8821.ndjson"
	head := blockedSpoolHead(t, dd, root, stuck, 0)
	reqs := []ipc.Request{head}
	for i := range 3 {
		reqs = append(reqs, liveOrderTool(dd, root, other, 40+i))
	}
	writeHookSpool(t, root, base, reqs...)
	n, err := dr.DrainClientSpools(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, n, "fixture: the first pass published the lines behind the head")
	meter.take()

	w, err := paths.AppendOnly(filepath.Join(paths.Of(root).Spool, base))
	require.NoError(t, err)
	_, err = w.Write(hookSpoolLine(t, liveOrderTool(dd, root, stuck, 50))) // a later arrival, which waits too
	require.NoError(t, err)
	require.NoError(t, w.Close())
	n, err = dr.DrainClientSpools(ctx)
	require.NoError(t, err)
	require.Zero(t, n)
	cost := meter.take()
	require.Zero(t, cost.admitted[other], "the lines consumed before the spool grew are not consumed again")
	require.Equal(t, 2, cost.admitted[stuck], "the head and the appended line, which both wait, are admitted")
	require.Equal(t, int64(1), cost.syncs, "the grown spool is synced: its appended bytes were not durable")
}

// TestDrainClientSpools_ARememberedBlobLineIsReleasedWithItsSpoolOnceTheHeadPublishes: a blob line an
// earlier pass consumed behind a waiting head is skipped by the passes after it (spoolMemo), and its
// blob stays while the line is ahead of the front. Once the head publishes, the pass that publishes it
// rolls the front over the remembered line to the end of the file, which releases the line's cleanup
// intent: the same pass removes the blob and the spool, though it consumed nothing new of that line.
func TestDrainClientSpools_ARememberedBlobLineIsReleasedWithItsSpoolOnceTheHeadPublishes(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	ctx := context.Background()
	dr := newDrainer(dd.drainConfig())
	dd.drain.Store(dr)
	const base, blob = "client-8801.ndjson", "blob-8801-1.bin"
	const waiting core.SessionID = "sess-release-waiting"
	p0 := spD3Prompt(dd, root, waiting, orderNonce(10), "p0")
	acceptPrompt(t, dd, p0) // leased; its job waits on the ring for a worker, and none runs
	head := liveOrderTool(dd, root, waiting, 1)
	other := blobSpoolLine(t, root, liveOrderTool(dd, root, "sess-release-other", 7), blob)
	writeHookSpool(t, root, base, head, other)
	blobPath := filepath.Join(paths.Of(root).Spool, blob)
	for pass := 1; pass <= 2; pass++ {
		n, err := dr.DrainClientSpools(ctx)
		require.NoError(t, err)
		require.Equal(t, 2-pass, n, "fixture: the first pass published the blob line, and the second nothing")
		require.FileExists(t, blobPath, "fixture: the blob stays while its line is ahead of the front")
	}

	select {
	case job := <-dd.ing.ring:
		dd.ing.dispatch(context.Background(), dd.runIngested, job)
	default:
		t.Fatal("fixture: p0's job is not queued")
	}
	n, err := dr.DrainClientSpools(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n, "the pass publishes the head p0 released")
	require.True(t, spoolWatchPublished(dd, head.Nonce))
	require.NoFileExists(t, blobPath, "the front passed the remembered blob line, so its blob is collected")
	require.True(t, spoolWatchGone(root, base), "and the consumed spool is released in the same pass")
	st, err := dr.loadState()
	require.NoError(t, err)
	require.NotContains(t, st, base, "nothing of the released spool is left in the progress")
}

// TestDrainClientSpools_ASpoolReplacedUnderItsNameIsSyncedAndReadAgain: a drainer skips the sync of a
// spool file it synced before and finds unchanged, and skips the lines it consumed there (spoolMemo).
// Both hold only for that very file. Here another file, with the same bytes and the same modification
// time, is renamed over the spool's name: nothing is known of its durability, so the pass syncs it, and
// nothing of its lines is known consumed, so the pass reads them as lines it has not seen.
func TestDrainClientSpools_ASpoolReplacedUnderItsNameIsSyncedAndReadAgain(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	ctx := context.Background()
	cfg := dd.drainConfig()
	meter := meterDrainCost(&cfg)
	dr := newDrainer(cfg)
	meter.meterDrainer(dr)
	dd.drain.Store(dr)
	const other core.SessionID = "sess-replaced-other"
	head := blockedSpoolHead(t, dd, root, "sess-replaced-stuck", 0)
	behind := liveOrderTool(dd, root, other, 7)
	path := filepath.Join(paths.Of(root).Spool, "client-8861.ndjson")
	writeHookSpool(t, root, filepath.Base(path), head, behind)
	n, err := dr.DrainClientSpools(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n, "fixture: the first pass published the line behind the head")
	meter.take()

	// Stat through a handle, so that each identity is its own file's on every platform.
	handleStat := func() os.FileInfo {
		t.Helper()
		f, err := os.Open(paths.Long(path))
		require.NoError(t, err)
		defer func() { _ = f.Close() }()
		fi, err := f.Stat()
		require.NoError(t, err)
		return fi
	}
	before := handleStat()
	body, err := os.ReadFile(paths.Long(path))
	require.NoError(t, err)
	// Written beside it and renamed over it, so the two files exist at once and cannot share an identity.
	replacement := filepath.Join(paths.Of(root).Spool, "replacement.tmp")
	require.NoError(t, os.WriteFile(paths.Long(replacement), body, 0o600))
	require.NoError(t, os.Chtimes(paths.Long(replacement), before.ModTime(), before.ModTime()))
	require.NoError(t, os.Rename(paths.Long(replacement), paths.Long(path)))
	after := handleStat()
	require.True(t, !os.SameFile(before, after) && after.Size() == before.Size() && after.ModTime().Equal(before.ModTime()),
		"fixture: another file, of the same size and modification time, has the spool's name")

	n, err = dr.DrainClientSpools(ctx)
	require.NoError(t, err)
	require.Zero(t, n, "the line behind the head is on the committed frontier already")
	cost := meter.take()
	require.Equal(t, int64(1), cost.syncs, "nothing is known of the replacement's durability, so it is synced")
	require.Equal(t, 1, cost.admitted[other], "nothing of the replacement's lines is known consumed, so they are read")
}

// TestDrainer_ReadsABlobBodyOnlyThroughItsSeam: the cost rows count the blob bodies a pass reads through
// drainer.readBlobBody. A call of readBlob anywhere else in drain.go would read bodies they cannot see,
// as naming a consumed line's blob by reading the whole blob once did at four sites, so drain.go calls
// readBlob nowhere: it is only that seam's value.
func TestDrainer_ReadsABlobBodyOnlyThroughItsSeam(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "drain.go", nil, 0)
	require.NoError(t, err)
	var calls []string
	ast.Inspect(f, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "readBlob" {
				calls = append(calls, fset.Position(call.Pos()).String())
			}
		}
		return true
	})
	require.Empty(t, calls, "drain.go reads a blob's body only through drainer.readBlobBody")
}
