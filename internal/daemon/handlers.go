package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/pluginmanifest"
)

// promptReplyDeadline bounds observe.prompt's synchronous ObservePrompt call — well inside the
// manifest's UserPromptSubmit timeout. On timeout the daemon replies with an empty Output; a
// prompt is never blocked on the daemon.
const promptReplyDeadline = 250 * time.Millisecond

// sentinelScanTailBytes bounds how much of the transcript's tail the off-reply-path sentinel scan
// reads (task-5-spec.md handlers.go).
const sentinelScanTailBytes = 256 << 10

// hookEventNameSessionStart is the exact SessionStart HSO.HookEventName spelling
// hookio.SessionStartOutput uses internally (unexported there); respelled here because the
// session.start route must set it on an Output a Services seam already partially built, which
// hookio's own constructor would overwrite rather than augment.
const hookEventNameSessionStart = "SessionStart"

// loudTailCount is §5.17's "the last five Loud messages", the count the status route and the
// metrics idle task both request from loudTail.
const loudTailCount = 5

// The daemon's own obs.Counter names, beyond the ones already declared in ingest.go/drain.go.
const (
	// counterHandlerPanic reuses ipc's own "ipc_handler_panic" spelling deliberately (ruling: the
	// ipc server's own recovery around this same composed Handler is belt-and-braces on top of
	// this one — both increment the identical metric name rather than two names for one concept).
	counterHandlerPanic = "ipc_handler_panic"

	counterUnhandledObserveTool = "l0_unhandled_observe_tool"
	counterUnhandledObserveStop = "l0_unhandled_observe_stop"

	// counterL0AcceptError counts an observe.prompt request whose WAL append could not be made
	// durable (an EncodeRequest or ingest.Accept failure) — the one hot-path event with no NAK
	// fallback to fall back on, so this is its only observability (fix round 2, FR-2).
	counterL0AcceptError = "l0_accept_error"

	counterHotpathDegraded = "hotpath_degraded"

	// counterHotpathSampleInvalid counts a hot-path sample rejected by validHotPathTS (an absent,
	// zero, or implausible req.TS) before it ever reaches a histogram or the breach detector.
	counterHotpathSampleInvalid = "hotpath_sample_invalid"

	counterContractFailPrefix = "contract_fail_"
	counterContractModeChange = "contract_mode_change"
)

// histHookControlledObserved is the daemon-owned histogram name for the strict lower bound on
// B-A: recvTS - req.TS, before hotPathTailAllowance is added.
const histHookControlledObserved = "hook_controlled_observed"

// StatusSnapshot is the status route's Data payload (task-5-spec.md handlers.go). SP-05 produces
// it; SP-14 renders it.
type StatusSnapshot struct {
	Mode       string                      `json:"mode"`
	Contract   []contract.Result           `json:"contract"`
	Hot        string                      `json:"hot"`
	Sessions   []SessionState              `json:"sessions"`
	Latency    map[string]obs.HistSnapshot `json:"latency"`
	Budgets    []obs.BudgetBreach          `json:"budgets"`
	Counters   map[string]int64            `json:"counters"`
	SpoolFiles int                         `json:"spool_files"`
	LoudTail   []string                    `json:"loud_tail"`
	Extra      json.RawMessage             `json:"extra,omitempty"`
}

// hotModeString renders ipc.HotPathMode as the wire-stable "sync"/"spool" spelling — ipc ships no
// String method on the type (its zero value, HotSync, is meant to be read via ==, not printed),
// so /qompack:status's JSON needs its own rendering here.
func hotModeString(h ipc.HotPathMode) string {
	if h == ipc.HotSpool {
		return "spool"
	}
	return "sync"
}

// resolveEvent returns req.Event, defensively defaulting its SessionID from req.Session, or a
// bare Event carrying only req.Session when req.Event is nil (a malformed or synthetic request —
// every real client always sets Event). It never returns nil, so every route can dereference
// freely.
func resolveEvent(req ipc.Request) *hookio.Event {
	if req.Event != nil {
		ev := *req.Event
		if ev.SessionID == "" {
			ev.SessionID = req.Session
		}
		return &ev
	}
	return &hookio.Event{SessionID: req.Session}
}

// decodeSubagent reads observe.stop's {"subagent":true} marker out of req.Raw. A missing or
// unparseable Raw reports false — the ordinary, non-subagent Stop hook.
func decodeSubagent(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var payload struct {
		Subagent bool `json:"subagent"`
	}
	_ = json.Unmarshal(raw, &payload)
	return payload.Subagent
}

// dispatchOp is the single ipc.Handler the daemon hands to ipc.NewServer — every LIVE request
// from every connection passes through here. It injects the Services/Registry/Daemon context
// values every route reads, looks up the op in the resolved route table (unknown op -> a
// refusal, never a panic), records the hot-path latency estimate for the three ops that carry it,
// and forces a NAK for a fire-and-forget hot-path request once the daemon has degraded to spool
// submode (§12.2's NAK-with-hint: the request is already WAL'd by the route's own ingest.Accept
// call, so refusing it here costs freshness, never data — the client's own spool absorbs the
// duplicate, and Drain's seenSet collapses it back to one dispatch).
//
// It is NOT drainer.Dispatch's function — drainDispatch (daemon.go) is, and deliberately routes
// only session.start/checkpoint/status/mcp through this function; hot-path ops go straight to
// runIngested and flush/admin.* are special-cased or skipped entirely, because a drained flush or
// admin.drain line reaching this function's own routes would call back into Drain on the same
// goroutine that is already holding drainer.Drain's non-reentrant mutex (fix round 1, Critical
// C-1 — see drainDispatch's doc comment for the full rationale).
func (d *daemon) dispatchOp(ctx context.Context, req ipc.Request) ipc.Response {
	recvTS := core.NowMilli(d.clk)
	ctx = withServices(ctx, d.svc)
	ctx = withRegistry(ctx, d.registry)
	ctx = withDaemon(ctx, d)

	var resp ipc.Response
	if h, ok := d.routes[req.Op]; ok {
		resp = d.callHandler(ctx, h, req)
	} else {
		resp = ipc.Response{OK: false, Err: "unknown op: " + string(req.Op)}
	}

	if req.Op.HotPath() {
		d.recordHotPathSample(req, recvTS)
		if !req.Reply && d.registry.HotMode() == ipc.HotSpool {
			resp.OK = false
		}
	}

	resp.Mode = d.monitor.Mode()
	resp.Hot = d.registry.HotMode()
	return resp
}

// callHandler invokes h, recovering a panic into a refusal so one broken route can never bring
// down the daemon's accept loop. The ipc server's own dispatch() wraps this same composed
// Handler with an identical recovery, incrementing the identical counter name — belt and braces,
// deliberately not deduplicated away.
func (d *daemon) callHandler(ctx context.Context, h ipc.Handler, req ipc.Request) (resp ipc.Response) {
	defer func() {
		if r := recover(); r != nil {
			if d.m != nil {
				d.m.Counter(counterHandlerPanic).Add(1)
			}
			d.log.Loud("daemon: handler panicked — request refused", "op", string(req.Op), "recover", r)
			resp = ipc.Response{OK: false, Err: fmt.Sprintf("panic: %v", r)}
		}
	}()
	return h(ctx, req)
}

// recordHotPathSample computes hook.controlled.observed (a strict lower bound on B-A: recvTS -
// req.TS) and hook.controlled (that value plus hotPathTailAllowance), records both into the
// metrics registry, and hands the estimate to the breach-detector worker via a non-blocking send
// — a full channel drops the sample rather than blocking the caller, which is this package's
// standing rule for anything that would otherwise sit on the ACK path.
func (d *daemon) recordHotPathSample(req ipc.Request, recvTS core.UnixMilli) {
	if !validHotPathTS(req.TS, recvTS) {
		if d.m != nil {
			d.m.Counter(counterHotpathSampleInvalid).Add(1)
		}
		return
	}

	observed := time.Duration(int64(recvTS)-int64(req.TS)) * time.Millisecond
	estimated := observed + hotPathTailAllowance

	if d.m != nil {
		d.m.Hist(histHookControlledObserved).Observe(observed)
		d.m.Hist(histName(obs.BA)).Observe(estimated)
	}

	select {
	case d.hotSamples <- estimated:
	default:
	}
}

// hotPathSampleMaxAge bounds how stale a live request's TS may be before its observed latency is
// treated as garbage rather than a real B-A sample: a genuine hot-path request is milliseconds
// old by the time the daemon reads it, so any age beyond a generous multi-second ceiling can only
// be a missing/corrupt TS, never a real measurement worth degrading the hot path over (fix
// round 1, I-6).
const hotPathSampleMaxAge = 10 * time.Second //nomagic:allow sanity ceiling on an untrusted wire timestamp, not a budget

// hotPathSampleFutureSlop tolerates ordinary clock imprecision between a request's stamped TS and
// the daemon's own recvTS without rejecting the sample outright.
const hotPathSampleFutureSlop = 250 * time.Millisecond //nomagic:allow clock-skew tolerance, not a budget

// validHotPathTS reports whether ts is fit to feed the B-A histograms and the breach detector:
// present (ipc.DecodeRequest performs no validation of its own, so an absent or zero "t" would
// otherwise read as an ~55-year-old sample), not implausibly ahead of recvTS, and not implausibly
// stale.
func validHotPathTS(ts, recvTS core.UnixMilli) bool {
	if ts <= 0 {
		return false
	}
	if ts > recvTS+core.UnixMilli(hotPathSampleFutureSlop.Milliseconds()) {
		return false
	}
	if recvTS-ts > core.UnixMilli(hotPathSampleMaxAge.Milliseconds()) {
		return false
	}
	return true
}

// hotPathWorker drains d.hotSamples and feeds the breach detector — the "window closure runs on a
// worker goroutine, not on the ACK path" rule. It also refreshes the cached CheckBudgets result
// the status route serves, so /qompack:status is never more than one sample stale.
func (d *daemon) hotPathWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case sample := <-d.hotSamples:
			t, closed := d.breach.Observe(sample)
			if t != NoTransition {
				d.applyHotPathTransition(t)
			}
			if closed {
				d.updateBudgetsCache()
			}
		}
	}
}

// updateBudgetsCache calls obs.Registry.CheckBudgets and caches the result for the status route
// (task-5-spec.md: "Budgets = the breaches from the most recent CheckBudgets result (cache it on
// the daemon)").
func (d *daemon) updateBudgetsCache() {
	if d.m == nil {
		return
	}
	breaches := d.m.CheckBudgets(d.currentCfg())
	d.histMu.Lock()
	d.lastBudgets = breaches
	d.histMu.Unlock()
}

// applyHotPathTransition is §12.2's sync<->spool submode transition. Both directions log and
// count regardless of spoolOnBreach; only the actual mode flip (registry.SetHotMode + WriteState)
// is gated on it, so an operator who disabled the fallback still gets full visibility into every
// window that would otherwise have tripped it (TestSpoolOnBreachFalseDoesNotTransition).
func (d *daemon) applyHotPathTransition(t Transition) {
	cfg := d.currentCfg()
	// A config reload does not currently re-apply cfg.Runtime.HotPath.BudgetMs/BreachWindows to
	// the already-constructed breachDetector (M-5), so the log reports the detector's own actual
	// limit/need — what it just gated the transition on — rather than cfg's current (possibly
	// since-reloaded and therefore different) values, which would otherwise be actively
	// misleading.
	limit, need := d.breach.Config()
	switch t {
	case ToSpool:
		if d.m != nil {
			d.m.Counter(counterHotpathDegraded).Add(1)
		}
		d.log.Warn("daemon: hot path degraded to spool submode",
			"budget_ms", limit.Milliseconds(), "windows", need)
		d.log.Loud("daemon: hot path degraded to spool submode",
			"budget_ms", limit.Milliseconds(), "windows", need)
		if !cfg.Runtime.HotPath.SpoolOnBreach {
			return
		}
		d.registry.SetHotMode(ipc.HotSpool, "breach")
		_ = ipc.WriteState(d.root, d.currentState())
	case ToSync:
		d.log.Info("daemon: hot path reverted to sync submode")
		d.log.Loud("daemon: hot path reverted to sync submode")
		d.registry.SetHotMode(ipc.HotSync, "")
		_ = ipc.WriteState(d.root, d.currentState())
	case NoTransition:
	}
}

// handleObserveTool is the default observe.tool route: registry.Touch, then — unless the mode is
// ModeOff — ingest.Accept, so the WAL holds it before anything else. The worker pool (runIngested)
// calls svc.ObserveTool asynchronously.
func (d *daemon) handleObserveTool(ctx context.Context, req ipc.Request) ipc.Response {
	return d.acceptHotPathEvent(req)
}

// handleObserveStop is observe.stop's default route: identical shape to observe.tool. The
// subagent flag travels in req.Raw and is decoded by runIngested, not here.
func (d *daemon) handleObserveStop(ctx context.Context, req ipc.Request) ipc.Response {
	return d.acceptHotPathEvent(req)
}

// acceptHotPathEvent is observe.tool's and observe.stop's shared body: registry.Touch, then
// ingest.Accept gated on Mode.MayRecord() (ModeOff -> skipped entirely: ACK returned, nothing
// written, per the mode-enforcement table's row 1).
func (d *daemon) acceptHotPathEvent(req ipc.Request) ipc.Response {
	ev := resolveEvent(req)
	now := core.NowMilli(d.clk)
	// Deliberately unconditional: the mode-enforcement table groups registry.Touch with
	// ingest.Accept as "skipped" under ModeOff, but liveness tracking (LastActivity/Live, which
	// the idle-exit timer reads) is cheap, in-memory, and arguably worth keeping even while
	// ModeOff suppresses recording — a session that is still sending traffic should not look idle
	// to Run's own idle-exit tick just because the operator turned recording off (M-2).
	d.registry.Touch(ev.SessionID, now)

	if !d.monitor.Mode().MayRecord() {
		return ipc.Response{OK: true}
	}

	line, err := ipc.EncodeRequest(req)
	if err != nil {
		return ipc.Response{OK: false, Err: err.Error()}
	}
	if err := d.ing.Accept(req, line); err != nil {
		return ipc.Response{OK: false, Err: err.Error()}
	}
	return ipc.Response{OK: true}
}

// handleObservePrompt is the default observe.prompt route: registry.Touch, ingest.Accept (WAL
// first), then — synchronously and inside promptReplyDeadline — svc.ObservePrompt when non-nil
// and the mode MayAct(). The result becomes Response.Output; a nil seam, a suppressed mode, an
// error or a timeout all fall back to hookio.Empty(). The sentinel scan runs later, off this
// reply path, in runIngested via the same ingest.Accept job.
func (d *daemon) handleObservePrompt(ctx context.Context, req ipc.Request) ipc.Response {
	ev := resolveEvent(req)
	now := core.NowMilli(d.clk)
	d.registry.Touch(ev.SessionID, now)

	mode := d.monitor.Mode()
	if !mode.MayRecord() {
		empty := hookio.Empty()
		return ipc.Response{OK: true, Output: &empty}
	}

	// observe.tool/stop's own failure paths return OK:false (a NAK the client answers by
	// spooling), so the event stays durable either way. observe.prompt has no such fallback here
	// — it always ACKs — so a silently-swallowed EncodeRequest/Accept error was the one hot-path
	// event that could vanish with zero observability (fix round 2, FR-2): §12.1's "nothing fails
	// silently" applies to this WAL append exactly as it does to a contract degradation.
	line, err := ipc.EncodeRequest(req)
	if err != nil {
		d.log.Warn("daemon: observe.prompt: failed to encode request for the WAL", "err", err)
		if d.m != nil {
			d.m.Counter(counterL0AcceptError).Add(1)
		}
	} else if err := d.ing.Accept(req, line); err != nil {
		d.log.Warn("daemon: observe.prompt: WAL append failed", "err", err)
		if d.m != nil {
			d.m.Counter(counterL0AcceptError).Add(1)
		}
	}

	out := hookio.Empty()
	if d.svc.ObservePrompt != nil && mode.MayAct() {
		out = d.callObservePromptWithDeadline(ctx, ev)
	}
	return ipc.Response{OK: true, Output: &out}
}

// callObservePromptWithDeadline enforces promptReplyDeadline even against a callee that ignores
// its own context: it races the call (in a goroutine, since a badly-behaved seam might not
// respect cancellation) against the deadline and falls back to hookio.Empty() if the deadline
// wins. The goroutine, if the callee never returns, is abandoned rather than leaked-and-blocked-
// on — a prompt is never blocked on the daemon (task-5-spec.md handlers.go).
func (d *daemon) callObservePromptWithDeadline(ctx context.Context, ev *hookio.Event) hookio.Output {
	rctx, cancel := context.WithTimeout(ctx, promptReplyDeadline)
	defer cancel()

	type result struct {
		out hookio.Output
		err error
	}
	ch := make(chan result, 1)
	go func() {
		o, err := d.svc.ObservePrompt(rctx, *ev)
		ch <- result{o, err}
	}()

	select {
	case r := <-ch:
		if r.err == nil {
			return r.out
		}
	case <-rctx.Done():
	}
	return hookio.Empty()
}

// scanSentinelForPrompt is the §12.1 hook.additional_context_delivered probe's other half: a
// worker (never the reply path) scans the transcript tail for the sentinel SessionStart minted,
// and records what it found. It is a no-op once the sentinel has already been observed, or if
// none was ever minted this session (an act.-suppressed SessionStart, or a session that predates
// this mechanism).
func (d *daemon) scanSentinelForPrompt(ev *hookio.Event) {
	d.historyMu.Lock()
	defer d.historyMu.Unlock()

	h := contract.LoadHistory(contract.HistoryPath(d.root))
	if h.Sentinel.Token == "" || h.Sentinel.Observed {
		return
	}
	found, _ := contract.ScanTranscriptTail(ev.TranscriptPath, h.Sentinel.Token, sentinelScanTailBytes)
	h.RecordSentinelScan(found)
	if err := contract.SaveHistory(contract.HistoryPath(d.root), h); err != nil {
		d.log.Warn("daemon: failed to save history after sentinel scan", "err", err)
	}
}

// handleSessionStart is the session.start warm path (task-5-spec.md handlers.go, normative
// ordering): the contract monitor runs before any other work (§5.21), the sentinel is minted only
// when the mode MayAct(), and the session_start.fires marker is deliberately never written here
// — see contract.WriteMarker's own doc comment for why.
func (d *daemon) handleSessionStart(ctx context.Context, req ipc.Request) ipc.Response {
	ev := resolveEvent(req)
	now := core.NowMilli(d.clk)

	d.maybeReloadConfig(ctx, d.cfgEnv)

	// A brand-new session id resets the breach detector's ring and streaks, mirroring
	// registry.Ensure's own reset of the hot-path submode to HotSync (task-3, registry.go): a
	// partially-filled window carrying breaching samples from a previous, unrelated session must
	// never close on this session's very first request (fix round 1, I-7/I-8). The existence
	// check happens BEFORE Ensure so "new" means what registry.Ensure itself means.
	_, existedBefore := d.registry.Get(ev.SessionID)
	d.registry.Ensure(ev, now)
	if !existedBefore {
		d.breach.Reset()
	}
	cfg := d.currentCfg()

	// Phase 1 (locked): RunAll and its own history mutations. RunAll is this package's own
	// bounded work (StandardAssertions' Checks) rather than a third-party seam, so serializing it
	// behind historyMu is safe and keeps its history reads/writes atomic with respect to any
	// concurrent route. Saved and unlocked immediately after — never held across the seam call
	// below (fix round 1, I-5).
	d.historyMu.Lock()
	h := contract.LoadHistory(contract.HistoryPath(d.root))
	env := contract.Env{
		ProjectRoot: d.root,
		Event:       *ev,
		Cfg:         cfg,
		Store:       d.svc.Store,
		Log:         d.log,
		Clock:       d.clk,
		History:     h,
	}
	results, mode := d.monitor.RunAll(ctx, env)
	justDegraded := d.recordContractObservability(results, mode)
	_ = ipc.WriteState(d.root, d.currentState())
	if err := contract.SaveHistory(contract.HistoryPath(d.root), h); err != nil {
		d.log.Warn("daemon: failed to save history after RunAll", "err", err)
	}
	d.historyMu.Unlock()

	// Phase 2 (unlocked): the wave-3 seam call. A seam's own budget (B-E is 2s) or, in principle,
	// a re-entrant call back into the daemon via DaemonFrom(ctx) must never be serialized behind
	// historyMu — every other history-touching route (checkpoint, the observe.prompt sentinel
	// scan) would otherwise queue behind one slow or misbehaving seam, and a re-entrant call would
	// self-deadlock on a non-reentrant mutex (fix round 1, I-5).
	var out hookio.Output
	if mode.MayAct() && d.svc.SessionStart != nil {
		if o, err := d.svc.SessionStart(ctx, *ev); err == nil {
			out = o
		} else {
			d.log.Warn("daemon: SessionStart failed", "err", err)
			out = hookio.Empty()
		}
	} else {
		out = hookio.Empty()
	}

	// Phase 3 (re-locked): re-load — a concurrent route may have saved its own changes while
	// phase 2 ran unlocked — then apply this route's remaining mutations on top of the fresh copy
	// and save.
	d.historyMu.Lock()
	defer d.historyMu.Unlock()
	h = contract.LoadHistory(contract.HistoryPath(d.root))

	if mode.MayAct() {
		s := contract.MintSentinel(ev.SessionID, now)
		// Only the token's own identifying fields are assigned — never the whole struct.
		// SentinelState.Observed is documented as "never resets to false" once the mechanism has
		// proven itself (contract/history.go), so a session.start that mints a fresh token must
		// leave it exactly as RecordSentinelScan last left it (fix round 1, I-2). Chances DOES
		// reset to 0 here, deliberately: it counts consecutive scans that missed THIS token, and a
		// freshly minted token has had zero chances to be found yet.
		h.Sentinel.Token = s.Token
		h.Sentinel.Session = ev.SessionID
		h.Sentinel.MintedAt = now
		h.Sentinel.Chances = 0
		if out.HookSpecificOutput == nil {
			out.HookSpecificOutput = &hookio.HSO{HookEventName: hookEventNameSessionStart}
		}
		sentinelText := contract.RenderSentinel(s)
		if out.HookSpecificOutput.AdditionalContext == "" {
			out.HookSpecificOutput.AdditionalContext = sentinelText
		} else {
			out.HookSpecificOutput.AdditionalContext += "\n" + sentinelText
		}
	}

	if justDegraded {
		out.SystemMessage = degradeBanner(results)
	}

	h.SessionCount++
	if err := contract.SaveHistory(contract.HistoryPath(d.root), h); err != nil {
		d.log.Warn("daemon: failed to save history", "err", err)
	}

	return ipc.Response{OK: true, Output: &out}
}

// recordContractObservability is ruling #26's obligation: a per-failing-assertion counter and a
// mode-transition counter, both underscore-idiom. It returns whether THIS RunAll is the one that
// transitioned into ModeDegradedPassive, which is what the session.start route's SystemMessage
// banner and nothing else consults.
func (d *daemon) recordContractObservability(results []contract.Result, mode contract.Mode) bool {
	if d.m != nil {
		for _, r := range results {
			if !r.OK {
				d.m.Counter(counterContractFailPrefix + toUnderscoreID(r.ID)).Add(1)
			}
		}
	}

	d.modeMu.Lock()
	prev := d.lastReportedMode
	changed := prev != mode
	justDegraded := prev != contract.ModeDegradedPassive && mode == contract.ModeDegradedPassive
	d.lastReportedMode = mode
	d.modeMu.Unlock()

	if changed && d.m != nil {
		d.m.Counter(counterContractModeChange).Add(1)
	}
	return justDegraded
}

// toUnderscoreID respells a dotted contract.ID as the repo's underscore counter idiom:
// "session_start.fires" -> "session_start_fires".
func toUnderscoreID(id contract.ID) string {
	out := make([]byte, len(id))
	for i := 0; i < len(id); i++ {
		c := id[i]
		if c == '.' {
			c = '_'
		}
		out[i] = c
	}
	return string(out)
}

// degradeBanner renders the §12.1 SystemMessage banner naming the first failing SevCritical
// assertion, falling back to a generic banner in the (should-be-impossible) case RunAll reported
// ModeDegradedPassive without any critical failure in this run's own results.
func degradeBanner(results []contract.Result) string {
	for _, r := range results {
		if !r.OK && r.Severity == contract.SevCritical {
			return fmt.Sprintf("Qompack: degraded to passive recording — %s expected %q, observed %q. See /qompack:status.",
				r.ID, r.Expected, r.Observed)
		}
	}
	return "Qompack: degraded to passive recording. See /qompack:status."
}

// handleCheckpoint is the checkpoint route: records the PreCompact observation into History,
// writes the terminal-hook marker, then — when svc.PreCompact is bound and the mode MayAct() —
// calls it timed into the B-E histogram, and captures any returned CustomInstructions.
func (d *daemon) handleCheckpoint(ctx context.Context, req ipc.Request) ipc.Response {
	ev := resolveEvent(req)
	now := core.NowMilli(d.clk)
	routeStart := d.clk.Now()

	// Phase 1 (locked): record the PreCompact observation — this package's own file I/O only,
	// no seam call — and save immediately, matching the spec's own ordering (history observation,
	// then the marker, then the seam call).
	d.historyMu.Lock()
	h := contract.LoadHistory(contract.HistoryPath(d.root))
	h.LastPrecompactTS = now
	h.LastPrecompactSession = ev.SessionID
	h.AwaitingCompactStart = true
	if h.PrecompactTimeoutMs == 0 {
		h.PrecompactTimeoutMs = precompactTimeoutMs()
	}
	mode := d.monitor.Mode()
	if err := contract.SaveHistory(contract.HistoryPath(d.root), h); err != nil {
		d.log.Warn("daemon: failed to save history", "err", err)
	}
	d.historyMu.Unlock()

	if err := contract.WriteMarker(d.root, ev.SessionID, now); err != nil {
		d.log.Warn("daemon: WriteMarker failed", "err", err)
	}

	// Phase 2 (unlocked): the wave-3 seam call, timed into B-E — its own budget is 2s, which must
	// never be spent holding historyMu (fix round 1, I-5): every other history-touching route
	// would queue behind it for the duration.
	out := hookio.Empty()
	if d.svc.PreCompact != nil && mode.MayAct() {
		var callErr error
		// d.m is dereferenced unguarded here and in handleStatus (M-12): New always seeds it
		// (obs.New(o.Clock) when o.Metrics is nil), so it is never nil for a *daemon reached
		// through New — unlike the ingest/drain layer's defensive "if i.m != nil" checks, which
		// exist because those types are also constructible directly by tests without going
		// through New.
		_ = obs.Timed(d.m.Hist(histName(obs.BE)), func() error {
			o, err := d.svc.PreCompact(ctx, *ev)
			callErr = err
			if err == nil {
				out = o
			}
			return err
		})
		if callErr != nil {
			d.log.Warn("daemon: PreCompact failed", "err", callErr)
		}
	}

	// Phase 3 (re-locked): re-load — a concurrent route may have saved its own changes while
	// phase 2 ran unlocked — then apply this route's remaining mutations and save.
	d.historyMu.Lock()
	defer d.historyMu.Unlock()
	h = contract.LoadHistory(contract.HistoryPath(d.root))

	if out.HookSpecificOutput != nil && out.HookSpecificOutput.CustomInstructions != "" {
		h.SetPrecompactInstr(out.HookSpecificOutput.CustomInstructions)
	}
	h.AddPrecompactWallSample(d.clk.Now().Sub(routeStart).Milliseconds())

	if err := contract.SaveHistory(contract.HistoryPath(d.root), h); err != nil {
		d.log.Warn("daemon: failed to save history", "err", err)
	}

	return ipc.Response{OK: true, Output: &out}
}

// precompactTimeoutMs reads the PreCompact hook's manifest timeout (internal/pluginmanifest),
// converted to milliseconds. The manifest exposes it as a per-second integer on the one
// HookEntry PreCompact's HookGroup carries; an unexpectedly empty manifest shape reports 0
// (unknown) rather than guessing.
func precompactTimeoutMs() int64 {
	m := pluginmanifest.Default(core.Version)
	groups, ok := m.Hooks.Hooks["PreCompact"]
	if !ok || len(groups) == 0 || len(groups[0].Hooks) == 0 {
		return 0
	}
	const msPerSecond = 1000
	return int64(groups[0].Hooks[0].Timeout) * msPerSecond
}

// handleFlush is the flush (SessionEnd) route: registry.End, ingest.CloseSession, svc.SessionEnd
// when bound and the mode MayRecord(), the terminal-hook marker, SketchSet.Save, then Drain.
func (d *daemon) handleFlush(ctx context.Context, req ipc.Request) ipc.Response {
	return d.flushRoute(ctx, req, true)
}

// flushRoute is handleFlush's body, with the trailing Drain call made optional. drainDispatch
// passes false: a flush line replayed BY Drain must never call back into Drain on the same
// goroutine — drainer.Drain holds a plain, non-reentrant sync.Mutex for the whole replay
// (drain.go's dr.mu), so a re-entrant call would deadlock the daemon permanently on the very
// first drained flush line, including the startup drain that runs before Serve ever accepts a
// connection (Critical C-1, fix round 1).
func (d *daemon) flushRoute(ctx context.Context, req ipc.Request, drain bool) ipc.Response {
	ev := resolveEvent(req)
	now := core.NowMilli(d.clk)

	d.registry.End(ev.SessionID, now)
	_ = d.ing.CloseSession(ev.SessionID)

	// MayRecord() is an extra mode-check site beyond the normative table's list — defensible
	// because SessionEnd is SP-08's L1 flush semantics (store.Flush and friends), which is
	// recording work, not acting work, so it belongs behind the same predicate row 1's
	// ingest.Accept uses, not behind MayAct() (M-3).
	if d.svc.SessionEnd != nil && d.monitor.Mode().MayRecord() {
		if err := d.svc.SessionEnd(ctx, *ev); err != nil {
			d.log.Warn("daemon: SessionEnd failed", "err", err)
		}
	}

	if err := contract.WriteMarker(d.root, ev.SessionID, now); err != nil {
		d.log.Warn("daemon: WriteMarker failed", "err", err)
	}

	if d.svc.Sketches != nil {
		d.svc.Sketches.Save(d.root, d.log)
	}

	if !drain {
		return ipc.Response{OK: true}
	}

	n, err := d.Drain(ctx)
	data, _ := json.Marshal(map[string]any{"drained": n})
	if err != nil {
		return ipc.Response{OK: false, Err: err.Error(), Data: data}
	}
	return ipc.Response{OK: true, Data: data}
}

// handleStatus assembles the StatusSnapshot payload. Nothing here mutates daemon state; every
// field is read from something already maintained elsewhere (the metrics registry, the monitor,
// the registry, the cached CheckBudgets result).
func (d *daemon) handleStatus(ctx context.Context, req ipc.Request) ipc.Response {
	snap := d.m.Snapshot()
	latency := make(map[string]obs.HistSnapshot, len(obs.Budgets())+1)
	for _, b := range obs.Budgets() {
		if hs, ok := snap.Hists[b.Hist]; ok {
			latency[b.Hist] = hs
		}
	}
	if hs, ok := snap.Hists[histHookControlledObserved]; ok {
		latency[histHookControlledObserved] = hs
	}

	d.histMu.Lock()
	budgets := append([]obs.BudgetBreach(nil), d.lastBudgets...)
	d.histMu.Unlock()

	spoolFiles, _ := ipc.SpoolFiles(paths.Of(d.root).Spool)

	var extra json.RawMessage
	if d.svc.StatusExtra != nil {
		if e, err := d.svc.StatusExtra(ctx); err == nil {
			extra = e
		}
	}

	snapshot := StatusSnapshot{
		Mode:       d.monitor.Mode().String(),
		Contract:   d.monitor.Report(),
		Hot:        hotModeString(d.registry.HotMode()),
		Sessions:   d.registry.Snapshot(),
		Latency:    latency,
		Budgets:    budgets,
		Counters:   snap.Counters,
		SpoolFiles: len(spoolFiles),
		LoudTail:   loudTail(d.root, loudTailCount),
		Extra:      extra,
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return ipc.Response{OK: false, Err: err.Error()}
	}
	return ipc.Response{OK: true, Data: data}
}

// handleMCP is the mcp route's wave-1 placeholder: without an MCPInitialized seam bound, there is
// no MCP server to talk to. SP-13 replaces this route entirely.
func (d *daemon) handleMCP(ctx context.Context, req ipc.Request) ipc.Response {
	if d.svc.MCPInitialized == nil {
		return ipc.Response{OK: false, Err: "mcp not built"}
	}
	return ipc.Response{OK: true}
}

// handleAdminPing answers OK:true unconditionally — Ruling #22/#1: the ACK path's liveness proof
// depends on Response.OK, and admin.ping's whole job is to be that proof over the reply channel.
func (d *daemon) handleAdminPing(ctx context.Context, req ipc.Request) ipc.Response {
	var uptime int64
	if start := d.startTS.Time(); !start.IsZero() {
		uptime = int64(d.clk.Now().Sub(start).Seconds())
	}
	data, _ := json.Marshal(map[string]any{
		"pid": os.Getpid(), "version": core.Version, "uptime_seconds": uptime,
	})
	return ipc.Response{OK: true, Data: data}
}

// handleAdminDrain runs Drain synchronously and reports how many entries it applied.
func (d *daemon) handleAdminDrain(ctx context.Context, req ipc.Request) ipc.Response {
	n, err := d.Drain(ctx)
	data, _ := json.Marshal(map[string]any{"drained": n})
	if err != nil {
		return ipc.Response{OK: false, Err: err.Error(), Data: data}
	}
	return ipc.Response{OK: true, Data: data}
}

// handleAdminReload forces a config reload regardless of config.json's mtime/size.
func (d *daemon) handleAdminReload(ctx context.Context, req ipc.Request) ipc.Response {
	changed, err := d.reloadConfig(ctx, d.cfgEnv, true)
	data, _ := json.Marshal(map[string]any{"changed": changed})
	if err != nil {
		return ipc.Response{OK: false, Err: err.Error(), Data: data}
	}
	return ipc.Response{OK: true, Data: data}
}

// handleAdminIdle runs one idle pass on demand, with a generous operator-facing budget rather
// than Run's own idle-tick budget.
func (d *daemon) handleAdminIdle(ctx context.Context, req ipc.Request) ipc.Response {
	ran, err := d.idle.RunOnce(ctx, adminIdleBudget)
	data, _ := json.Marshal(map[string]any{"ran": ran})
	if err != nil {
		return ipc.Response{OK: false, Err: err.Error(), Data: data}
	}
	return ipc.Response{OK: true, Data: data}
}

// handleAdminShutdown replies first, then stops the daemon asynchronously so the reply write
// itself is never racing the shutdown it announces.
func (d *daemon) handleAdminShutdown(ctx context.Context, req ipc.Request) ipc.Response {
	go func() { _ = d.Stop(context.Background()) }()
	return ipc.Response{OK: true}
}

// idleDrain, idleSaveSketches and idleWriteMetrics are SP-05's own three idle tasks, registered by
// New at priorities 10/20/30. None carries the act. prefix: all three are recording/maintenance
// work that must keep running in degraded-passive.
func (d *daemon) idleDrain(ctx context.Context) error {
	_, err := d.Drain(ctx)
	return err
}

func (d *daemon) idleSaveSketches(ctx context.Context) error {
	if d.svc.Sketches != nil {
		d.svc.Sketches.Save(d.root, d.log)
	}
	return nil
}

func (d *daemon) idleWriteMetrics(ctx context.Context) error {
	return writeMetricsSnapshot(d.root, d.m)
}
