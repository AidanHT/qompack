// V5 §4.8 — SP-15's grammar and codec and SP-16's demand promotion coexist at the checkpoint seam
// under scope, overhead and provenance.
//
// The historical row imagined `checkpoint.Finalize` folding a grammar section into the narrative
// AND reordering pointers.tools from promotion evidence, and read both back out of one
// `checkpoint-now --json` file. Neither producer is wired into Finalize on this tree: SP-15's
// grammar reaches the writer only as SourceSet.Grammar (required non-nil, never read into the
// document), and SP-16's `checkpoint.Promote` is a pure function with no production call site
// (V5-SP-16-M6-evidence.md §5, "partially met"). This row therefore composes the seam the way the
// daemon does — one grammar instance shared by the real observer and the real SourceSet, the real
// mcp `expand` handler feeding the real promoter, the real demand log — and asserts what the real
// components produce, recording as absent what the historical text asserted as present.
//
// Level: in-process integration. `checkpoint-now` is not a subcommand (`qompack checkpoint` is the
// PreCompact hook) and nothing across a process boundary can observe promotion, so the e2e seam
// is unwritable. plans/sdd/V5-VERIFY/x08-disposition.md carries the old-to-new assertion map.
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/pins"
	"github.com/qompack/qompack/internal/redact"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/qompack/qompack/internal/tokens"
)

// x8v5Session is this row's session identity; x8v5OtherSession is a session that never expanded
// anything, for the scope assertion.
const (
	x8v5Session      = core.SessionID("sess-integration-v5-x08")
	x8v5OtherSession = core.SessionID("sess-integration-v5-x08-other")
)

// x8v5Cycles is the historical setup's "11 FileRead FileEdit Bash test:fail cycles".
const x8v5Cycles = 11

// x8v5SymbolsPerCycle is what one cycle appends to the action grammar through the real observer:
// the prompt symbol, FileRead, FileEdit, Bash and the test:fail verdict symbol.
const x8v5SymbolsPerCycle = 5

// x8v5Expansions is the historical setup's "expand one root 3 times" — above the configured
// promoteAfterExpansions threshold (2) and above checkpoint.Promote's own MinExpansions floor.
const x8v5Expansions = 3

// x8v5RecoveryCostMs is the recovery cost recorded on the useful demand observations; any measured
// positive value serves, the gate only orders on it.
const x8v5RecoveryCostMs = 250

// x8v5EventGap is the FakeClock advance between hook events.
const x8v5EventGap = 30 * time.Second

// x8v5PromotionKey is the migration-gate key checkpoint.Promote's Enabled is read from.
const x8v5PromotionKey = "runtime.phase7.retrieval.demandPromotion"

// x8v5PromotionDropKind is the drop-report kind promote.go leaves for a budget overflow.
const x8v5PromotionDropKind = "promotion_overflow"

// x8v5FailOutputFmt is a failing `go test` run in the shape observer.ExtractTestOutcome reads as
// TestFail — the same shape V3 X3 used. A per-cycle WORD goes into the failure message so every
// run's output has its own content root: eleven byte-identical outputs would share one hash, a
// promotion keyed on that hash would move all eleven pointers at once, and varying only the
// timings does not help because the store's canonicalizer strips them before hashing.
const x8v5FailOutputFmt = "--- FAIL: TestPool (0.03s)\n    pool_test.go:42: the pool still starves on %s\nFAIL\nFAIL\tqompack/internal/db\t0.412s\n"

// x8v5Words distinguishes the eleven cycles' outputs by a word rather than a number.
var x8v5Words = [x8v5Cycles]string{
	"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel", "india", "juliet", "kilo",
}

// x8v5Path is the project-relative file cycle i reads and edits.
func x8v5Path(i int) string { return fmt.Sprintf("src/pool_%02d.ts", i) }

// x8v5CodeLineRe is internal/checkpoint's nocode_test.go heuristic, re-stated here because that
// guard is unexported and this row holds the file it produced to the same rule.
var x8v5CodeLineRe = regexp.MustCompile(`(?m)^\s*(func |class |def |import |package |const |return |if \(|\}\s*$)`)

// x8v5Rig is the real collaborator set: the composition the daemon's scheduler wiring performs,
// with the ONE grammar instance handed to both the observer and the checkpoint SourceSet.
type x8v5Rig struct {
	P       *testutil.Project
	Store   store.Store
	Graph   dag.Graph
	Grammar grammar.Sequitur
	Obs     observer.Observer
	Writer  *checkpoint.FileWriter
	Src     checkpoint.SourceSet
	Server  mcp.Server
	Prom    mcp.Promoter
	Demand  *store.DemandLog
}

// x8v5Open composes the rig over one disposable project. Every collaborator is the production
// type; nothing stands in for a producer under test.
func x8v5Open(t *testing.T) *x8v5Rig {
	t.Helper()

	// The eleven files the cycles touch exist in the working tree, so their file pointers
	// validate as present rather than being dropped as pointer_missing.
	files := make(map[string]string, x8v5Cycles)
	for i := 0; i < x8v5Cycles; i++ {
		files[x8v5Path(i)] = fmt.Sprintf("pool.timeout = %d\n", 1000*(i+1))
	}
	p := testutil.NewProject(t, testutil.WithFiles(files))
	s := p.Store(t)

	g, err := dag.Open(p.Root, p.Cfg, p.Log)
	require.NoError(t, err)
	led, err := negknow.Open(p.Root, p.Cfg, nil, negknow.Deps{
		Store: s, Graph: g, Session: x8v5Session, Log: p.Log, Clock: p.Clock,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = led.Close() })

	pinStore, err := pins.OpenWith(p.Root, p.Log, nil, p.Clock)
	require.NoError(t, err)

	// One grammar for both consumers, exactly as internal/cli's scheduler wiring hands
	// opts.Grammar to the SourceSet after the observer was built over it.
	gram := grammar.New()

	obsv, err := observer.New(observer.Options{
		ProjectRoot: p.Root, Cfg: p.Cfg, Store: s, Graph: g, Grammar: gram,
		Log: p.Log, Clock: p.Clock,
	})
	require.NoError(t, err)

	w, err := checkpoint.OpenWriter(p.Root, p.Cfg, p.Log, nil, p.Clock)
	require.NoError(t, err)
	src := checkpoint.SourceSet{
		Store: s, Segments: s.Segments(), Ledger: led, Pins: pinStore, Graph: g,
		Grammar: gram,
		Tokens:  tokens.NewForProject(p.Cfg, tokens.DefaultCalibPath(), p.Root),
	}
	require.NoError(t, src.Validate(), "the SourceSet must be complete before Begin is called")
	require.NoError(t, w.SetSources(src))

	prom, err := mcp.NewPromoter(mcp.PromotionsPath(p.Root), p.Cfg.Retrieval.PromoteAfterExpansions, p.Clock)
	require.NoError(t, err)

	srv := mcp.NewServer(mcp.ServerName, "v5-x08", logging.Nop())
	require.NoError(t, mcp.RegisterAll(srv, mcp.ToolDeps{
		Store: s, Cfg: p.Cfg, ProjectRoot: p.Root, DisableWhy: true,
		Promoter: prom,
		// mcp fails CLOSED without a retrieval-side redactor; supplying it is the composition
		// root's job, as x9v4Open does.
		Redactor: x9v4Redactor{r: redact.New(p.Cfg)},
	}))

	return &x8v5Rig{
		P: p, Store: s, Graph: g, Grammar: gram, Obs: obsv, Writer: w, Src: src,
		Server: srv, Prom: prom, Demand: store.OpenDemandLog(p.Root),
	}
}

// event builds one hook event the way Claude Code spells it.
func (r *x8v5Rig) event(t *testing.T, hook string, id core.ToolUseID, tool string, input, response map[string]any) observer.Event {
	t.Helper()
	e := observer.Event{HookEventName: hook, SessionID: x8v5Session, CWD: r.P.Root, ToolName: tool, ToolUseID: id}
	if input != nil {
		in, err := json.Marshal(input)
		require.NoError(t, err)
		e.ToolInput = json.RawMessage(in)
	}
	if response != nil {
		resp, err := json.Marshal(response)
		require.NoError(t, err)
		e.ToolResponse = json.RawMessage(resp)
	}
	return e
}

// tool drives one PostToolUse event through the real observer.
func (r *x8v5Rig) tool(t *testing.T, id core.ToolUseID, tool string, input, response map[string]any) {
	t.Helper()
	r.P.Clock.Advance(x8v5EventGap)
	_, err := r.Obs.OnToolUse(context.Background(), r.event(t, "PostToolUse", id, tool, input, response))
	require.NoError(t, err, "PostToolUse %s (%s)", id, tool)
}

// prompt closes the current user turn.
func (r *x8v5Rig) prompt(t *testing.T, text string) {
	t.Helper()
	r.P.Clock.Advance(x8v5EventGap)
	e := observer.Event{HookEventName: "UserPromptSubmit", SessionID: x8v5Session, CWD: r.P.Root, Prompt: text}
	_, err := r.Obs.OnUserPrompt(context.Background(), e)
	require.NoError(t, err)
}

// driveCycles runs the historical setup: a SessionStart, then x8v5Cycles of
// `FileRead FileEdit Bash(test:fail)` through the real observer, then a SessionEnd so the segment
// the observer opened is closed the way the daemon closes it. It returns the tool-use ids in
// the order they were observed.
func (r *x8v5Rig) driveCycles(t *testing.T) []core.ToolUseID {
	t.Helper()
	ctx := context.Background()

	start := observer.Event{HookEventName: "SessionStart", SessionID: x8v5Session, CWD: r.P.Root, Source: "startup"}
	_, err := r.Obs.OnSessionStart(ctx, start)
	require.NoError(t, err)

	var ids []core.ToolUseID
	for i := 0; i < x8v5Cycles; i++ {
		// A distinct path per cycle keeps the store's supersession class from collapsing the
		// eleven reads and edits into one surviving record; the grammar sees only tool names.
		path := x8v5Path(i)
		r.prompt(t, fmt.Sprintf("cycle %d: widen the pool timeout and rerun the db tests", i))

		read := core.ToolUseID(fmt.Sprintf("toolu_v5x08_%02d_read", i))
		r.tool(t, read, "Read",
			map[string]any{"file_path": path},
			map[string]any{"content": fmt.Sprintf("pool.timeout = %d\n", 1000*(i+1))})

		// The edit's result has a different shape from any read's, so no two of the 33 results
		// share a content root: a shared root would be one hash behind two pointers.
		edit := core.ToolUseID(fmt.Sprintf("toolu_v5x08_%02d_edit", i))
		r.tool(t, edit, "Edit",
			map[string]any{"file_path": path, "new_string": fmt.Sprintf("pool.timeout = %d", 2000*(i+1))},
			map[string]any{"content": fmt.Sprintf("pool.timeout = %d\npool.retries = 3\n", 2000*(i+1))})

		test := core.ToolUseID(fmt.Sprintf("toolu_v5x08_%02d_test", i))
		testEvent := r.event(t, "PostToolUse", test, "Bash",
			map[string]any{"command": "go test ./internal/db/..."},
			map[string]any{"exit_code": 1, "stdout": fmt.Sprintf(x8v5FailOutputFmt, x8v5Words[i])})
		require.Equal(t, observer.TestFail, observer.ExtractTestOutcome(testEvent),
			"the observer must read the failing run as TestFail, or no test:fail symbol reaches the grammar")
		r.P.Clock.Advance(x8v5EventGap)
		_, err = r.Obs.OnToolUse(ctx, testEvent)
		require.NoError(t, err)

		ids = append(ids, read, edit, test)
	}

	r.P.Clock.Advance(x8v5EventGap)
	end := observer.Event{HookEventName: "SessionEnd", SessionID: x8v5Session, CWD: r.P.Root}
	_, err = r.Obs.OnSessionEnd(ctx, end)
	require.NoError(t, err)
	return ids
}

// finalize seals one real checkpoint over every closed segment of the session and returns the
// on-disk document, its raw bytes and its Ref.
func (r *x8v5Rig) finalize(t *testing.T) (checkpoint.Checkpoint, []byte, checkpoint.Ref) {
	t.Helper()
	ctx := context.Background()

	segs, err := r.Store.Segments().Unencoded(ctx, x8v5Session)
	require.NoError(t, err)
	var ids []core.SegmentID
	for _, seg := range segs {
		require.True(t, seg.Closed, "SessionEnd must have closed segment %d before it can be encoded", int(seg.ID))
		ids = append(ids, seg.ID)
	}
	require.NotEmpty(t, ids, "the observer must have opened at least one segment for the session")

	d, err := r.Writer.Begin(ctx, x8v5Session, 0, r.Src)
	require.NoError(t, err)
	_, err = r.Writer.Advance(ctx, d, ids)
	require.NoError(t, err)
	ref, err := r.Writer.Finalize(ctx, d, core.Tokens(r.P.Cfg.Checkpoint.BudgetTokens))
	require.NoError(t, err)

	raw, err := os.ReadFile(paths.Long(ref.Path))
	require.NoError(t, err)
	cp, err := checkpoint.Unmarshal(raw)
	require.NoError(t, err)
	return cp, raw, ref
}

// expand drives the real `expand` handler for id and returns the promoter's running count and
// promoted flag as the handler published them in _meta.qompack.
func (r *x8v5Rig) expand(t *testing.T, id core.ToolUseID) (count int, promoted bool) {
	t.Helper()
	args, err := json.Marshal(map[string]any{"tool_use_id": string(id)})
	require.NoError(t, err)
	for _, tool := range r.Server.Tools() {
		if tool.Name != mcp.ToolExpand {
			continue
		}
		resp, hErr := tool.Handler(context.Background(), mcp.Request{
			Session: x8v5Session, Name: mcp.ToolExpand, Args: args,
		})
		require.NoError(t, hErr, "expand must not raise a protocol error")
		require.False(t, resp.IsError, "expand(%s) must resolve: %+v", id, resp.Content)
		n, ok := resp.Meta["expansions"].(int)
		require.True(t, ok, "expand must publish _meta.qompack.expansions as an int, got %T", resp.Meta["expansions"])
		p, ok := resp.Meta["promoted"].(bool)
		require.True(t, ok, "expand must publish _meta.qompack.promoted as a bool")
		return n, p
	}
	require.FailNow(t, "the registered tool set must include expand")
	return 0, false
}

// recordDemand appends one observation to the real demand log at the clock's current time.
func (r *x8v5Rig) recordDemand(t *testing.T, kind store.DemandKind, key, touch string, recoveryMs int64) {
	t.Helper()
	r.P.Clock.Advance(time.Second)
	require.NoError(t, r.Demand.Record(store.DemandObservation{
		Kind: kind, Key: key, TS: core.NowMilli(r.P.Clock), Session: x8v5Session,
		Touch: touch, RecoveryCostMs: recoveryMs,
	}))
}

// demandFor aggregates the real demand log and returns the record for key.
func (r *x8v5Rig) demandFor(t *testing.T, key string) store.Demand {
	t.Helper()
	agg, err := r.Demand.Aggregate()
	require.NoError(t, err)
	d, ok := agg[key]
	require.True(t, ok, "the demand log must carry key %s", key)
	return d
}

// x8v5Size prices a checkpoint the way Finalize does.
func x8v5Size(t *testing.T, c checkpoint.Checkpoint, est tokens.Estimator) core.Tokens {
	t.Helper()
	b, err := checkpoint.Marshal(c)
	require.NoError(t, err)
	return est.Estimate(b, tokens.ClassJSON)
}

// x8v5ToolHashes lists a checkpoint's tool pointer hashes in document order.
func x8v5ToolHashes(c checkpoint.Checkpoint) []string {
	out := make([]string, 0, len(c.Pointers.Tools))
	for _, p := range c.Pointers.Tools {
		out = append(out, p.Hash.String())
	}
	return out
}

// x8v5HasToolHash reports whether c still points at h.
func x8v5HasToolHash(c checkpoint.Checkpoint, h core.Hash) bool {
	for _, p := range c.Pointers.Tools {
		if p.Hash == h {
			return true
		}
	}
	return false
}

// x8v5TopLevelKeys returns the sorted top-level key set of a JSON object.
func x8v5TopLevelKeys(t *testing.T, raw []byte) []string {
	t.Helper()
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &m))
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// x8v5WalkStrings visits every string value in a decoded JSON document.
func x8v5WalkStrings(path string, v any, visit func(path, s string)) {
	switch x := v.(type) {
	case string:
		visit(path, x)
	case []any:
		for i, e := range x {
			x8v5WalkStrings(fmt.Sprintf("%s[%d]", path, i), e, visit)
		}
	case map[string]any:
		for k, e := range x {
			x8v5WalkStrings(path+"."+k, e, visit)
		}
	}
}

// x8v5RequireNoCode holds a produced checkpoint to internal/checkpoint's
// TestGoldenCheckpointsContainNoCodeBlocks rule, all three layers.
func x8v5RequireNoCode(t *testing.T, raw []byte, c checkpoint.Checkpoint) {
	t.Helper()
	require.NotContains(t, string(raw), "```", "§13 invariant 5: no fenced code block in a checkpoint")
	var doc any
	require.NoError(t, json.Unmarshal(raw, &doc))
	x8v5WalkStrings("$", doc, func(path, s string) {
		require.NotContains(t, s, "```", "%s: fenced code block hidden behind JSON escaping", path)
		require.NotRegexp(t, x8v5CodeLineRe, s, "%s: string value reads like source code", path)
	})
	for i, f := range c.Pointers.Files {
		require.NotContains(t, f.Why, "\n", "pointers.files[%d].why must be one line", i)
	}
	for i, tp := range c.Pointers.Tools {
		require.NotContains(t, tp.Summary, "\n", "pointers.tools[%d].summary must be one line", i)
	}
}

// x8v5Symbols joins a symbol sequence for substring assertions.
func x8v5Symbols(s []grammar.Symbol) string {
	parts := make([]string, len(s))
	for i, sym := range s {
		parts[i] = string(sym)
	}
	return strings.Join(parts, " ")
}

// TestV5_GrammarAndPromotionCoexistInFinalize is V5-VERIFY §4.8.
//
// Criterion: "SP-15/SP-16 codec/selection/promotion coexist under scope, overhead and provenance."
func TestV5_GrammarAndPromotionCoexistInFinalize(t *testing.T) {
	r := x8v5Open(t)
	ctx := context.Background()
	ids := r.driveCycles(t)
	require.Len(t, ids, x8v5Cycles*3)

	// ── SP-15: the real grammar induced structure from the observer's symbol stream ────────────
	t.Run("GrammarInducesRulesFromTheObservedCycles", func(t *testing.T) {
		appended := x8v5Cycles * x8v5SymbolsPerCycle
		compressed := r.Grammar.Compressed()
		require.NotEmpty(t, compressed)
		require.Less(t, len(compressed), appended,
			"the top-level sequence must be shorter than the %d symbols the observer appended; got %d",
			appended, len(compressed))

		rules := r.Grammar.Rules()
		require.NotEmpty(t, rules, "eleven identical cycles must induce at least one rule")
		var cycleRule bool
		for _, rule := range rules {
			exp := x8v5Symbols(rule.Expansion)
			if strings.Contains(exp, "FileRead") && strings.Contains(exp, "FileEdit") &&
				strings.Contains(exp, "Bash") && strings.Contains(exp, "test:fail") {
				cycleRule = true
			}
		}
		require.True(t, cycleRule,
			"some rule must expand to the FileRead FileEdit Bash test:fail cycle the observer fed; rules=%+v", rules)
		require.NotEmpty(t, r.Grammar.Thrash(2),
			"a cycle repeated eleven times must be reported by Thrash(2)")
	})

	// ── SP-15: the codec round-trips the live grammar and refuses a truncated frame ────────────
	t.Run("CodecRoundTripsTheLiveGrammar", func(t *testing.T) {
		frame, err := r.Grammar.MarshalBinary()
		require.NoError(t, err)
		require.NotEmpty(t, frame)

		restored := grammar.New()
		require.NoError(t, restored.UnmarshalBinary(frame))
		require.Equal(t, r.Grammar.Compressed(), restored.Compressed(),
			"the restored grammar must reproduce the live top-level sequence")
		require.Equal(t, r.Grammar.Rules(), restored.Rules(),
			"the restored grammar must reproduce every rule, id for id")

		// NEGATIVE CONTROL for the codec: half a frame is not a grammar.
		_, err = grammar.DecodeSnapshot(frame[:len(frame)/2])
		require.Error(t, err, "a truncated frame must be refused, not decoded as a smaller grammar")
		require.Error(t, grammar.New().UnmarshalBinary(frame[:len(frame)/2]))
	})

	// ── The real Finalize over the real SourceSet that carries that grammar ────────────────────
	cp, raw, ref := r.finalize(t)
	est := r.Src.Tokens
	tiers := r.P.Cfg.Checkpoint.Tiers

	t.Run("FinalizeWritesTheV1ShapeWithNoGrammarSection", func(t *testing.T) {
		require.Equal(t, checkpoint.SchemaVersion, cp.Version)
		require.Equal(t, 1, cp.Version, `"version": 1 is unchanged`)
		require.Equal(t, x8v5Session, cp.Session)

		golden, err := os.ReadFile(filepath.Join(dagselModuleRoot(t), "testdata", "golden", "checkpoints", "0001-minimal.json"))
		require.NoError(t, err)
		require.Equal(t, x8v5TopLevelKeys(t, golden), x8v5TopLevelKeys(t, raw),
			"the v1 key set must be identical to the golden's")

		require.Len(t, cp.Pointers.Tools, len(ids),
			"every observed tool use must have become a tier-3 pointer that survived Finalize's store check")
		seen := map[core.Hash]core.ToolUseID{}
		for _, p := range cp.Pointers.Tools {
			require.NotContains(t, seen, p.Hash, "pointer %s shares its root with %s; the fixture must keep roots distinct", p.ToolUseID, seen[p.Hash])
			seen[p.Hash] = p.ToolUseID
		}
		require.Len(t, cp.Pointers.Files, x8v5Cycles, "the eleven files on disk must have become file pointers")
		require.Equal(t, core.TurnIndex(x8v5Cycles), ref.Frontier, "the frontier is the last user turn the segment closed at")
		require.NotEmpty(t, cp.EncodedSegments)
		require.Contains(t, cp.Narrative, "seg ", "the writer's own per-segment narrative line must be present")

		// The historical expectation, and what is actually true on this tree: the grammar in
		// SourceSet.Grammar is never read into the document. Asserted as ABSENT so that the day a
		// fold is wired, this row fails and its disposition is rewritten rather than silently
		// passing on an expectation nobody re-examined.
		require.NotContains(t, cp.Narrative, "grammar-compressed",
			"no grammar fold is wired into Finalize on this tree; if one now is, update x08-disposition.md")
		require.Equal(t, map[string]string{"tried": "tried.bloom", "touch": "touch.cms"}, cp.SketchRefs,
			"sketch_refs carries exactly the two default keys; no grammar/actions.seq is written")
		require.NoFileExists(t, paths.Long(filepath.Join(paths.Of(r.P.Root).Grammar, "actions.seq")))

		x8v5RequireNoCode(t, raw, cp)
	})

	// ── SP-16: real expansions through the real mcp handler, counted by the real promoter ─────
	// The tail pointer is the one Truncate cuts first, so it is the one promotion has to save.
	tail := cp.Pointers.Tools[len(cp.Pointers.Tools)-1]
	second := cp.Pointers.Tools[len(cp.Pointers.Tools)-2]
	for i := 1; i <= x8v5Expansions; i++ {
		count, promoted := r.expand(t, tail.ToolUseID)
		require.Equal(t, i, count, "expand must report the running count")
		require.Equal(t, i >= r.P.Cfg.Retrieval.PromoteAfterExpansions, promoted,
			"promoted must flip exactly at retrieval.promoteAfterExpansions=%d", r.P.Cfg.Retrieval.PromoteAfterExpansions)
	}
	for i := 1; i <= x8v5Expansions; i++ {
		r.expand(t, second.ToolUseID)
	}
	promotedHashes, err := r.Prom.Promoted(ctx, x8v5Session)
	require.NoError(t, err)
	require.Equal(t, []core.Hash{tail.Hash, second.Hash}, promotedHashes,
		"the promoter must list both re-expanded roots in promotion order")

	// Provenance: the tail's outcomes were instrumented (every request has an outcome, one
	// recovery cost measured); the second pointer's were NOT — requests only, which is
	// frequency and nothing else.
	for i := 0; i < x8v5Expansions; i++ {
		r.recordDemand(t, store.DemandRequested, tail.Hash.String(), "", 0)
		r.recordDemand(t, store.DemandUseful, tail.Hash.String(), "", x8v5RecoveryCostMs)
		r.recordDemand(t, store.DemandRequested, second.Hash.String(), "", 0)
	}
	tailDemand := r.demandFor(t, tail.Hash.String())
	secondDemand := r.demandFor(t, second.Hash.String())
	require.True(t, tailDemand.Instrumented())
	_, known := tailDemand.Usefulness()
	require.True(t, known)
	_, known = secondDemand.Usefulness()
	require.False(t, known, "requests alone must leave usefulness unknown")

	candidates := []checkpoint.PromotionCandidate{
		{Pointer: tail, Expansions: x8v5Expansions, Demand: tailDemand, Authority: core.AuthorityToolObservation},
		{Pointer: second, Expansions: x8v5Expansions, Demand: secondDemand, Authority: core.AuthorityToolObservation},
	}
	request := func(enabled bool, overhead core.Tokens) checkpoint.PromotionRequest {
		return checkpoint.PromotionRequest{
			Enabled: enabled, ObservedSeq: ref.Seq, TargetSeq: ref.Seq + 1,
			Candidates: candidates, Overhead: overhead, Est: est,
		}
	}
	const roomForEverything = core.Tokens(1_000_000)

	t.Run("DisabledPromotionIsRecordedAsDisabledNeverAsApplied", func(t *testing.T) {
		// The switch as the real loader resolved it for this project: false, because the
		// M6-G16-C gate is pending in every build and Validate refuses a true value.
		require.False(t, r.P.Cfg.Runtime.Phase7.Retrieval.DemandPromotion)
		var gate config.MigrationGate
		for _, g := range config.MigrationGates() {
			if g.Key == x8v5PromotionKey {
				gate = g
			}
		}
		require.Equal(t, x8v5PromotionKey, gate.Key, "the promotion switch must be in the migration-gate table")
		require.False(t, gate.Passed, "M6-G16-C is partially met; the gate must still read pending")

		on := config.Defaults()
		on.Runtime.Phase7.Retrieval.DemandPromotion = true
		var refused bool
		for _, v := range on.Validate() {
			if v.Key == x8v5PromotionKey {
				refused = true
				require.Contains(t, v.Message, "M6-G16-C")
			}
		}
		require.True(t, refused, "Validate must refuse demandPromotion=true while its gate is pending")

		// And through the real config file layer: a project that asks for it does not get it.
		asked := testutil.NewProject(t, testutil.WithConfig(
			`{"runtime":{"phase7":{"settingsVersion":1,"retrieval":{"demandPromotion":true}}}}`))
		require.False(t, asked.Cfg.Runtime.Phase7.Retrieval.DemandPromotion,
			"the loader must fall back to false for a gated switch no build can honour")

		plan := checkpoint.Promote(request(r.P.Cfg.Runtime.Phase7.Retrieval.DemandPromotion, roomForEverything))
		require.False(t, plan.Enabled)
		require.Equal(t, 2, plan.Considered)
		require.Equal(t, 1, plan.Admitted(), "report-only mode still evaluates: the instrumented candidate passes")
		require.Empty(t, plan.Order, "a disabled pass applies nothing")
		var disabledNoted bool
		for _, o := range plan.Omissions {
			if strings.Contains(o.Reason, "disabled") {
				disabledNoted = true
				require.Contains(t, o.Recovery, x8v5PromotionKey)
			}
		}
		require.True(t, disabledNoted, "the plan must say it was disabled, not merely apply nothing: %+v", plan.Omissions)

		applied, drops := plan.Apply(cp)
		require.Empty(t, drops)
		require.Equal(t, x8v5ToolHashes(cp), x8v5ToolHashes(applied), "disabled promotion reorders nothing")
		got, err := checkpoint.Marshal(applied)
		require.NoError(t, err)
		want, err := checkpoint.Marshal(cp)
		require.NoError(t, err)
		require.Equal(t, string(want), string(got), "the archived document is byte-for-byte what a disabled pass yields")
	})

	t.Run("EnabledPromotionReordersOnlyTheNextCheckpointAndSurvivesTruncation", func(t *testing.T) {
		// The largest budget that actually cuts a tool pointer, found by stepping down from the
		// document's own size so the test keeps cutting if the estimator's constants move.
		var budget core.Tokens
		var before checkpoint.Checkpoint
		for b := x8v5Size(t, cp, est); b > 0; b-- {
			before, _ = checkpoint.Truncate(cp, b, tiers, est)
			if len(before.Pointers.Tools) < len(cp.Pointers.Tools) {
				budget = b
				break
			}
		}
		require.Positive(t, budget, "no budget cut a tool pointer, so promotion would have nothing to save")
		require.False(t, x8v5HasToolHash(before, tail.Hash), "without promotion the tail pointer is the one cut")

		// The enabled evaluation. No build can turn the switch on, so this is the composition
		// root's call with the gate's future value: it is what the row can prove about the
		// mechanism, and it is labelled as such in the disposition.
		plan := checkpoint.Promote(request(true, roomForEverything))
		require.True(t, plan.Enabled)
		require.Equal(t, 1, plan.Admitted())
		require.Equal(t, []core.Hash{tail.Hash}, plan.Order, "only the instrumented, useful candidate is promoted")
		require.Empty(t, plan.Dropped)

		// Provenance, in the same plan: the uninstrumented candidate is withheld with the
		// frequency-is-not-usefulness reason, never refused and never admitted.
		require.Equal(t, checkpoint.PromotionAdmitted, plan.Results[0].Decision)
		require.Equal(t, checkpoint.PromotionWithheld, plan.Results[1].Decision)
		require.Contains(t, plan.Results[1].Why.Reason, "frequency")

		promoted, drops := plan.Apply(cp)
		require.Empty(t, drops)
		require.Equal(t, tail.Hash, promoted.Pointers.Tools[0].Hash, "the promoted pointer leads")
		require.ElementsMatch(t, x8v5ToolHashes(cp), x8v5ToolHashes(promoted), "promotion adds and removes nothing")
		require.Equal(t, tail.Hash, cp.Pointers.Tools[len(cp.Pointers.Tools)-1].Hash,
			"Apply must not have reordered the archived document in place")

		after, _ := checkpoint.Truncate(promoted, budget, tiers, est)
		require.True(t, x8v5HasToolHash(after, tail.Hash), "the promoted pointer must survive the budget that cut it")
		require.LessOrEqual(t, x8v5Size(t, after, est), budget, "promotion must not exceed the budget")

		// Coexistence: the promoted document is still the v1 shape, still code-free.
		out, err := checkpoint.Marshal(after)
		require.NoError(t, err)
		require.Equal(t, x8v5TopLevelKeys(t, raw), x8v5TopLevelKeys(t, out))
		require.Equal(t, 1, after.Version)
		x8v5RequireNoCode(t, out, after)
	})

	t.Run("OverheadBudgetOverflowsWithADropEntry", func(t *testing.T) {
		priced := checkpoint.Promote(request(true, roomForEverything))
		cost := priced.Results[0].Cost
		require.Positive(t, cost, "the real estimator must price the promoted pointer above zero")

		plan := checkpoint.Promote(request(true, cost-1))
		require.Equal(t, checkpoint.PromotionOverflowed, plan.Results[0].Decision)
		require.Empty(t, plan.Order)
		require.Zero(t, plan.Spent)
		require.Len(t, plan.Dropped, 1, "an overflow is never a silent loss")
		require.Equal(t, x8v5PromotionDropKind, plan.Dropped[0].Kind)
		require.Equal(t, string(tail.ToolUseID), plan.Dropped[0].ID)

		applied, drops := plan.Apply(cp)
		require.Equal(t, plan.Dropped, drops, "Apply must hand the overflow trace back even though nothing moved")
		require.Equal(t, x8v5ToolHashes(cp), x8v5ToolHashes(applied))

		zero := checkpoint.Promote(request(true, 0))
		require.Empty(t, zero.Order, "a zero overhead promotes nothing rather than everything")
	})

	t.Run("ProvenanceAndScopeGates", func(t *testing.T) {
		// Same epoch: evidence gathered while serving this checkpoint may not change it.
		same := request(true, roomForEverything)
		same.TargetSeq = ref.Seq
		plan := checkpoint.Promote(same)
		require.Empty(t, plan.Order)
		for _, res := range plan.Results {
			require.Equal(t, checkpoint.PromotionRefused, res.Decision)
			require.Contains(t, res.Why.Reason, "same-epoch")
		}

		// An authority the vocabulary does not recognise is refused on evidence.
		unattributed := request(true, roomForEverything)
		unattributed.Candidates = []checkpoint.PromotionCandidate{{
			Pointer: tail, Expansions: x8v5Expansions, Demand: tailDemand, Authority: core.Authority("folklore"),
		}}
		plan = checkpoint.Promote(unattributed)
		require.Equal(t, checkpoint.PromotionRefused, plan.Results[0].Decision)
		require.Contains(t, plan.Results[0].Why.Reason, "authority")

		// Scope: the promoter counts per session. Another session's view of the same project has
		// no promotions, so nothing this session re-expanded is evidence for it.
		other, err := r.Prom.Promoted(ctx, x8v5OtherSession)
		require.NoError(t, err)
		require.Empty(t, other, "expansions in one session must not promote in another")
	})

	// ── NEGATIVE CONTROL: sever the promotion producer through its real corrupt-file path ─────
	t.Run("NegativeControl_CorruptPromotionsFileYieldsNoCandidates", func(t *testing.T) {
		path := mcp.PromotionsPath(r.P.Root)
		require.FileExists(t, paths.Long(path), "the real promoter must have persisted its counts")
		require.NoError(t, paths.WriteAtomic(path, []byte("{not json"), 0o600))

		severed, err := mcp.NewPromoter(path, r.P.Cfg.Retrieval.PromoteAfterExpansions, r.P.Clock)
		require.NoError(t, err, "a corrupt state file is quarantined, never a refusal to run")
		got, err := severed.Promoted(ctx, x8v5Session)
		require.NoError(t, err)
		require.Empty(t, got, "with its state quarantined the promoter must offer nothing")

		// The same composition step the enabled arm performed now finds no candidate at all.
		var none []checkpoint.PromotionCandidate
		for _, p := range cp.Pointers.Tools {
			for _, h := range got {
				if p.Hash == h {
					none = append(none, checkpoint.PromotionCandidate{Pointer: p})
				}
			}
		}
		plan := checkpoint.Promote(checkpoint.PromotionRequest{
			Enabled: true, ObservedSeq: ref.Seq, TargetSeq: ref.Seq + 1,
			Candidates: none, Overhead: roomForEverything, Est: est,
		})
		require.Zero(t, plan.Considered)
		require.Empty(t, plan.Order)
		applied, _ := plan.Apply(cp)
		require.Equal(t, x8v5ToolHashes(cp), x8v5ToolHashes(applied),
			"NEGATIVE CONTROL: with the producer severed the tail pointer stays at the tail; if it moved, "+
				"the enabled arm's reorder assertion says nothing about the promoter")
		require.Equal(t, tail.Hash, applied.Pointers.Tools[len(applied.Pointers.Tools)-1].Hash)

		entries, err := os.ReadDir(paths.Long(filepath.Join(paths.Of(r.P.Root).Tmp, "quarantine")))
		require.NoError(t, err)
		require.NotEmpty(t, entries, "the corrupt bytes must have been kept as evidence")
	})

	// ── NEGATIVE CONTROL for the grammar: a grammar that was fed nothing has no structure ────
	t.Run("NegativeControl_AnUnfedGrammarInducesNothing", func(t *testing.T) {
		fresh := grammar.New()
		require.Empty(t, fresh.Compressed())
		require.Empty(t, fresh.Rules())
		require.Empty(t, fresh.Thrash(2))
	})

	// The seam's durable output is append-only: testutil.AssertAppendOnly's checkpoint probes, run
	// against the artifact THIS row sealed rather than the seed that helper writes for itself
	// (it requires 0001.json not to exist yet, which a finalized session cannot offer).
	t.Run("SealedCheckpointIsAppendOnly", func(t *testing.T) {
		f, err := paths.OpenFile(ref.Path, os.O_WRONLY|os.O_TRUNC, 0o600)
		if f != nil {
			_ = f.Close()
		}
		require.ErrorIs(t, err, core.ErrAppendOnly, "a truncating write to the sealed checkpoint must be refused")
		require.ErrorIs(t, paths.CreateNew(ref.Path, []byte("{}\n")), os.ErrExist,
			"the same checkpoint sequence written twice must be refused")
		again, err := os.ReadFile(paths.Long(ref.Path))
		require.NoError(t, err)
		require.Equal(t, string(raw), string(again), "the sealed bytes are the sealed bytes")
	})
}
