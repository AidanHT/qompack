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

// TestCarriedDefect_SP08D2_ReusedLeaseRedeliveryIsNotIdempotent was carried defect SP08-D2's
// evidence test and is now its proof. It was INVERTED when the defect was fixed: it used to assert
// that the observer wrote a second capture and a second set of supersede marks for one host event,
// and it now asserts that it writes neither. The name is kept deliberately, so that
// plans/CARRIED-DEFECTS.tsv's evidence column, the x09 comment block and V5-report §21 all stay
// valid references to the same test; the row's summary is what was rewritten to say the defect is
// closed.
//
// The window is unchanged, and so is the daemon's half of the contract. ingest.go's dispatch
// contract says "Restart does not retain this set, so handlers must tolerate at-least-once
// delivery": a delivery the live path LEASED and whose handler RAN — the sidecar is durable, the
// observer's reference writes landed — but whose acknowledgement never reached the journal because
// the process died between publication and commitDelivery (ingest.go, stage 3). The next daemon
// starts with an empty seenSet, finds the WAL copy the dying process never drained (and the
// client's fallback copy, spooled because the one-byte transport ACK never left that process),
// re-takes the SAME lease (delivery_lease.go lease is idempotent for a known delivery) and
// dispatches it once more under the SAME ObservationID (observer.WithObservation). Reusing the
// identity is correct and MUST stay: the assertion that the handler runs a second time under the
// first delivery's identity is kept below, unchanged in meaning.
//
// What changed is what the second run does. This test now drives the REAL observer over a REAL
// store rather than a counting stub, because the absorption is the observer's and a stub cannot
// show it. Both sites are exercised in one session:
//
//   - (P2) observer/stop.go captureSubagent used to mint SubagentCaptureID(e.SessionID, st.Turn)
//     from the fresh process's turn counter (0 on restart) instead of from the reused observation
//     identity, so a second SubagentStop capture blob and index record appeared for one event —
//     x09's phantom subagent_<session>_0. It now recognizes the capture this observation already
//     published, through the capture sidecar, and writes nothing.
//   - (P3) the read-supersede path used to let a replayed read whose content is OLDER supersede
//     records appended AFTER it, because a replay re-ran supersession with the redelivery's own
//     clock. A record and the marks it authors now land in one index write, so a replay that
//     records nothing marks nothing.
//
// The cut is placed where the crash is: the ingest worker's journal resolver answers the lease at
// Accept but refuses the frontier write, the shape TestCrashCutBetweenReferenceAndFrontierRedelivers
// (delivery_publication_test.go) gives the drain-side worker. Withholding the acknowledgement this
// way, rather than failing the handler, is deliberate: a handler that returns an error is the
// SP05-D1 retry (TestCarriedDefect_SP05D1_IdleBudgetExpiryLeavesInterruptedLinePending,
// TestDrainRejectedResponseIsRetryableAfterRestart) and has no side effects to absorb, and
// deleting the ack line afterwards changes nothing the open journal reads. Only "handler
// committed, frontier did not" is the window.
//
// This file must not be confused with TestDrainDoesNotRedeliverAnAcknowledgedClientCopy (F4-P1),
// whose delivery IS on the frontier and must not be dispatched at all; here the frontier is
// genuinely empty and the redelivery is legitimate.
//
// The pin is not vacuous in either direction. A seenSet that already held the keys as completed
// (what "the set survived the restart" would look like) takes the drain's completed branch and
// never dispatches, and the second-invocation assertions below are the ones that would fail — the
// crash-cut leaves the key out of the set anyway, since seenSet.finish drops an unacknowledged
// key, so a same-process retry redelivers too; the restart shape is used because it is the one x09
// produced. And on the pre-fix tree the three index assertions fail: the redelivered Stop appends a
// second SubagentStop record, and the redelivered read appends a supersede mark naming a record
// that landed after it.
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
		if _, leased := j.leases[cutToken]; leased {
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

	// 2. A LATER read of the same content, fully acknowledged. It supersedes the first, which is
	//    what makes an INVERTED mark reachable: when the first read is replayed, a supersession
	//    re-run from the replay's own clock sees this record as "earlier" and marks it superseded by
	//    a read that actually predates it (x09 v3_x09_test.go:358).
	laterToken := testDeliveryToken('a')
	cutToken = ""
	require.True(t, dd.dispatchOp(ctx, sp08d2Read(laterToken, sess, "toolu_sp08d2_later")).OK)
	drainRing(t, dd)

	// 3. A SubagentStop whose acknowledgement the shutdown cuts: site P2's delivery.
	stopToken := testDeliveryToken('b')
	stopReq := sp08d2Stop(stopToken, sess)
	cutToken = stopToken
	require.True(t, dd.dispatchOp(ctx, stopReq).OK)
	drainRing(t, dd)
	cutToken = ""

	journal, err := dd.deliveryJournal()
	require.NoError(t, err)
	readLease, readLeased := journal.leases[readToken]
	require.True(t, readLeased, "fixture: the live path took the read's lease")
	stopLease, stopLeased := journal.leases[stopToken]
	require.True(t, stopLeased, "fixture: the live path took the stop's lease")
	require.False(t, journal.acknowledged(readToken), "fixture: the read's frontier was not committed")
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

	n, err := dd.Drain(ctx)
	require.NoError(t, err)

	// The daemon's half, unchanged and still asserted: BOTH cut deliveries are dispatched a second
	// time, under the identity they were first assigned. Nothing in the daemon absorbs this, and
	// nothing should — the frontier was empty, so the redelivery is the contract working.
	require.Equal(t, 2, sp08d2Seen(ids(), readLease.ObservationID),
		"SP08-D2: the at-least-once redelivery runs the handler again under the same ObservationID "+
			"(ingest.go dispatch: handlers must tolerate at-least-once delivery)")
	require.Equal(t, 2, sp08d2Seen(ids(), stopLease.ObservationID),
		"the same for the Stop: one delivery, two dispatches, one identity")
	require.Equal(t, 2, n, "the WAL copies are dispatched; the client copy is the same delivery")

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
	require.Equal(t, int64(2), dd.m.Counter("observer.redelivery_absorbed").Value(),
		"both redeliveries must be counted as absorbed rather than silently skipped")

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
