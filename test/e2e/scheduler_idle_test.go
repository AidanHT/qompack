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
	schedCounterCommitClose    = "sched.segment.closed.commit"
	schedGaugeFrontierTicks    = "sched.frontier.ticks_since_advance"
	schedCounterTapPanic       = "sched.tap.panic"
	schedCounterPrefix         = "sched."
	schedHistAdvance           = "idle_task_act.advance_frontier"
	schedHistGC                = "idle_task_gc"
	schedStateScheduler        = "scheduler.json"
	schedStateBOCD             = "bocd.json"
	schedTriedBloom            = "tried.bloom"
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

// schedCommitToolPayload is a successful `git commit` run through the shell tool. It is the tap's
// commit boundary (observer.isGitCommit, scheduler_tap.go closeOnBoundary): the daemon closes the
// session's open segment at the record's turn and rolls its successor open, so the idle pass has one
// closed, unencoded segment to advance the frontier over. Without one the frontier has nothing to
// begin a draft for, and the no-ledger route below would go unexercised.
func schedCommitToolPayload(t *testing.T, root string, id core.ToolUseID) []byte {
	t.Helper()
	input, err := json.Marshal(map[string]string{"command": `git commit -m "wire the idle frontier"`})
	require.NoError(t, err)
	resp, err := json.Marshal(map[string]any{
		"stdout": "[main 1a2b3c4] wire the idle frontier\n 1 file changed, 1 insertion(+)\n",
		"stderr": "", "interrupted": false,
	})
	require.NoError(t, err)
	b, err := json.Marshal(hookio.Event{
		HookEventName: "PostToolUse", SessionID: e2eSession, CWD: root,
		ToolName: "Bash", ToolUseID: id,
		ToolInput: input, ToolResponse: resp,
	})
	require.NoError(t, err)
	return b
}

// schedReadDraft is cpReadDraft without the require: it runs inside a poll, against a file the
// daemon replaces atomically while the row reads it, so a read or decode that fails is "not yet"
// and its reason is handed back for the failure message.
func schedReadDraft(root string, sess core.SessionID) (cpDraftState, string) {
	b, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(root).State, "draft-"+string(sess)+".json")))
	if err != nil {
		return cpDraftState{}, err.Error()
	}
	var d cpDraftState
	if err := json.Unmarshal(b, &d); err != nil {
		return cpDraftState{}, err.Error()
	}
	return d, ""
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
//   - a git commit closes a segment and the idle frontier encodes it into the session's draft
//     although no ledger is open (D49), without opening one and without counting the source as
//     unavailable;
//   - state/scheduler.json and state/bocd.json exist and carry the session id, written by
//     refresh_delta's Persist during the idle pass;
//   - nothing about the scheduler was ever Loud.
func TestDaemonIdleRunsSchedulerWork(t *testing.T) {
	bin := Build(t)
	p := testutil.NewProject(t, testutil.WithConfig(`{"scheduler":{"idle":{"detectAfterSeconds":1}}}`))
	// Shutdown, then wait for the PROCESS to be gone, not only the lock: ruling R52 persists the
	// scheduler's state from runDaemon AFTER d.Run has returned, i.e. after Stop's last act has
	// released daemon.lock. Handing the tree to t.TempDir's RemoveAll at that moment races
	// WriteAtomic's staging in .qompack/tmp and the two renames into .qompack/state (measured
	// once: "directory is not empty"). e2eShutdownIfReachable now waits for that itself — its
	// "gone" is no live lock holder AND every pid it saw holding the lock exited — so the pid wait
	// below asks the same question of the pid this row read, and stays as this row's own check.
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
	pid, held := testutil.DaemonHoldingLock(p.Root)
	require.True(t, held, "a reachable daemon holds daemon.lock")
	daemonPID = pid

	// A task boundary first: the commit closes the session's open segment, which is what the idle
	// frontier then advances over (see the D49 block below). It comes BEFORE the two reads, and the
	// row waits for the close to be durable, so the reads' tokens land in the successor segment:
	// Evaluate plans act.advance_frontier only while the residual (tokens no checkpoint encodes) is
	// positive (scheduler planBackground), so a session whose every token had just been encoded
	// would leave the scheduler's acting body unplanned and the frontier gauge the poll waits on unset.
	l := paths.Of(p.Root)
	stdout, stderr, code = Run(t, bin, []string{"observe", "tool"}, schedCommitToolPayload(t, p.Root, "toolu_sched_commit"), env)
	require.Equal(t, 0, code, "observe tool (git commit): stderr:\n%s", stderr)
	requireParsesAsOutput(t, stdout)
	segLog := paths.Long(filepath.Join(l.Index, "segments.jsonl"))
	require.True(t, pollUntil(obsProcessBound, obsProcessTick, func() bool {
		b, err := os.ReadFile(segLog)
		return err == nil && strings.Contains(string(b), `"op":"close"`)
	}), "the git commit never closed the session's open segment in %s within %s", segLog, obsProcessBound)

	for i, id := range []core.ToolUseID{"toolu_sched_1", "toolu_sched_2"} {
		stdout, stderr, code = Run(t, bin, []string{"observe", "tool"}, schedObserveToolPayload(t, p.Root, id), env)
		require.Equal(t, 0, code, "observe tool #%d: stderr:\n%s", i, stderr)
		requireParsesAsOutput(t, stdout)
	}
	metricsPath := filepath.Join(l.Metrics, "latency.json")
	var snap obs.Snapshot
	var draft cpDraftState
	var last, lastDraft string
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
		if draft, lastDraft = schedReadDraft(p.Root, e2eSession); lastDraft != "" || len(draft.Encoded) == 0 {
			return false // the frontier has not advanced a draft over the closed segment yet
		}
		snap = s
		return true
	})
	require.True(t, ok, "the idle pass never ran SP-12's tasks into %s, or never advanced the frontier into "+
		"state/draft-%s.json, within %s (idle tick %s); last read: %s; draft: %+v %s",
		metricsPath, e2eSession, schedIdleWorkBound, schedIdleTick, last, draft, lastDraft)

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

	// It advanced WITHOUT a ledger, and that is the D49 rule (F-C4-C49-3): no ledger yet is not an
	// advance_frontier error. The daemon opens its elimination ledger lazily, through one memoized
	// opener (internal/daemon rehydrate_service.go): on a compaction, on the first already_tried or
	// record_eliminated call (internal/cli openingLedger), or, only in a project that already holds
	// elimination records, on the checkpoint sources' first resolve after Run is serving
	// (internal/cli recordedLedger). This project holds none and this session neither compacts nor
	// calls a ledger tool, so nothing opens one. Both frontier paths -- checkpoint's own
	// advance_frontier and the scheduler's act.advance_frontier -- may begin a draft without
	// negative knowledge (checkpoint admitNoLedger); whichever reaches the closed segment first
	// encodes it, and the other finds nothing left to encode.
	//
	// Each assertion below is a regression this row exists to catch:
	//   - a closed segment the commit boundary produced, or the frontier had nothing to advance over;
	//   - a draft with that segment encoded (the poll above waited for it): a no-ledger case turned
	//     back into an error leaves no draft at all;
	//   - checkpoint.sources.unavailable at ZERO: the unavailable route is counted, once per pass, by
	//     checkpoint's advance_frontier when a sweep is refused for want of a ledger (and it was this
	//     row's REQUIRED value before D49);
	//   - no sketches/tried.bloom: negknow.Open persists a filter even over no records, and §3.3
	//     reserves that file for the ledger, so its presence means something opened the ledger
	//     eagerly to get past the refusal (TestE2E_ObserverThroughDaemon asserts the same of a
	//     session that never touches the ledger; V3-X08 that nothing but negknow.Open creates it).
	// Unit rows on each half: internal/checkpoint TestFrontierAdvancer_AdvancesBeforeAnyLedgerExists,
	// internal/daemon TestAdvanceFrontierTask_AdvancesBeforeAnyLedgerExists, internal/cli
	// TestProductionCheckpointSourcesOpenNothingWithoutRecords. The first PreCompact opens the
	// ledger and seals (internal/daemon TestBindCheckpointSealsOnTheFirstPreCompact,
	// TestE2E_CheckpointHookWritesImmutableArtifact).
	require.Positive(t, snap.Counters[schedCounterCommitClose],
		"the git commit is a task boundary: it must have closed the session's open segment: %v", snap.Counters)
	require.NotEmpty(t, draft.Encoded,
		"with no ledger yet the frontier still advances: state/draft-%s.json encodes the closed segment", e2eSession)
	require.Zero(t, snap.Counters[schedCounterSourcesUnavail],
		"no ledger yet is not an unavailable source (D49); a count means the frontier refused a ledgerless "+
			"project: %v", snap.Counters)
	require.NoFileExists(t, paths.Long(filepath.Join(l.Sketches, schedTriedBloom)),
		"advancing the frontier must not open the ledger: nothing in this session needs one")
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
