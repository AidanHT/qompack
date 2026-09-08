// V4 §4.6 — the negative-knowledge answer state the real ledger produces is the state the real MCP
// surface renders, across the enum's members, and the rehydrated standing instruction reflects it.
//
// SUPERSEDED CLAUSE (reconciliation map §4.6, NC-8): the scenario NAME says "three-way", and the
// enum is no longer three-way. Units D (5935015, aa073fa, 0b530fb) and F (f9cfcc5) make it FIVE
// states — absent / active / stale / unavailable / uncertain. The name is kept because it is the
// identifier the V4 inventory keys on; the assertions below are the CURRENT semantics, and the
// retired three-way claim is guarded against explicitly: a ledger that cannot answer must never
// render as absence.
//
// Wired: the real internal/negknow ledger over a real store and DAG, the real MCP `already_tried`
// and `record_eliminated` handlers, and the real internal/rehydrate digest.
package e2e

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/rehydrate"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
)

// x6v4Session is this row's session identity.
const x6v4Session = core.SessionID("sess-e2e-v4-x06")

// The elimination the active arm records and then queries back.
const (
	x6v4Target   = "src/db/pool.ts:acquire"
	x6v4Approach = "raise the pool timeout to 60s"
	x6v4Reason   = "pgbouncer in transaction mode caps it at 30s regardless"
	x6v4DependsA = "docker-compose.yml"
)

// x6v4LedgerServer builds a real MCP server whose ledger is exactly led.
func x6v4LedgerServer(t *testing.T, p *testutil.Project, st store.Store, led negknow.Ledger) mcp.Server {
	t.Helper()
	return v4Server(t, mcp.ToolDeps{
		Store:       st,
		LedgerFn:    func() negknow.Ledger { return led },
		Cfg:         p.Cfg,
		ProjectRoot: p.Root,
		DisableWhy:  true,
	})
}

// x6v4Answer drives the real already_tried handler and returns its decoded body.
func x6v4Answer(t *testing.T, srv mcp.Server, target, approach string) mcp.AlreadyTriedResult {
	t.Helper()
	var got mcp.AlreadyTriedResult
	v4Call(t, srv, x6v4Session, mcp.ToolAlreadyTried,
		map[string]any{"target": target, "approach": approach}, &got)
	return got
}

// TestV4_AlreadyTriedThreeWayThroughTheRehydratedStandingInstruction is V4-VERIFY §4.6.
//
// The negative control is arm 3: a ledger whose Get cannot be served must answer "unavailable",
// never "absent". That is the assertion the retired three-way name would have made impossible, and
// it is the one that matters — an absence claimed by a blind ledger is a false negative that
// forbids a viable approach.
func TestV4_AlreadyTriedThreeWayThroughTheRehydratedStandingInstruction(t *testing.T) {
	ctx := context.Background()

	// ── Arm 1 + 2: absent, then active, through the real ledger and the real MCP surface ─────────
	p := v4Project(t)
	r := v4StartRig(t, p)
	env := e2eEnv(p)

	obsRunHook(t, r.Bin, []string{"session-start"}, sessionStartFor(t, p.Root, x6v4Session), env)
	obsRunHook(t, r.Bin, []string{"observe", "prompt"},
		obsPromptPayload(t, p.Root, x6v4Session, "why does the pool still time out at 30s"), env)
	r.SeedTurns(t, x6v4Session, "v4x06", 3)
	v4EnsureLedger(t, r, x6v4Session)

	srv := v4Server(t, v4ToolDeps(t, r))

	before := x6v4Answer(t, srv, x6v4Target, x6v4Approach)
	require.Equal(t, "absent", before.State,
		"a BACKED lookup with complete coverage and no record is the only thing that may say absent")
	require.False(t, before.Degraded, "a backed absence is not a degradation")

	var recorded struct {
		Recorded bool   `json:"recorded"`
		ID       string `json:"id"`
	}
	v4Call(t, srv, x6v4Session, mcp.ToolRecordEliminated, map[string]any{
		"target": x6v4Target, "approach": x6v4Approach, "reason": x6v4Reason,
		"scope": "session", "depends_on": []string{x6v4DependsA},
	}, &recorded)

	after := x6v4Answer(t, srv, x6v4Target, x6v4Approach)
	require.Equal(t, "active", after.State,
		"the state the ledger produced must be the state the MCP surface renders: %+v", after)
	require.Contains(t, after.Reason, "pgbouncer", "the surface must carry the recorded reason")

	// The standing instruction: one sentence, both surfaces, byte for byte.
	require.Equal(t, rehydrate.StandingInstruction(), mcp.StandingInstruction,
		"the rehydrated item 3 instruction and the MCP one must agree byte for byte")
	require.NotEmpty(t, mcp.StandingInstruction, "an empty instruction would agree vacuously")

	ac := r.CompactStart(t, x6v4Session)
	require.NotEmpty(t, ac, "a compact SessionStart must inject a rehydrated context")
	require.Contains(t, ac, "## 3. Approaches already eliminated",
		"item 3 is where the ledger's state reaches the model: %s", ac)
	require.Contains(t, ac, x6v4Approach,
		"the standing instruction's section must reflect the elimination the ledger holds: %s", ac)

	// ── Arm 3, the NEGATIVE CONTROL: a ledger whose record log cannot be read ────────────────────
	//
	// The elimination log is replaced by a DIRECTORY of the same name, which is the portable way to
	// make paths.ReadFileShared fail. negknow.Open's materialize then takes its goBlind path — the
	// real degradation, in the real ledger — and every Query answers unavailable.
	pb := v4Project(t)
	require.NoError(t, os.MkdirAll(paths.Long(paths.Of(pb.Root).Records), 0o755))
	require.NoError(t, os.MkdirAll(
		paths.Long(filepath.Join(paths.Of(pb.Root).Records, "eliminations.jsonl")), 0o755),
		"an unreadable elimination log is what makes the ledger blind")

	stb, err := store.Open(pb.Root, pb.Cfg, store.Deps{Log: pb.Log, Clock: pb.Clock})
	require.NoError(t, err)
	t.Cleanup(func() { _ = stb.Close() })
	gb, err := dag.Open(pb.Root, pb.Cfg, pb.Log)
	require.NoError(t, err)
	blind, err := negknow.Open(pb.Root, pb.Cfg, nil, negknow.Deps{
		Store: stb, Graph: gb, Session: x6v4Session, Log: pb.Log, Clock: pb.Clock,
	})
	require.NoError(t, err, "a blind ledger is still USABLE — it answers unavailable, it does not fail to open")
	t.Cleanup(func() { _ = blind.Close() })

	blindAns, err := blind.Query(ctx, x6v4Target, x6v4Approach, negknow.ScopeSession)
	require.NoError(t, err)
	require.Equal(t, negknow.AnswerUnavailable, blindAns.State,
		"a ledger that could not be consulted must answer unavailable, never absent")
	require.NotEmpty(t, blindAns.Coverage.Reason, "an unavailable answer must say WHY")
	require.NotEmpty(t, blindAns.Coverage.Recovery, "and what would resolve it")

	blindSrv := x6v4LedgerServer(t, pb, stb, blind)
	rendered := x6v4Answer(t, blindSrv, x6v4Target, x6v4Approach)
	require.Equal(t, "unavailable", rendered.State,
		"NEGATIVE CONTROL: the MCP surface must render the ledger's unavailable state as its own "+
			"outcome. Rendering it as \"absent\" is the retired three-way behaviour and is a false "+
			"negative: %+v", rendered)
	require.NotEqual(t, "absent", rendered.State)
	require.True(t, rendered.Degraded, "an unavailable answer must be marked degraded")
	require.NotEmpty(t, rendered.Reason, "the surface must carry the coverage reason through")

	// ── Arm 4: uncertain — the ledger answered, but coverage could not back it ───────────────────
	//
	// eliminations.staleResponse "drop" is the shipped configuration under which a stale record is
	// neither an active prohibition nor an absence. The record is made genuinely stale by changing
	// a dependency's stored content and running the ledger's own RefreshStaleness.
	pc := testutil.NewProject(t, testutil.WithConfig(`{"eliminations":{"staleResponse":"drop"}}`))
	require.Equal(t, "drop", pc.Cfg.Eliminations.StaleResponse, "the drop policy must be in force")

	stc, err := store.Open(pc.Root, pc.Cfg, store.Deps{Log: pc.Log, Clock: pc.Clock})
	require.NoError(t, err)
	t.Cleanup(func() { _ = stc.Close() })
	gc, err := dag.Open(pc.Root, pc.Cfg, pc.Log)
	require.NoError(t, err)
	ledc, err := negknow.Open(pc.Root, pc.Cfg, nil, negknow.Deps{
		Store: stc, Graph: gc, Session: x6v4Session, Log: pc.Log, Clock: pc.Clock,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = ledc.Close() })
	mc, ok := ledc.(negknow.Maintainer)
	require.True(t, ok)

	putFileVersion(t, ctx, stc, pc.Clock, 0, x6v4DependsA, "services:\n  db:\n    image: pg:15\n")
	_, _, err = mc.IngestMCP(ctx, negknow.MCPArgs{
		Target: x6v4Target, Approach: x6v4Approach, Reason: x6v4Reason,
		Scope: string(negknow.ScopeSession), DependsOn: []string{x6v4DependsA},
	})
	require.NoError(t, err)

	putFileVersion(t, ctx, stc, pc.Clock, 1, x6v4DependsA, "services:\n  db:\n    image: pg:16\n")
	flipped, err := ledc.RefreshStaleness(ctx, stc)
	require.NoError(t, err)
	require.NotEmpty(t, flipped, "changing a dependency's content must flip the record stale")

	uncertain, err := ledc.Query(ctx, x6v4Target, x6v4Approach, negknow.ScopeSession)
	require.NoError(t, err)
	require.Equal(t, negknow.AnswerUncertain, uncertain.State,
		"under staleResponse=drop a stale record is neither a prohibition nor an absence")
	require.NotEmpty(t, uncertain.Coverage.Reason)

	uncertainSrv := x6v4LedgerServer(t, pc, stc, ledc)
	renderedUncertain := x6v4Answer(t, uncertainSrv, x6v4Target, x6v4Approach)
	require.Equal(t, "uncertain", renderedUncertain.State,
		"the MCP surface must render uncertainty as its own outcome, never as absence: %+v",
		renderedUncertain)
	require.NotEqual(t, "absent", renderedUncertain.State)
}
