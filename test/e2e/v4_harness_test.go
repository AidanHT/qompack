// The composition the fourteen V4 §4 cross-component scenarios are driven through.
//
// It is the SHIPPED composition, transcribed: internal/cli's runDaemon calls WireObserver, then
// wireCheckpointSources (writer + pin store + LIVE source supplier + BindCheckpoint), then
// daemon.New, then registerCheckpointIdle (WireCheckpoint with WithSourceSupplier). The order and
// the supplier's shape are copied from there, not invented here, because §4's whole claim is that
// the components work together AS COMPOSED.
//
// It is composed IN PROCESS for the two reasons test/e2e/checkpoint_test.go already documents and
// for no others: BindCheckpoint takes an addressable *Options and must run before daemon.New,
// while WireCheckpoint takes the constructed Daemon because Idle() is a method on it — no spawned
// process can perform that two-phase split — and RRunOnce's own `ran` list is the only observable
// form of the §12.1 act.-prefix mode gate. Everything a scenario TESTS still arrives through the
// real binary: the hook calls are real processes over the real transport.
//
// THE PRODUCTION SEAM GAP THESE ROWS FOUND is fixed, and the fix is why they no longer arm
// anything. On the shipped path the first PreCompact of a daemon's life could not seal: the
// composition root published a ledger-less SourceSet, SetSources dropped it silently because
// Validate rejected a nil Ledger, and the negative-knowledge ledger was opened only by
// WireRehydrator's lazy opener on the first COMPACTION — the SessionStart(source=compact) that
// arrives AFTER the PreCompact that needed it — so `qompack checkpoint` failed with "checkpoint:
// SourceSet.Store is nil" and a null hookSpecificOutput. The rows worked around it by driving one
// rehydration and one idle pass before their first PreCompact.
//
// SourceSet now carries LedgerFn, an accessor onto the lazily-opened handle, so a wiring-time set
// is publishable; and the bound PreCompact seam triggers the one lazy open itself, through
// Options.OpenLedger, before it seals. The composition below carries both, because it is a
// transcription and not a variant. The workaround is gone and every row's first PreCompact is now
// the daemon's first PreCompact, unarmed — which is the path a user actually walks.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/pins"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/qompack/qompack/internal/tokens"
)

// v4IdleBudget is the budget handed to IdleController.RunOnce. Basis: the same reasoning
// checkpoint_test.go's cpIdleBudget records — B-E is 2 s for a whole PreCompact and every
// registered task's own budget is orders of magnitude below it, so past ten seconds the tick has
// provably stopped making progress rather than merely being slow.
const v4IdleBudget = 20 * time.Second

// v4Rig is one composed in-process daemon with the full wave-3 resident set, plus the handles the
// scenarios drive it through.
type v4Rig struct {
	D    daemon.Daemon
	W    *checkpoint.FileWriter
	Segs store.SegmentLog
	Src  func() (checkpoint.SourceSet, error)
	Opts *daemon.Options
	P    *testutil.Project
	Bin  string
}

// v4StartRig composes the daemon and runs it.
//
// The MCP retrieval layer is deliberately NOT installed here. internal/cli's installMCPTools is
// the shipped wiring for it, it is unexported, and reproducing its Widener/promoter adaptation in
// a test would make the retrieval rows assert a composition nobody ships. The four rows that drive
// retrieval use the real spawned daemon and a real `qompack mcp` child instead (see v4_x05).
func v4StartRig(t *testing.T, p *testutil.Project) *v4Rig {
	t.Helper()

	opts := daemon.NewOptions(p.Root, p.Cfg)
	opts.Log = p.Log
	// opts.Clock stays NewOptions' SystemClock, never p.Clock: the PreCompact path derives a
	// context deadline from the clock it is handed, and a FakeClock frozen at testutil.Epoch would
	// cancel the very write these rows exist to assert (checkpoint_test.go records the same).

	obsv, err := daemon.WireObserver(&opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = opts.Store.Close() })
	t.Cleanup(func() {
		if led := opts.LedgerHandle(); led != nil {
			_ = led.Close()
		}
	})

	pinStore, err := pins.OpenWith(p.Root, p.Log, opts.Metrics, opts.Clock)
	require.NoError(t, err)

	w, err := checkpoint.OpenWriter(p.Root, p.Cfg, p.Log, opts.Metrics, opts.Clock)
	require.NoError(t, err)

	gram := grammar.New()
	toks := tokens.NewForProject(p.Cfg, tokens.DefaultCalibPath(), p.Root)

	// The LIVE supplier, shaped exactly as internal/cli's wireCheckpointSources shapes it: every
	// seam is read as a FIELD on each call, so the ledger WireRehydrator opens on the first
	// compaction is seen without re-wiring anything.
	sources := func() (checkpoint.SourceSet, error) {
		var segs store.SegmentLog
		if opts.Store != nil {
			segs = opts.Store.Segments()
		}
		src := checkpoint.SourceSet{
			Store:    opts.Store,
			Segments: segs,
			Ledger:   opts.LedgerHandle(),
			// The accessor onto that same field, exactly as wireCheckpointSources supplies it: it
			// is what makes a set assembled before the first compaction publishable rather than
			// silently dropped.
			LedgerFn: opts.LedgerHandle,
			Pins:     pinStore,
			Graph:    opts.Graph,
			Grammar:  gram,
			Tokens:   toks,
		}
		// Resolve, not Validate: a consumer asks this in order to BEGIN a draft, and until
		// something has opened the ledger the honest answer is that it cannot.
		if _, valErr := src.Resolve(); valErr != nil {
			return src, fmt.Errorf("%w: %w", valErr, core.ErrDegraded)
		}
		return src, nil
	}

	opts.Checkpoints = w
	snapshot, _ := sources()
	daemon.BindCheckpoint(&opts, p.Cfg, w, snapshot, daemon.WithSourceSupplier(sources))

	d, err := daemon.New(opts)
	require.NoError(t, err)
	daemon.RegisterObserverIdleWork(d, obsv)
	daemon.WireCheckpoint(d, p.Cfg, w, snapshot, daemon.WithSourceSupplier(sources))

	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- d.Run(runCtx) }()
	t.Cleanup(func() {
		if stopErr := d.Stop(context.Background()); stopErr != nil {
			t.Errorf("e2e: stopping the V4 daemon: %v", stopErr)
		}
		cancelRun()
		if runErr := <-runDone; runErr != nil {
			t.Errorf("e2e: the V4 daemon's Run returned: %v", runErr)
		}
	})
	e2eWaitDaemonUp(t, p.Root)

	return &v4Rig{D: d, W: w, Segs: opts.Store.Segments(), Src: sources, Opts: &opts, P: p, Bin: Build(t)}
}

// RunIdle drives one synchronous idle pass and returns the names that ran. It is what makes these
// rows deterministic instead of waiting on the daemon's own multi-second tick.
func (r *v4Rig) RunIdle(t *testing.T) []string {
	t.Helper()
	ran, err := r.D.Idle().RunOnce(context.Background(), v4IdleBudget)
	require.NoError(t, err)
	return ran
}

// Hook runs one hook subcommand through the real binary against this rig's daemon.
func (r *v4Rig) Hook(t *testing.T, argv []string, payload []byte) hookio.Output {
	t.Helper()
	stdout, stderr, code := Run(t, r.Bin, argv, payload, e2eEnv(r.P))
	require.Equal(t, 0, code, "%v must exit 0\nstdout:\n%s\nstderr:\n%s", argv, stdout, stderr)
	var out hookio.Output
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(string(stdout))), &out),
		"%v stdout must be one hookio.Output:\n%s", argv, stdout)
	return out
}

// CompactStart runs SessionStart(source=compact) and returns the additionalContext the host would
// inject, or "" when the daemon suppressed it.
func (r *v4Rig) CompactStart(t *testing.T, sess core.SessionID) string {
	t.Helper()
	b, err := json.Marshal(hookio.Event{
		HookEventName: "SessionStart", SessionID: sess, CWD: r.P.Root, Source: "compact",
	})
	require.NoError(t, err)
	out := r.Hook(t, []string{"session-start"}, b)
	if out.HookSpecificOutput == nil {
		return ""
	}
	return out.HookSpecificOutput.AdditionalContext
}

// PreCompact runs `qompack checkpoint` as a real process and holds what it wrote to the host's
// PreCompact contract: the empty response. Claude Code has no PreCompact hookSpecificOutput variant
// and rejects the whole response over one (C1.12), and it discards a PreCompact systemMessage, so a
// conforming checkpoint hook says nothing on stdout whether or not it sealed. A row that needs to
// know the seal happened reads the artifact (cpCheckpointArtifacts), not stdout.
func (r *v4Rig) PreCompact(t *testing.T, sess core.SessionID) hookio.Output {
	t.Helper()
	out := r.Hook(t, []string{"checkpoint"}, cpPreCompactPayload(t, r.P.Root, sess))
	cpRequireHostConformingPreCompact(t, out)
	return out
}

// PreCompactReply sends the PreCompact a host would deliver straight to the daemon's checkpoint
// route over the real IPC transport — the hop between the hook client and the daemon — and returns
// the daemon's own reply with the focus instruction in it ("" when the daemon suppressed it).
//
// It exists because that instruction no longer travels any further. The daemon still renders
// Qompack.md §8.5's O1 focus paragraphs and records them (contract.History.PrecompactInstr), but
// the hook client reduces the reply to what the host accepts for PreCompact, which is nothing. The
// rows that pin the instruction's CONTENT — the span paragraph, the sentinel, what must never leak
// into it — therefore read it on the hop where it still exists. Every other row, and the seal
// itself, goes through the real binary via PreCompact.
func (r *v4Rig) PreCompactReply(t *testing.T, sess core.SessionID) (hookio.Output, string) {
	t.Helper()
	return cpPreCompactReply(t, r.P.Root, sess)
}

// OpenLedgerByCompacting drives one SessionStart(source=compact) so that the lazily-opened
// negative-knowledge ledger exists on Options.
//
// This is NOT the old ArmCheckpointSources workaround. That one existed because the first
// PreCompact of a daemon's life could not seal at all; it is gone, and every row's first
// `qompack checkpoint` now seals unarmed. What survives is the ledger's LAZINESS, which is a
// product rule and not a defect: negknow.Open has exactly one production call site and it runs on
// the first COMPACTION, because an eager open creates sketches/tried.bloom in every daemon that
// never compacts. So a daemon that has never compacted has no ledger, and the idle frontier and
// §8.5 cadence tasks — which seed a draft's tier 1 from eliminations — correctly report themselves
// unavailable until one has.
//
// Only the two rows whose SUBJECT is those idle paths call this. A PreCompact would open the
// ledger just as well, and would also seal an artifact — which is exactly what x02's "nothing may
// be sealed before the cadence threshold is crossed" forbids, so the compact-start is the one that
// isolates the variable.
//
// It ASSERTS both halves rather than merely performing them: the set must be unresolvable before
// (the ledger really is lazy) and resolvable after (the compaction really did open one).
func (r *v4Rig) OpenLedgerByCompacting(t *testing.T, sess core.SessionID) {
	t.Helper()

	if r.Opts.LedgerHandle() == nil {
		_, err := r.Src()
		require.Error(t, err,
			"before the first compaction the SourceSet must be incomplete — the ledger is opened lazily")
	}

	if len(cpCheckpointArtifacts(t, r.P.Root)) == 0 {
		ac := r.CompactStart(t, sess)
		if seq, tagged := x4InjectedSeq(t, ac); tagged {
			require.Equal(t, core.CheckpointSeq(0), seq,
				"no checkpoint exists yet, so this rehydration must take the no-checkpoint path")
		}
	}

	require.Eventually(t, func() bool {
		_, srcErr := r.Src()
		return srcErr == nil
	}, 10*time.Second, 100*time.Millisecond,
		"the compaction must have opened the negative-knowledge ledger onto Options")
}

// v4SeedTurns drives n PostToolUse events with distinct content through the real binary and waits
// for the observer to index them.
func (r *v4Rig) SeedTurns(t *testing.T, sess core.SessionID, prefix string, n int) {
	t.Helper()
	env := e2eEnv(r.P)
	for i := range n {
		obsRunHook(t, r.Bin, []string{"observe", "tool"},
			obsToolPayload(t, r.P.Root, sess, fmt.Sprintf("toolu_%s_%02d", prefix, i),
				fmt.Sprintf("src/%s_%02d.go", prefix, i),
				fmt.Sprintf("package %s\n\nfunc handler%02d() error { return nil }\n", prefix, i)), env)
	}
	r.WaitIndexed(t, n)
}

// WaitIndexed waits until index/tool_use.jsonl holds at least want records, driving Drain rather
// than waiting on the daemon's own 30 s idle backstop.
//
// It drives exactly ONE Drain unconditionally before it first looks at the index, and that is not
// an optimization — it is what makes the wait symmetric. The loop below evaluates its condition
// first, so a wait whose records the async ingest had already published drives NO Drain at all
// while a slower one drives several; and every Drain rewrites state/drain.json with advanced
// offsets (internal/daemon/drain.go, saveState). §4.13 compares the write sets of two arms that
// both come through here, so without this the file's membership in each arm's delta is a timing
// coin flip in the fixture rather than anything about the hot path — which is exactly how that row
// failed, in both directions. Draining once up front puts both arms on the same drain path:
// state/drain.json lands in both deltas or in neither, and the comparison is unaffected either way.
func (r *v4Rig) WaitIndexed(t *testing.T, want int) {
	t.Helper()
	ctx := context.Background()
	ticker := time.NewTicker(obsProcessTick)
	defer ticker.Stop()
	timeout := time.NewTimer(obsProcessBound)
	defer timeout.Stop()
	_, _ = r.D.Drain(ctx)
	for len(obsToolUseLines(r.P.Root)) < want {
		_, _ = r.D.Drain(ctx)
		if len(obsToolUseLines(r.P.Root)) >= want {
			return
		}
		select {
		case <-ticker.C:
		case <-timeout.C:
			require.FailNowf(t, "the replayed turns never reached the index",
				"index/tool_use.jsonl never reached %d lines within %s (have %d); LOUD: %v",
				want, obsProcessBound, len(obsToolUseLines(r.P.Root)), loudLines(t, r.P.Root))
		}
	}
}

// LedgerFn resolves the negative-knowledge ledger LIVE, on every read, exactly as internal/cli's
// own wiring does: the ledger is opened lazily on the first compaction, so a value captured at
// composition time would be nil for the life of the process.
func (r *v4Rig) LedgerFn() func() negknow.Ledger {
	return r.Opts.LedgerHandle
}

// v4StartObserverOnly composes a daemon with the L0/L1 observer and NOTHING from wave 3: no
// checkpoint writer, no pin store, no source supplier, no checkpoint idle tasks. It is the
// reference arm of §4.13's structural A/B, and it is the only rig variant that deliberately leaves
// production seams unwired — which is the point, not an omission.
func v4StartObserverOnly(t *testing.T, p *testutil.Project) *v4Rig {
	t.Helper()

	opts := daemon.NewOptions(p.Root, p.Cfg)
	opts.Log = p.Log

	obsv, err := daemon.WireObserver(&opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = opts.Store.Close() })
	t.Cleanup(func() {
		if led := opts.LedgerHandle(); led != nil {
			_ = led.Close()
		}
	})

	d, err := daemon.New(opts)
	require.NoError(t, err)
	daemon.RegisterObserverIdleWork(d, obsv)

	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- d.Run(runCtx) }()
	t.Cleanup(func() {
		if stopErr := d.Stop(context.Background()); stopErr != nil {
			t.Errorf("e2e: stopping the observer-only daemon: %v", stopErr)
		}
		cancelRun()
		if runErr := <-runDone; runErr != nil {
			t.Errorf("e2e: the observer-only daemon's Run returned: %v", runErr)
		}
	})
	e2eWaitDaemonUp(t, p.Root)

	return &v4Rig{D: d, Segs: opts.Store.Segments(), Opts: &opts, P: p, Bin: Build(t)}
}
