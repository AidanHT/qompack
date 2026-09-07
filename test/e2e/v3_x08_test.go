package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/testutil"
)

// X8 — TestV3_DegradedPassiveStillRecordsEverything (plans/V3-VERIFY §5).
//
// Seams: contract.Monitor -> daemon mode enforcement -> observer -> negknow (SP-05 + SP-08 +
// SP-09). §12.1: in ModeDegradedPassive, "L0 and L1 keep running (observe, chunk, store, sketches,
// DAG, verbatim capture, elimination records — the store stays correct and the session's data is
// not lost). Everything that acts is off." Wave 2 is the first wave in which both the recording
// half and the elimination-record half exist, so the clause is testable in full here.
//
// The daemon is composed IN PROCESS (daemon.New over the same store/graph/observer WireObserver
// opens plus a real negknow.Ledger), because the test must reach two seams no spawned process can
// expose: IdleController.RunOnce's ran list and the §12.1 state machine's own transitions. The 44
// hook calls still go through the REAL BINARY against that daemon — the hook client connects to
// whatever daemon owns the project's endpoint — so "every hook exits 0" and "HookSpecificOutput is
// nil" are asserted on genuine process exit codes and stdout bytes.
//
// Where a wave-3 component would sit, the seam is composed here instead (§5's Rule): SP-12's idle
// registration of the ledger's maintenance task is one Register call from Maintainer.
// MaintenanceTask, the acting idle task a wave-3 scheduler would register is a stand-in with the
// act. prefix, and the observer-facing contract->observer mode adapter (daemon/observer_ops.go's
// unexported mapContractMode) is re-composed as x8ObserverMode.

// x8Session is the one session identity every event in this test belongs to.
const x8Session = core.SessionID("sess-v3-x08")

// x8ActTask is the acting idle task a wave-3 component (SP-12's scheduler frontier advancement)
// would register: the name carries the act. prefix, which is the whole §12.1 enforcement key.
const x8ActTask = "act.x8-checkpoint"

// x8RunHook runs one hook subcommand through the real binary and asserts the three properties
// every X8 hook call owes: exit 0, a stdout that is one valid hookio.Output, and — the degraded-
// passive clause — a nil HookSpecificOutput (no additionalContext, no customInstructions).
func x8RunHook(t *testing.T, bin string, argv []string, payload []byte, env map[string]string) {
	t.Helper()
	stdout, stderr, code := Run(t, bin, argv, payload, env)
	require.Equal(t, 0, code, "argv=%v must exit 0 even degraded (§12.1)\nstdout:\n%s\nstderr:\n%s",
		argv, stdout, stderr)
	var out hookio.Output
	require.NoError(t, json.Unmarshal(stdout, &out), "argv=%v stdout must be one hookio.Output:\n%s", argv, stdout)
	require.Nil(t, out.HookSpecificOutput,
		"argv=%v: in ModeDegradedPassive every HookSpecificOutput must be nil — nothing may act; got:\n%s",
		argv, stdout)
}

// x8LinesContaining returns the lines of lines that contain substr.
func x8LinesContaining(lines []string, substr string) []string {
	var out []string
	for _, l := range lines {
		if strings.Contains(l, substr) {
			out = append(out, l)
		}
	}
	return out
}

// x8ObserverMode is the contract->observer mode adapter the daemon composes in observer_ops.go
// (mapContractMode), re-composed here because that adapter is unexported and this test must
// assert its flip directly: only contract.ModeFull maps to observer.ModeFull, both degraded
// states fall to observer.ModePassive.
func x8ObserverMode(mode func() contract.Mode) func() observer.Mode {
	return func() observer.Mode {
		if mode() == contract.ModeFull {
			return observer.ModeFull
		}
		return observer.ModePassive
	}
}

// x8ContractState is the on-disk shape of state/contract.json this test reads back: the mode, the
// persisted reason, and since — the DegradedSince timestamp of the transition.
type x8ContractState struct {
	Mode   string `json:"mode"`
	Reason string `json:"reason"`
	Since  int64  `json:"since"`
}

func x8ReadContractState(t *testing.T, statePath string) x8ContractState {
	t.Helper()
	b, err := os.ReadFile(paths.Long(statePath))
	require.NoError(t, err, "state/contract.json must exist once Degrade has persisted")
	var st x8ContractState
	require.NoError(t, json.Unmarshal(b, &st))
	return st
}

func TestV3_DegradedPassiveStillRecordsEverything(t *testing.T) {
	ctx := context.Background()
	bin := Build(t)
	p := testutil.NewProject(t)
	// A hook whose connect transiently fails can lazily spawn a real detached daemon; whatever is
	// reachable at the end is shut down before t.TempDir()'s own cleanup.
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	env := e2eEnv(p)

	statePath := filepath.Join(paths.Of(p.Root).State, "contract.json")

	// ── Force contract.Monitor.Degrade("v3 test", …) before the daemon exists ────────────────────
	// The daemon constructs its monitor over <root>/.qompack/state/contract.json and §12.1 requires
	// a degradation to survive into the next SessionStart, so degrading through a monitor on the
	// same state path IS the seam: the daemon's own monitor loads this state at New.
	forced := []contract.Result{{
		ID: contract.ID("x8.forced"), OK: false, Severity: contract.SevCritical,
		Expected: "host contract holds", Observed: "forced by X8",
		TS: core.NowMilli(p.Clock),
	}}
	preMon := contract.NewMonitor(p.Log, nil, statePath)
	preMon.Degrade("v3 test", forced)
	require.Equal(t, contract.ModeDegradedPassive, preMon.Mode())

	// LOUD.log contains exactly one degradation line, and state/contract.json carries the reason
	// and DegradedSince.
	require.Len(t, x8LinesContaining(loudLines(t, p.Root), "degrading to passive"), 1,
		"exactly one degradation line must have been Loud-logged")
	st := x8ReadContractState(t, statePath)
	require.Equal(t, contract.ModeDegradedPassive.String(), st.Mode)
	require.Contains(t, st.Reason, "v3 test", "the persisted reason must carry the forced Degrade's reason")
	require.Positive(t, st.Since, "DegradedSince (state/contract.json's since) must record when the degradation was observed")

	// ── Compose the real daemon: real store, graph, ledger and observer ──────────────────────────
	opts := daemon.NewOptions(p.Root, p.Cfg)
	opts.Log = p.Log
	obsv, err := daemon.WireObserver(&opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = opts.Store.Close() })

	// tried.bloom does not exist before the LEDGER has ever run: the store, graph, sketch set and
	// observer just composed above own touch.cms/explore.hll and must never write it (§3.3).
	triedPath := paths.Long(filepath.Join(paths.Of(p.Root).Sketches, "tried.bloom"))
	require.NoFileExists(t, triedPath, "nothing but the ledger may ever create tried.bloom")

	led, err := negknow.Open(p.Root, p.Cfg, nil, negknow.Deps{
		Store: opts.Store, Graph: opts.Graph, Session: x8Session, Log: p.Log, Clock: p.Clock,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = led.Close() })
	maint, ok := led.(negknow.Maintainer)
	require.True(t, ok, "negknow.Open must return a value satisfying negknow.Maintainer")
	opts.Ledger = led

	d, err := daemon.New(opts)
	require.NoError(t, err)
	daemon.RegisterObserverIdleWork(d, obsv)

	// SP-12 (wave 3) would perform these two registrations; the seam is composed here instead
	// (§5's Rule): the ledger's own maintenance triple, and an ACTING task under the act. prefix.
	maintName, maintPrio, maintFn := maint.MaintenanceTask(opts.Store)
	require.Equal(t, "negknow.maintain", maintName,
		"the ledger's maintenance task must register under the name the idle assertions below key on")
	d.Idle().Register(maintName, maintPrio, maintFn)
	actRan := false
	d.Idle().Register(x8ActTask, 90, func(context.Context) error { actRan = true; return nil })

	runCtx, cancelRun := context.WithCancel(ctx)
	runDone := make(chan error, 1)
	go func() { runDone <- d.Run(runCtx) }()
	t.Cleanup(func() { _ = d.Stop(context.Background()); cancelRun() })
	e2eWaitDaemonUp(t, p.Root)

	// ── Inputs: 40 observe.tool, 3 observe.prompt, 1 observe.stop --subagent, all real binary ────
	for i := range 40 {
		x8RunHook(t, bin, []string{"observe", "tool"},
			obsToolPayload(t, p.Root, x8Session, fmt.Sprintf("toolu_x8_%02d", i),
				fmt.Sprintf("src/x8_%02d.go", i), fmt.Sprintf("package x8_%02d\n", i)), env)
	}
	// The middle prompt is the one that WOULD trigger a thrash warning if grammar were real — at
	// wave 2 grammar is a stub, and in degraded-passive nothing may answer anyway, so the only
	// assertion is x8RunHook's own: no additionalContext is emitted for any of the three.
	for _, prompt := range []string{
		"start on the x8 feature",
		"the test is red again after the same edit — try the exact same fix again",
		"ok, wrap it up",
	} {
		x8RunHook(t, bin, []string{"observe", "prompt"}, obsPromptPayload(t, p.Root, x8Session, prompt), env)
	}
	x8RunHook(t, bin, []string{"observe", "stop", "--subagent"},
		obsSubagentStopPayload(t, p.Root, x8Session, "x8-worker", "finished the delegated piece"), env)

	// 40 tool records + 3 verbatim prompts + 1 subagent capture, processed asynchronously behind
	// the ACK: wait on the index itself before anything below reads the tree. A hot-path call
	// whose 8 ms ACK wait (or whose §12.2 NAK-with-hint) sent it to the client spool is durable
	// but invisible until a drain, and the daemon's own idle-tick backstop is 30 s away — so the
	// poll drives Drain itself, exactly as the spool tier is designed to be caught up (it is
	// idempotent, and ingest's seen-set collapses a WAL+spool duplicate back to one dispatch).
	const x8WantRecords = 44
	x8Deadline := time.Now().Add(obsProcessBound)
	// A ticker paces the poll, never time.Sleep: §6.1 bans wall-clock sleeps outside test/bench,
	// _test.go files included, and devtool lint's sleepcheck sub-check enforces it by AST scan.
	x8Tick := time.NewTicker(obsProcessTick)
	defer x8Tick.Stop()
	for len(obsToolUseLines(p.Root)) < x8WantRecords {
		if time.Now().After(x8Deadline) {
			// Diagnostics before failing: spool depth, LOUD, daemon counters and the day-log
			// tail say WHERE the missing records stalled (client spool, WAL, or dispatch).
			entries, _ := os.ReadDir(paths.Long(paths.Of(p.Root).Spool))
			for _, e := range entries {
				b, _ := os.ReadFile(paths.Long(filepath.Join(paths.Of(p.Root).Spool, e.Name())))
				t.Logf("spool file %s: %d lines", e.Name(), countNonEmptyLines(string(b)))
			}
			t.Logf("LOUD: %v", loudLines(t, p.Root))
			snap := e2eStatus(t, p.Root)
			t.Logf("counters: %v", snap.Counters)
			t.Logf("hot=%s mode=%s sessions=%+v", snap.Hot, snap.Mode, snap.Sessions)
			dayLogs, _ := os.ReadDir(paths.Long(paths.Of(p.Root).Logs))
			for _, e := range dayLogs {
				if e.Name() == "LOUD.log" {
					continue
				}
				b, _ := os.ReadFile(paths.Long(filepath.Join(paths.Of(p.Root).Logs, e.Name())))
				lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
				if len(lines) > 60 {
					lines = lines[len(lines)-60:]
				}
				t.Logf("day log %s tail:\n%s", e.Name(), strings.Join(lines, "\n"))
			}
			require.FailNowf(t, "index never reached 44",
				"index/tool_use.jsonl never reached %d lines in degraded-passive (have %d)",
				x8WantRecords, len(obsToolUseLines(p.Root)))
		}
		_, _ = d.Drain(ctx)
		<-x8Tick.C
	}

	// ── 2 IngestMCP eliminations: the elimination-record half keeps recording too ────────────────
	rec1, warns, err := maint.IngestMCP(ctx, negknow.MCPArgs{
		Target:   "src/x8_00.go:handler",
		Approach: "retry with exponential backoff",
		Reason:   "the upstream rejects retries inside the same connection",
	})
	require.NoError(t, err)
	require.Empty(t, warns)
	require.NotEmpty(t, rec1.ID)
	rec2, warns, err := maint.IngestMCP(ctx, negknow.MCPArgs{
		Target:   "src/x8_01.go:parser",
		Approach: "widen the token buffer",
		Reason:   "the overflow is in the grammar, not the buffer",
	})
	require.NoError(t, err)
	require.Empty(t, warns)
	require.NotEmpty(t, rec2.ID)
	require.NotEqual(t, rec1.ID, rec2.ID)

	// ── flush ────────────────────────────────────────────────────────────────────────────────────
	x8RunHook(t, bin, []string{"flush"}, obsFlushPayload(t, p.Root, x8Session), env)

	// ── Idle enforcement: acting tasks skipped, recording/maintenance tasks run ──────────────────
	ran, err := d.Idle().RunOnce(ctx, 5*time.Second)
	require.NoError(t, err)
	require.Subset(t, ran, []string{"drain", "sketches", "metrics", maintName},
		"every recording/maintenance idle task must run in degraded-passive; ran=%v", ran)
	require.NotContains(t, ran, x8ActTask,
		"an act.-prefixed idle task must be skipped while the mode does not MayAct")
	require.False(t, actRan, "the acting task's body must never have run")

	// ── The store stayed correct and complete ────────────────────────────────────────────────────
	require.Len(t, obsToolUseLines(p.Root), x8WantRecords,
		"index/tool_use.jsonl must hold exactly one record per driven event")

	deps, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(p.Root).DAG, "deps.jsonl")))
	require.NoError(t, err, "dag/deps.jsonl must exist after the flush")
	require.NotEmpty(t, strings.TrimSpace(string(deps)))

	sketches := paths.Of(p.Root).Sketches
	require.FileExists(t, paths.Long(filepath.Join(sketches, "touch.cms")))
	require.FileExists(t, paths.Long(filepath.Join(sketches, "explore.hll")))
	require.FileExists(t, triedPath,
		"tried.bloom must exist once the ledger has run — written by the ledger, and only by the ledger")

	// records/eliminations.jsonl has exactly 2 add lines. An add IS a bare record line: the log's
	// two line kinds are told apart by the presence of an "op" key (internal/negknow/log.go), and
	// the only control op this version writes is "stale".
	elim, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(p.Root).Records, "eliminations.jsonl")))
	require.NoError(t, err)
	var addLines int
	for _, line := range strings.Split(strings.TrimRight(string(elim), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var m map[string]json.RawMessage
		require.NoError(t, json.Unmarshal([]byte(line), &m), "eliminations.jsonl line must be JSON: %s", line)
		_, isControl := m["op"]
		require.False(t, isControl, "no control line belongs in this run's log: %s", line)
		require.Contains(t, m, "id", "an add line carries the record's id: %s", line)
		addLines++
	}
	require.Equal(t, 2, addLines, "records/eliminations.jsonl must hold exactly the 2 ingested add lines")

	// Still exactly one degradation line: nothing in the run above may have degraded (or shouted
	// about degrading) a second time.
	require.Len(t, x8LinesContaining(loudLines(t, p.Root), "degrading to passive"), 1)

	// ── Restore path ─────────────────────────────────────────────────────────────────────────────
	// The daemon's part is done; stop it before running the state machine forward, exactly as the
	// next session's SessionStart would find the world.
	require.NoError(t, d.Stop(context.Background()))
	cancelRun()
	require.NoError(t, <-runDone)

	mon := contract.NewMonitor(p.Log, nil, statePath)
	require.Equal(t, contract.ModeDegradedPassive, mon.Mode(),
		"the degradation must survive into a fresh monitor over the same state path (§12.1)")
	obsMode := x8ObserverMode(mon.Mode)
	require.Equal(t, observer.ModePassive, obsMode(),
		"while degraded, the observer-facing adapter must report ModePassive")

	// Two consecutive clean RunAll cycles (no critical failure registered) restore ModeFull.
	cleanEnv := contract.Env{ProjectRoot: p.Root, Cfg: p.Cfg, Log: p.Log, Clock: p.Clock}
	_, mode := mon.RunAll(ctx, cleanEnv)
	require.Equal(t, contract.ModeDegradedPassive, mode, "one clean run must not restore yet")
	_, mode = mon.RunAll(ctx, cleanEnv)
	require.Equal(t, contract.ModeFull, mode, "the second consecutive clean run must restore ModeFull")
	require.Equal(t, contract.ModeFull, mon.Mode())

	require.Len(t, x8LinesContaining(loudLines(t, p.Root), "restoring full"), 1,
		"the restore must be logged exactly as loudly as the degradation")
	require.Equal(t, observer.ModeFull, obsMode(),
		"the observer's Mode() adapter must flip back to observer.ModeFull")
	require.Equal(t, contract.ModeFull.String(), x8ReadContractState(t, statePath).Mode,
		"the restored mode must be persisted for the next session")
}
