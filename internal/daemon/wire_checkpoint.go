package daemon

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
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
// the B-E histogram timing around the seam, SetPrecompactInstr, AddPrecompactWallSample — and
// therefore BOTH PreCompact contract assertions. The seam SP-05 built for exactly this is
// Options.Bind plus the nil-tolerant Services.PreCompact field, which handleCheckpoint calls.
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
func BindCheckpoint(o *Options, cfg config.Config, w *checkpoint.FileWriter, src checkpoint.SourceSet) {
	// Publish the seams to the writer before anything can call it. The cold PreCompact path -- a
	// compaction that fires before the first idle tick -- has no draft to take a SourceSet from,
	// and there is no reason to make it wait for one when the daemon holds it right here.
	w.SetSources(src)

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
			now := time.Now()
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
			res, err := w.PreCompact(ctx, checkpoint.PreCompactInput{
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
			if err != nil {
				return hookio.Empty(), err
			}
			return hookio.PreCompactOutput(res.Instructions), nil
		}
	})
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

type wireCfg struct{ noter LocalCheckpointNoter }

// ReportLocalCheckpointsTo routes the cadence's own seals to the scheduler runtime, which records
// them SEPARATELY from host compactions (see schedRuntime.NoteLocalCheckpoint). Without it the
// cadence still runs and is still counted here; what is lost is only the scheduler-side record.
func ReportLocalCheckpointsTo(n LocalCheckpointNoter) WireOption {
	return func(c *wireCfg) { c.noter = n }
}

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

	if cfg.Checkpoint.Frontier.AdvanceOnSegmentClose {
		idle.Register(idleTaskAdvanceFrontier, idlePrioAdvanceFrontier, func(ctx context.Context) error {
			return advanceAllSessions(ctx, d.Registry(), w, src, log)
		})
	}

	idle.Register(idleTaskCheckpointCadence, idlePrioCheckpointCadence, func(ctx context.Context) error {
		return finalizeIfDue(ctx, cfg, w, wc.noter, metricsOf(d))
	})

	idle.Register(idleTaskMaterializePins, idlePrioMaterializePins, func(ctx context.Context) error {
		if src.Pins == nil {
			return nil
		}
		return src.Pins.Materialize(ctx)
	})
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
func advanceAllSessions(ctx context.Context, reg *SessionRegistry, w *checkpoint.FileWriter, src checkpoint.SourceSet, log logging.Logger) error {
	if log == nil {
		log = logging.Nop()
	}
	var firstErr error
	// The port's source supplier VALIDATES before handing anything over. A SourceSet with a nil
	// seam is not a source: Begin would reach several frames deeper before failing, and a partially
	// wired composition root would look, at this call site, exactly like a working one. Validating
	// here means the frontier route is either backed by a real source or explicitly unavailable —
	// never quietly advancing over a stub.
	advancer := checkpoint.NewFrontierAdvancer(w, func() (checkpoint.SourceSet, error) {
		if err := src.Validate(); err != nil {
			return checkpoint.SourceSet{}, fmt.Errorf("%w: %w", err, core.ErrDegraded)
		}
		return src, nil
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
		ids, stop := verifyEvidence(ctx, src.Segments, s, closed)
		if stop != nil {
			log.Warn(msgUnverifiedEvidence,
				"session", string(s), "reason", stop.reason,
				"segment", int(stop.segment), "atTurn", int(stop.atTurn),
				"verified", len(ids), "backlog", len(closed))
		}
		if len(ids) == 0 {
			continue
		}
		if _, err := advancer.Advance(ctx, s, ids); err != nil {
			// A DPI violation -- the same segment reachable from two checkpoints -- is the §4.6
			// invariant this whole layer exists to enforce mechanically, so §16 requires it Loud
			// and the ids dropped, not folded into a sweep error that surfaces as an ordinary
			// Warn. Folding it in also LOSES it: firstNonNil keeps only the first error of the
			// sweep, so a violation on a later session behind any earlier failure would never
			// reach a log line at all. The ids are already skipped inside Advance and the draft is
			// already persisted, so continuing is the documented handling, not a swallow.
			// The owner already retried one sealed-draft handoff. A repeated seal or another
			// failure remains a failed sweep result and may be retried by a later idle tick.
			if errors.Is(err, core.ErrAlreadyEncoded) {
				log.Loud("checkpoint: DPI violation: segments are already encoded by another checkpoint and were skipped",
					"session", string(s), "err", err.Error())
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
