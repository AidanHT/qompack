package negknow

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

// This file covers §8.3's explicitly-reported elimination sources — #1 record_eliminated,
// #2 /qompack:pin --eliminated and #4 the user statement — plus the observation ring source #3
// reads. Source #3 itself is detector_test.go.
//
// Every ledger here is opened through openLedger (ledger_test.go), which registers the Close:
// Windows refuses to unlink a file with an open handle, so a ledger left open makes t.TempDir()'s
// own cleanup fail with a message that says nothing about the ledger.

// ── helpers ───────────────────────────────────────────────────────────────────────────────────

// ingestDeps is testDeps plus a store, which every ingest path needs for dependency resolution
// and for minting evidence out of the reason text.
func ingestDeps(sess core.SessionID, m obs.Registry, s store.Store) Deps {
	d := testDeps(sess, m)
	d.Store = s
	return d
}

// storedVersion builds one scripted store.FileVersion whose Root is derived from seed, so a test
// can predict the dep hash resolveDeps will lift out of the history.
func storedVersion(ts int64, seed string) store.FileVersion {
	return store.FileVersion{TS: core.UnixMilli(ts), Root: versionRoot(seed)}
}

// versionRoot is storedVersion's deterministic root for seed.
func versionRoot(seed string) core.Hash { return core.HashBytes("negknow.test.version", []byte(seed)) }

// depPaths projects a dependency list onto its paths, which is what the ORDER assertions are
// about; the hashes are asserted separately where they matter.
func depPaths(deps []Dep) []string {
	out := make([]string, 0, len(deps))
	for _, d := range deps {
		out = append(out, d.Path)
	}
	return out
}

// signalsLineCount returns the number of lines currently in the project's signals log.
func signalsLineCount(t *testing.T, root string) int {
	t.Helper()
	b, err := os.ReadFile(signalsPath(root))
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(t, err)
	if len(b) == 0 {
		return 0
	}
	return len(strings.Split(strings.TrimRight(string(b), "\n"), "\n"))
}

// ── source #1: record_eliminated ──────────────────────────────────────────────────────────────

// TestIngestMCP_Full is the whole of source #1 in one call: an explicit dependency list, an
// explicit scope, and evidence minted from the reason text through the store's redaction choke
// point. The two deps land on the record in ASCENDING PATH order (R13, normalizeRecord), and
// package-lock.json resolves to its GREATEST-TS version, not its first.
func TestIngestMCP_Full(t *testing.T) {
	root, cfg := newProject(t)
	m := newMetrics()
	fs := newFakeStore().
		withHistory(paths.Key("docker-compose.yml"), storedVersion(10, "compose-v1")).
		withHistory(paths.Key("package-lock.json"), storedVersion(20, "lock-v1"), storedVersion(30, "lock-v2"))
	l := openLedger(t, root, cfg, nil, ingestDeps("s1", m, fs))

	const reason = "pgbouncer 1.18 ignores it in transaction mode"
	rec, warns, err := l.IngestMCP(context.Background(), MCPArgs{
		Target:    "src/auth.ts:refreshToken",
		Approach:  "widen pool timeout",
		Reason:    reason,
		Scope:     "project",
		DependsOn: []string{"docker-compose.yml", "package-lock.json"},
	})
	require.NoError(t, err)
	require.Empty(t, warns, "every supplied dependency resolved, so nothing is warned about")

	require.Equal(t, SourceMCP, rec.Source)
	require.Equal(t, ScopeProject, rec.Scope)
	require.Equal(t, StatusActive, rec.Status)
	require.NotEmpty(t, rec.ID)
	require.Equal(t, core.SessionID("s1"), rec.Session)
	require.Equal(t, fakeStoreRoot([]byte(reason)), rec.Evidence,
		"with no evidence supplied, the reason text is stored and its root becomes the evidence")
	require.Equal(t, []Dep{
		{Path: paths.Key("docker-compose.yml"), Hash: versionRoot("compose-v1")},
		{Path: paths.Key("package-lock.json"), Hash: versionRoot("lock-v2")},
	}, rec.DependsOn)
	require.Equal(t, "src/auth.ts:refreshToken", rec.Target)
	require.Equal(t, ApproachClass("widen pool timeout"), rec.Desc.ApproachClass)
}

// TestIngestMCP_AutoDeps pins BOTH orders R13 distinguishes: the record carries its dependencies
// sorted ascending by path, because that is what makes a JSONL line byte-stable, while
// resolveDeps itself returns CANDIDATE order — the target's own path first, then autoDepCandidates
// in the order that file declares them.
func TestIngestMCP_AutoDeps(t *testing.T) {
	root, cfg := newProject(t)
	m := newMetrics()
	fs := newFakeStore().
		withHistory(paths.Key("src/auth.ts"), storedVersion(10, "auth")).
		withHistory(paths.Key("package-lock.json"), storedVersion(11, "lock")).
		withHistory(paths.Key("docker-compose.yml"), storedVersion(12, "compose"))
	l := openLedger(t, root, cfg, nil, ingestDeps("s1", m, fs))

	rec, warns, err := l.IngestMCP(context.Background(), MCPArgs{
		Target:   "src/auth.ts:refreshToken",
		Approach: "widen pool timeout",
		Reason:   "pgbouncer 1.18 ignores it in transaction mode",
	})
	require.NoError(t, err)
	require.Empty(t, warns, "an auto-derived candidate with no stored version is filtered, not warned about")
	require.Equal(t, []string{
		paths.Key("docker-compose.yml"),
		paths.Key("package-lock.json"),
		paths.Key("src/auth.ts"),
	}, depPaths(rec.DependsOn), "the stored record sorts its dependencies ascending by path")

	got, gotWarns := l.resolveDeps(context.Background(), nil, "src/auth.ts:refreshToken")
	require.Empty(t, gotWarns)
	require.Equal(t, []string{
		paths.Key("src/auth.ts"),
		paths.Key("package-lock.json"),
		paths.Key("docker-compose.yml"),
	}, depPaths(got), "resolveDeps returns candidate order: the target first, then autoDepCandidates")
}

// TestIngestMCP_UnknownDepSkipped pins the rule that keeps a staleness baseline honest: a path
// with no known version cannot serve as one, so it is dropped and named rather than recorded with
// a zero hash that would compare equal to nothing.
func TestIngestMCP_UnknownDepSkipped(t *testing.T) {
	root, cfg := newProject(t)
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, ingestDeps("s1", m, newFakeStore()))

	rec, warns, err := l.IngestMCP(context.Background(), MCPArgs{
		Target:    "src/auth.ts:refreshToken",
		Approach:  "widen pool timeout",
		Reason:    "pgbouncer 1.18 ignores it in transaction mode",
		DependsOn: []string{"missing.yml"},
	})
	require.NoError(t, err)
	require.Empty(t, rec.DependsOn)
	require.Equal(t,
		[]string{"no stored version for missing.yml; not used as a staleness dependency"},
		warns)
}

// TestIngestMCP_BadScope pins that an unparseable scope degrades to the configured default with a
// warning rather than failing the call: an MCP tool call that dies takes the elimination with it.
func TestIngestMCP_BadScope(t *testing.T) {
	root, cfg := newProject(t)
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, ingestDeps("s1", m, newFakeStore()))

	rec, warns, err := l.IngestMCP(context.Background(), MCPArgs{
		Target:   "src/auth.ts:refreshToken",
		Approach: "widen pool timeout",
		Reason:   "pgbouncer 1.18 ignores it in transaction mode",
		Scope:    "global",
	})
	require.NoError(t, err)
	require.Equal(t, Scope(l.elim.DefaultScope), rec.Scope)
	require.Len(t, warns, 1)
	require.Contains(t, warns[0], "global")
}

// TestIngestMCP_NoStore_RequireEvidence pins the requireEvidence refusal at the ingest boundary:
// with no store there is nothing to mint evidence from, and nothing may be appended.
func TestIngestMCP_NoStore_RequireEvidence(t *testing.T) {
	root, cfg := newProject(t)
	cfg.Eliminations.RequireEvidence = true
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, testDeps("s1", m))

	_, _, err := l.IngestMCP(context.Background(), MCPArgs{
		Target:   "src/auth.ts:refreshToken",
		Approach: "widen pool timeout",
		Reason:   "pgbouncer 1.18 ignores it in transaction mode",
	})
	require.ErrorIs(t, err, ErrNoEvidence)
	require.Equal(t, 0, l.Health().Records)
	require.Equal(t, 0, logLineCount(t, root))
}

// ── source #2: /qompack:pin --eliminated ──────────────────────────────────────────────────────

// TestIngestPin_EvidenceText pins the one difference between sources #1 and #2: a slash command
// carries evidence as text, so it is parsed rather than minted.
func TestIngestPin_EvidenceText(t *testing.T) {
	root, cfg := newProject(t)
	m := newMetrics()
	fs := newFakeStore()
	l := openLedger(t, root, cfg, nil, ingestDeps("s1", m, fs))

	want := core.HashBytes("negknow.test.pin", []byte("evidence"))
	rec, warns, err := l.IngestPin(context.Background(), PinArgs{
		Target:   "src/auth.ts:refreshToken",
		Approach: "widen pool timeout",
		Reason:   "pgbouncer 1.18 ignores it in transaction mode",
		Evidence: want.String(),
	})
	require.NoError(t, err)
	require.Empty(t, warns)
	require.Equal(t, want, rec.Evidence)
	require.Equal(t, SourceSlashCommand, rec.Source)
	require.Empty(t, fs.putBytes, "a parsed evidence hash mints nothing")
}

// TestIngestPin_BadEvidenceText pins the fall-through: an unparseable hash is a warning, not a
// refusal, and the record still gets real evidence out of the reason text.
func TestIngestPin_BadEvidenceText(t *testing.T) {
	root, cfg := newProject(t)
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, ingestDeps("s1", m, newFakeStore()))

	const reason = "pgbouncer 1.18 ignores it in transaction mode"
	rec, warns, err := l.IngestPin(context.Background(), PinArgs{
		Target:   "src/auth.ts:refreshToken",
		Approach: "widen pool timeout",
		Reason:   reason,
		Evidence: "garbage",
	})
	require.NoError(t, err)
	require.Len(t, warns, 1)
	require.Contains(t, warns[0], "garbage")
	require.Equal(t, fakeStoreRoot([]byte(reason)), rec.Evidence)
	require.Equal(t, SourceSlashCommand, rec.Source)
}

// ── source #4: explicit user statements ───────────────────────────────────────────────────────

// TestIngestUserStatement_Matches pins the whole of source #4's happy path.
func TestIngestUserStatement_Matches(t *testing.T) {
	root, cfg := newProject(t)
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, ingestDeps("s1", m, newFakeStore()))

	const prompt = "That didn't work — the pool is still saturated."
	recs, err := l.IngestUserStatement(context.Background(), UserStatement{
		Prompt:   prompt,
		Turn:     7,
		Path:     "src/db.ts",
		Approach: "widen pool timeout",
	})
	require.NoError(t, err)
	require.Len(t, recs, 1)
	require.Equal(t, SourceUserStatement, recs[0].Source)
	require.Equal(t, "src/db.ts", recs[0].Target)
	require.Equal(t, "widen pool timeout", recs[0].Approach)
	require.True(t, strings.HasPrefix(recs[0].Reason, "user stated: "), "got %q", recs[0].Reason)
	require.Contains(t, recs[0].Reason, "the pool is still saturated")
	require.Equal(t, fakeStoreRoot([]byte(prompt)), recs[0].Evidence,
		"with no PromptRoot supplied, the prompt text is stored and its root becomes the evidence")
	require.Equal(t, int64(0), counterValue(t, m, counterUserStatementUnresolved))
}

// TestIngestUserStatement_ApostropheVariants pins the normalizer: "didn't", "didn’t" and "didnt"
// are one phrase after the apostrophe is stripped, which is the only reason a closed phrase list
// can work at all against text a human typed.
func TestIngestUserStatement_ApostropheVariants(t *testing.T) {
	for _, prompt := range []string{
		"that didnt work",
		"that didn't work",
		"that didn’t work",
	} {
		t.Run(prompt, func(t *testing.T) {
			root, cfg := newProject(t)
			m := newMetrics()
			l := openLedger(t, root, cfg, nil, ingestDeps("s1", m, newFakeStore()))

			recs, err := l.IngestUserStatement(context.Background(), UserStatement{
				Prompt:   prompt,
				Path:     "src/db.ts",
				Approach: "widen pool timeout",
			})
			require.NoError(t, err)
			require.Len(t, recs, 1)
		})
	}
}

// TestIngestUserStatement_NoTarget pins the refusal that keeps source #4 safe: guessing a target
// would manufacture a wrong elimination, which is exactly the stale-block failure §12 rates High.
func TestIngestUserStatement_NoTarget(t *testing.T) {
	root, cfg := newProject(t)
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, ingestDeps("s1", m, newFakeStore()))

	recs, err := l.IngestUserStatement(context.Background(), UserStatement{
		Prompt:   "That didn't work — the pool is still saturated.",
		Approach: "widen pool timeout",
	})
	require.NoError(t, err)
	require.Empty(t, recs)
	require.Equal(t, int64(1), counterValue(t, m, counterUserStatementUnresolved))
	require.Equal(t, 0, l.Health().Records)
}

// TestIngestUserStatement_NoMatch pins that an ordinary prompt moves nothing at all — not even
// the unresolved counter, which is reserved for a phrase that DID match and could not be placed.
func TestIngestUserStatement_NoMatch(t *testing.T) {
	root, cfg := newProject(t)
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, ingestDeps("s1", m, newFakeStore()))

	recs, err := l.IngestUserStatement(context.Background(), UserStatement{
		Prompt:   "looks good, ship it",
		Path:     "src/db.ts",
		Approach: "widen pool timeout",
	})
	require.NoError(t, err)
	require.Empty(t, recs)
	require.Equal(t, int64(0), counterValue(t, m, counterUserStatementUnresolved))
	require.Equal(t, 0, l.Health().Records)
}

// ── the observation ring source #3 reads ──────────────────────────────────────────────────────

// TestObserve_AppendsSignals pins both halves of Observe: the durable JSONL line and the
// in-memory ring the detector actually scans.
func TestObserve_AppendsSignals(t *testing.T) {
	root, cfg := newProject(t)
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, ingestDeps("s1", m, newFakeStore()))

	ctx := context.Background()
	require.NoError(t, l.Observe(ctx, Observation{Turn: 1, Kind: ObsEdit, Path: "src/db.ts", Detail: "widen pool timeout"}))
	require.NoError(t, l.Observe(ctx, Observation{Turn: 2, Kind: ObsTestFail, Path: "src/db.ts"}))
	require.NoError(t, l.Observe(ctx, Observation{Turn: 3, Kind: ObsRevert, Path: "src/db.ts"}))

	require.Equal(t, 3, signalsLineCount(t, root))

	// The line reads like the other JSONL logs beside it rather than like leaked Go field names.
	b, err := os.ReadFile(signalsPath(root))
	require.NoError(t, err)
	first := strings.SplitN(strings.TrimRight(string(b), "\n"), "\n", 2)[0]
	var w signalWire
	require.NoError(t, json.Unmarshal([]byte(first), &w))
	require.Equal(t, signalWire{Turn: 1, Kind: ObsEdit, Path: "src/db.ts", Detail: "widen pool timeout"}, w)

	got := l.Since(0)
	require.Len(t, got, 3)
	require.Equal(t, []core.TurnIndex{1, 2, 3}, []core.TurnIndex{got[0].Turn, got[1].Turn, got[2].Turn})
	require.Equal(t, "widen pool timeout", got[0].Detail)
}

// TestObserve_RingBounded pins the bound: a long session's signal history stays at signalRing
// entries and keeps the MOST RECENT ones, because the detector's window is at the tail.
func TestObserve_RingBounded(t *testing.T) {
	root, cfg := newProject(t)
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, ingestDeps("s1", m, newFakeStore()))

	ctx := context.Background()
	const total = 600
	for i := range total {
		require.NoError(t, l.Observe(ctx, Observation{Turn: core.TurnIndex(i), Kind: ObsEdit, Path: "src/db.ts"}))
	}

	got := l.Since(0)
	require.Len(t, got, signalRing)
	require.Equal(t, core.TurnIndex(total-signalRing), got[0].Turn, "the oldest surviving observation")
	require.Equal(t, core.TurnIndex(total-1), got[len(got)-1].Turn, "the newest observation is kept")
}

// TestObserve_AfterCloseIsRefused pins the closed-ledger contract for the one write path this
// task adds: os.ErrClosed, and nothing appended.
func TestObserve_AfterCloseIsRefused(t *testing.T) {
	root, cfg := newProject(t)
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, ingestDeps("s1", m, newFakeStore()))

	require.NoError(t, l.Close())
	err := l.Observe(context.Background(), Observation{Turn: 1, Kind: ObsEdit, Path: "src/db.ts"})
	require.ErrorIs(t, err, os.ErrClosed)
	require.Equal(t, 0, signalsLineCount(t, root))
	require.Empty(t, l.Since(0))
}

// TestSince_CopiesRingEntries pins that Since hands out copies: a caller that mutates what it was
// given must not be able to rewrite the ledger's own history through the alias.
func TestSince_CopiesRingEntries(t *testing.T) {
	root, cfg := newProject(t)
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, ingestDeps("s1", m, newFakeStore()))

	require.NoError(t, l.Observe(context.Background(), Observation{
		Turn: 1, Kind: ObsEdit, Path: "src/db.ts", Detail: "widen pool timeout",
	}))

	got := l.Since(0)
	require.Len(t, got, 1)
	got[0].Detail = "mutated"

	again := l.Since(0)
	require.Len(t, again, 1)
	require.Equal(t, "widen pool timeout", again[0].Detail)
}

// TestSince_FiltersByTurn pins the source contract the detector's `since` argument rests on.
func TestSince_FiltersByTurn(t *testing.T) {
	root, cfg := newProject(t)
	m := newMetrics()
	l := openLedger(t, root, cfg, nil, ingestDeps("s1", m, newFakeStore()))

	ctx := context.Background()
	for i := range 5 {
		require.NoError(t, l.Observe(ctx, Observation{Turn: core.TurnIndex(i), Kind: ObsEdit, Path: "src/db.ts"}))
	}
	got := l.Since(3)
	require.Len(t, got, 2)
	require.Equal(t, core.TurnIndex(3), got[0].Turn)
	require.Equal(t, core.TurnIndex(4), got[1].Turn)
}

// ── the two assertions the extended surface exists for ────────────────────────────────────────

// TestOpenReturnsMaintainer pins the type assertion SP-11 through SP-14 reach the extended ledger
// surface with. Open returns a Ledger and the concrete type is unexported, so a silent failure
// here would surface in those subplans as a missing feature rather than as a build error.
func TestOpenReturnsMaintainer(t *testing.T) {
	root, cfg := newProject(t)
	led, err := Open(root, cfg, nil, testDeps("s1", newMetrics()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = led.Close() })

	m, ok := led.(Maintainer)
	require.True(t, ok, "the value Open returns must satisfy negknow.Maintainer")
	require.False(t, m.NeedsRebuild(), "a fresh, empty ledger owes no rebuild")
}

// TestOpenReturnsObservationSource pins the assertion NewDetector's caller makes, and the empty
// non-nil slice a fresh ring answers with.
func TestOpenReturnsObservationSource(t *testing.T) {
	root, cfg := newProject(t)
	led, err := Open(root, cfg, nil, testDeps("s1", newMetrics()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = led.Close() })

	src, ok := led.(ObservationSource)
	require.True(t, ok, "the value Open returns must satisfy negknow.ObservationSource")
	got := src.Since(0)
	require.NotNil(t, got, "a fresh ring answers with an empty slice, never nil")
	require.Empty(t, got)
}
