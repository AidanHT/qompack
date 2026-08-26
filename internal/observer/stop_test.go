package observer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/tokens"
	"github.com/stretchr/testify/require"
)

// ── §8.1 item 8 / G10.1: Stop and SubagentStop ──

// subagentSummaryText is the prose a subagent returns to its parent. It is the ONLY part of a
// subagent's work the parent's context ever holds, which is the whole of G10.1.
const subagentSummaryText = "Found the bug in retry.ts"

// retryBody is the tool-result body the ref rows read back through the fakes.
const retryBody = "export async function retry() {}\n"

// stopOf builds a Stop / SubagentStop payload.
func stopOf(subagent bool) Event {
	name := "Stop"
	if subagent {
		name = subagentStop
	}
	return Event{HookEventName: name, SessionID: testSession}
}

// stop feeds one Stop through OnStop, asserting the silence rule both branches owe the host.
func (h *harness) stop(e Event, subagent bool) {
	h.t.Helper()
	out, err := h.obs.OnStop(context.Background(), e, subagent)
	require.NoError(h.t, err)
	require.Equal(h.t, hookio.Empty(), out)
}

// lastPut returns the body of the most recent PutBytes call — the capture blob, since a
// SubagentStop's Put is the last thing the hook does with the store.
func (h *harness) lastPut() putCall {
	h.Store.mu.Lock()
	defer h.Store.mu.Unlock()
	require.NotEmpty(h.t, h.Store.Puts, "no PutBytes call was made")
	return h.Store.Puts[len(h.Store.Puts)-1]
}

// decodeCapture unmarshals a capture blob. It asserts the bytes really are a SubagentCapture
// rather than trusting the observer's own struct, which is what makes the retrieval path in G10.1
// a claim about the STORED JSON.
func decodeCapture(t *testing.T, b []byte) SubagentCapture {
	t.Helper()
	var capture SubagentCapture
	require.NoError(t, json.Unmarshal(b, &capture))
	return capture
}

// lastCapture decodes the most recent capture blob.
func (h *harness) lastCapture() SubagentCapture {
	h.t.Helper()
	return decodeCapture(h.t, h.lastPut().Body)
}

// captureObserver builds an observer over an ALREADY-OPEN store with a fresh session map, a fresh
// graph and the given clock. Two of these over one store is what "two independent observers" means
// in TestOnStop_CaptureIsDeterministic: nothing is shared but the store the claim is about.
func captureObserver(t *testing.T, root string, st store.Store, clock *fakeClock) *observer {
	t.Helper()
	built, err := New(Options{
		ProjectRoot: root, Cfg: config.Defaults(), Store: st, Graph: newFakeGraph(),
		Log: logging.Nop(), Metrics: obs.New(clock), Clock: clock,
	})
	require.NoError(t, err)
	impl, ok := built.(*observer)
	require.True(t, ok, "New must return the concrete observer")
	return impl
}

func TestOnStop_MainAgentIncrementsTurnAndFlushes(t *testing.T) {
	g := &fakeGrammar{}
	h := newHarness(t, func(o *Options) { o.Grammar = g })

	h.stop(stopOf(false), false)

	st := h.state(testSession)
	st.mu.Lock()
	defer st.mu.Unlock()
	require.Equal(t, core.TurnIndex(1), st.Turn, "the assistant turn has ended (decision 4)")
	require.Equal(t, core.UnixMilli(testEpoch.UnixMilli()), st.LastTS)
	require.Equal(t, 1, h.Graph.FlushCalls, "a turn boundary is where the graph is durable")
	require.Empty(t, h.Store.Puts,
		"the assistant's own text is not a tool result and the transcript already holds it")
	require.Empty(t, h.Store.Records, "no index entry either: nothing was stored to point at")
	require.Equal(t, []grammar.Symbol{stopSymbol}, g.appended(), "§8.1 item 6's action stream")
}

func TestOnStop_SubagentCapturesSummary(t *testing.T) {
	h := newHarness(t)

	e := stopOf(true)
	e.ToolResponse = json.RawMessage(fmt.Sprintf(`{"content":%s}`, jsonString(subagentSummaryText)))
	h.stop(e, true)

	require.Len(t, h.Store.Puts, 1, "one capture per SubagentStop")
	capture := h.lastCapture()
	require.Equal(t, subagentSummaryText, capture.Summary)
	require.Equal(t, testSession, capture.Session)
	require.Equal(t, core.TurnIndex(0), capture.Turn, "recorded at the PRE-increment turn")
	require.Equal(t, core.UnixMilli(testEpoch.UnixMilli()), capture.TS)
	require.NotNil(t, capture.ToolResults, "an empty window is [], never null")

	opts := h.Store.putOpts(0)
	require.Equal(t, subagentStop, opts.Tool)
	require.Empty(t, opts.Path, "a capture is not the content of a path")
	require.NotNil(t, opts.Canon.Strip, "a NIL Strip is canon's documented \"every class\"")
	require.Empty(t, opts.Canon.Strip, "the blob is JSON this package generated, not host content")
	require.False(t, opts.Canon.MinHash.Enabled)
	require.True(t, opts.KeepRaw)

	require.Equal(t, int64(1), h.counter(counterSubagentCapture))

	st := h.state(testSession)
	st.mu.Lock()
	defer st.mu.Unlock()
	require.Equal(t, core.TurnIndex(1), st.Turn, "the capture occupies a turn slot (decision 4)")
}

func TestOnStop_SubagentCapturesToolHashes(t *testing.T) {
	h := newHarness(t)

	h.drive(
		readOf("toolu_1", "src/retry.ts", "export async function retry() {}\n"),
		readOf("toolu_2", "src/backoff.ts", "export const backoff = 2\n"),
		bashOf("toolu_3", "npm test", "3 passing\n"),
	)
	h.stop(stopOf(true), true)

	capture := h.lastCapture()
	require.Len(t, capture.ToolResults, 3,
		"every tool result the subagent produced since the last boundary")

	// The capture's OWN index entry is the fourth record; the refs describe the three before it.
	require.Len(t, h.Store.Records, 4)
	for i, want := range h.Store.Records[:3] {
		got := capture.ToolResults[i]
		require.Equal(t, want.ID, got.ToolUseID, "ref %d", i)
		require.Equal(t, want.Root.String(), got.Root,
			"ref %d carries the STORE's own root, so each one is independently expand-able", i)
		require.Equal(t, want.Tool, got.Tool, "ref %d", i)
		require.Equal(t, want.Path, got.Path, "ref %d", i)
		require.Equal(t, want.Bytes, got.Bytes, "ref %d", i)
	}
	require.Equal(t, "FileRead", capture.ToolResults[0].Tool, "the DISPLAY name, not the host's")
	require.NotEmpty(t, capture.ToolResults[0].Path)
	require.Empty(t, capture.ToolResults[2].Path, "a Bash result is the content of no path")
}

func TestOnStop_SubagentWindowStartsAtLastPrompt(t *testing.T) {
	h := newHarness(t)

	h.submit("review the retry path")
	h.drive(
		readOf("toolu_1", "src/retry.ts", "export async function retry() {}\n"),
		readOf("toolu_2", "src/backoff.ts", "export const backoff = 2\n"),
	)
	h.stop(stopOf(true), true)
	first := h.lastCapture()

	h.drive(readOf("toolu_3", "src/clock.ts", "export const now = () => 0\n"))
	h.stop(stopOf(true), true)
	second := h.lastCapture()

	require.Len(t, first.ToolResults, 2, "the window opens at the prompt that provoked the subagent")
	require.Equal(t, core.ToolUseID("toolu_1"), first.ToolResults[0].ToolUseID)
	require.Equal(t, core.ToolUseID("toolu_2"), first.ToolResults[1].ToolUseID)

	require.Len(t, second.ToolResults, 1, "the previous capture moved the cursor")
	require.Equal(t, core.ToolUseID("toolu_3"), second.ToolResults[0].ToolUseID)
}

func TestOnStop_SubagentNameFromExtra(t *testing.T) {
	const agent = "code-reviewer"
	h := newHarness(t)

	e := stopOf(true)
	e.Extra = map[string]json.RawMessage{extraAgentKey: json.RawMessage(jsonString(agent))}
	h.stop(e, true)

	require.Equal(t, agent, h.lastCapture().Agent,
		"resolveEvent restores the one resolved key into Extra daemon-side")
	require.Len(t, h.Store.Records, 1)
	require.Equal(t, agent, h.Store.Records[0].Subagent,
		"the index carries the same name, so a capture is findable by who produced it")
}

func TestOnStop_SubagentNameFallback(t *testing.T) {
	for name, extra := range map[string]map[string]json.RawMessage{
		"absent":     nil,
		"other_keys": {"subagent": json.RawMessage(`true`)},
		"a_number":   {extraAgentKey: json.RawMessage(`42`)},
		"an_object":  {extraAgentKey: json.RawMessage(`{"name":"code-reviewer"}`)},
		"empty":      {extraAgentKey: json.RawMessage(`""`)},
		"malformed":  {extraAgentKey: json.RawMessage(`"unterminated`)},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			e := stopOf(true)
			e.Extra = extra
			h.stop(e, true)

			require.Equal(t, defaultSubagentName, h.lastCapture().Agent)
			require.Equal(t, defaultSubagentName, h.Store.Records[0].Subagent)
		})
	}
}

func TestOnStop_SummaryFromTranscriptTail(t *testing.T) {
	h := newHarness(t)

	e := stopOf(true)
	e.TranscriptPath = filepath.Join("testdata", "transcript_tail.jsonl")
	h.stop(e, true)

	require.Equal(t, "block one\nblock two", h.lastCapture().Summary,
		"the LAST assistant line with a text block, its text blocks joined by a newline")
}

func TestOnStop_TranscriptMissingIsSilent(t *testing.T) {
	h := newHarness(t)

	e := stopOf(true)
	e.TranscriptPath = filepath.Join(t.TempDir(), "no-such-transcript.jsonl")
	out, err := h.obs.OnStop(context.Background(), e, true)
	require.NoError(t, err, "a missing transcript is not an error a hook may surface")
	require.Equal(t, hookio.Empty(), out)

	require.Len(t, h.Store.Puts, 1, "the hashes are the point; the summary is a bonus")
	require.Empty(t, h.lastCapture().Summary)
	require.Equal(t, int64(0), h.counter(counterErrPrefix+"stop.put"))
}

func TestOnStop_EmptySummaryStillStoresHashes(t *testing.T) {
	h := newHarness(t)

	h.drive(
		readOf("toolu_1", "src/retry.ts", "export async function retry() {}\n"),
		readOf("toolu_2", "src/backoff.ts", "export const backoff = 2\n"),
	)
	h.stop(stopOf(true), true)

	capture := h.lastCapture()
	require.Empty(t, capture.Summary, "no tool_response and no transcript")
	require.Len(t, capture.ToolResults, 2,
		"the parent never held these results; losing the prose must not lose the pointers")
}

// stopTokens is the tokens.Estimator double. It implements EstimateRoot only: that is the single
// method this path calls, and embedding the interface makes any other one panic loudly rather than
// answer a silent zero.
type stopTokens struct {
	tokens.Estimator

	Answer core.Tokens
	Class  tokens.Class
	Calls  int
}

func (e *stopTokens) EstimateRoot(_ context.Context, _ []core.ChunkRef, c tokens.Class) core.Tokens {
	e.Calls++
	e.Class = c
	return e.Answer
}

func TestOnStop_TokensEstimatedWhenTheStoreReportsNone(t *testing.T) {
	const answer = core.Tokens(37)
	est := &stopTokens{Answer: answer}
	h := newHarness(t, func(o *Options) { o.Tokens = est })
	h.Store.DefaultTokens = 0 // the store priced nothing

	h.stop(stopOf(true), true)

	require.Equal(t, 1, est.Calls)
	require.Equal(t, tokens.ClassJSON, est.Class, "the blob is JSON this package generated")
	require.Len(t, h.Store.Records, 1)
	require.Equal(t, answer, h.Store.Records[0].Tokens)

	node, ok := h.Graph.Node(dag.ToolUseNode(SubagentCaptureID(testSession, 0)))
	require.True(t, ok)
	require.Equal(t, answer, node.Tokens, "the record and the node agree on one price")
	require.Equal(t, 0, node.Pos, "Pos is the node's START position (decision 5)")

	st := h.state(testSession)
	st.mu.Lock()
	defer st.mu.Unlock()
	require.Equal(t, int(answer), st.PrefixTokens, "and a capture advances the prefix like any node")
}

func TestOnStop_PutFailureStillIncrementsTurn(t *testing.T) {
	h := newHarness(t)
	h.drive(readOf("toolu_1", "src/retry.ts", retryBody))
	h.Store.PutErr = errors.New("no space left on device")

	h.stop(stopOf(true), true)

	require.Equal(t, int64(1), h.counter(counterErrPrefix+stageStopPut))
	require.Equal(t, int64(0), h.counter(counterSubagentCapture))
	require.Len(t, h.Store.Records, 1,
		"no index record may name an object that was never written")
	require.False(t, h.Graph.has(dag.ToolUseNode(SubagentCaptureID(testSession, 0))),
		"and no DAG node either")

	st := h.state(testSession)
	st.mu.Lock()
	defer st.mu.Unlock()
	require.Equal(t, core.TurnIndex(1), st.Turn,
		"the capture is lost; the turn the session took is not")
	require.Equal(t, 0, st.SubagentSince,
		"the window does not close on a failure, so the next capture still names toolu_1")
}

func TestOnStop_ConsumesEdges(t *testing.T) {
	h := newHarness(t)

	h.drive(
		readOf("toolu_1", "src/retry.ts", "export async function retry() {}\n"),
		readOf("toolu_2", "src/backoff.ts", "export const backoff = 2\n"),
	)
	h.stop(stopOf(true), true)

	id := SubagentCaptureID(testSession, 0)
	node, ok := h.Graph.Node(dag.ToolUseNode(id))
	require.True(t, ok, "the capture is a tool-use node minted by dag.ToolUseNode")
	require.Equal(t, dag.KindToolUse, node.Kind)
	require.Equal(t, subagentStop, node.Ref, "Ref is the tool name, as on any tool-use node")

	edges := h.Graph.edges()
	consumed := 0
	for _, e := range edges {
		if e.To == dag.ToolUseNode(id) && e.Kind == dag.EdgeConsumes {
			consumed++
		}
	}
	require.Equal(t, 2, consumed, "one EdgeConsumes per ref and not one more")

	for _, ref := range []core.ToolUseID{"toolu_1", "toolu_2"} {
		_, ok := findEdge(edges, dag.ToolResultNode(ref), dag.ToolUseNode(id), dag.EdgeConsumes)
		require.True(t, ok, "D-1: the subagent's RESULT is the earlier/producer end (%s)", ref)
		_, ok = findEdge(edges, dag.ToolUseNode(id), dag.ToolResultNode(ref), dag.EdgeConsumes)
		require.False(t, ok, "reversed, a backward slice would never reach the detail (%s)", ref)
	}
}

func TestOnStop_CaptureIsDeterministic(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	clock := newFakeClock()

	st, err := store.Open(root, config.Defaults(), store.Deps{Log: logging.Nop(), Clock: clock})
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	// The two preceding tool uses, replayed identically by each observer.
	seen := []Event{
		readOf("toolu_1", "src/retry.ts", strings.Repeat("export async function retry() {}\n", 8)),
		readOf("toolu_2", "src/backoff.ts", strings.Repeat("export const backoff = 2\n", 8)),
	}
	e := stopOf(true)
	e.ToolResponse = json.RawMessage(fmt.Sprintf(`{"content":%s}`, jsonString(subagentSummaryText)))
	id := SubagentCaptureID(testSession, 0)

	run := func() (store.ToolUseRecord, string) {
		o := captureObserver(t, root, st, clock)
		for _, ev := range seen {
			_, err := o.OnToolUse(ctx, ev)
			require.NoError(t, err)
		}
		_, err := o.OnStop(ctx, e, true)
		require.NoError(t, err)
		rec, err := st.ToolUse(ctx, id)
		require.NoError(t, err)
		return rec, readRoot(ctx, t, st, rec.Root)
	}

	firstRec, firstBlob := run()

	// The second observer replays the tool uses first, so the object count is snapshotted with
	// only the capture still to come: "unchanged" is then a statement about the CAPTURE.
	second := captureObserver(t, root, st, clock)
	for _, ev := range seen {
		_, err := second.OnToolUse(ctx, ev)
		require.NoError(t, err)
	}
	require.NoError(t, st.Flush(ctx))
	before, err := st.Stats(ctx)
	require.NoError(t, err)

	_, err = second.OnStop(ctx, e, true)
	require.NoError(t, err)
	require.NoError(t, st.Flush(ctx))
	after, err := st.Stats(ctx)
	require.NoError(t, err)

	secondRec, err := st.ToolUse(ctx, id)
	require.NoError(t, err)
	secondBlob := readRoot(ctx, t, st, secondRec.Root)

	require.Equal(t, firstBlob, secondBlob, "struct field order makes the blob byte-deterministic")
	require.Equal(t, firstRec.Root, secondRec.Root, "same bytes, same content address")
	require.Equal(t, before.Objects, after.Objects,
		"the second capture deduped completely: nothing novel was written")
}

func TestOnStop_RetrievalPathG10_1(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	clock := newFakeClock()

	o, st := newRealStoreObserver(t, root, clock, obs.New(clock))
	t.Cleanup(func() { _ = st.Close() })

	for _, ev := range []Event{
		readOf("toolu_1", "src/retry.ts", strings.Repeat("export async function retry() {}\n", 8)),
		bashOf("toolu_2", "npm test -- retry", strings.Repeat("3 passing\n", 8)),
	} {
		_, err := o.OnToolUse(ctx, ev)
		require.NoError(t, err)
	}

	e := stopOf(true)
	e.Extra = map[string]json.RawMessage{extraAgentKey: json.RawMessage(`"code-reviewer"`)}
	e.ToolResponse = json.RawMessage(fmt.Sprintf(`{"content":%s}`, jsonString(subagentSummaryText)))
	_, err := o.OnStop(ctx, e, true)
	require.NoError(t, err)
	require.NoError(t, st.Flush(ctx))

	// expand(subagent_<session>_<turn>) — the retrieval path §3's G10.1 row says does not exist.
	rec, err := st.ToolUse(ctx, SubagentCaptureID(testSession, 0))
	require.NoError(t, err)
	require.Equal(t, subagentStop, rec.Tool)
	require.Equal(t, "code-reviewer", rec.Subagent)

	capture := decodeCapture(t, []byte(readRoot(ctx, t, st, rec.Root)))
	require.Equal(t, testSession, capture.Session)
	require.Equal(t, "code-reviewer", capture.Agent)
	require.Equal(t, core.TurnIndex(0), capture.Turn)
	require.Equal(t, core.UnixMilli(testEpoch.UnixMilli()), capture.TS)
	require.Equal(t, subagentSummaryText, capture.Summary)
	require.Len(t, capture.ToolResults, 2)

	// …and each root inside it is independently expand-able, which is the half of G10.1 the
	// summary alone can never give back.
	for _, ref := range capture.ToolResults {
		refRec, err := st.ToolUse(ctx, ref.ToolUseID)
		require.NoError(t, err)
		require.Equal(t, refRec.Root.String(), ref.Root)
		require.NotEmpty(t, readRoot(ctx, t, st, refRec.Root),
			"detail the parent never held, still reachable by hash")
	}
}

// ── tailAssistantText ──

// assistantLine renders one transcript line whose assistant message carries texts as text blocks.
func assistantLine(texts ...string) string {
	blocks := make([]string, len(texts))
	for i, s := range texts {
		blocks[i] = fmt.Sprintf(`{"type":"text","text":%s}`, jsonString(s))
	}
	return fmt.Sprintf(
		`{"isSidechain":true,"type":"assistant","message":{"role":"assistant","content":[%s]}}`,
		strings.Join(blocks, ","))
}

// writeTranscript writes lines as a JSONL file and returns its path.
func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "transcript.jsonl")
	require.NoError(t, os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600))
	return p
}

// partialLinePad is how much junk precedes the valid JSON inside the first line, so that a
// truncation window landing halfway through it leaves a remnant that WOULD parse.
const partialLinePad = 3000

func TestTailAssistantText_PartialFirstLine(t *testing.T) {
	const fragment = "FRAGMENT-THAT-MUST-NOT-BE-RETURNED"
	const wanted = "the last complete assistant line"

	// A line whose TAIL is itself a valid assistant line: only then does "drop the partial line"
	// mean something, because a truncated remnant that cannot parse would be skipped anyway.
	padded := strings.Repeat("x", partialLinePad) + assistantLine(fragment)

	t.Run("a_later_complete_line_wins", func(t *testing.T) {
		p := writeTranscript(t, padded, assistantLine(wanted))
		fi, err := os.Stat(p)
		require.NoError(t, err)
		// Exactly at the start of line 1's embedded JSON, so its remnant is a line that WOULD
		// parse: only then is "drop the partial line" a claim about anything.
		window := fi.Size() - partialLinePad

		got := tailAssistantText(p, window)
		require.Equal(t, wanted, got)
		require.NotContains(t, got, fragment)
	})

	t.Run("the_fragment_is_never_the_answer", func(t *testing.T) {
		// Nothing after the partial line qualifies, so without the drop the fragment WOULD win.
		p := writeTranscript(t, padded, `{"type":"user","message":{"role":"user","content":[]}}`)
		fi, err := os.Stat(p)
		require.NoError(t, err)
		window := fi.Size() - partialLinePad

		require.Empty(t, tailAssistantText(p, window),
			"a partial line is not a line, and half a JSON object is not a summary")
	})

	t.Run("a_whole_file_keeps_its_first_line", func(t *testing.T) {
		// max(0, size-maxBytes) is what makes the drop CONDITIONAL: when the window covers the
		// whole file there is no partial line, and dropping line 1 would lose a one-line
		// transcript's only assistant message.
		p := writeTranscript(t, assistantLine(wanted))
		require.Equal(t, wanted, tailAssistantText(p, transcriptTailBytes))
	})
}

func TestTailAssistantText_Degrades(t *testing.T) {
	require.Empty(t, tailAssistantText("", transcriptTailBytes), "no transcript path")
	require.Empty(t, tailAssistantText(filepath.Join(t.TempDir(), "gone.jsonl"), transcriptTailBytes))
	require.Empty(t, tailAssistantText(t.TempDir(), transcriptTailBytes), "a directory is not a file")

	p := writeTranscript(t, "{not json at all", `{"type":"assistant"`, "")
	require.Empty(t, tailAssistantText(p, transcriptTailBytes), "an unparseable line is skipped")

	// An assistant line whose only block is a tool_use has no text to return, so the scan keeps
	// walking backwards rather than answering with "".
	p = writeTranscript(t,
		assistantLine(wantedEarlier),
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1"}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"not mine"}]}}`)
	require.Equal(t, wantedEarlier, tailAssistantText(p, transcriptTailBytes))
	require.Empty(t, tailAssistantText(p, 0), "a zero window reads nothing rather than everything")

	// A window landing inside one enormous line with no newline after it holds no COMPLETE line
	// at all, which is not the same as a file whose lines carry no assistant message.
	huge := filepath.Join(t.TempDir(), "huge.jsonl")
	require.NoError(t, os.WriteFile(huge, []byte(strings.Repeat("y", partialLinePad)), 0o600))
	require.Empty(t, tailAssistantText(huge, partialLinePad/2))
}

// wantedEarlier is the assistant text a scan must walk back to past a tool-use-only line.
const wantedEarlier = "the assistant text before the tool call"

func TestSubagentArgs_IsTheToolInputACaptureDoesNotHave(t *testing.T) {
	digest, preview := store.ArgsDigest(subagentArgs("code-reviewer", subagentSummaryText))
	other, _ := store.ArgsDigest(subagentArgs("test-runner", subagentSummaryText))

	require.NotEqual(t, digest, other,
		"without a synthesized tool_input every capture would collapse onto one digest")
	require.Contains(t, preview, "code-reviewer")
}

func TestSubagentCaptureID(t *testing.T) {
	require.Equal(t, core.ToolUseID("subagent_sess_sp08_7"), SubagentCaptureID(testSession, 7))
}
