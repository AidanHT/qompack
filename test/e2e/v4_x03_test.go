// V4 §4.3 — the checkpoint frontier a seal records is a REAL, evidence-backed one.
//
// Wired: internal/store's SegmentLog closes and encodes real segments; internal/daemon's
// advance_frontier idle task drives internal/checkpoint's FrontierAdvancer (unit G) over them; and
// the frontier the advancer commits is the one the seal records in state/precompact.json.
//
// Criterion change (C1.18): this row used to read the O1 span PARAGRAPH — rendered from that
// frontier — from the daemon's checkpoint reply and pin that it named the committed frontier (and
// did not, in the negative-control arm). The paragraph was a PreCompact customInstructions, which no
// host accepts (C1.12), so its producer is retired and the reply is the empty object: both arms now
// pin that, and the frontier half — the part that is Qompack's own state — is asserted as before.
//
// RETIRED CLAUSE (reconciliation map §4.3, row V4-SP10-13): this is LOCAL draft progress, not a
// native O(1) context boundary. Nothing here asserts any host-side compaction effect.
package e2e

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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

	// ── Arm 1, the negative control: no encoded evidence, so no frontier past turn 0 ──────────────
	cpRequireNoInstructionReply(t, r.PreCompactReply(t, x3v4Session))
	artNoEvidence := x3v4ReadPreCompactArtifact(t, p.Root)
	require.Equal(t, core.TurnIndex(0), artNoEvidence.Frontier,
		"NEGATIVE CONTROL: with no segment closed and encoded there is no durable evidence, so the "+
			"committed frontier is 0 — the advancer's documented fallback, not a guess")

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

	cpRequireNoInstructionReply(t, r.PreCompactReply(t, x3v4Session))
	art := x3v4ReadPreCompactArtifact(t, p.Root)
	require.Equal(t, x3v4FrontierTurn, art.Frontier,
		"the frontier the seal records must be the one the advancer committed")
	require.Greater(t, int(art.Frontier), int(artNoEvidence.Frontier),
		"the two arms differ only in the evidence, so only the evidence can have moved the frontier")
}
