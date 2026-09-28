package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/pluginmanifest"
	"github.com/qompack/qompack/internal/redact"
)

// promptReplyDeadline bounds how long observe.prompt's reply waits on the ObservePrompt call — well
// inside the manifest's UserPromptSubmit timeout. On timeout the daemon replies with an empty
// Output; a prompt is never blocked on the daemon. It bounds the WAIT only, never the verbatim
// capture the call performs (callObservePromptWithDeadline).
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

	// counterPromptReplyLate counts an observe.prompt reply that went out empty because
	// promptReplyDeadline EXPIRED while the verbatim capture it started was still running. The
	// capture itself is not lost — it finishes on its own context (startPromptRecording) — but
	// whatever it would have said back (a thrash warning) never reaches the host, and this is the
	// only record that it did not. A request cancelled for another reason (shutdown cancels the
	// serving context) and a capture that panicked are not overruns and are not counted here.
	counterPromptReplyLate = "l0_prompt_reply_late"

	// counterPromptCaptureRefused counts an observe.prompt whose verbatim capture was never started
	// because Stop had already begun joining the captures (startPromptRecording). That is a lost
	// G2.3 capture, so it is countable on its own rather than only a Warn in a log nobody reads.
	counterPromptCaptureRefused = "l0_prompt_capture_refused"

	// counterPromptReplayedUncaptured counts an observe.prompt delivery a drain replayed through
	// runIngested, whose prompt arm runs only the sentinel scan: the delivery is acknowledged and its
	// spool copy released with no verbatim capture made (SP08-D3, carried to V6). It is an UPPER
	// bound on lost G2.3 captures, not an exact count: a live line the full ring refused, and a line
	// whose live capture landed before a crash, replay the same way and cannot be told apart. It is
	// counted in drainDispatch only (the live worker shares runIngested) and only on an acknowledged
	// dispatch, since an unacknowledged one is redelivered and would be counted twice.
	counterPromptReplayedUncaptured = "l0_prompt_replayed_uncaptured"

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
//
// It also restores Event.Extra from req.Raw, which is a restoration rather than a new channel:
// §5.3 already makes Extra the home for a hook payload's unclaimed top-level keys, and it is the
// ONE field of hookio.Event the transport silently empties — it is tagged `json:"-"`, so
// hookio.ReadEvent fills it in the hook-client process and ipc.EncodeRequest then drops it. Every
// bound Services seam would otherwise read an empty map in production while the same code read a
// full one in any in-process test.
func resolveEvent(req ipc.Request) *hookio.Event {
	ev := &hookio.Event{SessionID: req.Session}
	if req.Event != nil {
		cp := *req.Event
		if cp.SessionID == "" {
			cp.SessionID = req.Session
		}
		ev = &cp
	}
	ev.Extra = restoredExtra(ev.Extra, req.Raw)
	return ev
}

// evidenceOnlyDelivery reports whether req is a classified capture with NO derived Event: the
// record the hook client mints when admission refused the payload for observation but still
// classified what arrived (a bounded permitted prefix, the fidelity that describes it, the capture
// error naming why, and the observed source size). hookio derived no Event from such a payload and
// none may be invented from it.
//
// resolveEvent answers an absent Event with a SYNTHETIC one — SessionID and nothing else — which
// is right for a direct IPC caller that sent only Raw, and wrong here: handing that to ObserveTool
// would record a tool use whose every field is a zero value the host never sent, which is the
// synthetic default the capture contract exists to prevent. So the capture is published (the
// durable object, stage 1, written before run is ever called) and the observation is skipped.
func evidenceOnlyDelivery(req ipc.Request) bool {
	if req.Event != nil || req.Capture == nil {
		return false
	}
	return req.Capture.Outcome != core.OutcomeOK && captureIsDecided(*req.Capture)
}

// restoredExtra merges raw's top-level keys into extra, returning extra unchanged when raw is not
// a JSON object (nil, a null, an array, a scalar, or malformed — all of which a corrupt spool line
// can produce, and none of which is an error worth failing a hook over).
//
// A key already present in extra WINS: the Event's own value is what an in-process caller set
// deliberately, while raw is a reconstruction. The merge always allocates a fresh map rather than
// writing into extra, because resolveEvent copies the Event by VALUE — the copy shares the
// caller's map header, so writing through it would mutate a request the caller still holds.
func restoredExtra(extra map[string]json.RawMessage, raw json.RawMessage) map[string]json.RawMessage {
	if len(raw) == 0 {
		return extra
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || len(m) == 0 {
		return extra
	}
	merged := make(map[string]json.RawMessage, len(extra)+len(m))
	for k, v := range m {
		merged[k] = v
	}
	for k, v := range extra {
		merged[k] = v
	}
	return merged
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
	// The daemon is provably serving — release Run's spool re-drain (daemon.go, redrainOnceServing).
	// A request drainDispatch replays from a spool proves nothing of the kind: Run's startup drain
	// replays before Run dispatches any request, and spending the signal there would run the
	// re-drain before the cold-start window it exists to cover has closed.
	if !spoolReplay(ctx) {
		d.noteServed()
	}
	ctx = withServices(ctx, d.svc)
	ctx = withRegistry(ctx, d.registry)
	ctx = withDaemon(ctx, d)

	// Privacy admission for the live connection, before any route can persist anything. Only the
	// observing ops carry a host payload; everything else (status, admin, mcp, checkpoint) has no
	// capture to decide about and is left exactly as it was.
	if req.Op.HotPath() {
		v := d.admitDelivery(req)
		switch {
		case v.Denied:
			if d.m != nil {
				d.m.Counter(counterAdmissionDenied).Add(1)
			}
			resp := admissionResponse(v)
			resp.Mode, resp.Hot = d.monitor.Mode(), d.registry.HotMode()
			return resp
		case v.Failed:
			if d.m != nil {
				d.m.Counter(counterAdmissionFailed).Add(1)
			}
			d.log.Warn("daemon: capture not admitted; nothing persisted", "op", string(req.Op), "reason", v.Reason)
			resp := admissionResponse(v)
			resp.Mode, resp.Hot = d.monitor.Mode(), d.registry.HotMode()
			return resp
		case v.Degraded:
			// Deliberately NOT a return. A degraded decision routes exactly like an admitted one:
			// the WAL line, then publishCapture's sidecar, so the record that says "a delivery
			// arrived and could not be admitted whole" is durable on the live path exactly as it
			// is on the spool path. Only the Event is withheld, and only when none was derived —
			// see evidenceOnlyDelivery.
			if d.m != nil {
				d.m.Counter(counterAdmissionDegraded).Add(1)
			}
			d.log.Info("daemon: capture admitted as evidence only", "op", string(req.Op), "reason", v.Reason)
		}
		// The request is NOT rewritten. The WAL line must stay byte-identical to what the client
		// sent, or a daemon-minted capture would make the WAL copy and the client's own spool copy
		// of one delivery hash differently and be replayed twice. The decision travels beside the
		// request instead, and a drained line is re-admitted deterministically by the same policy.
		ctx = withAdmittedCapture(ctx, v.Request.Capture)
	}

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
// req.TS) and hook.controlled (through handler completion plus an estimated client tail), records both into the
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
	handler := max(time.Duration(int64(core.NowMilli(d.clk))-int64(recvTS))*time.Millisecond, 0)
	estimated := observed + handler + hotPathTailAllowance

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

// persistHotMode writes state.bin carrying hot, and is called BEFORE registry.SetHotMode publishes
// the same value in memory. That order is the point of the function.
//
// registry.SetHotMode is the moment the transition becomes visible to anything outside the
// transition's own goroutine: dispatchOp reads registry.HotMode() to decide §12.2's NAK-with-hint,
// and Registry().HotMode() is what any other observer polls. A state.bin write ordered AFTER that
// flip leaves a window one whole paths.WriteAtomic wide — stage into .qompack/tmp, fsync, chmod,
// rename, measured on a Windows host at an 8.5 ms mean with nothing else touching the file and a
// 0.52 s worst case with concurrent readers on it — in which the registry says spool, the daemon
// NAKs, and the 32-byte record every newly constructed client reads still says sync. A client born
// in that window dials a daemon that has already stopped accepting, which is precisely the connect
// the spec's own rationale for this write ("so the next client process skips the connect entirely")
// says it exists to prevent. Persisting first closes it: the record can lead the registry, never
// trail it.
//
// The error is logged rather than discarded. daemon.Run and reloadConfig both log theirs, and this
// was the one WriteState call site that did not — yet it is the one whose failure makes §12.2's
// fallback invisible to every process except this one.
func (d *daemon) persistHotMode(hot ipc.HotPathMode) {
	if err := ipc.WriteState(d.root, d.stateWithHot(hot)); err != nil {
		d.log.Warn("daemon: failed to write state.bin for the hot-path transition",
			"hot", hotModeString(hot), "err", err)
	}
}

// applyHotPathTransition is §12.2's sync<->spool submode transition. Both directions log and
// count regardless of spoolOnBreach; only the actual mode flip (WriteState + registry.SetHotMode)
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
		d.persistHotMode(ipc.HotSpool)
		d.registry.SetHotMode(ipc.HotSpool, "breach")
	case ToSync:
		d.log.Info("daemon: hot path reverted to sync submode")
		d.log.Loud("daemon: hot path reverted to sync submode")
		d.persistHotMode(ipc.HotSync)
		d.registry.SetHotMode(ipc.HotSync, "")
	case NoTransition:
	}
}

// handleObserveTool is the default observe.tool route: registry.Touch, then — unless the mode is
// ModeOff — ingest.Accept, so the WAL holds it before anything else. The worker pool (runIngested)
// calls svc.ObserveTool asynchronously.
func (d *daemon) handleObserveTool(ctx context.Context, req ipc.Request) ipc.Response {
	return d.acceptHotPathEvent(ctx, req)
}

// handleObserveStop is observe.stop's default route: identical shape to observe.tool. The
// subagent flag travels in req.Raw and is decoded by runIngested, not here.
func (d *daemon) handleObserveStop(ctx context.Context, req ipc.Request) ipc.Response {
	return d.acceptHotPathEvent(ctx, req)
}

// acceptHotPathEvent is observe.tool's and observe.stop's shared body: registry.Touch, then
// ingest.Accept gated on Mode.MayRecord() (ModeOff -> skipped entirely: ACK returned, nothing
// written, per the mode-enforcement table's row 1).
func (d *daemon) acceptHotPathEvent(ctx context.Context, req ipc.Request) ipc.Response {
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
	if err := d.ing.Accept(withCapture(ctx, req), line); err != nil {
		return ipc.Response{OK: false, Err: err.Error()}
	}
	return ipc.Response{OK: true}
}

// handleObservePrompt is the default observe.prompt route: registry.Touch, ingest.Accept (WAL
// first), then — synchronously and inside promptReplyDeadline — svc.ObservePrompt when non-nil.
// The result becomes Response.Output; a nil seam, a suppressed mode, an error or a timeout all
// fall back to hookio.Empty(). The sentinel scan runs later, off this reply path, in runIngested
// via the same ingest.Accept job.
//
// The two mode gates are DIFFERENT gates, and collapsing them is a silent data-loss bug. §12.1
// says ModeDegradedPassive keeps "L0 and L1 running (observe, chunk, store, sketches, DAG,
// verbatim capture …)" and turns only ACTING off, but the ObservePrompt seam does both jobs in one
// call: G2.3's verbatim prompt capture is recording, and the hookio.Output it returns is acting.
// So MayRecord gates the WAL append and the CALL, exactly as it does on observe.tool/observe.stop,
// while MayAct gates only whether the returned Output reaches the reply. Gating the call itself on
// MayAct — which this route used to do — stopped the verbatim capture the moment the contract
// degraded, with no error, no counter and a reply indistinguishable from a healthy passive one.
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
	if err == nil {
		err = d.ing.Accept(withCapture(ctx, req), line)
	}
	if err != nil {
		d.log.Warn("daemon: observe.prompt: durable acceptance failed", "err", err)
		if d.m != nil {
			d.m.Counter(counterL0AcceptError).Add(1)
		}
		empty := hookio.Empty()
		return ipc.Response{OK: false, Err: err.Error(), Output: &empty}
	}

	out := hookio.Empty()
	if d.svc.ObservePrompt != nil {
		// The call is recording, so it runs under the MayRecord check above; only what it hands
		// back is acting, and under !MayAct the reply stays the empty output.
		produced := d.callObservePromptWithDeadline(ctx, ev)
		if mode.MayAct() {
			out = produced
		}
	}
	return ipc.Response{OK: true, Output: &out}
}

// callObservePromptWithDeadline enforces promptReplyDeadline on the REPLY, and on nothing else: it
// races the call (on a goroutine, since a badly-behaved seam might not respect cancellation)
// against the deadline and falls back to hookio.Empty() if the deadline wins — a prompt is never
// blocked on the daemon (task-5-spec.md handlers.go).
//
// The seam does two jobs in one call (see handleObservePrompt): G2.3's verbatim capture, which is
// recording, and the Output it returns, which is acting. Only the second is the hook's to wait for.
// The call used to run under a context DERIVED from the deadline, so a capture that overran it —
// the observer's session lock held by ingest workers draining a tool backlog, or a slow disk —
// reached store.PutBytes with a dead context and was soft-dropped into observer.err.prompt.put.
// Nothing records it later: the WAL line this route appended replays through runIngested, which
// for observe.prompt runs only the sentinel scan. So the capture now runs on a context of its own
// (startPromptRecording), governed by the daemon's lifetime rather than by this reply, and the
// deadline bounds only the wait below.
//
// A capture that finishes after the deadline has its Output discarded: the reply has already gone
// out empty, and a thrash warning held back for a later turn would describe a loop the agent may
// since have left. The miss is counted (counterPromptReplyLate), never silent — but only when the
// deadline is what ended the wait: a request cancelled from outside (Stop cancels the serving
// context) is not an overrun, and neither is a panicking seam, which answers the wait at once.
func (d *daemon) callObservePromptWithDeadline(ctx context.Context, ev *hookio.Event) hookio.Output {
	wait, cancel := context.WithTimeout(ctx, promptReplyDeadline)
	defer cancel()

	type result struct {
		out hookio.Output
		err error
	}
	ch := make(chan result, 1)
	e := *ev
	if !d.startPromptRecording(ctx, func(rec context.Context) {
		// Sent from a defer so that a seam which panics still answers the wait at once, with an
		// error, rather than leaving the reply to run out the deadline and be counted as late.
		// startPromptRecording's recover runs after this send; ch is buffered, so it never blocks.
		r := result{err: errPromptCapturePanicked}
		defer func() { ch <- r }()
		// SP08-D3 (Option A): the reply is the WARNING only. The verbatim capture is the
		// worker/replay's, under the leased observation identity, so a prompt no daemon captured
		// live is still captured and a later live prompt cannot take a turn 0 the replay owed. The
		// reply-only marker makes ObservePrompt drain the pending warning under the session lock and
		// record nothing.
		r.out, r.err = d.svc.ObservePrompt(observer.WithPromptReplyOnly(rec), e)
	}) {
		return hookio.Empty()
	}

	select {
	case r := <-ch:
		if r.err == nil {
			return r.out
		}
	case <-wait.Done():
		if d.m != nil && errors.Is(wait.Err(), context.DeadlineExceeded) {
			d.m.Counter(counterPromptReplyLate).Add(1)
		}
	}
	return hookio.Empty()
}

// errPromptCapturePanicked is the result a panicking ObservePrompt seam hands the reply wait. It
// never leaves this file: the reply is hookio.Empty(), as for any other seam error, and the panic
// itself is counted and Loud'd by startPromptRecording's recover.
var errPromptCapturePanicked = errors.New("daemon: observe.prompt capture panicked")

// startPromptRecording runs record on a goroutine of its own, under a context that keeps ctx's
// VALUES — the Services/Registry/Daemon values dispatchOp bound, so a call that finishes inside the
// deadline sees exactly what it always saw — and none of ctx's cancellation or deadline. The
// daemon's lifetime cancels it instead: promptCancel, which only Stop calls (stopPromptRecordings),
// and which Stop joins this goroutine behind.
//
// It reports false, and runs nothing, once Stop has begun that join. A WaitGroup may not grow
// after its Wait has started, and a capture started then would outlive the store the composition
// root closes as soon as Stop returns. The daemon is going away at that point; the refusal is
// logged and counted (counterPromptCaptureRefused) rather than silent.
//
// A panic inside record is recovered here, counted and Loud'd exactly as callHandler recovers one
// on the handler's own goroutine: this goroutine now outlives the request, so callHandler's
// recovery can no longer reach it, and one broken seam must never bring the daemon down.
func (d *daemon) startPromptRecording(ctx context.Context, record func(context.Context)) bool {
	d.promptMu.Lock()
	if d.promptClosed {
		d.promptMu.Unlock()
		if d.m != nil {
			d.m.Counter(counterPromptCaptureRefused).Add(1)
		}
		d.log.Warn("daemon: observe.prompt arrived during shutdown; verbatim capture not started")
		return false
	}
	d.promptWG.Add(1)
	d.promptMu.Unlock()

	rec, cancel := context.WithCancel(context.WithoutCancel(ctx))
	stopAfter := context.AfterFunc(d.promptCtx, cancel)
	go func() {
		defer d.promptWG.Done()
		defer cancel()
		defer stopAfter()
		defer func() {
			if r := recover(); r != nil {
				if d.m != nil {
					d.m.Counter(counterHandlerPanic).Add(1)
				}
				d.log.Loud("daemon: ObservePrompt panicked — verbatim capture lost", "recover", r)
			}
		}()
		record(rec)
	}()
	return true
}

// stopPromptRecordings is Stop's join for every capture startPromptRecording launched.
//
// It closes the gate first, so nothing can join the group behind the wait. The captures already in
// flight then get grace to finish on their own merits — Stop hands over the drain's own bounded
// context, so they run concurrently with the drain and add no shutdown window of their own. Only
// then is whatever remains cancelled, which a cooperating capture answers promptly by failing on
// its merits (store.PutBytes's own ctx check, counted in observer.err.prompt.put). A callee that
// ignores even that is abandoned with a Loud line after promptAbandonAfter (promptReplyDeadline in
// every daemon New builds) — the same abandonment the reply path has always accepted — rather than
// wedging the shutdown.
func (d *daemon) stopPromptRecordings(grace context.Context) {
	d.promptMu.Lock()
	d.promptClosed = true
	d.promptMu.Unlock()
	defer d.promptCancel()

	done := make(chan struct{})
	go func() {
		d.promptWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return
	case <-grace.Done():
	}

	d.promptCancel()
	t := time.NewTimer(d.promptAbandonAfter)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
		d.log.Loud("daemon: stop: a verbatim prompt capture ignored cancellation; abandoning it",
			"bound", d.promptAbandonAfter.String())
	}
}

// scanSentinelForPrompt is the §12.1 hook.additional_context_delivered probe's other half: a
// worker (never the reply path) scans the transcript tail for the sentinel SessionStart minted,
// and records what it found. It is a no-op once the sentinel has already been observed, or when
// none is current: none was ever minted (every start so far was act.-suppressed or replayed, or the
// project predates this mechanism), or the replay of the start whose answer lost it withdrew it
// (withdrawLostStartAnswer).
//
// A miss is counted only from a prompt that had a chance to find the sentinel (sentinelMissCounts):
// two misses that were never chances would degrade the project for a probe no prompt has really
// looked for. A find always counts, because a transcript that holds the sentinel is evidence of its
// delivery whichever prompt read it.
//
// promptTS is the prompt hook's own first-statement time (ipc.Request.TS), and nonce its delivery
// nonce (ipc.Request.Nonce): the same for every copy of one delivery, so a delivery the daemon handles
// more than once — a retry after a capture that failed, a redelivery after a restart — is one chance
// (contract.SessionHistory.RecordSentinelScanOf), not one per attempt.
func (d *daemon) scanSentinelForPrompt(ev *hookio.Event, promptTS core.UnixMilli, nonce string) {
	d.historyMu.Lock()
	defer d.historyMu.Unlock()

	h := contract.LoadHistory(contract.HistoryPath(d.root))
	if h.Sentinel.Token == "" || h.Sentinel.Observed {
		return
	}
	found, _ := contract.ScanTranscriptTail(ev.TranscriptPath, h.Sentinel.Token, sentinelScanTailBytes)
	if !found && !sentinelMissCounts(h.Sentinel, ev.SessionID, promptTS) {
		return
	}
	h.RecordSentinelScanOf(found, nonce)
	if err := contract.SaveHistory(contract.HistoryPath(d.root), h); err != nil {
		d.log.Warn("daemon: failed to save history after sentinel scan", "err", err)
	}
}

// sentinelMissCounts reports whether a prompt of sess, sent at promptTS, that did not find sentinel s
// in its transcript was a chance to find it — one of the two §12.1 allows before
// hook.additional_context_delivered fails. It was not:
//
//   - when sess is not the session s was minted for. Only that session's transcript was sent the
//     probe; another session's — a second window's, or one whose own start was replayed from a spool
//     and minted nothing, which leaves an earlier session's probe current — never had it.
//   - when the prompt was sent before s was minted: one replayed from a spool or deferred to a drain
//     after a later start. Its transcript could not have held a probe that did not exist yet.
//
// An unknown side of either comparison — a sentinel minted before Session was recorded, a prompt
// with no session id, a request from before TS was stamped — counts, as every miss always did.
func sentinelMissCounts(s contract.SentinelState, sess core.SessionID, promptTS core.UnixMilli) bool {
	if s.Session != "" && sess != "" && sess != s.Session {
		return false
	}
	return promptTS <= 0 || promptTS >= s.MintedAt
}

// The session.start route's phase histograms (C1.16). route is the whole handler; contract is
// phase 1 including the wait for historyMu; seam is phase 2 — the observer's SessionStart, or for
// source=compact the wait for the rehydration phase 1 started (session_start.compact_wait times
// that wait alone); finish is phase 3. A slow answer is attributable from metrics/latency.json
// alone, without a debugger on the host that saw it.
const (
	histSessionStartRoute    = "session_start.route"
	histSessionStartContract = "session_start.contract"
	histSessionStartSeam     = "session_start.seam"
	histSessionStartFinish   = "session_start.finish"
)

// observePhase records the wall time since start under name.
func (d *daemon) observePhase(name string, start time.Time) {
	if d.m != nil {
		d.m.Hist(name).Observe(time.Since(start))
	}
}

// handleSessionStart is the session.start warm path (task-5-spec.md handlers.go, normative
// ordering): the contract monitor runs before any other work (§5.21), the sentinel is minted only
// when the mode MayAct(), and the session_start.fires marker is deliberately never written here
// — see contract.WriteMarker's own doc comment for why.
//
// A request drainDispatch replays from a hook client's spool (spoolReplay) is one no answer from
// this daemon reached in time: the daemon was down or not yet listening, or its reply missed the
// client's deadline, and the hook has already answered the host without it. The replay does the
// start's durable bookkeeping — the registry, the contract run and its observations, the session
// count, the observer's SessionStart, a compact start's undelivered drop report — because the host
// did start the session. It puts nothing into an answer, because no answer reaches the host: it
// mints no §12.1 probe (one nobody received would run out its two chances and degrade the project,
// blaming the host for a reply Qompack lost), and it leaves the degrade banner to the next live start
// (recordContractObservability). And when the request's own live answer was the one that was lost,
// the replay withdraws what that answer carried (withdrawLostStartAnswer).
func (d *daemon) handleSessionStart(ctx context.Context, req ipc.Request) ipc.Response {
	ev := resolveEvent(req)
	now := core.NowMilli(d.clk)
	routeStart := time.Now()
	defer d.observePhase(histSessionStartRoute, routeStart)
	replayed := spoolReplay(ctx)

	d.maybeReloadConfig(ctx, d.cfgEnv)

	// A brand-new session id resets the breach detector's ring and streaks, mirroring
	// registry.Ensure's own reset of the hot-path submode to HotSync (task-3, registry.go): a
	// partially-filled window carrying breaching samples from a previous, unrelated session must
	// never close on this session's very first request (fix round 1, I-7/I-8). The existence
	// check happens BEFORE Ensure so "new" means what registry.Ensure itself means.
	_, existedBefore := d.registry.Get(ev.SessionID)
	d.registry.Ensure(ev, now)
	// When the host fired this start, which for a replay is long before now: the checkpoint route
	// asks it whether a PreCompact it replays was already followed by a start (handleCheckpoint).
	d.registry.NoteStart(ev.SessionID, hookTime(req, now))
	if !existedBefore {
		d.breach.Reset()
	}
	cfg := d.currentCfg()

	// Phase 1 (locked): RunAll and its own history mutations. RunAll is this package's own
	// bounded work (StandardAssertions' Checks) rather than a third-party seam, so serializing it
	// behind historyMu is safe and keeps its history reads/writes atomic with respect to any
	// concurrent route. Saved and unlocked immediately after — never held across the seam call
	// below (fix round 1, I-5).
	contractStart := time.Now()
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
	// A replayed start the host fired BEFORE the pending PreCompact is not the start that PreCompact
	// announced: its hook had already run when the PreCompact did, and only its replay comes after.
	// Resolving session_start.source_compact against it would fail the assertion at critical
	// severity — a startup is no compact — and degrade the project for Qompack's own replay order. So
	// its contract run does not see the obligation, which stays pending for the start that follows.
	heldBack := replayed && h.AwaitingCompactStart && hookTime(req, now) < h.LastPrecompactTS
	if heldBack {
		h.AwaitingCompactStart = false
	}
	results, mode := d.monitor.RunAll(ctx, env)
	if heldBack {
		h.AwaitingCompactStart = true
	}
	// A compact SessionStart's rehydration starts here, as soon as the contract run has said the
	// mode may act and before this phase's own durable writes, so the two overlap; the route
	// collects it where the seam call would be (session_start_compact.go, C1.16).
	var compact *compactAnswer
	if ev.Source == sessionSourceCompact && mode.MayAct() {
		compact = d.startCompactAnswer(ctx, *ev, routeStart)
	}
	justDegraded := d.recordContractObservability(results, mode, !replayed)
	_ = ipc.WriteState(d.root, d.currentState())
	if err := contract.SaveHistory(contract.HistoryPath(d.root), h); err != nil {
		d.log.Warn("daemon: failed to save history after RunAll", "err", err)
	}
	// The same nine results, read as per-capability evidence (SP-19 commit 3; observations.go).
	d.recordCapabilityObservations(results, ev.SessionID, now)
	d.historyMu.Unlock()
	d.observePhase(histSessionStartContract, contractStart)

	// Phase 2 (unlocked): the wave-3 seam call. A seam's own budget (B-E is 2s) or, in principle,
	// a re-entrant call back into the daemon via DaemonFrom(ctx) must never be serialized behind
	// historyMu — every other history-touching route (checkpoint, the observe.prompt sentinel
	// scan) would otherwise queue behind one slow or misbehaving seam, and a re-entrant call would
	// self-deadlock on a non-reentrant mutex (fix round 1, I-5).
	//
	// A compact SessionStart does not call the seam here: its rehydration and the observer's
	// bookkeeping were started in phase 1, and only the rehydration is waited for, within
	// compactAnswerBudget of the request's arrival (session_start_compact.go).
	seamStart := time.Now()
	out := hookio.Empty()
	switch {
	case compact != nil:
		out = d.awaitCompactAnswer(ctx, compact)
	case mode.MayAct() && d.svc.SessionStart != nil:
		if o, err := d.svc.SessionStart(ctx, *ev); err == nil {
			out = o
		} else {
			d.log.Warn("daemon: SessionStart failed", "err", err)
		}
	}
	d.observePhase(histSessionStartSeam, seamStart)

	// Phase 3 (re-locked): re-load — a concurrent route may have saved its own changes while
	// phase 2 ran unlocked — then apply this route's remaining mutations on top of the fresh copy
	// and save.
	finishStart := time.Now()
	defer d.observePhase(histSessionStartFinish, finishStart)
	d.historyMu.Lock()
	defer d.historyMu.Unlock()
	h = contract.LoadHistory(contract.HistoryPath(d.root))

	if replayed {
		// Nothing below reaches the host (see the doc comment), so nothing is minted or shown; what
		// this request's own live answer carried, if it was the one that was lost, is withdrawn.
		d.withdrawLostStartAnswer(h, req.Nonce)
		out = hookio.Empty()
	} else {
		answer := startAnswer{nonce: req.Nonce}
		if mode.MayAct() {
			s := contract.MintSentinel(ev.SessionID, now)
			// Only the token's own identifying fields are assigned — never the whole struct.
			// SentinelState.Observed is documented as "never resets to false" once the mechanism has
			// proven itself (contract/history.go), so a session.start that mints a fresh token must
			// leave it exactly as RecordSentinelScan last left it (fix round 1, I-2). Chances DOES
			// reset to 0 here, deliberately: it counts consecutive scans that missed THIS token, and a
			// freshly minted token has had zero chances to be found yet — and so does MissedBy, the
			// deliveries those chances were spent by.
			h.Sentinel.Token = s.Token
			h.Sentinel.Session = ev.SessionID
			h.Sentinel.MintedAt = now
			h.Sentinel.Chances = 0
			h.Sentinel.MissedBy = nil
			if out.HookSpecificOutput == nil {
				out.HookSpecificOutput = &hookio.HSO{HookEventName: hookEventNameSessionStart}
			}
			sentinelText := contract.RenderSentinel(s)
			if out.HookSpecificOutput.AdditionalContext == "" {
				out.HookSpecificOutput.AdditionalContext = sentinelText
			} else {
				out.HookSpecificOutput.AdditionalContext += "\n" + sentinelText
			}
			answer.token = s.Token
		}

		if justDegraded {
			out.SystemMessage = degradeBanner(results)
			answer.banner = true
		}
		d.lastStartAnswer = answer
	}

	h.SessionCount++
	if err := contract.SaveHistory(contract.HistoryPath(d.root), h); err != nil {
		d.log.Warn("daemon: failed to save history", "err", err)
	}

	return ipc.Response{OK: true, Output: &out}
}

// startAnswer is what one live session.start put into its answer that is true only once the host
// has that answer: the §12.1 probe token it minted, and whether it carried the degrade banner. The
// daemon keeps the last one (daemon.lastStartAnswer, under historyMu) with the request's delivery
// nonce, the identity the hook client gives a delivery and keeps across its spooled copy.
type startAnswer struct {
	nonce  string
	token  string
	banner bool
}

// withdrawLostStartAnswer is a replayed session.start's correction of its own live answer. The hook
// client spools a request only when no answer reached it in time, so a replay whose nonce is the
// last live start's proves that live answer was lost: the probe it minted was never delivered, and
// the banner it carried was never shown. The probe is withdrawn — while it is still the current one —
// so the session's prompts cannot run out its chances and degrade the project for a reply Qompack
// lost; Observed, which never resets, is left alone. The banner is owed again to the next live start.
// A replay of any other request, or one without a nonce, changes nothing: that answer may well have
// been delivered. The record is in memory only, so a daemon that restarts between the live answer
// and its replay cannot make this correction.
//
// h is the history phase 3 loaded, under historyMu, which also guards lastStartAnswer.
func (d *daemon) withdrawLostStartAnswer(h *contract.SessionHistory, nonce string) {
	lost := d.lastStartAnswer
	if nonce == "" || lost.nonce != nonce {
		return
	}
	d.lastStartAnswer = startAnswer{}
	if lost.token != "" && h.Sentinel.Token == lost.token {
		h.Sentinel.Token, h.Sentinel.Session, h.Sentinel.MintedAt, h.Sentinel.Chances = "", "", 0, 0
		h.Sentinel.MissedBy = nil
	}
	if lost.banner {
		d.modeMu.Lock()
		d.lastAnnouncedMode = contract.ModeFull
		d.modeMu.Unlock()
	}
}

// hookTime is when the host fired the hook req came from: the hook's own first-statement timestamp,
// or now when the request carries none.
func hookTime(req ipc.Request, now core.UnixMilli) core.UnixMilli {
	if req.TS > 0 {
		return req.TS
	}
	return now
}

// recordContractObservability is ruling #26's obligation: a per-failing-assertion counter and a
// mode-transition counter, both underscore-idiom. It returns whether THIS RunAll is the one that
// transitioned into ModeDegradedPassive, which is what the session.start route's SystemMessage
// banner and nothing else consults.
//
// announce is false for a start whose answer reaches no host (a replay from a spool): its mode
// change is counted, but the host has not been told, so the transition is announced by the next
// live start instead — lastAnnouncedMode, not lastReportedMode, is what the banner compares against.
func (d *daemon) recordContractObservability(results []contract.Result, mode contract.Mode, announce bool) bool {
	if d.m != nil {
		for _, r := range results {
			if !r.OK {
				d.m.Counter(counterContractFailPrefix + toUnderscoreID(r.ID)).Add(1)
			}
		}
	}

	d.modeMu.Lock()
	changed := d.lastReportedMode != mode
	d.lastReportedMode = mode
	justDegraded := false
	if announce {
		justDegraded = d.lastAnnouncedMode != contract.ModeDegradedPassive && mode == contract.ModeDegradedPassive
		d.lastAnnouncedMode = mode
	}
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

// The degrade banner's size bounds (C1.20). The banner is a systemMessage, and the host delivers a
// systemMessage whole only up to hookio.HostFieldMaxChars; over it the user sees a file path and a
// preview instead. The values the banner quotes are not Qompack's to bound: Observed can be
// host-supplied text (session_start.source_compact reports the SessionStart payload's `source`
// verbatim), so each variable part is cut to a fixed budget here, measured the way the host
// measures (hookio.HostChars), and the whole banner stays an order of magnitude under the cap.
const (
	// degradeBannerMaxChars is the most the whole banner can ever measure: the fixed text plus the
	// three bounded parts below, with room to spare.
	degradeBannerMaxChars = 1000
	// degradeBannerValueMaxChars bounds each quoted Expected/Observed value, quotes and cut marker
	// included. A real contract value is a few words; this keeps a long one legible.
	degradeBannerValueMaxChars = 200
	// degradeBannerIDMaxChars bounds the assertion id. Every shipped id is under 40 characters.
	degradeBannerIDMaxChars = 64
)

// bannerCutMarker ends a value boundedQuote had to cut, so a reader never mistakes a prefix for the
// whole observation. It is one UTF-16 unit.
const bannerCutMarker = "…"

// degradeBanner renders the §12.1 SystemMessage banner naming the first failing SevCritical
// assertion, falling back to a generic banner in the (should-be-impossible) case RunAll reported
// ModeDegradedPassive without any critical failure in this run's own results. Every variable part is
// bounded (degradeBannerMaxChars), so the banner never reaches the host's file-path fallback.
func degradeBanner(results []contract.Result) string {
	for _, r := range results {
		if !r.OK && r.Severity == contract.SevCritical {
			return fmt.Sprintf("Qompack: degraded to passive recording — %s expected %s, observed %s. See /qompack:status.",
				boundedPrefix(string(r.ID), degradeBannerIDMaxChars),
				boundedQuote(r.Expected),
				boundedQuote(r.Observed))
		}
	}
	return "Qompack: degraded to passive recording. See /qompack:status."
}

// boundedQuote is strconv.Quote(s) — what %q renders — when that fits in degradeBannerValueMaxChars
// host characters. Otherwise it quotes the longest whole-rune prefix of s that fits together with
// bannerCutMarker.
//
// It measures the QUOTED form, not s, because quoting is what makes a value long: %q spells a
// control rune as a ten-character \U escape and doubles every quote and backslash. Quoting is
// per rune and context-free, so the quoted form of a prefix is the concatenation of each rune's
// own quoted form, which is what lets this walk s once. An invalid UTF-8 byte is walked as the
// one-byte unit strconv.Quote escapes it as, so the cut never splits a rune or an escape.
func boundedQuote(s string) string {
	if q := strconv.Quote(s); hookio.HostChars(q) <= degradeBannerValueMaxChars {
		return q
	}
	// Room for the two quotes and the marker.
	budget := degradeBannerValueMaxChars - 2 - hookio.HostChars(bannerCutMarker)
	used, end := 0, 0
	for end < len(s) {
		_, size := utf8.DecodeRuneInString(s[end:])
		q := strconv.Quote(s[end : end+size])
		n := hookio.HostChars(q) - 2
		if used+n > budget {
			break
		}
		used += n
		end += size
	}
	return strconv.Quote(s[:end] + bannerCutMarker)
}

// boundedPrefix is s when it fits in maxChars host characters, and otherwise its longest whole-rune
// prefix that fits together with bannerCutMarker. It is for text the banner prints unquoted.
func boundedPrefix(s string, maxChars int) string {
	if hookio.HostChars(s) <= maxChars {
		return s
	}
	budget := maxChars - hookio.HostChars(bannerCutMarker)
	used, end := 0, 0
	for end < len(s) {
		_, size := utf8.DecodeRuneInString(s[end:])
		n := hookio.HostChars(s[end : end+size])
		if used+n > budget {
			break
		}
		used += n
		end += size
	}
	return s[:end] + bannerCutMarker
}

// handleCheckpoint is the checkpoint route: records the PreCompact observation into History,
// writes the terminal-hook marker, then — when svc.PreCompact is bound and the mode MayAct() —
// calls it timed into the B-E histogram and records the route's wall time. It records no
// instruction: the PreCompact focus instruction is retired (C1.18, wire_checkpoint.go), and the
// hook client answers the host with the empty object whatever this route replies.
func (d *daemon) handleCheckpoint(ctx context.Context, req ipc.Request) ipc.Response {
	ev := resolveEvent(req)
	now := core.NowMilli(d.clk)
	routeStart := d.clk.Now()

	// Phase 1 (locked): record the PreCompact observation — this package's own file I/O only,
	// no seam call — and save immediately, matching the spec's own ordering (history observation,
	// then the marker, then the seam call).
	//
	// The observation arms session_start.source_compact: the session's next start must be a
	// compact one. A PreCompact replayed from a hook's spool (drainDispatch) can arrive after that
	// start — the hook spools a request whose reply missed its deadline, which this daemon may
	// already have handled — and re-arming then would pin the obligation on the session's NEXT start,
	// a resume, and degrade the project at critical severity for Qompack's own replay order. So a
	// replay arms it only if no start of the session has been seen since the hook fired
	// (SessionRegistry.StartedSince); the PreCompact a daemon never saw live, replayed by the
	// startup drain ahead of its compact start, still arms it.
	//
	// LastPrecompactTS is when the host fired the PreCompact (the hook's own timestamp), not when this
	// route ran: the session.start route compares it with a replayed start's hook time, to tell a start
	// fired before the PreCompact from the one it announced (handleSessionStart).
	d.historyMu.Lock()
	h := contract.LoadHistory(contract.HistoryPath(d.root))
	if !spoolReplay(ctx) || !d.registry.StartedSince(ev.SessionID, req.TS) {
		h.LastPrecompactTS = hookTime(req, now)
		h.LastPrecompactSession = ev.SessionID
		h.AwaitingCompactStart = true
	}
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
	// would queue behind it for the duration. The seam is called for the seal it makes; whatever
	// Output it returns is discarded, because nothing a PreCompact reply could carry survives the
	// host's PreCompact contract (C1.12) and the focus instruction it used to carry is retired
	// (C1.18). The route therefore answers the empty object whichever seam is bound.
	if d.svc.PreCompact != nil && mode.MayAct() {
		var callErr error
		// d.m is dereferenced unguarded here and in handleStatus (M-12): New always seeds it
		// (obs.New(o.Clock) when o.Metrics is nil), so it is never nil for a *daemon reached
		// through New — unlike the ingest/drain layer's defensive "if i.m != nil" checks, which
		// exist because those types are also constructible directly by tests without going
		// through New.
		_ = obs.Timed(d.m.Hist(histName(obs.BE)), func() error {
			_, callErr = d.svc.PreCompact(ctx, *ev)
			return callErr
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

	h.AddPrecompactWallSample(d.clk.Now().Sub(routeStart).Milliseconds())

	if err := contract.SaveHistory(contract.HistoryPath(d.root), h); err != nil {
		d.log.Warn("daemon: failed to save history", "err", err)
	}

	out := hookio.Empty()
	return ipc.Response{OK: true, Output: &out}
}

// precompactTimeoutMs reads the PreCompact hook's manifest timeout (internal/pluginmanifest),
// converted to milliseconds (manifestHookTimeoutMs).
func precompactTimeoutMs() int64 {
	return manifestHookTimeoutMs("PreCompact")
}

// manifestHookTimeoutMs reads a hook event's manifest timeout (internal/pluginmanifest), converted
// to milliseconds. The manifest exposes it as a per-second integer on the one HookEntry the event's
// HookGroup carries; an unexpectedly empty manifest shape reports 0 (unknown) rather than guessing.
func manifestHookTimeoutMs(event string) int64 {
	m := pluginmanifest.Default(core.Version)
	groups, ok := m.Hooks.Hooks[event]
	if !ok || len(groups) == 0 || len(groups[0].Hooks) == 0 {
		return 0
	}
	const msPerSecond = 1000
	return int64(groups[0].Hooks[0].Timeout) * msPerSecond
}

// handleFlush is the flush (SessionEnd) route (C1.15, session_end.go). It answers as soon as the flush
// is durable — its line in the session's WAL and leased, what an observe event's ACK promises — and
// ends the session on a goroutine of its own, because the host gives every plugin SessionEnd hook one
// shared 1.5 s budget and cancels a hook still running when it runs out. The registry learns at once
// that the host ended the session; everything else is endSession's, which the goroutine runs. A Reply
// request (an older hook client, an operator, a test) asks for the end's own answer and waits for it.
func (d *daemon) handleFlush(ctx context.Context, req ipc.Request) ipc.Response {
	ev := resolveEvent(req)
	d.registry.End(ev.SessionID, core.NowMilli(d.clk))

	own, durable, err := d.acceptSessionEnd(req)
	if err != nil {
		// Not durable, so not acknowledged: the hook client spools the flush, and a drain replays it.
		return ipc.Response{OK: false, Err: err.Error()}
	}
	done := d.startSessionEnd(ctx, req, own, durable)
	if !req.Reply {
		return ipc.Response{OK: true}
	}
	select {
	case resp := <-done:
		return resp
	case <-ctx.Done():
		return ipc.Response{OK: false, Err: ctx.Err().Error()}
	}
}

// flushRoute is a session's end with no accepted flush of its own to finish: the form drainDispatch
// replays a spooled or WAL flush line through when no session end of its own could take it
// (endDrainedFlush: Run's startup drain, Stop's drain, a drain a session end runs itself). drain is
// false: a flush replayed BY Drain must never call back into Drain on the same goroutine —
// drainer.Drain holds a plain, non-reentrant sync.Mutex for the whole replay, so a re-entrant call
// would deadlock the daemon on the very first drained flush line, including the startup drain that
// runs before Run dispatches any request (Critical C-1, fix round 1). The drain that replays such
// a line acknowledges it itself when this answers OK, which it does not when the end's context cut it
// short, and hands its lease over on the context, so the end still settles only the arrivals before
// it (sessionEndArrival).
func (d *daemon) flushRoute(ctx context.Context, req ipc.Request, drain bool) ipc.Response {
	return d.endSession(ctx, req, drain, nil)
}

// endSession is the work a session's end has always been: registry.End, ingest.CloseSession, the
// settle of the session's earlier deliveries, svc.SessionEnd when bound and the mode MayRecord(), the
// terminal-hook marker, SketchSet.Save, then — unless drain is false — a drain. own is the accepted
// flush this end was started for (startSessionEnd), or nil: when there is one, the end settles only the
// arrivals before it and, once SessionEnd, the marker and the sketches are done, finishes it
// (finishOwnFlush) before its final drain, which would otherwise meet the flush's own line still owned.
func (d *daemon) endSession(ctx context.Context, req ipc.Request, drain bool, own *job) ipc.Response {
	ev := resolveEvent(req)
	now := core.NowMilli(d.clk)
	released := own == nil
	release := func(done bool) {
		if !released {
			released = true
			d.ing.seen.finish(own.key, done)
		}
	}
	defer release(false) // an end that does not get as far as finishing its flush leaves it pending

	// A recovery-needed state, recorded BEFORE anything is finalized. Everything below this line
	// can be interrupted, and until the marker is cleared the session's flush is unfinished — which
	// is what SessionEnd must record rather than declaring work final that was never acknowledged.
	d.markRecoveryNeeded(ev.SessionID, recoveryStageBegin, d.DrainGaps().PendingBytes)

	d.registry.End(ev.SessionID, now)
	_ = d.ing.CloseSession(ev.SessionID)

	// MayRecord() is an extra mode-check site beyond the normative table's list — defensible
	// because SessionEnd is SP-08's L1 flush semantics (store.Flush and friends), which is
	// recording work, not acting work, so it belongs behind the same predicate row 1's
	// ingest.Accept uses, not behind MayAct() (M-3).
	//
	// cut is set when the end's context ended before SessionEnd had finished: the session was not
	// ended, whatever SessionEnd returned, since every step of it answers its context.
	cut := false
	if d.svc.SessionEnd != nil && d.monitor.Mode().MayRecord() {
		// SessionEnd is the session's last arrival: its earlier deliveries publish first (C1.1).
		d.settleSession(ctx, ev.SessionID, drain, sessionEndArrival(ctx, own))
		d.markRecoveryNeeded(ev.SessionID, recoveryStageSessionEnd, 0)
		if err := d.svc.SessionEnd(ctx, *ev); err != nil {
			d.log.Warn("daemon: SessionEnd failed", "err", err)
		}
		cut = ctx.Err() != nil
		// What the settle left parked is the WAL's now; the ended session's lane stops holding it.
		if n := d.ing.lanes.forget(ev.SessionID); n > 0 {
			d.log.Debug("daemon: flush: released the ended session's parked deliveries to the WAL",
				"session", string(ev.SessionID), "jobs", n)
		}
	}

	d.markRecoveryNeeded(ev.SessionID, recoveryStageMarker, 0)
	if err := contract.WriteMarker(d.root, ev.SessionID, now); err != nil {
		d.log.Warn("daemon: WriteMarker failed", "err", err)
	}

	if d.svc.Sketches != nil {
		d.markRecoveryNeeded(ev.SessionID, recoveryStageSketches, 0)
		d.svc.Sketches.Save(d.root, d.log)
	}

	if own != nil {
		d.finishOwnFlush(ctx, own, release)
	}

	if !drain {
		if cut {
			// A flush a drain replays inline is acknowledged by that drain when this answers OK. One
			// whose end its context cut short has not ended its session: it stays unacknowledged for
			// a later drain to replay, and not acknowledged with SessionEnd never having run.
			return ipc.Response{OK: false, Err: "daemon: session end cut short: " + ctx.Err().Error()}
		}
		// The drained-flush path does not run the replay, so it is not the step that finishes the
		// flush; the marker stays until a route that does run it clears it.
		return ipc.Response{OK: true}
	}

	d.markRecoveryNeeded(ev.SessionID, recoveryStageDrain, 0)
	n, err := d.Drain(ctx)
	data, _ := json.Marshal(map[string]any{"drained": n})
	if err != nil {
		// The replay did not finish, so neither did the flush. The marker stays: an unacknowledged
		// delivery must reappear after restart rather than be finalized here.
		return ipc.Response{OK: false, Err: err.Error(), Data: data}
	}
	// Only a complete replay finishes the flush. A drain that left gaps keeps the marker, so restart
	// finds a session that needs recovery instead of one that looks finalized.
	if gaps := d.DrainGaps(); gaps.Observed && gaps.Complete {
		d.clearRecoveryNeeded(ev.SessionID)
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

// handleAdminShutdown stops the daemon asynchronously and answers OK. The Stop goroutine can reach
// ipc's Close (through runCancel and Serve's context.AfterFunc) before this handler's reply is
// written, so the reply's delivery rests on Close leaving a connection with a request in flight open
// for that reply, under a bounded write deadline (internal/ipc server.go; the V6 close-out's N1,
// where the reply was lost in one Linux -race run in 100 before Close did).
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

// ---------------------------------------------------------------------------
// Privacy admission on the daemon side (invariant 1, S1 gap 4)
//
// Admission used to live only in internal/cli, so exactly two ways in had none: a direct IPC
// caller talking to a resident daemon (dispatchOp), and a spool/WAL record inherited from an
// earlier process (drain.go). Both are reached here.
//
// The rule that decides which of the two models applies is deliberately one-directional:
//
//   - A request that ALREADY carries a decision (req.Capture != nil) keeps it. The daemon never
//     re-runs a policy over a payload that has already been through one, because the only material
//     a second pass could work from is the derived Event, and re-deriving bytes from it could put
//     back content the first policy removed. A decision is inspected for internal consistency and
//     otherwise taken as given.
//   - A request that carries NO decision is admitted here, before anything is persisted, over the
//     most faithful reconstruction of the host payload this side has: the Event as it arrived,
//     merged with the unclaimed top-level keys Raw preserved. That capture is honest about what it
//     is — its fidelity can never be exact, because the transport already dropped whatever the
//     Event's field list does not name.
//
// Denial and failure are different answers. Denial is a decision: nothing is persisted, the
// delivery is terminally resolved, and the host result is untouched. Failure is a gap: nothing is
// persisted either, but the delivery stays retryable and is reported as a gap rather than being
// quietly counted as published.
const (
	counterAdmissionDenied   = "l0_admission_denied"
	counterAdmissionFailed   = "l0_admission_failed"
	counterAdmissionDegraded = "l0_admission_degraded"
	counterAdmissionDaemon   = "l0_admission_daemon"
	// counterAdmissionScopeDenied counts PROVEN out-of-project refusals by the path-scope trust
	// boundary (V6-AUTH-1), and counterAdmissionScopeUnavailable counts refusals where containment
	// could not be proven. Both are distinct from a content-policy denial so an operator can tell an
	// out-of-project (or unprovable) capture refusal from a redaction refusal (authority-review §7
	// #7). Neither carries any path text.
	counterAdmissionScopeDenied      = "l0_admission_scope_denied"
	counterAdmissionScopeUnavailable = "l0_admission_scope_unavailable"
	counterEvidenceOnly              = "l0_capture_evidence_only"
	counterDeliveryUnleased          = "l0_delivery_unleased"
	counterDeliveryAckFailed         = "l0_delivery_ack_failed"
	counterSidecarFailed             = "l0_capture_sidecar_failed"
)

// admissionVerdict is the daemon-side privacy gate's answer for one delivery.
type admissionVerdict struct {
	// Request is req with Capture normalized to the decision that governs it. Downstream code —
	// the WAL line, the sidecar, the drain that later re-reads that line — sees exactly one
	// decision, made once.
	Request ipc.Request
	// Denied means policy refused retention. Persist nothing; do not retry.
	Denied bool
	// Failed means no decision could be reached. Persist nothing; the delivery is a gap.
	Failed bool
	// Degraded means the policy DID decide and the decision is degraded: the delivery is admitted
	// as EVIDENCE — a sidecar carrying the fidelity, the capture error and the observed source
	// size — and not as an observation. It is neither a denial nor a gap, and treating it as
	// either loses the one record that says a delivery arrived and could not be admitted whole.
	Degraded bool
	// Reason is a closed label, never payload-derived text.
	Reason string
}

// captureIsDecided separates "the policy decided, and the decision is degraded" from "no decision
// could be reached". A capture that was RECORDED by an admission run and NAMES why it is degraded,
// from core's closed label set, is a decision: an oversize payload, a short host read, or a
// delivery that was not an admissible JSON object. Each of those is a fact about what arrived, and
// re-running the policy on a later pass cannot change it.
//
// The three excluded labels are the ones that describe THIS PROCESS rather than the delivery:
// CaptureErrorPolicy (no policy compiled, so nothing classified anything), CaptureErrorContract
// (the record itself is malformed), and CaptureErrorNone paired with a non-OK outcome (an outcome
// that refuses to say why, which is exactly the shape a decision cannot have).
func captureIsDecided(c hookio.Capture) bool {
	if !c.Recorded() {
		return false
	}
	switch c.CaptureError {
	case core.CaptureErrorNone, core.CaptureErrorPolicy, core.CaptureErrorContract:
		return false
	default:
		return true
	}
}

// admitDelivery applies the gate above. It is safe to call more than once for the same delivery:
// the second call sees the capture the first one attached and returns it unchanged.
func (d *daemon) admitDelivery(req ipc.Request) admissionVerdict {
	if req.Capture != nil {
		c := *req.Capture
		if !c.Fidelity.Valid() || !c.CaptureError.Valid() {
			return admissionVerdict{Request: req, Failed: true, Reason: string(core.CaptureErrorContract)}
		}
		switch {
		case c.Outcome == core.OutcomeDenied:
			return admissionVerdict{Request: req, Denied: true, Reason: "policy denied"}
		case c.Outcome == core.OutcomeOK:
			// A supplied OK decision is taken as given for CONTENT — re-running the policy could
			// only work from the derived Event and might restore what the first policy removed. Path
			// SCOPE is a separate boundary and is NOT trusted from the client: every byte source this
			// delivery would make durable is scoped (see scopeSupplied). A forged OK capture that
			// names an out-of-project path in its Event, its already-redacted Capture.Bytes, or its
			// Raw extras — or one whose path cannot be proven — is refused, persisting nothing
			// (authority-review §5, forged-OKcap bypass).
			if v := d.scopeRefusal(req); v != nil {
				return *v
			}
			return admissionVerdict{Request: req}
		case captureIsDecided(c):
			// A degraded DECISION is admitted as evidence, not refused — UNLESS a byte source it
			// would persist fails scope. This branch used to return before scoping, so a degraded
			// capture whose retained prefix carried an out-of-project (or unprovable) path was a
			// durable leak.
			if v := d.scopeRefusal(req); v != nil {
				return *v
			}
			return admissionVerdict{Request: req, Degraded: true, Reason: string(c.CaptureError)}
		default:
			return admissionVerdict{Request: req, Failed: true, Reason: string(c.CaptureError)}
		}
	}
	if d.m != nil {
		d.m.Counter(counterAdmissionDaemon).Add(1)
	}
	cfg := d.currentCfg()
	payload, fragment, err := d.capturePolicies(cfg)
	if err != nil {
		return admissionVerdict{Request: req, Failed: true, Reason: string(core.CaptureErrorPolicy)}
	}
	raw, rawErr := reconstructedPayload(req)
	if rawErr != nil {
		return admissionVerdict{Request: req, Failed: true, Reason: string(core.CaptureErrorNotJSON)}
	}
	// The path-scope trust boundary runs before the content policy: an out-of-project (or
	// unprovable) file capture is refused whether or not redaction would have admitted it, and
	// refusing here means the policy is never even run over bytes that must not be retained.
	if v := d.scopeRefusal(req); v != nil {
		return *v
	}
	capture, _, err := hookio.CaptureHook(raw, cfg.Runtime.HotPath.MaxPayloadBytes,
		redact.CapturePolicyVersion, payload, hookio.CaptureFragment{Policy: fragment})
	// A payload reconstructed from an already-decoded Event is never the host's literal delivery,
	// so an "exact" verdict over it would claim a fidelity this side cannot support.
	if capture.Fidelity == core.FidelityExact {
		capture.Fidelity = core.FidelityPartial
	}
	req.Capture = &capture
	switch {
	case err != nil:
		return admissionVerdict{Request: req, Failed: true, Reason: string(capture.CaptureError)}
	case capture.Outcome == core.OutcomeDenied:
		return admissionVerdict{Request: req, Denied: true, Reason: "policy denied"}
	case capture.Outcome != core.OutcomeOK && captureIsDecided(capture):
		return admissionVerdict{Request: req, Degraded: true, Reason: string(capture.CaptureError)}
	case capture.Outcome != core.OutcomeOK:
		return admissionVerdict{Request: req, Failed: true, Reason: string(capture.CaptureError)}
	}
	return admissionVerdict{Request: req}
}

// scopeRefusal returns a non-nil refusal verdict when any byte source a supplied or reconstructed
// delivery would make durable fails the path-scope boundary, and nil when it may proceed. A PROVEN
// escape is Denied — a decision, terminal, persist nothing. An UNPROVABLE source is Failed — a gap
// recorded as unavailable, never a false absence, because "cannot prove inside" is not "proven
// outside". Both persist nothing; the finer denied-vs-unavailable distinction the client mints on
// the Capture itself cannot be expressed on a WAL-identical request the daemon must not rewrite, so
// the daemon refuses and records which kind under its own closed counter (authority-review §7 #7).
func (d *daemon) scopeRefusal(req ipc.Request) *admissionVerdict {
	switch d.scopeSupplied(req) {
	case hookio.ScopeOutOfProject:
		if d.m != nil {
			d.m.Counter(counterAdmissionScopeDenied).Add(1)
		}
		return &admissionVerdict{Request: req, Denied: true, Reason: hookio.ScopeOutOfProject.Reason()}
	case hookio.ScopeUnprovable:
		if d.m != nil {
			d.m.Counter(counterAdmissionScopeUnavailable).Add(1)
		}
		return &admissionVerdict{Request: req, Failed: true, Reason: hookio.ScopeUnprovable.Reason()}
	default:
		return nil
	}
}

// scopeSupplied scopes EVERY byte source a delivery would make durable and returns the worst
// verdict: the reconstructed Event+Raw the object-store record derives from, the already-redacted
// Capture.Bytes the sidecar persists, and the Raw extras on their own (a forged Raw the reconstructed
// merge masks behind the Event's own fields). Parsing the redacted bytes reintroduces nothing — they
// are exactly what would be written. A request that cannot be reconstructed cannot be proven inside.
func (d *daemon) scopeSupplied(req ipc.Request) hookio.ScopeVerdict {
	raw, err := reconstructedPayload(req)
	if err != nil {
		return hookio.ScopeUnprovable
	}
	worst := hookio.CaptureScopeRaw(d.root, raw).Verdict
	if req.Capture != nil && len(req.Capture.Bytes) > 0 {
		worst = worseScope(worst, hookio.CaptureScopeRaw(d.root, req.Capture.Bytes).Verdict)
	}
	if len(req.Raw) > 0 {
		worst = worseScope(worst, hookio.CaptureScopeRaw(d.root, req.Raw).Verdict)
	}
	return worst
}

// worseScope returns the more restrictive of two verdicts: a proven escape dominates an unprovable
// one, which dominates allow. It is how one out-of-scope source among several refuses the whole.
func worseScope(a, b hookio.ScopeVerdict) hookio.ScopeVerdict {
	switch {
	case a == hookio.ScopeOutOfProject || b == hookio.ScopeOutOfProject:
		return hookio.ScopeOutOfProject
	case a == hookio.ScopeUnprovable || b == hookio.ScopeUnprovable:
		return hookio.ScopeUnprovable
	default:
		return hookio.ScopeAllow
	}
}

// capturePolicies compiles the configured rule set at most once per configuration. Compilation is
// ~20 fresh regexps; a resident daemon that re-derived them per delivery would pay the most
// expensive half of admission on every direct IPC call and every drained line.
func (d *daemon) capturePolicies(cfg config.Config) (payload, fragment hookio.CapturePolicy, err error) {
	d.policyMu.Lock()
	defer d.policyMu.Unlock()
	if d.policyPayload != nil && reflect.DeepEqual(d.policyCfg, cfg.Runtime.Redact) {
		return d.policyPayload, d.policyFragment, nil
	}
	p, f, err := redact.CapturePolicies(cfg)
	if err != nil {
		d.policyPayload, d.policyFragment = nil, nil
		return nil, nil, err
	}
	d.policyCfg, d.policyPayload, d.policyFragment = cfg.Runtime.Redact, p, f
	return p, f, nil
}

// reconstructedPayload rebuilds the host payload from what survived the transport: the Event's own
// named fields plus the unclaimed top-level keys Raw carried. It is a reconstruction and is treated
// as one — see admitDelivery's fidelity downgrade — but it is the complete set of bytes this side
// could ever persist for a request that arrived without a capture, so it is exactly the right thing
// to put in front of the policy.
func reconstructedPayload(req ipc.Request) ([]byte, error) {
	fields := map[string]json.RawMessage{}
	if len(req.Raw) != 0 {
		var extra map[string]json.RawMessage
		if json.Unmarshal(req.Raw, &extra) == nil {
			for k, v := range extra {
				fields[k] = v
			}
		}
	}
	if req.Event != nil {
		encoded, err := json.Marshal(req.Event)
		if err != nil {
			return nil, err
		}
		var named map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &named); err != nil {
			return nil, err
		}
		for k, v := range named {
			fields[k] = v
		}
	}
	if len(fields) == 0 {
		fields["session_id"], _ = json.Marshal(string(req.Session))
	}
	return json.Marshal(fields)
}

// admissionResponse renders a refused delivery. A denial and a failure both ACK the transport —
// neither is something the client can fix by sending the payload again — and both say which they
// are, so "we recorded nothing" is never confused with "we could not tell".
func admissionResponse(v admissionVerdict) ipc.Response {
	outcome := string(core.OutcomeUnavailable)
	if v.Denied {
		outcome = string(core.OutcomeDenied)
	}
	data, _ := json.Marshal(map[string]string{"outcome": outcome, "reason": v.Reason})
	return ipc.Response{OK: true, Data: data}
}

// admittedCaptureKey carries the daemon-side admission decision from dispatchOp to the route that
// hands the request to the ingest queue. It is a context value rather than a field on the request
// because the request's ENCODED bytes are the WAL record, and rewriting them here would make the
// daemon's own copy of a delivery differ from the client's spool copy of the same delivery — two
// content identities for one event, which is precisely the confusion invariant 2 forbids.
type admittedCaptureKey struct{}

func withAdmittedCapture(ctx context.Context, c *hookio.Capture) context.Context {
	if c == nil {
		return ctx
	}
	return context.WithValue(ctx, admittedCaptureKey{}, c)
}

// withCapture attaches the admitted decision to an in-memory request, leaving one already present
// alone. The result is what the ingest queue persists as a sidecar; the WAL line is unchanged.
func withCapture(ctx context.Context, req ipc.Request) ipc.Request {
	if req.Capture != nil {
		return req
	}
	if c, ok := ctx.Value(admittedCaptureKey{}).(*hookio.Capture); ok {
		req.Capture = c
	}
	return req
}

// ---------------------------------------------------------------------------
// SessionEnd recovery state (T20-M1-05, invariant 5)
//
// flushRoute runs registry.End -> ingest.CloseSession -> svc.SessionEnd -> contract.WriteMarker ->
// Sketches.Save -> Drain. Every one of those steps can be interrupted, and the sequence used to
// leave nothing behind that said so: contract.WriteMarker is SP-08's "the terminal hook fired"
// witness, which a crashed flush also writes on its next attempt, so it cannot distinguish a
// finished flush from an abandoned one.
//
// The marker below is that distinction. It is written BEFORE the sequence starts and removed only
// after every step has returned, so a marker found on disk means exactly one thing: a SessionEnd
// began and did not finish, and the work it was flushing has not been finalized. It records which
// step was last entered, so recovery knows whether the interruption was before or after the store's
// own flush — never so that recovery can skip a step, only so it can say what it is resuming.
const sessionRecoveryFile = "session-recovery.json"

// The flush stages a recovery marker can name, in the order flushRoute runs them.
const (
	recoveryStageBegin      = "begin"
	recoveryStageSessionEnd = "session_end"
	recoveryStageMarker     = "marker"
	recoveryStageSketches   = "sketches"
	recoveryStageDrain      = "drain"
)

// SessionRecovery is state/session-recovery.json's shape: one entry per session whose SessionEnd
// began and has not been observed to finish.
type SessionRecovery struct {
	Version  int                              `json:"v"`
	Sessions map[core.SessionID]RecoveryEntry `json:"sessions"`
}

// RecoveryEntry says what was in progress and how far it got.
type RecoveryEntry struct {
	Stage string         `json:"stage"`
	TS    core.UnixMilli `json:"ts"`
	// Unacknowledged is how many spool bytes the drain still had to account for when the flush
	// began. It is evidence for the recovery decision, not an instruction to it.
	Unacknowledged int64 `json:"unacknowledged"`
}

func sessionRecoveryPath(root string) string {
	return filepath.Join(paths.Of(root).State, sessionRecoveryFile)
}

// LoadSessionRecovery reads the recovery-needed set. A missing file is an empty set — no session is
// mid-flush — while an unreadable one is an error, because "we cannot tell" must not be rendered as
// "nothing to recover".
//
// The daemon's own two callers hold recoveryMu with the writer, but the function is exported and
// read from other processes too (test/e2e polls it while the daemon settles a session end), so the
// read is shared (paths.ReadFileShared): on Windows an ordinary handle would fail
// writeSessionRecovery's paths.WriteAtomic, which nothing retries, and leave a stale recovery
// marker behind (test/guards' sharedReaders).
func LoadSessionRecovery(root string) (SessionRecovery, error) {
	b, err := paths.ReadFileShared(sessionRecoveryPath(root))
	if os.IsNotExist(err) {
		return SessionRecovery{Version: core.EvidenceVersion, Sessions: map[core.SessionID]RecoveryEntry{}}, nil
	}
	if err != nil {
		return SessionRecovery{}, err
	}
	var sr SessionRecovery
	if err := json.Unmarshal(b, &sr); err != nil {
		return SessionRecovery{}, fmt.Errorf("daemon: session recovery state is unreadable")
	}
	if sr.Sessions == nil {
		sr.Sessions = map[core.SessionID]RecoveryEntry{}
	}
	return sr, nil
}

func (d *daemon) writeSessionRecovery(sr SessionRecovery) error {
	sr.Version = core.EvidenceVersion
	b, err := json.Marshal(sr)
	if err != nil {
		return err
	}
	p := sessionRecoveryPath(d.root)
	if err := os.MkdirAll(paths.Long(filepath.Dir(p)), 0o700); err != nil {
		return err
	}
	return paths.WriteAtomic(p, b, 0o600)
}

// markRecoveryNeeded records that sess entered stage. Every call rewrites the whole file, which is
// what makes the marker's presence the fact and its stage merely the detail. recoveryMu makes each
// rewrite see the one before it: session ends run concurrently since C1.15.
func (d *daemon) markRecoveryNeeded(sess core.SessionID, stage string, unacknowledged int64) {
	d.recoveryMu.Lock()
	defer d.recoveryMu.Unlock()
	sr, err := LoadSessionRecovery(d.root)
	if err != nil {
		// Unreadable recovery state is itself a recovery-needed condition; replace it rather than
		// leaving a flush unmarked, and say so.
		d.log.Warn("daemon: session recovery state unreadable; replacing", "err", err)
		sr = SessionRecovery{Sessions: map[core.SessionID]RecoveryEntry{}}
	}
	sr.Sessions[sess] = RecoveryEntry{Stage: stage, TS: core.NowMilli(d.clk), Unacknowledged: unacknowledged}
	if err := d.writeSessionRecovery(sr); err != nil {
		d.log.Warn("daemon: could not record SessionEnd recovery state", "err", err)
	}
}

// clearRecoveryNeeded removes sess's marker. It runs only after every flush step has returned, so a
// marker that survives is a genuine interruption and not a slow step.
func (d *daemon) clearRecoveryNeeded(sess core.SessionID) {
	d.recoveryMu.Lock()
	defer d.recoveryMu.Unlock()
	sr, err := LoadSessionRecovery(d.root)
	if err != nil {
		return
	}
	if _, ok := sr.Sessions[sess]; !ok {
		return
	}
	delete(sr.Sessions, sess)
	if err := d.writeSessionRecovery(sr); err != nil {
		d.log.Warn("daemon: could not clear SessionEnd recovery state", "err", err)
	}
}
