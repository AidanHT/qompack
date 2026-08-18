// dagselection_test.go implements plans/V2-VERIFY-primitives-store-dag-and-baseline.md section
// 4.5: the DAG, positions and the p-selection substrate. Three seams are exercised with every
// dependency real:
//
//   - eval's clairvoyant keep-set against dag's coupling measure, over the whole committed
//     synthetic corpus (the wave-1 low-coupling baseline);
//   - dag.BackwardSlice scores resolving through store.ToolUse with nothing dangling and nothing
//     collapsed into a keep-set;
//   - symbol nodes minted from the REAL extractor's names, with dag never importing symbols.
//
// Section 4.5's guard applies to the first test: it must not import internal/analyzer or
// internal/scheduler, and it must never turn slice scores into a keep-set. Nothing in this file
// does either. Every helper and constant carries a dagsel prefix so sibling section-4 files in
// this package cannot collide with it.
package integration

import (
	"context"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/eval"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/symbols"
	"github.com/qompack/qompack/internal/testutil"
)

// dagselModulePath is this repository's module path, used by the import-graph guard.
const dagselModulePath = "github.com/qompack/qompack"

// dagselCorpusSessions is the committed synthetic corpus size: eight shapes, three seeds each
// (internal/eval/corpus.go). Section 4.5 measures over every one of them.
const dagselCorpusSessions = 24

// dagselSampleCount is how many uniformly spaced positions the low-coupling comparison samples
// per compaction event, and dagselLowCouplingFloor is section 4.5's floor: the fraction of events
// in which CrossingEdges(p_min) must land strictly below the sampled mean. The floor is the
// spec's own number and must never be lowered to fit a measurement.
const (
	dagselSampleCount      = 32
	dagselLowCouplingFloor = 0.70
)

// dagselPosStep spaces the token positions of the tool uses the store-backed tests ingest. The
// value only has to be positive and increasing; it is not a Qompack tunable.
const dagselPosStep = 1000

// The real fixture set the store-backed tests ingest: SP-06's TypeScript-flavoured FileRead
// captures, all of one logical file, ascending versions (see the .meta.json beside each).
const (
	dagselFixtureDir = "testdata/corpora/toolout/sp06"
	dagselAuthPath   = "src/auth.ts"
	dagselAuthTool   = "Read"
)

// dagselAuthFixtures lists the ingested fixture basenames, oldest first.
var dagselAuthFixtures = []string{
	"fileread-auth-v1.txt",
	"fileread-auth-v2.txt",
	"fileread-auth-v3.txt",
	"fileread-auth-v4.txt",
}

// dagselSessionID names the synthetic session the store-backed tests record tool uses under.
const dagselSessionID = core.SessionID("sess_dagsel_integration")

// dagselWritingTools is the set of tool names that mutate a path, deciding the direction of the
// shared-state edges dag.BuildToolUse emits (D-1).
var dagselWritingTools = map[string]bool{
	"Edit":      true,
	"Write":     true,
	"MultiEdit": true,
}

// dagselModuleRoot walks up from the test's working directory (test/integration) to the
// directory holding go.mod, so fixture paths do not depend on how the test binary was invoked.
func dagselModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, parent, dir, "walked past the filesystem root without finding go.mod")
		dir = parent
	}
}

// dagselLoadCorpus loads the committed synthetic corpus through the real eval harness, exactly
// as test/replay does, and pins its size.
func dagselLoadCorpus(t *testing.T) []eval.Session {
	t.Helper()
	dir := filepath.Join(dagselModuleRoot(t), "testdata", "sessions", "synthetic")
	sessions, err := eval.New(eval.Options{}).Load(dir)
	require.NoError(t, err)
	require.Len(t, sessions, dagselCorpusSessions,
		"the committed corpus is %d sessions; a different count means the corpus changed under this baseline", dagselCorpusSessions)
	return sessions
}

// dagselReadFixture reads one committed tool-output fixture.
func dagselReadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dagselModuleRoot(t), filepath.FromSlash(dagselFixtureDir), name))
	require.NoError(t, err, "committed fixture missing: %s", name)
	require.NotEmpty(t, b)
	return b
}

// dagselPrev carries the predecessor link BuildToolUse chains observations with.
type dagselPrev struct {
	id   core.ToolUseID
	turn core.TurnIndex
}

// dagselToolResultPositions maps each turn to its tool-result block positions, in tool-call
// order. eval.Blocks emits one BlockToolResult per tool call, in ToolCalls order, so the i-th
// entry of a turn's slice is the position of that turn's i-th tool call.
func dagselToolResultPositions(blocks []eval.Block) map[core.TurnIndex][]int {
	m := make(map[core.TurnIndex][]int)
	for _, b := range blocks {
		if b.Kind == eval.BlockToolResult {
			m[b.Turn] = append(m[b.Turn], b.Pos)
		}
	}
	return m
}

// dagselPrefixTokens is n: the position-advancing token length of the prefix the blocks
// describe. Only the three prefix-content kinds advance a position (eval.Blocks); derived blocks
// alias positions already counted.
func dagselPrefixTokens(blocks []eval.Block) int {
	n := 0
	for _, b := range blocks {
		switch b.Kind {
		case eval.BlockToolResult, eval.BlockUserPrompt, eval.BlockAssistant:
			if end := b.Pos + int(b.Tokens); end > n {
				n = end
			}
		default:
			// Derived block kinds re-use positions counted above.
		}
	}
	return n
}

// dagselFeedToolCalls replays turns [from, to) of s into g through dag.BuildToolUse, using the
// eval block positions in pos as each node's Pos, and returns the updated predecessor link.
func dagselFeedToolCalls(t *testing.T, g dag.Graph, s eval.Session, from, to int,
	pos map[core.TurnIndex][]int, prev dagselPrev,
) dagselPrev {
	t.Helper()
	for ti := from; ti < to; ti++ {
		turn := s.Turns[ti]
		for ci, tc := range turn.ToolCalls {
			if tc.ID == "" {
				continue
			}
			positions := pos[turn.Index]
			require.Greater(t, len(positions), ci,
				"session %s turn %d: eval.Blocks emitted fewer tool-result blocks than tool calls", s.ID, ti)
			o := dag.ObservedTool{
				ToolUseID:     tc.ID,
				PrevToolUseID: prev.id,
				PrevTurn:      prev.turn,
				Turn:          turn.Index,
				TS:            turn.TS,
				Pos:           positions[ci],
				Tool:          tc.Name,
				Writes:        dagselWritingTools[tc.Name],
			}
			if len(tc.Paths) > 0 {
				o.PathKey = paths.Key(tc.Paths[0])
			}
			require.NoError(t, dag.BuildToolUse(g, o))
			prev = dagselPrev{id: tc.ID, turn: turn.Index}
		}
	}
	return prev
}

// dagselSampleMeanCrossing is the comparison side of the section 4.5 measurement: the mean of
// CrossingEdges over dagselSampleCount positions spaced evenly through (0, n]. Even spacing is
// the deterministic uniform sampling the spec permits — no randomness, seeded or otherwise.
func dagselSampleMeanCrossing(g dag.Graph, n int) float64 {
	total := 0
	for i := 0; i < dagselSampleCount; i++ {
		pos := (i + 1) * n / (dagselSampleCount + 1)
		if pos < 1 {
			pos = 1
		}
		total += g.CrossingEdges(pos)
	}
	return float64(total) / float64(dagselSampleCount)
}

// TestIntegration_BeladyPMinLandsAtLowCoupling is section 4.5's first empirical evidence that
// "cut where coupling is low" and "cut where OPT drops the earliest block" agree — the
// hypothesis SP-12's p-selection is built on.
//
// For each of the 24 synthetic corpus sessions, at each compaction turn: derive
// eval.Blocks(s, at); build a dag.Graph whose node Pos values are those block positions, edges
// via dag.BuildToolUse from the session's own tool calls; compute eval.BeladyDetail to get
// KeepSet.P. Across all compaction events, dag.CrossingEdges(P) must be strictly lower than the
// mean of CrossingEdges over 32 evenly spaced positions in the same prefix, in at least 70% of
// events. This is a measurement with a floor, not a proof: the observed percentage is logged and
// recorded in the completion report as the wave-1 baseline.
func TestIntegration_BeladyPMinLandsAtLowCoupling(t *testing.T) {
	ctx := context.Background()
	sessions := dagselLoadCorpus(t)
	cfg := config.Defaults()

	events, low := 0, 0
	for _, s := range sessions {
		g, err := dag.Open(t.TempDir(), cfg, logging.Nop())
		require.NoError(t, err)

		var prev dagselPrev
		fed := 0
		for _, at := range s.CompactionAt {
			end := int(at)
			if end > len(s.Turns) {
				end = len(s.Turns)
			}
			blocks := eval.Blocks(s, at)
			prev = dagselFeedToolCalls(t, g, s, fed, end, dagselToolResultPositions(blocks), prev)
			fed = end
			require.Positive(t, g.Stats().Edges, "session %s built an edgeless graph; the measurement would be vacuous", s.ID)

			ks, _, err := eval.BeladyDetail(ctx, s, at, eval.DefaultKeepBudget, eval.DefaultBeladyOptions())
			require.NoError(t, err)

			n := dagselPrefixTokens(blocks)
			require.Positive(t, n, "session %s at turn %d has an empty prefix", s.ID, int(at))

			crossingAtP := g.CrossingEdges(ks.P)
			mean := dagselSampleMeanCrossing(g, n)
			events++
			if float64(crossingAtP) < mean {
				low++
			}
		}
	}

	require.Positive(t, events, "the corpus contains no compaction events; nothing was measured")
	rate := float64(low) / float64(events)
	t.Logf("wave-1 baseline: CrossingEdges(p_min) beat the 32-sample mean in %d/%d compaction events (%.1f%%)",
		low, events, rate*100)
	require.GreaterOrEqual(t, rate, dagselLowCouplingFloor,
		"section 4.5 floor: Belady's p_min must land at below-mean coupling in at least 70%% of events; "+
			"report the measured rate, do not lower the floor")
}

// dagselIngest is what dagselIngestAuthVersions put into the store and the graph: the tool-use
// ids in ingest order and each id's content root.
type dagselIngest struct {
	ids   []core.ToolUseID
	roots map[core.ToolUseID]core.Hash
}

// dagselIngestAuthVersions ingests the four real fileread-auth fixtures through the real store
// (real chunker, canonicalizer, symbol extractor, token estimator and redactor behind
// store.Open's defaults) and records the same chain into the DAG via dag.BuildToolUse — the
// modest in-test population section 4.5 calls for, no daemon involved.
func dagselIngestAuthVersions(ctx context.Context, t *testing.T, p *testutil.Project,
	st store.Store, g dag.Graph,
) dagselIngest {
	t.Helper()
	pathKey := paths.Key(dagselAuthPath)
	out := dagselIngest{roots: make(map[core.ToolUseID]core.Hash)}
	var prev dagselPrev
	for i, name := range dagselAuthFixtures {
		raw := dagselReadFixture(t, name)
		res, err := st.PutBytes(ctx, raw, store.PutOptions{Tool: dagselAuthTool, Path: pathKey})
		require.NoError(t, err)

		id := core.ToolUseID("toolu_dagsel_auth_v" + strconv.Itoa(i+1))
		turn := core.TurnIndex(i + 1)
		ts := core.NowMilli(p.Clock)
		require.NoError(t, st.RecordToolUse(ctx, store.ToolUseRecord{
			ID:          id,
			Session:     dagselSessionID,
			Turn:        turn,
			TS:          ts,
			Tool:        dagselAuthTool,
			ArgsDigest:  core.HashBytes(core.DomainArgs, []byte(dagselAuthPath)),
			ArgsPreview: dagselAuthPath,
			Root:        res.Root.Hash,
			Path:        pathKey,
			Bytes:       res.Root.RawBytes,
			Tokens:      res.Root.Tokens,
			Signature:   res.Signature,
		}))
		require.NoError(t, dag.BuildToolUse(g, dag.ObservedTool{
			ToolUseID:     id,
			PrevToolUseID: prev.id,
			PrevTurn:      prev.turn,
			Turn:          turn,
			TS:            ts,
			Pos:           (i + 1) * dagselPosStep,
			Tool:          dagselAuthTool,
			PathKey:       pathKey,
			Root:          res.Root.Hash,
			Tokens:        res.Root.Tokens,
		}))

		prev = dagselPrev{id: id, turn: turn}
		out.ids = append(out.ids, id)
		out.roots[id] = res.Root.Hash
		p.Clock.Advance(time.Second)
	}
	return out
}

// TestIntegration_DAGSliceRanksStoreRootsWithoutDroppingAnything pins the dag.NodeID <->
// core.ToolUseID <-> paths.Key conventions across packages that cannot import each other: a
// BackwardSlice from the most recent tool-use node over a store-backed population must score
// every ingested tool use, every scored tool-use node must resolve through store.ToolUse to a
// real record, every score must lie in (0, 1], and the result is a score map — never a keep-set.
func TestIntegration_DAGSliceRanksStoreRootsWithoutDroppingAnything(t *testing.T) {
	ctx := context.Background()
	p := testutil.NewProject(t)
	st := p.Store(t)
	g, err := dag.Open(p.Root, p.Cfg, p.Log)
	require.NoError(t, err)

	ing := dagselIngestAuthVersions(ctx, t, p, st, g)
	newest := ing.ids[len(ing.ids)-1]

	// Options are spelled literally: thin slicing (the production default), the package's own
	// node cap and decay, and NO deadline — a wall-clock bound would make the result depend on
	// machine load, and this test asserts contents, not latency.
	sl, err := g.BackwardSlice([]dag.NodeID{dag.ToolUseNode(newest)}, dag.SliceOptions{
		Thin:     true,
		MaxNodes: dag.DefaultMaxNodes,
		Decay:    dag.DefaultDecay,
	})
	require.NoError(t, err)
	require.False(t, sl.Truncated, "an uncapped, undeadlined slice over four tool uses must complete")
	require.NotEmpty(t, sl.Scores)

	// The compile-time pin of section 4.5's "never a keep-set": the returned value is a score
	// per node, map[NodeID]float32, and this assignment fails to build if that type ever drifts.
	var scores map[dag.NodeID]float32 = sl.Scores

	toolUses := 0
	for id, score := range scores {
		require.Greater(t, score, float32(0), "score for %s must be positive", id)
		require.LessOrEqual(t, score, float32(1), "score for %s must not exceed 1", id)

		kind, key, ok := dag.ParseNodeID(id)
		require.True(t, ok, "scored node id %q does not parse under the D-2 scheme", id)
		if kind != dag.KindToolUse {
			continue
		}
		toolUses++
		rec, err := st.ToolUse(ctx, core.ToolUseID(key))
		require.NoError(t, err, "tool-use node %q does not resolve through store.ToolUse: dangling key", id)
		require.Equal(t, core.ToolUseID(key), rec.ID)
		require.Equal(t, ing.roots[rec.ID], rec.Root, "record %s carries a different root than was ingested", rec.ID)
		root, err := st.GetRoot(ctx, rec.Root)
		require.NoError(t, err, "record %s's root does not resolve in the object store", rec.ID)
		require.Equal(t, rec.Root, root.Hash)
		require.Equal(t, paths.Key(dagselAuthPath), rec.Path)
	}
	require.Equal(t, len(ing.ids), toolUses,
		"the backward slice must rank every ingested tool use and drop none")
}

// dagselSortedUnique returns names sorted ascending with duplicates removed — the order and
// multiplicity dag.BuildToolUse promises for symbol nodes and edges.
func dagselSortedUnique(names []string) []string {
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// dagselRequireDagNeverImportsSymbols is the import-graph seam of section 4.5's third test:
// internal/dag's non-test sources must never import internal/symbols — the CALLER resolves
// names (00-ARCHITECTURE.md §3.2). It parses dag's sources directly, the same technique as
// internal/eval's import-graph test, so it needs no toolchain subprocess; `go run ./tools/devtool
// lint` enforces the full allow-set on top.
func dagselRequireDagNeverImportsSymbols(t *testing.T) {
	t.Helper()
	dir := filepath.Join(dagselModuleRoot(t), "internal", "dag")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	fset := token.NewFileSet()
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ImportsOnly)
		require.NoError(t, err)
		checked++
		for _, spec := range f.Imports {
			imp, err := strconv.Unquote(spec.Path.Value)
			require.NoError(t, err)
			require.NotEqual(t, dagselModulePath+"/internal/symbols", imp,
				"internal/dag/%s imports internal/symbols; dag takes resolved names from the caller (§3.2)", name)
		}
	}
	require.Positive(t, checked, "no dag sources were parsed, so the import guard checked nothing")
}

// TestIntegration_SymbolNodesUseTheRealExtractor feeds the REAL extractor's names for a real
// TypeScript-flavoured fixture into dag.BuildToolUse and asserts the section 4.5 contract: one
// symbol node per extracted symbol keyed "<pathKey>#<name>" under the symbol prefix,
// deduplicated, shared-symbol edges appended in ascending name order, and store.Search resolving
// each name back to the same ingested root. dag itself never imports symbols — the caller
// resolved the names above — and the import-graph guard pins that.
func TestIntegration_SymbolNodesUseTheRealExtractor(t *testing.T) {
	ctx := context.Background()
	p := testutil.NewProject(t)
	st := p.Store(t)
	g, err := dag.Open(p.Root, p.Cfg, p.Log)
	require.NoError(t, err)

	raw := dagselReadFixture(t, dagselAuthFixtures[0])
	pathKey := paths.Key(dagselAuthPath)

	extracted := symbols.New().Extract(dagselAuthPath, raw)
	require.NotEmpty(t, extracted, "the fixture must contain definitions the real extractor handles")
	names := make([]string, 0, len(extracted)+1)
	for _, sym := range extracted {
		names = append(names, sym.Name)
	}
	// One deliberate duplicate, so the deduplication assertion below cannot pass vacuously.
	names = append(names, names[0])
	unique := dagselSortedUnique(names)

	res, err := st.PutBytes(ctx, raw, store.PutOptions{Tool: dagselAuthTool, Path: pathKey})
	require.NoError(t, err)
	id := core.ToolUseID("toolu_dagsel_symbols_v1")
	ts := core.NowMilli(p.Clock)
	require.NoError(t, st.RecordToolUse(ctx, store.ToolUseRecord{
		ID:          id,
		Session:     dagselSessionID,
		Turn:        1,
		TS:          ts,
		Tool:        dagselAuthTool,
		ArgsDigest:  core.HashBytes(core.DomainArgs, []byte(dagselAuthPath)),
		ArgsPreview: dagselAuthPath,
		Root:        res.Root.Hash,
		Path:        pathKey,
		Bytes:       res.Root.RawBytes,
		Tokens:      res.Root.Tokens,
		Signature:   res.Signature,
	}))
	require.NoError(t, dag.BuildToolUse(g, dag.ObservedTool{
		ToolUseID: id,
		Turn:      1,
		TS:        ts,
		Pos:       dagselPosStep,
		Tool:      dagselAuthTool,
		PathKey:   pathKey,
		Symbols:   names,
		Root:      res.Root.Hash,
		Tokens:    res.Root.Tokens,
	}))

	// One node per extracted symbol, deduplicated: the live symbol-node count equals the unique
	// name count even though the input carried a duplicate.
	require.Equal(t, len(unique), g.Stats().NodesByKind[dag.KindSymbol.String()],
		"expected exactly one symbol node per unique extracted name")
	for _, name := range unique {
		nodeID := dag.SymbolNode(pathKey, name)
		require.Equal(t, dag.NodeID(dag.KindSymbol.String()+":"+pathKey+"#"+name), nodeID,
			"the symbol node key grammar is <prefix>:<pathKey>#<name>")
		n, ok := g.Node(nodeID)
		require.True(t, ok, "missing symbol node for %q", name)
		require.Equal(t, dag.KindSymbol, n.Kind)
		require.Equal(t, name, n.Ref)
	}

	// A read CONSUMES its anchors (D-1), so every shared-symbol edge enters the tool use. The
	// graph returns incident edges in insertion order, which pins the builder's append order:
	// ascending name order, one edge per unique name.
	var edgeNames []string
	for _, e := range g.In(dag.ToolUseNode(id)) {
		if e.Kind != dag.EdgeSharedSymbol {
			continue
		}
		_, key, ok := dag.ParseNodeID(e.From)
		require.True(t, ok, "shared-symbol edge from unparseable node %q", e.From)
		name, found := strings.CutPrefix(key, pathKey+"#")
		require.True(t, found, "shared-symbol edge from %q is not keyed on this path", e.From)
		edgeNames = append(edgeNames, name)
	}
	require.True(t, sort.StringsAreSorted(edgeNames), "shared-symbol edges must be appended in ascending name order")
	require.Equal(t, unique, edgeNames,
		"one shared-symbol edge per unique extracted name, in ascending order")

	// The store's symbol search — running the SAME extractor over the stored content — resolves
	// every name back to the ingested root.
	for _, name := range unique {
		hits, err := st.Search(ctx, store.Query{Symbol: name, K: 1})
		require.NoError(t, err)
		require.NotEmpty(t, hits, "store.Search found nothing for symbol %q", name)
		require.Equal(t, res.Root.Hash, hits[0].Root, "symbol %q resolved to a different root", name)
	}

	dagselRequireDagNeverImportsSymbols(t)
}
