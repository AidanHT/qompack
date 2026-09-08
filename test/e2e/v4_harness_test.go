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
// PRODUCTION SEAM GAP found while writing these rows, and the reason RunIdle exists: on the
// shipped path the first PreCompact of a daemon's life cannot seal anything. wireCheckpointSources
// publishes a ledger-less SourceSet, SetSources drops it because Validate rejects a nil Ledger, and
// the negative-knowledge ledger is opened only by WireRehydrator's lazy opener on the FIRST
// COMPACTION — which is the SessionStart(source=compact) that arrives AFTER the PreCompact. Until
// an idle pass runs advanceFrontierTask (which republishes the now-complete set), `qompack
// checkpoint` fails with "checkpoint: SourceSet.Store is nil". The rows below therefore drive one
// rehydration and one idle pass before the first PreCompact, exactly as a long-lived daemon would,
// and say so at the call site.
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
		if opts.Ledger != nil {
			_ = opts.Ledger.Close()
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
			Ledger:   opts.Ledger,
			Pins:     pinStore,
			Graph:    opts.Graph,
			Grammar:  gram,
			Tokens:   toks,
		}
		if valErr := src.Validate(); valErr != nil {
			return src, fmt.Errorf("%w: %w", valErr, core.ErrDegraded)
		}
		return src, nil
	}

	opts.Checkpoints = w
	snapshot, _ := sources()
	daemon.BindCheckpoint(&opts, p.Cfg, w, snapshot)

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

// PreCompact runs `qompack checkpoint` and returns the emitted customInstructions, or "" when the
// daemon suppressed them.
func (r *v4Rig) PreCompact(t *testing.T, sess core.SessionID) (hookio.Output, string) {
	t.Helper()
	out := r.Hook(t, []string{"checkpoint"}, cpPreCompactPayload(t, r.P.Root, sess))
	if out.HookSpecificOutput == nil {
		return out, ""
	}
	return out, out.HookSpecificOutput.CustomInstructions
}

// ArmCheckpointSources performs the two steps the shipped daemon needs before its FIRST PreCompact
// can seal anything (see this file's header): one compaction, which is what opens the ledger, and
// one idle pass, whose advanceFrontierTask republishes the now-complete SourceSet to the writer.
//
// It asserts BOTH halves rather than merely performing them, because each is a real precondition a
// regression could remove: the first compact start must report no checkpoint (there is none yet),
// and the supplier must actually resolve afterwards.
func (r *v4Rig) ArmCheckpointSources(t *testing.T, sess core.SessionID) {
	t.Helper()

	_, err := r.Src()
	require.Error(t, err,
		"before the first compaction the SourceSet must be incomplete — the ledger is opened lazily")

	ac := r.CompactStart(t, sess)
	if seq, tagged := x4InjectedSeq(t, ac); tagged {
		require.Equal(t, core.CheckpointSeq(0), seq,
			"no checkpoint exists yet, so this rehydration must take the no-checkpoint path")
	}

	require.Eventually(t, func() bool {
		_, srcErr := r.Src()
		return srcErr == nil
	}, 10*time.Second, 100*time.Millisecond,
		"the first compaction must have opened the negative-knowledge ledger onto Options")

	ran := r.RunIdle(t)
	require.Contains(t, ran, "advance_frontier",
		"the idle pass that republishes the complete SourceSet must have run; ran=%v", ran)
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
func (r *v4Rig) WaitIndexed(t *testing.T, want int) {
	t.Helper()
	ctx := context.Background()
	ticker := time.NewTicker(obsProcessTick)
	defer ticker.Stop()
	timeout := time.NewTimer(obsProcessBound)
	defer timeout.Stop()
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
	return func() negknow.Ledger { return r.Opts.Ledger }
}
