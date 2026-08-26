package observer

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

// thrashRule is one Sequitur rule above the multiplicity threshold.
func thrashRule(id grammar.RuleID, uses int, expansion ...grammar.Symbol) grammar.Rule {
	return grammar.Rule{ID: id, Uses: uses, Expansion: expansion, Span: len(expansion)}
}

func TestCollectThrash_QueuesEachRuleExactlyOnce(t *testing.T) {
	g := &fakeGrammar{Thrashing: []grammar.Rule{
		thrashRule(1, 11, "FileRead", "FileEdit", "Bash"),
	}}
	h := newHarness(t, func(o *Options) { o.Grammar = g })

	h.drive(
		readOf("toolu_1", "src/a.ts", "alpha\n"),
		readOf("toolu_2", "src/a.ts", "alpha\n"),
		readOf("toolu_3", "src/a.ts", "alpha\n"),
	)

	st := h.state(testSession)
	st.mu.Lock()
	defer st.mu.Unlock()
	require.Len(t, st.PendingThrash, 1,
		"Sequitur reports a rule for as long as it stays above the threshold; the warning is per rule")
	require.True(t, st.WarnedRules[1])
}

func TestCollectThrash_QueuesANewlyThrashingRule(t *testing.T) {
	g := &fakeGrammar{}
	h := newHarness(t, func(o *Options) { o.Grammar = g })

	h.drive(readOf("toolu_1", "src/a.ts", "alpha\n"))
	g.mu.Lock()
	g.Thrashing = []grammar.Rule{thrashRule(1, 4, "FileRead", "Bash"), thrashRule(2, 9, "Grep", "FileEdit")}
	g.mu.Unlock()
	h.drive(readOf("toolu_2", "src/a.ts", "alpha\n"))

	st := h.state(testSession)
	st.mu.Lock()
	defer st.mu.Unlock()
	require.Len(t, st.PendingThrash, 2)
}

func TestCollectThrash_AppendsTheToolSymbolAndTheTestVerdict(t *testing.T) {
	g := &fakeGrammar{}
	h := newHarness(t, func(o *Options) { o.Grammar = g })

	h.drive(
		readOf("toolu_1", "src/a.ts", "alpha\n"),
		bashOf("toolu_2", "go test ./...", "ok  \tgithub.com/example/pkg\t0.42s\n"),
		bashOf("toolu_3", "go test ./...", "FAIL\tgithub.com/example/pkg\t0.42s\n"),
	)

	require.Equal(t, []grammar.Symbol{
		"FileRead",
		"Bash", grammarTestPass,
		"Bash", grammarTestFail,
	}, g.appended(), "the action stream carries the tool symbol and, when there is one, the verdict")
}

func TestPendingThrashLines_DrainsAndFormatsThroughGrammar(t *testing.T) {
	rule := thrashRule(1, 11, "FileRead", "FileEdit", "Bash")
	h := newHarness(t, func(o *Options) { o.Grammar = &fakeGrammar{Thrashing: []grammar.Rule{rule}} })

	h.drive(readOf("toolu_1", "src/a.ts", "alpha\n"))
	st := h.state(testSession)
	st.mu.Lock()
	st.Turn = 42
	lines := h.obs.pendingThrashLines(st)
	drained := h.obs.pendingThrashLines(st)
	st.mu.Unlock()

	require.Equal(t, []string{grammar.FormatWarning(grammar.Warning{
		Rule: rule, Repeats: 11, Message: thrashAdvice, Turns: []core.TurnIndex{42},
	})}, lines)
	require.Nil(t, drained, "the queue is drained, so one loop is reported once")
}

func TestCollectThrash_NilGrammarIsTolerated(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.Grammar = nil })

	require.NotPanics(t, func() {
		h.drive(readOf("toolu_1", "src/a.ts", "alpha\n"))
		st := h.state(testSession)
		st.mu.Lock()
		defer st.mu.Unlock()
		h.obs.collectThrash(st)
		require.Empty(t, st.PendingThrash)
	})
}

// ── §8.1 item 7 / G2.3: verbatim UserPromptSubmit capture ──

// verbatimPrompt carries one of each of the three volatile shapes the six configured strip classes
// rewrite in a tool result — a curly apostrophe, an ISO-8601 timestamp and a PID — so that reading
// it back byte for byte is a statement about the OPT-OUT rather than about a prompt that nothing
// would have touched anyway.
const verbatimPrompt = "The runner’s log says the pool reset at " +
	"2026-08-24T12:34:56Z and PID 4711 never exited."

// samplePrompt is the plain prompt the double-backed rows submit.
const samplePrompt = "fix the pgbouncer 1.18 pool bypass"

// promptOf builds a UserPromptSubmit payload carrying text.
func promptOf(text string) Event {
	return Event{HookEventName: userPromptSubmit, SessionID: testSession, Prompt: text}
}

// submit feeds one prompt through OnUserPrompt and returns its output.
func (h *harness) submit(text string) Output {
	h.t.Helper()
	out, err := h.obs.OnUserPrompt(context.Background(), promptOf(text))
	require.NoError(h.t, err)
	return out
}

// readRoot re-materializes root's whole content out of st.
func readRoot(ctx context.Context, t *testing.T, st store.Store, root core.Hash) string {
	t.Helper()
	rc, err := st.Open(ctx, root)
	require.NoError(t, err)
	defer func() { require.NoError(t, rc.Close()) }()
	b, err := io.ReadAll(rc)
	require.NoError(t, err)
	return string(b)
}

func TestOnUserPrompt_StoresVerbatim(t *testing.T) {
	h := newHarness(t)

	h.submit(samplePrompt)

	require.Len(t, h.Store.Puts, 1, "one PutBytes per non-empty prompt")
	require.Equal(t, samplePrompt, string(h.Store.Puts[0].Body), "the user's own bytes, unmodified")

	opts := h.Store.putOpts(0)
	require.Equal(t, userPromptSubmit, opts.Tool)
	require.Empty(t, opts.Path, "a prompt is not the content of a path")
	require.NotNil(t, opts.Canon.Strip,
		`a NIL Strip is canon's documented "every class" — the OPPOSITE request`)
	require.Empty(t, opts.Canon.Strip, `"no optional class" is an EMPTY, non-nil Strip`)
	require.False(t, opts.Canon.MinHash.Enabled,
		"no near-duplicate signature: a prompt is never delta-encoded against a prior one")
	require.True(t, opts.KeepRaw, "KeepRaw, not Canon.KeepDeltas, is what preserves the deltas")
	require.False(t, opts.Ephemeral, "G2.3: the user's own words are never a first eviction candidate")
}

func TestOnUserPrompt_VerbatimAgainstARealStore(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	clock := newFakeClock()

	require.True(t, config.Defaults().Store.Canonicalize.Enabled)
	require.Equal(t, []string{"timestamps", "ansi", "pids", "addresses", "tmpPaths", "durations"},
		config.Defaults().Store.Canonicalize.Strip,
		"the six classes this row proves the PROMPT path opts out of and the tool path does not")

	o, st := newRealStoreObserver(t, root, clock, obs.New(clock))
	t.Cleanup(func() { _ = st.Close() })

	_, err := o.OnUserPrompt(ctx, promptOf(verbatimPrompt))
	require.NoError(t, err)

	rec, err := st.ToolUse(ctx, VerbatimPromptID(testSession, 0))
	require.NoError(t, err)
	require.Equal(t, verbatimPrompt, readRoot(ctx, t, st, rec.Root),
		"§8.1 item 7: the stored object is the user's bytes, byte for byte")

	// The control. The SAME text as a Bash result, through the SAME store, IS canonicalized — so
	// the row above is a statement about verbatimOptions rather than about a store that happened
	// to be configured to strip nothing.
	_, err = o.OnToolUse(ctx, bashOf("toolu_1", "systemctl status pgbouncer", verbatimPrompt))
	require.NoError(t, err)
	bashRec, err := st.ToolUse(ctx, "toolu_1")
	require.NoError(t, err)
	canonicalized := readRoot(ctx, t, st, bashRec.Root)
	require.NotEqual(t, verbatimPrompt, canonicalized)
	require.NotContains(t, canonicalized, "2026-08-24T12:34:56Z", "the timestamps class ran here")
	require.NotContains(t, canonicalized, "4711", "the pids class ran here")
}

func TestOnUserPrompt_RecordsIndexEntry(t *testing.T) {
	const promptTokens = core.Tokens(9)
	h := newHarness(t)
	h.Store.DefaultTokens = promptTokens

	h.submit(samplePrompt)

	require.Len(t, h.Store.Records, 1)
	rec := h.Store.Records[0]
	require.Equal(t, userPromptSubmit, rec.Tool)
	require.Equal(t, core.ToolUseID("prompt_sess_sp08_0"), rec.ID,
		"a UserPromptSubmit payload carries no tool_use_id, so the identity is DERIVED")
	require.Equal(t, VerbatimPromptID(testSession, 0), rec.ID)
	require.Equal(t, testSession, rec.Session)
	require.Equal(t, core.TurnIndex(0), rec.Turn, "the prompt is recorded AT Turn (decision 4)")
	require.Equal(t, core.UnixMilli(testEpoch.UnixMilli()), rec.TS)
	require.Equal(t, int64(len(samplePrompt)), rec.Bytes)
	require.Equal(t, promptTokens, rec.Tokens)
	require.Equal(t, store.StatusOK, rec.Status)
	require.Empty(t, rec.Path)

	digest, preview := store.ArgsDigest(promptArgs(samplePrompt))
	require.Equal(t, digest, rec.ArgsDigest, "§5.8 owns the digest; nothing else may re-derive it")
	require.Equal(t, preview, rec.ArgsPreview)
	require.Contains(t, rec.ArgsPreview, "pgbouncer", "the preview carries the prompt itself")
}

func TestOnUserPrompt_TurnIncrements(t *testing.T) {
	h := newHarness(t)

	h.submit("why did the pool reset?")
	h.submit("and what changed just before that?")

	require.Len(t, h.Store.Records, 2)
	require.Equal(t, core.ToolUseID("prompt_sess_sp08_0"), h.Store.Records[0].ID)
	require.Equal(t, core.ToolUseID("prompt_sess_sp08_1"), h.Store.Records[1].ID)

	st := h.state(testSession)
	st.mu.Lock()
	defer st.mu.Unlock()
	require.Equal(t, core.TurnIndex(2), st.Turn, "OnUserPrompt records at Turn, THEN increments")
	require.Equal(t, core.TurnIndex(1), st.LastPromptTurn, "the turn the last prompt was recorded at")
}

func TestOnUserPrompt_DAGNodeAndSegmentEdge(t *testing.T) {
	const segment = core.SegmentID(3)
	const promptTokens = core.Tokens(11)

	h := newHarness(t)
	h.Store.DefaultTokens = promptTokens
	st := h.state(testSession)
	st.mu.Lock()
	st.Segment = segment
	st.mu.Unlock()

	h.submit("why did the pool reset?")

	node, ok := h.Graph.Node(dag.UserPromptNode(0))
	require.True(t, ok, "userprompt:0 — the node is keyed on the TURN, with no session component")
	require.Equal(t, dag.KindUserPrompt, node.Kind)
	require.Equal(t, core.TurnIndex(0), node.Turn)
	require.Equal(t, promptTokens, node.Tokens)
	require.Equal(t, 0, node.Pos, "Pos is the node's START position (decision 5)")
	require.Equal(t, string(VerbatimPromptID(testSession, 0)), node.Ref)

	edges := h.Graph.edges()
	_, ok = findEdge(edges, dag.UserPromptNode(0), dag.AssistantNode(0), dag.EdgeConsumes)
	require.True(t, ok, "BuildUserPrompt's own same-turn edge; it dangles inertly (D-6)")
	_, ok = findEdge(edges, dag.UserPromptNode(0), dag.AssistantNode(1), dag.EdgeConsumes)
	require.True(t, ok, "the bridge to the ANSWERING assistant turn (decision 4: prompt turn + 1) "+
		"— §4.4's actual path by which a backward slice reaches the request that set it off")
	_, ok = findEdge(edges, dag.UserPromptNode(0), dag.SegmentNode(segment), dag.EdgeSequence)
	require.True(t, ok, "segment members point INTO the segment (SP-07 D-1)")

	st.mu.Lock()
	defer st.mu.Unlock()
	require.Equal(t, int(promptTokens), st.PrefixTokens, "a prompt advances the prefix counter")
}

func TestOnUserPrompt_NeverRegenerated(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	clock := newFakeClock()

	o, st := newRealStoreObserver(t, root, clock, obs.New(clock))
	t.Cleanup(func() { _ = st.Close() })

	_, err := o.OnUserPrompt(ctx, promptOf(verbatimPrompt))
	require.NoError(t, err)
	first, err := st.Stats(ctx)
	require.NoError(t, err)

	clock.Advance(time.Minute)
	_, err = o.OnUserPrompt(ctx, promptOf(verbatimPrompt))
	require.NoError(t, err)
	second, err := st.Stats(ctx)
	require.NoError(t, err)

	require.Equal(t, first.Objects, second.Objects,
		"content-addressed: resubmitting the same text writes no chunk the second time")

	rec0, err := st.ToolUse(ctx, VerbatimPromptID(testSession, 0))
	require.NoError(t, err)
	rec1, err := st.ToolUse(ctx, VerbatimPromptID(testSession, 1))
	require.NoError(t, err)
	require.Equal(t, rec0.Root, rec1.Root, "the same bytes are the same object")
	require.NotEqual(t, rec0.ID, rec1.ID, "RecordToolUse APPENDS; there is no update path")
	require.NotEqual(t, rec0.TS, rec1.TS, "each capture keeps the turn and time it was observed at")
	require.Equal(t, 2, second.ToolUses)
}

func TestOnUserPrompt_EmptyPromptIgnored(t *testing.T) {
	g := &fakeGrammar{}
	h := newHarness(t, func(o *Options) { o.Grammar = g })

	out, err := h.obs.OnUserPrompt(context.Background(), promptOf(""))
	require.NoError(t, err)
	require.Equal(t, hookio.Empty(), out)

	require.Empty(t, h.Store.Puts, "an empty prompt mints no object")
	require.Empty(t, h.Store.Records)
	require.Equal(t, graphCounts{}, h.Graph.counts())
	require.Empty(t, g.appended())

	st := h.state(testSession)
	st.mu.Lock()
	defer st.mu.Unlock()
	require.Equal(t, core.TurnIndex(0), st.Turn, "a turn that never happened is not counted")
}

func TestOnUserPrompt_GrammarSymbolAppended(t *testing.T) {
	g := &fakeGrammar{}
	h := newHarness(t, func(o *Options) { o.Grammar = g })

	h.submit(samplePrompt)

	require.Equal(t, []grammar.Symbol{promptSymbol}, g.appended(),
		"the user half of §8.1 item 6's action stream, appended exactly once")
}

func TestOnUserPrompt_ThrashWarningInFullMode(t *testing.T) {
	rule := thrashRule(1, 11, "FileRead", "FileEdit", "Bash")
	h := newHarness(t, func(o *Options) {
		o.Grammar = &fakeGrammar{Thrashing: []grammar.Rule{rule}}
	})

	// PostToolUse is where a thrashing nonterminal becomes visible, and it has no channel to say
	// so through: the warning has to wait for the next prompt.
	h.drive(readOf("toolu_1", "src/a.ts", "alpha\n"))
	out := h.submit("keep going")

	require.NotNil(t, out.HookSpecificOutput)
	require.Equal(t, userPromptSubmit, out.HookSpecificOutput.HookEventName,
		"additionalContext reaches the transcript only when the event name matches its hook")
	require.Equal(t, grammar.FormatWarning(grammar.Warning{
		Rule: rule, Repeats: 11, Message: thrashAdvice, Turns: []core.TurnIndex{1},
	}), out.HookSpecificOutput.AdditionalContext)
}

func TestOnUserPrompt_NoThrashWarningInPassiveMode(t *testing.T) {
	rule := thrashRule(1, 11, "FileRead", "FileEdit", "Bash")
	h := newHarness(t, func(o *Options) {
		o.Grammar = &fakeGrammar{Thrashing: []grammar.Rule{rule}}
		o.Mode = func() Mode { return ModePassive }
	})

	h.drive(readOf("toolu_1", "src/a.ts", "alpha\n"))
	out := h.submit("keep going")

	require.Equal(t, hookio.Empty(), out, "§12 forbids injection while degraded")
	require.Len(t, h.Store.Puts, 2, "and mode gates OUTPUT, never the capture (decision 10)")
	require.Len(t, h.Store.Records, 2)
}

func TestOnUserPrompt_ThrashWarnedOncePerRule(t *testing.T) {
	rule := thrashRule(1, 11, "FileRead", "FileEdit", "Bash")
	h := newHarness(t, func(o *Options) {
		o.Grammar = &fakeGrammar{Thrashing: []grammar.Rule{rule}}
	})

	h.drive(readOf("toolu_1", "src/a.ts", "alpha\n"))
	first := h.submit("keep going")
	h.drive(readOf("toolu_2", "src/a.ts", "alpha\n"))
	second := h.submit("still going")

	require.NotNil(t, first.HookSpecificOutput)
	require.NotEmpty(t, first.HookSpecificOutput.AdditionalContext)
	require.Equal(t, hookio.Empty(), second,
		"Sequitur reports a rule for as long as it stays above the threshold; the warning is once")
}

func TestOnUserPrompt_PutFailureStillIncrementsTurn(t *testing.T) {
	h := newHarness(t)
	h.Store.PutErr = errors.New("no space left on device")

	out := h.submit(samplePrompt)

	require.Equal(t, hookio.Empty(), out, "a failed capture never blocks the prompt")
	require.Equal(t, int64(1), h.counter("observer.err.prompt.put"))
	require.Empty(t, h.Store.Records, "no index entry may point at an object that was never written")
	require.Equal(t, graphCounts{}, h.Graph.counts())

	st := h.state(testSession)
	st.mu.Lock()
	defer st.mu.Unlock()
	require.Equal(t, core.TurnIndex(1), st.Turn, "the turn happened whether or not the bytes landed")
	require.Equal(t, 0, st.PrefixTokens, "no node was minted, so nothing advanced the prefix")
}

func TestVerbatimPromptID(t *testing.T) {
	require.Equal(t, core.ToolUseID("prompt_abc_7"), VerbatimPromptID("abc", 7))
	require.Equal(t, core.ToolUseID("prompt_abc_0"), VerbatimPromptID("abc", 0),
		"turn 0 is a real turn, not an absent one")
}

func TestPromptArgs_IsTheToolInputAPromptDoesNotHave(t *testing.T) {
	require.JSONEq(t, `{"prompt":"fix the pgbouncer 1.18 pool bypass"}`,
		string(promptArgs(samplePrompt)))

	_, preview := store.ArgsDigest(promptArgs(samplePrompt))
	require.NotEmpty(t, preview,
		"without a synthesized document every prompt would collapse onto the empty-args digest")
}

// TestOnUserPrompt_BackwardSliceReachesThePrompt is the reachability half of §4.4 that the edge
// assertions above cannot state: against a REAL graph, a backward slice from a tool-use node must
// surface the user prompt that set the work off. Decision 4 puts the answering assistant turn at
// the prompt's turn + 1, and only BuildToolUse mints assistant nodes, so the path is
// tooluse ← assistant:1 ← userprompt:0 — through the hand-emitted bridge edge, not the builder's
// same-turn edge (which targets assistant:0, a node no path creates, and dangles inertly).
func TestOnUserPrompt_BackwardSliceReachesThePrompt(t *testing.T) {
	h, g := newRealGraphHarness(t)

	h.submit(samplePrompt)                        // recorded at turn 0; Turn advances to 1
	h.drive(readOf("toolu_1", "src/a.ts", "a\n")) // recorded at turn 1; mints assistant:1

	for _, thin := range []bool{true, false} {
		slice, err := g.BackwardSlice([]dag.NodeID{dag.ToolUseNode("toolu_1")},
			dag.SliceOptions{Thin: thin})
		require.NoError(t, err)
		require.Contains(t, slice.Scores, dag.UserPromptNode(0),
			"thin=%v: the slice from the work must reach the request that caused it (§4.4)", thin)
	}
}
