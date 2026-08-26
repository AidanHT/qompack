package e2e

// V3-VERIFY §5 X2 — TestV3_ObserverFileVersionsDriveEliminationStaleness.
//
// Seams: observer (file version history) → store.ChangedSince → negknow.RefreshStaleness →
// negknow.RebuildBloom → negknow.Query. (SP-08 + SP-06 + SP-09 + SP-03)
//
// This is the seam the design calls out as the High-severity risk (§12: "Stale negative knowledge
// blocks a now-viable approach"), and wave 2 is the first point at which both ends of it exist:
// the OBSERVER writes the file-version history that the ledger's staleness refresh reads back
// through store.ChangedSince. If the two disagree about paths.Key normalization, an elimination
// never goes stale and a now-viable approach stays blocked — that is the bug this test exists to
// find.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
)

// x2Session is the one session everything in X2 runs under: the observer's events, the ledger's
// records, and the queries.
const x2Session = core.SessionID("sess_v3_x02")

// The X2 elimination, spelled exactly as the §5 X2 row spells it. The reason text deliberately
// differs from negknow_test.go's so that a verbatim-return assertion cannot pass by accident
// against a record another test's fixture happened to shape.
const (
	x2Target   = "src/auth.ts:refreshToken"
	x2Approach = "widen pool timeout"
	x2Reason   = "pgbouncer 1.18 ignores it in transaction mode"
	// x2Synonym is the query phrasing that must resolve to the SAME ApproachClass as x2Approach:
	// the already_tried answer is only useful if a rephrasing of the approach still finds it.
	x2Synonym = "Increasing the connection-pool timeouts"
)

// x2StaleNote is Qompack.md §8.3's re-verification sentence, transcribed here BYTE FOR BYTE —
// including the em dash — so that the assertion against negknow.StaleNote cannot degenerate into
// comparing the constant with itself.
const x2StaleNote = "previously eliminated, but the evidence has changed since — re-verification may be warranted"

// x2ToolEvent builds one PostToolUse hook event: tool (host spelling: "Read", "Write") applied to
// path, with content as the tool response. The response is a bare JSON string, which is the first
// shape observer's responseText decodes.
func x2ToolEvent(t *testing.T, id core.ToolUseID, tool, path, content string) observer.Event {
	t.Helper()
	in, err := json.Marshal(map[string]string{"file_path": path})
	require.NoError(t, err)
	resp, err := json.Marshal(content)
	require.NoError(t, err)
	return observer.Event{
		SessionID:    x2Session,
		ToolName:     tool,
		ToolUseID:    id,
		ToolInput:    in,
		ToolResponse: resp,
	}
}

// x2CountingStore is the thin wrapper the X2 row's step-5 bullet names: it delegates everything
// to the real store and counts ChangedSince calls, so the test can assert RefreshStaleness makes
// exactly one.
type x2CountingStore struct {
	store.Store
	changedSinceCalls int
}

func (s *x2CountingStore) ChangedSince(ctx context.Context, deps []core.Dep) ([]core.Dep, error) {
	s.changedSinceCalls++
	return s.Store.ChangedSince(ctx, deps)
}

// x2BloomBytes returns the current bytes of <root>/.qompack/sketches/tried.bloom, or nil when the
// file does not exist yet.
func x2BloomBytes(t *testing.T, root string) []byte {
	t.Helper()
	b, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(root).Sketches, sketch.TriedBloomBase)))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	return b
}

// TestV3_ObserverFileVersionsDriveEliminationStaleness is §5 X2: the observer — not the test —
// writes the file versions an elimination's depends_on hashes are resolved from and later compared
// against, and a dependency rewrite observed by the observer is what flips the elimination stale.
func TestV3_ObserverFileVersionsDriveEliminationStaleness(t *testing.T) {
	ctx := context.Background()
	p := testutil.NewProject(t)
	before := snapshotProjectFiles(t, p.Root)

	// The three project files the X2 setup names.
	p.WithFiles(t, map[string]string{
		"src/auth.ts":        authTSV1,
		"docker-compose.yml": composeV1,
		"package-lock.json":  lockJSONV1,
	})

	// Setup sanity: the X2 row pins eliminations.rebuildOnStale = "nextIdle" (the Appendix C
	// default) — the flip in step 5 must OWE the rebuild rather than perform it, so that step 7's
	// explicit RebuildBloom is the one and only rebuild.
	require.Equal(t, "nextIdle", p.Cfg.Eliminations.RebuildOnStale,
		"X2 requires eliminations.rebuildOnStale = nextIdle")

	// Real store, real DAG, real ledger over a real bloom, real observer over the SAME store and
	// graph.
	s := p.Store(t)
	g, err := dag.Open(p.Root, p.Cfg, p.Log)
	require.NoError(t, err)

	// The ledger's filter is a real sketch.NewBloom(cfg.Sketches.Bloom.Capacity,
	// cfg.Sketches.Bloom.FPRate): that is byte-for-byte what Open's newConfiguredBloom builds when
	// b is nil. nil is passed rather than the pre-built value because handing Open an already-
	// sized filter makes it ADOPT the filter without ever persisting sketches/tried.bloom — no
	// daemon has loaded one in wave 2 — and step 7's "exactly one tried.bloom.<seq>.bak" bullet
	// requires the initial on-disk generation that only Open's own rebuild-at-open writes.
	require.Positive(t, p.Cfg.Sketches.Bloom.Capacity)
	require.Positive(t, p.Cfg.Sketches.Bloom.FPRate)
	led, err := negknow.Open(p.Root, p.Cfg, nil, negknow.Deps{
		Store:   s,
		Graph:   g,
		Session: x2Session,
		Log:     p.Log,
		Clock:   p.Clock,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = led.Close() })
	m, ok := led.(negknow.Maintainer)
	require.True(t, ok, "negknow.Open must return a value satisfying negknow.Maintainer")

	obsv, err := observer.New(observer.Options{
		ProjectRoot: p.Root,
		Cfg:         p.Cfg,
		Store:       s,
		Graph:       g,
		Log:         p.Log,
		Clock:       p.Clock,
	})
	require.NoError(t, err)

	composeKey := paths.Key("docker-compose.yml")
	lockKey := paths.Key("package-lock.json")

	// ── step 1: the OBSERVER writes the file versions — a Read of each dependency ──
	_, err = obsv.OnToolUse(ctx, x2ToolEvent(t, "toolu_x2_001", "Read", "docker-compose.yml", composeV1))
	require.NoError(t, err)
	p.Clock.Advance(time.Second)
	_, err = obsv.OnToolUse(ctx, x2ToolEvent(t, "toolu_x2_002", "Read", "package-lock.json", lockJSONV1))
	require.NoError(t, err)

	composeHist, err := s.FileHistory(ctx, "docker-compose.yml")
	require.NoError(t, err, "the observer must have appended a file version for docker-compose.yml")
	require.Len(t, composeHist, 1)
	lockHist, err := s.FileHistory(ctx, "package-lock.json")
	require.NoError(t, err, "the observer must have appended a file version for package-lock.json")
	require.Len(t, lockHist, 1)

	// ── step 2: IngestMCP — depends_on resolved from the roots the OBSERVER wrote ──
	rec, warns, err := m.IngestMCP(ctx, negknow.MCPArgs{
		Target:    x2Target,
		Approach:  x2Approach,
		Reason:    x2Reason,
		Scope:     "project",
		DependsOn: []string{"docker-compose.yml", "package-lock.json"},
	})
	require.NoError(t, err)
	require.Empty(t, warns, "both dependency paths have an observer-written store version, so nothing should be warned about")
	require.NotEmpty(t, rec.ID)

	// Exactly two core.Deps, sorted by path, whose hashes equal the roots the observer wrote. If
	// they differ, the observer and the ledger disagree about paths.Key normalization — the bug
	// this test exists to find.
	require.Len(t, rec.DependsOn, 2)
	require.Equal(t, composeKey, rec.DependsOn[0].Path)
	require.Equal(t, lockKey, rec.DependsOn[1].Path)
	require.Less(t, rec.DependsOn[0].Path, rec.DependsOn[1].Path, "depends_on must be sorted by path")
	require.Equal(t, composeHist[0].Root, rec.DependsOn[0].Hash,
		"the docker-compose.yml dep hash must be the root the OBSERVER wrote (paths.Key normalization must agree)")
	require.Equal(t, lockHist[0].Root, rec.DependsOn[1].Hash,
		"the package-lock.json dep hash must be the root the OBSERVER wrote (paths.Key normalization must agree)")

	// ── step 3: Query through the synonym phrasing → active ──
	ans, err := led.Query(ctx, x2Target, x2Synonym, negknow.ScopeProject)
	require.NoError(t, err)
	require.Equal(t, negknow.AnswerActive, ans.State,
		"the synonym phrasing must resolve through ApproachClass to the recorded elimination")
	require.False(t, ans.BloomOnly)
	require.NotNil(t, ans.Record)
	require.Equal(t, x2Reason, ans.Record.Reason, "the reason must be returned verbatim")
	require.Equal(t, "", ans.Note)

	// ── step 4: the OBSERVER sees docker-compose.yml v2 — a Write with different bytes ──
	p.Clock.Advance(time.Hour)
	_, err = obsv.OnToolUse(ctx, x2ToolEvent(t, "toolu_x2_003", "Write", "docker-compose.yml", composeV2))
	require.NoError(t, err)

	composeHist2, err := s.FileHistory(ctx, "docker-compose.yml")
	require.NoError(t, err)
	require.Len(t, composeHist2, 2, "the observer must have appended a SECOND version of docker-compose.yml")
	require.NotEqual(t, composeHist2[0].Root, composeHist2[1].Root, "fixture sanity: v2 must hash differently")

	// ── step 5: RefreshStaleness — exactly one flip, exactly one ChangedSince call ──
	cs := &x2CountingStore{Store: s}
	flipped, err := led.RefreshStaleness(ctx, cs)
	require.NoError(t, err)
	require.Equal(t, []string{rec.ID}, flipped, "exactly one ID must be returned")
	require.Equal(t, 1, cs.changedSinceCalls, "RefreshStaleness must make exactly one store.ChangedSince call")

	stale, err := led.Get(ctx, rec.ID)
	require.NoError(t, err)
	require.NotEmpty(t, stale.StaleBecause)
	require.Contains(t, stale.StaleBecause[0], composeKey,
		"StaleBecause[0] must name the changed dependency")
	require.Contains(t, stale.StaleBecause[0], fmt.Sprintf("sha256:%s", composeHist[0].Root.Short()),
		"StaleBecause[0] must carry the PRIOR hash — the value the record was recorded against")

	// ── step 6: the same query is now stale — never active ──
	ans, err = led.Query(ctx, x2Target, x2Synonym, negknow.ScopeProject)
	require.NoError(t, err)
	require.Equal(t, negknow.AnswerStale, ans.State,
		"zero stale-block incidents means this answer is stale, never active")
	require.Equal(t, negknow.StaleNote, ans.Note)
	require.Equal(t, x2StaleNote, ans.Note, "the note must be the §8.3 literal, em dash included")
	require.NotNil(t, ans.Record, "the stale record must still be returned")
	require.Equal(t, rec.ID, ans.Record.ID)

	// ── step 7: RebuildBloom — tried.bloom replaced, one .bak, and the stale record's keys LEAVE ──
	//
	// Controller ruling (V3-VERIFY §8.1 case d): the X2 row's original step-7/8 bullets contradicted
	// the plan's own I8/I12 rows, §13 invariant 3 (tried.bloom is a cache rebuilt from ACTIVE
	// records only) and the already-merged TestE2E_EliminationLifecycle step 4. The record is
	// counted in Health but its keys must vanish from the rebuilt filter.
	require.NotNil(t, x2BloomBytes(t, p.Root), "negknow.Open must have persisted an initial tried.bloom")
	require.Empty(t, bloomBackupNames(t, p.Root), "no backup generation may exist before the explicit rebuild")

	// Health BEFORE the rebuild: the one record exists and is stale, and its keys are still on the
	// filter — FillRatio in (0, 0.5] (a healthy, non-empty filter never crosses the §11.4 resize
	// threshold in a one-record fixture).
	healthBefore := led.Health()
	require.Equal(t, 1, healthBefore.Records)
	require.Equal(t, 0, healthBefore.Active)
	require.Equal(t, 1, healthBefore.Stale)
	require.Greater(t, healthBefore.FillRatio, 0.0, "pre-rebuild FillRatio must lie in (0, 0.5]")
	require.LessOrEqual(t, healthBefore.FillRatio, 0.5, "pre-rebuild FillRatio must lie in (0, 0.5]")

	_, health, err := led.RebuildBloom(ctx)
	require.NoError(t, err)

	// tried.bloom was REPLACED: the previous generation now survives as exactly one
	// tried.bloom.<seq>.bak, which is §3.3's rename-and-keep-one-generation swap.
	require.NotNil(t, x2BloomBytes(t, p.Root))
	baks := bloomBackupNames(t, p.Root)
	require.Len(t, baks, 1, "exactly one tried.bloom.<seq>.bak generation must survive: %v", baks)

	// I8/I12 and §13 invariant 3: RebuildBloom draws from ACTIVE records only, and none remain
	// active — the rebuilt filter is empty, exactly as TestE2E_EliminationLifecycle step 4 already
	// pins at the unit-composition level.
	require.Zero(t, health.FillRatio,
		"post-rebuild FillRatio must be 0: the only record is stale, and I8/I12 rebuild from active records only")

	// ── step 8: after the rebuild the stale record's keys are gone from the cache → absent ──
	//
	// Query gates on bloom.Test first (I8/I12; TestE2E_EliminationLifecycle step 4): once
	// tried.bloom is rebuilt from active records only, the stale record's keys leave the cache and
	// Query can no longer route to it, so AnswerStale is unreachable here. AnswerActive would be
	// the §12 stale-block incident this test exists to prevent. The structured record — read
	// directly below — remains the source of truth (§13 invariant 3: the bloom is a cache, never
	// the source of truth).
	ans, err = led.Query(ctx, x2Target, x2Synonym, negknow.ScopeProject)
	require.NoError(t, err)
	require.Equal(t, negknow.AnswerAbsent, ans.State,
		"post-rebuild the answer is absent (I8/I12, TestE2E_EliminationLifecycle step 4): the stale record's keys left the rebuilt cache")
	require.False(t, ans.BloomOnly, "a clean bloom miss is not a BloomOnly hit")

	rawRec, err := led.Get(ctx, rec.ID)
	require.NoError(t, err)
	require.Equal(t, negknow.StatusStale, rawRec.Status,
		"the structured record remains stale and readable even once the bloom no longer flags it")

	// ── append-only holds; nothing was written outside .qompack/ ──
	p.AssertAppendOnly(t)
	assertNoStrayFiles(t, p.Root, before, "src/auth.ts", "docker-compose.yml", "package-lock.json")
}
