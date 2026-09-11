package observer

import (
	"context"
	"errors"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
)

// Delivery identity: how a handler recognizes a redelivery of work it has already published, and
// how it mints a derived id that nothing else already holds. This is carried defect SP08-D2.
//
// internal/daemon's dispatch contract is explicit that "Restart does not retain this set, so
// handlers must tolerate at-least-once delivery": a delivery whose handler RAN but whose
// acknowledgement never reached the journal — the pre-flush shutdown cancels the worker between the
// observer's append and commitDelivery — is redelivered under its STORED lease, with the same
// delivery token and the same ObservationID, and the handler runs a second time. That redelivery is
// correct and must stay. What has to change is what the second run does.
//
// There are two identity regimes, and only one of them needs help:
//
//   - A HOST-identified record (a payload carrying tool_use_id) already names itself. The store's
//     own append-only dedup recognizes the second delivery, because the id and the root are the
//     same bytes either time; no observation is consulted, and none could be — two deliveries
//     carrying one host id are indistinguishable at this layer.
//   - A DERIVED-id record (a SubagentStop capture, a tool payload with no tool_use_id) is named
//     from this process's per-session turn counter, which a second process re-derives differently
//     (0 on a fresh daemon, or whatever state/observer.json last held). Nothing in the id ties it to
//     the delivery, so the store sees a novel record and appends a second one for one host event.
//
// The join that fixes the second case is the capture sidecar. The daemon writes it before every
// dispatch, keyed by the ObservationID; LinkCaptureReference stamps the published reference into it
// once the index record lands. So "has this delivery already published a record?" is answerable
// from durable state, in one file read, and observationRecord is that question.

// derivedTurnProbe bounds how far freeDerivedTurn looks for an unoccupied derived id.
//
// It is a bound on work, not a configuration default (§11.6 D11): the first probed id is free in
// every normal run, and the probe exists for the case where this process's turn counter lags the
// index — a restored state file, or a turn another in-flight handler for the same session already
// took. 64 consecutive occupied turns at or after st.Turn is a state file lagging the index by more
// than a session's worth of captures, which no shutdown path produces; past it the mint falls back
// to today's behaviour and says so through a counter rather than probing unboundedly on the hot
// path.
const derivedTurnProbe = 64

// The capture sidecar's Op values this package matches against, spelled as literals because §3.2
// does not give observer internal/ipc: these are ipc.OpObserveStop and ipc.OpObserveTool's wire
// forms, which publishCapture copies verbatim into CaptureSidecar.Op.
const (
	opObserveStop = "observe.stop"
	opObserveTool = "observe.tool"
)

// The counters this file registers.
const (
	// counterRedelivery counts deliveries recognized as a redelivery of a publication this
	// observation already made, and absorbed: no second record, no second blob, no re-run pipeline.
	counterRedelivery = "observer.redelivery_absorbed"
	// counterLegacySupersede counts observers built over a Store WITHOUT the atomic
	// record-plus-marks capability, at construction. The path they fall back to is defect-bearing
	// by design — it is the 1+N write shape SP08-D2's second mechanism lives in — so a production
	// composition that lands on it must be visible rather than silently slower to notice.
	counterLegacySupersede = "observer.legacy_supersede_path"
	// counterTurnExhausted counts mints that probed derivedTurnProbe occupied ids and gave up,
	// taking today's collision behaviour.
	counterTurnExhausted = "observer.derived_turn_exhausted"
)

// observationRecord returns the index record delivery obs already published, and whether there is
// one. It is the recognition half of the identity rule.
//
// The evidence is the capture sidecar's verified reference — the ToolUseID and Root
// LinkCaptureReference stamped after the record landed — CONFIRMED against the index, because a
// sidecar is written by the daemon and the index is written here: a reference naming a record the
// index does not hold (a link that outlived its record line through a power loss before the index
// was flushed) must read as "not published", not as an id to reuse.
//
// sess and op are checked against the sidecar as well, and that check is load-bearing rather than
// defensive. ObservationID is H(session‖arrival) over the delivery journal's dense per-session
// arrival counter, and nothing retires that counter today (carried defect SP20-D4 pins exactly
// that). A future retention or compaction pass that restarts those counters would recycle
// ObservationIDs, and a recycled id whose old sidecar is still Published would otherwise make this
// function swallow a genuinely new capture. A sidecar describing a different session, or a
// different op, is not this delivery's publication whatever its id says.
//
// Every failure answers false, and that direction is deliberate: an unreadable, missing or pruned
// sidecar degrades recognition to a miss, which is today's duplicate — the defect this is fixing,
// not a worse one. The invariant it does depend on is stated in SP08-D2's Resolution: an
// ObservationID is never reused for a different delivery, and a sidecar outlives its delivery's
// redelivery window.
func (o *observer) observationRecord(ctx context.Context, obs core.ObservationID,
	sess core.SessionID, op string,
) (store.ToolUseRecord, bool) {
	if obs == "" {
		return store.ToolUseRecord{}, false // an unleased delivery or an in-process caller
	}
	sc, err := store.ReadCaptureSidecar(o.opt.ProjectRoot, obs)
	if err != nil || !sc.Published || sc.ToolUseID == "" {
		return store.ToolUseRecord{}, false
	}
	if sc.Session != sess || sc.Op != op {
		return store.ToolUseRecord{}, false
	}
	rec, err := o.opt.Store.ToolUse(ctx, sc.ToolUseID)
	if err != nil || rec.Root != sc.Root {
		return store.ToolUseRecord{}, false
	}
	return rec, true
}

// linkObservation is publication order's SECOND stage for any record: the verified reference joined
// to the durable capture the daemon already made, keyed by this delivery's observation identity.
//
// A delivery with no identity has no link to make and skips the stage, which is what keeps an
// in-process caller — a test, a tool with no daemon behind it — from claiming a durable identity it
// was never given.
func (o *observer) linkObservation(obs core.ObservationID, rec store.ToolUseRecord) error {
	if obs == "" {
		return nil
	}
	return store.LinkCaptureReference(o.opt.ProjectRoot, obs, store.CaptureReference{
		ToolUseID: rec.ID, Root: rec.Root,
	})
}

// freeDerivedTurn returns the first index at or after start whose derived id the index does not
// already hold, and whether it found one inside derivedTurnProbe.
//
// The index it walks is whichever component of the derived id is free to move: the TURN for a
// SubagentStop capture (subagent_<s>_<turn>), where the turn, the id, the record and the capture
// blob's own Turn field all move together and stay in agreement; the WINDOW POSITION for a tool
// record with no host id (tu_<s>_<turn>_<n>), where the turn is the session's own and may not be
// rewritten to dodge a collision. mint spells the id, so the probe and the caller can never
// disagree about it — the same discipline SubagentCaptureID is exported under.
//
// This is what makes a derived id collision-aware, and it closes a class the recognition rule alone
// does not. Recognition absorbs a redelivery of a record that WAS published; it says nothing about
// a delivery nobody has ever processed, minted by a process whose turn counter points into a range
// another handler already used. That is reachable in ordinary operation, not only after a crash:
// the ingest worker pool has at least two workers, the turn is taken inside the handler under the
// session lock, and the transport ACK is written before any worker touches the job — so two Stops of
// one session can be in flight together and take their turns in an order that is not the WAL's.
// Minting blind into that range makes RecordToolUse answer ErrAppendOnly (a different root: the
// capture blob carries its own TS), the capture is soft-dropped, and one host event ends up with no
// record at all.
//
// Probing is cheap: FSStore.ToolUse is an in-memory map read under a read lock, no I/O, and the
// first probed id is free in every normal run.
//
// The caller gates this on the delivery carrying an observation identity, and that asymmetry is
// deliberate. A leased delivery is one the system promised to record durably, so losing its capture
// to an id collision is the failure this row exists to remove; an in-process caller has no delivery
// to lose, and TestOnStop_CaptureIsDeterministic pins the behaviour it must keep — a second
// observer replaying the same session over the same store re-mints the SAME id, dedups completely
// and adds no object. An ungated probe would step past that id and write a novel blob.
func (o *observer) freeDerivedTurn(ctx context.Context, start int,
	mint func(int) core.ToolUseID,
) (int, bool) {
	for i := start; i < start+derivedTurnProbe; i++ {
		if _, err := o.opt.Store.ToolUse(ctx, mint(i)); errors.Is(err, core.ErrNotFound) {
			return i, true
		}
	}
	return start, false
}

// adoptTurn moves the session past a derived-id record a redelivery found already published, so the
// NEXT event of that session does not re-mint the id it just recognized.
//
// It only ever moves forward: a redelivery arriving after the session has already advanced past the
// record must not rewind the counter.
func adoptTurn(st *sessionState, rec store.ToolUseRecord) {
	if next := rec.Turn + 1; next > st.Turn {
		st.Turn = next
	}
}

// rememberToolUseOnce restores rec's membership in the SubagentStop window when this process does
// not already hold it, and does nothing when it does.
//
// A recognized replay re-runs none of the derived pipeline — that state belongs to the first run and
// recomputing it from the redelivering process's view produces wrong data — but window membership is
// the exception, because its correct value after a replay is "contains this tool use" in ANY
// process. A restarted daemon that skipped this would take the next subagent capture with the tool
// result missing from its hash list, which is the G10.1 detail the capture exists for.
//
// The scan is linear over a ring bounded at subagentWindowCap, and it is what keeps the same-process
// case idempotent: TestOnStop_CaptureIsDeterministic replays two tool uses through a second observer
// over one store and requires the resulting capture blob to be byte-identical, which a duplicated
// window entry would break.
func (o *observer) rememberToolUseOnce(st *sessionState, rec store.ToolUseRecord) {
	for _, t := range st.ToolUses {
		if t.ID == rec.ID {
			return
		}
	}
	o.rememberToolUse(st, rec)
}
