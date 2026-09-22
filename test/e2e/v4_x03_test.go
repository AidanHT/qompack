// V4 §4.3 — the O1 span instruction is rendered from a REAL checkpoint frontier.
//
// Wired: internal/store's SegmentLog closes and encodes real segments; internal/daemon's
// advance_frontier idle task drives internal/checkpoint's FrontierAdvancer (unit G) over them; the
// frontier the advancer commits is what FocusInstructions renders the span paragraph from; and the
// paragraph itself is read from the daemon's checkpoint reply over the real IPC transport. It no
// longer reaches `qompack checkpoint`'s stdout: the host has no PreCompact hookSpecificOutput and
// rejects one (C1.12), so the hook client withholds it (v4Rig.PreCompactReply).
//
// RETIRED CLAUSE (reconciliation map §4.3, row V4-SP10-13): this is LOCAL draft progress, not a
// native O(1) context boundary. Nothing here asserts any host-side compaction effect, and the
// guard below asserts the emitted paragraph makes no such claim either.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/testutil"
)

// x3v4Session is this row's session identity.
const x3v4Session = core.SessionID("sess-e2e-v4-x03")

// The synthetic segment ladder this row closes directly through SP-06's shipped
// store.SegmentLog.Close — not through SP-12's changepoint detector, whose firing is a property of
// the corpus rather than of the frontier this row is about.
const (
	x3v4Segments    = 3
	x3v4SegmentTurn = 20
	// x3v4FrontierTurn is store.SegmentLog.Frontier's answer once every segment of the session is
	// encoded: the EndTurn of the last CONSECUTIVELY encoded segment.
	x3v4FrontierTurn = core.TurnIndex(x3v4Segments*x3v4SegmentTurn - 1)
)

// x3v4ReadPreCompactArtifact reads state/precompact.json — the only published carrier of the
// Ref.Frontier the emitted span paragraph was rendered from.
func x3v4ReadPreCompactArtifact(t *testing.T, root string) cpPreCompactArtifact {
	t.Helper()
	b, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(root).State, "precompact.json")))
	require.NoError(t, err, "§15 step 6 writes state/precompact.json on every PreCompact")
	var art cpPreCompactArtifact
	require.NoError(t, json.Unmarshal(b, &art), "state/precompact.json: %s", b)
	return art
}

// TestV4_O1SpanInstructionFromARealCheckpointFrontier is V4-VERIFY §4.3.
//
// The negative control is the FIRST arm: before any segment is closed and encoded there is no
// durable evidence, and the emitted instruction must not claim a span past turn 0. The second arm
// closes real segments, lets the real advancer commit a frontier over them, and asserts the
// paragraph names THAT position. A span instruction that were a constant, a fabrication, or a
// value read from anywhere but the committed frontier would make both arms report the same number
// and fail here.
func TestV4_O1SpanInstructionFromARealCheckpointFrontier(t *testing.T) {
	ctx := context.Background()
	p := testutil.NewProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	r := v4StartRig(t, p)
	env := e2eEnv(p)

	obsRunHook(t, r.Bin, []string{"session-start"}, sessionStartFor(t, p.Root, x3v4Session), env)
	obsRunHook(t, r.Bin, []string{"observe", "prompt"},
		obsPromptPayload(t, p.Root, x3v4Session, "trace the frontier through a real segment ladder"), env)
	r.SeedTurns(t, x3v4Session, "v4x03", 4)

	// ── Arm 1, the negative control: no encoded evidence, so no span past turn 0 ──────────────────
	_, instrNoEvidence := r.PreCompactReply(t, x3v4Session)
	require.NotEmpty(t, instrNoEvidence, "PreCompact must still emit instructions with no frontier")
	artNoEvidence := x3v4ReadPreCompactArtifact(t, p.Root)
	require.Equal(t, core.TurnIndex(0), artNoEvidence.Frontier,
		"with no segment closed and encoded there is no durable evidence, so the committed frontier "+
			"is 0 — the advancer's documented fallback, not a guess")
	require.NotContains(t, instrNoEvidence,
		fmt.Sprintf("through turn %d", int(x3v4FrontierTurn)),
		"NEGATIVE CONTROL: the span paragraph must not name a frontier no evidence backs")

	// ── Arm 2: real closed segments, a real advance, and the paragraph that names it ─────────────
	cpCloseObserverSegment(t, r.Segs, x3v4Session)
	segIDs := make([]core.SegmentID, 0, x3v4Segments)
	for i := range x3v4Segments {
		start := core.TurnIndex(i * x3v4SegmentTurn)
		segIDs = append(segIDs, cpCloseSegment(t, r.Segs, x3v4Session, start, start+x3v4SegmentTurn-1))
	}

	ran := r.RunIdle(t)
	require.Contains(t, ran, "advance_frontier", "the advancing idle task must have run; ran=%v", ran)

	draft, ok := cpReadDraft(t, p.Root, x3v4Session)
	require.True(t, ok, "advance_frontier must have begun and persisted state/draft-%s.json", x3v4Session)
	require.Equal(t, x3v4FrontierTurn, draft.Frontier,
		"the committed frontier is store.SegmentLog.Frontier — the EndTurn of the last "+
			"CONSECUTIVELY encoded segment, so a gap anywhere below holds it back")
	require.Subset(t, draft.Encoded, segIDs,
		"every segment closed above must have been folded into the draft; draft.encoded=%v", draft.Encoded)

	// The committed position is backed by DURABLE evidence: the segment log itself, re-read, agrees.
	logFrontier, err := r.Segs.Frontier(ctx, x3v4Session)
	require.NoError(t, err)
	require.Equal(t, draft.Frontier, logFrontier,
		"the draft's frontier must be the segment log's own answer, not a number the draft invented")
	for _, id := range segIDs {
		seg, segErr := r.Segs.Get(ctx, id)
		require.NoError(t, segErr)
		require.True(t, seg.Closed, "segment %d must be closed", int(id))
		require.True(t, seg.EncodedOnce,
			"segment %d must be marked encoded in the durable index — that mark IS the evidence "+
				"the committed frontier stands on", int(id))
	}

	_, instr := r.PreCompactReply(t, x3v4Session)
	require.NotEmpty(t, instr)
	art := x3v4ReadPreCompactArtifact(t, p.Root)
	require.Equal(t, x3v4FrontierTurn, art.Frontier,
		"the frontier the span paragraph was rendered from must be the one the advancer committed")
	require.True(t, art.SpanInstruction,
		"IncrementalSpanInstruction is on by default, so the span paragraph was requested")

	require.Contains(t, instr,
		fmt.Sprintf("A durable checkpoint (`.qompack/checkpoints/%04d.json`) fully covers the "+
			"session through turn %d", int(art.Seq), int(x3v4FrontierTurn)),
		"the span paragraph must name the artifact's project-relative path and the LOCAL frontier "+
			"the advancer committed: %s", instr)
	require.Contains(t, instr, fmt.Sprintf("Summarize only what happened after turn %d", int(x3v4FrontierTurn)),
		"narrowing the summarizer to the residual span is the paragraph's whole purpose: %s", instr)

	// The retired clause, asserted as an absence: this is local draft progress and must not be
	// dressed up as a host-side context boundary (row V4-SP10-13).
	for _, forbidden := range []string{
		"context window has been", "native compaction", "the host has compacted", "O(1) context",
	} {
		require.NotContains(t, strings.ToLower(instr), strings.ToLower(forbidden),
			"the span paragraph must claim LOCAL draft progress only, never a host-side effect: %s", instr)
	}
}
