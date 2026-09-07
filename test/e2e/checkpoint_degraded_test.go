package e2e

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/testutil"
)

// cpDegradedSession is the session identity the degraded-passive row drives, distinct from
// cpSession so the two rows never share a draft file or a registry entry.
const cpDegradedSession = core.SessionID("sess-e2e-sp10-degraded")

// cpDegradedReplays is how many tool uses this row replays before closing its first segment. It is
// deliberately smaller than the full-mode row's 60: what this row proves is a control-flow split,
// not an encoding volume, and every replayed turn is a real process spawn.
const cpDegradedReplays = 20

// The two segment spans this row closes, one on each side of the `qompack checkpoint` call. Two,
// and not one, because "the draft still advanced" is a statement about MOVEMENT: a single closed
// segment proves only that a draft exists, while a second one closed after the hook proves the
// frontier is still climbing on the far side of a degraded PreCompact.
const (
	cpDegradedFirstEnd  = core.TurnIndex(cpSegmentTurns - 1)
	cpDegradedSecondEnd = core.TurnIndex(2*cpSegmentTurns - 1)
)

// TestE2E_CheckpointDegradedPassiveSealsNothing is the degraded-passive SP-10 e2e row
// (plans/V4-SP-10-checkpointer-l4.md "Conformance and e2e"; V4-VERIFY row V4-SP10-18's second
// half). It is a DIFFERENT outcome from the full-mode row, not a weaker one, and the reason is
// worth stating exactly because the gate is not internal/checkpoint's to choose.
//
// The SHIPPED route gates the seam: handleCheckpoint calls svc.PreCompact only when
// `d.svc.PreCompact != nil && mode.MayAct()` (internal/daemon/handlers.go). In
// contract.ModeDegradedPassive, MayAct() is false, so in this mode the seam is NEVER CALLED AT
// ALL. Nothing is finalized, no customInstructions is emitted, and the route still records the
// History observation, writes the terminal-hook marker and answers OK: true with hookio.Empty() —
// which is §12.1's "everything that acts is off" applied by the layer that owns the mode.
//
// Durable progress is not lost, and that is the other half this row pins. Frontier advancement
// carries NO act. prefix, so the idle controller keeps running it while degraded; it writes only
// state/draft-<session>.json, never the context window. The draft therefore keeps growing, and the
// first PreCompact or act.checkpoint_cadence tick after the mode returns to full seals it. The row
// asserts that split — no seal, continued advance — rather than asserting a write that provably
// cannot happen.
func TestE2E_CheckpointDegradedPassiveSealsNothing(t *testing.T) {
	ctx := context.Background()
	bin := Build(t)
	p := testutil.NewProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	env := e2eEnv(p)

	// ── Force ModeDegradedPassive BEFORE the daemon exists ───────────────────────────────────────
	// The daemon constructs its own monitor over <root>/.qompack/state/contract.json inside New and
	// loads that file, so degrading through a monitor on the same state path IS the seam — the same
	// one v3_x08_test.go uses. There is no other way in: the daemon's monitor is an unexported
	// field on neither Options nor the Daemon interface.
	statePath := filepath.Join(paths.Of(p.Root).State, "contract.json")
	forced := []contract.Result{{
		ID: contract.ID("sp10.forced"), OK: false, Severity: contract.SevCritical,
		Expected: "host contract holds", Observed: "forced by the SP-10 degraded e2e row",
		TS: core.NowMilli(p.Clock),
	}}
	preMon := contract.NewMonitor(p.Log, nil, statePath)
	preMon.Degrade("sp10 degraded e2e", forced)
	require.Equal(t, contract.ModeDegradedPassive, preMon.Mode())

	c := cpStartDaemon(t, p, cpDegradedSession)
	require.Equal(t, contract.ModeDegradedPassive.String(), e2eStatus(t, p.Root).Mode,
		"the daemon must have adopted the persisted degradation at New (§12.1: it survives into the next session)")

	// EXACTLY ONE session-start, and the count is load-bearing. Every SessionStart runs the
	// monitor's full RunAll, and TWO consecutive clean cycles restore ModeFull — so a second
	// session-start here would silently un-degrade the daemon and turn every assertion below into a
	// full-mode assertion wearing a degraded-mode name. One call leaves the restore counter at 1.
	//
	// It cannot simply be skipped either: session-start is what makes the session LIVE in the
	// registry, and advanceAllSessions iterates live sessions.
	obsRunHook(t, bin, []string{"session-start"}, sessionStartFor(t, p.Root, cpDegradedSession), env)
	require.True(t, c.D.Registry().IsLive(cpDegradedSession),
		"session-start must register %s as live even while degraded — §12.1 keeps L0/L1 recording", cpDegradedSession)

	cpReplayTurns(t, c, bin, p, cpDegradedSession, cpDegradedReplays)

	// ── Tick 1: the frontier advances while degraded, and the cadence does not ───────────────────
	firstSeg := cpCloseSegment(t, c.Segs, cpDegradedSession, 0, cpDegradedFirstEnd)

	ran, err := c.D.Idle().RunOnce(ctx, cpIdleBudget)
	require.NoError(t, err)
	require.Subset(t, ran, []string{"advance_frontier", "materialize_pins"},
		"neither name carries the act. prefix, so §12.1 keeps both running while degraded; ran=%v", ran)
	require.NotContains(t, ran, "act.checkpoint_cadence",
		"act.checkpoint_cadence is scheduler-initiated work: §12.1's \"no scheduler-initiated "+
			"checkpoints\" is enforced purely by that name prefix, so the ASSERTION is on the name, "+
			"not only on the effect; ran=%v", ran)

	draft, ok := cpReadDraft(t, p.Root, cpDegradedSession)
	require.True(t, ok, "advance_frontier must have begun and persisted state/draft-%s.json while degraded", cpDegradedSession)
	require.Equal(t, cpDegradedFirstEnd, draft.Frontier,
		"the draft covers through the EndTurn of the one segment closed so far")
	require.Contains(t, draft.Encoded, firstSeg)
	require.Empty(t, cpCheckpointArtifacts(t, p.Root),
		"no idle tick may seal an artifact in ModeDegradedPassive")

	// ── The hook: exit 0, no customInstructions, nothing sealed ──────────────────────────────────
	require.Equal(t, contract.ModeDegradedPassive.String(), e2eStatus(t, p.Root).Mode,
		"the mode must still be degraded-passive at the instant the checkpoint hook runs — otherwise "+
			"this row is silently asserting the full-mode path")

	out := cpRunCheckpointHook(t, bin, p, cpDegradedSession)
	require.Nil(t, out.HookSpecificOutput,
		"handleCheckpoint leaves out = hookio.Empty() when !mode.MayAct(), so the response carries no "+
			"hookSpecificOutput at all — and therefore no customInstructions")

	require.Empty(t, cpCheckpointArtifacts(t, p.Root),
		"the seam was never called, so nothing was finalized: no new file may appear under checkpoints/")
	manifest, err := paths.ReadManifest(paths.Of(p.Root))
	require.NoError(t, err)
	require.Empty(t, manifest,
		"a manifest line without an artifact would be the worse half of the same failure; ReadManifest "+
			"reports (nil, nil) for a manifest that was never written")

	// The route's own non-seam work still happened: the history observation is what both PreCompact
	// contract assertions read, and §12.1 keeps recording on while acting is off.
	h := contract.LoadHistory(contract.HistoryPath(p.Root))
	require.Equal(t, cpDegradedSession, h.LastPrecompactSession,
		"handleCheckpoint records the PreCompact observation before the mode gate, not after")
	require.Positive(t, h.LastPrecompactTS)
	require.Empty(t, h.PrecompactInstr,
		"nothing was emitted, so nothing may have been copied into History.PrecompactInstr")

	// ── Tick 2: the frontier is still climbing on the far side of a degraded PreCompact ──────────
	secondSeg := cpCloseSegment(t, c.Segs, cpDegradedSession, cpDegradedFirstEnd+1, cpDegradedSecondEnd)

	ran, err = c.D.Idle().RunOnce(ctx, cpIdleBudget)
	require.NoError(t, err)
	require.Subset(t, ran, []string{"advance_frontier", "materialize_pins"}, "ran=%v", ran)
	require.NotContains(t, ran, "act.checkpoint_cadence", "ran=%v", ran)

	advanced, ok := cpReadDraft(t, p.Root, cpDegradedSession)
	require.True(t, ok)
	require.Equal(t, cpDegradedSecondEnd, advanced.Frontier,
		"the draft's frontier must keep advancing across idle ticks while degraded (§12.1)")
	require.Greater(t, advanced.Frontier, draft.Frontier,
		"\"still advanced\" is a claim about movement: the frontier must be strictly greater than it "+
			"was before the degraded PreCompact, not merely non-zero")
	require.Contains(t, advanced.Encoded, secondSeg)
	require.Equal(t, draft.Seq, advanced.Seq,
		"nothing was finalized, so the draft still holds the same unclaimed seq it did before the hook")

	require.Empty(t, cpCheckpointArtifacts(t, p.Root),
		"the durable seal is deferred, not lost: it happens on the first PreCompact or "+
			"act.checkpoint_cadence tick after the mode returns to full, which is outside this row")
}
