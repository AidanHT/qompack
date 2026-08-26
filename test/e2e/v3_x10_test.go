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

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
)

// V3-VERIFY §5 X10: crash recovery replays the observer pipeline and the negative-knowledge
// ledger consistently. Seams: ipc spool + daemon WAL → daemon.Drain → observer → store/dag/sketch,
// with the ledger open concurrently (SP-05 + SP-08 + SP-06 + SP-09).
//
// The shape of the crash: 50 observe.tool events are driven at a real daemon through the real
// binary; after event 30 the daemon is killed with Process.Kill — no flush, no shutdown — so the
// WAL holds exactly the 30 events it accepted and nothing else survives the process. Events 31-50
// are then emitted with the hot-path state record forced to HotSpool: ipc.Client's step 3 spools
// a hot-path op before it ever connects OR lazy-spawns (internal/ipc/client.go, Send), which is
// what pins those 20 events in the CLIENT spool tier rather than racing a fire-and-forget respawn
// for them. Three eliminations were ingested through the real negknow.Ledger before the kill. A
// restart then drains everything, and every assertion below is about the recovery being exact:
// no event lost, no event applied twice, no elimination lost.
const (
	// x10Session is the crashed session's id; its WAL is spool/wal-<x10Session>.ndjson.
	x10Session = core.SessionID("sess-x10-crash")
	// x10RestartSession is the session id the RESTART's session-start runs under. It is a
	// different id on purpose: the restarted daemon's registry has never seen x10Session, so the
	// crashed session is not live, and a fully-drained WAL for a non-live session is deleted
	// (internal/daemon/drain.go, shouldDelete) — which is what the "both spool tiers empty after
	// the drain" assertion needs to be able to hold.
	x10RestartSession = core.SessionID("sess-x10-restart")

	// x10Total events are driven in all; the first x10KillAfter are accepted live by the first
	// daemon, the rest land in the client spool while nothing is running.
	x10Total     = 50
	x10KillAfter = 30
)

// x10Eliminations are the three records ingested through the ledger before the kill. IngestMCP
// stores each record's rendered text as a content-addressed object (internal/negknow/ingest.go,
// PutBytes), so the control run ingests these same three records: both runs then hold
// byte-identical object populations and the Stats().Objects comparison stays exact. No
// DependsOn, so dependency resolution never consults file history and the two runs' ledger
// writes cannot diverge on it.
var x10Eliminations = []struct{ target, approach, reason string }{
	{"src/x10_00.go:F00", "retry with a longer timeout", "the failure is deterministic, not a race"},
	{"src/x10_01.go:F01", "bump the pool size", "the pool is not the bottleneck; the scheduler is"},
	{"src/x10_02.go:F02", "pin the dependency to v1", "v1 has the same defect; the regression is upstream"},
}

// x10ToolID returns the i-th event's tool_use id (0-based), identical across the crashed and
// control runs so the two index populations are comparable record for record.
func x10ToolID(i int) string { return fmt.Sprintf("toolu_x10_%02d", i) }

// x10DriveEvent sends the i-th observe.tool event at root through bin. Distinct id, path and
// content per i — no supersession, so the index line count is exact — and the bytes are a
// deterministic function of i alone, so the control project stores the same objects.
func x10DriveEvent(t *testing.T, bin, root string, env map[string]string, sess core.SessionID, i int) {
	t.Helper()
	obsRunHook(t, bin, []string{"observe", "tool"},
		obsToolPayload(t, root, sess, x10ToolID(i),
			fmt.Sprintf("src/x10_%02d.go", i),
			fmt.Sprintf("package x10ev%02d\n\nfunc F%02d() int { return %d }\n", i, i, i)), env)
}

// x10WalLines counts the non-empty lines across every WAL segment of sess under root's spool
// directory (wal-<sess>.ndjson plus any wal-<sess>.<n>.ndjson rotation, though 30 small events
// never rotate).
func x10WalLines(t *testing.T, root string, sess core.SessionID) int {
	t.Helper()
	files, err := ipc.SpoolFiles(paths.Of(root).Spool)
	require.NoError(t, err)
	total := 0
	for _, f := range files {
		base := filepath.Base(f)
		if !strings.HasPrefix(base, "wal-"+string(sess)) {
			continue
		}
		b, rerr := os.ReadFile(paths.Long(f))
		if rerr != nil {
			continue // deleted between the listing and the read: a drain got there first
		}
		total += countNonEmptyLines(string(b))
	}
	return total
}

// x10ClientSpoolBytes concatenates every client-*.ndjson (non-WAL) spool file under root.
func x10ClientSpoolBytes(t *testing.T, root string) string {
	t.Helper()
	files, err := ipc.SpoolFiles(paths.Of(root).Spool)
	require.NoError(t, err)
	var sb strings.Builder
	for _, f := range files {
		if strings.HasPrefix(filepath.Base(f), "wal-") {
			continue
		}
		b, rerr := os.ReadFile(paths.Long(f))
		require.NoError(t, rerr)
		sb.Write(b)
	}
	return sb.String()
}

// x10IndexIDs decodes index/tool_use.jsonl's lines to their ids, failing the test on any
// mutation (op-carrying) line: with distinct ids, paths and content nothing may supersede.
func x10IndexIDs(t *testing.T, root string) []string {
	t.Helper()
	var ids []string
	for _, line := range obsToolUseLines(root) {
		var rec struct {
			ID string `json:"id"`
			Op string `json:"op"`
		}
		require.NoError(t, json.Unmarshal([]byte(line), &rec), "index line: %s", line)
		require.Empty(t, rec.Op, "no mutation record may appear for distinct-content events: %s", line)
		ids = append(ids, rec.ID)
	}
	return ids
}

// x10KillDaemon SIGKILLs (Process.Kill) the daemon holding root's lock and waits for the process
// to be gone, then backdates its heartbeat past daemon's 90s staleness window. The backdating is
// what makes the crash recoverable PROMPTLY on Windows: lock_windows.go's pidAlive answers
// known=false there, so the restart's AcquireLock falls through to the heartbeat-mtime check
// (staleness step 4) — against a heartbeat the killed daemon refreshed seconds ago, the lock
// would read as held for up to 90 wall-clock seconds. On POSIX the kill(0) probe already answers
// "dead" and the backdated mtime is simply never consulted.
func x10KillDaemon(t *testing.T, root string) {
	t.Helper()
	info, ok := daemon.ReadLock(root)
	require.True(t, ok, "a live daemon must have recorded its pid in daemon.lock")
	require.Positive(t, info.PID)

	proc, err := os.FindProcess(info.PID)
	require.NoError(t, err)
	require.NoError(t, proc.Kill(), "Process.Kill of the daemon (pid %d)", info.PID)
	require.Eventually(t, func() bool { return !e2eProcessAlive(info.PID) },
		e2eDaemonDownBound, e2eDaemonDownTick, "the killed daemon (pid %d) never exited", info.PID)

	// The killed process released nothing: daemon.lock and daemon.hb are both still on disk.
	hb := filepath.Join(paths.Of(root).Run, "daemon.hb")
	past := time.Now().Add(-10 * time.Minute)
	require.NoError(t, os.Chtimes(paths.Long(hb), past, past),
		"the crashed daemon's heartbeat must exist to be backdated past staleAfter")
}

// x10SetHot rewrites the hot-path submode in run/state.bin, the same seam
// TestE2ESpoolSubmodeEndToEnd drives.
func x10SetHot(t *testing.T, root string, h ipc.HotPathMode) {
	t.Helper()
	st := ipc.ReadState(root, config.Defaults())
	st.Hot = h
	require.NoError(t, ipc.WriteState(root, st))
}

// x10OpenLedger opens the real negknow.Ledger over root with a fresh store and graph, returning
// all three plus the Maintainer seam. closeAll releases the ledger and the store (the graph holds
// no handle); it is idempotent so a test can both call it inline and register it as a cleanup.
func x10OpenLedger(t *testing.T, p *testutil.Project) (negknow.Ledger, negknow.Maintainer, store.Store, func()) {
	t.Helper()
	s, err := store.Open(p.Root, p.Cfg, store.Deps{Log: p.Log, Clock: p.Clock})
	require.NoError(t, err)
	g, err := dag.Open(p.Root, p.Cfg, p.Log)
	require.NoError(t, err)
	led, err := negknow.Open(p.Root, p.Cfg, nil, negknow.Deps{
		Store:   s,
		Graph:   g,
		Session: x10Session,
		Log:     p.Log,
		Clock:   p.Clock,
	})
	require.NoError(t, err)

	closed := false
	closeAll := func() {
		if closed {
			return
		}
		closed = true
		_ = led.Close()
		_ = s.Close()
	}
	t.Cleanup(closeAll)

	m, ok := led.(negknow.Maintainer)
	require.True(t, ok, "negknow.Open must return a value satisfying negknow.Maintainer")
	return led, m, s, closeAll
}

// x10StatsObjects drives one full no-crash session over its own fresh project — the control run:
// the same 50 events AND the same 3 ledger ingests (each of which stores the record's text as an
// object), minus the crash — and returns store.Stats().Objects afterwards. The crashed run's
// post-recovery object count is graded against this number.
func x10StatsObjects(t *testing.T, bin string) int {
	t.Helper()
	p := testutil.NewProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	env := e2eEnv(p)
	ctx := context.Background()

	obsRunHook(t, bin, []string{"session-start"}, sessionStartFor(t, p.Root, x10Session), env)
	e2eWaitDaemonUp(t, p.Root)
	for i := range x10Total {
		x10DriveEvent(t, bin, p.Root, env, x10Session, i)
	}
	require.Eventually(t, func() bool { return len(obsToolUseLines(p.Root)) >= x10Total },
		obsProcessBound, obsProcessTick,
		"control run: index/tool_use.jsonl never reached %d lines (have %d)",
		x10Total, len(obsToolUseLines(p.Root)))
	e2eShutdownIfReachable(t, p.Root)

	_, m, s, closeAll := x10OpenLedger(t, p)
	for _, e := range x10Eliminations {
		_, warns, err := m.IngestMCP(ctx, negknow.MCPArgs{
			Target: e.target, Approach: e.approach, Reason: e.reason,
		})
		require.NoError(t, err)
		require.Empty(t, warns)
	}
	st, err := s.Stats(ctx)
	require.NoError(t, err)
	closeAll()
	require.Positive(t, st.Objects, "control run sanity: 50 distinct events must store objects")
	return st.Objects
}

// x10Restart brings a daemon back up over the crashed project: session-start (whose preSend
// calls daemon.EnsureRunning), then wait for reachability. EnsureRunning's single spawn attempt
// is fire-and-poll with a 1.5s bound, so one attempt can legitimately lose to a loaded host; the
// retry re-runs the same hook, exactly as the host's next hook call would in production. On total
// failure it dumps the crashed project's daemon.lock, heartbeat age and day-log tail — the three
// facts that distinguish "the stale-lock reclaim refused" from "the spawn never happened".
func x10Restart(t *testing.T, bin, root string, env map[string]string) {
	t.Helper()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)

	const attempts = 3
	for a := range attempts {
		obsRunHook(t, bin, []string{"session-start"}, sessionStartFor(t, root, x10RestartSession), env)
		up := false
		require.Eventually(t, func() bool {
			up = ipc.Probe(addr, e2eProbeTimeout)
			return up || a < attempts-1 // only the LAST attempt is allowed to give up here
		}, e2eDaemonUpBound, e2eDaemonUpTick, "unreachable")
		if up {
			return
		}
		t.Logf("x10Restart: attempt %d/%d: no daemon became reachable at %s", a+1, attempts, addr.Path)
	}

	if info, ok := daemon.ReadLock(root); ok {
		t.Logf("x10Restart: daemon.lock still present: pid=%d addr=%s (pid alive: %v)",
			info.PID, info.Addr, e2eProcessAlive(info.PID))
	} else {
		t.Logf("x10Restart: no parseable daemon.lock on disk")
	}
	if fi, serr := os.Stat(paths.Long(filepath.Join(paths.Of(root).Run, "daemon.hb"))); serr == nil {
		t.Logf("x10Restart: daemon.hb mtime age: %s", time.Since(fi.ModTime()))
	}
	if entries, derr := os.ReadDir(paths.Long(paths.Of(root).Logs)); derr == nil {
		for _, e := range entries {
			b, rerr := os.ReadFile(paths.Long(filepath.Join(paths.Of(root).Logs, e.Name())))
			if rerr != nil {
				continue
			}
			lines := splitLines(string(b))
			if len(lines) > 40 {
				lines = lines[len(lines)-40:]
			}
			t.Logf("x10Restart: %s (tail):\n%s", e.Name(), strings.Join(lines, "\n"))
		}
	}
	t.Fatalf("x10Restart: no daemon ever became reachable at %s after %d session-start attempts", addr.Path, attempts)
}

func TestV3_CrashRecoveryReplaysObserverAndLedgerConsistently(t *testing.T) {
	ctx := context.Background()
	bin := Build(t)
	p := testutil.NewProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	env := e2eEnv(p)

	// ── phase 1: a live session accepts events 1-30 ──
	obsRunHook(t, bin, []string{"session-start"}, sessionStartFor(t, p.Root, x10Session), env)
	e2eWaitDaemonUp(t, p.Root)

	for i := range x10KillAfter {
		x10DriveEvent(t, bin, p.Root, env, x10Session, i)
	}

	// Wait for the 30 accepted events to be durably WAL'd AND asynchronously processed before
	// the kill. The WAL wait pins the recovery fixture the spec names (the WAL holds events 1-30
	// exactly: ingest.Accept appends before the ACK, so 30 returned hooks mean 30 lines); the
	// index wait lets the observer's async writers (index, dag, sketches) reach a line boundary,
	// so the kill exercises drain/replay recovery rather than torn-tail repair — the case where
	// the spec expects LoadErrors == 0.
	require.Eventually(t, func() bool { return x10WalLines(t, p.Root, x10Session) >= x10KillAfter },
		e2eWALVisibleBound, e2eSpoolDrainTick, "the session WAL never reached %d lines", x10KillAfter)
	require.Equal(t, x10KillAfter, x10WalLines(t, p.Root, x10Session),
		"every accepted event is WAL'd exactly once before its ACK, so 30 hooks mean 30 lines")
	require.Eventually(t, func() bool { return len(obsToolUseLines(p.Root)) >= x10KillAfter },
		obsProcessBound, obsProcessTick, "the first %d events were never all indexed", x10KillAfter)

	// ── phase 2: three eliminations through the real ledger, before the kill ──
	// The ledger is open concurrently with the live daemon, over the same project: its writes
	// (records/eliminations.jsonl, sketches/tried.bloom) are files the observer never touches.
	recIDs := make([]string, 0, len(x10Eliminations))
	{
		_, m, _, closeLedger := x10OpenLedger(t, p)
		for _, e := range x10Eliminations {
			rec, warns, err := m.IngestMCP(ctx, negknow.MCPArgs{
				Target: e.target, Approach: e.approach, Reason: e.reason,
			})
			require.NoError(t, err)
			require.Empty(t, warns)
			require.NotEmpty(t, rec.ID)
			recIDs = append(recIDs, rec.ID)
		}
		// Released before the kill: the recovery must read these records back from disk, not
		// from any handle this process kept warm across the crash.
		closeLedger()
	}

	// ── phase 3: the crash ──
	x10KillDaemon(t, p.Root)

	// ── phase 4: events 31-50 with nothing running — the client spool tier ──
	// HotSpool makes ipc.Client.Send spool a hot-path op at step 3, before it would ever connect
	// or lazy-spawn, so these 20 events deterministically land in client-*.ndjson instead of
	// racing a fire-and-forget respawn.
	x10SetHot(t, p.Root, ipc.HotSpool)
	for i := x10KillAfter; i < x10Total; i++ {
		x10DriveEvent(t, bin, p.Root, env, x10Session, i)
	}

	spooled := x10ClientSpoolBytes(t, p.Root)
	require.NotEmpty(t, spooled, "events sent with no daemon must land in the client spool")
	for i := x10KillAfter; i < x10Total; i++ {
		require.Contains(t, spooled, x10ToolID(i), "event %d must be in the client spool", i)
	}
	require.Equal(t, x10KillAfter, x10WalLines(t, p.Root, x10Session),
		"nothing may have touched the dead daemon's WAL: it still holds events 1-%d only", x10KillAfter)

	// ── phase 5: restart; the daemon drains on start ──
	x10SetHot(t, p.Root, ipc.HotSync)
	x10Restart(t, bin, p.Root, env)

	// After the drain: exactly 50 records, no duplicates. The WAL replay re-dispatches events
	// 1-30 (the new daemon's drain state has no offsets for lines the OLD daemon ingested live),
	// so this is TestNAKDuplicateIsDedupedOnDrain's dedup rule extended across the crash:
	// store.RecordToolUse treats a replayed id with the same Root as a silent no-op.
	require.Eventually(t, func() bool { return len(obsToolUseLines(p.Root)) >= x10Total },
		obsProcessBound, obsProcessTick,
		"the startup drain never brought index/tool_use.jsonl to %d records (have %d)",
		x10Total, len(obsToolUseLines(p.Root)))

	ids := x10IndexIDs(t, p.Root)
	require.Len(t, ids, x10Total,
		"exactly %d records: one per event, none lost, none applied twice", x10Total)
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		require.False(t, seen[id], "duplicate tool_use_id %q in the recovered index", id)
		seen[id] = true
	}
	for i := range x10Total {
		require.True(t, seen[x10ToolID(i)], "event %d is missing from the recovered index", i)
	}

	// Both spool tiers are deleted once fully drained: client-*.ndjson unconditionally, and the
	// crashed session's WAL because x10Session is not live in the restarted daemon.
	var survivors []string
	require.Eventually(t, func() bool {
		files, err := ipc.SpoolFiles(paths.Of(p.Root).Spool)
		survivors = files
		return err == nil && len(files) == 0
	}, e2eSpoolDrainBound+e2eWALVisibleBound, e2eSpoolDrainTick,
		"the drained spool files (client spool and the crashed session's WAL) must be deleted; surviving: %v; sessions: %+v",
		&survivors, e2eStatus(t, p.Root).Sessions)

	// Flush before shutdown: the drained events rebuilt the dependence graph in daemon memory,
	// and dag/deps.jsonl is written only by Graph.Flush (SessionEnd/flush/idle Persist) — without
	// this, phase 7's non-vacuity assertions read an empty log, which is exactly the vacuous pass
	// the X10 review flagged in the first authoring of this test.
	obsRunHook(t, bin, []string{"flush"}, obsFlushPayload(t, p.Root, x10RestartSession), env)

	e2eShutdownIfReachable(t, p.Root)

	// ── phase 6: the ledger recovered everything ──
	logBytes, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(p.Root).Records, "eliminations.jsonl")))
	require.NoError(t, err, "records/eliminations.jsonl must have survived the crash")
	var addLines int
	for _, line := range strings.Split(string(logBytes), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var probe struct {
			Op string `json:"op"`
		}
		require.NoError(t, json.Unmarshal([]byte(line), &probe), "elimination log line: %s", line)
		require.Empty(t, probe.Op, "only add (bare record) lines were ever written: %s", line)
		addLines++
	}
	require.Equal(t, len(x10Eliminations), addLines,
		"the ledger log must still hold exactly %d add lines", len(x10Eliminations))

	led2, _, s2, _ := x10OpenLedger(t, p)
	for i, e := range x10Eliminations {
		ans, qerr := led2.Query(ctx, e.target, e.approach, negknow.ScopeSession)
		require.NoError(t, qerr)
		require.Equal(t, negknow.AnswerActive, ans.State,
			"elimination %d (%s / %s) must answer active after recovery", i, e.target, e.approach)
		require.NotNil(t, ans.Record)
		require.Equal(t, recIDs[i], ans.Record.ID)
	}

	// ── phase 7: the DAG log loads clean ──
	g, err := dag.Open(p.Root, p.Cfg, p.Log)
	require.NoError(t, err)
	stats := g.Stats()
	// Non-vacuity first (review finding): dag.Open on an absent or empty deps.jsonl reports a
	// healthy zero-stats graph, and the observer soft-swallows DAG write errors, so LoadErrors==0
	// alone proves nothing. The crash spanned 50 tool uses, so the log must actually hold a graph.
	require.Positive(t, stats.LogRecords, "deps.jsonl must hold records from the 50-event session")
	require.Positive(t, stats.Nodes, "the recovered DAG must hold nodes, or phase 7 is vacuous")
	require.Positive(t, stats.Edges, "the recovered DAG must hold edges, or phase 7 is vacuous")
	require.Zero(t, stats.LoadErrors, "no whole deps.jsonl line may be damaged by the crash")
	if stats.TruncatedTail {
		// The kill landed mid-append: the torn final line is discarded and every prior record is
		// intact (LoadErrors is zero above). Controller ruling on the review finding: shipped dag
		// surfaces a torn tail with TruncatedTail plus a Warn ("discarded an unterminated final
		// record"), and reserves Loud for damaged records (log.go; a torn tail is "nothing is
		// wrong with the log"), so the X10 row's "exactly one Loud" rides the corrupt-line
		// surface, not this one — asserting project-wide Loud lines here was both loose and
		// wrong-shaped, and the intact-prior-records half above is the load-bearing assertion.
		t.Logf("crash landed mid-append: torn tail discarded, %d records intact", stats.LogRecords)
	}

	// ── phase 8: the recovered store matches a run that never crashed ──
	crashedStats, err := s2.Stats(ctx)
	require.NoError(t, err)
	require.Equal(t, x10StatsObjects(t, bin), crashedStats.Objects,
		"the crashed-and-recovered store must hold exactly the objects of a run that never crashed")
}
