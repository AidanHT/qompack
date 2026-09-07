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
// this tap nothing would ever call Observe, NotifyActivity or Persist. It decorates exactly the
// five L0 seams through SP-05's own late-binding hook, so no SP-05 or SP-08 file is edited.

// The tap's instruments and the causes it hands CloseSegmentOn.
const (
	counterTapNoRecord       = "sched.tap.no_record"
	counterTapPanic          = "sched.tap.panic"
	counterTapBoundary       = "sched.tap.boundary"
	counterTapBoundaryPrefix = "sched.tap.boundary."

	causeChangepoint = "changepoint"
	causeTodo        = "todo"
	causeTest        = "test"
	causeCommit      = "commit"

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
// hook's result. rt == nil, or a Runtime that is not the daemon's, leaves s untouched. The four
// seams SP-12 does not decorate — PreCompact, Rehydrate, MCPInitialized, StatusExtra — and
// Services.Mode are never read or replaced.
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
		t.guard("ObservePrompt", func() { t.observePrompt() })
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
func (t *schedTap) observeTool(ctx context.Context, e hookio.Event) {
	sig := observer.ExtractSignals(e)
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
	f := t.r.observeToolUse(ctx, sig, rec)
	t.r.NotifyActivity(rec.TS)
	t.r.NoteRequestStart(rec.TS)
	t.r.NoteEffort(e)
	t.r.AddOpenSegmentTokens(rec.Tokens)
	t.closeOnBoundary(ctx, rec.Turn, f, sig)
}

// observeStop is ObserveTool minus the record lookup and the token fold (a Stop carries neither),
// plus the round boundary — §2.6's "new assistant message.id", and Stop is the one hook that
// fires once per assistant turn. It never anchors the request start: Stop fires after
// generation. A subagent's Stop is activity but not a main-agent round or observation.
func (t *schedTap) observeStop(ctx context.Context, e hookio.Event, subagent bool) {
	now := t.r.nowMS()
	t.r.NotifyActivity(now)
	t.r.NoteEffort(e)
	if subagent {
		return
	}
	sig := observer.ExtractSignals(e)
	f, turn := t.r.observeStop(ctx, sig, now)
	t.r.NoteAPIRound(turn)
	t.closeOnBoundary(ctx, turn, f, sig)
}

// observePrompt runs synchronously inside SP-05's 250 ms reply deadline: activity and the
// request-start anchor only — no store I/O, no BOCD update.
func (t *schedTap) observePrompt() {
	now := t.r.nowMS()
	t.r.NotifyActivity(now)
	t.r.NoteRequestStart(now)
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

// closeOnBoundary closes the segment on the first boundary signal set (todo, test, commit — the
// non-changepoint members of §8.5's safe points plus G1.5's git commit), counting which one.
func (t *schedTap) closeOnBoundary(ctx context.Context, at core.TurnIndex, f scheduler.Features, sig observer.Signals) {
	cause := boundaryCause(sig)
	if cause == "" {
		return
	}
	t.count(counterTapBoundary)
	t.count(counterTapBoundaryPrefix + cause)
	if err := t.r.CloseSegmentOn(ctx, at, f, cause); err != nil {
		t.log.Debug("scheduler tap: segment close on boundary failed", "cause", cause, "turn", int(at), "err", err.Error())
	}
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

// observeToolUse stages the record's ArgsPreview for the lexical channel, derives the feature
// vector and folds it into the detector at the record's turn — all under the lock, because
// FeatureHistory and the assembler are not goroutine-safe.
func (r *schedRuntime) observeToolUse(ctx context.Context, sig observer.Signals, rec store.ToolUseRecord) scheduler.Features {
	r.mu.Lock()
	defer r.mu.Unlock()
	observeText(r.hist, rec.ArgsPreview)
	f := FeaturesFrom(r.hist, sig, rec.Tool, rec.TS)
	r.observeLocked(ctx, f, rec.Turn)
	return f
}

// observeStop derives a Stop's feature vector (no tool, the clock's timestamp) and folds it in
// at the highest turn seen, returning both.
func (r *schedRuntime) observeStop(ctx context.Context, sig observer.Signals, ts core.UnixMilli) (scheduler.Features, core.TurnIndex) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f := FeaturesFrom(r.hist, sig, "", ts)
	r.observeLocked(ctx, f, r.maxTurn)
	return f, r.maxTurn
}
