package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/qompack/qompack/internal/store"
)

// The stateful scheduler.Runtime the daemon owns (00-ARCHITECTURE.md §5.13; plan
// V4-SP-12-scheduler-l3 "scheduler_runtime.go"). scheduler.Evaluate is pure and internal/scheduler
// may import only foundation packages, so everything that needs the store, the DAG, the clock or
// the filesystem — session binding, the §2.5 window ladder, the measured-δ and burn-rate EWMAs,
// candidate assembly, the session-scoped state files — lives here, in the composition root.

const (
	deltaEWMAAlpha = 0.3 // smoothing for the measured compaction cost δ
	burnEWMAAlpha  = 0.2 // smoothing for the token burn rate

	// maxTurnHistory bounds cpTurns and rounds. 4096 is in §11.6's forbidden integer set, so
	// the annotation is mandatory — it is a memory bound, not a config default.
	maxTurnHistory = 4096 //nomagic:allow bounded-history cap, not an Appendix C default
)

// The documented host variables the window ladder reads (Qompack.md §2.5), by their documented
// names, always through the caller-supplied getenv (config.Env.Getenv) and never os.Getenv.
const (
	envAutoCompactWindow              = "CLAUDE_CODE_AUTO_COMPACT_WINDOW"
	envContextWindow                  = "QOMPACK_CONTEXT_WINDOW"
	envMaxOutputTokens                = "QOMPACK_MAX_OUTPUT_TOKENS" //nolint:gosec // an environment-variable name, not a credential (G101 keys on "TOKENS")
	envMaxContextTokens               = "CLAUDE_CODE_MAX_CONTEXT_TOKENS"
	envDisable1MContext               = "CLAUDE_CODE_DISABLE_1M_CONTEXT"
	envDisableCompact                 = "DISABLE_COMPACT"
	envDisableUnknownModelEnforcement = "CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT"
	envEffort                         = "CLAUDE_EFFORT"
)

// The documented range of CLAUDE_CODE_AUTO_COMPACT_WINDOW and CLAUDE_CODE_MAX_CONTEXT_TOKENS.
// A value outside it is CLAMPED into the range (ruling R53; the host's own range is a clamp
// range) — only an unparsable, empty or non-positive value falls through to the next rung.
const (
	hostWindowEnvMin core.Tokens = 100_000
	hostWindowEnvMax core.Tokens = 1_000_000
)

// Breakdown["window_source"] values: which rung of the ladder supplied the window.
const (
	windowSourceHostDefault      = 1.0 // HostDefaultContextWindow / HostDefaultMaxOutput
	windowSourceOverride         = 2.0 // QOMPACK_CONTEXT_WINDOW / QOMPACK_MAX_OUTPUT_TOKENS
	windowSourceMaxContextTokens = 2.5 // CLAUDE_CODE_MAX_CONTEXT_TOKENS with the host default output
	windowSourceAutoCompact      = 3.0 // CLAUDE_CODE_AUTO_COMPACT_WINDOW is the effective window
)

// The Breakdown keys the runtime stamps AFTER scheduler.Evaluate returns, so Evaluate stays pure
// and /qompack:status never presents a guess as a measurement.
const (
	breakdownWindowSource           = "window_source"
	breakdownHostCompactionDisabled = "host_compaction_disabled"
	breakdownWindowClamped200k      = "window_clamped_200k"
	breakdownHostEnforcementOff     = "host_enforcement_off"
)

// The runtime's own instruments.
const (
	counterPSelectionEnabled = "sched.pselection.enabled"

	// The two must never share a counter: one is the host acting on its own context window, the
	// other is this layer sealing an artifact beside it (see NoteLocalCheckpoint).
	counterHostCompaction  = "sched.compaction.host"
	counterLocalCheckpoint = "sched.checkpoint.local"
	counterChangepoint     = "sched.changepoint"
	counterPersist         = "sched.persist"
)

// The hookio.Event.Extra keys the runtime reads: the model id and subagent id the SessionStart
// payload carries, and the effort object every event may carry.
const (
	extraModel   = "model"
	extraAgentID = "agent_id"
	extraEffort  = "effort"
)

// The SessionStart source the host uses after its own auto-compact.
const sessionSourceCompact = "compact"

// Unit conversions for the EWMAs.
const (
	msPerSecondF     = 1_000.0
	secondsPerMinute = 60.0
)

// SchedulerRuntimeOptions is what NewSchedulerRuntime needs. Store, Graph, Clock, Log and
// ProjectRoot are required; everything else may be nil or empty.
type SchedulerRuntimeOptions struct {
	ProjectRoot string
	Session     core.SessionID // may be empty; bound by the tap on the first SessionStart
	Cfg         config.Config
	// CfgFn, when set, supplies the configuration at each use instead of Cfg. A composition root
	// passes the daemon's live configuration (Options.CurrentCfg), so a key the daemon's config
	// reload applies reaches the scheduler's next evaluation and idle pass (V6 close-out D49).
	// The keys it cannot apply live (the changepoint detector's shape, the cache regime) the
	// reload holds back until a restart (reload_keys.go), so they never change under it.
	CfgFn   func() config.Config
	Clock   core.Clock
	Log     logging.Logger
	Metrics obs.Registry
	Store   store.Store
	Graph   dag.Graph
	Ledger  negknow.Ledger // may be nil
	// LedgerFn resolves the ledger LIVE, on every read, and takes precedence over Ledger.
	//
	// It exists because the daemon opens the negative-knowledge ledger LAZILY -- on the first
	// compaction, through RehydrateOptions.OpenLedger, which assigns the handle back onto
	// daemon.Options.Ledger. wireScheduler runs long before that, so the Ledger VALUE copied here
	// at composition time is nil and stays nil for the life of the process. Anything that captured
	// that value (rebuild_bloom did) was permanently inert. A composition root supplies a closure
	// over its own Options so a ledger opened later is seen; nil ⇒ the static Ledger is used.
	LedgerFn func() negknow.Ledger
	// Frontier keeps draft lifecycle ownership in checkpoint. The legacy Checkpoints/Sources
	// options remain compatible inputs to its adapter when no explicit port is supplied.
	Frontier    checkpoint.FrontierAdvancer
	Checkpoints checkpoint.Writer
	Sources     func() (checkpoint.SourceSet, error) // may be nil
	// Getenv is config.Env.Getenv. nil ⇒ every lookup answers "". NEVER os.Getenv.
	Getenv func(string) string
}

// schedRuntime is the concrete scheduler.Runtime. mu guards every field; exported entry points
// take it, *Locked helpers assume it. Evaluate holds mu across assembly; Persist snapshots under
// mu and writes without it.
type schedRuntime struct {
	mu sync.Mutex

	root    string
	session core.SessionID
	cfg     config.Config
	cfgFn   func() config.Config // may be nil; see SchedulerRuntimeOptions.CfgFn and conf
	clock   core.Clock
	log     logging.Logger
	metrics obs.Registry
	getenv  func(string) string

	st       store.Store
	segs     store.SegmentLog
	graph    dag.Graph
	ledger   negknow.Ledger              // may be nil; the static fallback for currentLedger
	ledgerFn func() negknow.Ledger       // may be nil; resolves the lazily-opened ledger live
	advancer checkpoint.FrontierAdvancer // may be nil; never exposes a draft

	det  scheduler.Detector
	hist *FeatureHistory
	asm  *candidateAssembler

	d Daemon // bound by RegisterSchedulerIdleWork (C2); nil until then

	cpTurns []core.TurnIndex // ascending, deduplicated, capped at maxTurnHistory
	rounds  map[core.TurnIndex]struct{}
	maxTurn core.TurnIndex

	contextTokens     core.Tokens
	effectiveWindow   core.Tokens
	maxOutput         core.Tokens
	windowSource      float64            // 3 env autocompact | 2.5 CLAUDE_CODE_MAX_CONTEXT_TOKENS | 2 explicit override | 1 host default
	hostBreakdown     map[string]float64 // host_compaction_disabled, window_clamped_200k, host_enforcement_off — copied into Decision.Breakdown after Evaluate
	hostTriggerAbsent bool               // → Inputs.HostTriggerAbsent

	regime        scheduler.CacheRegime // resolved at BindSession via scheduler.ResolveCacheRegime
	model         string                // from the SessionStart event's Extra["model"] when present
	subagent      bool                  // Extra["agent_id"] present on the binding event
	lastEffort    string
	effortChanged bool // set by NoteEffort; consumed (reset) by Evaluate after it is read

	precomputed   dag.Slice
	precomputedOK bool

	lastActivity       core.UnixMilli
	lastAPICallTS      core.UnixMilli
	lastRequestStartTS core.UnixMilli
	lastCacheWriteTS   core.UnixMilli
	lastCompactionTS   core.UnixMilli
	// lastLocalCheckpointTS is when QOMPACK last sealed a checkpoint ON ITS OWN CADENCE. It is a
	// SEPARATE field from lastCompactionTS and must stay one: a local checkpoint is an artifact
	// this layer wrote beside the host, whereas a compaction is an action the HOST took on its own
	// context window. Folding a cadence seal into lastCompactionTS would restart the Young–Daly
	// clock and claim a δ sample for a compaction that never happened, so the scheduler would
	// believe the host had just compacted every time an idle tick sealed a draft.
	lastLocalCheckpointTS core.UnixMilli
	sessionStartTS        core.UnixMilli

	deltaEWMA    float64
	deltaSamples int
	burnEWMA     float64
	burnSamples  int
	lastTokens   core.Tokens
	lastTokensTS core.UnixMilli

	openSegTokens core.Tokens // Σ rec.Tokens since the open segment opened; reset by closeSegmentLocked

	lastDecision      scheduler.Decision
	lastEvaluateTS    core.UnixMilli
	frontierRuns      uint64
	frontierPlannedAt uint64
	frontierSkipTicks int
	frontier          core.TurnIndex
	residual          core.Tokens
	lastCheckpointSeq core.CheckpointSeq
	residualWarned    bool
	dirty             bool
	tapPanicLogged    bool // one Loud per session from the tap's recover

	// Additive to the seat contract (documented in the C1 report):
	//   persistMu serializes Persist end to end so two concurrent persists cannot write an older
	//   snapshot over a newer one; it is taken BEFORE mu and mu is released before the writes.
	//   idx is the reclaimable-token index Evaluate hands the assembler, built from the DAG's
	//   KindToolResult nodes joined to the store's records and cached on maxTurn exactly like the
	//   assembler's turn→Pos map.
	persistMu  sync.Mutex
	idx        *reclaimableIndex
	idxBuiltAt core.TurnIndex
	idxBuilt   bool
}

// NewSchedulerRuntime constructs the daemon-owned runtime. Store, Graph, Clock, Log and
// ProjectRoot are validated in that order; a missing one is reported as
// "daemon: scheduler runtime: <name> required" and the p-selection gate stays closed — the daemon
// tolerates a nil Sched (00-ARCHITECTURE §5.4), so an error here degrades the scheduler, never
// the session. Session may be empty: the daemon is per project and starts before any session
// exists, so the id arrives with the first SessionStart and the tap binds it then. On success
// scheduler.EnablePSelection() runs — the closing-note-3 unlock.
func NewSchedulerRuntime(o SchedulerRuntimeOptions) (scheduler.Runtime, error) {
	switch {
	case o.Store == nil:
		return nil, requiredDep("store")
	case o.Graph == nil:
		return nil, requiredDep("graph")
	case o.Clock == nil:
		return nil, requiredDep("clock")
	case o.Log == nil:
		return nil, requiredDep("log")
	case o.ProjectRoot == "":
		return nil, requiredDep("project root")
	}
	getenv := o.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	cp := o.Cfg.Scheduler.Changepoint
	if o.CfgFn != nil {
		cp = o.CfgFn().Scheduler.Changepoint
	}
	advancer := o.Frontier
	if advancer == nil && o.Checkpoints != nil && o.Sources != nil {
		advancer = checkpoint.NewFrontierAdvancer(o.Checkpoints, o.Sources)
	}
	r := &schedRuntime{
		root:     o.ProjectRoot,
		cfg:      o.Cfg,
		cfgFn:    o.CfgFn,
		clock:    o.Clock,
		log:      o.Log,
		metrics:  o.Metrics,
		getenv:   getenv,
		st:       o.Store,
		segs:     o.Store.Segments(),
		graph:    o.Graph,
		ledger:   o.Ledger,
		ledgerFn: o.LedgerFn,
		advancer: advancer,
		det:      scheduler.NewBOCD(cp.HazardRate, cp.Features),
		hist:     NewFeatureHistory(defaultFeatureWindow),
		rounds:   map[core.TurnIndex]struct{}{},
	}
	r.asm = newCandidateAssembler(o.Graph, r.segs, o.Log, o.Metrics)
	r.sessionStartTS = r.nowMS()
	if o.Session != "" {
		r.BindSession(o.Session, nil)
	}
	scheduler.EnablePSelection()
	r.count(counterPSelectionEnabled)
	return r, nil
}

// conf is the configuration in effect now: the live supplier's when one is wired, the
// construction-time configuration otherwise. It takes no lock of r's; the supplier's own lock is a
// leaf, so it is safe with or without mu held.
func (r *schedRuntime) conf() config.Config {
	if r.cfgFn != nil {
		return r.cfgFn()
	}
	return r.cfg
}

func requiredDep(name string) error {
	return fmt.Errorf("daemon: scheduler runtime: %s required", name)
}

// currentLedger resolves the ledger AT THE MOMENT OF THE CALL: the supplier first, the static
// field second. Every consumer must go through it rather than read r.ledger, because the daemon's
// ledger does not exist at construction time -- see SchedulerRuntimeOptions.LedgerFn. It takes mu
// only to read the two fields and releases it before the supplier runs, so a supplier that reaches
// back into the daemon cannot deadlock against a task body already holding mu.
func (r *schedRuntime) currentLedger() negknow.Ledger {
	r.mu.Lock()
	fn, static := r.ledgerFn, r.ledger
	r.mu.Unlock()
	if fn != nil {
		if l := fn(); l != nil {
			return l
		}
	}
	return static
}

// ── Session binding and the window ladder ────────────────────────────────────────────────────

// BindSession binds id: idempotent for the same id (a SessionStart(source=compact) re-fire only
// re-reads the model/subagent hints and restarts the Young–Daly clock), a full reset for a new
// one. In order: reset the session-scoped state, resolve the window (the §2.5 ladder, logged at
// Info once per session), resolve the cache regime from the binding event's model/agent_id,
// load state/bocd.json and state/scheduler.json for this id (session-scoped: another session's
// documents are discarded), seed the Young–Daly baseline from the session start when the loaded
// state has none, and re-open the p-selection gate that a previous session's Close released.
// e may be nil (tests); an empty id is ignored.
func (r *schedRuntime) BindSession(id core.SessionID, e *hookio.Event) {
	if id == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bindSessionLocked(id, e)
}

// bindSessionLocked is BindSession under r.mu, for a caller that already holds it; id is non-empty.
func (r *schedRuntime) bindSessionLocked(id core.SessionID, e *hookio.Event) {
	if e != nil {
		r.noteBindingEventLocked(e)
	}
	if r.session == id {
		r.regime = r.resolveRegimeLocked()
		if e != nil && e.Source == sessionSourceCompact {
			// The host has just compacted: the interval the Young–Daly clause measures restarts.
			r.lastCompactionTS = r.nowMS()
			r.dirty = true
		}
		// A same-id rebind after Close is the `--resume` of a session whose SessionEnd already
		// ran while this daemon stayed up: Close persisted the state this runtime still holds,
		// so nothing is reloaded, but the gate Close released must open again — Close is
		// idempotent, not final, and a bound runtime is a live one.
		scheduler.EnablePSelection()
		return
	}
	r.resetSessionLocked()
	r.session = id
	r.sessionStartTS = r.nowMS()
	r.resolveWindowLocked()
	r.regime = r.resolveRegimeLocked()
	r.loadStateLocked()
	if r.lastCompactionTS == 0 {
		r.lastCompactionTS = r.sessionStartTS
	}
	// The live context is the closed segments the log already holds for this session plus the
	// restored open accumulator — computed here, not left at open-only, so the first Evaluate's
	// recount is not a jump. The burn clock is re-baselined on it with no timestamp: the first
	// activity after a (re)bind starts the clock, and the restore itself is never a burn sample.
	r.recomputeContextTokensLocked(context.Background())
	r.lastTokens, r.lastTokensTS = r.contextTokens, 0
	scheduler.EnablePSelection()
	r.log.Info("scheduler: session bound",
		"session", string(id), "effective_window", int(r.effectiveWindow), "max_output", int(r.maxOutput),
		"window_source", r.windowSource, "host_trigger_absent", r.hostTriggerAbsent,
		"regime", r.regime.Source, "changepoints", len(r.cpTurns))
}

// bindOnFirstHook binds sess when the runtime is bound to no session and sess is live.
//
// A daemon that restarted in the middle of a session gets no SessionStart for it, and only a
// SessionStart, or a PreCompact's compaction close, bound the runtime. For the rest of such a
// session every Evaluate answered error_no_window and Persist wrote nothing. So the tap calls this
// before its own work on every tool use, Stop and captured prompt: the live session's first hook to
// reach the restarted daemon binds it, restoring its state from state/bocd.json and
// state/scheduler.json as a SessionStart bind does, and folding in whatever this runtime observed
// while unbound (bindUnboundLocked). The binding event is not a SessionStart, so it carries no model
// hint: the cache regime resolves from the environment and configuration until the next
// SessionStart. SessionEnd does not bind: its route ends the session in the registry first, and a
// session that is ending has nothing left to schedule.
//
// "Live" is the daemon registry's answer. Every hook route touches the registry before the event is
// dispatched, so a live hook's session is live by the time the tap sees it. A replayed delivery of a
// session no hook has touched since the restart (a leftover of another session in the drained WAL
// or a client spool) is not live and does not bind: binding it would leave the live session unbound
// for good, because only a SessionStart rebinds a bound runtime. With no daemon attached, or one
// with no registry, every session counts as live.
//
// A runtime already bound to any session is left alone, whichever session the hook names.
// Rebinding is a SessionStart's decision (BindSession).
func (r *schedRuntime) bindOnFirstHook(sess core.SessionID) {
	if sess == "" {
		return
	}
	r.mu.Lock()
	bound, d := r.session != "", r.d
	r.mu.Unlock()
	if bound {
		return
	}
	// The registry is asked without r.mu held: its lock is independent of this one, and nothing
	// here should wait on the registry while the tap's other seams wait on r.mu.
	if d != nil {
		if reg := d.Registry(); reg != nil && !reg.IsLive(sess) {
			r.count(counterTapBindNotLive)
			return
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.session != "" {
		return // another hook bound it in between
	}
	r.bindUnboundLocked(sess)
	r.count(counterTapBindFirstHook)
}

// noteBindingEventLocked reads the model id and the subagent marker off a SessionStart payload.
// Both ride Extra: neither is a key hookio.Event's struct tags claim.
func (r *schedRuntime) noteBindingEventLocked(e *hookio.Event) {
	if raw, ok := e.Extra[extraModel]; ok {
		var model string
		if err := json.Unmarshal(raw, &model); err == nil && model != "" {
			r.model = model
		}
	}
	// The subagent marker is per binding event, never sticky: a later SessionStart of the same
	// session without agent_id is the main agent's, and the regime must follow it.
	r.subagent = false
	if raw, ok := e.Extra[extraAgentID]; ok {
		var agent string
		if err := json.Unmarshal(raw, &agent); err == nil {
			r.subagent = agent != ""
		} else {
			r.subagent = len(raw) > 0 && string(raw) != "null"
		}
	}
}

func (r *schedRuntime) resolveRegimeLocked() scheduler.CacheRegime {
	return scheduler.ResolveCacheRegime(r.getenv, r.conf().Scheduler, r.model, r.subagent,
		r.conf().Runtime.Scheduler.Cache.AssumeMaxTTLSeconds)
}

// resetSessionLocked clears everything session-scoped: the detector, the feature history, the
// turn histories, the token accounting, the EWMAs, the decision and the local frontier.
// Checkpoint drafts remain with their owner across a scheduler session change.
func (r *schedRuntime) resetSessionLocked() {
	cp := r.conf().Scheduler.Changepoint
	r.det = scheduler.NewBOCD(cp.HazardRate, cp.Features)
	r.hist = NewFeatureHistory(defaultFeatureWindow)
	r.asm.Invalidate()
	r.idx, r.idxBuilt, r.idxBuiltAt = nil, false, 0
	r.cpTurns = nil
	r.rounds = map[core.TurnIndex]struct{}{0: {}} // turn 0 is always a legal cut point
	r.maxTurn = 0
	r.contextTokens, r.openSegTokens = 0, 0
	r.lastEffort, r.effortChanged = "", false
	r.precomputed, r.precomputedOK = dag.Slice{}, false
	r.lastActivity, r.lastAPICallTS, r.lastRequestStartTS, r.lastCacheWriteTS, r.lastCompactionTS = 0, 0, 0, 0, 0
	r.lastLocalCheckpointTS = 0
	r.deltaEWMA, r.deltaSamples = 0, 0
	r.burnEWMA, r.burnSamples, r.lastTokens, r.lastTokensTS = 0, 0, 0, 0
	r.lastDecision, r.lastEvaluateTS = scheduler.Decision{}, 0
	r.frontierRuns, r.frontierPlannedAt, r.frontierSkipTicks = 0, 0, 0
	r.frontier, r.residual, r.lastCheckpointSeq = 0, 0, 0
	r.residualWarned, r.dirty, r.tapPanicLogged = false, false, false
}

// resolveWindowLocked is the §2.5 ladder, read through getenv at bind:
//
//	3   CLAUDE_CODE_AUTO_COMPACT_WINDOW, clamped into [100 000, 1 000 000] — IS the effective window, maxOutput 0
//	2   QOMPACK_CONTEXT_WINDOW (+ QOMPACK_MAX_OUTPUT_TOKENS, else the host default output)
//	2.5 CLAUDE_CODE_MAX_CONTEXT_TOKENS, clamped into the same range, with the host default output
//	1   HostDefaultContextWindow / HostDefaultMaxOutput (§2.5's worked example: 180 000)
//
// The ranges on rungs 3 and 2.5 are clamps, not preconditions (ruling R53): 50 000 resolves as
// 100 000 and 2 000 000 as 1 000 000, both still naming their rung in window_source; only an
// unparsable, empty or non-positive value falls through. Then CLAUDE_CODE_DISABLE_1M_CONTEXT
// clamps whichever won to HostDefaultContextWindow (window_clamped_200k), DISABLE_COMPACT marks
// the host trigger absent (host_compaction_disabled)
// and CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT does the same while the window still
// resolves normally (host_enforcement_off). The host keys and window_source are stamped onto
// Decision.Breakdown after scheduler.Evaluate returns, so Evaluate sees only the number.
func (r *schedRuntime) resolveWindowLocked() {
	r.hostBreakdown = make(map[string]float64, 3)
	r.hostTriggerAbsent = false
	clamp1M := hostEnvTruthy(r.getenv(envDisable1MContext))
	clamped := false // window_clamped_200k is stamped only when the clamp changed the number
	clampTo200k := func(v core.Tokens) core.Tokens {
		if clamp1M && v > scheduler.HostDefaultContextWindow {
			clamped = true
			return scheduler.HostDefaultContextWindow
		}
		return v
	}

	// Ruling R53: rungs 3 and 2.5 CLAMP to the documented range (the host's is a clamp range);
	// only an unparsable, empty or non-positive value falls through to the next rung.
	if v, ok := parseTokenEnv(r.getenv(envAutoCompactWindow)); ok && v > 0 {
		r.effectiveWindow = clampTo200k(clampHostRange(v))
		r.maxOutput, r.windowSource = 0, windowSourceAutoCompact
	} else {
		window, output := scheduler.HostDefaultContextWindow, scheduler.HostDefaultMaxOutput
		source := windowSourceHostDefault
		if cw, ok := parseTokenEnv(r.getenv(envContextWindow)); ok && cw > 0 {
			window, source = cw, windowSourceOverride
			if v, ok := parseTokenEnv(r.getenv(envMaxOutputTokens)); ok && v >= 0 {
				output = v
			}
			if scheduler.EffectiveWindow(window, output) <= 0 {
				// An override that leaves no effective window would make error_no_window the
				// whole session's answer; the host default is the honest fallback.
				r.log.Warn("scheduler: QOMPACK_CONTEXT_WINDOW/QOMPACK_MAX_OUTPUT_TOKENS leave no effective window; using the host default",
					"context_window", int(window), "max_output", int(output))
				window, output, source = scheduler.HostDefaultContextWindow, scheduler.HostDefaultMaxOutput, windowSourceHostDefault
			}
		} else if mc, ok := parseTokenEnv(r.getenv(envMaxContextTokens)); ok && mc > 0 {
			window, source = clampHostRange(mc), windowSourceMaxContextTokens
		}
		window = clampTo200k(window)
		r.effectiveWindow, r.maxOutput, r.windowSource = scheduler.EffectiveWindow(window, output), output, source
	}
	if clamped {
		r.hostBreakdown[breakdownWindowClamped200k] = 1
	}
	if hostEnvTruthy(r.getenv(envDisableCompact)) {
		r.hostBreakdown[breakdownHostCompactionDisabled] = 1
		r.hostTriggerAbsent = true
	}
	if hostEnvTruthy(r.getenv(envDisableUnknownModelEnforcement)) {
		r.hostBreakdown[breakdownHostEnforcementOff] = 1
		r.hostTriggerAbsent = true
	}
}

// clampHostRange clamps a rung-3 / rung-2.5 value into the documented [100 000, 1 000 000].
func clampHostRange(v core.Tokens) core.Tokens {
	return min(max(v, hostWindowEnvMin), hostWindowEnvMax)
}

// parseTokenEnv parses a token count from an environment value; ok is false for an empty or
// non-numeric value.
func parseTokenEnv(v string) (core.Tokens, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, false
	}
	return core.Tokens(n), true
}

// hostEnvTruthy is the host-variable truth rule: the trimmed, lower-cased value is non-empty and
// not one of 0, false, no, off.
func hostEnvTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}

// ── scheduler.Runtime ────────────────────────────────────────────────────────────────────────

// Observe folds one observation into the detector. On a declaration the turn joins cpTurns
// (deduplicated, ascending, capped), sched.changepoint is counted and the current segment is
// closed with cause "changepoint". Every observation dirties the state for the next persist.
func (r *schedRuntime) Observe(ctx context.Context, f scheduler.Features, at core.TurnIndex) scheduler.ChangepointState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.observeLocked(ctx, f, at)
}

func (r *schedRuntime) observeLocked(ctx context.Context, f scheduler.Features, at core.TurnIndex) scheduler.ChangepointState {
	st := r.det.Observe(f)
	r.maxTurn = max(r.maxTurn, at)
	r.dirty = true
	if st.AtChangepoint {
		r.recordChangepointLocked(at)
		r.count(counterChangepoint)
		if err := r.closeSegmentLocked(ctx, at, f, causeChangepoint); err != nil {
			r.log.Warn("scheduler: segment close at changepoint failed", "turn", int(at), "err", err.Error())
		}
	}
	return st
}

// recordChangepointLocked inserts at into cpTurns keeping it ascending and duplicate-free — the
// assembler emits one candidate per entry, so a turn observed twice must not appear twice — and
// evicts the oldest entries beyond maxTurnHistory.
func (r *schedRuntime) recordChangepointLocked(at core.TurnIndex) {
	i, found := slices.BinarySearch(r.cpTurns, at)
	if found {
		return
	}
	r.cpTurns = slices.Insert(r.cpTurns, i, at)
	if excess := len(r.cpTurns) - maxTurnHistory; excess > 0 {
		n := copy(r.cpTurns, r.cpTurns[excess:])
		r.cpTurns = r.cpTurns[:n]
	}
}

// Evaluate runs the composite trigger against the current state. It never errors for ordinary
// states: before a session is bound it returns a Decision with Breakdown["error_no_window"]=1.
func (r *schedRuntime) Evaluate(ctx context.Context) (scheduler.Decision, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.evaluateLocked(ctx), nil
}

// evaluateLocked assembles Inputs, calls the pure scheduler.Evaluate, stamps the window and
// host keys, and stores lastDecision/lastEvaluateTS. effortChanged is consumed by exactly one
// evaluation so a single /effort switch produces one cold classification.
func (r *schedRuntime) evaluateLocked(ctx context.Context) scheduler.Decision {
	now := r.nowMS()
	in := scheduler.Inputs{Now: now, Cfg: r.conf().Scheduler}
	if r.effectiveWindow > 0 {
		in.ContextTokens = r.recomputeContextTokensLocked(ctx)
		in.EffectiveWindow = r.effectiveWindow
		in.MaxOutputTokens = r.maxOutput
		in.LastAPICallTS = r.lastAPICallTS
		in.LastCacheWriteTS = r.lastCacheWriteTS
		in.BurnRateTokensPerMin = r.burnEWMA
		in.MeasuredDeltaSeconds = r.deltaPtr()
		in.Changepoint = r.det.State()
		in.Candidates = r.assembleCandidatesLocked(ctx)
		in.FrontierTurn = r.frontier
		in.ResidualTokens = r.residual
		in.LastCompactionTS = r.lastCompactionTS
		in.CouplingLambda = r.conf().Selection.Submodular.Lambda
		in.Regime = r.regime
		in.LastRequestStartTS = r.lastRequestStartTS
		in.EffortChanged = r.effortChanged
		in.ExpiringTriggerFraction = r.conf().Runtime.Scheduler.Cache.ExpiringTriggerFraction
		in.AssumeMaxTTLSeconds = r.conf().Runtime.Scheduler.Cache.AssumeMaxTTLSeconds
		in.HostTriggerAbsent = r.hostTriggerAbsent
	}
	d := scheduler.Evaluate(in)
	r.effortChanged = false
	if r.effectiveWindow > 0 {
		d.Breakdown[breakdownWindowSource] = r.windowSource
		for k, v := range r.hostBreakdown {
			d.Breakdown[k] = v
		}
	}
	r.lastDecision, r.lastEvaluateTS = d, now
	return d
}

// assembleCandidatesLocked refreshes the reclaimable index and asks the assembler for the
// changepoint ∩ round-boundary candidates. An assembly error degrades to no candidates (Debug):
// evaluating with what it has is honest; assuming SegmentID 0 would not be.
func (r *schedRuntime) assembleCandidatesLocked(ctx context.Context) []scheduler.Candidate {
	r.rebuildReclaimableLocked(ctx)
	cands, err := r.asm.Assemble(ctx, r.session, r.cpTurns, r.rounds, r.idx, r.maxTurn)
	if err != nil {
		r.log.Debug("scheduler: candidate assembly degraded; evaluating without candidates", "err", err.Error())
		return nil
	}
	return cands
}

// rebuildReclaimableLocked builds the suffix-sum reclaimable index from the DAG's KindToolResult
// nodes — Ref carries the tool_use id and Pos the token position (dag.BuildToolUse) — joined to
// the store's records for the drop class and token count. A record the store no longer has is
// classified from the node alone. Cached on maxTurn like the assembler's turn→Pos map: rebuilt
// when the turn advances, reused otherwise.
func (r *schedRuntime) rebuildReclaimableLocked(ctx context.Context) {
	if r.idxBuilt && r.maxTurn <= r.idxBuiltAt {
		return
	}
	nodes := r.graph.NodesAfter(0)
	blocks := make([]dropBlock, 0, len(nodes))
	for _, n := range nodes {
		if n.Kind != dag.KindToolResult || n.Ref == "" {
			continue
		}
		if ctx.Err() != nil {
			return // leave the previous index in place; the next Evaluate retries
		}
		tokens := n.Tokens
		class := scheduler.DropClassOf("", n.Ephemeral, false)
		if rec, err := r.st.ToolUse(ctx, core.ToolUseID(n.Ref)); err == nil {
			class = ClassifyDrop(rec)
			if rec.Tokens > 0 {
				tokens = rec.Tokens
			}
		}
		blocks = append(blocks, dropBlock{Pos: n.Pos, Tokens: tokens, Class: class})
	}
	r.idx = newReclaimableIndex(blocks)
	r.idxBuiltAt, r.idxBuilt = r.maxTurn, true
}

// recomputeContextTokensLocked is n: Σ Segment.Tokens over this session's CLOSED segments from
// one segs.Range(0, maxTurn) call, plus openSegTokens, the tokens the tap has folded into the
// still-open segment. Both halves are required: Segment.Tokens is written once, by Close, so an
// open segment contributes zero to Range. Range failing ⇒ the open accumulator alone (Debug).
func (r *schedRuntime) recomputeContextTokensLocked(ctx context.Context) core.Tokens {
	var closed core.Tokens
	if r.segs != nil {
		segs, err := r.segs.Range(ctx, 0, r.maxTurn)
		if err != nil {
			r.log.Debug("scheduler: segment range unavailable; counting the open segment only", "err", err.Error())
		} else {
			for _, s := range segs {
				if s.Closed && s.Session == r.session {
					closed += s.Tokens
				}
			}
		}
	}
	r.contextTokens = closed + r.openSegTokens
	return r.contextTokens
}

// deltaPtr returns the measured δ, or nil until a compaction has been measured — "null means
// measure at runtime, not zero".
func (r *schedRuntime) deltaPtr() *float64 {
	if r.deltaSamples == 0 {
		return nil
	}
	v := r.deltaEWMA
	return &v
}

// NotifyActivity records activity at ts (never rewinding), sets the API-call clock the sliding
// TTL keys on (E1), and folds a burn-rate sample from the tokens observed since the previous
// sample when both the token and time deltas are positive. It does not forward to
// IdleController.Notify: SP-05's registry already does that per accepted request.
func (r *schedRuntime) NotifyActivity(ts core.UnixMilli) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ts <= r.lastActivity {
		return
	}
	r.lastActivity, r.lastAPICallTS = ts, ts
	r.dirty = true
	if r.lastTokensTS == 0 {
		r.lastTokens, r.lastTokensTS = r.contextTokens, ts
		return
	}
	dtok := r.contextTokens - r.lastTokens
	dt := ts - r.lastTokensTS
	switch {
	case dtok < 0:
		// The context shrank under us (a recount after a close): re-baseline, no sample.
		r.lastTokens, r.lastTokensTS = r.contextTokens, ts
	case dtok > 0 && dt > 0:
		// tokens per minute; multiplied out first so whole-minute intervals divide exactly.
		rate := float64(dtok) * (secondsPerMinute * msPerSecondF) / float64(dt)
		if r.burnSamples == 0 {
			r.burnEWMA = rate
		} else {
			r.burnEWMA = burnEWMAAlpha*rate + (1-burnEWMAAlpha)*r.burnEWMA
		}
		r.burnSamples++
		r.lastTokens, r.lastTokensTS = r.contextTokens, ts
	}
}

// IdleSince reports the last activity and whether cfg.Scheduler.Idle.DetectAfterSeconds have
// elapsed since it; (0, false) before any activity.
func (r *schedRuntime) IdleSince() (core.UnixMilli, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lastActivity == 0 {
		return 0, false
	}
	after := core.UnixMilli(r.conf().Scheduler.Idle.DetectAfterSeconds) * core.UnixMilli(msPerSecondF)
	return r.lastActivity, r.nowMS()-r.lastActivity >= after
}

// Persist writes state/bocd.json and state/scheduler.json with paths.WriteAtomic. The snapshot
// is taken under mu and written without it; persistMu keeps two concurrent persists from
// landing out of order. Nothing is written before a session is bound.
func (r *schedRuntime) Persist(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.persistMu.Lock()
	defer r.persistMu.Unlock()

	files, bound, err := r.snapshotState()
	if err != nil {
		return fmt.Errorf("daemon: scheduler persist: %w", err)
	}
	if !bound {
		return nil
	}

	if err := os.MkdirAll(paths.Long(filepath.Dir(files.bocdPath)), 0o700); err != nil {
		return r.persistFailed(fmt.Errorf("daemon: scheduler persist: %w", err))
	}
	if err := paths.WriteAtomic(files.bocdPath, files.bocd, 0o600); err != nil {
		return r.persistFailed(fmt.Errorf("daemon: scheduler persist %s: %w", stateFileBOCD, err))
	}
	if err := paths.WriteAtomic(files.schedulerPath, files.scheduler, 0o600); err != nil {
		return r.persistFailed(fmt.Errorf("daemon: scheduler persist %s: %w", stateFileScheduler, err))
	}
	r.count(counterPersist)
	return nil
}

// snapshotState encodes both documents under mu and clears dirty on success; bound is false,
// with nothing encoded, before a session is bound. The unlock is deferred on purpose: the tap's
// recover (schedTap.guard) takes mu to record a panic, so a snapshot that panicked while
// holding mu without a defer would turn the first Loud into a wedge of every seam behind it.
// With the defer the panic unwinds through the lock and surfaces as that one Loud.
func (r *schedRuntime) snapshotState() (files stateFiles, bound bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.session == "" {
		return stateFiles{}, false, nil
	}
	files, err = r.saveStateLocked()
	if err == nil {
		r.dirty = false
	}
	return files, true, err
}

// persistFailed re-arms the dirty flag so the next idle tick retries, and returns err.
func (r *schedRuntime) persistFailed(err error) error {
	r.mu.Lock()
	r.dirty = true
	r.mu.Unlock()
	return err
}

// Close persists and releases the p-selection gate. Checkpoint draft lifecycle stays with the
// checkpointer. Idempotent is not final: the daemon is per project and outlives sessions, so
// the next BindSession — the same id on a `--resume`, or a new one — re-opens the gate and the
// runtime is live again; a same-id rebind keeps the in-memory state Close has just persisted.
// Not on scheduler.Runtime (Rule W-3); reached through CloseSchedulerRuntime.
func (r *schedRuntime) Close() error {
	err := r.Persist(context.Background())
	scheduler.DisablePSelection()
	return err
}

// ── Additive methods on the concrete type (reached in-package; not on the interface) ─────────

// NoteAPIRound records at as an API-round boundary (§2.6: a new assistant message.id, i.e. one
// Stop). Called only from the ObserveStop tap. The set is capped at maxTurnHistory with the
// oldest (smallest) turn evicted first.
func (r *schedRuntime) NoteAPIRound(at core.TurnIndex) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.maxTurn = max(r.maxTurn, at)
	r.rounds[at] = struct{}{}
	r.dirty = true
	for len(r.rounds) > maxTurnHistory {
		oldest := at
		for t := range r.rounds {
			oldest = min(oldest, t)
		}
		delete(r.rounds, oldest)
	}
}

// NoteRequestStart anchors the TTL clock at ts: the start of the most recent API request. Called
// from the ObserveTool and ObservePrompt taps and deliberately never from ObserveStop, which
// fires after generation and would over-report warmth by the whole generation time.
func (r *schedRuntime) NoteRequestStart(ts core.UnixMilli) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastRequestStartTS = ts
	r.dirty = true
}

// NoteEffort reads the effort level off e.Extra["effort"] ({"level": "…"}), falling back to
// getenv("CLAUDE_EFFORT"), and flags a change against the previous level. The first observation
// is not a change. Effort is part of the prompt-cache key, so a change empties the prefix
// instantly; Evaluate consumes the flag once.
func (r *schedRuntime) NoteEffort(e hookio.Event) {
	level := effortLevel(e, r.getenv)
	if level == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lastEffort != "" && level != r.lastEffort {
		r.effortChanged = true
		r.dirty = true
	}
	r.lastEffort = level
}

// effortLevel extracts the effort level from the event, then the environment.
func effortLevel(e hookio.Event, getenv func(string) string) string {
	if raw, ok := e.Extra[extraEffort]; ok && len(raw) > 0 {
		var obj struct {
			Level string `json:"level"`
		}
		if err := json.Unmarshal(raw, &obj); err == nil && obj.Level != "" {
			return strings.TrimSpace(obj.Level)
		}
		var s string
		if err := json.Unmarshal(raw, &s); err == nil && s != "" {
			return strings.TrimSpace(s)
		}
	}
	return strings.TrimSpace(getenv(envEffort))
}

// AddOpenSegmentTokens folds an observed tool-use record's tokens into the open segment's
// accumulator (the number closeSegmentLocked hands to SegmentLog.Close) and into the live
// context estimate the burn-rate sample reads.
func (r *schedRuntime) AddOpenSegmentTokens(tok core.Tokens) {
	if tok <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.openSegTokens += tok
	r.contextTokens += tok
	r.dirty = true
}

// RecordCompactionCost folds a measured compaction wall-clock into the Young–Daly δ EWMA
// (seeded on the first sample) and restarts the Young–Daly clock. Non-positive or non-finite
// values are not measurements and are ignored. This is the CostRecorder seam SP-10's checkpoint
// op and SP-14's status command reach by type assertion.
func (r *schedRuntime) RecordCompactionCost(seconds float64) {
	if !(seconds > 0) || math.IsInf(seconds, 0) {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.deltaSamples == 0 {
		r.deltaEWMA = seconds
	} else {
		r.deltaEWMA = deltaEWMAAlpha*seconds + (1-deltaEWMAAlpha)*r.deltaEWMA
	}
	r.deltaSamples++
	r.lastCompactionTS = r.nowMS()
	r.dirty = true
	r.count(counterHostCompaction)
}

// NoteLocalCheckpoint records that QOMPACK sealed a checkpoint on its own cadence (§8.5's second
// trigger clause: "checkpoints exist even when compaction does not fire").
//
// What it does NOT do is the point of it. It does not move lastCompactionTS, does not fold a δ
// sample, and is counted under its own name. A Qompack-initiated checkpoint and a host compaction
// are different events with different consequences — one writes an artifact beside the session,
// the other rewrites the session's own context — and the scheduler's every cadence decision is
// derived from "how long since the HOST last compacted". Conflating them would restart that clock
// on our own action, suppressing the next real trigger.
func (r *schedRuntime) NoteLocalCheckpoint(seq core.CheckpointSeq) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if seq > r.lastCheckpointSeq {
		r.lastCheckpointSeq = seq
	}
	r.lastLocalCheckpointTS = r.nowMS()
	r.dirty = true
	r.count(counterLocalCheckpoint)
}

// LocalCheckpointNoter is the seam the checkpoint cadence reports through. It is deliberately a
// different method from CostRecorder.RecordCompactionCost so that no caller can reach the host
// path by accident.
type LocalCheckpointNoter interface {
	NoteLocalCheckpoint(seq core.CheckpointSeq)
}

// PrecomputedSlice returns the precompute_slice cache and whether one has been computed.
func (r *schedRuntime) PrecomputedSlice() (dag.Slice, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.precomputed, r.precomputedOK
}

// nowMS samples the injected clock in milliseconds.
func (r *schedRuntime) nowMS() core.UnixMilli { return core.NowMilli(r.clock) }

// count bumps a counter when a registry is wired.
func (r *schedRuntime) count(name string) {
	if r.metrics != nil {
		r.metrics.Counter(name).Add(1)
	}
}

// ── Package-level helpers ────────────────────────────────────────────────────────────────────

// CostRecorder is implemented by the concrete runtime. It is how a measured compaction
// wall-clock reaches the Young–Daly δ EWMA; nothing may assume it is present.
type CostRecorder interface {
	RecordCompactionCost(seconds float64)
}

// SchedulerSnapshot backs /qompack:status (SP-14) and the Phase 4 harness (SP-02 fixtures).
type SchedulerSnapshot struct {
	LastDecision         scheduler.Decision
	FrontierTurn         core.TurnIndex
	ResidualTokens       core.Tokens
	MaxResidualTokens    core.Tokens
	DeltaSeconds         float64
	DeltaSamples         int
	BurnRateTokensPerMin float64
	ChangepointTurns     []core.TurnIndex
	IdleSinceMS          int64
	RegimeSource         string
	WindowSource         float64
}

// CloseSchedulerRuntime closes rt when it is the daemon's runtime and returns nil otherwise
// (Close is not on scheduler.Runtime — Rule W-3 — so this is how the tap and the shutdown path
// reach it).
func CloseSchedulerRuntime(rt scheduler.Runtime) error {
	if r, ok := rt.(*schedRuntime); ok {
		return r.Close()
	}
	return nil
}

// PrecomputedSlice returns rt's precompute_slice cache; ok is false when rt is not the daemon's
// runtime or nothing has been computed yet.
func PrecomputedSlice(rt scheduler.Runtime) (dag.Slice, bool) {
	if r, ok := rt.(*schedRuntime); ok {
		return r.PrecomputedSlice()
	}
	return dag.Slice{}, false
}

// SchedulerSnapshotOf returns a copy of rt's observable state; ok is false when rt is not the
// daemon's runtime.
func SchedulerSnapshotOf(rt scheduler.Runtime) (SchedulerSnapshot, bool) {
	r, ok := rt.(*schedRuntime)
	if !ok {
		return SchedulerSnapshot{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return SchedulerSnapshot{
		LastDecision:         r.lastDecision,
		FrontierTurn:         r.frontier,
		ResidualTokens:       r.residual,
		MaxResidualTokens:    core.Tokens(r.conf().Checkpoint.Frontier.MaxResidualTokens),
		DeltaSeconds:         r.deltaEWMA,
		DeltaSamples:         r.deltaSamples,
		BurnRateTokensPerMin: r.burnEWMA,
		ChangepointTurns:     slices.Clone(r.cpTurns),
		IdleSinceMS:          int64(r.lastActivity),
		RegimeSource:         r.regime.Source,
		WindowSource:         r.windowSource,
	}, true
}
