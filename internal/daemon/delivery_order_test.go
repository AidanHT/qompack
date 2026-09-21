package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
)

// orderNonce is a unique 64-hex delivery token for the bulk fixtures. It is offset by one so it never
// produces the all-zeros token, which validDeliveryToken reserves as the "no token" sentinel.
func orderNonce(i int) string { return fmt.Sprintf("%064x", i+1) }

// orderSyntheticDrainer installs a drainer whose Dispatch is a no-op OK, so a bulk fixture exercises
// the drain's ordering/look-ahead and the real journal (lease + publishCapture + commitDelivery)
// without a real observer capture per line.
func orderSyntheticDrainer(dd *daemon, root string) {
	dd.drain.Store(newDrainer(DrainConfig{
		Root: root, Log: dd.log, Metrics: dd.m, Clock: dd.clk,
		Dispatch: func(context.Context, ipc.Request) ipc.Response { return ipc.Response{OK: true} },
		Seen:     dd.ing.seen, Admit: dd.admitDelivery,
		Journal: dd.deliveryJournal, IsLive: dd.sessionIsLive,
	}))
}

// Bounded leased-delivery ordering (delivery-order-decision.md). These exercise the actual journal,
// ingest and drain paths; they are run only after dist/v6-remediation/parallel-tests-ready exists.

// TestDeliveryOrder_PredecessorsAcknowledgedGate is the ordering query's negative control: a leased
// arrival N+1 is blocked while arrival N is unacknowledged, and unblocked the instant N reaches the
// committed frontier. A different session is never a predecessor, and the first arrival never is.
func TestDeliveryOrder_PredecessorsAcknowledgedGate(t *testing.T) {
	root := t.TempDir()
	_, dd, _ := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })

	ctx := context.Background()
	const sess core.SessionID = "sess-gate"
	j, err := dd.deliveryJournal()
	require.NoError(t, err)

	la, ok := dd.ing.leaseDelivery(ctx, spD3Prompt(dd, root, sess, testDeliveryToken('a'), "a"))
	require.True(t, ok)
	lb, ok := dd.ing.leaseDelivery(ctx, spD3Prompt(dd, root, sess, testDeliveryToken('b'), "b"))
	require.True(t, ok)
	require.Equal(t, la.ArrivalSeq+1, lb.ArrivalSeq, "the second lease is the next arrival in the session")

	require.True(t, j.predecessorsAcknowledged(sess, la.ArrivalSeq), "the first arrival has no predecessor")
	require.False(t, j.predecessorsAcknowledged(sess, lb.ArrivalSeq),
		"arrival N+1 is blocked while arrival N is unacknowledged")

	require.NoError(t, j.acknowledge(ctx, la.Delivery, la.ObservationID, core.Hash{}))
	require.True(t, j.predecessorsAcknowledged(sess, lb.ArrivalSeq),
		"acknowledging arrival N unblocks arrival N+1")

	require.True(t, j.predecessorsAcknowledged(core.SessionID("other-session"), lb.ArrivalSeq),
		"another session's arrivals are never predecessors")
}

// TestDeliveryOrder_DrainReversedWALvsLeaseOrder is the restart negative control the decision calls
// for: WAL fsync order and lease-arrival order are separate batches, so after a crash an earlier WAL
// line can hold the LATER arrival. Merely breaking on the unacknowledged predecessor would wedge the
// file; the bounded look-ahead must process the earlier arrival (whose line comes SECOND) first, then
// the deferred later one — so turn 0 holds the earliest leased arrival, not the physically-first line.
func TestDeliveryOrder_DrainReversedWALvsLeaseOrder(t *testing.T) {
	root := t.TempDir()
	_, dd, o := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })
	spD3Drainer(dd, root)

	ctx := context.Background()
	const sess core.SessionID = "sess-reversed"
	early := spD3Prompt(dd, root, sess, testDeliveryToken('a'), "first") // becomes arrival N
	late := spD3Prompt(dd, root, sess, testDeliveryToken('b'), "second") // becomes arrival N+1

	// The live daemon leased in arrival order before the crash: early = N, late = N+1. The drain
	// re-leases each line by nonce, so these pre-assigned arrivals are what it sees on restart.
	le, ok := dd.ing.leaseDelivery(ctx, early)
	require.True(t, ok)
	ll, ok := dd.ing.leaseDelivery(ctx, late)
	require.True(t, ok)
	require.Less(t, le.ArrivalSeq, ll.ArrivalSeq)

	// The WAL/spool order is REVERSED relative to the lease order: the later arrival's line comes
	// first. This is the separate-fsync-batch reorder a restart inherits.
	writeSpoolLines(t, root, "client-00001.ndjson", late, early)

	n, err := dd.Drain(ctx)
	require.NoError(t, err, "reversed order must not wedge the file")
	require.Equal(t, 2, n, "both leased lines are delivered in one pass via bounded look-ahead")

	require.Equal(t, "first", spD3PromptText(t, o, observer.VerbatimPromptID(sess, 0)),
		"the earliest leased arrival takes turn 0 though its WAL line came second")
	require.Equal(t, "second", spD3PromptText(t, o, observer.VerbatimPromptID(sess, 1)))

	require.Positive(t, dd.m.Counter(counterDrainOrderingDeferred).Value(),
		"the later-arrival-first line was deferred, not published out of order")
	require.Positive(t, dd.m.Counter(counterDrainOrderingResolved).Value(),
		"and a look-ahead resolved it within the same pass, without wedging")
}

// --- Item 1: an unreadable committed frontier must NOT publish (fail closed) ---

// TestDeliveryOrder_UnreadableFrontierFailsClosed: when ownership is lost the frontier cannot be
// read, so predecessorsAcknowledged and leaseHeld both fail closed — including for the FIRST arrival,
// which must not bypass an unusable journal through the arrival<=1 short-circuit.
func TestDeliveryOrder_UnreadableFrontierFailsClosed(t *testing.T) {
	root := t.TempDir()
	_, dd, _ := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })

	ctx := context.Background()
	const sess core.SessionID = "sess-closed"
	j, err := dd.deliveryJournal()
	require.NoError(t, err)
	la, ok := dd.ing.leaseDelivery(ctx, spD3Prompt(dd, root, sess, testDeliveryToken('a'), "a"))
	require.True(t, ok)
	require.True(t, j.predecessorsAcknowledged(sess, la.ArrivalSeq), "healthy: the first arrival proceeds")

	require.NoError(t, lock.Release()) // ownership lost — the frontier is unreadable

	require.False(t, j.predecessorsAcknowledged(sess, la.ArrivalSeq),
		"the first arrival must NOT bypass an unreadable frontier through arrival<=1")
	require.False(t, j.predecessorsAcknowledged(sess, la.ArrivalSeq+1),
		"a later arrival is deferred, not published, over an unreadable frontier")
	_, held, heldErr := j.leaseHeld(la.Delivery)
	require.ErrorIs(t, heldErr, core.ErrDegraded)
	require.False(t, held, "leaseHeld also fails closed when ownership is lost")
}

// TestDeliveryOrder_MissingJournalGetterFailsClosed: a leased delivery whose journal getter is nil
// cannot read the frontier and is deferred (false) with the explicit counter; an unleased delivery is
// never ordering-gated and keeps its qualified legacy behaviour.
func TestDeliveryOrder_MissingJournalGetterFailsClosed(t *testing.T) {
	root := t.TempDir()
	_, dd, _ := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })

	dr := newDrainer(DrainConfig{Root: root, Log: dd.log, Metrics: dd.m, Clock: dd.clk}) // Journal nil
	require.False(t, dr.leasedPredecessorsReady(deliveryLease{Session: "s", ArrivalSeq: 2}, true),
		"a leased delivery with no journal getter fails closed")
	require.Positive(t, dd.m.Counter(counterOrderingFrontierUnavailable).Value(),
		"the fail-closed defer emits an explicit diagnostic")
	require.True(t, dr.leasedPredecessorsReady(deliveryLease{}, false),
		"an unleased delivery is not ordering-gated (legacy behaviour)")
}

// TestDeliveryOrder_UnreadableFrontierNoPublication: at the actual dispatch layer, a leased delivery
// dispatched while the frontier is unreadable publishes NOTHING.
func TestDeliveryOrder_UnreadableFrontierNoPublication(t *testing.T) {
	root := t.TempDir()
	_, dd, o := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })

	ctx := context.Background()
	const sess core.SessionID = "sess-noown"
	acceptPrompt(t, dd, spD3Prompt(dd, root, sess, testDeliveryToken('a'), "only"))
	job := <-dd.ing.ring
	require.NoError(t, lock.Release()) // frontier now unreadable

	dd.ing.dispatch(ctx, dd.runIngested, job)
	_, err := o.Store.ToolUse(ctx, observer.VerbatimPromptID(sess, 0))
	require.ErrorIs(t, err, core.ErrNotFound, "no observer publication over an unreadable frontier")
}

// --- Item 3: a reversed prefix deeper than the buffer bound must not wedge ---

// TestDeliveryOrder_DrainReversedPrefixBeyondBufferBoundDoesNotWedge builds a synthetic file whose
// deferred prefix (arrivals 2..N) exceeds the buffer bound before the ready predecessor (arrival 1)
// at the end. The old "stop at the bound" wedged: every pass re-read the same prefix and never reached
// arrival 1. The bounded scan reaches it, acknowledges it, and cascades — resolving everything in a
// few passes with bounded memory.
func TestDeliveryOrder_DrainReversedPrefixBeyondBufferBoundDoesNotWedge(t *testing.T) {
	root := t.TempDir()
	_, dd, _ := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })
	orderSyntheticDrainer(dd, root)

	ctx := context.Background()
	const sess core.SessionID = "sess-bigreverse"
	const N = orderingLookaheadBound + 3 // 1027: >1024 deferred before the ready predecessor

	reqs := make([]ipc.Request, N)
	j, err := dd.deliveryJournal()
	require.NoError(t, err)
	for i := 0; i < N; i++ {
		reqs[i] = spD3Prompt(dd, root, sess, orderNonce(i), "p") // reqs[i] leases to arrival i+1
		_, ok := dd.ing.leaseDelivery(ctx, reqs[i])
		require.True(t, ok, "lease %d", i)
	}
	// WAL/spool order: arrivals 2..N first (all deferred), arrival 1 (the ready predecessor) LAST.
	spool := append(append([]ipc.Request{}, reqs[1:]...), reqs[0])
	writeSpoolLines(t, root, "client-00001.ndjson", spool...)

	acked := func() int {
		n := 0
		for i := 0; i < N; i++ {
			if j.acknowledged(orderNonce(i)) {
				n++
			}
		}
		return n
	}
	const maxPasses = 6
	passes := 0
	for acked() < N && passes < maxPasses {
		_, derr := dd.Drain(ctx)
		require.NoError(t, derr, "a >bound reversed prefix must not error/wedge")
		passes++
	}
	require.Equal(t, N, acked(), "every arrival is delivered within a few passes — no wedge")
	require.Greater(t, dd.m.Counter(counterDrainOrderingDeferred).Value(), int64(orderingLookaheadBound),
		"the deferred prefix exceeded the buffer bound, so the scan continued past it")
}

// TestDeliveryOrder_DrainOrdersPerSessionIndependently: ordering is per session. Two interleaved
// sessions in reversed order each get turn 0 for their earliest arrival, independently.
func TestDeliveryOrder_DrainOrdersPerSessionIndependently(t *testing.T) {
	root := t.TempDir()
	_, dd, o := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })
	spD3Drainer(dd, root)

	ctx := context.Background()
	const sa, sb = core.SessionID("sess-A"), core.SessionID("sess-B")
	a1 := spD3Prompt(dd, root, sa, testDeliveryToken('a'), "a1")
	b1 := spD3Prompt(dd, root, sb, testDeliveryToken('b'), "b1")
	a2 := spD3Prompt(dd, root, sa, testDeliveryToken('c'), "a2")
	b2 := spD3Prompt(dd, root, sb, testDeliveryToken('d'), "b2")
	for _, r := range []ipc.Request{a1, b1, a2, b2} {
		_, ok := dd.ing.leaseDelivery(ctx, r)
		require.True(t, ok)
	}
	writeSpoolLines(t, root, "client-00001.ndjson", b2, a2, b1, a1) // reversed within each session

	_, derr := dd.Drain(ctx)
	require.NoError(t, derr)
	require.Equal(t, "a1", spD3PromptText(t, o, observer.VerbatimPromptID(sa, 0)))
	require.Equal(t, "a2", spD3PromptText(t, o, observer.VerbatimPromptID(sa, 1)))
	require.Equal(t, "b1", spD3PromptText(t, o, observer.VerbatimPromptID(sb, 0)))
	require.Equal(t, "b2", spD3PromptText(t, o, observer.VerbatimPromptID(sb, 1)))
}

// TestDeliveryOrder_DrainCancellationPreservesUnconsumed: a drain cancelled mid-pass preserves the
// unconsumed remainder — its spool bytes stay and its deliveries are not acknowledged.
func TestDeliveryOrder_DrainCancellationPreservesUnconsumed(t *testing.T) {
	root := t.TempDir()
	_, dd, _ := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before the pass: the read loop breaks before consuming anything
	dd.drain.Store(newDrainer(DrainConfig{
		Root: root, Log: dd.log, Metrics: dd.m, Clock: dd.clk,
		Dispatch: func(context.Context, ipc.Request) ipc.Response { return ipc.Response{OK: true} },
		Seen:     dd.ing.seen, Admit: dd.admitDelivery,
		Journal: dd.deliveryJournal, IsLive: dd.sessionIsLive,
	}))

	const sess core.SessionID = "sess-cancel"
	j, err := dd.deliveryJournal()
	require.NoError(t, err)
	r1 := spD3Prompt(dd, root, sess, testDeliveryToken('a'), "one")
	r2 := spD3Prompt(dd, root, sess, testDeliveryToken('b'), "two")
	_, ok := dd.ing.leaseDelivery(context.Background(), r1)
	require.True(t, ok)
	_, ok = dd.ing.leaseDelivery(context.Background(), r2)
	require.True(t, ok)
	const spoolFile = "client-00001.ndjson"
	writeSpoolLines(t, root, spoolFile, r1, r2)

	_, _ = dd.Drain(ctx)
	require.False(t, j.acknowledged(r1.Nonce), "a cancelled pass consumes nothing")
	require.False(t, j.acknowledged(r2.Nonce))
	_, statErr := os.Stat(paths.Long(filepath.Join(paths.Of(root).Spool, spoolFile)))
	require.NoError(t, statErr, "the spool bytes are preserved for a later pass")
}

// Retained pre-integration identifier. Its provisional pending criterion is
// replaced by durable denial retirement, with no capture ACK or observer record.
func TestDeliveryOrder_LeasedDenyOnRetryPreservedPending(t *testing.T) {
	root := t.TempDir()
	_, dd, o := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })
	spD3Drainer(dd, root)

	ctx := context.Background()
	const sess core.SessionID = "sess-leasedeny"
	outside := filepath.Join(t.TempDir(), "escaped.txt") // outside the project root
	in, err := json.Marshal(map[string]any{"file_path": outside})
	require.NoError(t, err)
	read := ipc.Request{
		Op: ipc.OpObserveTool, Session: sess, TS: core.NowMilli(dd.clk), Nonce: testDeliveryToken('a'),
		Event: &hookio.Event{
			HookEventName: "PostToolUse", SessionID: sess, CWD: root,
			ToolName: "Read", ToolUseID: "toolu_leasedeny", ToolInput: in,
		},
	}
	// Leased on the earlier pass (when the path was in-project); leaseDelivery assigns identity, not
	// admission. A successor of the same session is leased too.
	rl, ok := dd.ing.leaseDelivery(ctx, read)
	require.True(t, ok)
	successor := spD3Prompt(dd, root, sess, testDeliveryToken('b'), "later")
	sl, ok := dd.ing.leaseDelivery(ctx, successor)
	require.True(t, ok)
	require.Less(t, rl.ArrivalSeq, sl.ArrivalSeq)

	const spoolFile = "client-00001.ndjson"
	writeSpoolLines(t, root, spoolFile, read)

	_, derr := dd.Drain(ctx)
	require.NoError(t, derr, "the pass continues past a preserved-pending line rather than erroring")

	retired, err := terminalForDelivery(dd.deliveryJournal, rl)
	require.NoError(t, err)
	require.True(t, retired, "explicit denial has a durable terminal disposition")
	require.False(t, dd.deliveryJournalAcked(t, read.Nonce), "the denied delivery is never acknowledged")
	_, statErr := os.Stat(paths.Long(filepath.Join(paths.Of(root).Spool, spoolFile)))
	require.True(t, os.IsNotExist(statErr), "durable retirement permits source consumption")
	_, err = o.Store.ToolUse(ctx, "toolu_leasedeny")
	require.ErrorIs(t, err, core.ErrNotFound, "and nothing was published")

	j, err := dd.deliveryJournal()
	require.NoError(t, err)
	require.True(t, j.predecessorsAcknowledged(sess, sl.ArrivalSeq),
		"terminal denial retires the predecessor without a capture ACK")
}

func (d *daemon) deliveryJournalAcked(t *testing.T, nonce string) bool {
	t.Helper()
	j, err := d.deliveryJournal()
	require.NoError(t, err)
	return j.acknowledged(nonce)
}
