package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
)

// V3-VERIFY §5 X9: a live session driven through the REAL binary and REAL daemon, graded on §13
// invariants 2 (append-only means append-only) and 7 (no network, no telemetry, no writes outside
// .qompack/) over the whole pipeline.
//
// The wave-3 surfaces the row's input list names but this checkpoint may not reference
// (internal/mcp is a stub; the plan's Rule forbids touching it) are composed as seams inside this
// file, exactly as the §5 preamble prescribes:
//
//   - the 20 "MCP-shaped ephemeral tool results" are PostToolUse events whose ToolName carries the
//     observer's own mcp__qompack__ prefix (internal/observer/tooluse.go, mcpToolPrefix), which is
//     the shape a real MCP retrieval result reaches the hot path in;
//   - the 6 IngestMCP eliminations and the RefreshStaleness+RebuildBloom cycle run through the
//     real negknow.Ledger opened in-process over the same project — the same composition
//     negknow_test.go's Commit 7 slice uses — after the daemon has been shut down, so exactly one
//     writer owns the store's append handles at any moment.
//
// ADAPTATION (append-only probes; same ruling as internal/store/appendonly_test.go:22 and
// internal/negknow/log_test.go:294). The row asks for O_TRUNC attempts on index/tool_use.jsonl,
// index/roots.jsonl, index/segments.jsonl, dag/deps.jsonl, records/eliminations.jsonl and
// spool/*.ndjson, each failing with core.ErrAppendOnly or os.ErrExist. That assertion cannot be
// made honestly against the shipped paths package: paths.IsProtected — the guard behind
// paths.OpenFile — covers exactly sketches/tried.bloom, checkpoints/ and pins/, and index/, dag/,
// records/ and spool/ are deliberately not among them, so a truncating paths.OpenFile on those
// files SUCCEEDS. What §7.4 actually buys there, and what the two package suites named above
// already pin, is (a) the door: every write reaches those files through paths.AppendOnly, which
// never asks for O_TRUNC and refuses any name outside *.jsonl/*.ndjson/*.log; and (b) the
// observable consequence: the logs are growth-only — bytes once written are still there, at the
// same offsets, after every later mutation. Both halves are asserted below, over the live run's
// own files, alongside the full §7.4 conformance list (p.AssertAppendOnly) whose four probes ARE
// the row's remaining bullets: O_TRUNC on a checkpoint, an in-place pins rewrite, WriteAtomic onto
// sketches/tried.bloom, and CreateNew twice on the same checkpoint seq.

// x9Session is the one session identity every event in this file belongs to.
const x9Session = core.SessionID("sess-e2e-x9")

// The row's event counts, verbatim.
const (
	x9ToolEvents   = 160
	x9PromptEvents = 12
	x9StopEvents   = 4
	x9MCPEvents    = 20
	x9Eliminations = 6
)

// x9MTimeCoarseSlack is how far outside the measured RebuildBloom window the bloom file's own
// mtime is allowed to fall on each side. It is not a tolerance for a sloppy assertion: it is the
// difference between the two clocks the assertion unavoidably compares. time.Now reads the fine
// clock; the timestamp a kernel stamps on an inode comes from a COARSE clock it only refreshes
// once per timer tick (Linux ktime_get_coarse_real_ts64, at most CONFIG_HZ granularity; Windows'
// file times move on the 15.625 ms scheduler tick, the same coarseness internal/obs/cpu_test.go
// already documents). So a file genuinely written INSIDE the window can carry an mtime stamped
// from a tick that began before the window did, and the strict comparison this constant replaces
// fails on a correct write. CI run 34052269275 (test, ubuntu-latest) is that failure, by 738
// microseconds:
//
//	tried.bloom's mtime (2026-09-06 19:00:39.486147054) must be later than
//	the RebuildBloom call (2026-09-06 19:00:39.486885591)
//
// A tick's worth of slack cannot weaken what the assertion is FOR. The claim being pinned is "no
// later writer ever touched this file", and every other writer in this test's timeline is seconds
// away — the daemon is already shut down, the flush has not run yet. 50 ms is an order of
// magnitude above the coarsest tick above and three orders below the nearest other write.
const x9MTimeCoarseSlack = 50 * time.Millisecond

// x9MCPToolName carries the observer's own retrieval-result prefix (tooluse.go mcpToolPrefix), so
// the 20 MCP-shaped events are recorded Ephemeral exactly as a real mcp__qompack__* result is.
const x9MCPToolName = "mcp__qompack__timeline"

// x9Tools is the row's tool mix for the 160 observe-tool events.
var x9Tools = []string{"Read", "Grep", "Bash", "Edit", "Write", "WebFetch"}

// x9GrowthLogs are the append-only logs the growth-only assertion is made over: the row's three
// index logs plus dag/deps.jsonl and records/eliminations.jsonl, as paths relative to .qompack.
// spool/*.ndjson is deliberately absent: WAL segments are transient BY DESIGN (a fully drained
// WAL is deleted — internal/daemon/drain.go, shouldDelete), so "still a prefix later" is not a
// property those files have; the door assertion below still covers the spool directory.
var x9GrowthLogs = []string{
	"index/tool_use.jsonl",
	"index/roots.jsonl",
	"index/segments.jsonl",
	"dag/deps.jsonl",
	"records/eliminations.jsonl",
}

// TestV3_LiveSessionWriteSetAndAppendOnly is V3-VERIFY §5 X9.
func TestV3_LiveSessionWriteSetAndAppendOnly(t *testing.T) {
	ctx := context.Background()
	bin := Build(t)
	p := testutil.NewProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	env := e2eEnv(p)

	// The whole-filesystem "before" snapshot: project tree + HOME, taken before the first event.
	before := x9Snapshot(t, p.Root, p.Home())

	// ── the 200-event session, through the real binary against the real daemon ──────────────────

	x9Hook(t, bin, env, []string{"session-start"}, sessionStartFor(t, p.Root, x9Session))
	e2eWaitDaemonUp(t, p.Root)

	for i := 0; i < x9ToolEvents; i++ {
		x9Hook(t, bin, env, []string{"observe", "tool"}, x9ToolPayload(t, p.Root, i))
	}
	for i := 0; i < x9PromptEvents; i++ {
		x9Hook(t, bin, env, []string{"observe", "prompt"}, x9PromptPayload(t, p.Root, i))
	}
	for i := 0; i < x9StopEvents; i++ {
		x9Hook(t, bin, env, []string{"observe", "stop", "--subagent"}, x9StopPayload(t, p.Root))
	}
	for i := 0; i < x9MCPEvents; i++ {
		x9Hook(t, bin, env, []string{"observe", "tool"}, x9MCPPayload(t, p.Root, i))
	}

	// ── the negative-knowledge phase, in-process over the same project ──────────────────────────
	//
	// The daemon is shut down first (its shutdown drains the spool before releasing the lock, for
	// as long as daemon.StopDrainBound allows), so the store's append handles have exactly one
	// owner while the ledger writes, and re-acquired afterwards by the flush below. Whatever that
	// bounded drain leaves — on a loaded host the live pipeline lags the hooks by most of the
	// session — is the flush's daemon's to replay first (x9FlushGCBound). This is the
	// seam-composition the §5 preamble prescribes for a flow whose production driver
	// (internal/mcp) is a wave-3 stub this test may not reference.
	e2eShutdownIfReachable(t, p.Root)

	// The in-process phase runs on a clock aligned with the daemon's wall clock, not p.Clock:
	// the daemon that stamped the 200 events runs on core.SystemClock, and the flush-time GC
	// judges its retention window against wall-now, so records the ledger stamps with the
	// Epoch-frozen p.Clock (2026-01-01) would look months stale and become GC-eligible —
	// breaking the row's "deleted nothing" bullet for a reason the pipeline does not have.
	x9Clock := testutil.NewFakeClock(time.Now())
	st, err := store.Open(p.Root, p.Cfg, store.Deps{Log: p.Log, Clock: x9Clock})
	require.NoError(t, err)
	stClosed := false
	defer func() {
		if !stClosed {
			_ = st.Close()
		}
	}()
	g, err := dag.Open(p.Root, p.Cfg, p.Log)
	require.NoError(t, err)

	led, err := negknow.Open(p.Root, p.Cfg, nil, negknow.Deps{
		Store: st, Graph: g, Session: x9Session, Log: p.Log, Clock: x9Clock,
	})
	require.NoError(t, err)
	ledClosed := false
	defer func() {
		if !ledClosed {
			_ = led.Close()
		}
	}()
	m, ok := led.(negknow.Maintainer)
	require.True(t, ok, "negknow.Open must return a value satisfying negknow.Maintainer")

	for i := 0; i < x9Eliminations; i++ {
		rec, _, ierr := m.IngestMCP(ctx, negknow.MCPArgs{
			Target:   fmt.Sprintf("src/f%d.go:handler%d", i, i),
			Approach: fmt.Sprintf("approach %d", i),
			Reason:   fmt.Sprintf("x9 elimination %d", i),
		})
		require.NoError(t, ierr, "IngestMCP #%d", i)
		require.NotEmpty(t, rec.ID)
	}

	// One RefreshStaleness + RebuildBloom cycle, exactly as the row drives it.
	_, err = led.RefreshStaleness(ctx, st)
	require.NoError(t, err)

	bloomPath := filepath.Join(paths.Of(p.Root).Sketches, "tried.bloom")
	beforeRebuild := time.Now()
	_, _, err = led.RebuildBloom(ctx)
	require.NoError(t, err)
	afterRebuild := time.Now()

	// tried.bloom was written ONLY through negknow.RebuildBloom: exactly one .bak generation
	// survives (negknow.Open's own reconcile rebuild wrote the initial file with nothing to back
	// up; this rebuild renamed it away), and the file's mtime sits inside the RebuildBloom call's
	// own window, widened on both sides by x9MTimeCoarseSlack for the clock the kernel stamps
	// inodes from — re-checked, unchanged, after the flush below, which is what "and earlier than
	// nothing else" means: no later writer ever touched it.
	require.Len(t, bloomBackupNames(t, p.Root), 1,
		"exactly one tried.bloom.<seq>.bak generation must survive the rebuild")
	fi, err := os.Stat(paths.Long(bloomPath))
	require.NoError(t, err)
	bloomMTime := fi.ModTime()
	require.False(t, bloomMTime.Before(beforeRebuild.Add(-x9MTimeCoarseSlack)),
		"tried.bloom's mtime (%s) must be later than the RebuildBloom call (%s, less %s of "+
			"coarse-clock slack)", bloomMTime, beforeRebuild, x9MTimeCoarseSlack)
	require.False(t, bloomMTime.After(afterRebuild.Add(x9MTimeCoarseSlack)),
		"tried.bloom's mtime (%s) must not postdate RebuildBloom's return (%s, plus %s of "+
			"coarse-clock slack)", bloomMTime, afterRebuild, x9MTimeCoarseSlack)

	ledClosed = true
	require.NoError(t, led.Close())
	stClosed = true
	require.NoError(t, st.Close())

	// Growth-only baseline and the object population, captured before flush.
	logBytesBefore := x9ReadLogs(t, p.Root)
	require.NotEmpty(t, logBytesBefore["records/eliminations.jsonl"],
		"fixture sanity: the six IngestMCP records must be on disk before flush")
	objectsBefore := x9ListFiles(t, paths.Of(p.Root).Objects)
	ephOnly := x9EphemeralOnlyObjects(t, p.Root)
	capturesBefore := x9CaptureSidecars(t, p.Root)
	acksBefore := x9AckedDeliveries(t, p.Root)

	// ── flush: the real binary again (its lazy spawn brings the real daemon back up) ────────────

	// The flush is the session's last hook, so its own lazy spawn is the only thing that can bring
	// a daemon back, and a fresh spawn.lock makes it stand aside (internal/ipc ClaimSpawn: a spawn
	// in flight). Under load a hook's 5 ms connect deadline expires often enough that some hook of
	// the session spawns a daemon that loses the lock to the running one and exits, and its claim
	// stays fresh for e2eSpawnLockStaleAfter. A flush inside that window started nothing, and no
	// daemon ever ran: Linux, -race, CPU and fsync co-load, "observer: gc" never logged in 300 s.
	// So the row lets any such claim lapse first; the flush's own spawn is then what it tests.
	x9AwaitNoSpawnInFlight(t, p.Root)
	pending := x9UnconsumedSpoolLines(t, p.Root)
	x9Hook(t, bin, env, []string{"flush"}, x9SessionEndPayload(t, p.Root))

	// store.GC ran on flush with the configured policy and deleted nothing: the observer's
	// SessionEnd logs exactly one "observer: gc" line per GC pass (internal/observer/session.go
	// step 6), and the retention window (config defaults: 30 days / 10 sessions) covers everything
	// this young project holds.
	var gcLine string
	require.Eventually(t, func() bool {
		line, found := x9LastGCLine(p.Root)
		gcLine = line
		return found
	}, x9FlushGCBound(pending), e2eDaemonDownTick,
		"flush never produced the observer's \"observer: gc\" log line — store.GC did not run on SessionEnd "+
			"(%d spool lines were left for the flush's daemon to replay before it)", pending)
	// The retention window covers every NON-ephemeral object this young project holds, so those
	// may never be collected. Ephemeral retrieval results are different by design: an ephemeral
	// root is never in-window by the age clause (Qompack.md 8.2 - "retrieval spam is reclaimable")
	// and survives only through the session clause, which the flush-time GC can miss for an event
	// the daemon's startup WAL replay still has mid-pipeline - same-session ordering over the
	// transport is best-effort (SP-08's parked R3). So the assertion is the architecture's, not a
	// blanket zero: nothing non-ephemeral is ever deleted.
	//
	// The flush is also a PUBLISHER now, not only a collector. SP-20 (T20-M1-03/04/05) made every
	// leased delivery durable in three stages — records/captures/<shard>/<observation>.json first,
	// then the index reference, then the acknowledged frontier (state/delivery-acks.jsonl) — and
	// an acknowledged delivery "advances a spool offset without republishing anything"
	// (internal/daemon/delivery_lease.go, acknowledge). So an object CAN land under objects/
	// during the flush, and the writer is never store.GC, which has no path that creates an
	// object (its report counts scanned, deleted and freed, nothing else): it is the daemon the
	// flush's lazy spawn brings up, redelivering from the spool through the drain and the bound
	// observer. The contract allows exactly one such redelivery — a delivery the previous daemon
	// leased but never acknowledged (SP05-D1's recovery path, T20-M1-05). Before V5-VERIFY's
	// fix(daemon) this test observed something else: every delivery was leased AND acknowledged
	// and the WAL fully drained before the flush, yet an object still appeared, from a client
	// fallback copy of an ALREADY-ACKNOWLEDGED delivery. A hook whose one-byte transport ACK is
	// lost after the daemon has leased and acknowledged its delivery appends the same request to
	// spool/client-<pid>.ndjson (ipc.awaitACK -> spoolAndReturn); the flush-time daemon's startup
	// Drain then consulted the acknowledged frontier only for a key its in-memory seenSet already
	// held, which is empty on a fresh daemon, so every such copy was dispatched again through
	// fresh handlers with turn=0: a fifth SubagentStop record under SubagentCaptureID(session, 0)
	// and supersede marks against records newer than the replayed content. The drain now asks the
	// frontier for every leased line before the seen set (internal/daemon/drain.go, pinned by
	// TestDrainDoesNotRedeliverAnAcknowledgedClientCopy), so an acknowledged copy advances the
	// offset without dispatch. What remains is the legitimate redelivery: a Stop whose observer
	// append landed but whose acknowledgement the pre-flush shutdown cancelled is redelivered
	// under its reused lease, and captureSubagent mints a fresh SubagentCaptureID from the
	// restored turn (carried defect SP08-D2, evidence
	// TestCarriedDefect_SP08D2_ReusedLeaseRedeliveryIsNotIdempotent). The SubagentStop count
	// below is what catches it; it fails only when host load lets the shutdown cut a Stop
	// mid-dispatch, and nothing here claims SP05-D1 fixed on the strength of any of this.
	//
	// So "GC must not add objects" is four claims, each proved on its own evidence. GC's
	// deletions are exactly the objects that vanished (the log's deleted counter equals the
	// directory diff). Every index record the flush writes is matched, one to one, to a delivery
	// that crossed the acknowledged frontier DURING the flush — the only deliveries a correct
	// flush may publish — so a republication of an acknowledged delivery is red here even though
	// the replay's own index line vouches for its object. Every supersede mark the flush appends
	// is authored by a record the flush wrote and newer than the record it supersedes, whatever
	// else crossed. And every object that appeared is named by a capture sidecar or an index line
	// the flush wrote — a durable object with no reference is the dangling state publication
	// order forbids.
	require.Contains(t, gcLine, " truncated=false", "the GC pass must have finished inside its deadline")

	e2eShutdownIfReachable(t, p.Root)

	// Independently of the log: no non-ephemeral object was removed. Any file GC did reclaim must
	// be referenced ONLY by ephemeral roots (the 8.2 carve-out above).
	afterObjects := x9ListFiles(t, paths.Of(p.Root).Objects)
	afterSet := make(map[string]bool, len(afterObjects))
	for _, f := range afterObjects {
		afterSet[f] = true
	}
	beforeSet := make(map[string]bool, len(objectsBefore))
	vanished := 0
	for _, f := range objectsBefore {
		beforeSet[f] = true
		if !afterSet[f] {
			vanished++
			require.True(t, ephOnly[f],
				"GC deleted %s, which is referenced by a non-ephemeral root - the retention window must cover it", f)
		}
	}
	// GC owns every disappearance, and nothing else: the counter it logged is the diff.
	require.Equal(t, vanished, x9GCCounter(t, gcLine, "deleted"),
		"the objects that vanished across flush must be exactly the ones GC reports deleting: %s", gcLine)
	// The flush publishes only what crossed the acknowledged frontier during the flush. Each new
	// index/tool_use.jsonl record is matched to ONE content delivery (an observe.* op) that was
	// unacknowledged before the flush and acknowledged by its end - a tool record to the crossing
	// whose capture sidecar LinkCaptureReference stamped with its tool_use_id, a SubagentStop or
	// UserPromptSubmit record (derived ids; the payload carries no tool_use_id) to a crossing of
	// its op in its session - and each crossing vouches for at most one record. A count would let
	// one legitimate crossing that publishes nothing hide one phantom record; the crossing's own
	// identity cannot. The flush's own lifecycle delivery crosses too and publishes no record.
	crossed := x9ContentDeliveriesCrossed(t, p.Root, acksBefore)
	newIndex := x9ParseToolUseLines(t, x9NewLines(t, p.Root, "index/tool_use.jsonl", logBytesBefore))
	unmatched := append([]x9Crossing(nil), crossed...)
	newRecordIDs := map[string]bool{}
	for _, ln := range newIndex {
		if ln.Op == "supersede" {
			continue
		}
		newRecordIDs[ln.ID] = true
		i := x9MatchCrossing(unmatched, ln)
		require.GreaterOrEqual(t, i, 0,
			"the flush wrote index record %s (%s turn %d session %s) and no content delivery that crossed the "+
				"acknowledged frontier during the flush accounts for it (unmatched crossings: %v; all: %v) - a "+
				"record with no crossing is a republication of a delivery the frontier already holds "+
				"(delivery_lease.go acknowledge: \"without republishing anything\")",
			ln.ID, ln.Tool, ln.Turn, ln.S, unmatched, crossed)
		unmatched = append(unmatched[:i], unmatched[i+1:]...)
	}
	// A supersede mark is the consequence of a record landing (observer/supersede.go marks every
	// earlier read the NEW record makes redundant), so whatever crossed: its superseder is newer
	// in the append-only index than the record it supersedes, and it is a record this flush
	// wrote. A replayed read fails both - it supersedes records that landed after it, and its own
	// id was in the index before the flush - and neither check depends on how many deliveries
	// legitimately crossed.
	ordinals := x9IndexOrdinals(t, p.Root)
	for _, ln := range newIndex {
		if ln.Op != "supersede" {
			continue
		}
		older, hasOlder := ordinals[ln.ID]
		newer, hasNewer := ordinals[ln.By]
		require.True(t, hasOlder && hasNewer,
			"supersede mark %s by %s names a record index/tool_use.jsonl does not hold", ln.ID, ln.By)
		require.Greater(t, newer, older,
			"supersede mark %s by %s: the superseder is the OLDER record (index ordinal %d, superseded record "+
				"at %d) - a replayed read is superseding records newer than its content",
			ln.ID, ln.By, newer, older)
		require.True(t, newRecordIDs[ln.By],
			"supersede mark %s by %s: the superseder is not a record this flush wrote (%v) - a mark with no "+
				"new record behind it is a redelivered read re-running supersession",
			ln.ID, ln.By, newRecordIDs)
	}
	// The row's four Stop events are the only SubagentStop captures this session ever took; a fifth
	// is a redelivered Stop re-captured under a fresh SubagentCaptureID.
	require.Equal(t, x9StopEvents, x9CountTool(t, p.Root, "SubagentStop"),
		"index/tool_use.jsonl must hold exactly one SubagentStop record per Stop event after the flush")
	// GC owns no appearance: each object the flush added is reachable from a record the flush
	// wrote. The witness is logged so the publication that produced it is on the record.
	witnesses := x9FlushWitnesses(t, p.Root, logBytesBefore, capturesBefore)
	for _, f := range afterObjects {
		if beforeSet[f] {
			continue
		}
		require.Contains(t, witnesses, f,
			"%s appeared during flush and no capture sidecar or index line the flush wrote names it "+
				"- GC adds nothing (%s), so an unreferenced new object is an unpublished writer",
			f, gcLine)
		t.Logf("x9: %s appeared during flush; published by %s", f, witnesses[f])
	}

	// tried.bloom is still exactly the file RebuildBloom wrote: same mtime, same one backup.
	fi, err = os.Stat(paths.Long(bloomPath))
	require.NoError(t, err)
	require.True(t, fi.ModTime().Equal(bloomMTime),
		"tried.bloom was written after RebuildBloom (mtime %s -> %s); only negknow.RebuildBloom may write it",
		bloomMTime, fi.ModTime())
	require.Len(t, bloomBackupNames(t, p.Root), 1)

	// ── §13 invariant 7: the write set ──────────────────────────────────────────────────────────

	after := x9Snapshot(t, p.Root, p.Home())
	x9AssertConfined(t, before, after, p.Root, p.Home())

	// ── §13 invariant 2: append-only means append-only ──────────────────────────────────────────

	// Growth-only over the live run's own logs: everything on disk before flush is still there,
	// byte-identical and at the same offsets, after the flush's segment close, store flush and GC.
	for rel, was := range logBytesBefore {
		now := x9ReadLog(t, p.Root, rel)
		require.True(t, bytes.HasPrefix(now, was),
			"%s lost or rewrote already-written bytes across flush: it grew from %d to %d bytes but the old content is not a prefix",
			rel, len(was), len(now))
	}

	// The §7.4 conformance list, verbatim: O_TRUNC on a checkpoint, an in-place rewrite of
	// pins/invariants.jsonl, paths.WriteAtomic onto sketches/tried.bloom, and CreateNew twice on
	// the same checkpoint seq — each failing with core.ErrAppendOnly or os.ErrExist.
	p.AssertAppendOnly(t)

	// The door: paths.AppendOnly launders nothing that is not a log file, in each of the row's
	// directories (see the ADAPTATION note in the file header for why the O_TRUNC probes on these
	// directories' files are not assertable against the shipped guard).
	l := paths.Of(p.Root)
	for _, dir := range []string{l.Index, l.DAG, l.Records, l.Spool} {
		_, doorErr := paths.AppendOnly(filepath.Join(dir, "x9-laundered.bin"))
		require.ErrorIs(t, doorErr, core.ErrAppendOnly,
			"paths.AppendOnly must refuse a non-log extension under %s", dir)
	}

	// ── §13 invariant 7: zero network — J3's import assertions, over the built binary ───────────

	x9AssertBinaryHasNoNetworkImports(t, bin)
}

// x9Hook runs one hook subcommand of the real binary and asserts §2.3's only permitted outcome:
// exit 0 with a single valid hookio.Output on stdout.
func x9Hook(t *testing.T, bin string, env map[string]string, args []string, payload []byte) {
	t.Helper()
	stdout, stderr, code := Run(t, bin, args, payload, env)
	require.Equal(t, 0, code, "%v: stderr:\n%s", args, stderr)
	requireParsesAsOutput(t, stdout)
}

// x9ToolPayload builds observe-tool event i of the row's mixed 160: the tool cycles through
// x9Tools, file tools re-visit a small path set (so re-reads supersede), and every event carries a
// unique tool_use_id and a non-empty response body.
func x9ToolPayload(t *testing.T, root string, i int) []byte {
	t.Helper()
	tool := x9Tools[i%len(x9Tools)]
	var input json.RawMessage
	switch tool {
	case "Read", "Edit", "Write":
		input = x9JSON(t, map[string]string{"file_path": fmt.Sprintf("src/f%d.go", i%8)})
	case "Grep":
		input = x9JSON(t, map[string]string{"pattern": "handler", "path": "src"})
	case "Bash":
		input = x9JSON(t, map[string]string{"command": "go test ./..."})
	default: // WebFetch
		input = x9JSON(t, map[string]string{"url": "https://example.invalid/doc"})
	}
	return x9Event(t, hookio.Event{
		HookEventName: "PostToolUse", SessionID: x9Session, CWD: root,
		ToolName: tool, ToolUseID: core.ToolUseID(fmt.Sprintf("toolu_x9_%03d", i)),
		ToolInput: input,
		ToolResponse: x9JSON(t, map[string]string{
			"content": fmt.Sprintf("x9 %s output %d\nline two of a small but real body\n", tool, i%8),
		}),
	})
}

// x9MCPPayload builds one of the 20 MCP-shaped ephemeral tool results: a PostToolUse whose
// ToolName carries the mcp__qompack__ prefix the observer records Ephemeral.
func x9MCPPayload(t *testing.T, root string, i int) []byte {
	t.Helper()
	return x9Event(t, hookio.Event{
		HookEventName: "PostToolUse", SessionID: x9Session, CWD: root,
		ToolName: x9MCPToolName, ToolUseID: core.ToolUseID(fmt.Sprintf("toolu_x9_mcp_%03d", i)),
		ToolInput:    x9JSON(t, map[string]string{"query": fmt.Sprintf("retrieval %d", i)}),
		ToolResponse: x9JSON(t, map[string]string{"content": fmt.Sprintf("x9 ephemeral retrieval result %d\n", i)}),
	})
}

// x9PromptPayload builds observe-prompt event i.
func x9PromptPayload(t *testing.T, root string, i int) []byte {
	t.Helper()
	return x9Event(t, hookio.Event{
		HookEventName: "UserPromptSubmit", SessionID: x9Session, CWD: root,
		Prompt: fmt.Sprintf("x9 user prompt %d: please look at src/f%d.go", i, i%8),
	})
}

// x9StopPayload builds the Stop event `observe stop --subagent` consumes; the subagent marker
// itself travels as the CLI flag, exactly as the plugin manifest's SubagentStop entry passes it.
func x9StopPayload(t *testing.T, root string) []byte {
	t.Helper()
	return x9Event(t, hookio.Event{HookEventName: "Stop", SessionID: x9Session, CWD: root})
}

// x9EphemeralOnlyObjects returns the object files referenced ONLY by ephemeral roots - the one
// class Qompack.md 8.2 lets a SessionEnd GC reclaim ("an ephemeral root is never in-window by the
// age clause; retrieval spam is reclaimable"). Everything else must survive the flush.
func x9EphemeralOnlyObjects(t *testing.T, root string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(paths.Long(filepath.Join(root, ".qompack", "index", "roots.jsonl")))
	require.NoError(t, err)

	ephFiles := map[string]bool{}
	keepFiles := map[string]bool{}
	for _, ln := range bytes.Split(raw, []byte{'\n'}) {
		if len(bytes.TrimSpace(ln)) == 0 {
			continue
		}
		rec := x9ParseRootLine(t, ln)
		dst := keepFiles
		if rec.Eph {
			dst = ephFiles
		}
		for _, p := range rec.objects() {
			dst[p] = true
		}
	}
	out := map[string]bool{}
	for f := range ephFiles {
		if !keepFiles[f] {
			out[f] = true
		}
	}
	return out
}

// x9RootLine is the slice of an index/roots.jsonl line these assertions read: the root, its
// chunk set, and whether the record is ephemeral.
type x9RootLine struct {
	Op     string `json:"op"`
	Root   string `json:"root"`
	Eph    bool   `json:"eph"`
	Chunks []struct {
		H string `json:"h"`
	} `json:"chunks"`
}

// objects lists every object file the line names, in x9ListFiles's slash form.
func (r x9RootLine) objects() []string {
	var out []string
	if p := x9ObjectPath(r.Root); p != "" {
		out = append(out, p)
	}
	for _, c := range r.Chunks {
		if p := x9ObjectPath(c.H); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func x9ParseRootLine(t *testing.T, ln []byte) x9RootLine {
	t.Helper()
	var rec x9RootLine
	require.NoError(t, json.Unmarshal(ln, &rec))
	return rec
}

// x9ObjectPath maps a hash, with or without its "sha256:" prefix, to the object file it names under
// objects/, in the slash form x9ListFiles keys by. An empty string means h names nothing.
func x9ObjectPath(h string) string {
	h = strings.TrimPrefix(h, "sha256:")
	if len(h) < 4 {
		return ""
	}
	return h[:2] + "/" + h[2:4] + "/" + h + ".zst"
}

// x9CaptureSidecars reads every SP-20 capture sidecar under records/captures/, keyed by its path
// relative to .qompack, so a later read can tell a sidecar the flush wrote or rewrote
// (store.WriteCaptureSidecar, then store.LinkCaptureReference) from one it left alone.
func x9CaptureSidecars(t *testing.T, root string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	dir := filepath.Join(paths.Of(root).Records, "captures")
	for _, rel := range x9ListFiles(t, dir) {
		b, err := os.ReadFile(paths.Long(filepath.Join(dir, filepath.FromSlash(rel))))
		require.NoError(t, err)
		out["records/captures/"+rel] = b
	}
	return out
}

// x9GCCounter reads one integer counter off the observer's "observer: gc" log line, which
// internal/observer/session.go step 6 writes as key=value pairs.
func x9GCCounter(t *testing.T, gcLine, key string) int {
	t.Helper()
	_, rest, found := strings.Cut(gcLine, " "+key+"=")
	require.True(t, found, "the gc line carries no %s counter: %s", key, gcLine)
	field, _, _ := strings.Cut(rest, " ")
	n, err := strconv.Atoi(field)
	require.NoError(t, err, "the gc line's %s counter is not an integer: %s", key, gcLine)
	return n
}

// x9FlushWitnesses maps every object file that a record written DURING the flush names to a
// description of that record. Three writers can publish an object across a flush, and each leaves
// a durable trace this reads: a capture sidecar the drain wrote or the observer linked
// (records/captures/, publication order's first two stages), a new index/roots.jsonl content
// record, and a new index/tool_use.jsonl reference. "Written during the flush" is a new file, a
// changed file, or bytes past the growth-only baseline taken before the flush.
func x9FlushWitnesses(t *testing.T, root string, logsBefore map[string][]byte, capturesBefore map[string][]byte) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, ln := range x9NewLines(t, root, "index/roots.jsonl", logsBefore) {
		rec := x9ParseRootLine(t, ln)
		for _, p := range rec.objects() {
			out[p] = fmt.Sprintf("index/roots.jsonl line (root=%s eph=%t)", rec.Root, rec.Eph)
		}
	}
	for _, ln := range x9NewLines(t, root, "index/tool_use.jsonl", logsBefore) {
		var rec struct {
			ID   string `json:"id"`
			Root string `json:"root"`
		}
		require.NoError(t, json.Unmarshal(ln, &rec))
		if p := x9ObjectPath(rec.Root); p != "" {
			out[p] = fmt.Sprintf("index/tool_use.jsonl line (id=%s root=%s)", rec.ID, rec.Root)
		}
	}
	for rel, now := range x9CaptureSidecars(t, root) {
		if was, ok := capturesBefore[rel]; ok && bytes.Equal(was, now) {
			continue
		}
		var sc store.CaptureSidecar
		require.NoError(t, json.Unmarshal(now, &sc), "%s is not a capture sidecar", rel)
		if sc.Root.IsZero() {
			continue // a lifecycle delivery (session-start, flush) captures no content root
		}
		if p := x9ObjectPath(sc.Root.String()); p != "" {
			out[p] = fmt.Sprintf("capture sidecar %s (op=%s delivery=%s session=%s arrival=%d tool_use_id=%s published=%t)",
				rel, sc.Op, sc.Delivery, sc.Session, sc.Arrival, sc.ToolUseID, sc.Published)
		}
	}
	return out
}

// x9ToolUseLine is the slice of an index/tool_use.jsonl line these assertions read: a record's id,
// tool and turn, or a supersede mark's id and superseder.
type x9ToolUseLine struct {
	Op   string `json:"op"`
	ID   string `json:"id"`
	By   string `json:"by"`
	S    string `json:"s"`
	Tool string `json:"tool"`
	Turn int    `json:"turn"`
}

func x9ParseToolUseLines(t *testing.T, lines [][]byte) []x9ToolUseLine {
	t.Helper()
	out := make([]x9ToolUseLine, 0, len(lines))
	for _, ln := range lines {
		var rec x9ToolUseLine
		require.NoError(t, json.Unmarshal(ln, &rec))
		out = append(out, rec)
	}
	return out
}

// x9CountTool counts the index/tool_use.jsonl records whose tool is name; supersede marks carry no
// tool and are never counted.
func x9CountTool(t *testing.T, root, name string) int {
	t.Helper()
	n := 0
	for _, ln := range bytes.Split(x9ReadLog(t, root, "index/tool_use.jsonl"), []byte{'\n'}) {
		if len(bytes.TrimSpace(ln)) == 0 {
			continue
		}
		var rec x9ToolUseLine
		require.NoError(t, json.Unmarshal(ln, &rec))
		if rec.Tool == name {
			n++
		}
	}
	return n
}

// x9AckedDeliveries reads the acknowledged frontier, state/delivery-acks.jsonl (SP-20 publication
// order's third stage, internal/daemon/delivery_lease.go), as delivery token -> observation id.
func x9AckedDeliveries(t *testing.T, root string) map[string]core.ObservationID {
	t.Helper()
	out := map[string]core.ObservationID{}
	raw, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(root).State, "delivery-acks.jsonl")))
	require.NoError(t, err, "the acknowledged frontier must exist once the daemon has run")
	for _, ln := range bytes.Split(raw, []byte{'\n'}) {
		if len(bytes.TrimSpace(ln)) == 0 {
			continue
		}
		var rec struct {
			Delivery      string             `json:"delivery"`
			ObservationID core.ObservationID `json:"observation_id"`
		}
		require.NoError(t, json.Unmarshal(ln, &rec))
		out[rec.Delivery] = rec.ObservationID
	}
	return out
}

// x9Crossing is one content delivery that crossed the acknowledged frontier during the flush, as
// the capture sidecar the daemon wrote before it could acknowledge anything describes it.
type x9Crossing struct {
	Delivery  string
	Op        string
	Arrival   uint64
	Session   core.SessionID
	ToolUseID core.ToolUseID
}

func (c x9Crossing) String() string {
	return fmt.Sprintf("%s (op=%s arrival=%d session=%s tool_use_id=%s)",
		c.Delivery, c.Op, c.Arrival, c.Session, c.ToolUseID)
}

// x9ContentDeliveriesCrossed lists the content deliveries (observe.* ops) that were absent from
// the acknowledged frontier before the flush and present after it - the only deliveries a correct
// flush may publish records for. Each is described by its capture sidecar, which the daemon wrote
// before it could acknowledge anything (publication order's first stage). Lifecycle deliveries
// (the flush itself, session-start) cross the frontier too but publish no index record.
//
// A lifecycle delivery has no capture sidecar since the V6 close-out: a drained control line used to
// publish one, which nothing referenced and the publication audit could only call an unrecognized
// op (internal/daemon/drain.go dispatchPending). A crossing with no sidecar is therefore counted
// rather than described, and at most one may cross in this window - the flush's own, the only
// lifecycle hook the row sends after acksBefore was taken. A content delivery acknowledged without
// its sidecar would be a second one, and fails here as it always did.
func x9ContentDeliveriesCrossed(t *testing.T, root string, acksBefore map[string]core.ObservationID) []x9Crossing {
	t.Helper()
	var out []x9Crossing
	var withoutSidecar []string
	for delivery, id := range x9AckedDeliveries(t, root) {
		if _, was := acksBefore[delivery]; was {
			continue
		}
		sc, err := store.ReadCaptureSidecar(root, id)
		if errors.Is(err, fs.ErrNotExist) {
			withoutSidecar = append(withoutSidecar, fmt.Sprintf("%s (observation %s)", delivery, id))
			continue
		}
		require.NoError(t, err, "delivery %s crossed the frontier during flush with an unreadable capture sidecar for %s", delivery, id)
		if !strings.HasPrefix(sc.Op, "observe.") {
			continue
		}
		out = append(out, x9Crossing{
			Delivery: delivery, Op: sc.Op, Arrival: sc.Arrival, Session: sc.Session, ToolUseID: sc.ToolUseID,
		})
	}
	require.LessOrEqual(t, len(withoutSidecar), 1,
		"only the flush's own lifecycle delivery may cross the frontier during flush with no capture sidecar; "+
			"these did: %v", withoutSidecar)
	return out
}

// x9MatchCrossing returns the position in crossings of the delivery that published rec, or -1. A
// tool record carries the payload's tool_use_id, which LinkCaptureReference (observer/tooluse.go
// step 6a) stamped on its crossing's sidecar. SubagentStop and UserPromptSubmit records carry ids
// derived from session and turn (observer/stop.go SubagentCaptureID, observer/prompt.go
// VerbatimPromptID) because their payloads have no tool_use_id, so they match the crossing of
// their op in their session.
func x9MatchCrossing(crossings []x9Crossing, rec x9ToolUseLine) int {
	for i, c := range crossings {
		switch rec.Tool {
		case "SubagentStop":
			if c.Op == "observe.stop" && c.Session == core.SessionID(rec.S) {
				return i
			}
		case "UserPromptSubmit":
			if c.Op == "observe.prompt" && c.Session == core.SessionID(rec.S) {
				return i
			}
		default:
			if c.Op == "observe.tool" && c.ToolUseID == core.ToolUseID(rec.ID) {
				return i
			}
		}
	}
	return -1
}

// x9IndexOrdinals maps every content record id in index/tool_use.jsonl to the ordinal of the line
// that wrote it, which in an append-only index is the record's age: a smaller ordinal landed
// first. Supersede marks are mutation records and take no ordinal; a duplicate id keeps its first.
func x9IndexOrdinals(t *testing.T, root string) map[string]int {
	t.Helper()
	out := map[string]int{}
	for i, ln := range bytes.Split(x9ReadLog(t, root, "index/tool_use.jsonl"), []byte{'\n'}) {
		if len(bytes.TrimSpace(ln)) == 0 {
			continue
		}
		var rec x9ToolUseLine
		require.NoError(t, json.Unmarshal(ln, &rec))
		if rec.Op == "supersede" {
			continue
		}
		if _, dup := out[rec.ID]; !dup {
			out[rec.ID] = i
		}
	}
	return out
}

// x9NewLines returns the non-empty lines of rel that lie past the growth-only baseline in
// logsBefore. A log that shrank is reported here rather than sliced past its end; the growth-only
// assertion later in the test says the same thing on its own terms.
func x9NewLines(t *testing.T, root, rel string, logsBefore map[string][]byte) [][]byte {
	t.Helper()
	now := x9ReadLog(t, root, rel)
	was := logsBefore[rel]
	require.GreaterOrEqual(t, len(now), len(was), "%s shrank across flush", rel)
	var out [][]byte
	for _, ln := range bytes.Split(now[len(was):], []byte{'\n'}) {
		if len(bytes.TrimSpace(ln)) != 0 {
			out = append(out, ln)
		}
	}
	return out
}

// x9SessionEndPayload builds the SessionEnd event the flush subcommand consumes.
func x9SessionEndPayload(t *testing.T, root string) []byte {
	t.Helper()
	return x9Event(t, hookio.Event{HookEventName: "SessionEnd", SessionID: x9Session, CWD: root})
}

// x9Event marshals e the way a host writes a hook payload.
func x9Event(t *testing.T, e hookio.Event) []byte {
	t.Helper()
	b, err := json.Marshal(e)
	require.NoError(t, err)
	return b
}

// x9JSON marshals a string map for a tool_input/tool_response field.
func x9JSON(t *testing.T, v map[string]string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

// x9Snapshot fingerprints every regular file under each root: slash-relative path (prefixed by
// the root's index, so the project and HOME trees cannot alias) -> FNV-64a of the content. A file
// that cannot be read mid-walk is recorded by its error text, so a transiently locked file changes
// the fingerprint rather than hiding.
func x9Snapshot(t *testing.T, roots ...string) map[string]uint64 {
	t.Helper()
	out := map[string]uint64{}
	for i, root := range roots {
		longRoot := paths.Long(root)
		err := filepath.WalkDir(longRoot, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			rel, relErr := filepath.Rel(longRoot, p)
			if relErr != nil {
				return relErr
			}
			h := fnv.New64a()
			b, readErr := os.ReadFile(p)
			if readErr != nil {
				_, _ = h.Write([]byte(readErr.Error()))
			} else {
				_, _ = h.Write(b)
			}
			out[fmt.Sprintf("%d|%s", i, filepath.ToSlash(rel))] = h.Sum64()
			return nil
		})
		require.NoError(t, err)
	}
	return out
}

// x9AssertConfined asserts §13 invariant 7's write set over two x9Snapshot results taken with
// roots (projectRoot, home) in that order: every path that was created, modified OR deleted
// between them lives under <projectRoot>/.qompack/ or <home>/.qompack/. Nothing else on disk
// changed.
func x9AssertConfined(t *testing.T, before, after map[string]uint64, projectRoot, home string) {
	t.Helper()
	confined := func(key string) bool {
		return strings.HasPrefix(key, "0|.qompack/") || strings.HasPrefix(key, "1|.qompack/")
	}
	name := func(key string) string {
		if strings.HasPrefix(key, "0|") {
			return filepath.Join(projectRoot, filepath.FromSlash(key[2:]))
		}
		return filepath.Join(home, filepath.FromSlash(key[2:]))
	}
	for key, sum := range after {
		was, existed := before[key]
		if !existed && !confined(key) {
			t.Errorf("write-set: %s was CREATED outside .qompack/", name(key))
		}
		if existed && was != sum && !confined(key) {
			t.Errorf("write-set: %s was MODIFIED outside .qompack/", name(key))
		}
	}
	for key := range before {
		if _, still := after[key]; !still && !confined(key) {
			t.Errorf("write-set: %s was DELETED outside .qompack/", name(key))
		}
	}
}

// x9ReadLogs reads every x9GrowthLogs file that exists right now, keyed by its .qompack-relative
// name. A log that does not exist yet is simply absent from the map.
func x9ReadLogs(t *testing.T, root string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, rel := range x9GrowthLogs {
		b := x9ReadLog(t, root, rel)
		if b != nil {
			out[rel] = b
		}
	}
	return out
}

// x9ReadLog reads one .qompack-relative log, returning nil when it does not exist.
func x9ReadLog(t *testing.T, root, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(root).Dot, filepath.FromSlash(rel))))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	return b
}

// x9ListFiles returns the sorted slash-relative paths of every regular file under dir; a missing
// dir is an empty listing.
func x9ListFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	longDir := paths.Long(dir)
	err := filepath.WalkDir(longDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(longDir, p)
		if relErr != nil {
			return relErr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	return out
}

// x9FlushGCBound bounds the wait for the flush's "observer: gc" line when pending spool lines were
// left unconsumed before the flush. The line comes from the SessionEnd of a daemon the flush itself
// spawns, and that daemon first replays the spool in its startup drain (internal/daemon/daemon.go
// Run: WAL segments, then the hooks' client spools, the flush's own among them) before it serves
// anything, one line at a time under daemon.DrainLineDeadline, the daemon's own unit for how long a
// drain may take. The flush's SessionEnd, GC included, is replayed there as one more line. So the
// bound is a daemon start-up and shutdown (e2eHistoryConvergeBound, what the row waited under
// before) plus pending+1 lines at DrainLineDeadline each. With an idle host the pre-flush shutdown
// drains nearly everything and this is within a few lines of the old bound. Under -race with CPU and
// fsync co-load on Linux the pre-flush shutdown left about 170 unpublished WAL lines and 185 hooks'
// client spools, the replay took about 0.75 s a line, and the GC line came 3 m 20 s and 4 m 4 s after
// the flush, with every other assertion of the row passing: the old 32 s bound was wrong for the work
// the row waits for, not a symptom of a lost SessionEnd.
func x9FlushGCBound(pending int) time.Duration {
	return e2eHistoryConvergeBound + time.Duration(pending+1)*daemon.DrainLineDeadline
}

// x9UnconsumedSpoolLines counts the lines in root's spool that no drain has consumed yet: every
// line of a file state/drain.json does not name, and the lines past its recorded offset in one it
// does. Each is one line a daemon's startup drain replays (x9FlushGCBound).
func x9UnconsumedSpoolLines(t *testing.T, root string) int {
	t.Helper()
	l := paths.Of(root)
	consumed := map[string]int64{}
	raw, err := os.ReadFile(paths.Long(filepath.Join(l.State, "drain.json")))
	if err == nil {
		var st map[string]struct {
			Offset int64 `json:"offset"`
		}
		require.NoError(t, json.Unmarshal(raw, &st), "state/drain.json must parse")
		for name, rec := range st {
			consumed[name] = rec.Offset
		}
	} else {
		require.ErrorIs(t, err, fs.ErrNotExist, "state/drain.json must be readable")
	}
	entries, err := os.ReadDir(paths.Long(l.Spool))
	require.NoError(t, err)
	n := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".ndjson") {
			continue
		}
		b, readErr := os.ReadFile(paths.Long(filepath.Join(l.Spool, e.Name())))
		require.NoError(t, readErr)
		if off := consumed[e.Name()]; off > 0 && off <= int64(len(b)) {
			b = b[off:]
		}
		n += bytes.Count(b, []byte{'\n'})
	}
	return n
}

// x9AwaitNoSpawnInFlight waits until root holds no spawn.lock the product still counts as a spawn in
// flight (e2eSpawnInFlight). No hook runs while it waits, and only a hook's lazy spawn makes a claim,
// so a claim present now lapses within e2eSpawnLockStaleAfter of now; the bound adds one tick of
// polling to that.
func x9AwaitNoSpawnInFlight(t *testing.T, root string) {
	t.Helper()
	require.Eventually(t, func() bool { return !e2eSpawnInFlight(root) },
		e2eSpawnLockStaleAfter+e2eLazySpawnSettleTick, e2eLazySpawnSettleTick,
		"a spawn.lock claim stayed fresh past %s with no hook running", e2eSpawnLockStaleAfter)
}

// x9LastGCLine scans the project's day logs for the observer's SessionEnd GC line
// (internal/observer/session.go step 6: msg="observer: gc" scanned=… deleted=… …) and returns the
// last one found. It reads with os.ReadFile only — the daemon may still hold the day log's append
// handle when this polls.
func x9LastGCLine(root string) (line string, found bool) {
	entries, err := os.ReadDir(paths.Long(paths.Of(root).Logs))
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		b, readErr := os.ReadFile(paths.Long(filepath.Join(paths.Of(root).Logs, e.Name())))
		if readErr != nil {
			continue
		}
		for _, l := range strings.Split(string(b), "\n") {
			if strings.Contains(l, `msg="observer: gc"`) {
				line, found = l, true
			}
		}
	}
	return line, found
}

// x9BannedSymbolPrefixes are the method-symbol spellings of J3's three unconditionally banned
// packages. A Go binary that links a package carries its funcname table entries
// ("net/http.(*Client).Do" and the like), so their absence from the executable's bytes is the
// binary-level form of `devtool lint --only=importgraph`'s zero-non-test-imports assertion. The
// "(" suffix keeps the probe from matching an incidental string literal that merely mentions the
// package path.
var x9BannedSymbolPrefixes = []string{"net/http.(", "net/url.(", "crypto/tls.("}

// x9AssertBinaryHasNoNetworkImports re-runs J3's import assertions over the built binary itself.
func x9AssertBinaryHasNoNetworkImports(t *testing.T, bin string) {
	t.Helper()
	b, err := os.ReadFile(bin)
	require.NoError(t, err)
	for _, sym := range x9BannedSymbolPrefixes {
		require.False(t, bytes.Contains(b, []byte(sym)),
			"the built binary links a banned network package: its symbol table carries %q (J3, §13 invariant 7)", sym)
	}
}
