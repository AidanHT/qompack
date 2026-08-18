// appendonly_test.go implements plans/V2-VERIFY-primitives-store-dag-and-baseline.md §4.7: the
// store, the daemon and the append-only invariant under concurrency. Both tests run every
// dependency real — the §5.8 store with the real chunker/canonicalizers/redactor, the real
// dag.Graph, the real daemon.SketchSet, and (in the first test) the real daemon over the real IPC
// transport — because the seam under test is exactly the one no single wave-1 branch could reach:
// many writers and the idle maintenance work (GC, Compact) sharing one .qompack/.
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/chunk"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/redact"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/symbols"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/qompack/qompack/internal/tokens"
)

// The on-disk names this file observes. Each mirrors the single authoritative constant of the
// package that owns the file — cited per name — because those constants are unexported and the
// names are on-disk contract (Qompack.md §7.4, and the store's own state files):
//
//   - aoDepsLogName    = internal/dag/graph.go depsLogName   ("dag/deps.jsonl # dependence edges")
//   - aoGCStateName    = internal/store/gcrun.go gcStateFile (the resumable GC cursor)
//   - aoTriedBloomName = internal/daemon/sketchset.go triedSketchFile (§3.3 append-only)
//   - aoChunkCacheName = internal/store/open.go chunkCacheFile (the exact estimator's cache)
const (
	aoDepsLogName    = "deps.jsonl"
	aoGCStateName    = "gc.json"
	aoTriedBloomName = "tried.bloom"
	aoChunkCacheName = "chunktokens.bin"
)

// aoRealStore opens the real content-addressed store over root with every §5.8 dependency real
// and none faked: the FastCDC chunker from configuration, the SP-04 canonicalizer registry, the
// real symbol extractor, the exact token estimator, and the real redactor — the same set
// internal/store's defaultDeps installs, spelled out so this file cannot silently regress to a
// stub if a default ever changes.
func aoRealStore(t *testing.T, root string, cfg config.Config, clk core.Clock, log logging.Logger) store.Store {
	t.Helper()
	s, err := store.Open(root, cfg, store.Deps{
		Chunker: chunk.New(chunk.FromConfig(cfg)),
		Canon:   canon.Default(cfg.Store.Canonicalize),
		Symbols: symbols.New(),
		Tokens:  tokens.NewExact(cfg, tokens.DefaultCalibPath(), filepath.Join(paths.Of(root).State, aoChunkCacheName)),
		Redact:  redact.New(cfg),
		Log:     log,
		Clock:   clk,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// aoObjectSet returns the relative slash-separated path of every file under root's objects/
// directory, sorted, so two stores' object trees can be compared as sets. It returns rather than
// asserts, because callers on non-test goroutines may not touch *testing.T.
func aoObjectSet(root string) ([]string, error) {
	base := paths.Long(paths.Of(root).Objects)
	var out []string
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil || d.IsDir() {
			return werr
		}
		rel, rerr := filepath.Rel(base, p)
		if rerr != nil {
			return rerr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// aoErrList collects failure descriptions from goroutines that must not call into *testing.T
// (require.* may only stop the test goroutine). The main goroutine asserts the list is empty
// after the workers have been joined.
type aoErrList struct {
	mu   sync.Mutex
	errs []string
}

func (l *aoErrList) add(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errs = append(l.errs, fmt.Sprintf(format, args...))
}

func (l *aoErrList) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.errs...)
}

// ─────────────────────────────────────────────────────────────────────────────────────────────
// §4.7 test 1 — TestIntegration_AppendOnlyHoldsUnderConcurrentDaemonWrites
// ─────────────────────────────────────────────────────────────────────────────────────────────

// The workload shape: 8 concurrent sessions × 200 events each, through the real daemon's
// ObserveTool binding, over one .qompack/.
const (
	cdwSessions         = 8 // == config.Defaults().Runtime.Daemon.MaxSessions, deliberately at the ceiling
	cdwEventsPerSession = 200
	cdwTotalEvents      = cdwSessions * cdwEventsPerSession
	// cdwSharedFiles is how many distinct project paths the events cycle over. Shared across all
	// eight sessions, so the store's per-path indices, the DAG's file nodes and the CMS/HLL keys
	// all genuinely contend.
	cdwSharedFiles = 20
	// cdwSharedLineRepeat pads every event body with repeated per-file lines, so chunk-level
	// dedup has real material to work on while each body stays unique at the root level.
	cdwSharedLineRepeat = 20
	// cdwPosStride spaces the token positions of consecutive turns, so the DAG's position index
	// covers a non-trivial range.
	cdwPosStride = 40
	// cdwTombstonedSessions is how many of the eight sessions' tool-use/tool-result nodes are
	// tombstoned to provoke the single sanctioned Compact rewrite. Each event appends 7 records
	// (four nodes — tool use, tool result, assistant, file re-add — and three edges), so the log
	// holds ~11,200 records; tombstoning four sessions' tool-use/tool-result nodes makes
	// 4×200×2 dead nodes plus three dangling edges per tombstoned event, waste of 4,000 —
	// past dag's quarter-of-the-log threshold with margin even after the tombstone records
	// themselves are flushed.
	cdwTombstonedSessions = 4
)

// The idle task this test registers on the real daemon's IdleController: one tick runs store.GC
// with a deadline and dag.Compact, exactly the O3 shape SP-12 will register for real. The name
// carries no "act." prefix on purpose — GC and log compaction are maintenance work that §12.1
// keeps running in degraded-passive.
const (
	cdwIdleTaskName = "v2.gc-compact"
	// cdwIdleTaskPrio slots after SP-05's own three tasks (drain=10, sketches=20, metrics=30).
	cdwIdleTaskPrio = 40
	// cdwGCTickDeadline is the GCPolicy.Deadline each tick grants. It is a POLICY INPUT to the
	// subject under test — short enough that a sweep over this store can truncate mid-pass and
	// exercise the resumable-cursor path under load, long enough to make progress — not a test
	// wait bound, so it is not derived from internal/daemon/timing.go. Per carried defect SP06-D1
	// nothing here asserts wall-clock compliance with it.
	cdwGCTickDeadline = 25 * time.Millisecond
	// cdwGCGateEvents gates the FIRST idle tick's GC until a quarter of the events have already
	// been observed, which guarantees the pass runs while the remaining three quarters are still
	// being written — deterministic concurrency between GC and the writers rather than a race
	// the scheduler may or may not produce.
	cdwGCGateEvents = cdwTotalEvents / 4
)

// Every wait in this test is derived from internal/daemon/timing.go's exported guarantees, never
// a bare literal, so a change to the daemon's own timing moves these bounds with it.
const (
	// cdwStartBound bounds waiting for the daemon to accept connections: a generous multiple of
	// the spawning side's own definition of "it never came up".
	cdwStartBound = 4 * daemon.SpawnPollBound
	// cdwPhaseBound bounds each whole phase of the test. Two full idle-tick fallback periods:
	// if a spooled event has not been drained by then, the mechanism this test waits on (the
	// drain this test itself keeps triggering, with IdleTickMax as the daemon's own fallback)
	// is broken, not slow.
	cdwPhaseBound = 2 * daemon.IdleTickMax
	// cdwStopBound bounds a clean shutdown: twice Stop's own bound on draining in-flight work.
	cdwStopBound = 2 * daemon.StopDrainBound
	// cdwReplyDeadline is the per-request connect/ACK budget for this test's own clients.
	// One drained line's worst case is the unit any single-request bound is built from.
	cdwReplyDeadline = daemon.DrainLineDeadline
	// cdwIdleBudget is the budget each test-driven idle tick grants RunOnce. Generous — the whole
	// phase bound — because the drain task inside a tick may legitimately pick up in-flight WAL
	// lines and dispatch them inline, and a starved per-task context would make the drain consume
	// a line and then kill its dispatch midway (drainFile advances past a dispatched line
	// unconditionally), turning a tight test budget into data loss the daemon's real idle loop —
	// which runs only on a quiet session — never risks.
	cdwIdleBudget = cdwPhaseBound
	// cdwPollTick and cdwProbeDial are poll cadences, not semantic bounds: how often a waiting
	// loop re-checks, and how long one liveness probe dial may take.
	cdwPollTick  = 25 * time.Millisecond
	cdwProbeDial = 100 * time.Millisecond
)

// cdwSessionID and cdwEventID are the identity scheme the binding parses back: session "sess-<n>"
// and tool use "tu-<session>-<index>".
func cdwSessionID(si int) core.SessionID {
	return core.SessionID(fmt.Sprintf("sess-%d", si))
}

func cdwEventID(si, i int) core.ToolUseID {
	return core.ToolUseID(fmt.Sprintf("tu-%d-%03d", si, i))
}

// cdwEventPath is the project path event (si, i) touches: one of cdwSharedFiles paths shared by
// every session.
func cdwEventPath(_, i int) string {
	return fmt.Sprintf("src/shared/f%02d.ts", i%cdwSharedFiles)
}

// cdwEventBody builds one event's tool output: a unique head line so every root is novel, then
// repeated per-file lines so chunk-level dedup is real work rather than a no-op.
func cdwEventBody(si, i int) string {
	unique := fmt.Sprintf("tool output for session %d event %03d\n", si, i)
	shared := strings.Repeat(
		fmt.Sprintf("shared diagnostic line for file f%02d: compile ok, tests green, nothing to report\n",
			i%cdwSharedFiles),
		cdwSharedLineRepeat)
	return unique + shared
}

// cdwEvent renders the hook payload for event (si, i) the way a PostToolUse hook would carry it.
func cdwEvent(t *testing.T, root string, si, i int) hookio.Event {
	t.Helper()
	body, err := json.Marshal(cdwEventBody(si, i))
	require.NoError(t, err)
	return hookio.Event{
		HookEventName: "PostToolUse",
		SessionID:     cdwSessionID(si),
		CWD:           root,
		ToolName:      "Read",
		ToolUseID:     cdwEventID(si, i),
		ToolResponse:  body,
	}
}

// cdwBinding is the test-local ObserveTool implementation the §4.2 seam exists for: it performs
// exactly the §8.1 pipeline SP-08 will later own — store.PutBytes → store.RecordToolUse →
// store.AppendFileVersion → dag.BuildToolUse → cms/hll adds — and tracks which events it has
// observed. It runs on the daemon's worker pool, many goroutines at once.
type cdwBinding struct {
	store    store.Store
	graph    dag.Graph
	sketches *daemon.SketchSet
	clk      core.Clock
	errs     *aoErrList

	// gate closes once cdwGCGateEvents distinct events have been observed; the idle task's first
	// GC waits on it so the pass provably overlaps the remaining writes.
	gate     chan struct{}
	gateOnce sync.Once

	mu       sync.Mutex
	observed map[core.ToolUseID]bool
	calls    int
}

func newCDWBinding(s store.Store, g dag.Graph, sk *daemon.SketchSet, clk core.Clock, errs *aoErrList) *cdwBinding {
	return &cdwBinding{
		store: s, graph: g, sketches: sk, clk: clk, errs: errs,
		gate:     make(chan struct{}),
		observed: map[core.ToolUseID]bool{},
	}
}

func (b *cdwBinding) observedCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.observed)
}

func (b *cdwBinding) markObserved(id core.ToolUseID) {
	b.mu.Lock()
	b.observed[id] = true
	b.calls++
	n := len(b.observed)
	b.mu.Unlock()
	if n >= cdwGCGateEvents {
		b.gateOnce.Do(func() { close(b.gate) })
	}
}

// observeTool is the bound Services.ObserveTool. Failures are recorded rather than asserted —
// this runs on daemon goroutines — and the main goroutine requires the list empty at the end.
func (b *cdwBinding) observeTool(ctx context.Context, e hookio.Event) error {
	var si, i int
	if _, err := fmt.Sscanf(string(e.ToolUseID), "tu-%d-%d", &si, &i); err != nil {
		b.errs.add("unparseable tool_use_id %q: %v", e.ToolUseID, err)
		return err
	}
	var body string
	if err := json.Unmarshal(e.ToolResponse, &body); err != nil {
		b.errs.add("event %s: undecodable tool_response: %v", e.ToolUseID, err)
		return err
	}

	turn := core.TurnIndex(si*cdwEventsPerSession + i)
	path := cdwEventPath(si, i)
	key := paths.Key(path)
	now := core.NowMilli(b.clk)

	res, err := b.store.PutBytes(ctx, []byte(body), store.PutOptions{Tool: e.ToolName, Path: path})
	if err != nil {
		b.errs.add("event %s: PutBytes: %v", e.ToolUseID, err)
		return err
	}
	if err := b.store.RecordToolUse(ctx, store.ToolUseRecord{
		ID: e.ToolUseID, Session: e.SessionID, Turn: turn, TS: now, Tool: e.ToolName,
		ArgsDigest: core.HashBytes(core.DomainArgs, []byte(path)), ArgsPreview: path,
		Root: res.Root.Hash, Path: path, Bytes: res.Root.CanonBytes, Tokens: res.Root.Tokens,
		Signature: res.Signature,
	}); err != nil {
		b.errs.add("event %s: RecordToolUse: %v", e.ToolUseID, err)
		return err
	}
	if err := b.store.AppendFileVersion(ctx, path, store.FileVersion{
		TS: now, Root: res.Root.Hash, Turn: turn, Bytes: res.Root.CanonBytes,
	}); err != nil {
		b.errs.add("event %s: AppendFileVersion: %v", e.ToolUseID, err)
		return err
	}
	if err := dag.BuildToolUse(b.graph, dag.ObservedTool{
		ToolUseID: e.ToolUseID, Turn: turn, TS: now, Pos: int(turn) * cdwPosStride,
		Tool: e.ToolName, PathKey: key, Root: res.Root.Hash, Tokens: res.Root.Tokens,
	}); err != nil {
		b.errs.add("event %s: BuildToolUse: %v", e.ToolUseID, err)
		return err
	}
	b.sketches.Write(func(ss *daemon.SketchSet) {
		ss.Touch.Add([]byte(key), 1)
		ss.Explore.Add([]byte(key))
	})

	b.markObserved(e.ToolUseID)
	return nil
}

// cdwIdleWork is the registered idle task: one tick = store.GC with a deadline + dag.Compact.
// Reports and errors are recorded for the main goroutine to assert on.
type cdwIdleWork struct {
	store store.Store
	graph dag.Graph
	gate  <-chan struct{}
	errs  *aoErrList

	gated atomic.Bool

	mu      sync.Mutex
	reports []store.GCReport
}

func (w *cdwIdleWork) reportCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.reports)
}

func (w *cdwIdleWork) run(ctx context.Context) error {
	if w.gated.CompareAndSwap(false, true) {
		// First invocation only: wait until a quarter of the events have landed, so this GC pass
		// provably runs while the writers are still writing. On a host so slow the tick budget
		// expires first, skip this tick rather than run GC under an already-expired context —
		// a later tick still runs it, and nothing below asserts WHICH tick did.
		select {
		case <-w.gate:
		case <-ctx.Done():
			return nil
		}
	}
	rep, err := w.store.GC(ctx, store.GCPolicy{Deadline: cdwGCTickDeadline})
	if err != nil {
		w.errs.add("idle store.GC: %v", err)
		return err
	}
	w.mu.Lock()
	w.reports = append(w.reports, rep)
	w.mu.Unlock()

	if err := w.graph.Compact(ctx); err != nil {
		w.errs.add("idle dag.Compact: %v", err)
		return err
	}
	return nil
}

// cdwTick runs one idle tick on the daemon's own IdleController — the same RunOnce that Run's
// idle ticker and the admin.idle route drive — and asserts the registered GC+Compact task ran.
// The tick is driven directly rather than through admin.idle so the test controls WHEN idle work
// happens (the daemon's own trigger is a 120s quiet period this test must not wait out) and so
// the tick's budget is cdwIdleBudget rather than the operator route's, which under -race load is
// tight enough to kill an inline drain dispatch midway.
func cdwTick(t *testing.T, d daemon.Daemon) []string {
	t.Helper()
	ran, err := d.Idle().RunOnce(context.Background(), cdwIdleBudget)
	require.NoError(t, err)
	require.Contains(t, ran, cdwIdleTaskName, "every idle tick must run the registered GC+Compact task")
	return ran
}

// cdwFileSize returns p's size, with 0 for a file that does not exist yet.
func cdwFileSize(t *testing.T, p string) int64 {
	t.Helper()
	info, err := os.Stat(paths.Long(p))
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(t, err)
	return info.Size()
}

// cdwRequireDirEmpty asserts dir exists and holds nothing — the "untouched" shape for a directory
// nothing in this run may write to.
func cdwRequireDirEmpty(t *testing.T, dir, what string) {
	t.Helper()
	entries, err := os.ReadDir(paths.Long(dir))
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	require.Empty(t, names, "%s must be untouched by the run (§3.3)", what)
}

// cdwAssertJSONLParse walks every *.jsonl under .qompack and asserts every line parses as JSON.
// It also asserts the walk really covered the two logs this test grows hardest — the roots index
// and the dependence log — so the check can never pass vacuously.
func cdwAssertJSONLParse(t *testing.T, root string) {
	t.Helper()
	l := paths.Of(root)
	seen := map[string]bool{}
	err := filepath.WalkDir(paths.Long(l.Dot), func(p string, d fs.DirEntry, werr error) error {
		if werr != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return werr
		}
		rel, rerr := filepath.Rel(paths.Long(l.Dot), p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		seen[rel] = true
		b, rerr2 := os.ReadFile(p)
		require.NoError(t, rerr2)
		for n, line := range strings.Split(string(b), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			require.True(t, json.Valid([]byte(line)), "%s line %d is not valid JSON: %q", rel, n+1, line)
		}
		return nil
	})
	require.NoError(t, err)
	require.True(t, seen["index/roots.jsonl"], "the walk must have covered index/roots.jsonl; saw %v", seen)
	require.True(t, seen["dag/"+aoDepsLogName], "the walk must have covered dag/%s; saw %v", aoDepsLogName, seen)
}

func TestIntegration_AppendOnlyHoldsUnderConcurrentDaemonWrites(t *testing.T) {
	p := testutil.NewProject(t)
	l := paths.Of(p.Root)
	ctx := context.Background()

	// Seed sketches/tried.bloom through its ONE sanctioned door before anything runs, so
	// "untouched" below is an assertion about real bytes surviving, not about a file that never
	// existed. A real, decodable Bloom, so the daemon's own SketchSet.Load succeeds over it.
	seedBloom := sketch.NewBloom(p.Cfg.Sketches.Bloom.Capacity, p.Cfg.Sketches.Bloom.FPRate)
	seedBloom.Add([]byte("v2-verify-seed-key"))
	triedPath := filepath.Join(l.Sketches, aoTriedBloomName)
	_, err := sketch.ReplaceGenerational(triedPath, seedBloom, 1)
	require.NoError(t, err)
	triedBefore, err := os.ReadFile(paths.Long(triedPath))
	require.NoError(t, err)

	// The real L1, the real DAG and the real sketch set, all over the one project root.
	s := aoRealStore(t, p.Root, p.Cfg, p.Clock, p.Log)
	g, err := dag.Open(p.Root, p.Cfg, p.Log)
	require.NoError(t, err)
	maint, ok := g.(dag.Maintainer)
	require.True(t, ok, "dag.Open's graph must implement dag.Maintainer")
	maint.SetClock(p.Clock)
	sketches := daemon.NewSketchSet(p.Cfg)

	taskErrs := &aoErrList{}
	binding := newCDWBinding(s, g, sketches, p.Clock, taskErrs)
	idleWork := &cdwIdleWork{store: s, graph: g, gate: binding.gate, errs: taskErrs}

	// The real daemon, composed through the §4.2 extension seam SP-05 shipped for exactly this:
	// daemon.NewOptions + Options.Bind.
	opts := daemon.NewOptions(p.Root, p.Cfg)
	opts.Log = p.Log
	opts.Clock = p.Clock
	opts.Store = s
	opts.Graph = g
	opts.Sketches = sketches
	opts.Bind(func(sv *daemon.Services) { sv.ObserveTool = binding.observeTool })
	d, err := daemon.New(opts)
	require.NoError(t, err)
	d.Idle().Register(cdwIdleTaskName, cdwIdleTaskPrio, idleWork.run)

	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	runDone := make(chan struct{})
	var runResult error
	go func() {
		runResult = d.Run(runCtx)
		close(runDone)
	}()
	t.Cleanup(func() {
		cancelRun()
		select {
		case <-runDone:
		case <-time.After(cdwStopBound):
			t.Errorf("daemon.Run did not return within %v of cancellation", cdwStopBound)
		}
	})

	addr, err := ipc.Resolve(p.Root)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return ipc.Probe(addr, cdwProbeDial) },
		cdwStartBound, cdwPollTick, "the daemon never started accepting connections at %v", addr)

	// One real ipc.Client shared by all writers (Send is safe for concurrent use; the spool
	// writer carries its own lock), so a transient ACK failure degrades to the spool-and-drain
	// path instead of losing an event.
	spool, err := ipc.NewSpool(l.Spool)
	require.NoError(t, err)
	client := ipc.NewClientWithOptions(addr, spool, p.Log, nil, ipc.ClientOptions{
		State:           ipc.State{Mode: contract.ModeFull, DaemonEnabled: true},
		ConnectDeadline: cdwReplyDeadline,
		AckDeadline:     cdwReplyDeadline,
	})
	t.Cleanup(func() { _ = client.Close() })

	// Phase A: eight concurrent sessions, 200 events each, racing the idle ticks this goroutine
	// keeps firing. The idle task's own gate guarantees at least one GC+Compact pass runs while
	// three quarters of the events are still in flight.
	sendErrs := &aoErrList{}
	var wg sync.WaitGroup
	writersDone := make(chan struct{})
	for si := 0; si < cdwSessions; si++ {
		wg.Add(1)
		go func(si int) {
			defer wg.Done()
			sess := cdwSessionID(si)
			for i := 0; i < cdwEventsPerSession; i++ {
				ev := cdwEvent(t, p.Root, si, i)
				req := ipc.Request{Op: ipc.OpObserveTool, Session: sess, TS: core.NowMilli(p.Clock), Event: &ev}
				if _, serr := client.Send(ctx, req, cdwReplyDeadline); serr != nil {
					sendErrs.add("session %d event %d: Send: %v", si, i, serr)
					return
				}
			}
		}(si)
	}
	go func() {
		wg.Wait()
		close(writersDone)
	}()

	depsPath := filepath.Join(l.DAG, aoDepsLogName)
	var sizes []int64
	var gens []int
	sample := func() {
		sizes = append(sizes, cdwFileSize(t, depsPath))
		gens = append(gens, g.Stats().Generation)
	}
	// The tick runs BEFORE the writers-done check, so at least one tick always happens while the
	// storm is in flight — and that first tick's gate (see cdwBinding.gate) holds its GC until a
	// quarter of the events have landed, which pins the pass to the middle of the write phase
	// rather than leaving the overlap to scheduler luck.
	phaseATicks := 0
	phaseAStart := time.Now()
	for {
		require.Less(t, time.Since(phaseAStart), cdwPhaseBound,
			"the writers did not finish within %v", cdwPhaseBound)
		cdwTick(t, d)
		phaseATicks++
		sample()
		done := false
		select {
		case <-writersDone:
			done = true
		default:
		}
		if done {
			break
		}
	}
	t.Logf("phase A: %d idle ticks completed concurrently with the writers", phaseATicks)

	// Every event must reach the binding; stragglers that fell back to the spool are picked up
	// by the drain task of the ticks this loop keeps firing.
	require.Eventually(t, func() bool {
		cdwTick(t, d)
		sample()
		return binding.observedCount() == cdwTotalEvents
	}, cdwPhaseBound, cdwPollTick,
		"not every event reached the ObserveTool binding (see the drain path, §2.4)")
	require.Empty(t, sendErrs.snapshot(), "no Send may fail hard")
	require.Empty(t, taskErrs.snapshot(), "no binding call and no idle GC/Compact may fail")
	require.Positive(t, idleWork.reportCount(), "store.GC must have run on the idle ticks")
	// Exactly once, not merely at-least-once: the ring workers and every drain share one
	// seen-set keyed over identical trimmed bytes (fix commit "fix(daemon): key live-vs-drain
	// dedup over the same trimmed bytes"). Before that fix this counted every event TWICE —
	// the WAL drain re-dispatched each live-dispatched line because ingest.Accept hashed the
	// line with its terminator while the drainer hashed it trimmed — which this assertion
	// pins against regressing.
	binding.mu.Lock()
	calls := binding.calls
	binding.mu.Unlock()
	require.Equal(t, cdwTotalEvents, calls,
		"every event must reach the ObserveTool binding exactly once (live-vs-drain dedup)")

	// Settle: one final tick so every pending dag record is flushed, then pin the pre-rewrite
	// facts: through the whole concurrent phase deps.jsonl only ever GREW and no rewrite happened.
	cdwTick(t, d)
	sample()
	sizePreTomb := cdwFileSize(t, depsPath)
	for i, gen := range gens {
		require.Zero(t, gen, "sample %d: no Compact rewrite may happen while nothing is tombstoned", i)
	}
	for i := 1; i < len(sizes); i++ {
		require.GreaterOrEqual(t, sizes[i], sizes[i-1],
			"dag/%s must grow monotonically outside the one sanctioned Compact rewrite (samples: %v)",
			aoDepsLogName, sizes)
	}
	require.GreaterOrEqual(t, sizePreTomb, sizes[len(sizes)-1])
	require.Positive(t, sizePreTomb, "the dependence log must actually have been written")

	// The single documented Compact rewrite: tombstone half the sessions' tool-use and
	// tool-result nodes, let the next idle tick compact, and require the Generation bump.
	var doomed []dag.NodeID
	for si := 0; si < cdwTombstonedSessions; si++ {
		for i := 0; i < cdwEventsPerSession; i++ {
			id := cdwEventID(si, i)
			doomed = append(doomed, dag.ToolUseNode(id), dag.ToolResultNode(id))
		}
	}
	require.NoError(t, maint.Tombstone(doomed))
	require.True(t, maint.NeedsCompaction(),
		"tombstoning %d of the graph's nodes must push waste past dag's rewrite threshold", len(doomed))
	require.Zero(t, maint.Generation())

	cdwTick(t, d)
	require.Equal(t, 1, maint.Generation(), "the sanctioned Compact rewrite must bump Generation exactly once")
	sizePostCompact := cdwFileSize(t, depsPath)
	require.Less(t, sizePostCompact, sizePreTomb,
		"the Compact rewrite must be the one write that SHRINKS dag/%s", aoDepsLogName)
	require.False(t, maint.NeedsCompaction())

	// One more tick proves the rewrite was singular: nothing further to compact, nothing written.
	cdwTick(t, d)
	require.Equal(t, 1, maint.Generation(), "a second tick must not produce a second rewrite")
	require.Equal(t, sizePostCompact, cdwFileSize(t, depsPath))
	require.Empty(t, taskErrs.snapshot())

	// Clean shutdown, then the §4.7 postconditions.
	require.NoError(t, d.Stop(ctx))
	select {
	case <-runDone:
	case <-time.After(cdwStopBound):
		t.Fatalf("daemon.Run did not return within %v of Stop", cdwStopBound)
	}
	require.NoError(t, runResult)

	// checkpoints/, pins/ and sketches/tried.bloom untouched — checked BEFORE AssertAppendOnly,
	// which itself seeds all three locations through the sanctioned doors as part of its probe.
	cdwRequireDirEmpty(t, l.Checkpoints, "checkpoints/")
	cdwRequireDirEmpty(t, l.Pins, "pins/")
	triedAfter, err := os.ReadFile(paths.Long(triedPath))
	require.NoError(t, err)
	require.Equal(t, triedBefore, triedAfter, "sketches/%s must survive the run byte for byte", aoTriedBloomName)
	sketchEntries, err := os.ReadDir(paths.Long(l.Sketches))
	require.NoError(t, err)
	for _, e := range sketchEntries {
		require.NotContains(t, e.Name(), ".bak",
			"no ReplaceBloom generation may have been displaced during the run")
	}

	// Every *.jsonl the run produced still parses, line by line.
	cdwAssertJSONLParse(t, p.Root)

	// The §3.3 append-only conformance list, against this live project.
	p.AssertAppendOnly(t)

	// store.Open on reopen reports the same Stats as before shutdown.
	statsBefore, err := s.Stats(ctx)
	require.NoError(t, err)
	require.Equal(t, cdwTotalEvents, statsBefore.ToolUses, "every event must have produced a tool_use record")
	require.Equal(t, cdwSharedFiles, statsBefore.Files)
	require.NoError(t, s.Close())

	reopened := aoRealStore(t, p.Root, p.Cfg, p.Clock, p.Log)
	statsAfter, err := reopened.Stats(ctx)
	require.NoError(t, err)
	require.Equal(t, statsBefore, statsAfter,
		"a reopened store must report exactly the Stats the closed one last reported")
	require.NoError(t, reopened.Close())
}

// ─────────────────────────────────────────────────────────────────────────────────────────────
// §4.7 test 2 — TestIntegration_GCNeverCollectsALiveRootUnderIngest
// ─────────────────────────────────────────────────────────────────────────────────────────────

const (
	// gciSeedRoots is how many collectible roots are planted before the clock advances past the
	// retention window. Far past the sweep's deadline-check interval (gcCheckEvery = 256 in
	// internal/store/gcrun.go), and with head-room for four bounded passes each retiring at most
	// one check interval's worth of objects, so EVERY bounded pass is structurally guaranteed to
	// meet a deadline check with work remaining and truncate.
	gciSeedRoots = 1200
	// gciPinnedRoots of those are held live by a checkpoint, so "what survives" is a set the test
	// chose, not "nothing".
	gciPinnedRoots = 50
	// gciBoundedPasses × gciBatchSize fresh roots are ingested during the run, one batch handed
	// off before each bounded pass; a final batch lands before the closing unbounded pass so that
	// EVERY pass restarts its mark phase (the live set changed every time) and the per-pass
	// reports stay independent — which is what makes their union comparable to one unbounded run.
	gciBoundedPasses = 4
	gciBatchSize     = 50
	// gciDisableSessions switches the session-retention axis off (negative = disabled, the same
	// convention the store's own GC tests force collection with); the day axis is inherited from
	// configuration and is what keeps the fresh roots alive.
	gciDisableSessions = -1
	// gciSweepFloor is the fixture-sanity minimum for on-disk objects before a bounded pass: past
	// the check interval, or truncation would be luck rather than structure.
	gciSweepFloor = 257
	// gciExpiredDeadline is a deadline that has already expired when the sweep starts: the pass
	// must stop at its FIRST deadline check, exactly as TestGC_DeadlineTruncatesAndResumes pins
	// in-package. Per carried defect SP06-D1 the tombstone phase before the sweep answers only to
	// ctx, so nothing here asserts wall-clock compliance — only Truncated/resume/no-live-collection.
	gciExpiredDeadline = time.Nanosecond
	// gciHoursPerDay converts the configured retention-days window into the clock advance that
	// retires the seeds.
	gciHoursPerDay = 24
)

// gciSeedBody and gciFreshBody keep every root's content unique, so object sets and deletion
// counts compare exactly between the concurrent store and the control store.
func gciSeedBody(i int) []byte {
	return []byte(fmt.Sprintf("seed body %04d — retired content, unique\n", i))
}

func gciSeedPath(i int) string { return fmt.Sprintf("src/old/f%04d.ts", i) }

func gciFreshBody(batch, j int) []byte {
	return []byte(fmt.Sprintf("fresh body %d-%02d — written during the gc run, unique\n", batch, j))
}

func gciFreshPath(batch, j int) string { return fmt.Sprintf("src/live/b%d/f%02d.ts", batch, j) }

// gciIngestBatch puts one batch of fresh roots into s, returning the roots, or an error message
// list — it runs on the ingester goroutine, which must not touch *testing.T.
func gciIngestBatch(ctx context.Context, s store.Store, batch int, errs *aoErrList) []store.Root {
	out := make([]store.Root, 0, gciBatchSize)
	for j := 0; j < gciBatchSize; j++ {
		res, err := s.PutBytes(ctx, gciFreshBody(batch, j),
			store.PutOptions{Tool: "FileRead", Path: gciFreshPath(batch, j)})
		if err != nil {
			errs.add("batch %d put %d: %v", batch, j, err)
			continue
		}
		out = append(out, res.Root)
	}
	return out
}

// gciSurvivalViolations reports every root in fresh that the store can no longer serve — the
// §4.7 invariant is that this list is empty after every pass, bounded or not.
func gciSurvivalViolations(ctx context.Context, s store.Store, fresh []store.Root) []string {
	var out []string
	for _, r := range fresh {
		if _, err := s.GetRoot(ctx, r.Hash); err != nil {
			out = append(out, fmt.Sprintf("live root %s was collected: %v", r.Hash.Short(), err))
			continue
		}
		for _, c := range r.Chunks {
			if !s.Has(c.Hash) {
				out = append(out, fmt.Sprintf("live root %s lost chunk %s", r.Hash.Short(), c.Hash.Short()))
			}
		}
	}
	return out
}

// gciGCState is the on-disk shape of the resumable cursor at .qompack/state/gc.json
// (internal/store/gcrun.go gcState); the two fields read here are what make "the mark phase
// restarted" observable from outside the package: a RESUMED pass carries the digest forward
// unchanged, a restarted one records the freshly recomputed digest.
type gciGCState struct {
	Phase      string `json:"phase"`
	LiveDigest string `json:"live_digest"`
}

func gciReadGCState(root string) (gciGCState, bool) {
	b, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(root).State, aoGCStateName)))
	if err != nil {
		return gciGCState{}, false
	}
	var st gciGCState
	if err := json.Unmarshal(b, &st); err != nil {
		return gciGCState{}, false
	}
	return st, true
}

// gciPassResult is what the collector goroutine records for each bounded pass, asserted on the
// main goroutine after the join.
type gciPassResult struct {
	rep           store.GCReport
	err           error
	state         gciGCState
	stateOK       bool
	objectsBefore int
	violations    []string
}

// gciPinCheckpoint plants checkpoints/0001.json through the sanctioned CreateNew door, holding
// the given hashes live for the mark phase.
func gciPinCheckpoint(t *testing.T, root string, hashes []string) {
	t.Helper()
	doc, err := json.Marshal(map[string]any{"seq": 1, "pointers": hashes})
	require.NoError(t, err)
	require.NoError(t, paths.CreateNew(paths.CheckpointPath(paths.Of(root), core.CheckpointSeq(1)), append(doc, '\n')))
}

func TestIntegration_GCNeverCollectsALiveRootUnderIngest(t *testing.T) {
	p := testutil.NewProject(t)
	ctx := context.Background()

	// Two identical stores: the one the concurrent run hammers, and a control that receives the
	// same content and one single unbounded GC — the yardstick "the union of reports" must equal.
	main := aoRealStore(t, p.Root, p.Cfg, p.Clock, p.Log)
	controlRoot := filepath.Join(t.TempDir(), "control")
	control := aoRealStore(t, controlRoot, p.Cfg, p.Clock, p.Log)

	// Seed both with identical collectible roots at the frozen Epoch, pin the first
	// gciPinnedRoots via a checkpoint, then advance the clock past the configured retention
	// window so the unpinned seeds age out while everything written from here on stays live.
	pinned := make([]string, 0, gciPinnedRoots)
	var seeds []store.Root
	for i := 0; i < gciSeedRoots; i++ {
		mr, err := main.PutBytes(ctx, gciSeedBody(i), store.PutOptions{Tool: "FileRead", Path: gciSeedPath(i)})
		require.NoError(t, err)
		cr, err := control.PutBytes(ctx, gciSeedBody(i), store.PutOptions{Tool: "FileRead", Path: gciSeedPath(i)})
		require.NoError(t, err)
		require.Equal(t, mr.Root.Hash, cr.Root.Hash,
			"identical content must be content-addressed identically in both stores")
		seeds = append(seeds, mr.Root)
		if i < gciPinnedRoots {
			pinned = append(pinned, mr.Root.Hash.String())
		}
	}
	gciPinCheckpoint(t, p.Root, pinned)
	gciPinCheckpoint(t, controlRoot, pinned)
	p.Clock.Advance(time.Duration(p.Cfg.Store.Retention.Days+1) * gciHoursPerDay * time.Hour)

	boundedPolicy := store.GCPolicy{RetainSessions: gciDisableSessions, Deadline: gciExpiredDeadline}
	unboundedPolicy := store.GCPolicy{RetainSessions: gciDisableSessions}

	// The run: an ingester goroutine and a GC goroutine share the store, handing off through
	// channels — real cross-goroutine concurrency for the race detector, with a deterministic
	// interleaving so "which roots were live at each mark" has one answer. Each batch lands
	// before its pass, so every pass's live set differs from the previous one and the persisted
	// cursor's digest can never match: every pass must RESTART its mark rather than resume a
	// stale one, which is exactly the §4.7 mechanism under test.
	ingestErrs := &aoErrList{}
	batches := make(chan []store.Root)
	swept := make(chan struct{})
	results := make([]gciPassResult, 0, gciBoundedPasses)
	var runFresh []store.Root // the ingester's cumulative output, read only after the join

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { // ingester
		defer wg.Done()
		defer close(batches)
		var fresh []store.Root
		for k := 0; k < gciBoundedPasses; k++ {
			batch := gciIngestBatch(ctx, main, k, ingestErrs)
			gciIngestBatch(ctx, control, k, ingestErrs) // the control mirrors every batch
			fresh = append(fresh, batch...)
			batches <- append([]store.Root(nil), fresh...)
			<-swept
		}
		runFresh = fresh
	}()
	go func() { // collector
		defer wg.Done()
		for fresh := range batches {
			var pr gciPassResult
			if objs, err := aoObjectSet(p.Root); err == nil {
				pr.objectsBefore = len(objs)
			}
			pr.rep, pr.err = main.GC(ctx, boundedPolicy)
			pr.state, pr.stateOK = gciReadGCState(p.Root)
			pr.violations = gciSurvivalViolations(ctx, main, fresh)
			results = append(results, pr)
			swept <- struct{}{}
		}
	}()
	wg.Wait()

	require.Empty(t, ingestErrs.snapshot(), "every ingest during the run must succeed")
	require.Len(t, results, gciBoundedPasses)
	allFresh := make([]store.Root, 0, gciBoundedPasses*gciBatchSize)
	var totalDeleted int
	var totalFreed int64
	for k, pr := range results {
		require.NoError(t, pr.err, "bounded pass %d: an expired deadline is a normal outcome, never an error", k)
		require.GreaterOrEqual(t, pr.objectsBefore, gciSweepFloor,
			"fixture sanity, pass %d: the sweep must meet a deadline check with work remaining", k)
		require.True(t, pr.rep.Truncated,
			"pass %d: an already-expired deadline over %d objects must truncate", k, pr.objectsBefore)
		require.True(t, pr.stateOK, "pass %d: a truncated pass must persist its cursor", k)
		require.Equal(t, "sweep", pr.state.Phase)
		require.NotEmpty(t, pr.state.LiveDigest)
		require.Empty(t, pr.violations,
			"pass %d: no root written during the run may ever be collected", k)
		if k > 0 {
			require.NotEqual(t, results[k-1].state.LiveDigest, pr.state.LiveDigest,
				"pass %d: a batch landed since the interrupted pass, so the recomputed live set "+
					"must differ and the mark phase must have restarted", k)
			require.Equal(t, results[0].rep.ScannedObjects, pr.rep.ScannedObjects,
				"pass %d: a RESTARTED pass sweeps from the top and stops at the same first check; "+
					"a cumulative count here would mean it resumed a stale cursor", k)
		}
		totalDeleted += pr.rep.DeletedObjects
		totalFreed += pr.rep.BytesFreed
	}
	require.Positive(t, results[0].rep.DeletedObjects, "a truncated pass must still have done real work")
	require.Len(t, runFresh, gciBoundedPasses*gciBatchSize)
	allFresh = append(allFresh, runFresh...)

	// One more batch before the closing pass, mirrored to the control, so the final pass's live
	// set has changed too: it must also restart, keeping its report independent of pass 4's
	// cursor and the arithmetic below honest.
	finalBatch := gciIngestBatch(ctx, main, gciBoundedPasses, ingestErrs)
	gciIngestBatch(ctx, control, gciBoundedPasses, ingestErrs)
	require.Empty(t, ingestErrs.snapshot())
	require.Len(t, finalBatch, gciBatchSize)
	allFresh = append(allFresh, finalBatch...)

	// A subsequent unbounded GC completes: no truncation, cursor cleared, every live root served.
	repFinal, err := main.GC(ctx, unboundedPolicy)
	require.NoError(t, err)
	require.False(t, repFinal.Truncated, "an unbounded pass must complete")
	require.NoFileExists(t, filepath.Join(paths.Of(p.Root).State, aoGCStateName),
		"a completed pass must clear its cursor")
	require.Empty(t, gciSurvivalViolations(ctx, main, allFresh),
		"no root written during the run may be collected by the closing unbounded pass either")
	totalDeleted += repFinal.DeletedObjects
	totalFreed += repFinal.BytesFreed

	// The pinned seeds survive; an unpinned seed is genuinely gone — collection was real.
	for i := 0; i < gciPinnedRoots; i++ {
		_, err := main.GetRoot(ctx, seeds[i].Hash)
		require.NoError(t, err, "checkpoint-pinned seed %d must survive", i)
	}
	_, err = main.GetRoot(ctx, seeds[gciSeedRoots-1].Hash)
	require.ErrorIs(t, err, core.ErrNotFound, "an aged-out, unpinned seed must have been collected")

	// The union property: the control store carries the identical final content and collects it
	// in ONE unbounded pass. The union of the concurrent run's reports must equal it — same
	// surviving object set, same cumulative deletions and bytes freed, same final live set.
	repControl, err := control.GC(ctx, unboundedPolicy)
	require.NoError(t, err)
	require.False(t, repControl.Truncated)
	require.Empty(t, gciSurvivalViolations(ctx, control, allFresh))

	mainObjects, err := aoObjectSet(p.Root)
	require.NoError(t, err)
	controlObjects, err := aoObjectSet(controlRoot)
	require.NoError(t, err)
	require.NotEmpty(t, mainObjects)
	require.Equal(t, controlObjects, mainObjects,
		"the interleaved passes must leave exactly the object set one unbounded pass leaves")
	require.Equal(t, repControl.DeletedObjects, totalDeleted,
		"the union of the run's reports must delete exactly what a single unbounded run deletes")
	require.Equal(t, repControl.BytesFreed, totalFreed)
	require.Equal(t, repControl.LiveObjects, repFinal.LiveObjects,
		"the final mark must agree with the control's on what is live")
	require.Equal(t, repControl.Roots, repFinal.Roots)
}
