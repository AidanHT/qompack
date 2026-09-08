package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/testutil"
)

// The idle-work bounds this test waits under, each named for the mechanism it waits on
// (V2-MERGE-25 ②: no bound may be smaller than the thing it waits for).
const (
	// schedIdleExitSeconds is the daemon's idle-exit window for this test. It is chosen for the
	// idle TICK it implies — Run's ticker is min(idleTickMax, idleExitSeconds/10) — and it also
	// bounds how long the daemon stays alive: a session goes abandoned after this many seconds
	// of silence and the daemon exits the same interval later, so every wait below must finish
	// inside it.
	schedIdleExitSeconds = 30
	schedIdleTick        = schedIdleExitSeconds * time.Second / 10

	// schedIdleWorkBound waits for the SECOND idle pass's metrics write. The first pass after
	// detectAfterSeconds (1 s) runs SP-12's tasks AFTER SP-05's `metrics` task has already
	// persisted the snapshot, so the pass whose snapshot carries SP-12's own histograms and
	// counters is the next one. Basis: detect-after + two ticks, with room for a loaded runner —
	// and well inside schedIdleExitSeconds, past which the daemon is legitimately gone.
	schedIdleWorkBound = time.Second + 6*schedIdleTick
	schedIdleWorkTick  = 250 * time.Millisecond
)

// The daemon-side names this test reads back by effect. They are spelled here rather than
// imported because e2e asserts what a process left on disk, not what a package exports.
const (
	schedCounterNoWriter       = "sched.frontier.no_writer"
	schedCounterPersist        = "sched.persist"
	schedCounterSourcesUnavail = "checkpoint.sources.unavailable"
	schedGaugeFrontierTicks    = "sched.frontier.ticks_since_advance"
	schedCounterTapPanic       = "sched.tap.panic"
	schedCounterPrefix         = "sched."
	schedHistAdvance           = "idle_task_act.advance_frontier"
	schedHistGC                = "idle_task_gc"
	schedStateScheduler        = "scheduler.json"
	schedStateBOCD             = "bocd.json"
)

// schedObserveToolPayload is observeToolPayload with a tool response large enough that the
// observer's token estimate is certainly non-zero: the residual span the scheduler plans
// advance_frontier from is exactly those tokens.
func schedObserveToolPayload(t *testing.T, root string, id core.ToolUseID) []byte {
	t.Helper()
	body := strings.Repeat("package a\n\nfunc a() int { return 42 }\n", 24)
	resp, err := json.Marshal(map[string]string{"content": body})
	require.NoError(t, err)
	b, err := json.Marshal(hookio.Event{
		HookEventName: "PostToolUse", SessionID: e2eSession, CWD: root,
		ToolName: "Read", ToolUseID: id,
		ToolInput: json.RawMessage(`{"file_path":"a.go"}`), ToolResponse: resp,
	})
	require.NoError(t, err)
	return b
}

// pollUntil samples cond every tick until it holds or bound elapses. It is require.Eventually
// with the diagnostic read AFTER the last sample rather than before the first: Eventually's
// message arguments are evaluated at the call, so a "last seen" value captured by the condition
// never reaches its failure message.
func pollUntil(bound, tick time.Duration, cond func() bool) bool {
	deadline := time.NewTimer(bound)
	defer deadline.Stop()
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		if cond() {
			return true
		}
		select {
		case <-deadline.C:
			return cond()
		case <-ticker.C:
		}
	}
}

// TestDaemonIdleRunsSchedulerWork is SP-12 commit 6's e2e case (ruling R22): the real binary
// starts a daemon, a session-start and a tool-use hook flow through the L0→L3 tap, the session
// goes idle, and the O3 idle pass runs SP-12's six tasks — asserted by effect, because
// IdleController.Registered() is not reachable from outside the daemon package:
//
//   - the persisted metrics snapshot (obs.Registry.Persist, written by SP-05's own `metrics`
//     idle task) carries the per-task histograms SP-05's RunOnce times every registered task
//     into, under SP-12's names, and the `sched.*` family, and the frontier gauge only the
//     ACTING body of act.advance_frontier can leave at zero;
//   - state/scheduler.json and state/bocd.json exist and carry the session id, written by
//     refresh_delta's Persist during the idle pass;
//   - nothing about the scheduler was ever Loud.
func TestDaemonIdleRunsSchedulerWork(t *testing.T) {
	bin := Build(t)
	p := testutil.NewProject(t, testutil.WithConfig(`{"scheduler":{"idle":{"detectAfterSeconds":1}}}`))
	// Shutdown, then wait for the PROCESS to be gone, not only the lock: ruling R52 persists the
	// scheduler's state from runDaemon AFTER d.Run has returned, i.e. after Stop's last act has
	// released daemon.lock — the signal e2eShutdownIfReachable treats as "gone". Handing the
	// tree to t.TempDir's RemoveAll at that moment races WriteAtomic's staging in .qompack/tmp
	// and the two renames into .qompack/state (measured once: "directory is not empty").
	var daemonPID int
	t.Cleanup(func() {
		e2eShutdownIfReachable(t, p.Root)
		if daemonPID > 0 && !pollUntil(e2eDaemonDownBound, e2eDaemonDownTick, func() bool { return !e2eProcessAlive(daemonPID) }) {
			t.Logf("daemon pid %d still alive %s after shutdown; the TempDir cleanup may race its last writes", daemonPID, e2eDaemonDownBound)
		}
	})
	env := e2eEnv(p)
	env[idleExitSecondsEnvKey] = strconv.Itoa(schedIdleExitSeconds)

	stdout, stderr, code := Run(t, bin, []string{"session-start"}, sessionStartPayload(t, p.Root), env)
	require.Equal(t, 0, code, "stderr:\n%s", stderr)
	requireParsesAsOutput(t, stdout)
	e2eWaitDaemonUp(t, p.Root)
	pid, held := e2eDaemonHoldingLock(p.Root)
	require.True(t, held, "a reachable daemon holds daemon.lock")
	daemonPID = pid

	for i, id := range []core.ToolUseID{"toolu_sched_1", "toolu_sched_2"} {
		stdout, stderr, code = Run(t, bin, []string{"observe", "tool"}, schedObserveToolPayload(t, p.Root, id), env)
		require.Equal(t, 0, code, "observe tool #%d: stderr:\n%s", i, stderr)
		requireParsesAsOutput(t, stdout)
	}

	l := paths.Of(p.Root)
	metricsPath := filepath.Join(l.Metrics, "latency.json")
	var snap obs.Snapshot
	var last string
	ok := pollUntil(schedIdleWorkBound, schedIdleWorkTick, func() bool {
		b, err := os.ReadFile(metricsPath)
		if err != nil {
			last = err.Error()
			return false
		}
		var s obs.Snapshot
		if err := json.Unmarshal(b, &s); err != nil {
			last = err.Error()
			return false
		}
		last = string(b)
		if s.Hists[schedHistAdvance].N < 1 || s.Hists[schedHistGC].N < 1 {
			return false // SP-12's tasks have not been timed by RunOnce yet
		}
		if s.Counters[schedCounterPersist] < 1 {
			return false
		}
		// The acting body of act.advance_frontier resets sched.frontier.ticks_since_advance to 0
		// on entry, and refreshDecision is the only other writer -- which only ever sets it to a
		// skip count it has just incremented, so never to 0. A gauge PRESENT and ZERO is therefore
		// the acting body having run, which is what this row waits for.
		if ticks, ran := s.Gauges[schedGaugeFrontierTicks]; !ran || ticks != 0 {
			return false
		}
		snap = s
		return true
	})
	require.True(t, ok, "the idle pass never ran SP-12's tasks into %s within %s (idle tick %s); last read: %s",
		metricsPath, schedIdleWorkBound, schedIdleTick, last)

	// The acting task ran against a REAL writer, and that is the direction that changed.
	// sched.frontier.no_writer is the Rule W-2 posture of a runtime with no frontier advancer,
	// and SP-12's own branch had none -- which is why this row used to REQUIRE the counter. The
	// integrated composition root hands wireScheduler both halves the advancer needs (the
	// checkpoint writer on Options.Checkpoints and the live source supplier), so the honest
	// assertion is now the absence: a regression that stopped passing either half would put the
	// counter back, and this row would catch it.
	require.Zero(t, snap.Counters[schedCounterNoWriter],
		"the shipped daemon wires a frontier advancer; a count here means it stopped: %v", snap.Counters)
	require.Zero(t, snap.Gauges[schedGaugeFrontierTicks],
		"...and O5 was never starved: the acting body ran on every pass it was planned for")

	// It found nothing to advance INTO, and that is by design rather than a hole. negknow.Open
	// has exactly one production call site and fires on the first COMPACTION, because an eager
	// open creates sketches/tried.bloom in every daemon that never compacts and §3.3 reserves
	// that file for the ledger alone (TestE2E_ObserverThroughDaemon and V3-X08 both guard it).
	// This daemon never compacts, so the SourceSet never resolves, and checkpoint's own
	// advance_frontier reports unavailable
	// once per pass -- counted, degraded, never Loud. The first PreCompact is what opens the
	// ledger and seals (internal/daemon TestBindCheckpointSealsOnTheFirstPreCompact,
	// TestE2E_CheckpointHookWritesImmutableArtifact), and the field is live for every pass after
	// it. A ZERO here would mean something opened the ledger eagerly after all.
	require.Positive(t, snap.Counters[schedCounterSourcesUnavail],
		"a daemon that has never compacted has no ledger, so frontier advancement reports "+
			"unavailable rather than advancing: %v", snap.Counters)
	require.Zero(t, snap.Counters[schedCounterTapPanic], "the tap never panicked")
	family := 0
	for name := range snap.Counters {
		if strings.HasPrefix(name, schedCounterPrefix) {
			family++
		}
	}
	require.GreaterOrEqual(t, family, 3, "the sched.* family is in the persisted snapshot: %v", snap.Counters)

	// Every one of the six was registered and timed by SP-05's controller.
	for _, name := range []string{
		"act.advance_frontier", "precompute_slice", "refresh_delta", "rebuild_bloom", "compact_dag", "gc",
	} {
		require.GreaterOrEqual(t, snap.Hists["idle_task_"+name].N, int64(1), "idle task %s never ran; hists=%v", name, snap.Hists)
	}

	// The state files were persisted by the idle pass and belong to this session.
	var idleUpdated core.UnixMilli
	for _, name := range []string{schedStateScheduler, schedStateBOCD} {
		doc := readSchedState(t, filepath.Join(l.State, name))
		require.Equal(t, e2eSession, doc.Session, "state/%s carries the session id", name)
		require.Positive(t, doc.Updated)
		if name == schedStateScheduler {
			idleUpdated = doc.Updated
		}
	}

	// No Loud about the scheduler: LOUD.log is append-only and may not exist at all.
	requireNoSchedulerLoud(t, l)

	// Ruling R52: the daemon's shutdown persists the scheduler's state once more, after d.Run
	// returns. Observable as the scheduler.json stamp advancing past the idle pass's, once the
	// process itself has exited.
	e2eShutdownIfReachable(t, p.Root)
	require.True(t, pollUntil(e2eDaemonDownBound, e2eDaemonDownTick, func() bool { return !e2eProcessAlive(daemonPID) }),
		"daemon pid %d did not exit within %s of admin.shutdown", daemonPID, e2eDaemonDownBound)
	closed := readSchedState(t, filepath.Join(l.State, schedStateScheduler))
	require.Equal(t, e2eSession, closed.Session)
	require.Greater(t, closed.Updated, idleUpdated, "daemon shutdown must persist the scheduler state (R52)")
	requireNoSchedulerLoud(t, l)
}

// schedStateDoc is the slice of state/scheduler.json and state/bocd.json this test reads.
type schedStateDoc struct {
	Session core.SessionID `json:"session"`
	Updated core.UnixMilli `json:"updated"`
}

// readSchedState decodes one scheduler state file.
func readSchedState(t *testing.T, path string) schedStateDoc {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err, "%s must exist", path)
	var doc schedStateDoc
	require.NoError(t, json.Unmarshal(b, &doc), "%s:\n%s", path, b)
	return doc
}

// requireNoSchedulerLoud fails on any LOUD.log line about the scheduler or the frontier.
func requireNoSchedulerLoud(t *testing.T, l paths.Layout) {
	t.Helper()
	loud, err := os.ReadFile(filepath.Join(l.Logs, "LOUD.log"))
	if err != nil {
		return
	}
	for _, line := range splitLines(string(loud)) {
		lower := strings.ToLower(line)
		require.False(t, strings.Contains(lower, "scheduler") || strings.Contains(lower, "frontier"),
			"unexpected Loud line about the scheduler: %s", line)
	}
}
