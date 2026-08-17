// V1 cross-component integration tests, guards half (plans/V1-VERIFY-foundation-and-contracts.md §4).
//
// The e2e half of §4 lives in test/e2e/v1_integration_test.go and drives the real binary through
// the host's own wire format. This half asserts the invariants that are only observable from a
// composition root that may import the whole tree at once: the append-only guard against a
// hook-created layout (IT-3), the write set (IT-4), the stub graph against its ownership table
// (IT-6), the contract monitor's transitions (IT-7), the budget table's config-drivenness (IT-8),
// the frozen fixture corpus (IT-9), and the toolchain gates that protect every later wave (IT-10).
package guards

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/cli"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/pins"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/stretchr/testify/require"
)

// v1Session is IT-1's eight-call lifecycle, expressed as hookio.Events for in-process dispatch.
//
// RunHook accepts a host event name, so "SubagentStop" is spelled here even though internal/cli
// routes it onto the same `observe stop` subcommand as "Stop".
func v1Session(root string) []struct {
	name string
	ev   hookio.Event
} {
	base := func(name string) hookio.Event {
		return hookio.Event{
			HookEventName:  name,
			SessionID:      core.SessionID("sess_v1_integration"),
			TranscriptPath: filepath.Join(root, "transcript.jsonl"),
			CWD:            root,
		}
	}
	sessionStart := base("SessionStart")
	sessionStart.Source = "startup"

	prompt := base("UserPromptSubmit")
	prompt.Prompt = "why does the refresh endpoint reject a valid token twice?"

	read := base("PostToolUse")
	read.ToolName = "FileRead"
	read.ToolUseID = core.ToolUseID("toolu_01A2B3C4D5E6F7G8H9J0K1L2")
	read.ToolInput = json.RawMessage(`{"file_path":"src/auth.ts"}`)
	read.ToolResponse = json.RawMessage(`{"type":"text","file":{"numLines":84}}`)

	bash := base("PostToolUse")
	bash.ToolName = "Bash"
	bash.ToolUseID = core.ToolUseID("toolu_09Z8Y7X6W5V4U3T2S1R0Q9P8")
	bash.ToolInput = json.RawMessage(`{"command":"go test ./internal/auth/..."}`)
	bash.ToolResponse = json.RawMessage(`{"stdout":"ok\n"}`)

	stop := base("Stop")
	stop.StopHookActive = true

	preCompact := base("PreCompact")
	preCompact.Trigger = "auto"

	subagent := base("SubagentStop")
	subagent.StopHookActive = true

	return []struct {
		name string
		ev   hookio.Event
	}{
		{"SessionStart", sessionStart},
		{"UserPromptSubmit", prompt},
		{"PostToolUse", read},
		{"PostToolUse", bash},
		{"Stop", stop},
		{"PreCompact", preCompact},
		{"SessionEnd", base("SessionEnd")},
		{"SubagentStop", subagent},
	}
}

// TestV1_AppendOnlyInvariantSurvivesRealHookRun is §4 IT-3.
//
// Crosses cli's hooks -> paths' layout and append-only guard -> testutil.AssertAppendOnly.
//
// internal/paths' own TestAppendOnlyGuard builds its layout by hand. That is the right unit test
// and it is not what this asserts. §7.4's invariant has to hold against the layout REAL CODE
// creates, because paths.EnsureLayout runs inside every hook and a regression there — a directory
// created with the wrong permissions, a protected path spelled differently — would leave
// TestAppendOnlyGuard green while the shipped product wrote through the guard.
func TestV1_AppendOnlyInvariantSurvivesRealHookRun(t *testing.T) {
	p := testutil.NewProject(t)

	// 1. Let real hook code create the layout.
	for _, call := range v1Session(p.Root) {
		p.RunHook(t, call.name, call.ev)
	}

	l := paths.Of(p.Root)
	pinLog := filepath.Join(l.Pins, "invariants.jsonl")
	bloom := filepath.Join(l.Sketches, "tried.bloom")

	// 2. The shipped assertion runs FIRST, because it seeds its own attack fixtures: it writes
	// checkpoints/0001.json through CreateNew — and treats that first write succeeding as the first
	// half of its duplicate-seq check — then seeds the pins ledger and the bloom. Pre-creating
	// 0001.json here would make its seeding step fail with os.ErrExist and report a false
	// violation, so the order is forced.
	p.AssertAppendOnly(t)

	// 3. Hand-place a SECOND checkpoint through the sanctioned door, so the five illegal writes
	// below attack an artifact this test created on the live, hook-made layout rather than one
	// AssertAppendOnly has already attacked.
	cp := paths.CheckpointPath(l, 2)
	require.NoError(t, paths.CreateNew(cp, []byte(`{"seq":2}`)))
	require.NoError(t, paths.AppendJSONL(pinLog, map[string]any{"op": "add", "id": "inv_v1"}))
	require.NoError(t, paths.ReplaceBloom(l, []byte("bloom-v1"), 2))

	// 4. The five illegal writes of TestAppendOnlyGuard, against this live layout.
	t.Run("a_trunc_on_checkpoint", func(t *testing.T) {
		_, err := paths.OpenFile(cp, os.O_WRONLY|os.O_TRUNC, 0o600)
		require.ErrorIs(t, err, core.ErrAppendOnly)
	})
	t.Run("b_nonappend_write_on_pins", func(t *testing.T) {
		_, err := paths.OpenFile(pinLog, os.O_WRONLY, 0o600)
		require.ErrorIs(t, err, core.ErrAppendOnly)
	})
	t.Run("c_writeatomic_on_bloom", func(t *testing.T) {
		require.ErrorIs(t, paths.WriteAtomic(bloom, []byte("replacement"), 0o600), core.ErrAppendOnly)
	})
	t.Run("d_createnew_twice", func(t *testing.T) {
		require.ErrorIs(t, paths.CreateNew(cp, []byte(`{"seq":2}`)), os.ErrExist)
	})
	t.Run("e_appendonly_wrong_extension", func(t *testing.T) {
		_, err := paths.AppendOnly(filepath.Join(l.Records, "x.json"))
		require.ErrorIs(t, err, core.ErrAppendOnly)
	})

	// 5. The checkpoint is immutable on disk, not merely guarded by the paths API.
	fi, err := os.Stat(cp)
	require.NoError(t, err)
	require.Zero(t, fi.Mode().Perm()&0o222, "a written checkpoint carries no write bit (§7.4)")

	before := v1Digests(t, cp, pinLog, bloom)

	// 6. A SECOND full session must not truncate or rewrite any protected file. This is the case a
	// hand-built layout cannot produce: EnsureLayout runs again, the logger reopens, and the hook
	// log grows — none of which may touch checkpoints/, pins/ or the bloom.
	for _, call := range v1Session(p.Root) {
		p.RunHook(t, call.name, call.ev)
	}
	require.Equal(t, before, v1Digests(t, cp, pinLog, bloom),
		"a second hook session rewrote a protected file (§7.4, §13 invariant 2)")
}

// TestV1_WriteSetConfinedAcrossFullHookSequence is §4 IT-4.
//
// Crosses every SP-01 package that touches the filesystem, through the REAL BINARY as separate
// processes. TestGuard_WriteSetConfinedToQompack already asserts the write set for six in-process
// hooks; this asserts it for the eight-call lifecycle run the way the host runs it, and adds the
// two things a process boundary makes observable: that the OS temp directory gains nothing that
// outlives the run, and that .qompack/tmp/ is empty at the end.
func TestV1_WriteSetConfinedAcrossFullHookSequence(t *testing.T) {
	bin := v1BuildBinary(t)
	p := testutil.NewProject(t, testutil.WithEnv(testutil.E2EBinaryEnv, bin))
	// v1Session's SessionStart call, run against the real binary below, brings up a real detached
	// daemon; shut it down before this test's own t.TempDir() cleanup runs, or a still-running
	// daemon holding its own executable open can make that cleanup fail on Windows ("Access is
	// denied" removing qompack.exe — open-running-executable semantics).
	t.Cleanup(func() { v1ShutdownDaemonIfReachable(t, p.Root) })

	osTemp := t.TempDir()
	t.Setenv("TMP", osTemp)
	t.Setenv("TEMP", osTemp)
	t.Setenv("TMPDIR", osTemp)

	before := snapshotTree(t, p.Root, p.Home(), osTemp)

	for _, call := range v1Session(p.Root) {
		p.RunHook(t, call.name, call.ev)
	}

	// Shut the daemon SessionStart brought up down explicitly, BEFORE snapshotTree walks the tree
	// a second time — not only in t.Cleanup, which would run after every assertion below (fix
	// round 1, Important I-9). A still-running daemon actively creating/renaming/deleting files
	// under .qompack/ (WAL rotation, run/spawn.lock, tmp/ staging) races filepath.WalkDir and
	// could fail this snapshot for reasons unrelated to the write-set invariant it exists to
	// check. t.Cleanup's own call is now a fast, idempotent no-op belt-and-braces (the daemon is
	// already gone by the time it runs).
	v1ShutdownDaemonIfReachable(t, p.Root)

	after := snapshotTree(t, p.Root, p.Home(), osTemp)

	allowed := []string{
		filepath.Join(p.Root, dotQompack) + string(os.PathSeparator),
		filepath.Join(p.Home(), dotQompack) + string(os.PathSeparator),
	}

	var offenders []string
	touchedStore := false
	for path, sum := range after {
		if old, existed := before[path]; existed && old == sum {
			continue
		}
		if underAny(path, allowed) {
			touchedStore = true
			continue
		}
		offenders = append(offenders, path)
	}
	sort.Strings(offenders)

	require.Empty(t, offenders,
		"§13 invariant 7: the hook sequence wrote outside %s and %s", allowed[0], allowed[1])
	require.True(t, touchedStore,
		"the hooks created nothing under .qompack/ — the guard would pass without testing anything")

	// Nothing survives in the OS temp dir. A spawned process that leaked a temp file would show up
	// here and nowhere else.
	for path := range after {
		if strings.HasPrefix(path, osTemp+string(os.PathSeparator)) {
			_, existed := before[path]
			require.True(t, existed, "the run left %s behind in the OS temp directory", path)
		}
	}

	// .qompack/tmp/ is WriteAtomic's staging area and must be empty once every write has landed.
	entries, err := os.ReadDir(paths.Of(p.Root).Tmp)
	require.NoError(t, err)
	require.Empty(t, entries, "WriteAtomic left staging files in .qompack/tmp/")
}

// v1CoverageFloors is 00-ARCHITECTURE.md §6.4's table, transcribed. It is written out here rather
// than read from plans/OWNERS.tsv because IT-6 is asserting that OWNERS.tsv AGREES with §6.4 —
// reading the floor from the file under test would make the assertion tautological.
var v1CoverageFloors = map[string]int{
	"config": 90, "store": 90, "sketch": 90, "chunk": 90,
	"canon": 90, "negknow": 90, "checkpoint": 90, "paths": 90,

	"scheduler": 85, "dag": 85, "analyzer": 85, "rehydrate": 85, "eval": 85, "mcp": 85,
}

// v1DefaultFloor is §6.4's "everything else".
const v1DefaultFloor = 75

// TestV1_StubGraphIsInertAndOwned is §4 IT-6.
//
// Crosses all 23 stub packages -> plans/OWNERS.tsv -> the on-disk package set.
//
// TestAllStubsReturnNotImplemented already walks the seams; what this adds is the OWNERSHIP half:
// that the table naming who implements what is in exact correspondence with what is on disk, and
// that every floor it claims is the floor §6.4 actually specifies. A package that appeared without
// an ownership decision, or a row whose floor drifted from the architecture, is invisible to every
// other test in the tree.
func TestV1_StubGraphIsInertAndOwned(t *testing.T) {
	root := repoRoot(t)

	rows := v1ReadOwners(t, filepath.Join(root, "plans", "OWNERS.tsv"))
	require.NotEmpty(t, rows, "OWNERS.tsv parsed to nothing — the assertions below would be vacuous")

	// 1. Exact correspondence with disk.
	onDisk := map[string]bool{"cmd/qompack": true}
	entries, err := os.ReadDir(filepath.Join(root, "internal"))
	require.NoError(t, err)
	for _, e := range entries {
		if e.IsDir() {
			onDisk[e.Name()] = true
		}
	}

	inFile := map[string]bool{}
	for _, r := range rows {
		inFile[r.pkg] = true
	}

	for pkg := range onDisk {
		require.True(t, inFile[pkg],
			"package %q exists on disk but has no plans/OWNERS.tsv row: a package cannot appear without an ownership decision", pkg)
	}
	for pkg := range inFile {
		require.True(t, onDisk[pkg],
			"plans/OWNERS.tsv names %q, which is not on disk", pkg)
	}

	// 2. Every floor matches §6.4.
	for _, r := range rows {
		want, ok := v1CoverageFloors[r.pkg]
		if !ok {
			want = v1DefaultFloor
		}
		require.Equal(t, want, r.floor,
			"plans/OWNERS.tsv gives %q a floor of %d; 00-ARCHITECTURE.md §6.4 says %d", r.pkg, r.floor, want)
	}

	// 3. Every SP-01 row is implemented (probe "-"); every other row still names a probe.
	for _, r := range rows {
		if r.owner == "SP-01" {
			require.Equal(t, "-", r.probe,
				"%q is owned by SP-01, so it is implemented now and has no stub probe", r.pkg)
			continue
		}
		require.NotEqual(t, "-", r.probe, "%q is stubbed by %s and must name a probe", r.pkg, r.owner)
	}

	// 4. No probe returns a data payload alongside its error. A stub that answered
	// (something, ErrNotImplemented) would be fabricating behaviour a caller might use.
	for _, pr := range []probe{evalProbe, storeProbe, negknowProbe, checkpointProbe, analyzerProbe} {
		require.True(t, isStub(t, pr), "%s must still be a stub at V1", pr.pkg)
	}
	v1AssertZeroPayloads(t)
}

// TestV1_ContractMonitorDegradesAndRestoresLoudly is §4 IT-7.
//
// Crosses contract -> logging's Loud channel -> obs -> paths' state file -> config.
//
// §13 invariant 10 says degradation is loud and a silent fallback is a bug regardless of how well
// it works. The monitor's own tests assert the state machine; this asserts that a transition
// actually reaches all three observers a human or a dashboard would look at, and — the part no
// single-package test can check — that the NUMBER of loud lines equals the number of transitions,
// so a degradation can neither be silent nor double-reported.
func TestV1_ContractMonitorDegradesAndRestoresLoudly(t *testing.T) {
	p := testutil.NewProject(t)
	l := paths.Of(p.Root)

	log, closer, err := logging.New(l.Logs, logging.Info)
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })

	reg := obs.New(p.Clock)

	// logging cannot import obs (§3.2 forbids the edge), so the loud counter only exists once a
	// COMPOSITION ROOT wires the two together. cli.AttachLoudCounter is what cmd/qompack calls at
	// startup; wiring it here is what makes "a degradation is counted" a real end-to-end assertion
	// rather than a property of a registry nothing is attached to. The observer is process-global,
	// so it is cleared again on the way out.
	cli.AttachLoudCounter(reg)
	t.Cleanup(func() { cli.AttachLoudCounter(nil) })

	statePath := filepath.Join(l.State, contractStateFile)
	m := contract.NewMonitor(log, reg, statePath)

	for _, a := range contract.StandardAssertions() {
		require.NoError(t, m.Register(a))
	}

	env := contract.Env{ProjectRoot: p.Root, Cfg: p.Cfg, Log: log, Clock: p.Clock}

	// (a) A clean run on a fresh build: ModeFull, every standard result not-yet-implemented, and
	// NOT ONE loud line. A build that shouted on startup would train everyone to ignore the
	// channel that §12.1 depends on.
	res, mode := m.RunAll(context.Background(), env)
	require.Equal(t, contract.ModeFull, mode)
	require.Len(t, res, len(contract.StandardAssertions()))
	for _, r := range res {
		require.True(t, r.OK, "%s must not fail on a fresh build", r.ID)
		require.Equal(t, contract.SevInfo, r.Severity, "%s", r.ID)
		require.Equal(t, notYetImplementedObserved, r.Observed, "%s", r.ID)
	}
	require.Equal(t, 0, v1LoudLines(t, l), "a clean run must be silent")
	require.Equal(t, int64(0), reg.Counter("loud.total").Value())

	// (b) A SevCritical failure degrades.
	const failing = contract.ID("v1.integration.synthetic")
	require.NoError(t, m.Register(contract.Assertion{
		ID:          failing,
		Severity:    contract.SevCritical,
		Description: "synthetic critical failure authored by V1 IT-7",
		Check: func(context.Context, contract.Env) contract.Result {
			return contract.Result{
				ID: failing, OK: false, Severity: contract.SevCritical,
				Expected: "the host contract holds", Observed: "deliberately broken by IT-7",
			}
		},
	}))

	_, mode = m.RunAll(context.Background(), env)
	require.Equal(t, contract.ModeDegradedPassive, mode)

	stateRaw, err := os.ReadFile(statePath)
	require.NoError(t, err, "§12.1: the reason must survive into the next SessionStart")
	require.Contains(t, string(stateRaw), string(failing), "state/contract.json must name the failed assertion")
	require.Contains(t, string(stateRaw), "deliberately broken by IT-7", "…and what was observed")

	require.Equal(t, 1, v1LoudLines(t, l), "the degradation must produce exactly one LOUD line")
	require.Contains(t, strings.Join(logging.LastLoud(), "\n"), string(failing),
		"logging.LastLoud is the in-process ring /qompack:status reads; the degradation must be in it")
	require.Equal(t, int64(1), reg.Counter("loud.total").Value())

	// (c) Two consecutive clean runs restore, logged just as loudly.
	m2 := contract.NewMonitor(log, reg, statePath)
	for _, a := range contract.StandardAssertions() {
		require.NoError(t, m2.Register(a))
	}
	require.Equal(t, contract.ModeDegradedPassive, m2.Mode(),
		"a degradation must be read back at construction (§12.1)")

	_, mode = m2.RunAll(context.Background(), env)
	require.Equal(t, contract.ModeDegradedPassive, mode, "one clean run is not enough to restore")
	require.Equal(t, 1, v1LoudLines(t, l), "a non-transition must not be logged")

	_, mode = m2.RunAll(context.Background(), env)
	require.Equal(t, contract.ModeFull, mode, "two consecutive clean runs restore (§12.1)")

	require.Equal(t, 2, v1LoudLines(t, l), "restoration is logged just as loudly as degradation")
	require.Equal(t, int64(2), reg.Counter("loud.total").Value())
}

// TestV1_ObsBudgetsAreConfigDrivenEndToEnd is §4 IT-8.
//
// Crosses config's load path -> the obs budget table -> Registry.CheckBudgets.
//
// obs' own TestBudgets_AllSixPresentAndConfigDriven mutates a config.Config value directly. This
// drives the change through config.Load instead, because §11.6's guarantee is about what an
// OPERATOR can change, and an operator changes a file or a flag, not a struct field.
func TestV1_ObsBudgetsAreConfigDrivenEndToEnd(t *testing.T) {
	p := testutil.NewProject(t)

	defaults, _, _, err := config.Load(config.Env{
		ProjectRoot: p.Root, HomeDir: p.Home(), Getenv: p.Getenv,
	})
	require.NoError(t, err)
	require.Equal(t, 15, defaults.Runtime.HotPath.BudgetMs, "B-A's default limit is 15 ms")

	tightened, _, _, err := config.Load(config.Env{
		ProjectRoot: p.Root, HomeDir: p.Home(), Getenv: p.Getenv,
		Flags: map[string]string{"runtime.hotPath.budgetMs": "1"},
	})
	require.NoError(t, err)
	require.Equal(t, 1, tightened.Runtime.HotPath.BudgetMs, "--set must reach the budget key")

	ba := v1BudgetByID(t, obs.BA)

	// Feed the B-A histogram 512 observations of exactly 10 ms.
	reg := obs.New(p.Clock)
	for i := 0; i < 512; i++ {
		reg.Hist(ba.Hist).Observe(10 * time.Millisecond)
	}

	// Under defaults, 10 ms is inside the 15 ms budget: no breach, three times running.
	for i := 1; i <= 3; i++ {
		require.Empty(t, reg.CheckBudgets(defaults), "call %d: 10ms is under the 15ms default", i)
	}

	// Under the loaded 1 ms config, the same histogram breaches, and the window count climbs.
	for want := 1; want <= 3; want++ {
		breaches := reg.CheckBudgets(tightened)
		require.Len(t, breaches, 1, "exactly one budget is over limit")
		require.Equal(t, string(obs.BA), breaches[0].Budget)
		require.Equal(t, time.Millisecond, breaches[0].Limit, "the limit came from the loaded config")
		require.GreaterOrEqual(t, breaches[0].Observed, 10*time.Millisecond)
		require.Equal(t, want, breaches[0].Windows, "Windows counts consecutive over-limit calls")
	}

	// The ungated budgets never surface, however bad their observations are.
	for _, id := range []obs.BudgetID{obs.BC, obs.BD} {
		b := v1BudgetByID(t, id)
		require.False(t, b.Gated, "%s is reported, never gated (§2.4)", id)
		reg.Hist(b.Hist).Observe(time.Hour)
	}
	for _, breach := range reg.CheckBudgets(tightened) {
		require.NotEqual(t, string(obs.BC), breach.Budget, "B-C is soft and must never gate")
		require.NotEqual(t, string(obs.BD), breach.Budget, "B-D is reported-only and must never gate")
	}

	// B-F is the one budget evaluated at p95 rather than p99.
	require.Equal(t, 95, v1BudgetByID(t, obs.BF).Pct, "B-F gates at p95 (§2.4)")
	for _, id := range []obs.BudgetID{obs.BA, obs.BB, obs.BE} {
		require.Equal(t, 99, v1BudgetByID(t, id).Pct, "%s gates at p99 (§2.4)", id)
	}
}

// v1FixtureType maps a frozen format fixture to the declared Go type it must round-trip through.
// The manifest names a file, not a type, so the correspondence lives here — and it is the point of
// the test: a frozen on-disk shape that no Go type can carry losslessly is a format nobody can
// actually read.
var v1FixtureType = map[string]func() any{
	"store/tool_use_line":        func() any { return new(store.ToolUseRecord) },
	"store/roots_line":           func() any { return new(store.Root) },
	"store/segments_line":        func() any { return new(store.Segment) },
	"dag/node_line":              func() any { return new(dag.Node) },
	"dag/edge_line":              func() any { return new(dag.Edge) },
	"negknow/elimination_record": func() any { return new(negknow.Record) },
	"paths/manifest_entry":       func() any { return new(paths.ManifestEntry) },
	"checkpoint/checkpoint_v1":   func() any { return new(checkpoint.Checkpoint) },
	"contract/result_set":        func() any { return new([]contract.Result) },
	"ipc/observe_tool":           func() any { return new(ipc.Request) },
	"ipc/response_reply":         func() any { return new(ipc.Response) },
}

// v1BinaryFixtureKeys names frozen format fixtures whose bytes are not JSON at all — a wire format
// 00-ARCHITECTURE.md §2.4 specifies as a fixed-layout binary record, not a JSON document (SP-05's
// 32-byte hot-path state record). v1RoundTrip only checks these are present and non-empty;
// json.Unmarshal has nothing to parse in a CRC-terminated binary record.
var v1BinaryFixtureKeys = map[string]bool{
	"ipc/state_degraded": true,
}

// v1CheckpointTiers is §8.5's complete field set: tiers 1-3 plus metadata. Every one must be
// populated in the frozen artifact, or the fixture is not the worked example §16 promises.
var v1CheckpointTiers = []string{
	"invariants", "user_intent", "eliminated", "decisions", "open_questions",
	"current_work", "pointers", "narrative", "sketch_refs", "dropped", "cache",
}

// TestV1_FrozenContractFixturesRoundTripIntoDeclaredTypes is §4 IT-9.
//
// Crosses testdata/golden/contracts/** -> hookio, checkpoint, pins, negknow, store, dag, paths,
// sketch.
//
// Rule W-2 makes a frozen fixture byte-final for every wave: "a fixture the real implementation
// cannot reproduce is a verification failure, not a fixture bug." That rule is only enforceable if
// the frozen bytes are actually consumable TODAY by the declared types. If a frozen line carries a
// field its Go type does not model, the first wave-1 subplan to round-trip it silently drops that
// field and the golden becomes unreproducible — at which point W-2 blames the implementation for a
// fixture that was never valid.
//
// The corpus is not all JSON. SP-03's five §5.7 sketch fixtures are QPKS frames: a versioned,
// checksummed byte layout whose entire reason for existing is that a sketch written by one plugin
// version still decodes years later (§6.2 — tried.bloom is permanent memory and is never
// regenerated). A JSON round-trip cannot express that property, so v1RoundTrip dispatches on the
// declared want extension and checks each wire form with the assertion that form actually supports.
// The dispatch is exhaustive by construction: an extension no arm claims fails the test.
func TestV1_FrozenContractFixturesRoundTripIntoDeclaredTypes(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "testdata", "golden", "contracts")

	pkgs, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.NotEmpty(t, pkgs)

	frozen, behaviour := 0, 0
	for _, pkg := range pkgs {
		if !pkg.IsDir() {
			continue
		}
		manifest := v1ReadManifest(t, filepath.Join(dir, pkg.Name(), "MANIFEST.json"))
		require.Equal(t, pkg.Name(), manifest.Package, "MANIFEST.json disagrees with its directory")
		require.NotEmpty(t, manifest.Owner)

		for _, fx := range manifest.Fixtures {
			key := pkg.Name() + "/" + fx.Name

			switch fx.Kind {
			case "behaviour":
				behaviour++
				require.Equal(t, "record-by-owner", fx.State,
					"%s: a behaviour fixture cannot be frozen before its owner records it", key)
				require.Empty(t, fx.Want, "%s: a record-by-owner fixture has no want yet", key)

				// The accessor must agree.
				_, want, isFrozen := testutil.ContractFixture(t, pkg.Name(), fx.Name)
				require.False(t, isFrozen, "%s: accessor must report frozen == false", key)
				require.Nil(t, want, "%s", key)

			case "format":
				frozen++
				require.Equal(t, "frozen", fx.State,
					"%s: §16 says format fixtures are authorable now and are frozen by SP-01", key)

				if fx.Want == "" {
					// An input-only fixture: the frozen artifact is the wire form itself.
					require.NotEmpty(t, fx.Input, "%s: a format fixture with neither input nor want is empty", key)
					continue
				}
				v1RoundTrip(t, key, filepath.Join(dir, pkg.Name(), filepath.FromSlash(fx.Want)))

			default:
				t.Fatalf("%s: unknown fixture kind %q", key, fx.Kind)
			}
		}
	}
	require.Positive(t, frozen, "no frozen format fixtures found — this test would assert nothing")
	require.Positive(t, behaviour, "no behaviour fixtures found")

	t.Run("hookio_covers_every_event_and_source", func(t *testing.T) {
		wantEvents := map[string]bool{
			"PostToolUse": false, "UserPromptSubmit": false, "SessionStart": false,
			"PreCompact": false, "Stop": false, "SubagentStop": false, "SessionEnd": false,
		}
		wantSources := map[string]bool{"startup": false, "resume": false, "compact": false, "clear": false}

		manifest := v1ReadManifest(t, filepath.Join(dir, "hookio", "MANIFEST.json"))
		for _, fx := range manifest.Fixtures {
			if fx.Input == "" {
				continue
			}
			raw, readErr := os.ReadFile(filepath.Join(dir, "hookio", filepath.FromSlash(fx.Input)))
			require.NoError(t, readErr)

			ev, _, evErr := hookio.ReadEvent(strings.NewReader(string(raw)), int64(len(raw)))
			require.NoError(t, evErr, "%s must parse", fx.Name)

			if _, ok := wantEvents[ev.HookEventName]; ok {
				wantEvents[ev.HookEventName] = true
			}
			if _, ok := wantSources[ev.Source]; ok {
				wantSources[ev.Source] = true
			}
		}
		for name, seen := range wantEvents {
			require.True(t, seen, "no frozen hookio fixture covers %s", name)
		}
		for src, seen := range wantSources {
			require.True(t, seen, "no frozen SessionStart fixture covers source=%q", src)
		}
	})

	t.Run("checkpoint_golden_populates_every_tier_and_carries_no_code", func(t *testing.T) {
		raw, readErr := os.ReadFile(filepath.Join(dir, "checkpoint", "want", "0001.json"))
		require.NoError(t, readErr)

		require.NotContains(t, string(raw), "```",
			"§13 invariant 5: a checkpoint carries {path, hash, why}, never a code snippet")

		var doc map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &doc))
		for _, tier := range v1CheckpointTiers {
			v, ok := doc[tier]
			require.True(t, ok, "the frozen checkpoint omits %q", tier)
			require.NotEqual(t, "null", string(v), "%q is null", tier)
			require.NotEqual(t, "[]", strings.TrimSpace(string(v)), "%q is empty", tier)
		}
	})

	t.Run("pins_records_carry_a_round_trippable_invariant", func(t *testing.T) {
		for _, name := range []string{"add", "tombstone"} {
			raw, readErr := os.ReadFile(filepath.Join(dir, "pins", "want", name+".jsonl"))
			require.NoError(t, readErr)

			var envelope struct {
				Op        string          `json:"op"`
				TS        int64           `json:"ts"`
				Invariant json.RawMessage `json:"invariant"`
			}
			require.NoError(t, json.Unmarshal(raw, &envelope), "%s", name)
			require.Contains(t, []string{"add", "remove"}, envelope.Op)
			require.Positive(t, envelope.TS)

			if len(envelope.Invariant) > 0 && string(envelope.Invariant) != "null" {
				var inv pins.Invariant
				require.NoError(t, json.Unmarshal(envelope.Invariant, &inv))
				v1RequireCanonicalEqual(t, "pins/"+name+".invariant", envelope.Invariant, inv)
			}
		}
	})
}

// v1Gate names one toolchain gate and the test that proves it can fail.
type v1Gate struct {
	pkg  string
	test string
	why  string
}

// v1Gates is the four gates every later wave depends on, paired with their negative tests.
// Each gate names its EXACT package, never a `...` pattern. A pattern that expands to more than one
// package makes the "no tests to run" assertion below meaningless, because the sibling packages the
// pattern also matched legitimately report it.
var v1Gates = []v1Gate{
	{
		"./tools/lint/nomagic", "TestNoMagic_Analyzer",
		"D11/§11.6: a float or int literal duplicating a config default outside defaults.go",
	},
	{
		"./tools/devtool", "TestImportGraph_RejectsViolation",
		"§3.2: an edge the layer map forbids, e.g. store -> negknow",
	},
	{
		"./tools/devtool", "TestTestDeps_RejectsProductionTestify",
		"§2.5: a test-only dependency reaching a production package",
	},
	{
		"./tools/lint/nomagic", "TestScanAllowComments_BareAllowIsNotExempt",
		"§11.6: a bare //nomagic:allow with no reason must not exempt anything",
	},
}

// TestV1_ToolchainGatesRejectRealViolations is §4 IT-10.
//
// Crosses tools/lint/nomagic and devtool's importgraph/testdeps/bindeps rule tables.
//
// The gates cannot be called directly from here: devtool and nomagic are `package main`, so a
// composition root cannot import them, and §4's suggested `guardprobe` build tag would mean
// shipping a tag into the tool whose only purpose is bypassing it. So this drives their negative
// tests as subprocesses instead, and asserts the thing that actually goes wrong in practice:
// `go test -run <name>` EXITS 0 WHEN <name> MATCHES NOTHING. A renamed or deleted negative test
// leaves every gate looking verified while nothing is verified at all — which is exactly how a
// vacuous gate reaches wave 1.
func TestV1_ToolchainGatesRejectRealViolations(t *testing.T) {
	if testing.Short() {
		t.Skip("platform: -short skips the subprocess gate probes")
	}
	root := repoRoot(t)

	for _, g := range v1Gates {
		t.Run(g.test, func(t *testing.T) {
			cmd := exec.CommandContext(context.Background(),
				"go", "test", "-count=1", "-v", "-run", "^"+g.test+"$", g.pkg)
			cmd.Dir = root
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, "%s must pass:\n%s", g.test, out)

			text := string(out)
			require.NotContains(t, text, "no tests to run",
				"%s matched NO test — the gate for %q is unverified, and `go test -run` "+
					"exits 0 in that case, so this would have looked green", g.test, g.why)
			require.Contains(t, text, "--- PASS: "+g.test,
				"%s did not report a pass:\n%s", g.test, text)
		})
	}

	// Deliberately NOT run here: `devtool lint` itself. Its stubskips sub-check shells out to the
	// whole test suite in order to classify every skip reason, so invoking it from inside a test
	// recurses into this package and hangs. `lint` is the main session's and CI's job (§1 B5, §8
	// verify job); what belongs in the suite is the proof that each gate can still fail.
}

// --- helpers -------------------------------------------------------------------------------

// v1Digests hashes the given files, for a before/after comparison.
func v1Digests(t *testing.T, files ...string) map[string]string {
	t.Helper()
	got := map[string]string{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		require.NoError(t, err, "digesting %s", f)
		sum := sha256.Sum256(b)
		got[f] = hex.EncodeToString(sum[:])
	}
	return got
}

// v1BuildBinary compiles cmd/qompack into a temp directory. test/guards may not import test/e2e —
// §3.2 makes both composition roots and forbids anything importing them — so the build is repeated
// here rather than shared.
func v1BuildBinary(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "qompack")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.CommandContext(context.Background(), "go", "build", "-o", out, "./cmd/qompack")
	cmd.Dir = repoRoot(t)
	combined, err := cmd.CombinedOutput()
	require.NoError(t, err, "building ./cmd/qompack:\n%s", combined)
	return out
}

// v1ProbeTimeout, v1RoundTripDeadline and v1ShutdownPollBound/Tick bound the raw ipc.Client
// v1ShutdownDaemonIfReachable constructs to poke and then stop a real daemon this file's own
// end-to-end hook runs may have started.
const (
	v1ProbeTimeout      = 200 * time.Millisecond
	v1RoundTripDeadline = 5 * time.Second
	v1ShutdownPollBound = 15 * time.Second
	v1ShutdownPollTick  = 100 * time.Millisecond
)

// v1ShutdownDaemonIfReachable dials root's resolved address and, only if something answers, sends
// admin.shutdown (retried, since Client.Send never propagates an error — a failed round trip just
// spools the request instead of delivering it) and waits for the daemon to go away. It is a fast
// no-op whenever no daemon ever came up.
func v1ShutdownDaemonIfReachable(t *testing.T, root string) {
	t.Helper()
	addr, err := ipc.Resolve(root)
	if err != nil {
		return
	}
	if !ipc.Probe(addr, v1ProbeTimeout) {
		return
	}

	sp, _ := ipc.NewSpool(paths.Of(root).Spool)
	c := ipc.NewClientWithOptions(addr, sp, nil, nil, ipc.ClientOptions{ProjectRoot: root})
	defer func() { _ = c.Close() }()

	// A ticker, not time.Sleep, per §6.1's wall-clock-sleep ban (devtool lint's sleepcheck
	// sub-check, which exempts only test/bench/**).
	ticker := time.NewTicker(v1ShutdownPollTick)
	defer ticker.Stop()
	timeout := time.NewTimer(v1ShutdownPollBound)
	defer timeout.Stop()
	for {
		_, _ = c.Send(context.Background(), ipc.Request{
			Op: ipc.OpAdminShutdown, TS: core.NowMilli(core.SystemClock()), Reply: true,
		}, v1RoundTripDeadline)
		if !ipc.Probe(addr, v1ProbeTimeout) {
			return
		}
		select {
		case <-ticker.C:
		case <-timeout.C:
			if ipc.Probe(addr, v1ProbeTimeout) {
				t.Logf("v1ShutdownDaemonIfReachable: daemon at %s still reachable after %s of retried admin.shutdown; leaving it running", root, v1ShutdownPollBound)
			}
			return
		}
	}
}

// v1LoudLines counts the lines in <logs>/LOUD.log, which §12 makes the never-rotated record of
// every degradation and restoration.
func v1LoudLines(t *testing.T, l paths.Layout) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(l.Logs, "LOUD.log"))
	if os.IsNotExist(err) {
		entries, _ := os.ReadDir(l.Logs)
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Logf("LOUD.log absent in %s (nothing loud has happened yet); directory holds: %v", l.Logs, names)
		return 0
	}
	require.NoError(t, err)

	n := 0
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			n++
		}
	}
	require.NoError(t, sc.Err())
	return n
}

// v1BudgetByID returns one budget from the §2.4 table.
func v1BudgetByID(t *testing.T, id obs.BudgetID) obs.Budget {
	t.Helper()
	for _, b := range obs.Budgets() {
		if b.ID == id {
			return b
		}
	}
	t.Fatalf("obs.Budgets() does not contain %s", id)
	return obs.Budget{}
}

// v1OwnerRow is one parsed plans/OWNERS.tsv line.
type v1OwnerRow struct {
	pkg, owner, probe string
	floor             int
}

// v1ReadOwners parses plans/OWNERS.tsv into rows.
func v1ReadOwners(t *testing.T, path string) []v1OwnerRow {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)

	var rows []v1OwnerRow
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		require.Len(t, f, 4, "OWNERS.tsv row is not four tab-separated fields: %q", line)
		if f[0] == "package" {
			continue
		}
		var floor int
		_, err := fmt.Sscanf(strings.TrimSpace(f[2]), "%d", &floor)
		require.NoError(t, err, "row %q has a non-numeric floor", line)
		rows = append(rows, v1OwnerRow{
			pkg: strings.TrimSpace(f[0]), owner: strings.TrimSpace(f[1]),
			floor: floor, probe: strings.TrimSpace(f[3]),
		})
	}
	require.NoError(t, sc.Err())
	return rows
}

// v1AssertZeroPayloads calls each build-order probe's seam and asserts the value returned beside
// ErrNotImplemented is the zero value. A stub returning real-looking data alongside its error is
// the failure mode §5.22's "no behaviour is faked" exists to prevent.
func v1AssertZeroPayloads(t *testing.T) {
	t.Helper()
	log, m, clk := probeDeps()

	s, err := store.Open(t.TempDir(), config.Defaults(), store.Deps{Log: log, Metrics: m, Clock: clk})
	require.NoError(t, err)
	res, err := s.PutBytes(context.Background(), []byte("probe"), store.PutOptions{})
	require.True(t, core.IsNotImplemented(err))
	require.Equal(t, store.PutResult{}, res, "store.PutBytes returned a payload beside its error")

	led, err := negknow.Open(t.TempDir(), config.Defaults(), nil,
		negknow.Deps{Log: log, Metrics: m, Clock: clk})
	require.NoError(t, err)
	ans, err := led.Query(context.Background(), "t", "a", negknow.ScopeProject)
	require.True(t, core.IsNotImplemented(err))
	require.Equal(t, negknow.Answer{}, ans, "negknow.Query returned an answer beside its error")
}

// v1Manifest mirrors testdata/golden/contracts/*/MANIFEST.json.
type v1Manifest struct {
	Package  string `json:"package"`
	Owner    string `json:"owner"`
	Fixtures []struct {
		Name  string `json:"name"`
		Kind  string `json:"kind"`
		State string `json:"state"`
		Input string `json:"input"`
		Want  string `json:"want"`
		Note  string `json:"note"`
	} `json:"fixtures"`
}

// v1ReadManifest decodes one fixture manifest.
func v1ReadManifest(t *testing.T, path string) v1Manifest {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err, "every contract fixture directory needs a MANIFEST.json")
	var m v1Manifest
	require.NoError(t, json.Unmarshal(b, &m), "parsing %s", path)
	return m
}

// v1RoundTrip asserts a frozen artifact is still readable by the declared decoder for its wire
// form, and dispatches to the arm for that form.
//
// The dispatch is on the DECLARED extension of the manifest's want path, never on "try JSON and
// fall back to binary". A fallback would let a corrupt JSON fixture pass as "probably binary",
// which is precisely the rot this walker exists to catch. So every fixture is checked by exactly
// one arm, and an extension with no arm is a hard failure rather than a silent skip: a new wire
// form must arrive with its own assertion, not slip through as unchecked bytes.
func v1RoundTrip(t *testing.T, key, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "%s: frozen fixture missing from disk", key)

	if v1BinaryFixtureKeys[key] {
		require.NotEmpty(t, raw, "%s: frozen fixture is empty", key)
		return
	}
	require.NotEmpty(t, strings.TrimSpace(string(raw)), "%s: frozen fixture is empty", key)

	switch {
	case strings.HasSuffix(path, ".bin"):
		v1RoundTripQPKS(t, key, raw)
	case strings.HasSuffix(path, ".jsonc"), strings.HasSuffix(path, ".jsonl"),
		strings.HasSuffix(path, ".ndjson"), strings.HasSuffix(path, ".json"):
		v1RoundTripJSON(t, key, path, raw)
	default:
		t.Fatalf("%s: frozen fixture %s has an extension no round-trip arm claims. Every frozen "+
			"artifact must be checked by exactly one arm; add one for this wire form rather than "+
			"letting the fixture through unchecked (Rule W-2).", key, path)
	}
}

// v1RoundTripJSON is the arm for every JSON-flavoured frozen artifact: .json, .jsonl, .ndjson and
// .jsonc. It unmarshals into the declared Go type and asserts the type can reproduce the bytes
// exactly.
//
// .ndjson is here because of the wave-1 merge, and the reason is worth keeping. SP-05 froze
// ipc/observe_tool.ndjson and ipc/response_reply.ndjson against a v1RoundTrip that had no
// extension dispatch at all — every non-binary fixture fell through to this JSON path, so .ndjson
// worked without ever being named. SP-03 then made the dispatch exhaustive for its .bin QPKS
// frames, with a fatal default. Each branch was right on its own and the composition rejected two
// fixtures that had been passing: an arm the exhaustive switch never learned about. NDJSON is
// line-delimited JSON and v1FirstLine already reads exactly one record, so it belongs with .jsonl
// rather than in v1BinaryFixtureKeys, which would have downgraded the check to "file is non-empty".
func v1RoundTripJSON(t *testing.T, key, path string, raw []byte) {
	t.Helper()

	// A .jsonc fixture carries comments, which no JSON decoder accepts. Stripping is the same step
	// every real reader of these bytes performs — config.Load runs StripJSONC before parsing — so
	// doing it here keeps the walker honest rather than special-casing the file out of the check.
	if strings.HasSuffix(path, ".jsonc") {
		raw = config.StripJSONC(raw)
	}

	newValue, ok := v1FixtureType[key]
	if !ok {
		// Not every frozen artifact has a single declared type (the pins envelope is checked in
		// its own subtest). Still assert it is well-formed JSON so the corpus cannot rot.
		var any0 any
		require.NoError(t, json.Unmarshal(v1FirstLine(raw), &any0), "%s is not valid JSON", key)
		return
	}

	v := newValue()
	dec := json.NewDecoder(strings.NewReader(string(v1FirstLine(raw))))
	require.NoError(t, dec.Decode(v), "%s does not unmarshal into its declared type", key)
	v1RequireCanonicalEqual(t, key, v1FirstLine(raw), v)
}

// qpksFixedOverhead is the part of a QPKS frame that can never be body: the fixed 32-byte prefix
// (magic, Ver, Kind, Reserved, Count, Created, ParamCount, BodyLen) plus the trailing 4-byte
// CRC32C. It is spelled here rather than imported because internal/sketch keeps its frame layout
// constants unexported, which is correct — the layout is that package's private business, and this
// guard only needs the two boundaries §5.7 documents publicly.
const qpksFixedOverhead = 32 + 4

// v1RoundTripQPKS is the arm for a .bin frozen artifact: a QPKS sketch frame (§5.7).
//
// A binary fixture is legitimate here, and is not an exception carved out of the JSON rule. The
// QPKS frame is a byte layout SP-03 specifies NORMATIVELY — magic, an explicit version, a kind, a
// sorted params block, a body, and a trailing CRC32C over everything before it — and its whole
// purpose is that a sketch written by one plugin version still decodes years later. §6.2 makes
// these structures permanent memory: tried.bloom is never regenerated, so the oldest file on disk
// has to keep decoding forever, and a decoder that quietly half-read it would be worse than one
// that refused. That is a property a JSON round-trip cannot express at all — JSON has no version
// field, no checksum, and says nothing about what an older reader must still accept. So this arm
// makes the binary equivalent of the JSON arm's assertion, that the declared decoder can still
// read what was frozen, in the terms the format actually has.
//
// CRC32C is asserted non-zero deliberately. sketch.Sketch documents that a LIVE sketch reports
// CRC32C == 0, because the checksum covers the encoded body and cannot be known without
// marshalling; only DecodeHeader populates it, from the frame's trailing four bytes. A non-zero
// value here is therefore evidence the checksum was really read off disk and verified, rather than
// defaulted by a decoder that skipped the check.
func v1RoundTripQPKS(t *testing.T, key string, raw []byte) {
	t.Helper()

	h, body, err := sketch.DecodeHeader(raw)
	require.NoError(t, err, "%s: frozen fixture is not a decodable QPKS frame", key)

	require.Equal(t, sketch.HeaderMagic, h.Magic, "%s: the frame does not begin with QPKS", key)
	require.Equal(t, sketch.FormatVersion, h.Ver,
		"%s: a frozen v1 frame must still decode at the CURRENT FormatVersion. A layout change "+
			"requires a version bump plus a new decoder case arm, keeping `case 1:` forever — never "+
			"a rewritten fixture (Rule W-2).", key)
	require.True(t, h.Kind.Valid(), "%s: kind %v is not one of the five defined sketch kinds", key, h.Kind)
	require.NotZero(t, h.CRC32C,
		"%s: DecodeHeader must report the checksum it read off disk; a zero here means the CRC was "+
			"never taken from the frame", key)

	// The body is a subslice of raw, so its length is the one number that proves the frame's
	// declared BodyLen agreed with the bytes actually on disk rather than being taken on trust.
	require.NotEmpty(t, body, "%s: a frozen sketch frame with an empty body carries no state", key)
	require.LessOrEqual(t, len(body)+qpksFixedOverhead, len(raw),
		"%s: DecodeHeader returned a %d-byte body, which cannot fit inside a %d-byte frame alongside "+
			"the 32-byte prefix, the params block and the 4-byte CRC32C", key, len(body), len(raw))
}

// v1RequireCanonicalEqual asserts that re-marshalling v reproduces raw, comparing canonically so
// key order and insignificant whitespace do not matter but a DROPPED OR ADDED FIELD does.
func v1RequireCanonicalEqual(t *testing.T, key string, raw []byte, v any) {
	t.Helper()
	round, err := json.Marshal(v)
	require.NoError(t, err, "%s does not marshal", key)

	require.Equal(t, v1Canonical(t, raw), v1Canonical(t, round),
		"%s: the declared Go type cannot reproduce the frozen bytes.\n"+
			"A field present on disk that the type does not model is silently dropped here, which "+
			"makes the fixture unreproducible for every later wave (Rule W-2).", key)
}

// v1Canonical renders JSON with sorted keys so two encodings of the same document compare equal.
func v1Canonical(t *testing.T, b []byte) string {
	t.Helper()
	var v any
	require.NoError(t, json.Unmarshal(b, &v))
	out, err := json.Marshal(v1Sort(v))
	require.NoError(t, err)
	return string(out)
}

// v1Sort rewrites every map in a decoded document as a key-sorted ordered form.
func v1Sort(v any) any {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make([]json.RawMessage, 0, len(keys))
		for _, k := range keys {
			b, _ := json.Marshal(v1Sort(t[k]))
			kb, _ := json.Marshal(k)
			out = append(out, json.RawMessage(string(kb)+":"+string(b)))
		}
		joined := make([]string, len(out))
		for i, o := range out {
			joined[i] = string(o)
		}
		return json.RawMessage("{" + strings.Join(joined, ",") + "}")
	case []any:
		for i := range t {
			t[i] = v1Sort(t[i])
		}
		return t
	default:
		return v
	}
}

// v1FirstLine returns the first non-empty line of a .jsonl artifact, or the whole document for a
// .json one.
func v1FirstLine(b []byte) []byte {
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) != "" {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
				return []byte(trimmed)
			}
			break
		}
	}
	return b
}
