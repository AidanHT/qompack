package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
)

// X7 (plans/V3-VERIFY-observer-and-negative-knowledge.md §5): the store's MarkEncoded DPI guard
// (§4.6, "never compress a compression"), proven against REAL observer-produced segments rather
// than hand-built ones. SP-08 opens the segment at session start and closes it at session end;
// this test plays the wave-3 checkpointer's role itself — the MarkEncoded calls below are the
// seam SP-10 will occupy, composed in the test file per the §5 rule.

// x7SessionID is the one session this test drives.
const x7SessionID = core.SessionID("v3-x7-session")

// The event mix the X7 row pins: 40 mixed tool uses and 3 prompts. Only prompts advance the
// observer's turn index (prompt.go st.Turn++; tool uses record at the current turn), so the
// observer's final turn — the EndTurn the session-end close must record — is exactly x7Prompts.
const (
	x7ToolUses    = 40
	x7Prompts     = 3
	x7FinalTurn   = core.TurnIndex(x7Prompts)
	x7FirstSeq    = core.CheckpointSeq(7)
	x7ConflictSeq = core.CheckpointSeq(8)
)

// x7FeatureKeys is §6.6's five-key BOCD feature summary, as session.go spells the keys.
var x7FeatureKeys = []string{
	"path_jaccard", "tool_shift", "lexical_cohesion", "gap_seconds", "todo_transition",
}

// x7MustJSON marshals v or fails the test.
func x7MustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

// x7ToolEvent shapes one PostToolUse the way the host would deliver it: file tools carry
// {"file_path":…}, Bash carries {"command":…}, and the response carries real content bytes.
func x7ToolEvent(t *testing.T, i int) hookio.Event {
	t.Helper()
	e := hookio.Event{
		HookEventName: "PostToolUse",
		SessionID:     x7SessionID,
		ToolUseID:     core.ToolUseID(fmt.Sprintf("toolu_v3_x7_%03d", i)),
	}
	switch i % 4 {
	case 0, 1: // Read: re-reads of a small path set, the L0 bread and butter
		path := fmt.Sprintf("src/pkg%d/handler.go", i%5)
		e.ToolName = "Read"
		e.ToolInput = x7MustJSON(t, map[string]string{"file_path": path})
		e.ToolResponse = x7MustJSON(t, map[string]string{
			"content": fmt.Sprintf("package pkg%d\n\nfunc Handler%d() int { return %d }\n", i%5, i, i),
		})
	case 2: // Bash: pathless output
		e.ToolName = "Bash"
		e.ToolInput = x7MustJSON(t, map[string]string{"command": "go test ./..."})
		e.ToolResponse = x7MustJSON(t, map[string]any{
			"exit_code": 0, "stdout": fmt.Sprintf("ok  \tqompack/pkg%d\t0.01%ds\n", i%5, i),
		})
	default: // Grep
		e.ToolName = "Grep"
		e.ToolInput = x7MustJSON(t, map[string]string{"pattern": "Handler", "path": "src"})
		e.ToolResponse = x7MustJSON(t, map[string]string{
			"content": fmt.Sprintf("src/pkg%d/handler.go:3:func Handler%d\n", i%5, i),
		})
	}
	return e
}

// x7EncodeLines counts index/segments.jsonl's `"op":"encode"` records — the durable half of the
// DPI guard, which idempotent re-marking must not grow.
func x7EncodeLines(t *testing.T, root string) int {
	t.Helper()
	b, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(root).Index, "segments.jsonl")))
	require.NoError(t, err, "index/segments.jsonl must exist once a segment has been opened")
	n := 0
	for _, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, `"op":"encode"`) {
			n++
		}
	}
	return n
}

// TestV3_ObserverSegmentsRespectDPIGuard drives a real observer over a real store through a full
// session lifecycle, then plays the checkpointer: MarkEncoded once (seq 7), again idempotently,
// then into a different seq — which the §4.6 DPI guard must refuse, in memory and after a reopen.
func TestV3_ObserverSegmentsRespectDPIGuard(t *testing.T) {
	ctx := context.Background()
	p := testutil.NewProject(t)
	clk := p.Clock

	st, err := store.Open(p.Root, p.Cfg, store.Deps{Log: p.Log, Clock: clk})
	require.NoError(t, err)
	closed := false
	defer func() {
		if !closed {
			_ = st.Close()
		}
	}()

	g, err := dag.Open(p.Root, p.Cfg, p.Log)
	require.NoError(t, err)

	obsv, err := observer.New(observer.Options{
		ProjectRoot: p.Root, Cfg: p.Cfg, Store: st, Graph: g,
		Log: p.Log, Clock: clk,
	})
	require.NoError(t, err)

	// Step 1: session start (source "startup") opens a segment.
	clk.Advance(time.Second)
	_, err = obsv.OnSessionStart(ctx, hookio.Event{
		HookEventName: "SessionStart", SessionID: x7SessionID, Source: "startup", CWD: p.Root,
	})
	require.NoError(t, err)

	cur, err := st.Segments().Current(ctx, x7SessionID)
	require.NoError(t, err, "OnSessionStart must have opened a segment")
	require.False(t, cur.Closed, "the freshly opened segment must be open")
	id := cur.ID
	require.NotZero(t, id, "SegmentID is 1-based; 0 means the open failed")

	// Step 2: 40 mixed tool uses and 3 prompts, one FakeClock second apart — the prompts are
	// interleaved so the final prompt is not the final event, proving EndTurn tracks the turn
	// index rather than the event count.
	promptAt := map[int]bool{0: true, 15: true, 30: true}
	prompts := []string{
		"add retry handling to the fetcher",
		"now make the retries observable in the log",
		"run the suite and fix what breaks",
	}
	tool, prompt := 0, 0
	for i := 0; i < x7ToolUses+x7Prompts; i++ {
		clk.Advance(time.Second)
		if promptAt[i] {
			_, err := obsv.OnUserPrompt(ctx, hookio.Event{
				HookEventName: "UserPromptSubmit", SessionID: x7SessionID, Prompt: prompts[prompt],
			})
			require.NoError(t, err)
			prompt++
			continue
		}
		_, err := obsv.OnToolUse(ctx, x7ToolEvent(t, tool))
		require.NoError(t, err)
		tool++
	}
	require.Equal(t, x7ToolUses, tool)
	require.Equal(t, x7Prompts, prompt)

	// Step 3: session end closes the segment with endTurn == st.Turn and the five-key §6.6
	// feature summary.
	clk.Advance(time.Second)
	_, err = obsv.OnSessionEnd(ctx, hookio.Event{
		HookEventName: "SessionEnd", SessionID: x7SessionID,
	})
	require.NoError(t, err)

	segs := st.Segments()
	seg, err := segs.Get(ctx, id)
	require.NoError(t, err)
	require.True(t, seg.Closed, "OnSessionEnd must close the segment")
	require.Equal(t, x7FinalTurn, seg.EndTurn,
		"EndTurn must equal the observer's final turn (only prompts advance it)")
	require.Len(t, seg.Features, len(x7FeatureKeys),
		"the close must record exactly the five §6.6 feature keys, got %v", seg.Features)
	for _, k := range x7FeatureKeys {
		require.Contains(t, seg.Features, k)
	}

	// Step 4: first MarkEncoded — the checkpointer's role, played from the test file.
	require.NoError(t, segs.MarkEncoded(ctx, []core.SegmentID{id}, x7FirstSeq))
	seg, err = segs.Get(ctx, id)
	require.NoError(t, err)
	require.True(t, seg.EncodedOnce)
	require.Equal(t, x7FirstSeq, seg.CheckpointSeq)
	require.Equal(t, 1, x7EncodeLines(t, p.Root))

	// Step 5: same seq again — idempotent, and no second durable encode record.
	require.NoError(t, segs.MarkEncoded(ctx, []core.SegmentID{id}, x7FirstSeq),
		"re-marking into the same seq must be idempotent")
	seg, err = segs.Get(ctx, id)
	require.NoError(t, err)
	require.True(t, seg.EncodedOnce)
	require.Equal(t, x7FirstSeq, seg.CheckpointSeq)
	require.Equal(t, 1, x7EncodeLines(t, p.Root),
		"an idempotent re-mark must not append a second encode record")

	// Step 6: a DIFFERENT seq — the §4.6 DPI guard, over the live write path.
	err = segs.MarkEncoded(ctx, []core.SegmentID{id}, x7ConflictSeq)
	require.ErrorIs(t, err, core.ErrAlreadyEncoded)
	seg, err = segs.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, x7FirstSeq, seg.CheckpointSeq, "a refused mark must leave the recorded seq untouched")

	// Step 7: the frontier is the segment's EndTurn and nothing is left unencoded.
	fr, err := segs.Frontier(ctx, x7SessionID)
	require.NoError(t, err)
	require.Equal(t, seg.EndTurn, fr)
	un, err := segs.Unencoded(ctx, x7SessionID)
	require.NoError(t, err)
	require.Empty(t, un)

	// Reopen from disk and repeat step 6: the guard is durable, not in-memory.
	closed = true
	require.NoError(t, st.Close())

	st2, err := store.Open(p.Root, p.Cfg, store.Deps{Log: p.Log, Clock: clk})
	require.NoError(t, err)
	defer func() { require.NoError(t, st2.Close()) }()

	err = st2.Segments().MarkEncoded(ctx, []core.SegmentID{id}, x7ConflictSeq)
	require.ErrorIs(t, err, core.ErrAlreadyEncoded,
		"the DPI guard must survive a store reopen")
	seg, err = st2.Segments().Get(ctx, id)
	require.NoError(t, err)
	require.True(t, seg.EncodedOnce)
	require.Equal(t, x7FirstSeq, seg.CheckpointSeq)
	require.Equal(t, 1, x7EncodeLines(t, p.Root))
}
