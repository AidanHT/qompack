package observer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// Carried defect SP08-D2: the observer must absorb an at-least-once redelivery under a reused lease.
//
// The window these tests model is the one internal/daemon's dispatch contract promises to tolerate
// ("Restart does not retain this set, so handlers must tolerate at-least-once delivery"): a
// delivery whose handler RAN and whose acknowledgement never reached the journal, because the
// pre-flush shutdown cancelled the worker between the observer's append and commitDelivery. The
// next daemon re-takes the SAME lease and dispatches it again under the SAME ObservationID. That
// redelivery is correct; what must change is that the second run stops writing a second record.
//
// Everything here is deterministic: a fake clock, a real store, and the daemon's own publication
// stage 1 (WriteCaptureSidecar) reproduced directly. No sleeps and no wall-clock assertions — the
// e2e x09 run is the end-to-end confirmation, and it is statistical; these are the proof.
//
// Every identifier the fix introduces is restated below rather than imported from the production
// code. Two reasons, and both matter: these tests must COMPILE UNCHANGED on the pre-fix tree, so
// that "they fail without the fix" is a fact about the defect rather than about a build error; and
// a derived id or a metric name is a contract with things outside this package (retrieval reaches a
// capture by its id, an operator reads the counter), so a test that re-derives it independently
// catches a change to it, where one sharing the constant could not.
const (
	// The capture sidecar Op values, which are ipc.OpObserveStop and ipc.OpObserveTool's wire forms.
	rdxOpStop = "observe.stop"
	rdxOpTool = "observe.tool"

	rdxCounterAbsorbed   = "observer.redelivery_absorbed"
	rdxCounterExhausted  = "observer.derived_turn_exhausted"
	rdxCounterUnanswered = "observer.derived_turn_probe_unanswered"
	rdxCounterErrIndex   = "observer.err.index"
	rdxCounterErrStopPut = "observer.err.stop.put"
)

// rdxDerivedToolID spells the identity a tool payload with NO tool_use_id is recorded under.
func rdxDerivedToolID(s core.SessionID, t core.TurnIndex, n int) core.ToolUseID {
	return core.ToolUseID(fmt.Sprintf("tu_%s_%d_%d", s, t, n))
}

// rdxRig is one project: a real store, a real graph, a real observer, and the clock all three read.
type rdxRig struct {
	t       *testing.T
	root    string
	clock   *fakeClock
	metrics obs.Registry
	o       *observer
	st      store.Store
}

func newRdxRig(t *testing.T) *rdxRig {
	t.Helper()
	root := t.TempDir()
	clock := newFakeClock()
	metrics := obs.New(clock)
	o, st := newRealStoreObserver(t, root, clock, metrics)
	r := &rdxRig{t: t, root: root, clock: clock, metrics: metrics, o: o, st: st}
	t.Cleanup(func() { _ = r.st.Close() })
	return r
}

// restart models the flush-time daemon: a fresh observer and a fresh store over the same project,
// with or without the idle Persist having written state/observer.json first.
//
// persist=false is the shape x09 produced — a daemon that never reached an idle interval, so the
// restarted process re-derives every turn from 0.
func (r *rdxRig) restart(persist bool) {
	r.t.Helper()
	if persist {
		require.NoError(r.t, r.o.Persist(context.Background()))
	}
	require.NoError(r.t, r.st.Close())
	r.metrics = obs.New(r.clock)
	r.o, r.st = newRealStoreObserver(r.t, r.root, r.clock, r.metrics)
}

// loseIndexTail is a restart across a power loss that cost index/tool_use.jsonl its tail: the file
// is truncated back to keep bytes — the length it had before the record line was appended — while
// the capture sidecar's link survives.
//
// The asymmetry is the real crash window and not a contrivance. A tool_use line is written to an
// O_APPEND handle and is not fsynced until Flush, where LinkCaptureReference goes through
// paths.WriteAtomic. So a link CAN outlive the record line it names, which is the ordering
// observationRecord's own doc comment cites as the reason it confirms the reference at all.
//
// The store is closed around the truncation and a fresh one opened after it, exactly as restart
// does, because the in-memory tool_use index is replayed from the file at Open: this is the
// flush-time daemon meeting a file the power loss shortened.
func (r *rdxRig) loseIndexTail(keep int) {
	r.t.Helper()
	require.NoError(r.t, r.st.Close())
	require.NoError(r.t, os.Truncate(
		paths.Long(filepath.Join(paths.Of(r.root).Index, "tool_use.jsonl")), int64(keep)))
	r.metrics = obs.New(r.clock)
	r.o, r.st = newRealStoreObserver(r.t, r.root, r.clock, r.metrics)
}

// sidecar is publication order's stage 1, which the daemon runs before EVERY dispatch of a leased
// delivery — the first one and each redelivery alike. Calling it twice with one arrival is
// precisely what a redelivery does, and WriteCaptureSidecar carries any published reference
// forward, which is what the recognition rule then reads.
func (r *rdxRig) sidecar(arrival uint64, op string) core.ObservationID {
	r.t.Helper()
	return r.sidecarFor(testSession, arrival, op)
}

func (r *rdxRig) sidecarFor(sess core.SessionID, arrival uint64, op string) core.ObservationID {
	r.t.Helper()
	id, err := core.NewObservationID(sess, arrival)
	require.NoError(r.t, err)
	require.NoError(r.t, store.WriteCaptureSidecar(r.root, store.CaptureSidecar{
		ObservationID: id, Session: sess, Arrival: arrival, Op: op,
		Fidelity: core.FidelityExact, Outcome: core.OutcomeOK, Bytes: []byte(`{"a":1}`),
	}))
	return id
}

// counter reads one metric off the CURRENT registry (a restart installs a new one).
func (r *rdxRig) counter(name string) int64 { return r.metrics.Counter(name).Value() }

func (r *rdxRig) index() []byte { return rdxReadIndex(r.t, r.root, "tool_use.jsonl") }
func (r *rdxRig) roots() []byte { return rdxReadIndex(r.t, r.root, "roots.jsonl") }

// turn sets the session's turn counter, which is how a test puts the observer where a live daemon
// would have been without driving a dozen real events through it.
func (r *rdxRig) turn(n core.TurnIndex) {
	r.t.Helper()
	st := r.o.session(testSession)
	st.mu.Lock()
	defer st.mu.Unlock()
	st.Turn = n
}

func rdxReadIndex(t *testing.T, root, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(root).Index, name)))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	return b
}

// rdxLine is the slice of an index/tool_use.jsonl line these tests read — the same fields the x09
// flush arm reads, so a failure here and a failure there describe the same thing.
type rdxLine struct {
	Op   string `json:"op"`
	ID   string `json:"id"`
	By   string `json:"by"`
	Tool string `json:"tool"`
	Turn int    `json:"turn"`
}

func rdxParse(t *testing.T, b []byte) []rdxLine {
	t.Helper()
	var out []rdxLine
	for _, ln := range bytes.Split(b, []byte{'\n'}) {
		if len(bytes.TrimSpace(ln)) == 0 {
			continue
		}
		var l rdxLine
		require.NoError(t, json.Unmarshal(ln, &l))
		out = append(out, l)
	}
	return out
}

// rdxIDs returns the ids of every CONTENT record (a mutation record carries no new id), optionally
// narrowed to one tool.
func rdxIDs(t *testing.T, b []byte, tool string) []string {
	t.Helper()
	var out []string
	for _, l := range rdxParse(t, b) {
		if l.Op == "" && (tool == "" || l.Tool == tool) {
			out = append(out, l.ID)
		}
	}
	return out
}

// rdxCaptureIDs is the expected id list for n consecutive SubagentStop captures from turn first.
func rdxCaptureIDs(first core.TurnIndex, turns ...core.TurnIndex) []string {
	out := []string{string(SubagentCaptureID(testSession, first))}
	for _, t := range turns {
		out = append(out, string(SubagentCaptureID(testSession, t)))
	}
	return out
}

// ── Site 1: a redelivered SubagentStop re-captured under a fresh id ──────────────────────────

// TestRedelivery_StopUnderAReusedLeaseIsAbsorbed is the defect's first site, in the three states
// the redelivering process can be in. In every one, the capture this observation already published
// is recognized and the second run writes nothing: no index record, and no capture blob either.
//
// The third case is the one x09 produced. A daemon that never reached an idle Persist re-derives
// turn 0, so on the base it collides with the first run's id under a DIFFERENT root (the blob
// carries its own TS), RecordToolUse refuses it, and what is left behind is an orphan capture blob
// plus a soft-dropped record — the "fifth SubagentStop" x09 counts.
func TestRedelivery_StopUnderAReusedLeaseIsAbsorbed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		restart bool
		persist bool
	}{
		{name: "same process"},
		{name: "restart with a persisted turn", restart: true, persist: true},
		{name: "restart with no persisted state", restart: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRdxRig(t)
			ctx := context.Background()
			id := r.sidecar(1, rdxOpStop)

			_, err := r.o.OnStop(WithObservation(ctx, id), stopOf(true), true)
			require.NoError(t, err)
			require.Equal(t, rdxCaptureIDs(0), rdxIDs(t, r.index(), subagentStop),
				"fixture: the first run captured the subagent")

			if tc.restart {
				r.restart(tc.persist)
			}
			indexBefore, rootsBefore := r.index(), r.roots()

			// The redelivery: the daemon rewrites the sidecar (stage 1) and dispatches under the
			// lease it already holds, so the observation is the same one.
			r.clock.Advance(time.Second)
			r.sidecar(1, rdxOpStop)
			_, err = r.o.OnStop(WithObservation(ctx, id), stopOf(true), true)
			require.NoError(t, err)

			require.Equal(t, string(indexBefore), string(r.index()),
				"an absorbed redelivery appends NO index line; it appended:\n%s",
				r.index()[len(indexBefore):])
			require.Equal(t, string(rootsBefore), string(r.roots()),
				"an absorbed redelivery stores no second capture blob; new roots lines:\n%s",
				r.roots()[len(rootsBefore):])
			require.Equal(t, int64(1), r.counter(rdxCounterAbsorbed),
				"the redelivery must be counted as absorbed, not silently skipped")
		})
	}
}

// TestRedelivery_AbsorbedStopStillAdvancesTheSession is the other half of absorption: the session
// must move PAST the record it recognized, or the next Stop re-mints the id just recognized and the
// collision arrives one event later.
func TestRedelivery_AbsorbedStopStillAdvancesTheSession(t *testing.T) {
	r := newRdxRig(t)
	ctx := context.Background()
	first := r.sidecar(1, rdxOpStop)

	_, err := r.o.OnStop(WithObservation(ctx, first), stopOf(true), true)
	require.NoError(t, err)

	// A fresh daemon with no persisted state: its turn counter starts at 0 again.
	r.restart(false)
	r.sidecar(1, rdxOpStop)
	_, err = r.o.OnStop(WithObservation(ctx, first), stopOf(true), true)
	require.NoError(t, err)

	// adoptTurn's own contract, asserted DIRECTLY on the session rather than through the next
	// Stop's id. Going through the id does not pin it: the free-turn probe (G1) steps over an
	// occupied derived id anyway, so the next Stop lands on turn 1 whether or not the session was
	// advanced, and this test passed with adoptTurn removed. That was a recorded negative control
	// coming back NOT CAUGHT, and this assertion is what closes it.
	//
	// It matters beyond tidiness, because the probe is gated on a leased identity: an in-process
	// caller does not probe, so a session left pointing at a turn the index already holds re-mints
	// that id and has its capture soft-dropped.
	adopted := r.o.session(testSession)
	adopted.mu.Lock()
	turn := adopted.Turn
	adopted.mu.Unlock()
	require.Equal(t, core.TurnIndex(1), turn,
		"an absorbed redelivery must advance the session PAST the record it recognized")

	// A genuinely new Stop of the same session, under its own identity.
	r.clock.Advance(time.Second)
	second := r.sidecar(2, rdxOpStop)
	_, err = r.o.OnStop(WithObservation(ctx, second), stopOf(true), true)
	require.NoError(t, err)

	require.Equal(t, rdxCaptureIDs(0, 1), rdxIDs(t, r.index(), subagentStop),
		"the absorbed redelivery adopts turn 0's record, so the next distinct Stop is turn 1")
}

// TestRedelivery_StopMintsPastAnOccupiedTurn is guard G1, and it is the interleaving neither the
// evidence test nor a single-Stop sweep can see: absorption alone is not enough.
//
// Two Stops of one session can be in flight at once — the ingest pool has at least two workers, the
// turn is taken inside the handler under the session lock, and the transport ACK is written before
// any worker touches the job — so turns are assigned in an order that is not the WAL's. When the
// shutdown cuts the delivery that took the LOWER turn, the flush daemon recognizes it and adopts
// turn+1, which is an id a sibling Stop already holds. Minting blind there makes RecordToolUse
// answer ErrAppendOnly (the blob carries its own TS, so the root differs), the capture is
// soft-dropped, and four Stop events leave three records — x09's count assertion, red.
//
// The probe is what closes it: the mint walks to the first id the index does not hold.
func TestRedelivery_StopMintsPastAnOccupiedTurn(t *testing.T) {
	r := newRdxRig(t)
	ctx := context.Background()

	// Three Stops that landed, at turns 12, 13 and 14. Only the turn-12 delivery was leased and
	// linked; its acknowledgement is the one the shutdown cut.
	r.turn(12)
	cut := r.sidecar(1, rdxOpStop)
	_, err := r.o.OnStop(WithObservation(ctx, cut), stopOf(true), true)
	require.NoError(t, err)
	for range 2 {
		r.clock.Advance(time.Second)
		_, err = r.o.OnStop(ctx, stopOf(true), true)
		require.NoError(t, err)
	}
	require.Equal(t, rdxCaptureIDs(12, 13, 14), rdxIDs(t, r.index(), subagentStop),
		"fixture: turns 12, 13 and 14 are taken")

	// The flush daemon: no persisted state, so its counter starts at 0.
	r.restart(false)

	// It redelivers the cut delivery, recognizes it, and adopts turn 13 — which 13 already holds.
	r.clock.Advance(time.Second)
	r.sidecar(1, rdxOpStop)
	_, err = r.o.OnStop(WithObservation(ctx, cut), stopOf(true), true)
	require.NoError(t, err)
	require.Equal(t, int64(1), r.counter(rdxCounterAbsorbed))

	// And then a Stop nobody has ever processed, under its own identity.
	r.clock.Advance(time.Second)
	fresh := r.sidecar(2, rdxOpStop)
	_, err = r.o.OnStop(WithObservation(ctx, fresh), stopOf(true), true)
	require.NoError(t, err)

	require.Equal(t, rdxCaptureIDs(12, 13, 14, 15), rdxIDs(t, r.index(), subagentStop),
		"four Stop events must leave four records: the fresh capture mints past the occupied turns")
	require.Equal(t, int64(0), r.counter(rdxCounterErrIndex),
		"no capture may be soft-dropped to an id collision")
	require.Equal(t, int64(0), r.counter(rdxCounterExhausted))
}

// TestRedelivery_FreeTurnProbeIsGatedOnALeasedIdentity is the POSITIVE twin of the probe's gate.
//
// TestOnStop_CaptureIsDeterministic (stop_test.go) is the negative one and is the most load-bearing
// existing test in this change: it drives a second observer over the same store, session and turn
// from an in-process caller and requires that second capture to mint the SAME id, dedup completely
// and add no object. An ungated probe would step past turn 0 and write a novel blob, failing it.
//
// So the rule is "a leased delivery mints past a collision; an unleased one keeps today's
// behaviour", and both halves are pinned: a leased second capture over the same state DOES advance,
// and adds exactly one object.
func TestRedelivery_FreeTurnProbeIsGatedOnALeasedIdentity(t *testing.T) {
	r := newRdxRig(t)
	ctx := context.Background()

	_, err := r.o.OnStop(ctx, stopOf(true), true)
	require.NoError(t, err)
	require.Equal(t, rdxCaptureIDs(0), rdxIDs(t, r.index(), subagentStop))

	// A second observer over the same store, at the same turn — the deterministic-capture shape —
	// but this time the delivery carries an identity nothing has published.
	r.restart(false)
	require.NoError(t, r.st.Flush(ctx))
	before, err := r.st.Stats(ctx)
	require.NoError(t, err)

	id := r.sidecar(1, rdxOpStop)
	_, err = r.o.OnStop(WithObservation(ctx, id), stopOf(true), true)
	require.NoError(t, err)
	require.NoError(t, r.st.Flush(ctx))
	after, err := r.st.Stats(ctx)
	require.NoError(t, err)

	require.Equal(t, rdxCaptureIDs(0, 1), rdxIDs(t, r.index(), subagentStop),
		"a leased delivery whose derived id is taken mints the next free one rather than being dropped")
	require.Equal(t, before.Objects+1, after.Objects,
		"it is a real second capture: one new blob, not a dedup hit")
}

// TestRedelivery_DistinctObservationsAreNotAbsorbed is the over-absorption control. x09's own shape
// is four byte-identical Stop payloads, so "identical content" must never be what absorption keys
// on: two deliveries are two captures, and only a REDELIVERY of one identity is absorbed.
func TestRedelivery_DistinctObservationsAreNotAbsorbed(t *testing.T) {
	r := newRdxRig(t)
	ctx := context.Background()

	for _, arrival := range []uint64{1, 2} {
		id := r.sidecar(arrival, rdxOpStop)
		_, err := r.o.OnStop(WithObservation(ctx, id), stopOf(true), true)
		require.NoError(t, err)
	}

	require.Equal(t, rdxCaptureIDs(0, 1), rdxIDs(t, r.index(), subagentStop),
		"two deliveries of identical payloads are two captures")
	require.Equal(t, int64(0), r.counter(rdxCounterAbsorbed))
}

// TestRedelivery_StopWithNoIdentityKeepsTodaysBehaviour: an in-process caller has no delivery to
// lose and no observation to be recognized by, so nothing is absorbed and nothing is probed.
func TestRedelivery_StopWithNoIdentityKeepsTodaysBehaviour(t *testing.T) {
	r := newRdxRig(t)
	ctx := context.Background()

	for range 2 {
		_, err := r.o.OnStop(ctx, stopOf(true), true)
		require.NoError(t, err)
		r.clock.Advance(time.Second)
	}

	require.Equal(t, rdxCaptureIDs(0, 1), rdxIDs(t, r.index(), subagentStop))
	require.Equal(t, int64(0), r.counter(rdxCounterAbsorbed))
}

// TestRedelivery_AbsorbedStopDoesNotRestampTheSessionClock pins the two pieces of per-event
// bookkeeping the recognition arm deliberately does NOT do, because a redelivery is not a new host
// event but one already observed, arriving again.
//
// Both omissions are one-directional and both would be wrong the other way:
//
//   - st.LastTS keeps naming the last HOST event. §6.6's GapSeconds is measured against it, so
//     advancing it to the redelivery's instant — the flush-time daemon, minutes later — would
//     understate the real idle gap before the next event. In this process the first run already set
//     it correctly, so restamping could only ever make a right value wrong.
//   - st.SubagentSince keeps pointing where the first run closed the window. Re-closing it would
//     drop every tool use that arrived since from the NEXT capture's hash list, which is exactly
//     the G10.1 detail a subagent capture exists to preserve.
//
// The read between the two dispatches is what gives both assertions teeth: it moves LastTS and it
// grows the window, so a recognition arm that re-ran either piece of bookkeeping would be visible.
func TestRedelivery_AbsorbedStopDoesNotRestampTheSessionClock(t *testing.T) {
	r := newRdxRig(t)
	ctx := context.Background()
	id := r.sidecar(1, rdxOpStop)

	_, err := r.o.OnStop(WithObservation(ctx, id), stopOf(true), true)
	require.NoError(t, err)

	// A real host event after the capture: the window grows and the session's clock moves with it.
	r.clock.Advance(time.Second)
	_, err = r.o.OnToolUse(ctx, readOf("toolu_gap", supersedePath, rdxBody))
	require.NoError(t, err)
	lastHostEvent := r.o.now()

	// The redelivery arrives long afterwards, which is when a flush-time daemon drains the spool.
	r.clock.Advance(10 * time.Minute)
	r.sidecar(1, rdxOpStop)
	_, err = r.o.OnStop(WithObservation(ctx, id), stopOf(true), true)
	require.NoError(t, err)
	require.Equal(t, int64(1), r.counter(rdxCounterAbsorbed), "fixture: the redelivery was absorbed")

	st := r.o.session(testSession)
	st.mu.Lock()
	defer st.mu.Unlock()
	require.Equal(t, lastHostEvent, st.LastTS,
		"an absorbed redelivery must leave LastTS naming the last HOST event; restamping it to the "+
			"redelivery's own instant makes the next event's GapSeconds understate the real gap")
	require.Equal(t, 0, st.SubagentSince,
		"the subagent window must stay where the first run closed it: re-closing it here drops the "+
			"tool uses that arrived since from the next capture's hash list (G10.1)")
	require.Len(t, st.ToolUses, 1, "fixture: the read between the two dispatches is in the window")
}

// The leased worker must retain unavailable work. The client still preserves
// host output; the worker error must not become a false committed frontier.
func TestRedelivery_ProbeOnAStoreThatCannotAnswerNamesTheRightCause(t *testing.T) {
	r := newRdxRig(t)
	id := r.sidecar(1, rdxOpStop)
	require.NoError(t, r.st.Close())
	_, err := r.o.OnStop(WithObservation(context.Background(), id), stopOf(true), true)
	require.ErrorIs(t, err, ErrUnpublished)
	require.Equal(t, int64(1), r.counter(rdxCounterErrIndex))
	require.Equal(t, int64(0), r.counter(rdxCounterExhausted))
	require.Equal(t, int64(0), r.counter(rdxCounterErrStopPut), "unavailable identity prevents a new capture")
}

// ── Guard G3: a sidecar that does not describe THIS delivery is not its publication ──────────

// Conflicting identity metadata must retain the delivery for recovery, without
// treating it as either a successful publication or a new event.
func TestRedelivery_SidecarDescribingAnotherDeliveryIsNotRecognized(t *testing.T) {
	for _, tc := range []struct {
		name    string
		session core.SessionID
		op      string
	}{
		{name: "another session's sidecar", session: "sess-somebody-else", op: rdxOpStop},
		{name: "another op's sidecar", session: testSession, op: rdxOpTool},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRdxRig(t)
			ctx := context.Background()
			id := r.sidecar(1, rdxOpStop)

			_, err := r.o.OnStop(WithObservation(ctx, id), stopOf(true), true)
			require.NoError(t, err)

			// The same observation id, now describing a different delivery. WriteCaptureSidecar
			// carries the published reference forward, so the only thing that changes is what the
			// record CLAIMS to be about.
			require.NoError(t, store.WriteCaptureSidecar(r.root, store.CaptureSidecar{
				ObservationID: id, Session: tc.session, Arrival: 1, Op: tc.op,
				Fidelity: core.FidelityExact, Outcome: core.OutcomeOK, Bytes: []byte(`{"a":1}`),
			}))

			r.clock.Advance(time.Second)
			_, err = r.o.OnStop(WithObservation(ctx, id), stopOf(true), true)
			require.ErrorIs(t, err, ErrUnpublished)

			require.Equal(t, rdxCaptureIDs(0), rdxIDs(t, r.index(), subagentStop),
				"conflicting sidecar identity must neither absorb nor duplicate the event")
			require.Equal(t, int64(0), r.counter(rdxCounterAbsorbed))
		})
	}
}

// ── The index confirmation: a reference the index cannot corroborate is not a publication ────

// A surviving capture link is not sufficient evidence. A missing legacy record
// may be reconstructed from the durable intent; a conflicting record must remain
// unavailable rather than be overwritten or followed by a duplicate identity.
func TestRedelivery_ReferenceTheIndexDoesNotConfirmIsNotRecognized(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprintf("conflict=%v", conflict), func(t *testing.T) {
			r := newRdxRig(t)
			ctx := context.Background()
			id := r.sidecar(1, rdxOpStop)
			_, err := r.o.OnStop(WithObservation(ctx, id), stopOf(true), true)
			require.NoError(t, err)
			original := r.index()
			sc, err := store.ReadCaptureSidecar(r.root, id)
			require.NoError(t, err)
			require.True(t, sc.Published)
			r.loseIndexTail(0)
			if conflict {
				// Deliberate on-disk corruption, not an authorized Store writer.
				require.NoError(t, r.st.Close())
				var line map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(bytes.TrimSpace(original), &line))
				line["root"], err = json.Marshal(core.HashBytes("v6.corrupt-record", []byte("different")))
				require.NoError(t, err)
				bad, err := json.Marshal(line)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(paths.Long(filepath.Join(paths.Of(r.root).Index, "tool_use.jsonl")), append(bad, '\n'), 0o600))
				r.o, r.st = newRealStoreObserver(t, r.root, r.clock, r.metrics)
			}
			r.clock.Advance(time.Second)
			before := r.index()
			_, err = r.o.OnStop(WithObservation(ctx, id), stopOf(true), true)
			if conflict {
				require.ErrorIs(t, err, ErrUnpublished)
				require.Equal(t, before, r.index(), "conflicting evidence must remain untouched")
				require.Equal(t, int64(0), r.counter(rdxCounterAbsorbed))
				return
			}
			require.NoError(t, err)
			require.Equal(t, rdxCaptureIDs(0), rdxIDs(t, r.index(), subagentStop))
			rec, err := r.st.ToolUse(ctx, sc.ToolUseID)
			require.NoError(t, err)
			require.Equal(t, sc.Root, rec.Root, "repair restores the original referent")
			require.Equal(t, int64(1), r.counter(rdxCounterAbsorbed))
		})
	}
}

// ── The derived TOOL id: the same class, in the tool path ────────────────────────────────────

// TestRedelivery_DerivedToolIDIsAbsorbed: a payload with no tool_use_id is recorded under an id
// derived from this process's state, so it carries the Stop path's defect for the same reason.
//
// The two arms fail on the pre-fix tree for different reasons, and only the first one fails at all:
//
//   - SAME PROCESS. The first run appended the tool use to the subagent window, so the redelivery
//     derives tu_<s>_<turn>_<1> where the first took tu_<s>_<turn>_<0> — a DIFFERENT id, a second
//     record, and nothing anywhere to notice it is one host event.
//   - AFTER A RESTART the window is empty again, so the redelivery re-derives the SAME id over the
//     same bytes, and the store's own append-only dedup absorbs it. The defect is invisible here,
//     which is exactly why the same-process arm is the one that pins it — and why the tool path's
//     real predicate is "one record per (id, root)" rather than "per observation".
func TestRedelivery_DerivedToolIDIsAbsorbed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		restart bool
	}{
		{name: "same process"},
		{name: "restart with no persisted state", restart: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRdxRig(t)
			ctx := context.Background()
			id := r.sidecar(1, rdxOpTool)
			e := readOf("", supersedePath, rdxBody)

			_, err := r.o.OnToolUse(WithObservation(ctx, id), e)
			require.NoError(t, err)
			require.Equal(t, []string{string(rdxDerivedToolID(testSession, 0, 0))},
				rdxIDs(t, r.index(), ""), "fixture: the first run recorded under the derived id")

			if tc.restart {
				r.restart(false)
			}
			indexBefore := r.index()

			r.clock.Advance(time.Second)
			r.sidecar(1, rdxOpTool)
			_, err = r.o.OnToolUse(WithObservation(ctx, id), e)
			require.NoError(t, err)

			require.Equal(t, []string{string(rdxDerivedToolID(testSession, 0, 0))},
				rdxIDs(t, r.index(), ""),
				"one record per observation for a payload with no tool_use_id; the redelivery appended:\n%s",
				r.index()[len(indexBefore):])
		})
	}
}

// Retains guard G2's identifier but replaces its duplicate-minting criterion:
// an observation names its original record even when a replay would derive
// another representation. A real second delivery must have a distinct lease.
func TestRedelivery_DerivedToolIDWithADifferentRootMintsFreshly(t *testing.T) {
	r := newRdxRig(t)
	ctx := context.Background()
	id := r.sidecar(1, rdxOpTool)
	_, err := r.o.OnToolUse(WithObservation(ctx, id), readOf("", supersedePath, rdxBody))
	require.NoError(t, err)
	beforeIndex, beforeRoots := r.index(), r.roots()
	sc, err := store.ReadCaptureSidecar(r.root, id)
	require.NoError(t, err)
	originalRoot := sc.Root
	r.restart(false)
	r.clock.Advance(time.Second)
	r.sidecar(1, rdxOpTool)
	_, err = r.o.OnToolUse(WithObservation(ctx, id), readOf("", supersedePath, rdxBody+"changed derivation\n"))
	require.NoError(t, err)
	require.Equal(t, beforeIndex, r.index(), "a replay must retain the original record")
	require.Equal(t, beforeRoots, r.roots(), "recognition precedes new object derivation")
	sc, err = store.ReadCaptureSidecar(r.root, id)
	require.NoError(t, err)
	require.Equal(t, originalRoot, sc.Root)
}

// rdxBody is the tool-result content these tests read. It is long enough to chunk and to carry a
// path, which is what makes it a supersession candidate as well as a record.
const rdxBody = "export async function refreshToken() {}\nexport const ttl = 30\n"
