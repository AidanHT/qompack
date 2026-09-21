package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// TestCarriedDefect_SP08D2_ReusedLeaseRedeliveryIsNotIdempotent retains its original
// evidence identifier. It verifies the same publication-before-frontier crash cut
// with the current ordering contract: a later leased read in the SAME session
// waits until replay acknowledges its predecessor. Both the read and Stop replay
// reuse their observation and append no duplicate record.
//
// The historical supersession regression is also exercised at the observer
// boundary after the later read commits: redundant delivery of the original
// observation must not supersede that later read. Current daemon ordering no
// longer produces the old fixture's out-of-order publication deliberately.
func TestCarriedDefect_SP08D2_ReusedLeaseRedeliveryIsNotIdempotent(t *testing.T) {
	root := t.TempDir()
	dd, opts, ids := newRealObserverDaemon(t, root)
	lock := lockFor(t, dd, root)
	defer func() { _ = lock.Release() }()

	ctx := context.Background()
	const sess core.SessionID = "sess-reused-lease"

	// The crash window, armed per delivery: Accept's lease resolves the journal and succeeds,
	// because the token is not leased yet; the worker's commitDelivery resolves it again, after the
	// handler ran, and finds it gone. There is no daemon-side QOMPACK_FAULT seam (spawn.go strips
	// it), so the cut is the dependency itself. cutToken is what makes it per-delivery: the
	// deliveries that must REACH the frontier run with it empty.
	cutToken := ""
	dd.ing.journal = func() (*deliveryJournal, error) {
		j, err := dd.deliveryJournal()
		if err != nil || cutToken == "" {
			return j, err
		}
		if lease, leased := j.leases[cutToken]; leased && sp08d2Seen(ids(), lease.ObservationID) > 0 {
			return nil, deliveryJournalError() // the frontier write, and only it, is cut
		}
		return j, nil
	}

	// 1. A read whose acknowledgement the shutdown cuts. Its record and its capture sidecar are
	//    durable; its frontier record is not, so it is the delivery that will be redelivered.
	readToken := testDeliveryToken('9')
	readReq := sp08d2Read(readToken, sess, "toolu_sp08d2_first")
	cutToken = readToken
	require.True(t, dd.dispatchOp(ctx, readReq).OK)
	drainRing(t, dd)

	// The later read is admitted and leased, but cannot publish over the cut.
	laterToken := testDeliveryToken('a')
	cutToken = ""
	require.True(t, dd.dispatchOp(ctx, sp08d2Read(laterToken, sess, "toolu_sp08d2_later")).OK)
	drainRing(t, dd)
	_, err := opts.Store.ToolUse(ctx, "toolu_sp08d2_later")
	require.ErrorIs(t, err, core.ErrNotFound, "same-session successor waits for the earlier ACK")
	journal, err := dd.deliveryJournal()
	require.NoError(t, err)
	readLease := journal.leases[readToken]
	require.False(t, journal.acknowledged(readToken), "the first publication reached no frontier")
	require.Equal(t, 1, sp08d2Seen(ids(), readLease.ObservationID), "the ACK cut follows a real handler")

	// Replay closes that cut before publishing the later read. It may then
	// supersede the first record in the original causal order.
	n, err := dd.Drain(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.True(t, journal.acknowledged(readToken))
	require.True(t, journal.acknowledged(laterToken))
	beforeReplay := sp08d2Index(t, root)
	response := dd.runIngested(observer.WithObservation(ctx, readLease.ObservationID), readReq)
	require.True(t, response.OK, "historical duplicate observation remains absorbable after a successor")
	require.Equal(t, beforeReplay, sp08d2Index(t, root), "old replay must not invert later supersession")

	// 3. A SubagentStop whose acknowledgement the shutdown cuts: site P2's delivery.
	stopToken := testDeliveryToken('b')
	stopReq := sp08d2Stop(stopToken, sess)
	cutToken = stopToken
	require.True(t, dd.dispatchOp(ctx, stopReq).OK)
	drainRing(t, dd)
	cutToken = ""

	journal, err = dd.deliveryJournal()
	require.NoError(t, err)
	readLease, readLeased := journal.leases[readToken]
	require.True(t, readLeased, "fixture: the live path took the read's lease")
	stopLease, stopLeased := journal.leases[stopToken]
	require.True(t, stopLeased, "fixture: the live path took the stop's lease")
	require.True(t, journal.acknowledged(readToken), "fixture: replay committed the read before its successor")
	require.False(t, journal.acknowledged(stopToken), "fixture: the stop's frontier was not committed")
	require.True(t, journal.acknowledged(laterToken), "fixture: the later read IS on the frontier")
	require.Len(t, sidecarFiles(t, root), 3, "fixture: stage 1 is durable for all three deliveries")

	// The fixture the two sites are about: one capture, and a first read the later read superseded.
	require.Equal(t, 1, sp08d2CountTool(t, root, "SubagentStop"), "fixture: one capture so far")
	first, err := opts.Store.ToolUse(ctx, "toolu_sp08d2_first")
	require.NoError(t, err)
	require.Equal(t, store.StatusSuperseded, first.Status,
		"fixture: the later read superseded the first, so a replay of the first can invert it")
	require.Equal(t, core.ToolUseID("toolu_sp08d2_later"), first.SupersededBy)
	indexBefore := sp08d2Index(t, root)

	// Restart. The dying process never drained its WAL copies, and the hook client, whose transport
	// ACK never arrived, spooled its own copy. The seen set is process memory; the lease is not.
	require.NoError(t, dd.ing.Close())
	wals, err := filepath.Glob(paths.Long(filepath.Join(paths.Of(root).Spool, "wal-*.ndjson")))
	require.NoError(t, err)
	require.NotEmpty(t, wals, "fixture: the WAL copies survived the crash")
	const copyName = "client-00007.ndjson"
	writeSpoolLine(t, root, copyName, stopReq)
	dd.ing.seen = newSeenSet(seenCapacity)
	dd.drain.Load().cfg.Seen = dd.ing.seen

	n, err = dd.Drain(ctx)
	require.NoError(t, err)

	// The earlier read's replay and the explicit historical observer replay
	// retain its identity. Stop crosses the crash window here for the first time.
	require.Equal(t, 3, sp08d2Seen(ids(), readLease.ObservationID))
	require.Equal(t, 2, sp08d2Seen(ids(), stopLease.ObservationID),
		"one Stop delivery, two handler calls, one durable identity")
	require.Equal(t, 1, n, "only the unacknowledged Stop is dispatched after restart")

	// The observer's half, INVERTED: the second run writes nothing at all. This single assertion
	// covers both sites, because the index file is where both defects were visible.
	require.Equal(t, string(indexBefore), string(sp08d2Index(t, root)),
		"an absorbed redelivery must append NO index line; it appended:\n%s",
		sp08d2Index(t, root)[len(indexBefore):])
	require.Equal(t, 1, sp08d2CountTool(t, root, "SubagentStop"),
		"P2: one SubagentStop record per Stop event - a second is a redelivered Stop re-captured "+
			"under a fresh SubagentCaptureID (x09's phantom subagent_<session>_0)")
	later, err := opts.Store.ToolUse(ctx, "toolu_sp08d2_later")
	require.NoError(t, err)
	require.Equal(t, store.StatusOK, later.Status,
		"P3: a replayed read must not supersede a record appended AFTER it")
	require.Equal(t, int64(3), dd.m.Counter("observer.redelivery_absorbed").Value(),
		"both crash replays and the historical observer replay are counted as absorbed")

	// The correct half, as before: one identity, one delivery, one sidecar each, frontier reached.
	require.Len(t, sidecarFiles(t, root), 3, "the identity is reused, not re-minted")
	require.True(t, journal.acknowledged(readToken), "the read's redelivery reaches the frontier")
	require.True(t, journal.acknowledged(stopToken), "the stop's redelivery reaches the frontier")
	require.NoFileExists(t, paths.Long(filepath.Join(paths.Of(root).Spool, copyName)),
		"the client copy is released once its offset is past the frontier")
	require.True(t, dd.DrainGaps().Complete, "a redelivery is not a gap")
}

// sp08d2Path and sp08d2Body are the one path and one content both reads carry. Identical content is
// what makes the second read supersede the first through the identical-root branch, with no
// dependence on chunking or on the near-duplicate threshold.
const (
	sp08d2Path = "src/auth.ts"
	sp08d2Body = "export async function refreshToken() {}\nexport const ttl = 30\n"
)

// sp08d2Read builds an observe.tool delivery carrying a REAL Read event, so the bound observer
// records an index entry for it and can supersede an earlier read of the same path. observeRequest
// deliberately carries no tool fields at all, which is right for the delivery-plumbing tests that
// use it and useless here.
func sp08d2Read(nonce string, sess core.SessionID, id core.ToolUseID) ipc.Request {
	body, err := json.Marshal(sp08d2Body)
	if err != nil {
		panic(err) // a string literal always marshals; a test fixture may say so this way
	}
	return ipc.Request{
		Op: ipc.OpObserveTool, Session: sess, TS: core.UnixMilli(epoch.UnixMilli()),
		Event: &hookio.Event{
			HookEventName: "PostToolUse", SessionID: sess,
			ToolName: "Read", ToolUseID: id,
			ToolInput:    json.RawMessage(`{"file_path":"` + sp08d2Path + `"}`),
			ToolResponse: json.RawMessage(`{"content":` + string(body) + `}`),
		},
		Capture: admittedCapture(`{"hook_event_name":"PostToolUse"}`), Nonce: nonce,
	}
}

// sp08d2Stop builds an observe.stop delivery for a SUBAGENT stop. The subagent marker travels in
// Raw, which runIngested decodes with decodeSubagent, exactly as the hook client sends it.
func sp08d2Stop(nonce string, sess core.SessionID) ipc.Request {
	return ipc.Request{
		Op: ipc.OpObserveStop, Session: sess, TS: core.UnixMilli(epoch.UnixMilli()),
		Event: &hookio.Event{
			HookEventName: "SubagentStop", SessionID: sess,
			ToolResponse: json.RawMessage(`{"content":"the subagent's summary"}`),
		},
		Raw:     json.RawMessage(`{"subagent":true,"agent":"code-reviewer"}`),
		Capture: admittedCapture(`{"hook_event_name":"SubagentStop"}`), Nonce: nonce,
	}
}

// sp08d2Index returns index/tool_use.jsonl verbatim. The bytes are the subject: "the redelivery
// appended nothing" is a statement about the FILE, and reading records back through the store's
// in-memory index would hide a duplicate line that the loader collapsed.
func sp08d2Index(t *testing.T, root string) []byte {
	t.Helper()
	b, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(root).Index, "tool_use.jsonl")))
	require.NoError(t, err, "the observer must have written index/tool_use.jsonl by now")
	return b
}

// sp08d2CountTool counts the CONTENT records in the index whose tool is name. A mutation record
// ("op":"supersede") carries no tool and is skipped, which is the same split x09's own parser makes.
func sp08d2CountTool(t *testing.T, root, name string) int {
	t.Helper()
	n := 0
	for _, line := range bytes.Split(sp08d2Index(t, root), []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var rec struct {
			Op   string `json:"op"`
			Tool string `json:"tool"`
		}
		require.NoError(t, json.Unmarshal(line, &rec))
		if rec.Op == "" && rec.Tool == name {
			n++
		}
	}
	return n
}

// sp08d2Seen counts how many dispatches carried id.
func sp08d2Seen(seen []core.ObservationID, id core.ObservationID) int {
	n := 0
	for _, got := range seen {
		if got == id {
			n++
		}
	}
	return n
}

// newRealObserverDaemon builds a daemon whose L0 seams are the REAL observer over a real store,
// with a counting wrapper registered AFTER WireObserver's bind so that it WRAPS the real seam
// rather than replacing it.
//
// Registration order is what makes that work: New applies every Bind in the order it was
// registered, so a bind added after WireObserver's sees the seams WireObserver assigned and can
// capture them. wireTestDaemon's mutator runs BEFORE WireObserver and would be overwritten instead,
// which is why this does its own wiring.
//
// It returns the Options too, because the store WireObserver opened is the only handle a caller has
// on what the observer actually wrote.
func newRealObserverDaemon(t *testing.T, root string) (*daemon, *Options, func() []core.ObservationID) {
	t.Helper()

	o := NewOptions(root, testConfig())
	o.Log = logging.Nop()
	ledgerIsOurs := o.Ledger == nil

	_, err := WireObserver(&o)
	require.NoError(t, err)
	require.NotNil(t, o.Store, "WireObserver must open the store when the field is nil")
	// Both handles hold append files, and Windows refuses to unlink an open file, so TempDir's
	// cleanup fails the test after its body passed unless they are closed first. Cleanups run LIFO,
	// so the ingest close registered below runs before these.
	t.Cleanup(func() { _ = o.Store.Close() })
	t.Cleanup(func() {
		if ledgerIsOurs && o.Ledger != nil {
			_ = o.Ledger.Close()
		}
	})

	var seen []core.ObservationID
	o.Bind(func(s *Services) {
		tool, stop := s.ObserveTool, s.ObserveStop
		s.ObserveTool = func(ctx context.Context, e hookio.Event) error {
			seen = append(seen, observer.ObservationFrom(ctx))
			return tool(ctx, e)
		}
		s.ObserveStop = func(ctx context.Context, e hookio.Event, subagent bool) error {
			seen = append(seen, observer.ObservationFrom(ctx))
			return stop(ctx, e, subagent)
		}
	})

	d, err := New(o)
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })
	dd.drain.Store(newDrainer(DrainConfig{
		Root: root, Log: logging.Nop(), Metrics: dd.m, Clock: dd.clk,
		Dispatch: dd.drainDispatch, Seen: dd.ing.seen, Admit: dd.admitDelivery,
		Journal: dd.deliveryJournal, IsLive: dd.sessionIsLive,
	}))
	return dd, &o, func() []core.ObservationID {
		return append([]core.ObservationID(nil), seen...)
	}
}

// newIdentityRecordingDaemon is newObservingDaemon with a handler that also records the
// observation identity each invocation carried on its context (observer.ObservationFrom), so a
// test can tell a reused lease from a re-minted one at the handler rather than by counting
// sidecars alone.
//
// It is the STUB-handler form, and it stays because TestCarriedDefect_SP20D4_* uses it: that row is
// about the lease journal's entry cap, where a real observer would only add noise.
func newIdentityRecordingDaemon(t *testing.T, root string) (*daemon, func() int, func() []core.ObservationID) {
	t.Helper()
	var seen []core.ObservationID
	o := Options{ProjectRoot: root, Cfg: testConfig(), Log: logging.Nop()}
	o.Bind(func(s *Services) {
		s.ObserveTool = func(ctx context.Context, _ hookio.Event) error {
			seen = append(seen, observer.ObservationFrom(ctx))
			return nil
		}
	})
	d, err := New(o)
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })
	dd.drain.Store(newDrainer(DrainConfig{
		Root: root, Log: logging.Nop(), Metrics: dd.m, Clock: dd.clk,
		Dispatch: dd.drainDispatch, Seen: dd.ing.seen, Admit: dd.admitDelivery,
		Journal: dd.deliveryJournal, IsLive: dd.sessionIsLive,
	}))
	return dd, func() int { return len(seen) }, func() []core.ObservationID {
		return append([]core.ObservationID(nil), seen...)
	}
}
