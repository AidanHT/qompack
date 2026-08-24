package observer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

func TestOnToolUse_StoresAndIndexes(t *testing.T) {
	h := newHarness(t)
	body := strings.Repeat("export async function refreshToken() {}\n", 52) // ~2 KB

	h.drive(readOf("toolu_1", "src/auth.ts", body))

	require.Len(t, h.Store.Puts, 1, "one PutBytes per non-empty tool result")
	require.Equal(t, "FileRead", h.Store.Puts[0].Opts.Tool, "the DISPLAY name selects the canonicalizer")
	require.Equal(t, "src/auth.ts", h.Store.Puts[0].Opts.Path)
	require.Equal(t, body, string(h.Store.Puts[0].Body))

	require.Len(t, h.Store.Records, 1)
	rec := h.Store.Records[0]
	require.Equal(t, core.ToolUseID("toolu_1"), rec.ID)
	require.Equal(t, testSession, rec.Session)
	require.Equal(t, core.TurnIndex(0), rec.Turn, "OnToolUse records at the current turn (decision 4)")
	require.Equal(t, "FileRead", rec.Tool)
	require.Equal(t, "src/auth.ts", rec.Path)
	require.Equal(t, int64(len(body)), rec.Bytes)
	require.Equal(t, store.StatusOK, rec.Status)
	require.False(t, rec.Ephemeral)

	wantRoot := core.HashBytes(core.DomainArgs, []byte(body))
	require.Equal(t, wantRoot, rec.Root, "the record's Root is the PutResult's root, not a second identity")
}

func TestOnToolUse_CanonOptionsAlwaysIncludeCRLF(t *testing.T) {
	h := newHarness(t, func(o *Options) {
		o.Cfg.Store.Canonicalize.Enabled = false
	})

	h.drive(bashOf("toolu_1", "go build ./...", "ok\n"))

	opts := h.Store.putOpts(0)
	require.Equal(t, []canon.Class{canon.Class("crlf")}, opts.Canon.Strip,
		"crlf is unconditional (resolved decision 2)")
	require.False(t, opts.Canon.MinHash.Enabled)
}

func TestOnToolUse_CanonOptionsFromConfig(t *testing.T) {
	h := newHarness(t)

	h.drive(bashOf("toolu_1", "go test ./...", "ok  \tgithub.com/example/pkg\t0.42s\n"))

	opts := h.Store.putOpts(0)
	require.Equal(t, []canon.Class{
		canon.Class("crlf"),
		canon.Class("timestamps"), canon.Class("ansi"), canon.Class("pids"),
		canon.Class("addresses"), canon.Class("tmpPaths"), canon.Class("durations"),
	}, opts.Canon.Strip)
	require.True(t, opts.Canon.MinHash.Enabled)
	require.Equal(t, 128, opts.Canon.MinHash.Permutations)
	require.InDelta(t, 0.9, opts.Canon.MinHash.NearDupThreshold, 1e-9)
	require.True(t, opts.KeepRaw, "the volatile deltas are the byte-exact recovery record of §8.1 item 1")
}

func TestOnToolUse_FileVersionAppendedForFileContent(t *testing.T) {
	h := newHarness(t)

	h.drive(
		readOf("toolu_1", "src/auth.ts", "before\n"),
		toolUse("toolu_2", "Edit",
			`{"file_path":"src/auth.ts","old_string":"before","new_string":"after"}`,
			`{"content":"after\n"}`),
	)

	require.Len(t, h.Store.FileVersions, 2)
	for i, fv := range h.Store.FileVersions {
		require.Equal(t, "src/auth.ts", fv.Path, "call %d carries the normalized key", i)
		require.Equal(t, core.TurnIndex(0), fv.Version.Turn)
		require.NotZero(t, fv.Version.TS)
	}
}

func TestOnToolUse_NoFileVersionForGrep(t *testing.T) {
	h := newHarness(t)

	h.drive(toolUse("toolu_1", "Grep",
		`{"pattern":"refreshToken","path":"src"}`,
		`{"content":"src/auth.ts:12:refreshToken"}`))

	require.Empty(t, h.Store.FileVersions, "a Grep result is not a version of a file")
}

func TestOnToolUse_ArgsDigestAndPreview(t *testing.T) {
	h := newHarness(t)
	input := `{"command":"go test ./..."}`

	h.drive(toolUse("toolu_1", "Bash", input, `{"exit_code":0,"stdout":"ok\n"}`))

	wantDigest, wantPreview := store.ArgsDigest(json.RawMessage(input))
	require.Len(t, h.Store.Records, 1)
	require.Equal(t, wantDigest, h.Store.Records[0].ArgsDigest,
		"§5.8 owns the digest; the observer must not re-derive it")
	require.Equal(t, wantPreview, h.Store.Records[0].ArgsPreview)
	require.Equal(t, "go test ./...", h.Store.Records[0].ArgsPreview)
}

func TestOnToolUse_ArgsDigestIsKeyOrderInvariant(t *testing.T) {
	h := newHarness(t)

	h.drive(
		toolUse("toolu_1", "Bash", `{"a":1,"command":"x"}`, `{"stdout":"out\n"}`),
		toolUse("toolu_2", "Bash", `{"command":"x","a":1}`, `{"stdout":"out\n"}`),
	)

	require.Len(t, h.Store.Records, 2)
	require.Equal(t, h.Store.Records[0].ArgsDigest, h.Store.Records[1].ArgsDigest,
		"a local json.Compact digest would be key-order dependent")
}

func TestOnToolUse_PreviewTruncatedByStore(t *testing.T) {
	h := newHarness(t)
	long := strings.Repeat("go test ./internal/observer ", 20) // > 500 bytes

	h.drive(bashOf("toolu_1", long, "ok\n"))

	preview := h.Store.Records[0].ArgsPreview
	require.LessOrEqual(t, len(preview), 120, "the ≤120-BYTE cap is §5.8's, not the observer's")
	require.True(t, strings.HasSuffix(preview, "…"), "a truncated preview ends in an ellipsis: %q", preview)
}

func TestOnToolUse_MCPResultIsEphemeral(t *testing.T) {
	h := newHarness(t)

	h.drive(toolUse("toolu_1", "mcp__qompack__expand",
		`{"path":"src/auth.ts"}`, `{"content":"the expanded result"}`))

	require.Len(t, h.Store.Records, 1)
	require.True(t, h.Store.Records[0].Ephemeral, "an mcp__qompack__ result is born ephemeral (decision 6)")
	require.True(t, h.Store.putOpts(0).Ephemeral)

	require.Zero(t, h.Touch.Total(), "retrieval is not exploration: no CMS feed")
	require.Zero(t, h.Explore.Cardinality(), "no HLL feed")
	require.Empty(t, h.Hot.Top(1), "no Misra-Gries feed")
	// This assertion is vacuous until supersede.go exists — nothing calls ToolUsesByPath yet.
	// Commit 3 must keep it meaningful: the supersession scan it lands is exactly what would
	// start calling it, and the ephemeral exclusion of resolved decision 6 is what must keep it
	// from doing so for an mcp__qompack__ result.
	require.Zero(t, h.Store.ByPathCalls, "an ephemeral result is excluded from supersession in both directions")

	require.True(t, h.Graph.has(dag.ToolUseNode("toolu_1")), "it still gets DAG nodes")
}

func TestOnToolUse_EmptyResponseStillIndexed(t *testing.T) {
	h := newHarness(t)

	h.drive(toolUse("toolu_1", "Read", `{"file_path":"src/empty.ts"}`, "null"))

	require.Empty(t, h.Store.Puts, "there is nothing to store")
	require.Len(t, h.Store.Records, 1, "a zero-byte result still produces an index entry")
	require.Equal(t, int64(0), h.Store.Records[0].Bytes)
	require.Empty(t, h.Store.FileVersions, "there is no content to version")
	require.True(t, h.Graph.has(dag.ToolUseNode("toolu_1")))
	require.Len(t, h.signals(), 1)
}

func TestOnToolUse_PutFailureIsSoft(t *testing.T) {
	h := newHarness(t)
	h.Store.PutErr = errors.New("disk full")

	out, err := h.obs.OnToolUse(context.Background(), readOf("toolu_1", "src/auth.ts", "body\n"))

	require.NoError(t, err, "no I/O failure escapes an Observer method (decision 7)")
	require.Equal(t, hookio.Empty(), out)
	require.Equal(t, int64(1), h.counter("observer.err.put"))
	require.Empty(t, h.Store.Records, "never a dangling index record")
}

func TestOnToolUse_IndexFailureStillFeedsSketchesAndDAG(t *testing.T) {
	h := newHarness(t)
	h.Store.RecordErr = errors.New("index unavailable")

	h.drive(readOf("toolu_1", "src/auth.ts", "body\n"))

	require.Equal(t, int64(1), h.counter("observer.err.index"))
	require.Equal(t, uint64(2), h.Touch.Total(), "the tool key and the path key are both fed")
	require.True(t, h.Graph.has(dag.ToolUseNode("toolu_1")), "the object is stored and reachable by root hash")
	require.True(t, h.Graph.has(dag.ToolResultNode("toolu_1")))
}

func TestOnToolUse_CancelledContext(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	out, err := h.obs.OnToolUse(ctx, readOf("toolu_1", "src/auth.ts", "body\n"))

	require.ErrorIs(t, err, context.Canceled, "ctx.Err() is the ONLY error an entry point returns")
	require.Equal(t, hookio.Empty(), out)
	require.Equal(t, storeCounts{}, h.Store.counts())
	require.Equal(t, graphCounts{}, h.Graph.counts())
}

func TestOnToolUse_OversizePayloadTruncated(t *testing.T) {
	const cap1MiB = 1 << 20
	h := newHarness(t, func(o *Options) {
		o.Cfg.Runtime.HotPath.MaxPayloadBytes = cap1MiB
	})

	// 4 MiB of payload. signals.go's own maxScanBytes cap trims the raw JSON to 4 MiB first,
	// which is what leaves the observer a 4 MiB body to apply the CONFIGURED cap to.
	huge := strings.Repeat("a", 4<<20)
	h.drive(toolUse("toolu_1", "Bash", `{"command":"cat big.log"}`,
		fmt.Sprintf(`{"stdout":%s}`, jsonString(huge))))

	require.Len(t, h.Store.Puts, 1)
	require.Len(t, h.Store.Puts[0].Body, cap1MiB, "runtime.hotPath.maxPayloadBytes bounds the hot path")
}

func TestOnToolUse_SignalsDelivered(t *testing.T) {
	h := newHarness(t)

	h.drive(bashOf("toolu_1", `git commit -m "extract the identity call"`,
		"[main 1a2b3c4] extract the identity call\n"))

	got := h.signals()
	require.Len(t, got, 1, "the callback fires once per tool use")
	require.Equal(t, testSession, got[0].Session)
	require.True(t, got[0].Signals.GitCommit)
	require.False(t, got[0].Signals.TestPassed)
	require.Equal(t, int64(1), h.counter("observer.signal.git"))
	require.Equal(t, int64(0), h.counter("observer.signal.test"))
}

func TestOnToolUse_TodoTransitionOnlyOnce(t *testing.T) {
	h := newHarness(t)
	todo := toolUse("toolu_1", "TodoWrite",
		`{"todos":[{"content":"trace the timeout","status":"completed"}]}`,
		`{"content":"ok"}`)
	again := todo
	again.ToolUseID = "toolu_2"

	h.drive(todo, again)

	got := h.signals()
	require.Len(t, got, 2)
	require.True(t, got[0].Signals.TodoCompleted, "the first delivery is the transition")
	require.False(t, got[1].Signals.TodoCompleted, "the same list re-submitted closes nothing new")
	require.Equal(t, int64(1), h.counter("observer.signal.todo"))
}

func TestOnToolUse_ReturnsEmptyOutput(t *testing.T) {
	h := newHarness(t)

	out, err := h.obs.OnToolUse(context.Background(), readOf("toolu_1", "src/auth.ts", "body\n"))

	require.NoError(t, err)
	require.Equal(t, hookio.Empty(), out, "PostToolUse emits nothing")
}

func TestOnToolUse_RemembersToolUseForSubagentWindow(t *testing.T) {
	h := newHarness(t)

	h.drive(
		readOf("toolu_1", "src/a.ts", "alpha\n"),
		readOf("toolu_2", "src/b.ts", "beta\n"),
		bashOf("toolu_3", "go build ./...", "ok\n"),
	)

	st := h.state(testSession)
	require.Len(t, st.ToolUses, 3)
	require.Equal(t, core.ToolUseID("toolu_1"), st.ToolUses[0].ID)
	require.Equal(t, "FileRead", st.ToolUses[0].Tool)
	require.Equal(t, "src/a.ts", st.ToolUses[0].Path)
	require.Equal(t, core.HashBytes(core.DomainArgs, []byte("alpha\n")), st.ToolUses[0].Root)
	require.Equal(t, int64(len("alpha\n")), st.ToolUses[0].Bytes)
	require.Equal(t, core.ToolUseID("toolu_3"), st.ToolUses[2].ID)
	require.Equal(t, "Bash", st.ToolUses[2].Tool)
	require.Empty(t, st.ToolUses[2].Path)
}

func TestOnToolUse_ToolUseRingEvictsAndClampsSubagentSince(t *testing.T) {
	h := newHarness(t)

	// Five calls, then the cursor a prompt or a SubagentStop would leave behind.
	for i := range 5 {
		h.drive(readOf(fmt.Sprintf("toolu_pre_%d", i), "src/a.ts", "alpha\n"))
	}
	st := h.state(testSession)
	st.mu.Lock()
	st.SubagentSince = len(st.ToolUses)
	st.mu.Unlock()

	for i := range subagentWindowCap + 10 {
		h.drive(readOf(fmt.Sprintf("toolu_%d", i), "src/a.ts", "alpha\n"))
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	require.Len(t, st.ToolUses, subagentWindowCap, "the ring is front-evicted at its cap")
	require.Equal(t, 0, st.SubagentSince, "the cursor is clamped in the same operation as the eviction")
	require.NotPanics(t, func() { _ = st.ToolUses[st.SubagentSince:] },
		"a SubagentStop must be able to slice from the cursor")
}

func TestOnToolUse_LastTSIsPreviousEventTS(t *testing.T) {
	h := newHarness(t)

	// features() reports nothing until two full windows are in hand, so the 45 s gap is placed
	// between the last two of the 2*featureWindow events that fill them.
	for i := range 2*featureWindow - 1 {
		h.drive(readOf(fmt.Sprintf("toolu_%d", i), "src/a.ts", "alpha\n"))
	}
	h.Clock.Advance(45 * time.Second)
	h.drive(readOf("toolu_last", "src/a.ts", "alpha\n"))

	got := h.features()
	require.Len(t, got, 1, "exactly the last event completes the two windows")
	require.InDelta(t, 45.0, got[0].Sample.GapSeconds, 1e-9,
		"LastTS must still be the PREVIOUS event's when features() runs")
}

func TestModePassiveStillWrites(t *testing.T) {
	// Every fifth event is a PROMPT, and the grammar has a rule above the thrash threshold, so the
	// two runs genuinely differ in the one way §12 permits: in ModeFull the first prompt carries
	// an additionalContext block and in ModePassive it carries nothing. Everything counted below
	// — the puts, the index entries, the file versions, the nodes, the edges and the CMS — has to
	// come out identical, which is what "mode gates output, never writes" actually claims.
	session := func(t *testing.T, mode Mode) (storeCounts, graphCounts, uint64, []Output) {
		t.Helper()
		h := newHarness(t, func(o *Options) {
			o.Mode = func() Mode { return mode }
			o.Grammar = &fakeGrammar{Thrashing: []grammar.Rule{
				thrashRule(1, 11, "FileRead", "FileEdit", "Bash"),
			}}
		})
		var prompts []Output
		for i := range 20 {
			if i%5 == 0 {
				prompts = append(prompts, h.submit(fmt.Sprintf("keep going, step %d", i)))
			}
			h.drive(readOf(fmt.Sprintf("toolu_%d", i), fmt.Sprintf("src/f%d.ts", i%4), "alpha\n"))
		}
		return h.Store.counts(), h.Graph.counts(), h.Touch.Total(), prompts
	}

	// warned counts how many of a run's prompts carried an injected thrash line.
	warned := func(outs []Output) int {
		n := 0
		for _, out := range outs {
			if out.HookSpecificOutput != nil && out.HookSpecificOutput.AdditionalContext != "" {
				n++
			}
		}
		return n
	}

	fullStore, fullGraph, fullCMS, fullPrompts := session(t, ModeFull)
	passiveStore, passiveGraph, passiveCMS, passivePrompts := session(t, ModePassive)

	require.Equal(t, fullStore, passiveStore, "§12: L0 keeps observing, chunking and storing")
	require.Equal(t, fullGraph, passiveGraph, "the DAG stays correct in degraded-passive mode")
	require.Equal(t, fullCMS, passiveCMS, "the sketches stay correct too")

	require.Equal(t, 1, warned(fullPrompts),
		"the queued warning drains on the first prompt AFTER a tool use, and only once")
	require.Equal(t, 0, warned(passivePrompts),
		"only OnUserPrompt's AdditionalContext differs between the two modes")
}

func TestOnToolUse_ConcurrentSessionsRaceFree(t *testing.T) {
	const perSession = 200
	// Every put is priced identically, so the expected prefix position is arithmetic rather than
	// a recorded number: one prompt node plus one tool_result node per iteration, each advancing
	// by its own token count (decision 5).
	const tokensPerPut = core.Tokens(7)

	h := newHarness(t)
	h.Store.DefaultTokens = tokensPerPut

	var wg sync.WaitGroup
	for _, s := range []core.SessionID{"sess_a", "sess_b"} {
		wg.Add(1)
		go func(sid core.SessionID) {
			defer wg.Done()
			for i := range perSession {
				// A prompt and a tool use per iteration, because both entry points mutate the
				// same session state and decision 9's locking has to hold across both.
				p := promptOf(fmt.Sprintf("step %d", i))
				p.SessionID = sid
				if _, err := h.obs.OnUserPrompt(context.Background(), p); err != nil {
					t.Errorf("OnUserPrompt(%s, %d): %v", sid, i, err)
					return
				}
				e := readOf(fmt.Sprintf("toolu_%s_%d", sid, i), "src/a.ts", "alpha\n")
				e.SessionID = sid
				if _, err := h.obs.OnToolUse(context.Background(), e); err != nil {
					t.Errorf("OnToolUse(%s, %d): %v", sid, i, err)
					return
				}
			}
		}(s)
	}
	wg.Wait()

	for _, sid := range []core.SessionID{"sess_a", "sess_b"} {
		st := h.state(sid)
		st.mu.Lock()
		require.Equal(t, core.TurnIndex(perSession), st.Turn,
			"%s: each prompt increments the turn exactly once and no tool use ever does", sid)
		require.Equal(t, perSession*2*int(tokensPerPut), st.PrefixTokens,
			"%s: PrefixTokens is what a sequential run produces", sid)
		require.Equal(t, core.TurnIndex(perSession-1), st.LastPromptTurn, "%s", sid)
		st.mu.Unlock()
	}
}

// benchHarness builds an observer over a REAL store and a REAL graph on b.TempDir(), which is what
// makes the numbers comparable against budget B-C (l0_process, p99 < 50 ms).
//
// It returns the registry as well as the observer because a Go benchmark reports a MEAN ns/op and
// B-C is stated as a p99. The observer already times every OnToolUse into the observer.tooluse
// histogram, so reportBudget reads the real percentiles back out of it rather than inferring them.
func benchHarness(b *testing.B) (*observer, store.Store, obs.Registry) {
	b.Helper()
	root := b.TempDir()
	cfg := config.Defaults()
	clock := newFakeClock()
	metrics := obs.New(clock)

	st, err := store.Open(root, cfg, store.Deps{Log: logging.Nop(), Clock: clock})
	require.NoError(b, err)
	b.Cleanup(func() { _ = st.Close() })

	g, err := dag.Open(root, cfg, logging.Nop())
	require.NoError(b, err)

	built, err := New(Options{
		ProjectRoot: root,
		Cfg:         cfg,
		Store:       st,
		Graph:       g,
		Touch:       sketch.NewCMS(testCMSEpsilon, testCMSDelta),
		Explore:     sketch.NewHLL(testHLLRegs),
		Hot:         sketch.NewMisraGries(testMGCounters),
		Log:         logging.Nop(),
		Metrics:     metrics,
		Clock:       clock,
	})
	require.NoError(b, err)
	impl, ok := built.(*observer)
	require.True(b, ok)
	return impl, st, metrics
}

// reportBudget publishes the observer.tooluse histogram's p50 and p99, in milliseconds, plus two
// numbers that say whether those latencies were earned honestly.
//
// Feeding one identical payload every iteration would leave the store fully deduped from the second
// call on, so the run would never pay the novel-chunk cost and its headroom against budget B-C would
// be fiction. varyBody is what prevents that, and OBJECTS is the metric that proves it: it is the
// count of chunks actually stored, so it stays flat across iterations when they dedup and climbs
// with them when they do not.
//
// dedup-x is reported beside it but is deliberately NOT the proof. store.Stats.DedupRatio is
// RawBytes/Bytes over a compressed, content-addressed store, so it folds zstd compression and
// WITHIN-payload repetition into the same number as cross-iteration dedup — a self-similar fixture
// scores high on it while still writing every chunk anew.
func reportBudget(b *testing.B, st store.Store, metrics obs.Registry) {
	b.Helper()
	snap := metrics.Hist(histToolUse).Snapshot()
	b.ReportMetric(float64(snap.P50.Microseconds())/1000, "p50-ms")
	b.ReportMetric(float64(snap.P99.Microseconds())/1000, "p99-ms")

	stats, err := st.Stats(context.Background())
	require.NoError(b, err)
	b.ReportMetric(float64(stats.Objects), "objects")
	b.ReportMetric(stats.DedupRatio, "dedup-x")
}

// benchTag returns a per-iteration marker.
//
// It is purely alphabetic on purpose. A numeric counter would be a canonicalization target — the
// pids, addresses and durations classes all rewrite numbers — and a marker the canonicalizer
// normalized away would leave every iteration's CANONICAL bytes identical again, which is the same
// dedup these fixtures exist to control, hidden one layer deeper.
func benchTag(i int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz"
	tag := make([]byte, 8)
	for k := range tag {
		tag[k] = alphabet[i%len(alphabet)]
		i /= len(alphabet)
	}
	return "//qompackbench" + string(tag)
}

// deltaBody rewrites exactly ONE line, near the middle of body: Qompack.md §8.1's own near-duplicate
// case, "same test suite, one new failure". One chunk per call is novel and the rest dedup, which is
// what a real session's re-run of a test command looks like.
func deltaBody(body string, i int) string {
	mid := len(body) / 2
	start := strings.LastIndexByte(body[:mid], '\n') + 1
	end := strings.IndexByte(body[mid:], '\n')
	if end < 0 {
		return body
	}
	return body[:start] + benchTag(i) + body[mid+end:]
}

// varyStride is how often varyBody splices its marker into a payload. It is below the store's
// minimum FastCDC chunk size, so every chunk of every iteration carries at least one marker and no
// chunk can dedup against the previous iteration's.
const varyStride = 512

// varyBody rewrites the payload every varyStride bytes, so EVERY chunk of every iteration is novel
// and every call pays the full object-write cost. It is the pessimal end of the dedup axis.
func varyBody(body string, i int) string {
	marker := []byte(benchTag(i) + "\n")
	out := []byte(body)
	for off := 0; off+len(marker) <= len(out); off += varyStride {
		copy(out[off:], marker)
	}
	return string(out)
}

// benchFixture is one payload-variation strategy: how much of a tool result differs from the
// previous call.
//
// That is the axis the store's dedup path lives on, and measuring one point of it and calling the
// answer "the cost of the pipeline" is exactly the mistake this table exists to stop. Deduped is
// the optimistic end and is kept only for comparison; Delta is the realistic middle and is the one
// carried defect SP08-D1 is stated against; AllNovel is the pessimal end.
type benchFixture struct {
	name string
	vary func(body string, i int) string
}

func benchFixtures() []benchFixture {
	return []benchFixture{
		{"Deduped", func(body string, _ int) string { return body }},
		{"Delta", deltaBody},
		{"AllNovel", varyBody},
	}
}

// runOnToolUseBench drives events through OnToolUse and reports the budget metrics.
//
// It asserts NOTHING about latency, deliberately. B-C is a soft, ungated budget and these numbers
// move with the host; a benchmark that failed the build on one would make every unrelated change
// look like a regression and would put a machine-dependent number in the way of every commit. The
// breach is RECORDED in plans/CARRIED-DEFECTS.tsv (SP08-D1), which is where a known problem belongs.
func runOnToolUseBench(b *testing.B, o *observer, st store.Store, metrics obs.Registry, events []Event) {
	b.Helper()
	ctx := context.Background()
	b.ResetTimer()
	for i := range b.N {
		if _, err := o.OnToolUse(ctx, events[i]); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	reportBudget(b, st, metrics)
}

// repeatTo returns s repeated until it is at least n bytes long.
func repeatTo(s string, n int) string {
	if len(s) == 0 || len(s) >= n {
		return s
	}
	return strings.Repeat(s, n/len(s)+1)
}

func BenchmarkOnToolUse_FileRead64KB(b *testing.B) {
	body := repeatTo("func refreshToken(ctx context.Context) (Token, error) { return t, nil }\n", 64<<10)

	for _, f := range benchFixtures() {
		b.Run(f.name, func(b *testing.B) {
			o, st, metrics := benchHarness(b)
			events := make([]Event, b.N)
			for i := range events {
				events[i] = readOf(fmt.Sprintf("toolu_%d", i), "src/auth.go", f.vary(body, i))
			}
			runOnToolUseBench(b, o, st, metrics, events)
		})
	}
}

// BenchmarkOnToolUse_TestOutput256KB is the evidence benchmark named by carried defect SP08-D1.
// Its Delta sub-bench is the one the defect's acceptance criterion is stated against.
func BenchmarkOnToolUse_TestOutput256KB(b *testing.B) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "corpora", "toolout", "testrunner", "go-test-rerun.txt"))
	require.NoError(b, err)
	out := repeatTo(string(raw), 256<<10)

	for _, f := range benchFixtures() {
		b.Run(f.name, func(b *testing.B) {
			o, st, metrics := benchHarness(b)
			events := make([]Event, b.N)
			for i := range events {
				events[i] = bashOf(fmt.Sprintf("toolu_%d", i), "go test ./...", f.vary(out, i))
			}
			runOnToolUseBench(b, o, st, metrics, events)
		})
	}
}
