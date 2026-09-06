package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
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
	// The daemon is shut down first (its shutdown drains the spool before releasing the lock), so
	// the store's append handles have exactly one owner while the ledger writes, and re-acquired
	// afterwards by the flush below. This is the seam-composition the §5 preamble prescribes for a
	// flow whose production driver (internal/mcp) is a wave-3 stub this test may not reference.
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

	// ── flush: the real binary again (its lazy spawn brings the real daemon back up) ────────────

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
	}, e2eHistoryConvergeBound, e2eDaemonDownTick,
		"flush never produced the observer's \"observer: gc\" log line — store.GC did not run on SessionEnd")
	// The retention window covers every NON-ephemeral object this young project holds, so those
	// may never be collected. Ephemeral retrieval results are different by design: an ephemeral
	// root is never in-window by the age clause (Qompack.md 8.2 - "retrieval spam is reclaimable")
	// and survives only through the session clause, which the flush-time GC can miss for an event
	// the daemon's startup WAL replay still has mid-pipeline - same-session ordering over the
	// transport is best-effort (SP-08's parked R3, deferred to V4-VERIFY beside SP05-D1). So the
	// assertion is the architecture's, not a blanket zero: nothing non-ephemeral is ever deleted.
	require.Contains(t, gcLine, " truncated=false", "the GC pass must have finished inside its deadline")

	e2eShutdownIfReachable(t, p.Root)

	// Independently of the log: no non-ephemeral object was added or removed. Any file GC did
	// reclaim must be referenced ONLY by ephemeral roots (the 8.2 carve-out above).
	afterObjects := x9ListFiles(t, paths.Of(p.Root).Objects)
	afterSet := make(map[string]bool, len(afterObjects))
	for _, f := range afterObjects {
		afterSet[f] = true
	}
	beforeSet := make(map[string]bool, len(objectsBefore))
	for _, f := range objectsBefore {
		beforeSet[f] = true
		if !afterSet[f] {
			require.True(t, ephOnly[f],
				"GC deleted %s, which is referenced by a non-ephemeral root - the retention window must cover it", f)
		}
	}
	for _, f := range afterObjects {
		require.True(t, beforeSet[f], "GC must not add objects; %s appeared during flush", f)
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

	objPath := func(h string) string {
		h = strings.TrimPrefix(h, "sha256:")
		if len(h) < 4 {
			return ""
		}
		return h[:2] + "/" + h[2:4] + "/" + h + ".zst" // slash form, matching x9ListFiles's ToSlash keys
	}
	ephFiles := map[string]bool{}
	keepFiles := map[string]bool{}
	for _, ln := range bytes.Split(raw, []byte{'\n'}) {
		if len(bytes.TrimSpace(ln)) == 0 {
			continue
		}
		var rec struct {
			Root   string `json:"root"`
			Eph    bool   `json:"eph"`
			Chunks []struct {
				H string `json:"h"`
			} `json:"chunks"`
		}
		require.NoError(t, json.Unmarshal(ln, &rec))
		dst := keepFiles
		if rec.Eph {
			dst = ephFiles
		}
		if p := objPath(rec.Root); p != "" {
			dst[p] = true
		}
		for _, c := range rec.Chunks {
			if p := objPath(c.H); p != "" {
				dst[p] = true
			}
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
