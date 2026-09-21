package observer

import (
	"context"
	"errors"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
)

// Leased replay identity is independent of content and host tool IDs. A durable
// observation intent binds the original legacy record before publication; the
// capture sidecar links it afterward. Replays complete that intent without
// deriving a second record from a restarted process's turn counter.

// derivedTurnProbe bounds the search for a free legacy derived ID. Exhaustion
// or an unavailable lookup retains the leased delivery for recovery.
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
	//
	// It is bumped ONCE PER OBSERVER CONSTRUCTION rather than once per event, so read it as a flag
	// saying "this process's observer is on the legacy path" and not as a rate: a value of 1 with a
	// million events through it means every one of those events took the separate-writes path.
	counterLegacySupersede = "observer.legacy_supersede_path"
	// counterTurnExhausted counts a bounded search with every candidate occupied.
	// It does not establish why the process's local state trails those records.
	counterTurnExhausted = "observer.derived_turn_exhausted"
	// counterProbeUnanswered counts mints whose probe stopped because the store could not ANSWER
	// whether an id was taken: core.ErrDegraded from a closed store, or any other failure that is
	// not ErrNotFound.
	//
	// A failed lookup is counted separately from a range of occupied IDs.
	counterProbeUnanswered = "observer.derived_turn_probe_unanswered"
)

// observationRecord completes a durable publication intent or confirms a legacy
// capture link against the index. Missing bindings permit a first publication;
// conflicting, incomplete or unreadable evidence retains the leased delivery.
// Session and operation must agree before either recovery path can be accepted.
func (o *observer) observationRecord(ctx context.Context, obs core.ObservationID,
	sess core.SessionID, op string,
) (store.ToolUseRecord, bool, error) {
	if obs == "" {
		return store.ToolUseRecord{}, false, nil
	}
	sc, err := store.ReadCaptureSidecar(o.opt.ProjectRoot, obs)
	if err != nil {
		return store.ToolUseRecord{}, false, o.unpublished(stageLink)
	}
	if sc.Session != sess || sc.Op != op {
		return store.ToolUseRecord{}, false, o.unpublished(stageLink)
	}
	recovery, ok := o.opt.Store.(store.ObservationRecovery)
	if !ok {
		return store.ToolUseRecord{}, false, o.unpublished(stageIndex)
	}
	rec, recoveryErr := recovery.RecoverToolUseByObservation(ctx, obs)
	if recoveryErr == nil {
		if rec.Session != sess || !recordMatchesObservationOp(rec, op) {
			return store.ToolUseRecord{}, false, o.unpublished(stageIndex)
		}
		return rec, true, nil
	}
	if !errors.Is(recoveryErr, core.ErrNotFound) {
		return store.ToolUseRecord{}, false, o.unpublished(stageIndex)
	}
	// Compatible recovery for a legacy published capture without a publication intent.
	if sc.Published && sc.ToolUseID != "" {
		rec, lookupErr := o.opt.Store.ToolUse(ctx, sc.ToolUseID)
		if lookupErr == nil && rec.Root == sc.Root && rec.Session == sess && recordMatchesObservationOp(rec, op) {
			return rec, true, nil
		}
		return store.ToolUseRecord{}, false, o.unpublished(stageIndex)
	}
	return store.ToolUseRecord{}, false, nil
}

func recordMatchesObservationOp(rec store.ToolUseRecord, op string) bool {
	switch op {
	case opObservePrompt:
		return rec.Tool == userPromptSubmit
	case opObserveStop:
		return rec.Tool == subagentStop
	case opObserveTool:
		return rec.Tool != userPromptSubmit && rec.Tool != subagentStop
	default:
		return false
	}
}

// finishObservation makes the referent and index durable before linking them to
// the accepted delivery. A failed or unavailable stage must retain that delivery.
func (o *observer) finishObservation(ctx context.Context, obs core.ObservationID, rec store.ToolUseRecord) error {
	if obs == "" {
		return nil
	}
	if err := o.syncObservation(ctx, rec.Root); err != nil {
		return err
	}
	if err := o.linkObservation(obs, rec); err != nil {
		return o.unpublished(stageLink)
	}
	return nil
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

// freeDerivedTurn probes a bounded range of legacy IDs for a new leased event.
// A subagent record moves its turn; a tool record moves only its window position.
// Only ErrNotFound proves a free ID. The caller retains work if no trustworthy
// answer is available, and the store independently enforces pending reservations.
// Unleased in-process calls retain their historical deterministic ID behavior.
func (o *observer) freeDerivedTurn(ctx context.Context, start int,
	mint func(int) core.ToolUseID,
) (int, bool) {
	for i := start; i < start+derivedTurnProbe; i++ {
		_, err := o.opt.Store.ToolUse(ctx, mint(i))
		switch {
		case errors.Is(err, core.ErrNotFound):
			return i, true
		case err != nil:
			o.count(counterProbeUnanswered)
			return start, false
		}
	}
	o.count(counterTurnExhausted)
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
