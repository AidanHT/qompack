package daemon

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/hostperm"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/rehydrate"
	"github.com/qompack/qompack/internal/rules"
	"github.com/qompack/qompack/internal/skills"
	"github.com/qompack/qompack/internal/tokens"
)

// RehydrateOptions is the dependency set NewRehydrateService reads. Every member is optional: a
// nil member means "that source is unavailable", which degrades the payload and is reported in the
// drop report rather than failing the hook (Qompack.md §12.3).
type RehydrateOptions struct {
	ProjectRoot string
	// Cfg is the configuration the service reads when CfgFn is nil.
	Cfg config.Config
	// CfgFn, when set, supplies the configuration at each use instead of Cfg. The daemon wiring
	// passes its live configuration (Options.CurrentCfg), so a reloaded runtime.rehydrate budget is
	// the budget the next compaction is built under rather than the one the daemon started with
	// (V6 close-out D49; the candidate 4 live re-run's UAT-05).
	CfgFn func() config.Config
	// Checkpoints is the L4 reader. A nil reader, or one whose Latest reports ErrNotFound or
	// ErrNotImplemented, is the no-checkpoint path: the payload is still built from the L0
	// verbatim capture, the ledger and the skill index, and Result.Degraded is set.
	Checkpoints checkpoint.Reader
	Deps        rehydrate.Deps
	Reporter    rehydrate.Reporter
	// Contract supplies the degradation mode when Mode is nil. Tests construct a Monitor directly;
	// the daemon wiring cannot, because contract.NewMonitor runs inside New — after WireObserver
	// has already had to return — so it uses Mode instead.
	Contract contract.Monitor
	// Mode is the late-bound degradation source, filled from Services.Mode by a Bind body. It wins
	// over Contract when both are set. Both nil means ModeFull.
	Mode func() contract.Mode
	// OpenLedger opens the negative-knowledge ledger the FIRST time a compaction actually needs it,
	// and is consulted only while Deps.Ledger is nil.
	//
	// It is a function rather than a handle because opening the ledger is not free of observable
	// effect: negknow.Open loads or rebuilds sketches/tried.bloom, and §3.3 gives that file to the
	// ledger alone — two inherited e2e cases assert it does not exist after the observer has been
	// wired and run. Opening eagerly at wire time would create it in every daemon that never
	// compacts, and would hold an append handle on records/eliminations.jsonl for the process
	// lifetime, which on Windows is enough to fail an unrelated test's directory cleanup.
	OpenLedger func() negknow.Ledger
	Log        logging.Logger
	// Metrics receives rehydrate.build timings and the tokens/dropped/items gauges.
	Metrics obs.Registry
	// Clock stamps State.Emitted. It is the ONLY wall clock in L5: rehydrate.Build reads none.
	Clock core.Clock
}

// rehydrateService implements observer.Rehydrator: the SessionStart compact and clear branches
// (00-ARCHITECTURE.md §5.21, Qompack.md §7.3, §8.6).
type rehydrateService struct {
	o RehydrateOptions

	// mu guards the lazily opened ledger. OnCompact runs on the daemon's worker pool, so two
	// concurrent compactions must not race to open two handles on one JSONL.
	mu         sync.Mutex
	ledgerOnce bool
	ledger     negknow.Ledger

	// tier1Loud names the sessions that have logged a tier-1 overflow Loud (D50: once per
	// session, not on every compaction). Guarded by mu. It holds one entry per session this
	// daemon has seen overflow, and a daemon serves one project's sessions until it idles out.
	// It is not persisted, so the rule is once per session per daemon (ADR 0011 §23.3): a daemon
	// started after another one ended logs a session's overflow Loud once more (F-C7-UAT05-2).
	tier1Loud map[core.SessionID]bool
}

// tier1Reported reports whether sess has already logged a tier-1 overflow Loud.
func (s *rehydrateService) tier1Reported(sess core.SessionID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tier1Loud[sess]
}

// noteTier1 records that sess has logged a tier-1 overflow Loud.
func (s *rehydrateService) noteTier1(sess core.SessionID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tier1Loud == nil {
		s.tier1Loud = make(map[core.SessionID]bool)
	}
	s.tier1Loud[sess] = true
}

// NewRehydrateService returns the observer.Rehydrator the SessionStart source switch delegates to.
// It is nil-tolerant at every call site and never returns nil.
func NewRehydrateService(o RehydrateOptions) observer.Rehydrator {
	if o.Log == nil {
		o.Log = logging.Nop()
	}
	if o.Clock == nil {
		o.Clock = core.SystemClock()
	}
	return &rehydrateService{o: o}
}

// cfg is the configuration in effect now: the live supplier's when one is wired, Cfg otherwise.
func (s *rehydrateService) cfg() config.Config {
	if s.o.CfgFn != nil {
		return s.o.CfgFn()
	}
	return s.o.Cfg
}

var _ observer.Rehydrator = (*rehydrateService)(nil)

// mode reports the degradation mode in force. When in doubt it reports ModeFull only if no source
// was configured at all; a configured source always wins.
func (s *rehydrateService) mode() contract.Mode {
	switch {
	case s.o.Mode != nil:
		return s.o.Mode()
	case s.o.Contract != nil:
		return s.o.Contract.Mode()
	default:
		return contract.ModeFull
	}
}

// OnCompact handles SessionStart with source "compact".
func (s *rehydrateService) OnCompact(ctx context.Context, e observer.Event) (out observer.Output, err error) {
	// §13 invariant 6: a hook exits 0 however badly it goes. A panic anywhere below — a fake
	// reader, a malformed checkpoint, a nil map in a builder — becomes the deferred note and a Loud,
	// never a non-zero exit that blocks the user's session from starting. A panic is a failed build,
	// so it is answered the way every failed build is (notBuilt, owner decision D11): the
	// session.start route waiting on this rehydration is answered now, with the note, rather than
	// with an empty output that says nothing or a wait that runs out its bound.
	defer func() {
		if v := recover(); v != nil {
			s.o.Log.Loud("rehydrate: panic recovered", "session", string(e.SessionID), "err", fmt.Sprint(v))
			out, err = s.notBuilt(ctx, e.SessionID, DeferredFailed, notBuiltBuildFailed), nil
		}
	}()

	// The observer's own SessionStart call, made beside a rehydration the session.start route is
	// already building (session_start_compact.go): that call is the observer's bookkeeping, and a
	// second rehydration here would only be thrown away.
	if routeRehydrates(ctx) {
		return hookio.Empty(), nil
	}

	// Every ledger read below — the selection's candidates and the build's item 3 — is made for
	// the session being rehydrated: the daemon's one ledger serves every session of the project,
	// and only this session's session-scoped eliminations belong in its block.
	ctx = negknow.WithCaller(ctx, negknow.Caller{Session: e.SessionID})

	// §12.1 is explicit: in ModeDegradedPassive there is "no additionalContext injection, no
	// customInstructions, no scheduler-initiated checkpoints, no drop report". L0/L1 keep running;
	// everything that ACTS is off. Getting this wrong would make the degradation doctrine a lie.
	if m := s.mode(); m != contract.ModeFull {
		s.o.Log.Debug("rehydrate: not acting", "session", string(e.SessionID), "mode", m.String())
		return hookio.Empty(), nil
	}

	// Qompack.md v1.5 Appendix C's independent injection kill switch (SP-19): recording is
	// untouched — runtime.mode governs that — and nothing is reinjected. Checked before the
	// checkpoint read for the same reason the mode is: work §12.1 forbids is not paid for first.
	cfg := s.cfg()
	if !cfg.Runtime.Migration.Reinjection.SessionStartCompact {
		s.o.Log.Info("rehydrate: injection disabled by runtime.migration.reinjection.sessionStartCompact",
			"session", string(e.SessionID))
		return hookio.Empty(), nil
	}

	var (
		cp       checkpoint.Checkpoint
		ref      checkpoint.Ref
		degraded error
	)
	s.phase(histRehydrateLatest, func() { cp, ref, degraded = s.latest(ctx, e.SessionID) })
	switch degraded {
	case errFatalCheckpoint:
		// Nothing is built on a store that cannot be read — a half-known context would be worse than
		// none — but the compaction is still answered: with the note, not silence (D11).
		return s.notBuilt(ctx, e.SessionID, DeferredCheckpointUnreadable, notBuiltCheckpointUnreadable), nil
	case errCheckpointCutShort:
		return s.stopped(ctx, e.SessionID, "reading the checkpoint"), nil
	}

	budget := core.Tokens(cfg.Runtime.Rehydrate.MaxTokens)
	req := rehydrate.Request{
		Session:     e.SessionID,
		Source:      e.Source,
		ProjectRoot: s.o.ProjectRoot,
		Budget:      budget,
		Checkpoint:  cp,
		Ref:         ref,
		Cfg:         cfg,
		Lineage:     s.lineage(e.SessionID),
		// A tier-1 overflow is Loud once per session; the payload names it every time (D50).
		Tier1OverflowReported: s.tier1Reported(e.SessionID),
	}

	var res rehydrate.Result
	var stats []rehydrate.ItemStat
	var buildErr error
	var deps rehydrate.Deps
	s.phase(histRehydrateDeps, func() { deps = s.deps() })

	// SP-15's representation selection, run HERE rather than inside Build. rehydrate may not
	// import analyzer (§3.2) and Build is a pure function of (Request, Deps), so the composition
	// root runs the selector and passes the outcome in as request data. A nil outcome — selection
	// disabled, no candidates, or any error — is the shipped pre-SP-15 path exactly.
	s.phase(histRehydrateSelection, func() { req.Selection = s.selectionFor(ctx, cp, budget, deps.Ledger) })

	s.timed(func() { res, stats, buildErr = rehydrate.BuildWithStats(ctx, req, deps) })
	if buildErr != nil {
		if cutShort(ctx, buildErr) {
			return s.stopped(ctx, e.SessionID, "building the rehydration"), nil
		}
		s.o.Log.Loud("rehydrate: build failed", "session", string(e.SessionID), "err", buildErr.Error())
		return s.notBuilt(ctx, e.SessionID, DeferredFailed, notBuiltBuildFailed), nil
	}
	if degraded == errNoCheckpoint {
		res.Degraded = true
	}
	if res.Tier1Overflow {
		s.noteTier1(e.SessionID)
	}

	// An empty additionalContext is noise: the host would inject a blank block and the §12.1 probe
	// would still be appended by the session.start route regardless.
	out = hookio.Empty()
	if res.Text != "" {
		out = hookio.Output{HookSpecificOutput: &hookio.HSO{
			HookEventName:     "SessionStart",
			AdditionalContext: res.Text,
		}}
	}

	// The answer is handed to a waiting session.start route BEFORE the drop report is written, so
	// that write (an atomic replace, two fsyncs) is never on the answer's path (C1.16). If the
	// route has already answered without it, what was built never reached the model, and the drop
	// report must say so rather than describe it as delivered.
	if t := compactTicketFrom(ctx); t != nil && !t.offer(out) && res.Text != "" {
		why := t.undeliveredReason()
		s.phase(histRehydrateRecord, func() { s.recordUndelivered(ctx, e.SessionID, res, budget, ref, why) })
		return out, nil
	}

	s.phase(histRehydrateRecord, func() { s.record(ctx, e.SessionID, res, stats, budget) })
	s.gauges(res)
	return out, nil
}

// deps returns the collaborator set for this build, resolving the lazily opened ledger.
//
// A ledger supplied in Deps wins outright: tests wire a fake, and the daemon path leaves that
// field nil precisely so the open happens here, on the first compaction, rather than at wire time.
// An open failure is Loud and then permanent for the process — retrying per compaction would turn
// one broken ledger into an unbounded log — and item 3 degrades to the checkpoint's own copy.
func (s *rehydrateService) deps() rehydrate.Deps {
	d := s.o.Deps
	if d.Ledger != nil || s.o.OpenLedger == nil {
		return d
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ledgerOnce {
		s.ledgerOnce = true
		s.ledger = s.o.OpenLedger()
	}
	d.Ledger = s.ledger
	return d
}

// OnClear handles SessionStart with source "clear".
//
// It resets the rehydration state file so the next compact injection is a fresh full payload
// rather than one carrying a stale drop report — and it touches NOTHING else. A /clear is not a
// session end: session-scoped eliminations "die with the session" (§8.3) and the session has not
// ended, so the ledger, the store, the DAG, the sketches and the checkpoints are all left alone.
// SP-08 owns whatever session-registry effects a clear has.
func (s *rehydrateService) OnClear(ctx context.Context, e observer.Event) (out observer.Output, err error) {
	defer func() {
		if v := recover(); v != nil {
			s.o.Log.Loud("rehydrate: panic recovered on clear", "session", string(e.SessionID), "err", fmt.Sprint(v))
			out, err = hookio.Empty(), nil
		}
	}()

	if s.o.Reporter != nil {
		if rerr := s.o.Reporter.Reset(ctx, e.SessionID); rerr != nil {
			s.o.Log.Warn("rehydrate: could not reset state on clear",
				"session", string(e.SessionID), "err", rerr.Error())
		}
	}
	s.o.Log.Info("rehydrate: session cleared; rehydration state reset", "session", string(e.SessionID))

	// Returned verbatim by observer.OnSessionStart's source switch, so it must be the empty output
	// and a nil error: a clear injects nothing.
	return hookio.Empty(), nil
}

// The checkpoint outcomes latest distinguishes besides a checkpoint: none to read, a store that
// cannot be read, and a read its own context cut short (cutShort), which is no fault of the store.
var (
	errNoCheckpoint       = errors.New("rehydrate: no checkpoint")
	errFatalCheckpoint    = errors.New("rehydrate: checkpoint unreadable")
	errCheckpointCutShort = errors.New("rehydrate: checkpoint read cut short")
)

// cutShort reports whether err is ctx's own ending rather than a fault of the work that returned it:
// the shipped checkpoint reader and rehydrate.BuildWithStats both return ctx.Err() once ctx has ended.
//
// In the daemon a rehydration's context ends only when Stop cancels it: startReplyWork runs it under
// a context that keeps none of the request's cancellation or deadline and is cancelled through
// promptCtx alone. So a rehydration cut short is one the daemon stopping took away (stopped), and
// never a store to be reported unreadable or a build that failed.
func cutShort(ctx context.Context, err error) bool {
	cerr := ctx.Err()
	return cerr != nil && errors.Is(err, cerr)
}

// latest resolves the checkpoint to rehydrate from.
//
// ErrNotFound and ErrNotImplemented are the SAME case: "there is no checkpoint to read". The
// second matters on this branch specifically — checkpoint.OpenReader returns SP-01's stub until
// SP-10 merges ahead of SP-11 in the wave-3 order, and treating its ErrNotImplemented as fatal
// would make the compact branch emit nothing at all until then, hiding every integration defect
// this slice is supposed to surface. Both produce a zero Checkpoint and Ref{Seq:0}, from which
// items 2, 3, 6b, 7 and 8 still build a useful payload.
func (s *rehydrateService) latest(ctx context.Context, sess core.SessionID) (checkpoint.Checkpoint, checkpoint.Ref, error) {
	if s.o.Checkpoints == nil {
		s.o.Log.Info("rehydrate: no checkpoint reader; building from L0 and the ledger", "session", string(sess))
		return checkpoint.Checkpoint{}, checkpoint.Ref{}, errNoCheckpoint
	}
	cp, ref, err := s.o.Checkpoints.Latest(ctx, sess)
	switch {
	case err == nil:
		s.rolledBack(sess, ref)
		return cp, ref, nil
	case errors.Is(err, core.ErrNotFound), errors.Is(err, core.ErrNotImplemented):
		s.o.Log.Info("rehydrate: no checkpoint for session; building from L0 and the ledger",
			"session", string(sess), "err", err.Error())
		// What Latest refused is kept: a store whose every checkpoint failed verification is not a
		// project that never had one, and the payload says which (D49).
		refused := checkpoint.Ref{Refused: ref.Refused}
		s.rolledBack(sess, refused)
		return checkpoint.Checkpoint{}, refused, errNoCheckpoint
	case cutShort(ctx, err):
		// The read stopped because its context ended, not because the store failed it: OnCompact
		// reports the daemon stopping (stopped), and the store is not Loud'd as unreadable.
		return checkpoint.Checkpoint{}, checkpoint.Ref{}, errCheckpointCutShort
	default:
		s.o.Log.Loud("rehydrate: checkpoint unreadable", "session", string(sess), "err", err.Error())
		return checkpoint.Checkpoint{}, checkpoint.Ref{}, errFatalCheckpoint
	}
}

// rolledBack Louds a checkpoint fallback: the reader stepped over a newer checkpoint that does not
// verify, and this rehydration is built from ref (or from no checkpoint). The reader's own Loud
// names the artifact that failed; this one names what the session was rolled back to, which is
// what F-C4-UAT03-1's LOUD.log left out (D49). The message carries the whole reason so the one
// line a reader of LOUD.log sees says both halves.
func (s *rehydrateService) rolledBack(sess core.SessionID, ref checkpoint.Ref) {
	if len(ref.Refused) == 0 {
		return
	}
	to := "no checkpoint"
	if ref.Seq != 0 {
		to = fmt.Sprintf("%04d", int(ref.Seq))
	}
	s.o.Log.Loud("rehydrate: newest checkpoint refused; rolled back to "+to,
		"session", string(sess), "refused", fmt.Sprint(ref.Refused), "rolled_back_to", to)
}

// record persists the drop report. A write failure is logged and swallowed: the payload has
// already been built and is worth emitting, and `dropped()` degrades to the previous state file.
func (s *rehydrateService) record(ctx context.Context, sess core.SessionID, res rehydrate.Result, stats []rehydrate.ItemStat, budget core.Tokens) {
	if s.o.Reporter == nil {
		return
	}
	st := rehydrate.State{
		Session: sess,
		Seq:     res.Seq,
		// The only wall-clock read in L5. Build is a pure function of (Request, Deps) precisely so
		// that PropBuild_Deterministic can demand byte-identical output for identical input.
		Emitted:  core.UnixMilli(s.o.Clock.Now().UnixMilli()),
		Tokens:   res.Tokens,
		Budget:   budget,
		Items:    stats,
		Dropped:  res.Dropped,
		Degraded: res.Degraded,
		// Why, when the drop report alone would not make it plain (a checkpoint fallback, D49).
		DegradedReason: res.DegradedReason,
	}
	if err := s.o.Reporter.Record(ctx, sess, st); err != nil {
		s.o.Log.Warn("rehydrate: could not record state", "session", string(sess), "err", err.Error())
	}
}

// undeliveredDropKind is the DropEntry kind of a whole rehydration that never reached the model:
// one built but never delivered (recordUndelivered), or one that could not be built (recordNotBuilt).
const undeliveredDropKind = "rehydration"

// Why a compaction's rehydration was never built, as its drop report's first entry says
// (recordNotBuilt), what the model received instead, and the entry's id.
const (
	notBuiltCheckpointUnreadable = "not delivered: the checkpoint store could not be read, so no rehydration " +
		"was built"
	notBuiltBuildFailed = "not delivered: building the rehydration failed"
	notBuiltStopping    = "not delivered: the Qompack daemon was shutting down, so no rehydration was built"
	// notBuiltAnswered is what the model received when the daemon answered the compaction itself: its
	// deferred note, on time or — when the route had already answered at its bound — earlier.
	notBuiltAnswered = "the model received a deferred note instead"
	notBuiltDropID   = "not-built"
)

// notBuilt is the answer to a compaction whose rehydration could not be built — the checkpoint store
// could not be read, the build failed or panicked, or the daemon stopping cut it short (stopped).
// Owner decision D11: such a compaction is answered with the explicit deferred note naming reason,
// exactly as a late one is (session_start_compact.go), never with silence, which would leave the
// model with a compacted context and no word of what it lost. A session.start route waiting on this
// rehydration is answered first, through its ticket, so the route counts and Louds the deferral like
// any other; then the drop report is recorded as never built (recordNotBuilt), off the answer's path,
// so dropped() does not describe an earlier rehydration as this one. Degraded-passive and the
// reinjection kill switch are checked before anything that can fail, so neither ever reaches here:
// they still answer nothing.
//
// What the model received instead is the daemon's note, except for a compaction replayed from a hook's
// spool (its ticket abandoned with undeliveredReplayed): that hook answered without the daemon, with
// the hook client's own note or with nothing, and the report says so rather than claim a note it
// cannot know was delivered.
func (s *rehydrateService) notBuilt(ctx context.Context, sess core.SessionID, reason, why string) hookio.Output {
	received := notBuiltAnswered
	if t := compactTicketFrom(ctx); t != nil {
		t.fail(reason)
		if t.undeliveredReason() == undeliveredReplayed {
			received = strings.TrimPrefix(undeliveredReplayed, undeliveredPrefix)
		}
	}
	s.phase(histRehydrateRecord, func() { s.recordNotBuilt(ctx, sess, why+"; "+received) })
	return compactDeferredOutput(sess, reason)
}

// stopped is notBuilt for a rehydration the daemon stopping cut short while it was doing what
// (cutShort): the note and the drop report name that cause (DeferredStopping, notBuiltStopping),
// never an unreadable store or a failed build. It is Loud once, as every compaction answered without
// its rehydration is (§13 invariant 10).
func (s *rehydrateService) stopped(ctx context.Context, sess core.SessionID, what string) hookio.Output {
	s.o.Log.Loud("rehydrate: stopped before the rehydration was built", "session", string(sess), "while", what)
	return s.notBuilt(ctx, sess, DeferredStopping, notBuiltStopping)
}

// recordNotBuilt persists the drop report of a compaction whose rehydration was never built: one
// entry for the whole rehydration, saying so and why, and where the checkpoints are kept. It lists no
// emitted items and no tokens, because nothing was emitted, and it is marked degraded.
//
// It records under a context that keeps ctx's values but not its cancellation: the rehydration may
// not have been built BECAUSE its context was cancelled (the daemon stopping, stopped), and that
// compaction is one dropped() must describe all the same. The write is one small atomic replace, and
// Stop joins it with the rest of the reply work (startReplyWork). A failed write is warned and
// swallowed, like record's.
func (s *rehydrateService) recordNotBuilt(ctx context.Context, sess core.SessionID, why string) {
	if s.o.Reporter == nil {
		return
	}
	st := rehydrate.State{
		Session: sess,
		Emitted: core.UnixMilli(s.o.Clock.Now().UnixMilli()),
		Budget:  core.Tokens(s.cfg().Runtime.Rehydrate.MaxTokens),
		Dropped: []checkpoint.DropEntry{{
			Kind: undeliveredDropKind, ID: notBuiltDropID,
			Detail: why + "; the checkpoints are kept in .qompack/checkpoints/ (the highest-numbered file is the newest)",
		}},
		Degraded: true,
	}
	if err := s.o.Reporter.Record(context.WithoutCancel(ctx), sess, st); err != nil {
		s.o.Log.Warn("rehydrate: could not record state", "session", string(sess), "err", err.Error())
	}
}

// recordUndelivered is record for a rehydration that never reached the model
// (session_start_compact.go): the session.start route answered without it, or it was built for a
// request replayed from a hook's spool after the hook had answered. why says which (undeliveredLate,
// undeliveredReplayed). The drop report therefore leads with one entry for the whole rehydration,
// pointing at where its content can still be read, followed by what the payload would have left out
// anyway; it lists no emitted items and no tokens, because nothing was emitted, and it is marked
// degraded. dropped() then tells the truth about the compaction instead of describing a payload as
// if the model had it.
func (s *rehydrateService) recordUndelivered(ctx context.Context, sess core.SessionID, res rehydrate.Result,
	budget core.Tokens, ref checkpoint.Ref, why string,
) {
	if s.o.Reporter == nil {
		return
	}
	detail := why
	if detail == "" {
		detail = undeliveredLate
	}
	id := "no-checkpoint"
	if ref.Seq > 0 {
		id = fmt.Sprintf("checkpoint-%04d", int(ref.Seq))
	}
	if p := s.projectRelative(ref.Path); p != "" {
		detail += "; restore: Read " + p
	}
	dropped := append([]checkpoint.DropEntry{{Kind: undeliveredDropKind, ID: id, Detail: detail}}, res.Dropped...)
	st := rehydrate.State{
		Session:  sess,
		Seq:      res.Seq,
		Emitted:  core.UnixMilli(s.o.Clock.Now().UnixMilli()),
		Budget:   budget,
		Dropped:  dropped,
		Degraded: true,
	}
	if err := s.o.Reporter.Record(ctx, sess, st); err != nil {
		s.o.Log.Warn("rehydrate: could not record state", "session", string(sess), "err", err.Error())
	}
}

// projectRelative renders p relative to the project root in slash form when it lies inside it (the
// form rehydrate's own restore pointers use), and p itself otherwise; "" stays "".
func (s *rehydrateService) projectRelative(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || s.o.ProjectRoot == "" {
		return p
	}
	rel, err := filepath.Rel(s.o.ProjectRoot, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return p
	}
	return filepath.ToSlash(rel)
}

// The per-phase histograms of one compact rehydration, beside rehydrate.build (the Build call
// itself). Together with the session.start route's own phases (handlers.go) they say where a slow
// SessionStart(source=compact) spent its time: C1.16's 10.3 s answer could not be attributed from
// the evidence the daemon kept, because only the whole Build was timed.
const (
	histRehydrateLatest    = "rehydrate.latest"
	histRehydrateDeps      = "rehydrate.deps"
	histRehydrateSelection = "rehydrate.selection"
	histRehydrateRecord    = "rehydrate.record"
)

// phase runs f and records its wall time under name when metrics are wired.
func (s *rehydrateService) phase(name string, f func()) {
	if s.o.Metrics == nil {
		f()
		return
	}
	start := time.Now()
	f()
	s.o.Metrics.Hist(name).Observe(time.Since(start))
}

// timed runs f under the rehydrate.build histogram when metrics are wired.
func (s *rehydrateService) timed(f func()) {
	if s.o.Metrics == nil {
		f()
		return
	}
	// obs.Timed takes a func() error and returns it; this call site has nothing to report, so the
	// error is discarded rather than invented.
	_ = obs.Timed(s.o.Metrics.Hist("rehydrate.build"), func() error {
		f()
		return nil
	})
}

// gauges publishes the three §11.2 observables.
func (s *rehydrateService) gauges(res rehydrate.Result) {
	if s.o.Metrics == nil {
		return
	}
	s.o.Metrics.Gauge("rehydrate.tokens").Set(int64(res.Tokens))
	s.o.Metrics.Gauge("rehydrate.dropped").Set(int64(len(res.Dropped)))
	s.o.Metrics.Gauge("rehydrate.items").Set(int64(len(res.Items)))
}

// BindRehydrate installs svc into the late-bound Services set through the Bind seam SP-05 already
// provides, adding NO member to daemon.Services.
//
// Setting Services.Rehydrate is what makes DeclareProducers declare contract.CAdditionalContext,
// and Services.Rehydrate is referenced nowhere else in this package — it is the seam SP-05
// provisioned for exactly this branch. Until something sets it, §12.1's
// hook.additional_context_delivered assertion reports "not-yet-implemented" forever.
//
// observer.Event and observer.Output are aliases of hookio.Event and hookio.Output, so
// svc.OnCompact satisfies the Services.Rehydrate func type directly.
func BindRehydrate(o *Options, svc observer.Rehydrator) {
	if o == nil || svc == nil {
		return
	}
	o.Bind(func(s *Services) { s.Rehydrate = svc.OnCompact })
}

// WireRehydrator builds the L5 service from a daemon Options and binds it.
//
// This is the wave-3 daemon bootstrap block. It deliberately REUSES o.Store and o.Graph rather
// than opening its own: a second store.Store handle on one project root is a corruption bug, not a
// redundancy. WireObserver opens both lazily just above this call, so by the time this runs they
// exist; anything still nil is passed through as nil and degrades that item.
//
// It adds the two handles nothing else opens yet — the negative-knowledge ledger and the
// checkpoint reader — and leaves each nil on failure, logging Loud. SP-12 reads these same fields
// and adds nothing; SP-13 extends this block with the MCP promoter and op registration.
func WireRehydrator(o *Options) observer.Rehydrator {
	// Wiring time, on the goroutine that owns this Options and before anything can read the
	// handle: the cell the opener publishes into must exist before the opener does.
	o.ensureLedgerCell()
	// Likewise the live configuration's cell, which the service and the ledger read (config_live.go).
	o.ensureLiveConfig()
	log := o.Log
	if log == nil {
		log = logging.Nop()
	}
	clk := o.Clock
	if clk == nil {
		clk = core.SystemClock()
	}

	// The ledger is opened on the FIRST compaction, not here. See RehydrateOptions.OpenLedger: an
	// eager open creates sketches/tried.bloom in every daemon that never compacts, which §3.3
	// reserves for the ledger itself, and holds a records/eliminations.jsonl handle for the process
	// lifetime. The handle is assigned back onto Options so a composition root can see it, and
	// registered on Options.OnStop so the DAEMON closes it -- see the owned-resource note inside
	// the opener.
	//
	// It is published on Options.OpenLedger rather than kept local, because the rehydration is no
	// longer the first thing in a compaction that needs a ledger: the PreCompact hook fires BEFORE
	// the SessionStart(source=compact) this service handles, and the checkpoint it seals reads
	// eliminations out of the same ledger. Sharing the accessor is what keeps negknow.Open at ONE
	// call site while letting either half of a compaction be the one that triggers it. The
	// sync.Once is the sharing rule: one open, one Loud on failure, no retry, whichever worker
	// arrives first.
	//
	// owned is taken HERE, at wiring time, and not from inside the closure: it must exist before
	// New copies Options, or the closer the opener registers seconds later would land on a list
	// the constructed daemon never saw.
	owned := o.ownedResources()
	var (
		ledgerOnce sync.Once
		ledger     negknow.Ledger
	)
	openLedger := func() negknow.Ledger {
		ledgerOnce.Do(func() {
			// A ledger the CALLER put on Options is already open on this project's
			// records/eliminations.jsonl, and opening a second handle beside it is the same
			// corruption class a second store.Open is -- two appenders racing one another's
			// offsets on one log, and two owners of one sketches/tried.bloom. So it is ADOPTED,
			// not duplicated: the accessor answers with it, and nothing is registered for close,
			// because that handle belongs to whoever supplied it. s.deps() applies the same rule
			// one layer down.
			if existing := o.LedgerHandle(); existing != nil {
				ledger = existing
				return
			}
			// Opened with the configuration in effect NOW, and reading the live one after
			// (Deps.Config): the ledger applies eliminations.* itself, and a reload between the
			// daemon's start and the first compaction, or after it, must reach it. Ancestry reads the
			// lineage records, so a fork's already_tried and rehydration see its parent's
			// session-scoped eliminations up to the fork point (D49, F-C4-UAT06-1).
			l, err := negknow.Open(o.ProjectRoot, o.CurrentCfg(), nil, negknow.Deps{
				Store: o.Store, Graph: o.Graph, Log: log, Metrics: o.Metrics, Clock: clk,
				Config:   o.CurrentCfg,
				Ancestry: checkpoint.LedgerAncestry(o.ProjectRoot),
			})
			if err != nil {
				log.Loud("daemon: negative-knowledge ledger unavailable; eliminations will not be rehydrated",
					"err", err.Error())
				return
			}
			// Published through the synchronized cell, NOT onto the Ledger field: the three
			// live accessors (liveLedger, the scheduler's LedgerFn, the checkpoint SourceSet
			// supplier) read it from per-connection goroutines, and sync.Once orders only the
			// goroutines that call Do. See Options.publishLedger.
			o.publishLedger(l)
			ledger = l
			// THIS handle has no other owner: nothing but this closure knows it exists until the
			// assignment above, and the assignment is to a field a composition root is free never
			// to read. Registering it here is what makes "the daemon closes what the daemon
			// opened" true for every embedder of New rather than for the one that happens to
			// carry a defer.
			owned.add("the negative-knowledge ledger", l.Close)
		})
		return ledger
	}
	// A caller that supplied its own accessor keeps it: tests wire a fake ledger this way, and
	// overwriting it here would open a real one beside it.
	if o.OpenLedger == nil {
		o.OpenLedger = openLedger
	}

	ckpt, err := checkpoint.OpenReader(o.ProjectRoot, log, o.Metrics)
	if err != nil {
		log.Loud("daemon: checkpoint reader unavailable; rehydration will run without a checkpoint",
			"err", err.Error())
		ckpt = nil
	}

	svc := NewRehydrateService(RehydrateOptions{
		ProjectRoot: o.ProjectRoot,
		Cfg:         o.Cfg,
		CfgFn:       o.CurrentCfg,
		Checkpoints: ckpt,
		OpenLedger:  o.OpenLedger,
		Deps: rehydrate.Deps{
			Store: o.Store,
			// o.Ledger is nil on the daemon path — nothing opens one before this — so the compact
			// branch routes through OpenLedger above. It is non-nil only when a CALLER supplied a
			// ledger on Options, and s.deps() honours that one outright rather than opening a
			// second handle on the same JSONL.
			Ledger: o.Ledger,
			Graph:  o.Graph,
			Rules:  rules.New(rules.WithLogger(log)),
			Skills: skills.New(skills.WithLogger(log)),
			Tokens: tokens.NewForProject(o.Cfg, tokens.DefaultCalibPath(), o.ProjectRoot),
			Log:    log,
			// Section 6 never shows a path re_read would refuse (D50): the same host rules.
			HostPaths: rehydrateHostPaths(hostPolicyFor(o), o.ProjectRoot, log),
		},
		Reporter: rehydrate.NewReporter(o.ProjectRoot, log),
		Log:      log,
		Metrics:  o.Metrics,
		Clock:    clk,
	})
	BindRehydrate(o, svc)
	return svc
}

// hostPolicyFor is o.HostPolicy, or the machine's own rules for the project when none is supplied —
// the default internal/mcp builds for re_read and expand, so the two judge a path alike.
func hostPolicyFor(o *Options) *hostperm.Policy {
	if o.HostPolicy != nil {
		return o.HostPolicy
	}
	return hostperm.New(hostperm.Options{ProjectRoot: o.ProjectRoot})
}

// rehydrateHostPaths adapts the host's permission policy to rehydrate.HostPaths: one rule snapshot
// per build, and a path refused when a Read deny or ask rule matches it (an archived rehydration
// cannot ask), judged as recorded and as the project's resolved root spells it — the two spellings
// internal/mcp's authorizeHost judges. Rules that cannot be established hand rehydrate no Refuses,
// and it then withholds every path and every free-text summary, as re_read withholds path-bearing
// content (fail closed).
//
// The snapshot also hands rehydrate every rule's path specifier (RuleSet.ReadRulePatterns), which
// its free-text screen reads with no host judgement (coordinator decision D63, ADR 0011 §23 items 6
// and 7):
// Refuses is asked only about file pointers, structured summaries (one path each: a summary that
// starts at the project root hands its path part, the stretch from the root through its last word
// that holds a separator, which may include an argument that holds one), the instruction and skill
// files items 6a and 6b would restore, while a rule anchored outside the project is in force one
// fresh name below the root, and at most 64 of the path-keyed checkpoint drops' other paths
// (rehydrate's maxDropJudgements, those a summary or a reason names first: the rest are withheld
// unjudged and learned as withheld, audit 2's finding 28; with no Read rule in force Refuses reads
// nothing and every drop is asked), once each per build, so a build costs
// at most two Evaluates for each of those, whatever its commands and queries say. The file pointers
// and structured summaries are bounded by the checkpoint's own budget and the instruction and skill
// files by the project's configuration; the drops, whose number grows with the session, by the cap.
func rehydrateHostPaths(p *hostperm.Policy, root string, log logging.Logger) rehydrate.HostPaths {
	return func() rehydrate.HostRules {
		rules, err := p.Snapshot()
		if err != nil {
			log.Loud("rehydrate: host permission policy unavailable; section 6 withholds every path",
				"err", err.Error())
			return rehydrate.HostRules{}
		}
		if rules.Empty() {
			return rehydrate.HostRules{Refuses: func(string) bool { return false }}
		}
		resolved := root
		if r, err := filepath.EvalSymlinks(root); err == nil {
			resolved = r
		}
		return rehydrate.HostRules{Patterns: rules.ReadRulePatterns(), Refuses: func(path string) bool {
			abs := path
			if !filepath.IsAbs(abs) {
				abs = filepath.Join(root, filepath.FromSlash(path))
			}
			if rules.Evaluate(abs).Effect != hostperm.Allow {
				return true
			}
			if resolved == root {
				return false
			}
			// The path's place below the root is read as rehydrate's containment reads it
			// (rehydrate.RootRelative: an ASCII letter's case folded where paths fold, nothing
			// else), so the resolved spelling judged is the project path containment found; on
			// macOS filepath.Rel folds nothing, and a root spelled in another ASCII case would put
			// the judged spelling outside the resolved root. Rehydrate asks only about such paths;
			// any other keeps filepath.Rel's reading, which can only judge one more spelling.
			rel, ok := rehydrate.RootRelative(root, abs)
			if !ok {
				r, err := filepath.Rel(root, abs)
				if err != nil {
					return false
				}
				rel = r
			}
			return rules.Evaluate(filepath.Join(resolved, rel)).Effect != hostperm.Allow
		}}
	}
}
