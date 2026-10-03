package daemon

import (
	"context"
	"errors"
	"fmt"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/qompack/qompack/internal/store"
)

// How L0 events reach L3. SP-05's daemon routes every hook to a Services function seam and never
// calls Services.Sched itself; scheduler.Runtime has no hook-shaped method it could call. Without
// this tap nothing would ever call Observe, NotifyActivity or Persist. It decorates the five L0
// seams, and the PreCompact seam for the compaction boundary, through SP-05's own late-binding
// hook, so no SP-05 or SP-08 file is edited.

// The tap's instruments and the causes it closes segments with.
const (
	counterTapNoRecord       = "sched.tap.no_record"
	counterTapPanic          = "sched.tap.panic"
	counterTapBoundary       = "sched.tap.boundary"
	counterTapBoundaryPrefix = "sched.tap.boundary."

	causeChangepoint = "changepoint"
	causeTodo        = "todo"
	causeTest        = "test"
	causeCommit      = "commit"
	// causeCompact is the host's compaction: the boundary every session has (F-UAT03-1).
	causeCompact = "compact"

	// counterTapCompactForeign counts compactions of a session other than the one this runtime is
	// bound to, whose segment the tap therefore leaves alone.
	counterTapCompactForeign = "sched.tap.compact_foreign"
	// counterTapCompactUnobserved counts compactions whose open segment the tap left open because
	// the runtime holds no token account for it: closed, it would record zero tokens for good.
	counterTapCompactUnobserved = "sched.tap.compact_unobserved"

	// counterTapBindFirstHook counts binds made by a session's first hook on a runtime bound to no
	// session: a daemon restarted in the middle of the session, which gets no SessionStart for it.
	counterTapBindFirstHook = "sched.tap.bind.first_hook"
	// counterTapBindNotLive counts hooks that found the runtime unbound but named a session the
	// daemon's registry does not hold live (a replayed delivery), which therefore did not bind.
	counterTapBindNotLive = "sched.tap.bind.not_live"
	// counterTapRedelivery counts deliveries the tap recognized as a replay of one it had already
	// applied (schedRuntime.applied) and therefore left alone: no token fold, no detector
	// observation, no anchor moved to the instant of the replay.
	counterTapRedelivery = "sched.tap.redelivery"

	msgTapPanic = "scheduler tap panicked; inner seam result returned unchanged"
)

// schedTap is the decorator state: the runtime, and the logger/registry it reports through.
type schedTap struct {
	r       *schedRuntime
	log     logging.Logger
	metrics obs.Registry
}

// WrapServicesForScheduler decorates the five L0 seams in place. Registered from the composition
// root as opts.Bind(func(s *Services) { WrapServicesForScheduler(s, sched, schedOpts) }).
//
// ORDERING IS LOAD-BEARING: Bind hooks run in registration order, so this Bind must be
// registered AFTER SP-08's (which sets the seams). A nil inner seam is tolerated — the tap still
// runs — so a build without SP-08 degrades to "scheduler sees timestamps only". Every wrapper
// calls the inner seam FIRST (SP-08 has then already written the tool-use record the tap reads)
// and returns the inner result unchanged: a tap failure, including a panic, never changes a
// hook's result. rt == nil, or a Runtime that is not the daemon's, leaves s untouched. The three
// seams SP-12 does not decorate — Rehydrate, MCPInitialized, StatusExtra — and Services.Mode are
// never read or replaced.
//
// PreCompact is the one seam decorated the other way round: the tap runs BEFORE the inner seam.
// A compaction is a segment boundary — the host is about to replace everything so far with its
// summary — and the checkpointer the inner seam calls encodes closed segments only. So the segment
// holding the compacted span is closed first, and the seal then encodes it; closed after, it was
// left out of the very checkpoint sealed for it (F-UAT03-1). It is decorated only when set: with no
// checkpointer there is no seal to close a segment for.
func WrapServicesForScheduler(s *Services, rt scheduler.Runtime, o SchedulerRuntimeOptions) {
	if s == nil || rt == nil {
		return
	}
	r, ok := rt.(*schedRuntime)
	if !ok {
		log := o.Log
		if log == nil {
			log = logging.Nop()
		}
		log.Warn("scheduler tap: runtime is not the daemon's; L0 seams left undecorated")
		return
	}
	t := &schedTap{r: r, log: o.Log, metrics: o.Metrics}
	if t.log == nil {
		t.log = r.log
	}
	if t.metrics == nil {
		t.metrics = r.metrics
	}

	innerStart, innerTool, innerStop, innerPrompt, innerEnd := s.SessionStart, s.ObserveTool, s.ObserveStop, s.ObservePrompt, s.SessionEnd

	s.SessionStart = func(ctx context.Context, e hookio.Event) (hookio.Output, error) {
		var out hookio.Output
		var err error
		if innerStart != nil {
			out, err = innerStart(ctx, e)
		}
		t.guard("SessionStart", func() { t.sessionStart(e) })
		return out, err
	}
	s.ObserveTool = func(ctx context.Context, e hookio.Event) error {
		var err error
		if innerTool != nil {
			err = innerTool(ctx, e)
		}
		t.guard("ObserveTool", func() { t.observeTool(ctx, e) })
		return err
	}
	s.ObserveStop = func(ctx context.Context, e hookio.Event, subagent bool) error {
		var err error
		if innerStop != nil {
			err = innerStop(ctx, e, subagent)
		}
		t.guard("ObserveStop", func() { t.observeStop(ctx, e, subagent) })
		return err
	}
	s.ObservePrompt = func(ctx context.Context, e hookio.Event) (hookio.Output, error) {
		var out hookio.Output
		var err error
		if innerPrompt != nil {
			out, err = innerPrompt(ctx, e)
		}
		t.guard("ObservePrompt", func() { t.observePrompt(ctx, e) })
		return out, err
	}
	s.SessionEnd = func(ctx context.Context, e hookio.Event) error {
		var err error
		if innerEnd != nil {
			err = innerEnd(ctx, e)
		}
		t.guard("SessionEnd", func() { t.sessionEnd(ctx) })
		return err
	}
	if innerPreCompact := s.PreCompact; innerPreCompact != nil {
		s.PreCompact = func(ctx context.Context, e hookio.Event) (hookio.Output, error) {
			t.guard("PreCompact", func() { t.preCompact(ctx, e) })
			return innerPreCompact(ctx, e)
		}
	}
}

// guard runs one seam's tap work, recovering a panic at the wrapper boundary: counted every
// time, logged Loud once per session, and never propagated.
func (t *schedTap) guard(seam string, fn func()) {
	defer func() {
		if rec := recover(); rec != nil {
			t.count(counterTapPanic)
			t.r.mu.Lock()
			first := !t.r.tapPanicLogged
			t.r.tapPanicLogged = true
			t.r.mu.Unlock()
			if first {
				t.log.Loud(msgTapPanic, "seam", seam, "panic", fmt.Sprint(rec))
			}
		}
	}()
	fn()
}

// sessionStart binds the event's session (loading its state files, seeding turn 0 as a round
// boundary) and records the activity. Warm path; no budget concern.
func (t *schedTap) sessionStart(e hookio.Event) {
	t.r.BindSession(e.SessionID, &e)
	t.r.NotifyActivity(t.r.nowMS())
}

// observeTool is the B-C path (worker pool, 50 ms): signals, the record SP-08 just wrote, one
// FeaturesFrom + Observe, the timestamp work, the token fold, and a segment close on a
// task-boundary signal. A missing record (core.ErrNotFound) is counted and skips the BOCD update;
// the timestamp work still happens, anchored on the clock instead of the record.
//
// Delivery is at least once, and this runs inside the handler, before the delivery's commit: a
// commit that fails replays the same delivery through here (schedRuntime.applied). So a delivery
// whose record the store holds is applied once, by its observation identity, in one critical
// section with that identity (applyToolUse), and a replay of it is counted and changes nothing,
// except that it makes the segment close the first run owed and did not make (closeOwed). The
// no-record path claims nothing: it folds nothing, and a replay that finds the record the first run
// could not publish must still apply it.
func (t *schedTap) observeTool(ctx context.Context, e hookio.Event) {
	t.r.bindOnFirstHook(e.SessionID)
	sig := observer.ExtractSignals(e)
	obs := observer.ObservationFrom(ctx)
	now := t.r.nowMS()
	rec, err := t.r.st.ToolUse(ctx, e.ToolUseID)
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			t.count(counterTapNoRecord)
		} else {
			t.log.Debug("scheduler tap: tool-use record unavailable", "tool_use_id", string(e.ToolUseID), "err", err.Error())
		}
		t.r.NotifyActivity(now)
		t.r.NoteRequestStart(now)
		t.r.NoteEffort(e)
		return
	}
	owed, applied := t.r.applyToolUse(ctx, e, obs, sig, rec)
	if applied {
		t.countBoundary(sig)
	} else {
		t.count(counterTapRedelivery)
	}
	t.closeOwed(ctx, e.SessionID, obs, owed)
}

// observeStop is ObserveTool minus the record lookup and the token fold (a Stop carries neither),
// plus the round boundary — §2.6's "new assistant message.id", and Stop is the one hook that
// fires once per assistant turn. It never anchors the request start: Stop fires after
// generation. A subagent's Stop is activity but not a main-agent round or observation. A replay of
// a Stop already applied changes nothing but the close it owes, as a tool use's does.
func (t *schedTap) observeStop(ctx context.Context, e hookio.Event, subagent bool) {
	t.r.bindOnFirstHook(e.SessionID)
	obs := observer.ObservationFrom(ctx)
	now := t.r.nowMS()
	var sig observer.Signals
	if !subagent {
		sig = observer.ExtractSignals(e)
	}
	owed, applied := t.r.applyStop(ctx, e, obs, subagent, sig, now)
	if applied {
		t.countBoundary(sig)
	} else {
		t.count(counterTapRedelivery)
	}
	t.closeOwed(ctx, e.SessionID, obs, owed)
}

// observePrompt runs twice per prompt: synchronously inside SP-05's 250 ms reply deadline, and on
// the ingest worker that captures the prompt. Both record activity and the request-start anchor
// only — no store I/O, no BOCD update. The worker's call also binds a session on its first hook
// (bindOnFirstHook), which reads the session's state files; the reply path never does. The worker's
// call is the prompt's delivery, so a replay of one already applied is left alone rather than
// claiming the prompt started at the instant of the replay. The reply path is not a delivery: it
// claims no identity.
func (t *schedTap) observePrompt(ctx context.Context, e hookio.Event) {
	if observer.PromptReplyOnly(ctx) {
		now := t.r.nowMS()
		t.r.NotifyActivity(now)
		t.r.NoteRequestStart(now)
		return
	}
	t.r.bindOnFirstHook(e.SessionID)
	if !t.r.applyPrompt(e.SessionID, observer.ObservationFrom(ctx), t.r.nowMS()) {
		t.count(counterTapRedelivery)
	}
}

// sessionEnd persists, then closes the runtime (flush op, 20 s hook timeout).
func (t *schedTap) sessionEnd(ctx context.Context) {
	if err := t.r.Persist(ctx); err != nil {
		t.log.Warn("scheduler tap: persist at session end failed", "err", err.Error())
	}
	if err := CloseSchedulerRuntime(t.r); err != nil {
		t.log.Warn("scheduler tap: close at session end failed", "err", err.Error())
	}
}

// preCompact closes the compacting session's open segment at the highest turn observed, with cause
// "compact", so the checkpoint sealed next encodes the span the host is about to summarize. A
// failure is logged at Warn and never stops the seal: the checkpoint is still written, as before,
// only without that span.
func (t *schedTap) preCompact(ctx context.Context, e hookio.Event) {
	if err := t.r.CloseSegmentForCompaction(ctx, e.SessionID); err != nil {
		t.log.Warn("scheduler tap: segment close at compaction failed; the checkpoint will not encode the open span",
			"session", string(e.SessionID), "err", err.Error())
	}
}

// countBoundary counts the first boundary signal set (todo, test, commit — the non-changepoint
// members of §8.5's safe points plus G1.5's git commit) of a delivery the tap has just applied. The
// close the signal calls for is the delivery's owed close (owedFor), made by closeOwed.
func (t *schedTap) countBoundary(sig observer.Signals) {
	cause := boundaryCause(sig)
	if cause == "" {
		return
	}
	t.count(counterTapBoundary)
	t.count(counterTapBoundaryPrefix + cause)
}

// closeOwed makes the segment close a delivery owes, if any: on the run that applied it, and again
// on each replay until one run makes it (schedRuntime.closeOwed). A failure is logged at Debug, as
// the boundary close always was; a changepoint's own first attempt has already logged at Warn.
func (t *schedTap) closeOwed(ctx context.Context, sess core.SessionID, obs core.ObservationID, c *owedClose) {
	if c == nil {
		return
	}
	if err := t.r.closeOwed(ctx, sess, obs, *c); err != nil {
		t.log.Debug("scheduler tap: segment close failed", "cause", c.cause, "turn", int(c.at), "err", err.Error())
	}
}

// owedFor is the segment close a delivery applied at turn at owes: the changepoint its observation
// declared when that close failed (cpErr), else the boundary its signals name, else none. carry is
// what the delivery folded into the open segment after observing, which a changepoint close leaves
// to the successor.
func owedFor(cpErr error, at core.TurnIndex, f scheduler.Features, sig observer.Signals, carry core.Tokens) *owedClose {
	if cpErr != nil {
		return &owedClose{at: at, f: f, cause: causeChangepoint, carry: carry}
	}
	if cause := boundaryCause(sig); cause != "" {
		return &owedClose{at: at, f: f, cause: cause}
	}
	return nil
}

// boundaryCause maps signals to a close cause; first true wins.
func boundaryCause(sig observer.Signals) string {
	switch {
	case sig.TodoCompleted:
		return causeTodo
	case sig.TestPassed:
		return causeTest
	case sig.GitCommit:
		return causeCommit
	default:
		return ""
	}
}

// count bumps a counter when a registry is wired.
func (t *schedTap) count(name string) {
	if t.metrics != nil {
		t.metrics.Counter(name).Add(1)
	}
}

// applyToolUse applies one delivered tool use whose record the store holds, unless obs names the
// session's last applied delivery (claimDeliveryLocked), in which case it changes nothing, reports
// false, and returns the close that delivery still owes. Applying stages the record's ArgsPreview
// for the lexical channel, derives the feature vector and folds it into the detector at the
// record's turn, records the activity and the request-start anchor at the record's instant, notes
// the effort level, and folds the record's tokens into the open segment; it returns the segment
// close the delivery owes (owedFor). All of it, the claim included, happens under one hold of the
// lock: FeatureHistory and the assembler are not goroutine-safe, and a Persist snapshotting between
// the claim and the fold would write an identity whose tokens the account does not hold.
func (r *schedRuntime) applyToolUse(ctx context.Context, e hookio.Event, obs core.ObservationID,
	sig observer.Signals, rec store.ToolUseRecord,
) (*owedClose, bool) {
	level := effortLevel(e, r.getenv)
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.claimDeliveryLocked(e.SessionID, obs) {
		return r.applied[e.SessionID].owed, false
	}
	observeText(r.hist, rec.ArgsPreview)
	f := FeaturesFrom(r.hist, sig, rec.Tool, rec.TS)
	_, cpErr := r.observeLocked(ctx, f, rec.Turn)
	r.notifyActivityLocked(rec.TS)
	r.noteRequestStartLocked(rec.TS)
	r.noteEffortLocked(level)
	r.addOpenSegmentTokensLocked(rec.Tokens)
	return r.oweLocked(e.SessionID, obs, owedFor(cpErr, rec.Turn, f, sig, rec.Tokens)), true
}

// applyStop applies one delivered Stop at ts, unless obs names the session's last applied delivery,
// in which case it returns the close that delivery still owes. Applying records the activity and
// the effort level, and for a main-agent Stop derives its feature vector (no tool, the clock's
// timestamp), folds it in at the highest turn seen, records that turn as an API-round boundary and
// returns the segment close the Stop owes (owedFor; a Stop folds no tokens, so it carries none). As
// in applyToolUse, the claim and the state it guards change under one hold of the lock.
func (r *schedRuntime) applyStop(ctx context.Context, e hookio.Event, obs core.ObservationID, subagent bool,
	sig observer.Signals, ts core.UnixMilli,
) (*owedClose, bool) {
	level := effortLevel(e, r.getenv)
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.claimDeliveryLocked(e.SessionID, obs) {
		return r.applied[e.SessionID].owed, false
	}
	r.notifyActivityLocked(ts)
	r.noteEffortLocked(level)
	if subagent {
		return nil, true
	}
	f := FeaturesFrom(r.hist, sig, "", ts)
	turn := r.maxTurn
	_, cpErr := r.observeLocked(ctx, f, turn)
	r.noteAPIRoundLocked(turn)
	return r.oweLocked(e.SessionID, obs, owedFor(cpErr, turn, f, sig, 0)), true
}

// applyPrompt applies one delivered prompt capture at ts (the activity and the request-start
// anchor) unless obs names the session's last applied delivery, and reports whether it did.
func (r *schedRuntime) applyPrompt(sess core.SessionID, obs core.ObservationID, ts core.UnixMilli) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.claimDeliveryLocked(sess, obs) {
		return false
	}
	r.notifyActivityLocked(ts)
	r.noteRequestStartLocked(ts)
	return true
}
