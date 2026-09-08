// V4 §4.12 — degraded-passive with every wave-3 subsystem resident.
//
// Wired: the real contract monitor over the real state/contract.json the daemon reads at New; the
// real observer, store, DAG, checkpoint writer, frontier advancer, idle controller and rehydrate
// service, all resident; and the real MCP retrieval handlers, which stay AVAILABLE because they are
// pull-based and cannot mislead (§12.1).
//
// The non-vacuity requirement the brief sets is that the suppression assertion be EXHAUSTIVE over
// the acting paths, enumerated from the live registry rather than from a list written here — so a
// new acting path added without an `act.` prefix fails this row instead of slipping past it.
package e2e

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/testutil"
)

// x12v4Session is this row's session identity.
const x12v4Session = core.SessionID("sess-e2e-v4-x12")

// x12v4ActingPrefix is §12.1's mode gate made mechanical: an idle task whose name carries it acts
// on the context window, and a degraded daemon must not run it.
const x12v4ActingPrefix = "act."

// x12v4Degrade forces ModeDegradedPassive through the same seam the daemon reads at New: a monitor
// over the project's own state/contract.json. There is no other way in — the daemon's monitor is an
// unexported field on neither Options nor the Daemon interface.
func x12v4Degrade(t *testing.T, p *testutil.Project) {
	t.Helper()
	statePath := filepath.Join(paths.Of(p.Root).State, "contract.json")
	mon := contract.NewMonitor(p.Log, nil, statePath)
	mon.Degrade("v4 x12 degraded-passive row", []contract.Result{{
		ID: contract.ID("v4.x12.forced"), OK: false, Severity: contract.SevCritical,
		Expected: "host contract holds", Observed: "forced by the V4 §4.12 row",
		TS: core.NowMilli(p.Clock),
	}})
	require.Equal(t, contract.ModeDegradedPassive, mon.Mode())
}

// TestV4_DegradedPassiveWithEverySubsystem is V4-VERIFY §4.12.
//
// The negative control is the enumeration itself: the suppressed set is derived from the FULL-mode
// run's own task list, so an acting path that is added and then not gated shows up here as a name
// that ran in both modes. A hard-coded suppression list could not catch that.
func TestV4_DegradedPassiveWithEverySubsystem(t *testing.T) {
	ctx := context.Background()

	// ── Reference arm: the same composition in FULL mode, to enumerate the acting paths ──────────
	pf := v4Project(t)
	rf := v4StartRig(t, pf)
	envf := e2eEnv(pf)
	obsRunHook(t, rf.Bin, []string{"session-start"}, sessionStartFor(t, pf.Root, x12v4Session), envf)
	obsRunHook(t, rf.Bin, []string{"observe", "prompt"},
		obsPromptPayload(t, pf.Root, x12v4Session, "keep recording while the contract is degraded"), envf)
	rf.SeedTurns(t, x12v4Session, "v4x12f", 3)

	fullRan := rf.RunIdle(t)
	var actingPaths []string
	for _, name := range fullRan {
		if strings.HasPrefix(name, x12v4ActingPrefix) {
			actingPaths = append(actingPaths, name)
		}
	}
	require.NotEmpty(t, actingPaths,
		"the live registry must contain at least one acting path, or the suppression assertion "+
			"below would be vacuous; ran=%v", fullRan)

	fullAC := rf.CompactStart(t, x12v4Session)
	require.NotEmpty(t, fullAC, "in ModeFull a compact SessionStart DOES inject")
	_, fullInstr := rf.PreCompact(t, x12v4Session)
	require.NotEmpty(t, fullInstr, "in ModeFull a PreCompact DOES emit customInstructions")

	// ── The row proper: the same composition, degraded before the daemon exists ──────────────────
	p := v4Project(t)
	x12v4Degrade(t, p)
	r := v4StartRig(t, p)
	env := e2eEnv(p)

	require.Equal(t, contract.ModeDegradedPassive.String(), e2eStatus(t, p.Root).Mode,
		"the daemon must adopt the persisted degradation at New (§12.1: it survives into the next session)")

	// EXACTLY ONE session-start in the whole row, and it is the COMPACT one. §12.1's recovery rule
	// restores ModeFull after two consecutive clean RunAll cycles, and every SessionStart runs one
	// — so a warm-up session-start followed by a compact session-start would silently un-degrade
	// the daemon and turn every assertion below into a full-mode assertion wearing a degraded name.
	// (Measured: that ordering injects a full payload, which is the shipped RESTORE working, not a
	// gating defect.)
	require.Empty(t, r.CompactStart(t, x12v4Session),
		"a degraded daemon must inject no additionalContext")
	require.True(t, r.D.Registry().IsLive(x12v4Session),
		"§12.1 keeps L0/L1 recording: the session must still register while degraded")

	obsRunHook(t, r.Bin, []string{"observe", "prompt"},
		obsPromptPayload(t, p.Root, x12v4Session, "keep recording while the contract is degraded"), env)
	r.SeedTurns(t, x12v4Session, "v4x12", 3)

	// ── SUPPRESSED: every acting path, exhaustively, from the full-mode enumeration ──────────────
	degradedRan := r.RunIdle(t)
	for _, name := range actingPaths {
		require.NotContains(t, degradedRan, name,
			"acting path %q ran while degraded. §12.1 gates every name carrying the %q prefix; "+
				"ran=%v", name, x12v4ActingPrefix, degradedRan)
	}
	for _, name := range degradedRan {
		require.False(t, strings.HasPrefix(name, x12v4ActingPrefix),
			"NEGATIVE CONTROL: no %q task may run while degraded, including one added since this "+
				"row was written; ran=%v", x12v4ActingPrefix, degradedRan)
	}

	// No customInstructions, no scheduler-initiated checkpoint, no drop report.
	_, instr := r.PreCompact(t, x12v4Session)
	require.Empty(t, instr,
		"the shipped route calls Services.PreCompact only when mode.MayAct(): degraded means the "+
			"seam is never called at all")
	require.Empty(t, cpCheckpointArtifacts(t, p.Root),
		"nothing may be sealed while degraded — neither by the hook nor by the cadence task")
	dropReport := filepath.Join(paths.Of(p.Root).State, "rehydrate-"+string(x12v4Session)+".json")
	require.NoFileExists(t, paths.Long(dropReport),
		"a rehydration that never ran writes no drop report")

	// ── STILL LIVE: L0 and L1 keep recording, and retrieval stays available ──────────────────────
	require.NotEmpty(t, obsToolUseLines(p.Root),
		"§12.1: L0 capture is not an acting path and must keep recording while degraded")
	require.Contains(t, degradedRan, "advance_frontier",
		"frontier advancement carries no act. prefix: it writes only the draft, never the context "+
			"window, so durable progress is not lost; ran=%v", degradedRan)

	srv := v4Server(t, v4ToolDeps(t, r))
	var recalled struct {
		Hits  []map[string]any `json:"hits"`
		Found bool             `json:"found"`
	}
	res := v4Call(t, srv, x12v4Session, mcp.ToolRecall,
		map[string]any{"query": "handler", "k": 5}, &recalled)
	require.False(t, res.IsError,
		"§12.1: MCP retrieval is PULL-based and cannot mislead, so it stays available while degraded")

	// The mode did not silently restore itself under us.
	require.Equal(t, contract.ModeDegradedPassive.String(), e2eStatus(t, p.Root).Mode,
		"the daemon must still be degraded at the end of the row, or every assertion above "+
			"described a different mode than it claimed")
	_ = ctx
}
