package e2e

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
)

// This file is SP-09's Commit 7 end-to-end slice: the elimination lifecycle exercised against a
// real store.Store, a real dag.Graph and the real negknow.Ledger in the test/e2e composition root,
// plus the §12.3 bloom-corruption degradation row. Every other negknow test either runs inside the
// package against fakes (internal/negknow/*_test.go) or through negknowtest's restricted seam; this
// file is the one place the whole stack is wired together the way a real qompack session wires it.

// negknowE2ESession is the fixed session identity both tests open every ledger under. Reopening a
// ledger with the SAME session id is what durability and scope visibility across a close/reopen
// cycle actually mean — a different session id per Open would make every session-scoped assertion
// meaningless.
const negknowE2ESession = core.SessionID("sess_e2e_negknow")

// negknow E2E fixture content. authTSV1 and lockJSONV1 never change; composeV1/composeV2 are the
// dependency whose content change is what flips the elimination stale in
// TestE2E_EliminationLifecycle step 3.
const (
	authTSV1 = "export async function refreshToken(pool: Pool): Promise<Token> {\n" +
		"  return pool.query(REFRESH_SQL);\n}\n"
	lockJSONV1 = `{"name":"demo","lockfileVersion":3,"packages":{"pgbouncer-shim":{"version":"1.18.0"}}}` + "\n"
	composeV1  = "services:\n  pgbouncer:\n    image: pgbouncer:1.18\n    environment:\n      POOL_TIMEOUT: 30\n"
	composeV2  = "services:\n  pgbouncer:\n    image: pgbouncer:1.19\n    environment:\n      POOL_TIMEOUT: 60\n"
)

// negknowTarget and negknowApproach are the elimination the lifecycle test records: the plan's own
// worked example (Qompack.md §8.3).
const (
	negknowTarget   = "src/auth.ts:refreshToken"
	negknowApproach = "widen pool timeout"
	negknowReason   = "pgbouncer 1.18 ignores statement_timeout in transaction pooling mode"
)

// openNegknowLedger opens a real negknow.Ledger over p, wired to s and g, and returns it already
// type-asserted to negknow.Maintainer — the surface IngestMCP is reached through. The Close is
// registered with t.Cleanup: Windows refuses to unlink a file with an open handle, so a ledger left
// open makes t.TempDir()'s own cleanup fail with a message that says nothing about the ledger.
func openNegknowLedger(t *testing.T, p *testutil.Project, s store.Store, g dag.Graph) (negknow.Ledger, negknow.Maintainer) {
	t.Helper()
	led, err := negknow.Open(p.Root, p.Cfg, nil, negknow.Deps{
		Store:   s,
		Graph:   g,
		Session: negknowE2ESession,
		Log:     p.Log,
		Clock:   p.Clock,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = led.Close() })

	m, ok := led.(negknow.Maintainer)
	require.True(t, ok, "negknow.Open must return a value satisfying negknow.Maintainer")
	return led, m
}

// putFileVersion is store.PutBytes plus store.AppendFileVersion for one project-relative path — the
// in-process stand-in for what the observer's PostToolUse hook does in production. It returns the
// resulting root, which a caller compares against a store.ChangedSince result.
func putFileVersion(t *testing.T, ctx context.Context, s store.Store, clk *testutil.FakeClock, turn core.TurnIndex, path, content string) core.Hash {
	t.Helper()
	res, err := s.PutBytes(ctx, []byte(content), store.PutOptions{Tool: "FileRead", Path: path})
	require.NoError(t, err)
	require.NoError(t, s.AppendFileVersion(ctx, path, store.FileVersion{
		TS:    core.UnixMilli(clk.Now().UnixMilli()),
		Root:  res.Root.Hash,
		Turn:  turn,
		Bytes: res.Root.RawBytes,
	}))
	return res.Root.Hash
}

// snapshotProjectFiles walks root and returns every regular file under it as a slash-separated path
// relative to root. It is the "before" half of the negknow_test.go "no file was written outside
// .qompack/" check: a listing taken right after testutil.NewProject, before this test's own source
// files or any .qompack write exists.
func snapshotProjectFiles(t *testing.T, root string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	longRoot := paths.Long(root)
	err := filepath.WalkDir(longRoot, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(longRoot, p)
		if relErr != nil {
			return relErr
		}
		out[filepath.ToSlash(rel)] = true
		return nil
	})
	require.NoError(t, err)
	return out
}

// assertNoStrayFiles asserts that every file under p.Root that was not present in before is either
// under .qompack/ or named in allowedRootFiles — the project source files this test itself wrote.
// Anything else is a write §7.4's layout does not sanction landing outside the .qompack tree.
func assertNoStrayFiles(t *testing.T, root string, before map[string]bool, allowedRootFiles ...string) {
	t.Helper()
	allowed := make(map[string]bool, len(allowedRootFiles))
	for _, f := range allowedRootFiles {
		allowed[f] = true
	}

	after := snapshotProjectFiles(t, root)
	for f := range after {
		if before[f] {
			continue
		}
		if strings.HasPrefix(f, ".qompack/") {
			continue
		}
		require.True(t, allowed[f], "unexpected file written outside .qompack/: %s", f)
	}
}

// bloomBackupNames returns the base names of every sketches/tried.bloom.<seq>.bak file surviving
// under root right now.
func bloomBackupNames(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(paths.Long(paths.Of(root).Sketches))
	require.NoError(t, err)
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), "tried.bloom.") && strings.HasSuffix(e.Name(), ".bak") {
			out = append(out, e.Name())
		}
	}
	return out
}

// loudLines returns every non-empty line of <root>/.qompack/logs/LOUD.log. logging.Logger.Loud is
// the only method that writes to LOUD.log at all (Debug/Info/Warn/Error go to the day log only), so
// a plain line count is already an exact count of Loud events — no message-text filter is needed. A
// project that has never Loud-logged has no LOUD.log at all, which is not itself a failure; the
// caller decides what count it wanted.
func loudLines(t *testing.T, root string) []string {
	t.Helper()
	p := filepath.Join(paths.Of(root).Logs, "LOUD.log")
	b, err := os.ReadFile(paths.Long(p))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)

	var lines []string
	for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// corruptBloomFile flips one byte in the middle of <root>/.qompack/sketches/tried.bloom's body,
// leaving the 4-byte "QPKS" magic at the front untouched. The flip lands well past the 32-byte fixed
// header for any real filter (Appendix A's default sizes to kilobytes), so it is the CRC32C check
// that fails on the next load, not the magic check — sketch.LoadWithLog then reports sketch.ErrCorrupt.
func corruptBloomFile(t *testing.T, root string) {
	t.Helper()
	p := filepath.Join(paths.Of(root).Sketches, "tried.bloom")
	orig, err := os.ReadFile(paths.Long(p))
	require.NoError(t, err)
	require.Greater(t, len(orig), 64, "fixture sanity: tried.bloom must be large enough that its middle byte falls inside the body")

	corrupted := append([]byte(nil), orig...)
	mid := len(corrupted) / 2
	corrupted[mid] ^= 0xFF
	require.NoError(t, os.WriteFile(paths.Long(p), corrupted, 0o600))

	require.Equal(t, orig[:4], corrupted[:4], "the QPKS magic must survive the flip untouched")
	require.NotEqual(t, orig, corrupted, "fixture sanity: the flip must actually change the file")
}

// TestE2E_EliminationLifecycle drives one elimination through every state §8.3 defines for it:
// recorded and active, durable across a ledger close/reopen, flipped stale when a dependency's
// content changes, and rebuilt out of tried.bloom once nothing active still needs it — against a
// real store.Store, a real dag.Graph and the real negknow.Ledger (plan Commit 7).
func TestE2E_EliminationLifecycle(t *testing.T) {
	ctx := context.Background()
	p := testutil.NewProject(t)
	before := snapshotProjectFiles(t, p.Root)

	// The three project source files the plan names. They are written AFTER the "before" snapshot
	// so the final "no file written outside .qompack/" check has to allow-list them explicitly,
	// exactly as negknow_test.go's brief describes.
	p.WithFiles(t, map[string]string{
		"src/auth.ts":        authTSV1,
		"docker-compose.yml": composeV1,
		"package-lock.json":  lockJSONV1,
	})

	s := p.Store(t)
	g, err := dag.Open(p.Root, p.Cfg, p.Log)
	require.NoError(t, err)

	// Seed the store's file-version history for the two dependency files BEFORE the elimination is
	// recorded, exactly as the observer would have from earlier PostToolUse hooks: resolveDeps can
	// only turn a dependency path into a staleness baseline when the store already knows a version
	// of it.
	putFileVersion(t, ctx, s, p.Clock, 0, "docker-compose.yml", composeV1)
	putFileVersion(t, ctx, s, p.Clock, 0, "package-lock.json", lockJSONV1)

	led, m := openNegknowLedger(t, p, s, g)

	// ── step 1: IngestMCP with deps on both config files → AnswerActive ──
	rec, warns, err := m.IngestMCP(ctx, negknow.MCPArgs{
		Target:    negknowTarget,
		Approach:  negknowApproach,
		Reason:    negknowReason,
		DependsOn: []string{"docker-compose.yml", "package-lock.json"},
	})
	require.NoError(t, err)
	require.Empty(t, warns, "both dependencies have a known store version, so nothing should be warned about")
	require.NotEmpty(t, rec.ID)
	require.Len(t, rec.DependsOn, 2, "the record must carry a staleness baseline for both config files")

	ans, err := led.Query(ctx, negknowTarget, negknowApproach, negknow.ScopeSession)
	require.NoError(t, err)
	require.Equal(t, negknow.AnswerActive, ans.State)
	require.NotNil(t, ans.Record)
	require.Equal(t, rec.ID, ans.Record.ID)

	// ── step 2: close and reopen the ledger from disk → still AnswerActive ──
	require.NoError(t, led.Close())
	led, _ = openNegknowLedger(t, p, s, g)

	ans, err = led.Query(ctx, negknowTarget, negknowApproach, negknow.ScopeSession)
	require.NoError(t, err)
	require.Equal(t, negknow.AnswerActive, ans.State, "the elimination must survive a ledger close/reopen")
	require.NotNil(t, ans.Record)
	require.Equal(t, rec.ID, ans.Record.ID)

	// ── step 3: rewrite docker-compose.yml, re-ingest it, RefreshStaleness → 1 flip, AnswerStale ──
	p.Clock.Advance(time.Hour)
	putFileVersion(t, ctx, s, p.Clock, 1, "docker-compose.yml", composeV2)

	flipped, err := led.RefreshStaleness(ctx, s)
	require.NoError(t, err)
	require.Equal(t, []string{rec.ID}, flipped, "exactly the one record depending on docker-compose.yml must flip")

	ans, err = led.Query(ctx, negknowTarget, negknowApproach, negknow.ScopeSession)
	require.NoError(t, err)
	require.Equal(t, negknow.AnswerStale, ans.State)
	require.Equal(t, negknow.StaleNote, ans.Note)
	require.NotNil(t, ans.Record)
	require.Equal(t, rec.ID, ans.Record.ID)

	// ── step 4: RebuildBloom → tried.bloom shrinks to nothing; still AnswerStale; Health flips ──
	healthBefore := led.Health()
	require.Positive(t, healthBefore.FillRatio, "fixture sanity: the still-active record's keys must still be in the on-disk filter before the rebuild")

	bloom, health, err := led.RebuildBloom(ctx)
	require.NoError(t, err)
	require.Zero(t, bloom.Count(), "RebuildBloom draws from active records only, and none remain active")
	require.Less(t, health.FillRatio, healthBefore.FillRatio, "tried.bloom must shrink once its only record goes stale")
	require.Equal(t, 0, health.Active)
	require.Equal(t, 1, health.Stale)

	ans, err = led.Query(ctx, negknowTarget, negknowApproach, negknow.ScopeSession)
	require.NoError(t, err)
	require.Equal(t, negknow.AnswerAbsent, ans.State, "Query gates on bloom.Test first (TestRebuildBloom_ActiveOnly): "+
		"once tried.bloom is rebuilt from active records only, a stale record's key is gone and Query can no "+
		"longer route to it — the record itself, read directly, still says stale")

	rawRec, err := led.Get(ctx, rec.ID)
	require.NoError(t, err)
	require.Equal(t, negknow.StatusStale, rawRec.Status, "the structured record remains the source of truth even once the bloom no longer flags it")

	// ── step 5: append-only holds; exactly one backup generation; nothing written outside .qompack/ ──
	p.AssertAppendOnly(t)

	baks := bloomBackupNames(t, p.Root)
	require.Len(t, baks, 1, "exactly one tried.bloom.<seq>.bak generation must survive: %v", baks)

	assertNoStrayFiles(t, p.Root, before, "src/auth.ts", "docker-compose.yml", "package-lock.json")
}

// TestE2E_BloomCorruptionRecovery is 00-ARCHITECTURE.md §12.3's "bloom load fails" degradation row,
// end to end: a bit-flipped tried.bloom must not cost a single already_tried answer, and the
// corruption must be reported loudly exactly once.
func TestE2E_BloomCorruptionRecovery(t *testing.T) {
	ctx := context.Background()
	p := testutil.NewProject(t)

	s := p.Store(t)
	g, err := dag.Open(p.Root, p.Cfg, p.Log)
	require.NoError(t, err)

	led, m := openNegknowLedger(t, p, s, g)

	rec, warns, err := m.IngestMCP(ctx, negknow.MCPArgs{
		Target:   negknowTarget,
		Approach: negknowApproach,
		Reason:   negknowReason,
	})
	require.NoError(t, err)
	require.Empty(t, warns)

	// Force the elimination's keys onto the ON-DISK filter before it gets corrupted: Record only
	// updates the in-memory bloom (RebuildOnStale defaults to "nextIdle"), so without this the file
	// on disk would still be the empty one Open wrote before any record existed, and corrupting it
	// would prove nothing about recovering a real filter.
	_, _, err = led.RebuildBloom(ctx)
	require.NoError(t, err)

	require.NoError(t, led.Close())
	corruptBloomFile(t, p.Root)

	require.Empty(t, loudLines(t, p.Root), "fixture sanity: nothing should have been Loud-logged before the corrupt file is ever loaded")

	led2, _ := openNegknowLedger(t, p, s, g)

	ans, err := led2.Query(ctx, negknowTarget, negknowApproach, negknow.ScopeSession)
	require.NoError(t, err)
	require.Equal(t, negknow.AnswerActive, ans.State, "the ledger must rebuild tried.bloom from records/eliminations.jsonl and answer correctly")
	require.NotNil(t, ans.Record)
	require.Equal(t, rec.ID, ans.Record.ID)

	// logging.Logger.Loud is the only path that writes to LOUD.log, so exactly one Loud call must
	// have fired for this single corrupt-load event (internal/sketch's LoadWithLog, which negknow's
	// acquireBloom relies on rather than logging a second line of its own — see ledger.go's
	// acquireBloom comment: "LoadWithLog has already written the Loud line for the corrupt case, so
	// this branch writes no second one").
	lines := loudLines(t, p.Root)
	require.Len(t, lines, 1, "LOUD.log must contain exactly one line for this single corruption event:\n%s", strings.Join(lines, "\n"))
	require.Contains(t, lines[0], "tried.bloom", "the one Loud line must be reporting the corrupt tried.bloom load")
}
