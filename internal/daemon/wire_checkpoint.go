package daemon

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
	"time"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/store"
)

// The checkpoint layer's wiring into the daemon. This is the only file SP-10 adds to a package it
// does not own, and it modifies no existing daemon file: it reaches the daemon through the two
// seams SP-05 already ships, in two phases, because they live on two different types.
//
// It deliberately does NOT call Options.Handle(ipc.OpCheckpoint, …). That op is already routed to
// handleCheckpoint, and Handle REPLACES a registration. Re-registering it would silently delete,
// all at once: the contract.SessionHistory PreCompact observation, the terminal-hook marker,
// the B-E histogram timing around the seam and AddPrecompactWallSample — and therefore the
// precompact.has_time_to_write assertion. The seam SP-05 built for exactly this is Options.Bind
// plus the nil-tolerant Services.PreCompact field, which handleCheckpoint calls.
//
// The seam seals and answers the empty object. It used to return the checkpointer's focus
// instruction (Qompack.md §8.5, O1) as a PreCompact customInstructions for the route to record,
// but no host accepts one: Claude Code has no PreCompact hookSpecificOutput variant, 2.1.280
// rejected the whole response over it (C1.12), and custom_instructions is PreCompact INPUT, not a
// summarizer-output setter (§7.3, §8.5 "Retire O1's output setter"). That producer is retired
// (C1.18); the checkpoint is Qompack's own artifact and does not need the summarizer's help.
const (
	// idleTaskAdvanceFrontier keeps the checkpoint frontier moving during idle windows (O5), so
	// PreCompact only has to finalize. Its name deliberately carries NO "act." prefix: the idle
	// controller gates acting tasks purely by that prefix, and frontier advancement must keep
	// running in ModeDegradedPassive. It writes only state/draft-*.json, never touches the context
	// window, and §12.1 explicitly keeps L1-correctness work running while degraded.
	idleTaskAdvanceFrontier = "advance_frontier"
	// idleTaskCheckpointCadence is the second half of Qompack.md §8.5's trigger clause: "and
	// independently on the scheduler's own cadence, so checkpoints exist even when compaction does
	// not fire." Unlike frontier advancement this one IS scheduler-initiated, so §12.1 requires it
	// off in ModeDegradedPassive — and the "act." prefix is the whole mechanism that turns it off.
	// A task named "checkpoint_cadence" would carry no prefix and would keep sealing checkpoints
	// while degraded.
	idleTaskCheckpointCadence = "act.checkpoint_cadence"
	// idleTaskMaterializePins regenerates pins/invariants.json from the append-only log. No "act."
	// prefix, deliberately: it is derived-state maintenance, it acts on nothing, and a stale view
	// during a degraded window is exactly the drift this layer exists to prevent.
	idleTaskMaterializePins = "materialize_pins"

	idlePrioAdvanceFrontier   = 10
	idlePrioCheckpointCadence = 20
	idlePrioMaterializePins   = 40
)

// precompactDeadlineSlack is subtracted from the manifest's PreCompact timeout to give the daemon
// its own, earlier deadline.
//
// The arithmetic is nested, and the nesting is the point:
//
//	daemon-internal deadline   now + 20s - 6s = 14s   (this file)
//	client reply wait          15s                    (cli.checkpointReplyDeadline, shipped)
//	host hook timeout          20s                    (pluginmanifest, shipped)
//
// 14 < 15 < 20: the daemon always answers before the client gives up, and the client always
// answers before the host's own timeout. A smaller slack would invert the first pair and produce a
// client-side timeout on exactly the slow compactions the deadline exists to survive. Raising the
// daemon's budget means raising checkpointReplyDeadline in internal/cli FIRST.
const precompactDeadlineSlack = 6 * time.Second

// cadenceSegmentThreshold is the number of segments encoded into a draft since Begin that, on its
// own, makes the draft worth sealing. It is the second of §8.5's two cadence conditions; the first
// is the draft reaching checkpoint.budgetTokens. Both are computed from state the checkpoint
// package already holds, so the cadence does not consult the scheduler and works on a tree where
// SP-12 has not merged.
const cadenceSegmentThreshold = 8

// sessionLister is the optional capability a Store may have for enumerating sessions it already
// knows about. store.Store does not declare RecentSessions — it lives on the concrete
// *store.FSStore — so this is reached by type assertion, the same way dag.Maintainer is.
//
// It is the SECONDARY source, and deliberately so: RecentSessions is ordered by End then Start and
// is populated from the store's session index, so a session that has started but not ended is
// exactly the session it cannot see. That is the only kind of session frontier advancement cares
// about. It earns its place for the other case — a daemon that has just restarted mid-project and
// has no live registry entry yet for a session whose segments are already on disk.
type sessionLister interface {
	RecentSessions(n int) []core.SessionID
}

// maxTrackedSessions bounds the recent-session sweep. A daemon serving more concurrent sessions
// than this is well outside the design point, and the drafts of the ones left out are still sealed
// by their own PreCompact.
const maxTrackedSessions = 32

// BindCheckpoint binds the PreCompact seam before daemon.New. It must run BEFORE New, because
// Options.Bind is what New applies to the Services struct it constructs.
//
// The variadic options are the same WireOptions WireCheckpoint takes, so the shipped four-argument
// call sites keep compiling. WithSourceSupplier is the one that matters here: it is what lets the
// bound seam re-resolve the SourceSet AT COMPACTION TIME instead of sealing from the snapshot
// wiring happened to hold, which on the production path is the ledger-less one.
func BindCheckpoint(o *Options, cfg config.Config, w *checkpoint.FileWriter, src checkpoint.SourceSet, opts ...WireOption) {
	var wc wireCfg
	for _, opt := range opts {
		opt(&wc)
	}
	resolve := wc.sources
	if resolve == nil {
		resolve = staticSources(src)
	}

	// Publish the seams to the writer before anything can call it. The cold PreCompact path -- a
	// compaction that fires before the first idle tick -- has no draft to take a SourceSet from,
	// and there is no reason to make it wait for one when the daemon holds it right here.
	//
	// The error is deliberately not returned: SetSources has already said Loud which seam is
	// missing, and a checkpoint layer that cannot publish its sources is a degradation (§12.3),
	// never a reason to refuse to construct a daemon.
	_ = w.SetSources(src)

	// The bind body's own logger. §16 requires this seam's two abnormal outcomes -- a panicking
	// checkpointer and an unreadable hook timeout -- to be Loud, and the daemon's own logger is
	// unreachable from inside the bound closure: Services carries no logger field, and o.Log is
	// the value New copies onto the daemon it constructs. Capturing it here binds the SAME logger
	// the route that calls this seam writes through. A caller that built Options as a bare literal
	// leaves it nil, and logging.Nop still records Loud in the process-wide ring.
	log := o.Log
	if log == nil {
		log = logging.Nop()
	}

	o.Bind(func(s *Services) {
		// Publish the writer on the struct-typed service slot as well. DeclareProducers gates both
		// PreCompact contract assertions on `s.PreCompact != nil || s.Checkpoints != nil`, and
		// SP-11 and SP-13 reach the writer through Services rather than through a second
		// constructor over the same root.
		s.Checkpoints = w
		s.PreCompact = func(ctx context.Context, e hookio.Event) (out hookio.Output, err error) {
			// Panic isolation. The shipped route already treats a seam error as non-fatal and
			// still answers OK, so converting a panic into an ordinary error keeps a checkpointer
			// bug from taking the daemon's worker pool with it.
			//
			// It is Loud, and that is §16, not decoration: the route that calls this seam reports
			// a returned error as an ordinary Warn, so a checkpointer that panics on every single
			// compaction would otherwise be indistinguishable in the logs from one that merely
			// declined once. A panic here means the L4 layer is not sealing anything at all.
			defer func() {
				if r := recover(); r != nil {
					log.Loud("checkpoint: PreCompact panicked; no checkpoint was sealed for this compaction",
						"session", string(e.SessionID), "trigger", e.Trigger, "recover", r)
					out = hookio.Empty()
					err = fmt.Errorf("checkpoint: PreCompact panicked: %v", r)
				}
			}()

			timeout := time.Duration(precompactTimeoutMs()) * time.Millisecond
			// now is taken BEFORE arming, so the arming cost is spent INSIDE the daemon's own
			// 14 s window rather than added to it. The nested 14 s < 15 s < 20 s argument above
			// only holds if everything this seam does happens inside the first number.
			now := time.Now()
			armSources(o, w, resolve, log)
			deadline, known := precompactDeadline(now, timeout)
			if !known {
				// Loud rather than Warn because this is the degenerate branch
				// precompactTimeoutMs was written to make explicit rather than guess at, and the
				// shipped manifest declares 20 s: reaching it at all means the plugin manifest
				// this build is running against is not the one it was built with.
				log.Loud("checkpoint: the plugin manifest declares no PreCompact timeout; "+
					"this compaction is sealed under the minimum window and the nested "+
					"14s<15s<20s budget argument does not apply to it",
					"session", string(e.SessionID), "trigger", e.Trigger)
			}
			_, err = w.PreCompact(ctx, checkpoint.PreCompactInput{
				Session:     e.SessionID,
				Trigger:     e.Trigger,
				Now:         now,
				Deadline:    deadline,
				HookTimeout: timeout,
				Budget:      core.Tokens(cfg.Checkpoint.BudgetTokens),
				Cfg:         cfg.Checkpoint,
				// SP-12 owns p-selection, the rewrite breakdown and the TTL state, and none of
				// them is readable today: scheduler.Runtime exposes no last Decision, has no
				// in-tree implementation, and Decision.Breakdown is never assigned. Synthesizing
				// a value inside a 2 s hook would also be a scheduler decision this layer does not
				// get to make. SP-12 fills these in through this same struct.
				Cache: checkpoint.CacheInfo{TTLState: "unknown"},
			})
			// The empty object on success as on failure: the seal is the whole of this seam's job,
			// and nothing it could say survives the host's PreCompact contract (C1.18).
			return hookio.Empty(), err
		}
	})
}

// armSources makes the writer's cold path usable BEFORE the compaction that is about to need it,
// and it is the whole of the first-PreCompact fix.
//
// The shipped daemon could not seal a checkpoint on the first PreCompact of its life. The chain
// ran: wireCheckpointSources published a SourceSet whose Ledger was nil, because negknow.Open is
// lazy; SetSources dropped it; and the ledger was opened only by WireRehydrator, on the first
// COMPACTION — the SessionStart(source=compact) that arrives AFTER the PreCompact that needed it.
// The user saw `hookSpecificOutput: null` and one Warn, and no checkpoint existed for a session
// whose context had just been thrown away. Every daemon's first compaction lost its checkpoint.
//
// Two things happen here, in this order:
//
//  1. The supplier is re-resolved and republished, so the set is the one that is true NOW rather
//     than the one wiring happened to hold.
//  2. ONLY if that set still has no ledger is the one lazy open triggered, through
//     Options.OpenLedger, and its handle folded in directly rather than left to be read back off
//     Options. This is not a second open and not an eager one: it is the SAME memoized accessor
//     the rehydration uses, called by the half of the compaction that reaches it first. A daemon
//     that never compacts never runs this seam, so sketches/tried.bloom is still created only by
//     a project that actually compacted.
//
// The ORDER of those two is not cosmetic. Opening first meant opening unconditionally, and a
// caller whose supplier already resolves a live ledger -- an embedder that opened one itself and
// wired it onto both Options and the SourceSet -- then had a SECOND negknow.Open run on the same
// project root at its first PreCompact: two append handles on one records/eliminations.jsonl, two
// owners of one sketches/tried.bloom, and one of the two closed by nobody. Resolving first asks
// whether the open is needed before paying for it, which is the question the accessor's own
// laziness exists to ask.
//
// An unresolvable supplier leaves the writer holding whatever it already had — a wiring-time set
// is still better than none — and says so. It is Warn, not Loud: PreCompact's own failure path
// reports the seal it could not make, and duplicating it here would put two lines in the log for
// one event.
func armSources(o *Options, w *checkpoint.FileWriter,
	resolve func() (checkpoint.SourceSet, error), log logging.Logger,
) {
	if w == nil || resolve == nil {
		return
	}
	live, err := resolve()
	if live.Ledger == nil && o != nil && o.OpenLedger != nil {
		live.Ledger = o.OpenLedger()
	}
	if err != nil && live.Ledger == nil {
		log.Warn(msgSourcesUnavailable, "err", err.Error())
		return
	}
	if setErr := w.SetSources(live); setErr != nil && err != nil {
		log.Warn(msgSourcesUnavailable, "err", err.Error())
	}
}

// precompactDeadline computes the daemon's own PreCompact deadline from the manifest's declared
// hook timeout, and reports whether that timeout was known at all.
//
// A zero (or negative) timeout is precompactTimeoutMs saying it did not recognize the manifest
// shape and is refusing to guess. That answer must not be folded into the arithmetic as though it
// were a real timeout: `now.Add(0 - precompactDeadlineSlack)` is `now - 6s`, a deadline already in
// the past, which floors PreCompact's internal budget to its minimum finalize window on EVERY
// compaction — a 6x reduction — while looking, at the call site and in the debug artifact, like an
// ordinary deadline. The nested 14 s < 15 s < 20 s argument above silently stops applying, and
// nothing says so.
//
// So the unknown is passed on AS an unknown: the ZERO instant, which checkpoint.PreCompact's own
// budget arithmetic tests for by name and answers with the same conservative floor a past deadline
// gets. The floor is the right answer here — a compaction whose host timeout we cannot read is the
// last one that should be granted a generous window — but it is now reached because no deadline was
// supplied, not because a fabricated one had already expired. The caller says so Loud; this
// function only decides.
func precompactDeadline(now time.Time, timeout time.Duration) (deadline time.Time, known bool) {
	if timeout <= 0 {
		return time.Time{}, false
	}
	return now.Add(timeout - precompactDeadlineSlack), true
}

// WireOption is an optional extra for WireCheckpoint. It is variadic so the shipped four-argument
// call sites keep compiling unchanged.
type WireOption func(*wireCfg)

type wireCfg struct {
	noter   LocalCheckpointNoter
	sources func() (checkpoint.SourceSet, error)
}

// ReportLocalCheckpointsTo routes the cadence's own seals to the scheduler runtime, which records
// them SEPARATELY from host compactions (see schedRuntime.NoteLocalCheckpoint). Without it the
// cadence still runs and is still counted here; what is lost is only the scheduler-side record.
func ReportLocalCheckpointsTo(n LocalCheckpointNoter) WireOption {
	return func(c *wireCfg) { c.noter = n }
}

// WithSourceSupplier resolves the SourceSet AT EACH IDLE RUN instead of freezing the value
// WireCheckpoint was handed at registration.
//
// It exists because the production composition root cannot hand over a complete SourceSet at
// wiring time and must not fabricate one to look complete. SourceSet.Ledger is the negative-
// knowledge ledger, and the daemon opens that LAZILY -- on the first compaction, through
// RehydrateOptions.OpenLedger, which assigns the handle back onto daemon.Options.Ledger. An eager
// open would create sketches/tried.bloom and hold an eliminations.jsonl handle in every daemon
// that never compacts, which is exactly what that call site refuses to do. So the frozen value is
// permanently nil-Ledger, SourceSet.Validate refuses it, and the three tasks below are inert for
// the life of the process -- the same class of capture bug SchedulerRuntimeOptions.LedgerFn was
// added for, one layer up.
//
// The supplier is expected to return its PARTIAL set alongside the error when only some seams are
// missing: materialize_pins needs Pins and nothing else, and refusing to materialize pins because
// no compaction has yet opened a ledger would be a degradation with no cause.
func WithSourceSupplier(fn func() (checkpoint.SourceSet, error)) WireOption {
	return func(c *wireCfg) { c.sources = fn }
}

// counterSourcesUnavailable counts idle passes that found no usable SourceSet. It is the
// unavailable-route signal for frontier advancement: a daemon whose ledger has never been opened
// reports this once per pass and advances nothing, rather than either crashing on a nil seam or
// going silent.
const counterSourcesUnavailable = "checkpoint.sources.unavailable"

// msgSourcesUnavailable is logged Warn the FIRST time a pass finds no usable source and Debug
// afterwards. Once per pass forever would be a line every idle tick for the whole life of a daemon
// that never compacts; never logging at all is the silence §16 forbids.
const msgSourcesUnavailable = "checkpoint: no usable source set; the frontier is not advancing"

// counterCadenceSeal counts checkpoints QOMPACK sealed on its OWN cadence. It is a different
// counter from every host-compaction instrument on purpose: §8.5's cadence clause exists so that
// checkpoints exist even when compaction does NOT fire, and a report that could not tell the two
// apart would show a project with no compactions at all as a project compacting all day.
const counterCadenceSeal = "checkpoint.cadence.local_seal"

// WireCheckpoint registers the three idle tasks. It must run AFTER daemon.New, because Idle() is a
// method on the constructed Daemon and there is no Options-level idle-registration seam.
func WireCheckpoint(d Daemon, cfg config.Config, w *checkpoint.FileWriter, src checkpoint.SourceSet, opts ...WireOption) {
	idle := d.Idle()
	log := daemonLog(d)
	var wc wireCfg
	for _, o := range opts {
		o(&wc)
	}

	resolve := wc.sources
	if resolve == nil {
		// No supplier: the caller froze a value, so keep answering with it. It still validates,
		// so a half-wired caller reaches the unavailable route rather than a nil dereference.
		resolve = staticSources(src)
	}

	if cfg.Checkpoint.Frontier.AdvanceOnSegmentClose {
		idle.Register(idleTaskAdvanceFrontier, idlePrioAdvanceFrontier,
			advanceFrontierTask(d.Registry(), w, resolve, log, metricsOf(d)))
	}

	idle.Register(idleTaskCheckpointCadence, idlePrioCheckpointCadence, func(ctx context.Context) error {
		return finalizeIfDue(ctx, cfg, w, wc.noter, metricsOf(d))
	})

	idle.Register(idleTaskMaterializePins, idlePrioMaterializePins, materializePinsTask(resolve))
}

// staticSources adapts a frozen SourceSet to the supplier shape, validating it so that a
// half-wired caller is reported unavailable instead of dereferenced.
func staticSources(src checkpoint.SourceSet) func() (checkpoint.SourceSet, error) {
	return func() (checkpoint.SourceSet, error) {
		// Resolve rather than Validate, for the reason the live supplier in internal/cli records:
		// a consumer asks this question in order to BEGIN a draft, and a ledger that is still only
		// an accessor is not one it can read eliminations from. The partial set travels with the
		// reason either way.
		if _, err := src.Resolve(); err != nil {
			return src, fmt.Errorf("%w: %w", err, core.ErrDegraded)
		}
		return src, nil
	}
}

// advanceFrontierTask is the body registered as idleTaskAdvanceFrontier, built separately so the
// supplier contract has a test seam that does not need a constructed Daemon.
//
// An unusable source is NOT an idle-task error. RunOnce warns on every error it is handed, so
// returning one here would put a line in the log on every tick of every daemon that has not
// compacted yet -- for a condition that is expected, temporary and already reported once, with a
// counter behind it. §12.3's "fail toward doing nothing" is the whole handling: nothing is
// advanced, nothing is begun, and the pass is over.
//
// A set whose only gap is the ledger (checkpoint.ErrNoLedger) is not unusable (coordinator
// decision D49, F-C4-C49-3): the daemon opens its ledger lazily, and a session that neither
// compacts nor records an elimination never opens one. The sweep runs over the partial set and the
// checkpoint writer begins its drafts without negative knowledge while the project holds no
// elimination record. Only when records exist that no open ledger serves does the writer refuse,
// and that refusal takes the unavailable route above rather than a task error.
func advanceFrontierTask(reg *SessionRegistry, w *checkpoint.FileWriter,
	resolve func() (checkpoint.SourceSet, error), log logging.Logger, m obs.Registry,
) func(context.Context) error {
	if log == nil {
		log = logging.Nop()
	}
	var reported atomic.Bool
	return func(ctx context.Context) error {
		unavailable := func(err error) error {
			if m != nil {
				m.Counter(counterSourcesUnavailable).Add(1)
			}
			if reported.CompareAndSwap(false, true) {
				log.Warn(msgSourcesUnavailable, "err", err.Error())
			} else {
				log.Debug(msgSourcesUnavailable, "err", err.Error())
			}
			return nil
		}
		live, err := resolve()
		if errors.Is(err, checkpoint.ErrNoLedger) {
			sweepErr := advanceAllSessions(ctx, reg, w, live, log, m)
			if errors.Is(sweepErr, checkpoint.ErrNoLedger) {
				return unavailable(sweepErr)
			}
			return sweepErr
		}
		if err != nil {
			return unavailable(err)
		}
		// Republish to the writer now that the set has resolved. This is no longer the cold
		// PreCompact path's only hope -- BindCheckpoint publishes a set at wiring time and
		// armSources republishes one at every compaction -- but a set that has since gained a
		// resolved ledger is strictly fresher than either, and SetSources validates, takes one
		// uncontended lock and is idempotent. The error is already Loud inside.
		_ = w.SetSources(live)
		return advanceAllSessions(ctx, reg, w, live, log, m)
	}
}

// materializePinsTask is the body registered as idleTaskMaterializePins.
//
// It deliberately ignores the supplier's error and reads Pins out of the PARTIAL set: pins are
// materialized from the pin log alone and need neither the ledger, the graph nor the store. A pin
// view left stale because no compaction has happened yet is exactly the derived-state drift this
// task exists to prevent.
func materializePinsTask(resolve func() (checkpoint.SourceSet, error)) func(context.Context) error {
	return func(ctx context.Context) error {
		live, _ := resolve()
		if live.Pins == nil {
			return nil
		}
		return live.Pins.Materialize(ctx)
	}
}

// daemonLog reports the logger d was constructed with, or a no-op one for any other Daemon
// implementation. The Daemon interface exposes no logger and this file may not add one to it
// (§5.4: the interface is SP-05's and later waves attach through the existing seams), but *daemon
// is this package's own type, so the field is in scope here without widening anything exported.
func daemonLog(d Daemon) logging.Logger {
	if dd, ok := d.(*daemon); ok && dd.log != nil {
		return dd.log
	}
	return logging.Nop()
}

// advanceAllSessions encodes every closed, unencoded segment of every live session into that
// session's draft. It is the O5 amortization: per-compaction cost drops from O(session) to
// O(delta) precisely because this runs all session long.
//
// Two rules govern the loop, and both are about work that must NOT happen.
//
// Nothing is begun for a session with nothing to encode. liveSessions folds in up to
// maxTrackedSessions ids from the store's session index, which is ordered by End and therefore
// answers with sessions that have already FINISHED. Beginning a draft for one of those is not a
// cheap no-op: Begin pays a full tier-1 seeding (pins, ledger, and a whole-graph scan for the
// earliest prompt) and writes state/draft-<session>.json, and nothing ever retires that draft --
// only Abort and Finalize do -- so it stays in w.OpenDrafts() for the daemon's life, its file
// survives restarts, and finalizeIfDue re-prices it on every subsequent tick. Asking Unencoded
// first is one cheap segment-log read that keeps all of that from ever starting.
//
// ctx is consulted per session. RunOnce gives each idle task a sub-context of the budget still
// remaining in the tick, so a sweep over many sessions must stop when that budget is gone rather
// than run to completion and starve every task queued behind it.
func advanceAllSessions(ctx context.Context, reg *SessionRegistry, w *checkpoint.FileWriter, src checkpoint.SourceSet, log logging.Logger, m obs.Registry) error {
	if log == nil {
		log = logging.Nop()
	}
	countDPI := func() {
		if m != nil {
			m.Counter(counterFrontierDPIGuard).Add(1)
		}
	}
	var firstErr error
	// The port's source supplier VALIDATES before handing anything over. A SourceSet with a nil
	// seam is not a source: Begin would reach several frames deeper before failing, and a partially
	// wired composition root would look, at this call site, exactly like a working one. Validating
	// here means the frontier route is either backed by a real source or explicitly unavailable —
	// never quietly advancing over a stub.
	// A set whose only gap is the ledger travels WITH its reason, so the port can begin a draft
	// without negative knowledge while the project holds none (D49); any other gap is refused.
	advancer := checkpoint.NewFrontierAdvancer(w, func() (checkpoint.SourceSet, error) {
		resolved, err := src.Resolve()
		switch {
		case errors.Is(err, checkpoint.ErrNoLedger):
			return resolved, fmt.Errorf("%w: %w", err, core.ErrDegraded)
		case err != nil:
			return checkpoint.SourceSet{}, fmt.Errorf("%w: %w", err, core.ErrDegraded)
		}
		return resolved, nil
	})
	for _, s := range liveSessions(reg, w, src) {
		if ctx.Err() != nil {
			return firstNonNil(firstErr, ctx.Err())
		}
		// Unencoded already filters to Closed && !EncodedOnce, and it runs BEFORE Begin so that a
		// session with nothing to encode costs one read instead of a draft.
		segs, err := src.Segments.Unencoded(ctx, s)
		if err != nil {
			firstErr = firstNonNil(firstErr, err)
			continue
		}
		if len(segs) == 0 {
			continue
		}
		// Same discipline as the scheduler's own pass: ascending by turn, then the longest
		// leading run whose evidence the segment log can still vouch for. A gap or an in-flight
		// segment stops this session's advance where the evidence stops rather than encoding
		// past it — see verifyEvidence for why a frontier may not skip a span.
		closed := segs[:0:0]
		for _, seg := range segs {
			if seg.Closed {
				closed = append(closed, seg)
			}
		}
		slices.SortFunc(closed, func(a, b store.Segment) int { return cmp.Compare(a.StartTurn, b.StartTurn) })
		ids, dpi, stop := verifyEvidence(ctx, src.Segments, s, closed)
		if len(dpi) > 0 {
			// Unencoded offered these and the segment log's own re-read says another checkpoint
			// owns them. That is the §4.6 violation, and it is reported HERE rather than left to
			// the ErrAlreadyEncoded branch below, which never sees it: verification does not
			// resubmit an encoded segment, so with only that branch the sweep would drop the ids
			// and say nothing at all. Reporting it before the stop warn and before the empty-prefix
			// `continue` is what keeps it from being lost behind either.
			countDPI()
			log.Loud(msgDPIViolation, "session", string(s), "segments", segmentIDInts(dpi))
		}
		if stop != nil {
			// Counted as well as logged, and keyed by reason, exactly as the scheduler's own pass
			// counts it. A frontier held back by a gap is a condition an operator has to be able
			// to SEE without reading logs -- a log line alone is not an instrument -- and the two
			// passes reaching the same verification must report it through the same names or the
			// sweep's shortfalls are invisible wherever the scheduler is not the one advancing.
			if m != nil {
				m.Counter(counterFrontierUnverified).Add(1)
				m.Counter(counterFrontierUnverified + "." + stop.reason).Add(1)
			}
			log.Warn(msgUnverifiedEvidence,
				"session", string(s), "reason", stop.reason,
				"segment", int(stop.segment), "atTurn", int(stop.atTurn),
				"verified", len(ids), "backlog", len(closed))
		}
		if len(ids) == 0 {
			continue
		}
		if _, err := advancer.Advance(ctx, s, ids); err != nil {
			// The SECOND DPI guard, and the one that catches the narrow race verification cannot:
			// a segment that was genuinely unencoded when verifyEvidence re-read it and was
			// encoded by another writer before Advance reached it. A DPI violation -- the same
			// segment reachable from two checkpoints -- is the §4.6 invariant this whole layer
			// exists to enforce mechanically, so §16 requires it Loud and the ids dropped, not
			// folded into a sweep error that surfaces as an ordinary Warn. Folding it in also LOSES it: firstNonNil keeps only the first error of the
			// sweep, so a violation on a later session behind any earlier failure would never
			// reach a log line at all. The ids are already skipped inside Advance and the draft is
			// already persisted, so continuing is the documented handling, not a swallow.
			// The owner already retried one sealed-draft handoff. A repeated seal or another
			// failure remains a failed sweep result and may be retried by a later idle tick.
			if errors.Is(err, core.ErrAlreadyEncoded) {
				countDPI()
				log.Loud(msgDPIViolation, "session", string(s), "err", err.Error())
				continue
			}
			firstErr = firstNonNil(firstErr, err)
		}
	}
	return firstErr
}

// finalizeIfDue seals a draft when either §8.5 cadence condition holds: the draft already prices at
// or above checkpoint.budgetTokens, or it has absorbed cadenceSegmentThreshold segments since
// Begin. Finalize opens a successor draft with parent = the sealed seq, so advancement resumes on
// the next tick.
//
// The LOOP is cancellation-aware; Finalize itself is deliberately not, and the split is the point.
// §12's PreCompact-timeout row says finalize as-is, so a Finalize already under way must run to
// completion even on a dead context -- but each one costs an artifact write, a manifest append, a
// pins materialize and a successor Begin, and RunOnce measures the remaining idle budget only
// BETWEEN tasks. Without this check a tick with N due drafts performs N full finalizes whatever the
// budget said, starving every idle task queued behind the cadence.
func finalizeIfDue(ctx context.Context, cfg config.Config, w *checkpoint.FileWriter, noter LocalCheckpointNoter, m obs.Registry) error {
	budget := core.Tokens(cfg.Checkpoint.BudgetTokens)
	var firstErr error
	for _, s := range w.OpenDrafts() {
		if ctx.Err() != nil {
			return firstNonNil(firstErr, ctx.Err())
		}
		d := w.DraftFor(s)
		if d == nil {
			continue
		}
		full := budget > 0 && d.EstimatedTokens() >= budget
		enough := d.EncodedCount() >= cadenceSegmentThreshold
		if !full && !enough {
			continue
		}
		ref, err := w.Finalize(ctx, d, budget)
		if err != nil {
			firstErr = firstNonNil(firstErr, err)
			continue
		}
		// This seal was OURS. It is recorded and counted as a local checkpoint and never as a
		// compaction: the host did not act, its context window is untouched, and the Young-Daly
		// clock the scheduler measures the host by must not restart here.
		if m != nil {
			m.Counter(counterCadenceSeal).Add(1)
		}
		if noter != nil {
			noter.NoteLocalCheckpoint(ref.Seq)
		}
	}
	return firstErr
}

// metricsOf reports the registry d was constructed with, or nil for any other Daemon, mirroring
// daemonLog.
func metricsOf(d Daemon) obs.Registry {
	if dd, ok := d.(*daemon); ok {
		return dd.m
	}
	return nil
}

// liveSessions is every session the frontier may need advancing for, newest source first: the
// daemon's own live registry, then the writer's open drafts, then whatever the store remembers.
//
// The registry is the primary source and the only one that answers the question §16 actually asks.
// A session becomes interesting to this layer the moment it starts producing closed segments, and
// it stops being interesting when it ends — which is the exact opposite of what the store's session
// index records, since that is written on End and read by GC's retention. Enumerating from the
// store alone left advance_frontier with nothing to do for the entire life of every live session:
// it would begin advancing only after the session it was meant to amortize had already finished.
func liveSessions(reg *SessionRegistry, w *checkpoint.FileWriter, src checkpoint.SourceSet) []core.SessionID {
	seen := map[core.SessionID]bool{}
	out := []core.SessionID{}
	add := func(s core.SessionID) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}

	if reg != nil {
		for _, st := range reg.Snapshot() {
			if st.Live {
				add(st.ID)
			}
		}
	}
	for _, s := range w.OpenDrafts() {
		add(s)
	}
	if l, ok := src.Store.(sessionLister); ok {
		for _, s := range l.RecentSessions(maxTrackedSessions) {
			add(s)
		}
	}
	return out
}

// firstNonNil keeps the first error of a sweep while letting the sweep finish: one session's
// failure must not stop the others from advancing.
func firstNonNil(a, b error) error {
	if a != nil {
		return a
	}
	return b
}
