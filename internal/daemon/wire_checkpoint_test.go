package daemon

// The behaviour suite for wire_checkpoint.go — the daemon half of SP-10.
//
// It exists because the two idle tasks this file registers were, until it was written, executed by
// nothing: both e2e rows assert that the cadence did NOT fire, so the whole body of finalizeIfDue
// could be deleted and every test on the branch still passed. Qompack.md §8.5's trigger clause has
// two halves — "at every PreCompact, and independently on the scheduler's own cadence, so
// checkpoints exist even when compaction does not fire" — and the second half is finalizeIfDue.
// The positive direction is asserted here: a checkpoint exists when compaction never fires.
//
// Everything runs against REAL backends (store.Open, dag.Open, negknow.Open, a real FileWriter).
// The conformance-suite packages export no constructible fakes, and internal/testutil cannot be
// imported from this package at all — testutil -> cli -> daemon would close a cycle in the test
// build graph, which is why fakeclock_test.go carries a local clock. The two seams that ARE
// substituted are substituted at genuine interface boundaries and for stated reasons: pins.Store,
// because Materialize's own filesystem behaviour is internal/pins' subject and not this file's,
// and store.SegmentLog, because the DPI-violation row needs a disagreement between two writers
// that one process driving one real segment log structurally cannot produce.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/pins"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/tokens"
)

// cpSession is the session every row in this file drives.
const cpSession = core.SessionID("sess_wire_checkpoint")

// stubPins is a test-local pins.Store. SourceSet.Pins is an interface, and what a checkpoint needs
// from it here is only that Begin can seed an (empty) invariant set and that Finalize can refresh
// the view; whether pins/invariants.json lands durably is internal/pins' own subject, asserted in
// its package and in paths' ReplacePinsView tests.
type stubPins struct {
	materialized int
}

var _ pins.Store = (*stubPins)(nil)

func (p *stubPins) Add(context.Context, pins.Invariant) error     { return nil }
func (p *stubPins) Remove(context.Context, string) error          { return nil }
func (p *stubPins) All(context.Context) ([]pins.Invariant, error) { return nil, nil }
func (p *stubPins) Materialize(context.Context) error             { p.materialized++; return nil }

// scriptedSegments wraps a real store.SegmentLog and overrides Unencoded per session.
//
// It buys exactly one thing, and only the DPI row uses it: a segment that Unencoded still offers
// even though the log already records it as encoded by a DIFFERENT checkpoint. One process driving
// one real log can never produce that pair — the real Unencoded filters on EncodedOnce — but two
// writers on one project can, and that disagreement is the whole reason the §4.6 DPI guard exists.
// Everything else falls through to the real log.
type scriptedSegments struct {
	store.SegmentLog
	unencoded map[core.SessionID][]store.Segment
	fail      map[core.SessionID]error
}

func (s scriptedSegments) Unencoded(ctx context.Context, sess core.SessionID) ([]store.Segment, error) {
	if err, ok := s.fail[sess]; ok {
		return nil, err
	}
	if segs, ok := s.unencoded[sess]; ok {
		return segs, nil
	}
	return s.SegmentLog.Unencoded(ctx, sess)
}

// persistedDraft is the shape of state/draft-<session>.json this file asserts on: the successor
// draft's parent is the only externally visible proof that Finalize chained the next draft to the
// checkpoint it just sealed, since *checkpoint.Draft exposes no Parent accessor.
type persistedDraft struct {
	Session core.SessionID     `json:"session"`
	Seq     core.CheckpointSeq `json:"seq"`
	Parent  core.CheckpointSeq `json:"parent"`
}

// cpFixture is one project with real backends and a FileWriter over the same root.
type cpFixture struct {
	t     *testing.T
	root  string
	l     paths.Layout
	cfg   config.Config
	clk   *fakeClock
	store store.Store
	src   checkpoint.SourceSet
	w     *checkpoint.FileWriter
	pins  *stubPins
	reg   *SessionRegistry
	seg   core.SegmentID
}

// newCPFixture builds the project testutil.NewProject would have built, by hand: an ensured
// layout, a temp HOME so the token calibration file cannot escape into the developer's real one,
// and real store/dag/ledger backends over the same root.
func newCPFixture(t *testing.T) *cpFixture {
	t.Helper()

	base := t.TempDir()
	root := filepath.Join(base, "project")
	home := filepath.Join(base, "home")
	require.NoError(t, os.MkdirAll(paths.Long(root), 0o700))
	require.NoError(t, os.MkdirAll(paths.Long(home), 0o700))
	t.Setenv("QOMPACK_PROJECT_ROOT", root)
	t.Setenv("QOMPACK_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	l := paths.Of(root)
	require.NoError(t, paths.EnsureLayout(l))

	cfg := testConfig()
	clk := newFakeClock(epoch)
	log := logging.Nop()

	st, err := store.Open(root, cfg, store.Deps{Log: log, Clock: clk})
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	g, err := dag.Open(root, cfg, log)
	require.NoError(t, err)

	led, err := negknow.Open(root, cfg, nil, negknow.Deps{
		Session: cpSession, Clock: clk, Log: log, Graph: g, Store: st,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = led.Close() })

	w, err := checkpoint.OpenWriter(root, cfg, log, obs.New(clk), clk)
	require.NoError(t, err)

	f := &cpFixture{
		t: t, root: root, l: l, cfg: cfg, clk: clk, store: st,
		pins: &stubPins{}, reg: NewSessionRegistry(), w: w,
	}
	f.src = checkpoint.SourceSet{
		Store: st, Segments: st.Segments(), Ledger: led, Pins: f.pins,
		Graph: g, Grammar: grammar.New(), Tokens: tokens.NewForProject(cfg, tokens.DefaultCalibPath(), root),
	}
	require.NoError(t, f.src.Validate())
	return f
}

func (f *cpFixture) ctx() context.Context { return context.Background() }

// live marks sess live in the session registry, which is the primary source liveSessions reads.
func (f *cpFixture) live(sess core.SessionID) {
	f.t.Helper()
	f.reg.Ensure(&hookio.Event{SessionID: sess}, core.NowMilli(f.clk))
}

// closeSegment opens and immediately closes one segment for sess, so Unencoded offers it.
func (f *cpFixture) closeSegment(sess core.SessionID, start, end core.TurnIndex) core.SegmentID {
	f.t.Helper()
	f.seg++
	id, err := f.store.Segments().Open(f.ctx(), store.Segment{ID: f.seg, Session: sess, StartTurn: start})
	require.NoError(f.t, err)
	require.NoError(f.t, f.store.Segments().Close(f.ctx(), id, end, map[string]float64{"tokens": 400}))
	return id
}

// draftPath is state/draft-<session>.json, the file Begin writes and the one whose ABSENCE proves
// no draft was opened.
func (f *cpFixture) draftPath(sess core.SessionID) string {
	return filepath.Join(f.l.State, "draft-"+string(sess)+".json")
}

func (f *cpFixture) readDraft(sess core.SessionID) persistedDraft {
	f.t.Helper()
	b, err := os.ReadFile(paths.Long(f.draftPath(sess)))
	require.NoError(f.t, err)
	var d persistedDraft
	require.NoError(f.t, json.Unmarshal(b, &d))
	return d
}

// advance runs one advance_frontier tick over the fixture's registry.
func (f *cpFixture) advance(ctx context.Context) error {
	return advanceAllSessions(ctx, f.reg, f.w, f.src, logging.Nop())
}

// captureLoud installs a process-wide Loud observer for the duration of one test and returns an
// accessor for the messages it saw. AttachLoudObserver is process-wide, so a test using it must
// not run in parallel with another that does.
func captureLoud(t *testing.T) func() []string {
	t.Helper()
	var msgs []string
	logging.AttachLoudObserver(func(msg string, _ ...any) { msgs = append(msgs, msg) })
	t.Cleanup(func() { logging.AttachLoudObserver(nil) })
	return func() []string { return msgs }
}

// TestCadenceFinalizesWhenDraftReachesBudget is §8.5's second trigger, asserted in the positive
// direction: no PreCompact fires anywhere in this test, and a checkpoint exists anyway.
//
// Both §8.5 cadence conditions get a row, and they are separated on purpose — each row makes the
// OTHER condition false, so neither can pass on the strength of the one the row is not about.
// The successor draft is asserted as well as the artifact: without it, frontier advancement would
// resume from nothing on the next tick and the next residual span would grow from zero again,
// which is the whole O5 property the cadence is protecting.
func TestCadenceFinalizesWhenDraftReachesBudget(t *testing.T) {
	cases := []struct {
		name string
		// segments is how many closed segments the draft absorbs before the tick.
		segments int
		// budgetFor turns the draft's measured price into the configured budget, so the row
		// controls which of `full` and `enough` is true without pinning a token count.
		budgetFor func(est core.Tokens) int
		wantFull  bool
	}{
		{
			name:      "the draft prices at or above the budget",
			segments:  1,
			budgetFor: func(est core.Tokens) int { return int(est) },
			wantFull:  true,
		},
		{
			name:      "the draft absorbed eight segments while still under budget",
			segments:  cadenceSegmentThreshold,
			budgetFor: func(est core.Tokens) int { return int(est) * 10 },
			wantFull:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newCPFixture(t)
			f.live(cpSession)
			for i := 0; i < tc.segments; i++ {
				f.closeSegment(cpSession, core.TurnIndex(i*2), core.TurnIndex(i*2+1))
			}
			require.NoError(t, f.advance(f.ctx()))

			d := f.w.DraftFor(cpSession)
			require.NotNil(t, d, "advance_frontier must leave a draft open for a session with segments")
			require.Equal(t, tc.segments, d.EncodedCount())

			est := d.EstimatedTokens()
			require.Positive(t, est, "a draft with encoded segments must price above zero")
			f.cfg.Checkpoint.BudgetTokens = tc.budgetFor(est)
			require.Equal(t, tc.wantFull, est >= core.Tokens(f.cfg.Checkpoint.BudgetTokens),
				"the row must exercise the cadence condition it names, not the other one")

			require.NoError(t, finalizeIfDue(f.ctx(), f.cfg, f.w, nil, nil))

			sealed := paths.CheckpointPath(f.l, 1)
			require.FileExists(t, paths.Long(sealed),
				"§8.5: a checkpoint must exist even though compaction never fired")

			successor := f.readDraft(cpSession)
			require.Equal(t, core.CheckpointSeq(1), successor.Parent,
				"Finalize must chain a successor draft to the checkpoint it just sealed, so the "+
					"next residual span stays O(delta) instead of growing from zero again")
			require.Equal(t, core.CheckpointSeq(2), successor.Seq)
			require.Equal(t, cpSession, successor.Session)
			require.Positive(t, f.pins.materialized, "sealing must refresh the materialized pins view")
		})
	}
}

// TestCadenceSealsNothingWhenNeitherConditionHolds is the negative control the two rows above need:
// without it, a finalizeIfDue that sealed unconditionally would pass every assertion in this file.
func TestCadenceSealsNothingWhenNeitherConditionHolds(t *testing.T) {
	f := newCPFixture(t)
	f.live(cpSession)
	f.closeSegment(cpSession, 0, 1)
	require.NoError(t, f.advance(f.ctx()))

	d := f.w.DraftFor(cpSession)
	require.NotNil(t, d)
	require.Less(t, d.EncodedCount(), cadenceSegmentThreshold)
	f.cfg.Checkpoint.BudgetTokens = int(d.EstimatedTokens()) * 10

	require.NoError(t, finalizeIfDue(f.ctx(), f.cfg, f.w, nil, nil))
	require.NoFileExists(t, paths.Long(paths.CheckpointPath(f.l, 1)))
	require.Equal(t, d, f.w.DraftFor(cpSession), "the draft must still be the one that was open")
}

// TestAdvanceFrontierOpensNoDraftForASessionWithNothingToEncode is the O(delta) property applied to
// the sweep itself.
//
// liveSessions folds in up to maxTrackedSessions ids from the store's session index, and that index
// is ordered by End — so it answers with sessions that have already FINISHED, which is exactly the
// set the frontier has no work for. Beginning a draft for one is not a cheap no-op: Begin pays a
// full tier-1 seeding (pins, ledger, a whole-graph scan for the earliest prompt) and writes
// state/draft-<session>.json, and only Abort and Finalize ever retire a draft — so every such draft
// stays in OpenDrafts for the daemon's life, its file survives restarts, and finalizeIfDue marshals
// and prices it on every subsequent tick. The absent draft file is what proves the order of the two
// calls, because a draft opened and left open leaves exactly that file behind.
func TestAdvanceFrontierOpensNoDraftForASessionWithNothingToEncode(t *testing.T) {
	const quiet = core.SessionID("sess_no_segments_at_all")

	f := newCPFixture(t)
	f.live(quiet)

	require.NoError(t, f.advance(f.ctx()))

	require.Empty(t, f.w.OpenDrafts(),
		"a session with no unencoded segments must not leave a draft the daemon then prices forever")
	require.NoFileExists(t, paths.Long(f.draftPath(quiet)))

	// The control: the same sweep, one closed segment later, must do all of that work.
	f.closeSegment(quiet, 0, 1)
	require.NoError(t, f.advance(f.ctx()))
	require.Equal(t, []core.SessionID{quiet}, f.w.OpenDrafts())
	require.FileExists(t, paths.Long(f.draftPath(quiet)))
}

// TestACancelledContextStopsTheFrontierSweep pins the first half of the cadence-loop cancellation
// gate. RunOnce hands each idle task a sub-context carrying only the budget left in the tick, so a
// sweep that ignores it runs to completion regardless of what the budget said and starves every
// task queued behind it.
func TestACancelledContextStopsTheFrontierSweep(t *testing.T) {
	f := newCPFixture(t)
	f.live(cpSession)
	f.closeSegment(cpSession, 0, 1)

	ctx, cancel := context.WithCancel(f.ctx())
	cancel()

	err := f.advance(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, f.w.OpenDrafts(), "a cancelled sweep must not have begun anything")
	require.NoFileExists(t, paths.Long(f.draftPath(cpSession)))
}

// TestACancelledContextStopsTheCadenceLoop pins the second half. Finalize keeps its cancellation
// immunity ONCE ENTERED — §12's PreCompact-timeout row says finalize as-is — so the gate is on the
// loop, before the next Finalize starts. Without it a tick with N due drafts performs N full
// finalizes (artifact write, manifest append, pins materialize, successor Begin) whatever the
// remaining budget was, because RunOnce measures the budget only BETWEEN tasks.
func TestACancelledContextStopsTheCadenceLoop(t *testing.T) {
	f := newCPFixture(t)
	f.live(cpSession)
	for i := 0; i < cadenceSegmentThreshold; i++ {
		f.closeSegment(cpSession, core.TurnIndex(i*2), core.TurnIndex(i*2+1))
	}
	require.NoError(t, f.advance(f.ctx()))

	d := f.w.DraftFor(cpSession)
	require.NotNil(t, d)
	require.GreaterOrEqual(t, d.EncodedCount(), cadenceSegmentThreshold, "the draft must be due")

	ctx, cancel := context.WithCancel(f.ctx())
	cancel()

	err := finalizeIfDue(ctx, f.cfg, f.w, nil, nil)
	require.ErrorIs(t, err, context.Canceled)
	require.NoFileExists(t, paths.Long(paths.CheckpointPath(f.l, 1)),
		"a cancelled tick must not have entered Finalize at all")
	require.Equal(t, d, f.w.DraftFor(cpSession), "the due draft must still be open for the next tick")
}

// TestADPIViolationIsLoggedLoudAndSkipped is the §4.6 invariant this whole layer exists to enforce
// mechanically, asserted where it is enforceable: the sweep.
//
// §16 and Advance's own doc both say the caller logs Loud and drops the ids. Folding the error into
// the sweep's first-error instead is two bugs at once: it downgrades a duplicate-prompt-injection
// violation to an ordinary Warn on the idle-task error path, and — because firstNonNil keeps only
// the FIRST error of the sweep — it can discard the violation entirely when anything benign failed
// earlier. The second row is that case.
func TestADPIViolationIsLoggedLoudAndSkipped(t *testing.T) {
	const other = core.SessionID("sess_unrelated_failure")
	benign := errors.New("segment log unavailable for this session")

	cases := []struct {
		name string
		// withBenignFailure adds a second session whose Unencoded fails, so the sweep already has
		// a first error by the time (or before) the violation is seen.
		withBenignFailure bool
	}{
		{name: "the violation is the only thing that went wrong"},
		{name: "an unrelated failure elsewhere in the sweep does not hide it", withBenignFailure: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loud := captureLoud(t)

			f := newCPFixture(t)
			f.live(cpSession)
			id := f.closeSegment(cpSession, 0, 1)

			// Another checkpoint already owns this segment. The real Unencoded would filter it out
			// from here on, so the row scripts it back in: that disagreement between two writers is
			// precisely the state the DPI guard exists to catch.
			require.NoError(t, f.store.Segments().MarkEncoded(f.ctx(), []core.SegmentID{id}, 5))
			seg, err := f.store.Segments().Get(f.ctx(), id)
			require.NoError(t, err)
			require.True(t, seg.EncodedOnce)

			scripted := scriptedSegments{
				SegmentLog: f.store.Segments(),
				unencoded:  map[core.SessionID][]store.Segment{cpSession: {seg}},
				fail:       map[core.SessionID]error{},
			}
			if tc.withBenignFailure {
				f.live(other)
				scripted.fail[other] = benign
			}
			f.src.Segments = scripted

			sweepErr := f.advance(f.ctx())
			if tc.withBenignFailure {
				require.ErrorIs(t, sweepErr, benign,
					"the ordinary failure is still what the sweep reports")
			} else {
				require.NoError(t, sweepErr,
					"a DPI violation is logged and its ids dropped; it is not a sweep failure")
			}

			require.NotEmpty(t, loud(), "a DPI violation must never be able to pass silently")
			require.Contains(t, loud()[0], "DPI")
		})
	}
}

// TestPrecompactDeadlineIsLeftUnsetWhenTheManifestDeclaresNoTimeout pins the degenerate branch
// precompactTimeoutMs was written to make explicit and the wiring used to fold into a
// normal-looking deadline.
//
// The value under test is a decision, not an arithmetic accident: an unreadable manifest yields the
// ZERO instant, which downstream reads as "no caller deadline", and never `now - 6s`, which is a
// deadline in the past that floors PreCompact's own budget to its minimum window on every single
// compaction while looking entirely ordinary at the call site and in the debug artifact.
func TestPrecompactDeadlineIsLeftUnsetWhenTheManifestDeclaresNoTimeout(t *testing.T) {
	t.Parallel()
	now := epoch

	cases := []struct {
		name      string
		timeout   time.Duration
		wantKnown bool
		want      time.Time
	}{
		{
			name:      "the shipped manifest's 20s",
			timeout:   20 * time.Second,
			wantKnown: true,
			want:      now.Add(20*time.Second - precompactDeadlineSlack),
		},
		{
			name:      "an unreadable manifest shape",
			timeout:   0,
			wantKnown: false,
			want:      time.Time{},
		},
		{
			name:      "a negative timeout is just as unusable",
			timeout:   -time.Second,
			wantKnown: false,
			want:      time.Time{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, known := precompactDeadline(now, tc.timeout)
			require.Equal(t, tc.wantKnown, known)
			require.True(t, tc.want.Equal(got), "want %v, got %v", tc.want, got)
			if !known {
				require.True(t, got.IsZero(),
					"an unknown timeout must not be reported as an instant already in the past")
			}
		})
	}
}

// TestPrecompactDeadlineIsNestedInsideTheClientWait restates the 14s < 15s < 20s argument the
// deadline arithmetic exists to satisfy, so that a future change to precompactDeadlineSlack that
// inverts the first pair fails here rather than as an intermittent client-side timeout on exactly
// the slow compactions the deadline was meant to survive.
func TestPrecompactDeadlineIsNestedInsideTheClientWait(t *testing.T) {
	t.Parallel()
	const hostTimeout = 20 * time.Second
	const clientReplyWait = 15 * time.Second

	now := epoch
	deadline, known := precompactDeadline(now, hostTimeout)
	require.True(t, known)
	require.Less(t, deadline.Sub(now), clientReplyWait,
		"the daemon must answer before internal/cli's checkpointReplyDeadline gives up")
	require.Less(t, clientReplyWait, hostTimeout,
		"and the client must answer before the host's own hook timeout")
}

// TestDaemonLogReachesTheConstructedDaemonsLogger pins the seam WireCheckpoint reads the Loud
// logger through. Services carries no logger field and the Daemon interface exposes none, so the
// DPI Loud line would otherwise have nowhere to go.
func TestDaemonLogReachesTheConstructedDaemonsLogger(t *testing.T) {
	t.Parallel()

	d, err := New(Options{ProjectRoot: t.TempDir(), Log: logging.Nop()})
	require.NoError(t, err)
	require.NotNil(t, daemonLog(d))
	require.NotNil(t, daemonLog(nil), "any other Daemon must degrade to a no-op logger, not nil")
}

// TestWireCheckpointResolvesItsSourcesLive is the supplier contract WithSourceSupplier exists for:
// the SAME registered closure must answer differently once the daemon's lazily-opened ledger
// appears, and must never have opened one itself to get there.
//
// The row matters because the production composition root physically cannot hand WireCheckpoint a
// complete SourceSet at registration. The negative-knowledge ledger is opened on the FIRST
// compaction and assigned back onto daemon.Options.Ledger; a value captured at wiring time is nil
// then and nil forever. Before this seam existed the three checkpoint idle tasks were therefore
// inert for the life of any daemon wired that way — the same capture bug SchedulerRuntimeOptions
// .LedgerFn was added for one layer up.
//
// Three phases run through ONE closure, never re-registered.
func TestWireCheckpointResolvesItsSourcesLive(t *testing.T) {
	f := newCPFixture(t)
	f.live(cpSession)
	f.closeSegment(cpSession, 1, 3)

	// Phase 1: the ledger has not been opened yet, exactly as on a daemon that has never
	// compacted. The supplier returns its PARTIAL set plus the reason.
	ledgerOpen := false
	resolve := func() (checkpoint.SourceSet, error) {
		s := f.src
		if !ledgerOpen {
			s.Ledger = nil
		}
		if err := s.Validate(); err != nil {
			return s, fmt.Errorf("%w: %w", err, core.ErrDegraded)
		}
		return s, nil
	}

	m := obs.New(f.clk)
	advance := advanceFrontierTask(f.reg, f.w, resolve, logging.Nop(), m)
	pinsTask := materializePinsTask(resolve)

	require.NoError(t, advance(f.ctx()), "an unusable source set is a degradation, never a task error")
	require.NoFileExists(t, paths.Long(f.draftPath(cpSession)),
		"nothing may be begun while the source set is incomplete")
	require.Equal(t, int64(1), m.Snapshot().Counters[counterSourcesUnavailable],
		"the unavailable route must be counted, not silent")

	// Pins are materialized from the pin log alone, so a missing ledger must not stop them.
	require.NoError(t, pinsTask(f.ctx()))
	require.Equal(t, 1, f.pins.materialized,
		"materialize_pins reads the PARTIAL set; it needs no ledger")

	// Phase 2: the first compaction opens the ledger. The same closure now advances.
	ledgerOpen = true
	require.NoError(t, advance(f.ctx()))
	require.FileExists(t, paths.Long(f.draftPath(cpSession)),
		"the same registered closure must see a ledger opened after registration")
	require.Equal(t, cpSession, f.readDraft(cpSession).Session)
	un, unErr := f.src.Segments.Unencoded(f.ctx(), cpSession)
	require.NoError(t, unErr)
	require.Empty(t, un, "the segment the advance encoded must no longer be offered as unencoded")
	require.Equal(t, int64(1), m.Snapshot().Counters[counterSourcesUnavailable],
		"a pass that found a usable set must not count as unavailable")
}

// TestWireCheckpointWithoutASupplierStillValidatesItsFrozenSet keeps the four-argument call shape
// honest: a caller that froze a half-wired SourceSet reaches the unavailable route, not a nil
// dereference several frames inside Begin.
func TestWireCheckpointWithoutASupplierStillValidatesItsFrozenSet(t *testing.T) {
	f := newCPFixture(t)
	f.live(cpSession)
	f.closeSegment(cpSession, 1, 3)

	half := f.src
	half.Ledger = nil
	m := obs.New(f.clk)
	require.NoError(t, advanceFrontierTask(f.reg, f.w, staticSources(half), logging.Nop(), m)(f.ctx()))
	require.NoFileExists(t, paths.Long(f.draftPath(cpSession)))
	require.Equal(t, int64(1), m.Snapshot().Counters[counterSourcesUnavailable])

	_, err := staticSources(half)()
	require.ErrorIs(t, err, core.ErrDegraded, "the reason must be reportable, not merely nil")
}

// TestBindCheckpointSealsOnTheFirstPreCompact is the daemon-side half of the first-PreCompact fix.
//
// The shipped daemon could not seal on the first PreCompact of its life. wireCheckpointSources
// published a SourceSet whose Ledger was nil — negknow.Open is lazy on purpose — SetSources dropped
// it in silence, and the ledger was opened only by the rehydration on the first COMPACTION, which
// is the SessionStart(source=compact) that arrives AFTER the PreCompact that needed it. Every
// daemon's first compaction therefore produced `hookSpecificOutput: null`, one Warn, and no
// checkpoint.
//
// Three things are asserted here, and the middle one is the invariant the fix had to keep:
//
//   - wiring publishes a usable set even though no ledger exists yet (the accessor is the seam);
//   - wiring opens NOTHING — Options.OpenLedger is untouched until a compaction arrives, so a
//     daemon that never compacts still never creates sketches/tried.bloom;
//   - the first PreCompact triggers that one lazy open itself and seals an artifact.
func TestBindCheckpointSealsOnTheFirstPreCompact(t *testing.T) {
	f := newCPFixture(t)
	f.live(cpSession)
	f.closeSegment(cpSession, 1, 3)

	// The ledger the fixture opened stands in for the one WireRehydrator would open, and the
	// field for daemon.Options.Ledger. Both start out of reach, exactly as on a fresh daemon.
	realLedger := f.src.Ledger
	var field negknow.Ledger
	opens := 0

	o := &Options{ProjectRoot: f.root, Cfg: f.cfg, Log: logging.Nop(), Clock: f.clk}
	o.OpenLedger = func() negknow.Ledger {
		opens++
		field = realLedger
		return field
	}

	// The supplier, shaped as internal/cli's wireCheckpointSources shapes it: the ledger arrives
	// as a FIELD plus an ACCESSOR onto that same field, and the answer is Resolve's, not
	// Validate's, because a consumer asks it in order to begin a draft.
	sources := func() (checkpoint.SourceSet, error) {
		s := f.src
		s.Ledger = field
		s.LedgerFn = func() negknow.Ledger { return field }
		if _, err := s.Resolve(); err != nil {
			return s, fmt.Errorf("%w: %w", err, core.ErrDegraded)
		}
		return s, nil
	}

	snapshot, snapErr := sources()
	require.Error(t, snapErr, "fixture sanity: no compaction has happened, so the set cannot resolve")
	require.NoError(t, snapshot.Validate(),
		"but it IS wired: an accessor is a ledger seam, and a producer must be able to publish this set")

	BindCheckpoint(o, f.cfg, f.w, snapshot, WithSourceSupplier(sources))
	require.Equal(t, 0, opens, "wiring must open nothing; the ledger's laziness is the whole reason it is an accessor")

	var s Services
	for _, bind := range o.binds {
		bind(&s)
	}
	require.NotNil(t, s.PreCompact, "BindCheckpoint must have bound the PreCompact seam")

	out, err := s.PreCompact(f.ctx(), hookio.Event{
		HookEventName: "PreCompact", SessionID: cpSession, Trigger: "auto", CWD: f.root,
	})
	require.NoError(t, err, "the FIRST PreCompact of a daemon's life must seal")
	require.NotNil(t, out.HookSpecificOutput, "a null hookSpecificOutput is the defect's own signature")
	require.NotEmpty(t, out.HookSpecificOutput.CustomInstructions)
	require.Equal(t, 1, opens, "the compaction that needed the ledger is what opened it — exactly once, here")

	entries, readErr := os.ReadDir(paths.Long(f.l.Checkpoints))
	require.NoError(t, readErr)
	require.NotEmpty(t, entries, "an artifact must exist on disk after the first PreCompact")
}

// TestBindCheckpointDegradesWhenTheLedgerCannotBeOpened is the other side of the same seam: a
// source that is genuinely unavailable must reach the caller as a reported failure, never as a
// panic or a silently-empty checkpoint.
//
// The accessor answering nil is what a failed negknow.Open looks like from here. SourceSet.Resolve
// names it, Begin refuses at the top of the call, and the hook route turns that into an ordinary
// error — which handleCheckpoint already reports as a Warn while still exiting 0.
func TestBindCheckpointDegradesWhenTheLedgerCannotBeOpened(t *testing.T) {
	f := newCPFixture(t)
	f.live(cpSession)

	opens := 0
	o := &Options{ProjectRoot: f.root, Cfg: f.cfg, Log: logging.Nop(), Clock: f.clk}
	o.OpenLedger = func() negknow.Ledger { opens++; return nil }

	sources := func() (checkpoint.SourceSet, error) {
		s := f.src
		s.Ledger = nil
		s.LedgerFn = func() negknow.Ledger { return nil }
		if _, err := s.Resolve(); err != nil {
			return s, fmt.Errorf("%w: %w", err, core.ErrDegraded)
		}
		return s, nil
	}
	snapshot, _ := sources()

	BindCheckpoint(o, f.cfg, f.w, snapshot, WithSourceSupplier(sources))

	var s Services
	for _, bind := range o.binds {
		bind(&s)
	}

	out, err := s.PreCompact(f.ctx(), hookio.Event{
		HookEventName: "PreCompact", SessionID: cpSession, Trigger: "auto", CWD: f.root,
	})
	require.Error(t, err, "a ledger that cannot be opened is reported, not sealed around")
	require.ErrorContains(t, err, "SourceSet.Ledger is nil",
		"and it is reported BY NAME, from the top of Begin, rather than as a nil dereference deeper in")
	require.Nil(t, out.HookSpecificOutput, "nothing was sealed, so there is nothing to instruct with")
	require.Equal(t, 1, opens)
}
