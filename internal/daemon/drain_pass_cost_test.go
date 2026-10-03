package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// V6 close-out C1.13, wave 20 (D31, D58(c), D60(a)). Since only progress counts against a pass budget,
// a pass that makes none reads every spool to its end, behind a head that waits on an earlier arrival
// of its session. Every line such a pass consumed out of order was consumed again by every later pass,
// at full cost: admitted again, its lease looked up again, the delivery journal asked three times, its
// blob read again for its name and appended to the file's cleanup intents again, a corrupt or unadmitted
// line counted and logged again, and every line still waiting re-attempted after each one, whatever its
// session. Each blocked spool also cost a sync and a rewrite of state/drain.json on every pass. The
// pre-freeze audit measured a pass with a spent budget over one spool of 300 waiting lines and 600
// consumed ones at 102.9 s, against candidate 7's 1.6 s. These rows count those operations, never time.

// journalQueriesPerLine bounds the delivery-journal queries one spool line costs a pass. Each query asks
// DrainConfig.Journal once. A line costs its lease lookup (1) and processOne's terminal, acknowledged and
// predecessor queries as it is read (3); a line that must still wait pays those three again at the
// pass's end-of-file re-attempt (3), and a line the pass publishes pays its acknowledgement instead (1).
// 1 + 3 + 3 = 7. What it must not cost is a re-attempt of every waiting line per line consumed.
const journalQueriesPerLine = 7

// drainCost counts what a drainer's passes did: admissions by session, delivery-journal queries, and
// syncs of spool files.
type drainCost struct {
	mu       sync.Mutex
	admitted map[core.SessionID]int
	journal  atomic.Int64
	syncs    atomic.Int64
}

// drainCostTaken is one reading of a drainCost, taken and reset at once (drainCost.take).
type drainCostTaken struct {
	admitted       map[core.SessionID]int
	admittedTotal  int
	journal, syncs int64
}

// meterDrainCost counts every admission and every delivery-journal query cfg's drainer makes. It
// returns the meter; meterSyncs adds the drainer's syncs once the drainer exists.
func meterDrainCost(cfg *DrainConfig) *drainCost {
	m := &drainCost{admitted: map[core.SessionID]int{}}
	admit, journal := cfg.Admit, cfg.Journal
	cfg.Admit = func(req ipc.Request) admissionVerdict {
		m.mu.Lock()
		m.admitted[req.Session]++
		m.mu.Unlock()
		if admit == nil {
			return admissionVerdict{Request: req}
		}
		return admit(req)
	}
	cfg.Journal = func() (*deliveryJournal, error) {
		m.journal.Add(1)
		return journal()
	}
	return m
}

// meterSyncs counts every sync dr makes of a spool file, keeping the real one.
func (m *drainCost) meterSyncs(dr *drainer) {
	sync := dr.syncFile
	dr.syncFile = func(path string) error {
		m.syncs.Add(1)
		return sync(path)
	}
}

// take returns what was counted since the last take, and starts counting again.
func (m *drainCost) take() drainCostTaken {
	m.mu.Lock()
	defer m.mu.Unlock()
	got := drainCostTaken{admitted: m.admitted, journal: m.journal.Swap(0), syncs: m.syncs.Swap(0)}
	for _, n := range m.admitted {
		got.admittedTotal += n
	}
	m.admitted = map[core.SessionID]int{}
	return got
}

// drainStateStat is state/drain.json's identity, size and modification time, read through a handle so
// that the identity is the file's own on every platform.
func drainStateStat(t *testing.T, root string) os.FileInfo {
	t.Helper()
	f, err := os.Open(paths.Long(drainStatePath(root)))
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	require.NoError(t, err)
	return fi
}

// drainStateRewritten reports whether state/drain.json was written since before was read. saveState
// renames a fresh file over it, so a write changes the file's identity, and its modification time.
func drainStateRewritten(t *testing.T, root string, before os.FileInfo) bool {
	t.Helper()
	after := drainStateStat(t, root)
	return !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) || before.Size() != after.Size()
}

// TestDrainClientSpools_ALineConsumedBehindAWaitingHeadCostsALaterPassOnlyItsRead is the audit's shape:
// one client spool holding a head that waits on an arrival nothing will publish, 300 later arrivals of
// its session waiting behind it, and 600 lines of another session, which the first pass publishes out
// of order. A later pass whose budget is spent makes no progress there, so it reads the spool to its
// end (D31, C1.13), and that must cost it only what is still waiting: the head and the 300 lines behind
// it are admitted and checked against the committed frontier again, while the 600 lines the first pass
// consumed cost their read alone — no admission, no journal query, no re-attempt of the waiting lines
// after each of them. The spool has not changed since the first pass synced it, so the pass syncs
// nothing and rewrites no progress. The first pass itself must not re-attempt every waiting line after
// each line it publishes: a publication can release only its own session's later arrivals.
func TestDrainClientSpools_ALineConsumedBehindAWaitingHeadCostsALaterPassOnlyItsRead(t *testing.T) {
	const waiting, consumed = 300, 600
	dd, _, root := laneTestDaemon(t)
	ctx := context.Background()
	cfg := dd.drainConfig()
	// The subject is the drain's cost; what the handler does with a publication is the observer's.
	cfg.Dispatch = func(context.Context, ipc.Request) ipc.Response { return ipc.Response{OK: true} }
	meter := meterDrainCost(&cfg)
	dr := newDrainer(cfg)
	meter.meterSyncs(dr)
	dd.drain.Store(dr)

	const stuck, other core.SessionID = "sess-cost-stuck", "sess-cost-other"
	head := blockedSpoolHead(t, dd, root, stuck, 0)
	lines := [][]byte{hookSpoolLine(t, head)}
	for i := range waiting {
		lines = append(lines, hookSpoolLine(t, liveOrderTool(dd, root, stuck, 1000+i)))
	}
	var behind []ipc.Request
	for i := range consumed {
		req := liveOrderTool(dd, root, other, 5000+i)
		behind = append(behind, req)
		lines = append(lines, hookSpoolLine(t, req))
	}
	writeRawSpool(t, root, "client-9901.ndjson", lines...)

	n, err := dr.DrainClientSpools(ctx)
	require.NoError(t, err)
	require.Equal(t, consumed, n, "fixture: the first pass published the other session's lines behind the waiting head")
	first := meter.take()
	require.LessOrEqual(t, first.journal, int64(journalQueriesPerLine*(1+waiting+consumed)),
		"the first pass re-attempted every waiting line after each line it published: a publication releases only "+
			"its own session's later arrivals")

	before := drainStateStat(t, root)
	n, err = dr.DrainClientSpools(withPassBudget(ctx, 0))
	cost := meter.take()
	require.NoError(t, err, "fixture: the pass made no progress, so its spent budget did not end it")
	require.Zero(t, n, "fixture: nothing behind the waiting head is left to publish")
	require.Zero(t, cost.admitted[other],
		"a line an earlier pass consumed behind the waiting head is not admitted again (journal queries %d)", cost.journal)
	require.Equal(t, 1+waiting, cost.admitted[stuck], "the lines still waiting are admitted again, each once")
	require.LessOrEqual(t, cost.journal, int64(journalQueriesPerLine*(1+waiting)),
		"the pass's journal queries are the waiting lines' alone: the consumed lines cost none, and none of them "+
			"re-attempted the waiting lines")
	require.Zero(t, cost.syncs, "the spool has not changed since the first pass synced it")
	require.False(t, drainStateRewritten(t, root, before), "the pass changed no progress, so it rewrote none")
	require.False(t, spoolWatchPublished(dd, head.Nonce), "control: the waiting head never publishes")
	for _, req := range behind[:3] {
		require.True(t, spoolWatchPublished(dd, req.Nonce), "control: the first pass's publications stand")
	}
}

// TestDrainClientSpools_APassOverBlockedSpoolsItHasReadSyncsAndWritesNothing is the audit's other shape:
// four client spools, each a head that waits on an arrival nothing will publish and a line behind it
// that an earlier pass published. A pass whose budget is spent makes no progress in any of them, so it
// finishes every one (D31, C1.13). On candidate 8 it paid a sync of each file and a rewrite of
// state/drain.json for each, and re-admitted each line behind each head; candidate 7 stopped in the
// first. None of the four has changed since the earlier pass synced it, so this pass syncs nothing and
// rewrites nothing, and only the four heads are admitted again.
func TestDrainClientSpools_APassOverBlockedSpoolsItHasReadSyncsAndWritesNothing(t *testing.T) {
	const spools = 4
	dd, _, root := laneTestDaemon(t)
	ctx := context.Background()
	cfg := dd.drainConfig()
	meter := meterDrainCost(&cfg)
	dr := newDrainer(cfg)
	meter.meterSyncs(dr)
	dd.drain.Store(dr)
	var heads []ipc.Request
	var bases []string
	for i := range spools {
		head := blockedSpoolHead(t, dd, root, core.SessionID(fmt.Sprintf("sess-blocked-%d", i)), 10*i)
		behind := liveOrderTool(dd, root, core.SessionID(fmt.Sprintf("sess-behind-%d", i)), 70+i)
		base := fmt.Sprintf("client-87%02d.ndjson", i)
		writeHookSpool(t, root, base, head, behind)
		heads, bases = append(heads, head), append(bases, base)
	}

	n, err := dr.DrainClientSpools(ctx)
	require.NoError(t, err)
	require.Equal(t, spools, n, "fixture: the first pass published the line behind each head")
	meter.take()

	before := drainStateStat(t, root)
	pass, budget := newPassBudget(ctx, 0)
	n, err = dr.DrainClientSpools(pass)
	cost := meter.take()
	require.NoError(t, err, "fixture: the pass made no progress, so its spent budget did not end it")
	require.Zero(t, n)
	for _, base := range bases {
		require.False(t, budget.leftUnfinished(base), "it finished %s, so the watcher keeps that spool's back-off", base)
	}
	require.Zero(t, cost.syncs, "no blocked spool has changed since the earlier pass synced it")
	require.False(t, drainStateRewritten(t, root, before), "the pass changed no progress, so it rewrote none")
	require.Equal(t, spools, cost.admittedTotal,
		"only the heads, which still wait, are admitted again; the lines behind them were consumed (%v)", cost.admitted)
	for i, head := range heads {
		require.False(t, spoolWatchPublished(dd, head.Nonce), "control: head %d never publishes", i)
		require.FileExists(t, filepath.Join(paths.Of(root).Spool, bases[i]), "nothing of the blocked spool is lost")
	}
}

// blobSpoolLine is req with its tool response externalized to the blob file name, as a hook's spool
// writer externalizes a large one: the blob holds the response, and req.Raw the descriptor naming it.
func blobSpoolLine(t *testing.T, root string, req ipc.Request, name string) ipc.Request {
	t.Helper()
	spool := paths.Of(root).Spool
	require.NoError(t, os.MkdirAll(paths.Long(spool), 0o700))
	body := []byte(req.Event.ToolResponse)
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(spool, name)), body, 0o600))
	ref, err := json.Marshal(blobRef{Blob: name, Bytes: len(body), Field: drainBlobToolResponse, X: req.Raw})
	require.NoError(t, err)
	ev := *req.Event
	ev.ToolResponse = nil
	req.Event, req.Raw = &ev, ref
	return req
}

// TestDrainClientSpools_ABlobLineBehindAWaitingHeadLeavesOneCleanupIntent: a line whose response is a
// blob, consumed out of order behind a head that waits, leaves the blob's name in its file's cleanup
// intents (drainFileState.PendingBlobs), and the blob stays while the line is still ahead of the
// consumed front. Every later pass consumed the line again and appended the name again, so
// state/drain.json grew by one entry per pass, for as long as the head waited. A later pass leaves the
// one intent, whether it skips the line or, after the file has grown, consumes it again.
func TestDrainClientSpools_ABlobLineBehindAWaitingHeadLeavesOneCleanupIntent(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	ctx := context.Background()
	dr := newDrainer(dd.drainConfig())
	dd.drain.Store(dr)
	const base, blob = "client-8851.ndjson", "blob-8851-1.bin"
	head := blockedSpoolHead(t, dd, root, "sess-blob-stuck", 0)
	other := blobSpoolLine(t, root, liveOrderTool(dd, root, "sess-blob-other", 7), blob)
	writeHookSpool(t, root, base, head, other)

	intents := func() []string {
		t.Helper()
		st, err := dr.loadState()
		require.NoError(t, err)
		require.NotNil(t, st[base])
		return st[base].PendingBlobs
	}
	for pass := 1; pass <= 3; pass++ {
		_, err := dr.DrainClientSpools(ctx)
		require.NoError(t, err)
		require.True(t, spoolWatchPublished(dd, other.Nonce), "fixture: the first pass published the blob line")
		require.Equal(t, []string{blob}, intents(), "after pass %d the blob has one cleanup intent", pass)
	}

	// The file grows (a later arrival of the waiting session, which waits too): a pass reads it as a file
	// it has not seen, and consumes the blob line again.
	w, err := paths.AppendOnly(filepath.Join(paths.Of(root).Spool, base))
	require.NoError(t, err)
	_, err = w.Write(hookSpoolLine(t, liveOrderTool(dd, root, "sess-blob-stuck", 8)))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	_, err = dr.DrainClientSpools(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{blob}, intents(), "consuming the line again leaves the one intent it already left")
	require.FileExists(t, filepath.Join(paths.Of(root).Spool, blob),
		"the blob stays while its line is still ahead of the consumed front")

	// Progress this drainer did not write: an operator removes state/drain.json. What the drainer
	// remembers of the file rests on the intents that file held, so it reads the file in full again, and
	// the intent is recorded again rather than lost with the file it was in.
	require.NoError(t, os.Remove(paths.Long(drainStatePath(root))))
	_, err = dr.DrainClientSpools(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{blob}, intents(), "the blob's intent is recorded again in the new progress")
}

// TestDrainClientSpools_ALineRewrittenInPlaceBehindAWaitingHeadIsReadAgain: a later pass skips a line
// an earlier one consumed behind a waiting head only when the bytes it reads at that offset are the
// bytes it consumed there (spoolMemo). Here the line is overwritten in place by another delivery of the
// same length, and the file's modification time put back, so the file looks unchanged by identity,
// size and time: the pass must still read the new line, and publish it, not skip it as consumed.
func TestDrainClientSpools_ALineRewrittenInPlaceBehindAWaitingHeadIsReadAgain(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	ctx := context.Background()
	dr := newDrainer(dd.drainConfig())
	dd.drain.Store(dr)
	head := blockedSpoolHead(t, dd, root, "sess-rewrite-stuck", 0)
	first := liveOrderTool(dd, root, "sess-rewrite-other", 7)
	second := liveOrderTool(dd, root, "sess-rewrite-other", 8)
	headLine, firstLine, secondLine := hookSpoolLine(t, head), hookSpoolLine(t, first), hookSpoolLine(t, second)
	require.Len(t, secondLine, len(firstLine), "fixture: the two lines have the same length")
	path := filepath.Join(paths.Of(root).Spool, "client-8881.ndjson")
	writeRawSpool(t, root, filepath.Base(path), headLine, firstLine)

	_, err := dr.DrainClientSpools(ctx)
	require.NoError(t, err)
	require.True(t, spoolWatchPublished(dd, first.Nonce), "fixture: the first pass published the line behind the head")

	before, err := os.Stat(paths.Long(path))
	require.NoError(t, err)
	f, err := os.OpenFile(paths.Long(path), os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = f.WriteAt(secondLine, int64(len(headLine)))
	require.NoError(t, err)
	require.NoError(t, f.Close())
	require.NoError(t, os.Chtimes(paths.Long(path), before.ModTime(), before.ModTime()))
	after, err := os.Stat(paths.Long(path))
	require.NoError(t, err)
	require.True(t, before.Size() == after.Size() && before.ModTime().Equal(after.ModTime()),
		"fixture: written in place, the file keeps its identity, and its size and modification time are as they were")

	n, err := dr.DrainClientSpools(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.True(t, spoolWatchPublished(dd, second.Nonce),
		"bytes the pass did not consume are read and published, whatever the file's stat says")
}

// TestDrain_ACorruptOrUnadmittedLineBehindAWaitingHeadIsAnnouncedOnce: a corrupt
// line and a line privacy admission cannot decide, behind a head that waits, are consumed out of order:
// each is counted (drain_file_error, drain_unadmitted) and announced, Warn and Loud. Every later pass
// consumed them again and counted and announced them again, so a waiting head turned two lines into a
// Loud line and a Warn on every idle tick. They are announced once, while every pass still reports the
// gaps they leave in the replay (DrainGaps), as it reports the file's pending bytes.
func TestDrain_ACorruptOrUnadmittedLineBehindAWaitingHeadIsAnnouncedOnce(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	ctx := context.Background()
	cfg := dd.drainConfig()
	log := newRecordingLogger()
	cfg.Log = log
	undecided := liveOrderTool(dd, root, "sess-undecided", 9)
	admit := cfg.Admit
	cfg.Admit = func(req ipc.Request) admissionVerdict {
		if req.Nonce == undecided.Nonce {
			return admissionVerdict{Request: req, Failed: true, Reason: "test policy unavailable"}
		}
		return admit(req)
	}
	dr := newDrainer(cfg)
	dd.drain.Store(dr)
	head := blockedSpoolHead(t, dd, root, "sess-once-stuck", 0)
	writeRawSpool(t, root, "client-8861.ndjson", hookSpoolLine(t, head), []byte("not a request\n"),
		hookSpoolLine(t, undecided))

	announced := func(level, msg string) int {
		n := 0
		for _, e := range log.entries(level) {
			if e.Msg == msg {
				n++
			}
		}
		return n
	}
	var gaps []DrainGapState
	for range 3 {
		_, err := dr.Drain(ctx)
		require.NoError(t, err)
		gaps = append(gaps, dr.GapState())
	}
	require.Equal(t, int64(1), dd.m.Counter(counterDrainFileError).Value(), "the corrupt line is counted once")
	require.Equal(t, int64(1), dd.m.Counter(counterDrainUnadmitted).Value(), "the unadmitted line is counted once")
	require.Equal(t, 1, announced(logWarn, "daemon: drain: corrupt line"), "the corrupt line is announced once")
	require.Equal(t, 1, announced(logLoud, "daemon: drain: capture not admitted; record skipped"),
		"the unadmitted line is announced once")
	for i, g := range gaps {
		require.False(t, g.Complete, "control: pass %d reports the replay incomplete", i+1)
		require.Equal(t, gaps[0].PendingBytes, g.PendingBytes, "pass %d reports the same pending bytes", i+1)
		require.Equal(t, gaps[0].Gaps, g.Gaps, "pass %d reports the same gaps as the first", i+1)
	}
	gapOfKind(t, gaps[2], DrainGapCorruptLine)
	gapOfKind(t, gaps[2], DrainGapUnadmitted)
}

// publishQueuedFirst is admit with one side effect: the first time a pass reads the delivery that
// carries nonce, the ingest first publishes the jobs queued on its ring, in ring order, as a worker
// would. Their live copies then reach the committed frontier while the pass reads on.
func publishQueuedFirst(t *testing.T, dd *daemon, admit func(ipc.Request) admissionVerdict, nonce string, jobs int,
) func(ipc.Request) admissionVerdict {
	var once sync.Once
	return func(req ipc.Request) admissionVerdict {
		if req.Nonce == nonce {
			once.Do(func() {
				for range jobs {
					select {
					case job := <-dd.ing.ring:
						dd.ing.dispatch(context.Background(), dd.runIngested, job)
					default:
						t.Error("fixture: a live job is not queued")
					}
				}
			})
		}
		return admit(req)
	}
}

// TestDrainClientSpools_ALineWhosePredecessorPublishedLateInThePassIsPublishedAtItsEnd: a line that
// waits on an earlier arrival of its session can be released after the last line the pass consumes in
// its file, by the live copy of that arrival publishing meanwhile. The pass re-attempted what was still
// waiting only after a line it consumed, so it left the line for a later pass, and the watcher backed
// the spool off as unconsumable. The pass re-attempts what is still waiting once more at the end of
// the file. Here the last line is one admission cannot decide, consumed without a re-attempt, and the
// live copy publishes as the pass reads it.
func TestDrainClientSpools_ALineWhosePredecessorPublishedLateInThePassIsPublishedAtItsEnd(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	ctx := context.Background()
	cfg := dd.drainConfig()
	const sess core.SessionID = "sess-late-release"
	p0 := spD3Prompt(dd, root, sess, orderNonce(10), "p0")
	acceptPrompt(t, dd, p0) // leased; its job waits on the ring for a worker, and none runs
	p1 := liveOrderTool(dd, root, sess, 1)
	undecided := liveOrderTool(dd, root, "sess-late-undecided", 2)
	admit := publishQueuedFirst(t, dd, cfg.Admit, undecided.Nonce, 1)
	cfg.Admit = func(req ipc.Request) admissionVerdict {
		verdict := admit(req)
		if req.Nonce == undecided.Nonce {
			return admissionVerdict{Request: req, Failed: true, Reason: "test policy unavailable"}
		}
		return verdict
	}
	dr := newDrainer(cfg)
	dd.drain.Store(dr)
	writeHookSpool(t, root, "client-8871.ndjson", p1, undecided)

	n, err := dr.DrainClientSpools(ctx)
	require.NoError(t, err)
	require.True(t, spoolWatchPublished(dd, p0.Nonce), "fixture: p0's live copy published during the pass")
	require.True(t, spoolWatchPublished(dd, p1.Nonce), "the line its publication released is published by the same pass")
	require.Equal(t, 1, n)
	require.True(t, spoolWatchGone(root, "client-8871.ndjson"), "and the consumed spool is released")
}
