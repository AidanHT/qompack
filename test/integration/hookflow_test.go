package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/redact"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/symbols"
	"github.com/qompack/qompack/internal/testutil"
)

// This file is V2-VERIFY §4.2: hook event → daemon → store → tombstone → retrieval, exercised with
// every dependency real. The wiring uses the extension seam SP-05 shipped for exactly this purpose
// — daemon.Options.Bind(func(*daemon.Services)) — and the test-local binding below is what
// observer.OnToolUse replaces in V3.
//
// Two §4.2 spellings are adapted to the shipped APIs, deliberately and narrowly:
//
//   - "testObserveTool … returns hookio.Empty()": daemon.Services.ObserveTool is
//     func(ctx, hookio.Event) error — it has no Output to return. The `{}` the host sees is the
//     daemon route's own ACK; the binding returns nil, which is the same statement at this seam.
//   - "Continue from the store above": each test builds its own §4.2 store/graph/sketches through
//     the one shared constructor (newHookflowPipeline), so every test stays runnable in isolation
//     under `go test -run` — the construction is identical, the instance is not shared.
//
// TestIntegration_TombstoneRoundTripThroughStore and
// TestIntegration_SupersessionMarksEarlierReadThroughDAG drive the bound function directly — it is
// literally the same func value a daemon dispatch would call — because their assertions read the
// store and the DAG, not the transport; the transport half is what
// TestIntegration_HookEventThroughDaemonToStore and
// TestIntegration_SpooledEventsSurviveToTheStore exist to cover with the real ipc.Client and the
// real binary.

// The wall-clock bounds this file waits under. Per V2-MERGE-25 ② (and test/e2e's bounds block,
// which this mirrors), every bound derives from the exported constant in internal/daemon/timing.go
// that governs the mechanism being waited on — never from a bare round number — so a daemon timing
// change moves every derived bound with it, and a timeout here names a broken mechanism rather
// than a slow machine. The *Tick values are poll cadences, not bounds.
const (
	// hookflowProbeDial bounds one liveness probe's dial. Basis: internal/cli's
	// hookConnectDeadlineFloor (250ms), the smallest budget in this tree documented as sufficient
	// for a cold endpoint — the same basis test/e2e's e2eProbeTimeout states.
	hookflowProbeDial = 250 * time.Millisecond

	// hookflowDaemonUpBound waits for the in-process daemon's transport to become reachable.
	// Basis: daemon.SpawnPollBound, the spawning side's own definition of "it never came up",
	// with test/e2e's 8× headroom for a loaded host.
	hookflowDaemonUpBound = 8 * daemon.SpawnPollBound
	hookflowDaemonUpTick  = 20 * time.Millisecond

	// hookflowClientBudget is the connect/ACK budget the in-test ipc.Client runs with, and the
	// Send deadline it passes. Basis: daemon.DrainLineDeadline — the daemon's own one-line worst
	// case, the smallest exported daemon bound unambiguously larger than a local round trip. The
	// production defaults (5ms connect / 8ms ACK) are warm-daemon budgets; against a
	// race-instrumented in-process daemon on a loaded CI machine they would be an implicit latency
	// assertion, which §4's conventions forbid — this test asserts ACK counts, not speed.
	hookflowClientBudget = daemon.DrainLineDeadline

	// hookflowSettleBound waits, after Daemon.Drain has returned, for worker-pool dispatches that
	// were already in flight when Drain skipped their (seen) lines. Basis: one
	// daemon.DrainLineDeadline, the per-line ceiling the same dispatch runs under when drained.
	hookflowSettleBound = daemon.DrainLineDeadline
	hookflowSettleTick  = 10 * time.Millisecond
)

// The two config env-layer keys (QOMPACK_<SEC>__<KEY>__<SUB>, internal/config/load.go) test 1
// widens the hot-path deadlines through, and the value both are set to: the daemon's own exported
// per-line ceiling in milliseconds, derived rather than spelled as a bare number. The widened
// values flow through p.Cfg into the state.bin our in-process daemon writes, which is what the
// spawned real-binary hooks read their budgets from.
const (
	daemonConnectDeadlineEnv = "QOMPACK_RUNTIME__DAEMON__CONNECTDEADLINEMS"
	daemonAckDeadlineEnv     = "QOMPACK_RUNTIME__DAEMON__ACKDEADLINEMS"
)

// hookflowDeadlineMs renders hookflowClientBudget for the env layer.
func hookflowDeadlineMs() string {
	return strconv.FormatInt(hookflowClientBudget.Milliseconds(), 10)
}

// The fixed identities of this file's sessions and events.
const (
	hookflowSession = core.SessionID("sess-hookflow-daemon")
	spoolSession    = core.SessionID("sess-hookflow-spool")

	// hookflowTool is the tool name every event in this file carries. §4.2 and the §8.1 item-2
	// tombstone example both spell the file-read tool "FileRead", and the tombstone regexp below
	// pins it, so the events use that spelling.
	hookflowTool = "FileRead"

	// hookflowAuthPath is the path the §4.2 round-trip tests read: already in paths.Key form.
	hookflowAuthPath = "src/auth.ts"

	// hookflowEventTotal and hookflowBinaryEvents are §4.2's own numbers: 200 observe.tool
	// requests, at least 20 of them through the real binary.
	hookflowEventTotal   = 200
	hookflowBinaryEvents = 20

	// spoolEventTotal is §4.2's spool-phase event count.
	spoolEventTotal = 50

	// hookflowHLLTolerance is §4.2's "within 7% of the distinct path count".
	hookflowHLLTolerance = 0.07

	// hookflowKBFloor guards the tombstone's size unit: observer.Tombstone renders bytes below
	// 1024 as "…B", and the §8.1 item-2 regexp this file pins requires "KB", so the auth fixture
	// must canonicalize to at least this many bytes.
	hookflowKBFloor = 1 << 10
)

// hookflowAuthTS is the src/auth.ts content the round-trip tests ingest. It is authored here, not
// taken from testdata, because §4.2's retrieval assertions need properties no committed fixture
// has together: a TOP-LEVEL `export function refreshToken` (so the §5.22b extractor yields a
// symbol NAMED refreshToken, making store.Search's symbol span and symbols.Enclosing's minimal
// span the same span), nothing the redactor flags, nothing the canonicalizer strips, LF endings,
// and a canonical size past hookflowKBFloor.
const hookflowAuthTS = `// src/auth.ts - session token bookkeeping for the hookflow integration fixture.
// Deliberately boring TypeScript: no timestamps, no durations, no hex addresses,
// nothing the canonicalizer strips and nothing the redactor would flag.

const TOKEN_ROTATION_LIMIT = 6;
const SESSION_NAMESPACE = 'qompack-fixture';

export interface SessionRecord {
  id: string;
  ownerId: string;
  rotations: number;
  scopes: readonly string[];
}

export interface RotationOutcome {
  session: SessionRecord;
  minted: string;
  exhausted: boolean;
}

function checksum(input: string): number {
  let acc = 0;
  for (let i = 0; i < input.length; i++) {
    acc = (acc * 31 + input.charCodeAt(i)) % 997;
  }
  return acc;
}

function encodeToken(namespace: string, ordinal: number, digest: number): string {
  const head = namespace.split('').reverse().join('');
  return [head, String(ordinal), String(digest)].join('.');
}

export function issueSession(ownerId: string, scopes: readonly string[]): SessionRecord {
  return {
    id: encodeToken(SESSION_NAMESPACE, checksum(ownerId), scopes.length),
    ownerId,
    rotations: 0,
    scopes,
  };
}

export function refreshToken(session: SessionRecord, salt: string): RotationOutcome {
  const rotations = session.rotations + 1;
  const digest = checksum(session.id + ':' + salt);
  const minted = encodeToken(SESSION_NAMESPACE, rotations, digest);
  const exhausted = rotations >= TOKEN_ROTATION_LIMIT;
  const next: SessionRecord = {
    id: session.id,
    ownerId: session.ownerId,
    rotations,
    scopes: session.scopes,
  };
  return { session: next, minted, exhausted };
}

export function revokeSession(session: SessionRecord): SessionRecord {
  return {
    id: session.id,
    ownerId: session.ownerId,
    rotations: TOKEN_ROTATION_LIMIT,
    scopes: [],
  };
}
`

// hookflowTombstoneRE is §4.2's §8.1 item-2 form, with the short hash captured. The separator is
// U+00B7 MIDDLE DOT with a space on each side and the short form is exactly the 12 hex characters
// core.Hash.Short produces (observer.Tombstone's own contract).
var hookflowTombstoneRE = regexp.MustCompile(
	`^\[cleared: sha256:([0-9a-f]{12}) · [\d.]+KB · FileRead src/auth\.ts · re-expandable\]$`)

// buildQompackBinary compiles ./cmd/qompack into this test's own temp dir and returns the
// executable's path.
//
// This replicates the build half of test/e2e's real-binary harness (test/e2e/harness.go, Build):
// test/e2e is a composition root — importgraph enforces that nothing may import it — so §4.2's
// "use test/e2e's harness" is satisfied the sanctioned way, by replicating the minimal build step
// here rather than importing the package. The spawn half needs no replication at all:
// (*testutil.Project).RunHook already runs the real binary whenever QOMPACK_E2E_BINARY names one,
// and that env var plus this build is exactly how test/e2e itself drives it.
func buildQompackBinary(t *testing.T) string {
	t.Helper()

	root, err := hookflowModuleRoot()
	require.NoError(t, err)

	out := filepath.Join(t.TempDir(), "qompack")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}

	cmd := exec.CommandContext(context.Background(), "go", "build", "-o", out, "./cmd/qompack")
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Run(), "go build -o %s ./cmd/qompack (in %s):\n%s", out, root, stderr.String())
	return out
}

// hookflowModuleRoot returns the nearest ancestor of the working directory holding a go.mod — a
// test's working directory is its own package directory, and `go build ./cmd/qompack` is only
// meaningful from the module root.
func hookflowModuleRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for d := wd; ; {
		if fi, statErr := os.Stat(filepath.Join(d, "go.mod")); statErr == nil && fi.Mode().IsRegular() {
			return d, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", fmt.Errorf("no go.mod found in %s or any parent directory", wd)
		}
		d = parent
	}
}

// hookflowPipeline is §4.2's testObserveTool with its collaborators: the SP-08 stand-in that
// daemon.Options.Bind attaches as Services.ObserveTool. ObserveTool performs exactly the §8.1
// items 1, 4 and 5 that SP-08 will later own — store.PutBytes → store.RecordToolUse →
// store.AppendFileVersion → dag.BuildToolUse → cms.Add(pathKey,1) + hll.Add(pathKey) — plus, when
// the test has seeded a supersession for the incoming tool_use id, the §8.1 item-3 pair (passing
// Supersedes to dag.BuildToolUse and calling store.MarkSuperseded) that
// TestIntegration_SupersessionMarksEarlierReadThroughDAG exercises.
//
// It records every dag.ObservedTool it built (appended LAST, after every side effect, so a
// recorded observation proves that event is fully through the pipeline, sketches included) and
// every error it hit, so a test can assert "no event failed" instead of trusting the daemon's
// Warn log.
//
// The binding is REPLAY-TOLERANT, exactly as SP-08's real observer must be: Turn is memoized per
// tool_use id, so dispatching the same event twice rebuilds byte-identical records and the same
// idempotent §8.1 item-4 node/edge set (dag.BuildToolUse's own documented property). That
// tolerance is not hypothetical — this file's first run surfaced V2-VERIFY's live-vs-drain dedup
// defect (see the note in TestIntegration_HookEventThroughDaemonToStore), under which a
// turn-per-invocation binding would have forked the graph instead of surfacing the doubling
// cleanly.
type hookflowPipeline struct {
	store    store.Store
	graph    dag.Graph
	sketches *daemon.SketchSet
	clock    core.Clock

	mu         sync.Mutex
	turn       core.TurnIndex
	turnByID   map[core.ToolUseID]core.TurnIndex
	observed   []dag.ObservedTool
	errs       []error
	supersedes map[core.ToolUseID]core.ToolUseID
}

// newHookflowPipeline opens the §4.2 composition over p: the real store (every store.Deps member
// real — store.Open's defaults are the real chunker, canonicalizers, symbols, tokens and redactor;
// §4.1 pins that explicitly), the real dag.Graph, and a real daemon.SketchSet sized from p.Cfg.
func newHookflowPipeline(t *testing.T, p *testutil.Project) *hookflowPipeline {
	t.Helper()

	st := p.Store(t)
	g, err := dag.Open(p.Root, p.Cfg, p.Log)
	require.NoError(t, err)
	if m, ok := g.(dag.Maintainer); ok {
		m.SetClock(p.Clock)
	}

	return &hookflowPipeline{
		store:      st,
		graph:      g,
		sketches:   daemon.NewSketchSet(p.Cfg),
		clock:      p.Clock,
		turnByID:   map[core.ToolUseID]core.TurnIndex{},
		supersedes: map[core.ToolUseID]core.ToolUseID{},
	}
}

// ObserveTool is the bound Services.ObserveTool. See hookflowPipeline's doc comment for the exact
// §8.1 chain; the daemon's route already ACKed (the event is WAL-durable) before this runs, so an
// error here costs freshness, never data — and the pipeline records it so the test still sees it.
func (hp *hookflowPipeline) ObserveTool(ctx context.Context, e hookio.Event) error {
	if err := hp.observeTool(ctx, e); err != nil {
		hp.mu.Lock()
		hp.errs = append(hp.errs, err)
		hp.mu.Unlock()
		return err
	}
	return nil
}

func (hp *hookflowPipeline) observeTool(ctx context.Context, e hookio.Event) error {
	content := hookflowContent(e.ToolResponse)
	pathKey := paths.Key(hookflowFilePath(e.ToolInput))

	// §8.1 item 1: redact → canonicalize → chunk → store, all inside PutBytes.
	res, err := hp.store.PutBytes(ctx, content, store.PutOptions{Tool: e.ToolName, Path: pathKey})
	if err != nil {
		return fmt.Errorf("hookflow: PutBytes: %w", err)
	}

	hp.mu.Lock()
	turn, seen := hp.turnByID[e.ToolUseID]
	if !seen {
		hp.turn++
		turn = hp.turn
		hp.turnByID[e.ToolUseID] = turn
	}
	supersedes := hp.supersedes[e.ToolUseID]
	hp.mu.Unlock()

	now := core.NowMilli(hp.clock)
	rec := store.ToolUseRecord{
		ID:          e.ToolUseID,
		Session:     e.SessionID,
		Turn:        turn,
		TS:          now,
		Tool:        e.ToolName,
		ArgsDigest:  core.HashBytes(core.DomainArgs, e.ToolInput),
		ArgsPreview: hookflowPreview(e.ToolInput),
		Root:        res.Root.Hash,
		Path:        pathKey,
		Bytes:       res.Root.CanonBytes,
		Tokens:      res.Root.Tokens,
		Signature:   res.Signature,
	}
	if err := hp.store.RecordToolUse(ctx, rec); err != nil {
		return fmt.Errorf("hookflow: RecordToolUse: %w", err)
	}
	if err := hp.store.AppendFileVersion(ctx, pathKey, store.FileVersion{
		TS: now, Root: res.Root.Hash, Turn: turn, Bytes: res.Root.CanonBytes,
	}); err != nil {
		return fmt.Errorf("hookflow: AppendFileVersion: %w", err)
	}

	// §8.1 item 4 (and, when seeded, item 3's Supersedes edge). PrevToolUseID stays empty on
	// purpose: the assistant-chain consumes edge is SP-08's own transcript-position work, and
	// leaving it out keeps every §4.2 score assertion a single-path product.
	obs := dag.ObservedTool{
		ToolUseID:  e.ToolUseID,
		Supersedes: supersedes,
		Turn:       turn,
		TS:         now,
		Pos:        int(turn),
		Tool:       e.ToolName,
		PathKey:    pathKey,
		Root:       res.Root.Hash,
		Tokens:     res.Root.Tokens,
	}
	if err := dag.BuildToolUse(hp.graph, obs); err != nil {
		return fmt.Errorf("hookflow: BuildToolUse: %w", err)
	}

	// §8.1 item 3's store half.
	if supersedes != "" {
		if err := hp.store.MarkSuperseded(ctx, supersedes, e.ToolUseID); err != nil {
			return fmt.Errorf("hookflow: MarkSuperseded: %w", err)
		}
	}

	// §8.1 item 5: the resident sketches.
	key := []byte(pathKey)
	hp.sketches.Write(func(s *daemon.SketchSet) {
		s.Touch.Add(key, 1)
		s.Explore.Add(key)
	})

	// Recorded last — see the type's doc comment for why the append is the completion marker.
	hp.mu.Lock()
	hp.observed = append(hp.observed, obs)
	hp.mu.Unlock()
	return nil
}

// superseding seeds §8.1 item 3: the NEXT observation of newer will pass Supersedes: older to
// dag.BuildToolUse and call store.MarkSuperseded(older, newer).
func (hp *hookflowPipeline) superseding(newer, older core.ToolUseID) {
	hp.mu.Lock()
	defer hp.mu.Unlock()
	hp.supersedes[newer] = older
}

// observedCount reports how many raw pipeline completions were recorded, duplicate dispatches of
// one event included.
func (hp *hookflowPipeline) observedCount() int {
	hp.mu.Lock()
	defer hp.mu.Unlock()
	return len(hp.observed)
}

// uniqueObservedCount reports how many DISTINCT events are fully through the pipeline — the
// completion measure the tests settle on, immune to a duplicate dispatch of an already-processed
// line.
func (hp *hookflowPipeline) uniqueObservedCount() int {
	return len(hp.snapshotObserved())
}

// snapshotObserved copies the recorded observations in pipeline-completion order, first
// occurrence per tool_use id (later dispatches of the same event are byte-identical replays —
// see the type's doc comment).
func (hp *hookflowPipeline) snapshotObserved() []dag.ObservedTool {
	hp.mu.Lock()
	defer hp.mu.Unlock()
	seen := make(map[core.ToolUseID]bool, len(hp.observed))
	out := make([]dag.ObservedTool, 0, len(hp.observed))
	for _, o := range hp.observed {
		if seen[o.ToolUseID] {
			continue
		}
		seen[o.ToolUseID] = true
		out = append(out, o)
	}
	return out
}

// takeErrs returns every error the pipeline recorded.
func (hp *hookflowPipeline) takeErrs() []error {
	hp.mu.Lock()
	defer hp.mu.Unlock()
	out := make([]error, len(hp.errs))
	copy(out, hp.errs)
	return out
}

// startHookflowDaemon is §4.2's setup steps 2–3: opts := daemon.NewOptions(p.Root, p.Cfg) with
// Store, Graph and Sketches set to the pipeline's real instances and the binding attached through
// opts.Bind — the extension seam's first real exercise. It runs the daemon on its own goroutine,
// waits until the transport is provably reachable, and registers an orderly stop.
func startHookflowDaemon(t *testing.T, p *testutil.Project, hp *hookflowPipeline) daemon.Daemon {
	t.Helper()

	opts := daemon.NewOptions(p.Root, p.Cfg)
	opts.Log = p.Log
	opts.Store = hp.store
	opts.Graph = hp.graph
	opts.Sketches = hp.sketches
	opts.Bind(func(s *daemon.Services) { s.ObserveTool = hp.ObserveTool })

	d, err := daemon.New(opts)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if runErr := <-done; runErr != nil {
			t.Errorf("daemon.Run returned %v", runErr)
		}
	})

	addr, err := ipc.Resolve(p.Root)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return ipc.Probe(addr, hookflowProbeDial) },
		hookflowDaemonUpBound, hookflowDaemonUpTick,
		"in-process daemon never became reachable at %s within %s (8×daemon.SpawnPollBound)",
		addr.Path, hookflowDaemonUpBound)
	return d
}

// hookflowEvent builds one PostToolUse payload: a FileRead of path whose tool_response carries
// content as a JSON string, the shape the binding ingests.
func hookflowEvent(t *testing.T, sess core.SessionID, id core.ToolUseID, path, content string) hookio.Event {
	t.Helper()
	resp, err := json.Marshal(content)
	require.NoError(t, err)
	return hookio.Event{
		HookEventName: "PostToolUse",
		SessionID:     sess,
		ToolName:      hookflowTool,
		ToolUseID:     id,
		ToolInput:     json.RawMessage(fmt.Sprintf(`{"file_path":%q}`, path)),
		ToolResponse:  resp,
	}
}

// hookflowContent extracts the ingestable bytes from a tool_response: a JSON string unquotes,
// anything else ingests verbatim.
func hookflowContent(raw json.RawMessage) []byte {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []byte(s)
	}
	return []byte(raw)
}

// hookflowFilePath reads tool_input's file_path.
func hookflowFilePath(raw json.RawMessage) string {
	var in struct {
		FilePath string `json:"file_path"`
	}
	_ = json.Unmarshal(raw, &in) // a payload without file_path simply yields ""
	return in.FilePath
}

// argsPreviewMaxBytes mirrors store.ToolUseRecord.ArgsPreview's documented cap ("truncated to at
// most 120 characters").
const argsPreviewMaxBytes = 120

// hookflowPreview renders tool_input as an ArgsPreview.
func hookflowPreview(raw json.RawMessage) string {
	s := string(raw)
	if len(s) > argsPreviewMaxBytes {
		s = s[:argsPreviewMaxBytes]
	}
	return s
}

// hookflowExpectedCanonical reproduces, independently of the store instance under test, the exact
// §8.1 item-1 pipeline PutBytes runs — REDACT then canonicalize, with the same tool, path and
// configured strip set — so the "full re-expansion equals the canonicalized ingested bytes"
// assertion compares against a value the store did not produce.
func hookflowExpectedCanonical(t *testing.T, p *testutil.Project, raw []byte) []byte {
	t.Helper()
	red, _ := redact.New(p.Cfg).Redact(raw)
	res, err := canon.Default(p.Cfg.Store.Canonicalize).Run(
		hookflowTool, hookflowAuthPath, red, canon.OptionsFrom(p.Cfg.Store.Canonicalize, false))
	require.NoError(t, err)
	return res.Canonical
}

// countFileLines counts non-empty lines in the file at path, 0 for a file that does not exist.
func countFileLines(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		require.NoError(t, err)
	}
	n := 0
	for _, line := range bytes.Split(b, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) > 0 {
			n++
		}
	}
	return n
}

// clientSpoolLines counts the non-empty lines across every client-side spool file under root's
// spool directory — everything ipc.SpoolFiles reports that is not a wal-* segment.
func clientSpoolLines(t *testing.T, root string) int {
	t.Helper()
	files, err := ipc.SpoolFiles(paths.Of(root).Spool)
	require.NoError(t, err)
	total := 0
	for _, f := range files {
		if strings.HasPrefix(filepath.Base(f), "wal-") {
			continue
		}
		total += countFileLines(t, f)
	}
	return total
}

// walSegmentCount counts wal-* segments under root's spool directory.
func walSegmentCount(t *testing.T, root string) int {
	t.Helper()
	files, err := ipc.SpoolFiles(paths.Of(root).Spool)
	require.NoError(t, err)
	n := 0
	for _, f := range files {
		if strings.HasPrefix(filepath.Base(f), "wal-") {
			n++
		}
	}
	return n
}

// The three-colour marks of hookflowFindCycle's iterative depth-first search.
const (
	hookflowDFSWhite uint8 = iota // not yet visited
	hookflowDFSGrey               // on the current DFS stack
	hookflowDFSBlack              // fully explored
)

// hookflowFindCycle runs an iterative depth-first search over g.Out from ids and returns the
// first directed cycle it finds, or nil.
//
// This replicates the DFS TestBuilderOutputIsAcyclic uses (internal/dag/builders_test.go,
// builderFindCycle): that helper is unexported inside another package's test binary, so a
// composition-root test replicates the walk rather than exporting it. Iterative, not recursive,
// for the same reason stated there: a graph that has accidentally become cyclic must name the
// cycle, not overflow the goroutine stack. A grey successor is a back edge; the stack from that
// node's depth to the top IS the cycle.
func hookflowFindCycle(g dag.Graph, ids []dag.NodeID) []dag.NodeID {
	type frame struct {
		id    dag.NodeID
		edges []dag.Edge
		next  int
	}
	colour := make(map[dag.NodeID]uint8, len(ids))
	depth := make(map[dag.NodeID]int, len(ids))

	for _, start := range ids {
		if colour[start] != hookflowDFSWhite {
			continue
		}
		colour[start] = hookflowDFSGrey
		depth[start] = 0
		stack := []frame{{id: start, edges: g.Out(start)}}
		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			if top.next >= len(top.edges) {
				colour[top.id] = hookflowDFSBlack
				stack = stack[:len(stack)-1]
				continue
			}
			next := top.edges[top.next].To
			top.next++
			switch colour[next] {
			case hookflowDFSGrey:
				cycle := make([]dag.NodeID, 0, len(stack)-depth[next]+1)
				for _, f := range stack[depth[next]:] {
					cycle = append(cycle, f.id)
				}
				return append(cycle, next)
			case hookflowDFSWhite:
				colour[next] = hookflowDFSGrey
				depth[next] = len(stack)
				stack = append(stack, frame{id: next, edges: g.Out(next)})
			}
		}
	}
	return nil
}

// hookflowNodeIDs derives the DFS start set from the pipeline's recorded observations: exactly
// the node ids the builders emitted for them.
func hookflowNodeIDs(observed []dag.ObservedTool) []dag.NodeID {
	seen := make(map[dag.NodeID]bool, len(observed)*4)
	ids := make([]dag.NodeID, 0, len(observed)*4)
	add := func(id dag.NodeID) {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, o := range observed {
		add(dag.ToolUseNode(o.ToolUseID))
		add(dag.ToolResultNode(o.ToolUseID))
		add(dag.AssistantNode(o.Turn))
		if o.PathKey != "" {
			add(dag.FileNode(o.PathKey))
		}
	}
	return ids
}

// replayShadowGraph replays the recorded observations into a second, fresh dag.Graph rooted in a
// scratch project layout: the builder's own expected node and edge counts, derived by running the
// builder rather than by re-implementing its arithmetic.
func replayShadowGraph(t *testing.T, p *testutil.Project, observed []dag.ObservedTool) dag.Graph {
	t.Helper()
	root := filepath.Join(t.TempDir(), "shadow")
	require.NoError(t, os.MkdirAll(paths.Long(root), 0o700))
	require.NoError(t, paths.EnsureLayout(paths.Of(root)))
	g, err := dag.Open(root, p.Cfg, logging.Nop())
	require.NoError(t, err)
	for _, o := range observed {
		require.NoError(t, dag.BuildToolUse(g, o))
	}
	return g
}

// TestIntegration_HookEventThroughDaemonToStore is §4.2's transport round trip: 200 observe.tool
// requests — 180 through the real ipc.Client, the final 20 through the real spawned binary — into
// a real daemon whose bound ObserveTool lands every event in the real store, the real DAG and the
// real sketch set.
func TestIntegration_HookEventThroughDaemonToStore(t *testing.T) {
	bin := buildQompackBinary(t)
	p := testutil.NewProject(t,
		testutil.WithEnv(testutil.E2EBinaryEnv, bin),
		testutil.WithEnv(daemonConnectDeadlineEnv, hookflowDeadlineMs()),
		testutil.WithEnv(daemonAckDeadlineEnv, hookflowDeadlineMs()),
	)
	hp := newHookflowPipeline(t, p)
	d := startHookflowDaemon(t, p, hp)
	ctx := context.Background()

	// A real host fires SessionStart before any PostToolUse, and the drainer only offset-marks —
	// never deletes — a LIVE session's WAL, which is what keeps the wal line count below stable
	// across the daemon's own eager re-drain (redrainOnceServing). Run it through the real binary
	// too: one more full wire exercise.
	p.RunHook(t, "SessionStart", hookio.Event{
		HookEventName: "SessionStart", SessionID: hookflowSession, Source: "startup",
	})

	// The event plan: one hot path plus a spread of others; ground truth is derived from the plan
	// itself so the sketch assertions compare against exact counts, not modular arithmetic.
	events := make([]hookio.Event, 0, hookflowEventTotal)
	pathCount := make(map[string]int)
	for i := 0; i < hookflowEventTotal; i++ {
		path := hookflowAuthPath
		if i%5 != 0 {
			path = fmt.Sprintf("src/pkg/file%02d.ts", i%39)
		}
		pathCount[paths.Key(path)]++
		id := core.ToolUseID(fmt.Sprintf("toolu_hookflow_%03d", i))
		events = append(events, hookflowEvent(t, hookflowSession, id, path, hookflowAuthTS))
	}
	distinctPaths := len(pathCount)

	addr, err := ipc.Resolve(p.Root)
	require.NoError(t, err)
	sp, err := ipc.NewSpool(paths.Of(p.Root).Spool)
	require.NoError(t, err)
	client := ipc.NewClientWithOptions(addr, sp, p.Log, nil, ipc.ClientOptions{
		ProjectRoot:     p.Root,
		State:           ipc.ReadState(p.Root, p.Cfg),
		ConnectDeadline: hookflowClientBudget,
		AckDeadline:     hookflowClientBudget,
	})
	defer func() { require.NoError(t, client.Close()) }()

	acks := 0
	for i := range events {
		if i >= hookflowEventTotal-hookflowBinaryEvents {
			// The real-binary tranche: a spawned `qompack observe tool` process, stdin payload,
			// exit code and stdout JSON asserted by RunHook — the hook-level ACK. That it really
			// REACHED the daemon (rather than spooling) is what the zero-spool-lines and
			// wal-line-count assertions below prove, the same way test/e2e's round trip does.
			p.RunHook(t, "PostToolUse", events[i])
			acks++
			continue
		}
		resp, sendErr := client.Send(ctx, ipc.Request{
			Op:      ipc.OpObserveTool,
			Session: hookflowSession,
			TS:      core.NowMilli(p.Clock),
			Event:   &events[i],
		}, hookflowClientBudget)
		require.NoError(t, sendErr)
		require.True(t, resp.OK, "observe.tool #%d must be ACKed on the wire, not spooled", i)
		acks++
	}
	require.Equal(t, hookflowEventTotal, acks, "200 ACKs")

	// Zero spool lines: no request fell back to the client-side durability tier.
	require.Zero(t, clientSpoolLines(t, p.Root),
		"zero spool lines: every request must have been ACKed by the live daemon")

	// The WAL holds exactly one line per accepted event: ingest.Accept appends before the ACK
	// (§2.4's durability boundary), and the daemon is in-process, so by the time the last ACK was
	// read every append has happened — no wait is needed or tolerated.
	walPath := filepath.Join(paths.Of(p.Root).Spool, "wal-"+string(hookflowSession)+".ndjson")
	require.Equal(t, hookflowEventTotal, countFileLines(t, walPath),
		"wal-%s.ndjson must hold exactly one line per accepted observe.tool", hookflowSession)

	// After Drain, every WAL line has either been dispatched synchronously by Drain itself or was
	// already claimed (seen) by a worker-pool dispatch; the only outstanding work is a dispatch in
	// flight at that instant, bounded by the same per-line ceiling a drained line runs under.
	//
	// The settle condition counts DISTINCT completed events, deliberately. This file's first run
	// surfaced a real daemon defect (V2-VERIFY, fixed on verify/v2 by 871f574): ipc.EncodeRequest
	// returns its line '\n'-terminated (frame.go: json.Encoder.Encode appends it — the
	// client-side spool.Append even documents that no caller may add a second newline), but
	// acceptHotPathEvent handed that terminated line to ingest.Accept, which appended ANOTHER
	// '\n' to the WAL and keyed the seen-set over the terminated bytes — while drain.go hashes
	// the newline-STRIPPED bytes it reads back. Live-vs-drain dedup therefore never matched, and
	// every Drain re-dispatched every already-processed WAL line (this run observed exactly
	// 400/200 completions). Wave 1 could not see it: with no ObserveTool bound, the doubling only
	// double-counted the unhandled-op counter. Exactly-once dispatch is pinned by the fix's own
	// tests in internal/daemon/ingest_test.go; HERE the distinct count plus the replay-tolerant
	// binding keep every §4.2 assertion exact on both sides of that fix, and the store/dag/sketch
	// assertions below stay the end-to-end truth either way.
	_, err = d.Drain(ctx)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return hp.uniqueObservedCount() == hookflowEventTotal },
		hookflowSettleBound, hookflowSettleTick,
		"after Drain, %d/%d distinct events are through the binding — an in-flight worker dispatch "+
			"is bounded by daemon.DrainLineDeadline, so exceeding it means events were lost, not "+
			"delayed (pipeline errors: %v)",
		hp.uniqueObservedCount(), hookflowEventTotal, hp.takeErrs())
	require.Empty(t, hp.takeErrs(), "no event may fail inside the binding")

	stats, err := hp.store.Stats(ctx)
	require.NoError(t, err)
	require.Equal(t, hookflowEventTotal, stats.ToolUses, "store.Stats().ToolUses == 200 after Drain")

	// dag.Stats().Nodes equals the builder's expected count — derived two independent ways: by
	// arithmetic over the §8.1 item-4 emission (tooluse + toolresult + one assistant per unique
	// turn per event, one file node per distinct path, no symbols observed), and by replaying the
	// identical observations through the builder into a fresh shadow graph.
	observed := hp.snapshotObserved()
	require.Len(t, observed, hookflowEventTotal, "one distinct observation per event")
	gs := hp.graph.Stats()
	require.Equal(t, 3*hookflowEventTotal+distinctPaths, gs.Nodes,
		"nodes: 2 per event + 1 assistant per (unique) turn + %d file nodes", distinctPaths)
	shadow := replayShadowGraph(t, p, observed)
	ss := shadow.Stats()
	require.Equal(t, ss.Nodes, gs.Nodes, "live graph and builder replay disagree on node count")
	require.Equal(t, ss.Edges, gs.Edges, "live graph and builder replay disagree on edge count")

	// The sketches: hll within 7% of the distinct path count; cms never under the true hot count.
	var card uint64
	var hotEstimate uint32
	hp.sketches.Read(func(s *daemon.SketchSet) {
		card = s.Explore.Cardinality()
		hotEstimate = s.Touch.Estimate([]byte(paths.Key(hookflowAuthPath)))
	})
	require.InDelta(t, float64(distinctPaths), float64(card), hookflowHLLTolerance*float64(distinctPaths),
		"hll.Cardinality() within 7%% of %d distinct paths", distinctPaths)
	hotCount := pathCount[paths.Key(hookflowAuthPath)]
	require.GreaterOrEqual(t, int(hotEstimate), hotCount,
		"cms.Estimate(hot path) may overestimate but never undercount (true count %d)", hotCount)

	// The graph stays acyclic — the same DFS TestBuilderOutputIsAcyclic runs (D-7).
	cycle := hookflowFindCycle(hp.graph, hookflowNodeIDs(observed))
	require.Nil(t, cycle, "the observed graph must be acyclic (D-7); cycle: %v", cycle)
}

// TestIntegration_TombstoneRoundTripThroughStore is §4.2's retrieval round trip: the §8.1 item-2
// tombstone of a recorded FileRead is addressable, not decorative — its short hash re-expands
// through the tool_use index to the full canonicalized bytes (G3.2), the §8.7 minimal span
// resolves through SP-04's extractor and SP-06's span reader to the same boundaries, and symbol
// search returns that exact span.
func TestIntegration_TombstoneRoundTripThroughStore(t *testing.T) {
	p := testutil.NewProject(t)
	hp := newHookflowPipeline(t, p)
	ctx := context.Background()

	const authID = core.ToolUseID("toolu_hookflow_auth")
	require.NoError(t, hp.ObserveTool(ctx, hookflowEvent(t, hookflowSession, authID, hookflowAuthPath, hookflowAuthTS)))

	rec, err := hp.store.ToolUse(ctx, authID)
	require.NoError(t, err)
	require.Equal(t, hookflowTool, rec.Tool)
	require.Equal(t, hookflowAuthPath, rec.Path)
	require.GreaterOrEqual(t, rec.Bytes, int64(hookflowKBFloor),
		"the fixture must canonicalize to at least 1KB or the tombstone's KB unit cannot appear")

	// 1. The marker matches the §8.1 item-2 form, with the real short hash.
	marker := observer.Tombstone(rec)
	m := hookflowTombstoneRE.FindStringSubmatch(marker)
	require.NotNil(t, m, "tombstone %q must match the §8.1 item-2 form", marker)
	shortHash := m[1]

	// 2. Re-expand the short form through the tool_use index and parse the full hash back with
	// core.ParseHash: the marker's 12 hex chars are exactly rec.Root.Short().
	rec2, err := hp.store.ToolUse(ctx, rec.ID)
	require.NoError(t, err)
	parsed, err := core.ParseHash(rec2.Root.String())
	require.NoError(t, err)
	require.Equal(t, rec.Root, parsed)
	require.Equal(t, shortHash, rec.Root.Short(),
		"the tombstone's short hash must be the tool use's own root address")

	// 3. Full re-expansion: store.Open returns the canonicalized bytes originally ingested,
	// compared against an independent run of the same redact→canonicalize pipeline.
	expected := hookflowExpectedCanonical(t, p, []byte(hookflowAuthTS))
	rc, err := hp.store.Open(ctx, rec.Root)
	require.NoError(t, err)
	full, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	require.Equal(t, expected, full, "G3.2: the tombstone must re-expand to the ingested bytes")

	// 4. Minimal span (§8.7): the smallest enclosing symbol of an offset inside refreshToken's
	// declaration is the refreshToken function itself, and OpenSpan returns exactly that slice.
	nameOff := bytes.Index(full, []byte("function refreshToken"))
	require.GreaterOrEqual(t, nameOff, 0, "the canonical content must still declare refreshToken")
	sym, ok := symbols.New().Enclosing(hookflowAuthPath, full, nameOff+len("function "))
	require.True(t, ok, "symbols.Enclosing must resolve an enclosing symbol")
	require.Equal(t, "refreshToken", sym.Name)
	src, err := hp.store.OpenSpan(ctx, rec.Root, int64(sym.Offset), int64(sym.Len))
	require.NoError(t, err)
	spanBytes, err := io.ReadAll(src)
	require.NoError(t, err)
	require.NoError(t, src.Close())
	require.Equal(t, full[sym.Offset:sym.Offset+sym.Len], spanBytes,
		"SP-04's extractor and SP-06's span reader must resolve the same boundaries")

	// 5. Symbol search returns a Hit carrying that exact span and root.
	hits, err := hp.store.Search(ctx, store.Query{Symbol: "refreshToken", K: 5})
	require.NoError(t, err)
	require.NotEmpty(t, hits, "recall by symbol must find the stored read")
	require.Equal(t, rec.Root, hits[0].Root)
	require.Equal(t, [2]int64{int64(sym.Offset), int64(sym.Offset + sym.Len)}, hits[0].Span,
		"the hit's span must be the symbol's own extent — §8.7's minimal span")
}

// TestIntegration_SupersessionMarksEarlierReadThroughDAG is §4.2's §8.1 item 3 across store and
// dag — which neither package can assert alone (store must not import dag; dag imports neither):
// a second read of the same path supersedes the first in the store's index AND as an
// EdgeSupersedes hop the backward slice scores at exactly Decay × Multiplier = 0.85 × 0.30.
func TestIntegration_SupersessionMarksEarlierReadThroughDAG(t *testing.T) {
	p := testutil.NewProject(t)
	hp := newHookflowPipeline(t, p)
	ctx := context.Background()

	// The committed SP-06 fixtures §4.2 names: two versions of one file on one path
	// (their .meta.json sidecars both record path "src/auth.ts").
	v1, err := os.ReadFile(filepath.Join("..", "..", "testdata", "corpora", "toolout", "sp06", "fileread-auth-v1.txt"))
	require.NoError(t, err)
	v2, err := os.ReadFile(filepath.Join("..", "..", "testdata", "corpora", "toolout", "sp06", "fileread-auth-v2.txt"))
	require.NoError(t, err)

	const (
		olderID = core.ToolUseID("toolu_hookflow_sp06_v1")
		newerID = core.ToolUseID("toolu_hookflow_sp06_v2")
	)

	require.NoError(t, hp.ObserveTool(ctx, hookflowEvent(t, hookflowSession, olderID, hookflowAuthPath, string(v1))))
	hp.superseding(newerID, olderID)
	require.NoError(t, hp.ObserveTool(ctx, hookflowEvent(t, hookflowSession, newerID, hookflowAuthPath, string(v2))))
	require.Empty(t, hp.takeErrs())

	// The store half: the older record is superseded, pointing at the newer; the newer stands.
	older, err := hp.store.ToolUse(ctx, olderID)
	require.NoError(t, err)
	require.Equal(t, store.StatusSuperseded, older.Status)
	require.Equal(t, newerID, older.SupersededBy)
	newer, err := hp.store.ToolUse(ctx, newerID)
	require.NoError(t, err)
	require.Equal(t, store.StatusOK, newer.Status)
	require.NotEqual(t, older.Root, newer.Root, "the two versions must be distinct stored contents")

	// The dag half, direction included: D-1 has no exception, so EdgeSupersedes runs
	// superseded → superseding.
	var supersedeEdges []dag.Edge
	for _, e := range hp.graph.Out(dag.ToolUseNode(olderID)) {
		if e.Kind == dag.EdgeSupersedes {
			supersedeEdges = append(supersedeEdges, e)
		}
	}
	require.Len(t, supersedeEdges, 1, "exactly one supersedes edge must leave the older tool use")
	require.Equal(t, dag.ToolUseNode(newerID), supersedeEdges[0].To)

	// The slice half. Deadline 0 means no wall-clock bound (dag's own documented deterministic
	// mode), so the score is a pure product: criterion 1.0 × Decay × EdgeSupersedes.Multiplier()
	// × weight 1 — the older read is reachable but scores low enough that a slice can never
	// resurrect §8.1 item 3's first eviction candidate.
	sl, err := hp.graph.BackwardSlice([]dag.NodeID{dag.ToolUseNode(newerID)}, dag.SliceOptions{
		Thin:     true,
		MaxNodes: dag.DefaultMaxNodes,
		Decay:    dag.DefaultDecay,
	})
	require.NoError(t, err)
	require.False(t, sl.Truncated)
	require.Equal(t, float32(1), sl.Scores[dag.ToolUseNode(newerID)], "a criterion scores 1.0")
	got, ok := sl.Scores[dag.ToolUseNode(olderID)]
	require.True(t, ok, "the superseded read must be reachable from the superseding one")
	require.Equal(t, dag.DefaultDecay*dag.EdgeSupersedes.Multiplier(), got,
		"one supersedes hop: Decay × Multiplier exactly")
	require.InDelta(t, 0.255, float64(got), 1e-6, "0.85 × 0.30 = 0.255")
}

// TestIntegration_SpooledEventsSurviveToTheStore is §4.2's D4 degradation path with a real L1
// behind it: 50 events spooled by real binary hooks in forced spool submode — zero connects, no
// daemon ever launched — then drained by a freshly started daemon into the real store, exactly
// once each. The spool loses freshness, never data.
func TestIntegration_SpooledEventsSurviveToTheStore(t *testing.T) {
	bin := buildQompackBinary(t)
	p := testutil.NewProject(t, testutil.WithEnv(testutil.E2EBinaryEnv, bin))
	ctx := context.Background()

	// Force state.bin to hot=1 (spool submode) before any daemon exists. ipc.Client's Send checks
	// the hot submode (step 3) before it ever dials or lazy-spawns (step 5), so every hook below
	// must append to its own spool and exit 0 without a daemon in the world.
	st := ipc.StateFromConfig(p.Cfg)
	st.Hot = ipc.HotSpool
	require.NoError(t, ipc.WriteState(p.Root, st))

	for i := 0; i < spoolEventTotal; i++ {
		id := core.ToolUseID(fmt.Sprintf("toolu_hookflow_spool_%02d", i))
		p.RunHook(t, "PostToolUse", hookflowEvent(t, spoolSession, id, hookflowAuthPath, hookflowAuthTS))
	}

	// Zero connects during the spool phase. There is no daemon whose accept counter could move,
	// and the three artifacts a connect (or a spawn) would necessarily leave are all absent:
	// a WAL segment (ingest.Accept appends before any ACK — §2.4's durability boundary),
	// run/daemon.lock (a launched daemon acquires it before serving), and a rewritten state.bin
	// (a serving daemon stamps its own pid and hot submode).
	require.Zero(t, walSegmentCount(t, p.Root),
		"a spool-submode client must never have reached a daemon's ingest")
	require.NoFileExists(t, filepath.Join(paths.Of(p.Root).Run, "daemon.lock"),
		"HotSpool must short-circuit before the connect-failure path that lazy-spawns a daemon")
	spoolPhase := ipc.ReadState(p.Root, p.Cfg)
	require.Equal(t, ipc.HotSpool, spoolPhase.Hot, "no daemon may have rewritten state.bin")
	require.Zero(t, spoolPhase.DaemonPID, "no daemon may have stamped its pid into state.bin")

	// 50 spool lines: one per event, across the per-pid client-*.ndjson files.
	require.Equal(t, spoolEventTotal, clientSpoolLines(t, p.Root),
		"every event must be durably spooled exactly once")

	// Flip back to sync and start the daemon over the real store/DAG/sketches.
	st.Hot = ipc.HotSync
	require.NoError(t, ipc.WriteState(p.Root, st))
	hp := newHookflowPipeline(t, p)
	d := startHookflowDaemon(t, p, hp)

	// Drain. Daemon.Drain serializes behind Run's own startup drain on the drainer's mutex and
	// dispatches every remaining line synchronously through the binding, so when this call
	// returns, all 50 events are fully through the pipeline — a deterministic fact, not a wait.
	// (The spec's "let one idle tick drain" names the fallback cadence; this calls the very
	// function that tick runs, without spending 30 wall-clock seconds to reach it.)
	_, err := d.Drain(ctx)
	require.NoError(t, err)
	require.Empty(t, hp.takeErrs(), "no drained event may fail inside the binding")
	require.Equal(t, spoolEventTotal, hp.observedCount())

	stats, err := hp.store.Stats(ctx)
	require.NoError(t, err)
	require.Equal(t, spoolEventTotal, stats.ToolUses,
		"D4: the spool path loses freshness, never data — all 50 events must reach the store")

	// No duplicates: a second Drain replays nothing (the drain state and the shared seen-set are
	// TestNAKDuplicateIsDedupedOnDrain's dedup rule, here held against a real store).
	n2, err := d.Drain(ctx)
	require.NoError(t, err)
	require.Zero(t, n2, "a second drain must find nothing left to dispatch")
	stats2, err := hp.store.Stats(ctx)
	require.NoError(t, err)
	require.Equal(t, spoolEventTotal, stats2.ToolUses, "no event may be applied twice")
}
