package analyzer_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/analyzer"
	"github.com/qompack/qompack/internal/analyzer/analyzertest"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

// The counterexample fixtures of contract §8, which back gate M5-G15-B. Each directory holds a
// provenance.json stating what the fixture demonstrates, which qualified claim it supports and
// which over-claim it exists to prevent; these constants are the only place a test names the path.
const (
	diagnosticsDir  = "testdata/diagnostics"
	fixtureNearDup  = "minhash-similarity-is-not-equivalence"
	fixtureSuperse  = "supersession-is-exact"
	fixtureRelation = "shared-file-edge-is-not-relevance"
)

// The session ids the diagnostic fixtures are recorded under. Two sessions, always, because half
// the properties under test are about a report NOT leaking across the boundary.
const (
	diagSession  = core.SessionID("sess_sp15c_diagnostics")
	diagOtherSes = core.SessionID("sess_sp15c_other")
	diagEmptySes = core.SessionID("sess_sp15c_empty")
)

// diagEpoch is the base timestamp the fixture records are spaced out from. Fixed rather than
// time.Now, so a failure reproduces.
const diagEpoch = core.UnixMilli(1_700_000_000_000)

// diagToolUse is one tool call to record into a fixture store.
type diagToolUse struct {
	ID      core.ToolUseID
	Session core.SessionID
	Turn    core.TurnIndex
	TS      core.UnixMilli
	Tool    string
	Path    string
	Content []byte
}

// diagStore opens a real FSStore under t's temp directory.
//
// It is a real store rather than a double on purpose: the properties under test here — that
// supersession is exact, that a near-duplicate is only a candidate, that the scan mutates nothing —
// are properties of what the tool_use index actually holds, and a hand-written double would let
// this file assert them against its own assumptions instead.
func diagStore(t *testing.T) store.Store {
	t.Helper()
	s, err := store.Open(t.TempDir(), config.Defaults(), store.Deps{Log: logging.Nop()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// diagRecord stores one tool call's content and indexes it, returning the record as written.
func diagRecord(t *testing.T, s store.Store, tu diagToolUse) store.ToolUseRecord {
	t.Helper()
	ctx := context.Background()

	res, err := s.PutBytes(ctx, tu.Content, store.PutOptions{Tool: tu.Tool, Path: tu.Path})
	require.NoError(t, err)

	rec := store.ToolUseRecord{
		ID:        tu.ID,
		Session:   tu.Session,
		Turn:      tu.Turn,
		TS:        tu.TS,
		Tool:      tu.Tool,
		Root:      res.Root.Hash,
		Path:      tu.Path,
		Bytes:     int64(len(tu.Content)),
		Tokens:    res.Root.Tokens,
		Signature: res.Signature,
	}
	require.NoError(t, s.RecordToolUse(ctx, rec))
	return rec
}

// diagFixture reads one counterexample fixture file.
func diagFixture(t *testing.T, fixture, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(diagnosticsDir, fixture, name))
	require.NoError(t, err, "fixture %s/%s must exist: it is the evidence for M5-G15-B", fixture, name)
	require.NotEmpty(t, b)
	return b
}

// diagDetect runs the detector under test against a store, at the shipped configuration.
func diagDetect(t *testing.T, s store.Store, sess core.SessionID) analyzer.RedundancyReport {
	t.Helper()
	got, err := analyzer.DetectRedundancy(context.Background(), s, sess)
	require.NoError(t, err)
	return got
}

// ── the conformance suite, now against a REAL store ────────────────────────────────────────────

// TestAnalyzerConformance_RedundancyAgainstARealStore runs analyzertest's RunRedundancySuite
// against the landed detector and a store that actually holds tool uses.
//
// analyzertest/suite_test.go already runs the same suite against a nil store, which proves the
// no-store path. This run is the one that exercises the behaviour block's assertions over real
// records — and it is the run that flips those assertions on: they are guarded by a Rule W-1 probe
// that skips them for as long as Detect reports core.ErrNotImplemented, and no edit to the suite
// is permitted to make them pass (contract §9).
func TestAnalyzerConformance_RedundancyAgainstARealStore(t *testing.T) {
	analyzertest.RunRedundancySuite(t, "analyzer.DetectRedundancy", func(t *testing.T) analyzertest.RedundancyFixture {
		t.Helper()
		s := diagStore(t)
		diagRecordSupersessionChain(t, s)
		return analyzertest.RedundancyFixture{
			Detect: func(ctx context.Context, sess core.SessionID) (analyzer.RedundancyReport, error) {
				return analyzer.DetectRedundancy(ctx, s, sess)
			},
			Session:      diagSession,
			EmptySession: diagEmptySes,
		}
	})
}

// ── the exact claim, checked exactly (fixture supersession-is-exact) ────────────────────────────

// diagRecordSupersessionChain writes the three-read chain of the supersession-is-exact fixture and
// returns the ids in the order they were recorded.
func diagRecordSupersessionChain(t *testing.T, s store.Store) []core.ToolUseID {
	t.Helper()
	const path = "internal/auth/session.go"
	ids := []core.ToolUseID{"toolu_sess_read_1", "toolu_sess_read_2", "toolu_sess_read_3"}
	files := []string{"session_go_read_1.txt", "session_go_read_2.txt", "session_go_read_3.txt"}

	for i, id := range ids {
		diagRecord(t, s, diagToolUse{
			ID: id, Session: diagSession, Turn: core.TurnIndex(i + 1),
			TS: diagEpoch + core.UnixMilli(i+1)*1000, Tool: "Read", Path: path,
			Content: diagFixture(t, fixtureSuperse, files[i]),
		})
	}
	return ids
}

// TestDetectRedundancy_SupersessionChainIsExact is the M5-G15-B counterpart of the two approximate
// fixtures: the one claim in this file that IS exact, asserted exactly.
//
// Three reads of one normalized path. The first two were replaced by a later read of the same path
// and the third was not, so the report names exactly those two — element for element, not as a
// count and not as a subset. An exact claim is one that can be checked exactly, and a test that
// only asserted "at least the first is listed" would pass just as happily against an estimator.
func TestDetectRedundancy_SupersessionChainIsExact(t *testing.T) {
	s := diagStore(t)
	ids := diagRecordSupersessionChain(t, s)

	got := diagDetect(t, s, diagSession)

	require.Equal(t, []core.ToolUseID{ids[0], ids[1]}, got.Superseded,
		"a later read of the same normalized path replaced the first two, and only those two")
	require.NotContains(t, got.Superseded, ids[2], "the final read of a path is not superseded by anything")
}

// TestDetectRedundancy_SupersessionStopsAtTheSessionBoundary pins the scope of the exact claim. A
// later read of the same path in ANOTHER session is not a fact about this session's records, and
// reporting it would make the "session" argument decorative.
func TestDetectRedundancy_SupersessionStopsAtTheSessionBoundary(t *testing.T) {
	s := diagStore(t)
	const path = "internal/auth/session.go"

	mine := diagRecord(t, s, diagToolUse{
		ID: "toolu_mine", Session: diagSession, Turn: 1, TS: diagEpoch + 1000,
		Tool: "Read", Path: path, Content: diagFixture(t, fixtureSuperse, "session_go_read_1.txt"),
	})
	diagRecord(t, s, diagToolUse{
		ID: "toolu_theirs", Session: diagOtherSes, Turn: 2, TS: diagEpoch + 2000,
		Tool: "Read", Path: path, Content: diagFixture(t, fixtureSuperse, "session_go_read_3.txt"),
	})

	got := diagDetect(t, s, diagSession)
	require.Empty(t, got.Superseded,
		"%s was replaced only by a record of another session, which this session's report may not claim",
		string(mine.ID))
}

// TestDetectRedundancy_PathlessResultsAreNeverSuperseded is the exclusion that keeps the exact
// claim exact. Supersession is a relation between reads OF SOMETHING; two Bash results that touched
// no path share no path on which one could have replaced the other, and grouping them under the
// empty key would report every one but the last as superseded on evidence nothing compared.
func TestDetectRedundancy_PathlessResultsAreNeverSuperseded(t *testing.T) {
	s := diagStore(t)
	for i, id := range []core.ToolUseID{"toolu_bash_1", "toolu_bash_2", "toolu_bash_3"} {
		diagRecord(t, s, diagToolUse{
			ID: id, Session: diagSession, Turn: core.TurnIndex(i + 1),
			TS: diagEpoch + core.UnixMilli(i+1)*1000, Tool: "Bash",
			Content: []byte("go test ./... exited 0 after " + string(rune('a'+i)) + " seconds"),
		})
	}

	got := diagDetect(t, s, diagSession)
	require.Empty(t, got.Superseded, "a result with no path cannot be replaced on a path")
}

// TestDetectRedundancy_HonoursTheStoresOwnSupersededStatus is the second exact ground. When the
// observer has already marked a record store.StatusSuperseded, that is a recorded fact and the
// report carries it — including for a record that is the last one this scan can see on its path.
func TestDetectRedundancy_HonoursTheStoresOwnSupersededStatus(t *testing.T) {
	s := diagStore(t)
	ctx := context.Background()

	older := diagRecord(t, s, diagToolUse{
		ID: "toolu_marked_older", Session: diagSession, Turn: 1, TS: diagEpoch + 1000,
		Tool: "Read", Path: "docs/a.md", Content: []byte("the first version of a short note"),
	})
	newer := diagRecord(t, s, diagToolUse{
		ID: "toolu_marked_newer", Session: diagOtherSes, Turn: 2, TS: diagEpoch + 2000,
		Tool: "Read", Path: "docs/a.md", Content: []byte("the second version of a short note"),
	})
	require.NoError(t, s.MarkSuperseded(ctx, older.ID, newer.ID))

	got := diagDetect(t, s, diagSession)
	require.Equal(t, []core.ToolUseID{older.ID}, got.Superseded,
		"the store's own recorded status is exact evidence, whoever wrote it")
}

// ── the approximate claim, qualified (fixture minhash-similarity-is-not-equivalence) ────────────

// TestDetectRedundancy_NearDuplicatesAreCandidatesNotEquivalence is gate M5-G15-B's central
// counterexample, and the reason internal/analyzer/testdata/diagnostics exists at all.
//
// The two fixture files differ in ONE token: a TLS client that skips certificate verification, and
// the same client that does not. MinHash puts them far above the shipped 0.9 threshold, so the
// report lists them as near-duplicate candidates — and the test then asserts, in the same breath,
// that they are NOT the same result:
//
//   - their content roots differ, which is the exact check a caller must run before acting;
//   - the token that differs is present in one fixture and absent from the other.
//
// A future change that lets a caller treat a NearDups entry as proven equivalence has to delete
// these assertions to do it, which is the whole point of committing them.
func TestDetectRedundancy_NearDuplicatesAreCandidatesNotEquivalence(t *testing.T) {
	permissive := diagFixture(t, fixtureNearDup, "tlsclient_permissive.txt")
	hardened := diagFixture(t, fixtureNearDup, "tlsclient_hardened.txt")
	require.NotEqual(t, permissive, hardened, "fixture sanity: the pair must actually differ")

	s := diagStore(t)
	a := diagRecord(t, s, diagToolUse{
		ID: "toolu_tls_permissive", Session: diagSession, Turn: 1, TS: diagEpoch + 1000,
		Tool: "Read", Path: "internal/httpclient/permissive.go", Content: permissive,
	})
	b := diagRecord(t, s, diagToolUse{
		ID: "toolu_tls_hardened", Session: diagSession, Turn: 2, TS: diagEpoch + 2000,
		Tool: "Read", Path: "internal/httpclient/hardened.go", Content: hardened,
	})

	got := diagDetect(t, s, diagSession)

	require.Equal(t, []core.ToolUseID{b.ID}, got.NearDups[a.ID],
		"a one-token edit is a near-duplicate CANDIDATE under the shipped threshold")
	require.Equal(t, []core.ToolUseID{a.ID}, got.NearDups[b.ID],
		"the candidate relation is reported symmetrically")

	// And now the qualification, asserted rather than asserted-about-in-a-comment.
	require.NotEqual(t, a.Root, b.Root,
		"the exact check disagrees with the sketch: these are different results, and root equality "+
			"is the cheap exact verification a caller must run before acting on a NearDups entry")
	require.Contains(t, string(permissive), "AllowUnverifiedTLS = true")
	require.NotContains(t, string(hardened), "AllowUnverifiedTLS = true",
		"the one token that differs is the one that decides whether TLS is verified at all")
}

// TestDetectRedundancy_NoResultIsItsOwnNearDuplicate pins the exclusion that keeps a saving from
// being counted twice. sketch.Signature.IsNearDup(x, x) is true for every real signature, so a scan
// that compared a record with itself would report a duplicate that does not exist.
func TestDetectRedundancy_NoResultIsItsOwnNearDuplicate(t *testing.T) {
	s := diagStore(t)
	diagRecordSupersessionChain(t, s)
	diagRecord(t, s, diagToolUse{
		ID: "toolu_tls_permissive", Session: diagSession, Turn: 9, TS: diagEpoch + 9000,
		Tool: "Read", Path: "internal/httpclient/permissive.go",
		Content: diagFixture(t, fixtureNearDup, "tlsclient_permissive.txt"),
	})

	got := diagDetect(t, s, diagSession)
	for id, dups := range got.NearDups {
		require.NotContains(t, dups, id, "%s is listed as its own near-duplicate", string(id))
	}
}

// TestDetectRedundancy_UnrelatedResultsAreNotNearDuplicates is the other half of the sketch's
// qualification: the estimator is not merely permissive, and two results with nothing in common
// stay out of the report. Without this, "everything is a candidate" would pass the test above.
func TestDetectRedundancy_UnrelatedResultsAreNotNearDuplicates(t *testing.T) {
	s := diagStore(t)
	a := diagRecord(t, s, diagToolUse{
		ID: "toolu_notes", Session: diagSession, Turn: 1, TS: diagEpoch + 1000,
		Tool: "Read", Path: "docs/pooling.md",
		Content: diagFixture(t, fixtureRelation, "changelog_release_notes.txt"),
	})
	b := diagRecord(t, s, diagToolUse{
		ID: "toolu_typo", Session: diagSession, Turn: 2, TS: diagEpoch + 2000,
		Tool: "Read", Path: "docs/typo.md",
		Content: diagFixture(t, fixtureRelation, "changelog_typo_fix.txt"),
	})

	got := diagDetect(t, s, diagSession)
	require.Empty(t, got.NearDups[a.ID])
	require.Empty(t, got.NearDups[b.ID])
}

// ── read-only, determinism and the no-store path ───────────────────────────────────────────────

// TestDetectRedundancy_IsReadOnly asserts the property store.MarkSuperseded's ownership depends on:
// the scan reports, and changes nothing. Two consecutive scans agree, and — checked directly rather
// than inferred — every record's Status is exactly what it was before the first scan ran.
func TestDetectRedundancy_IsReadOnly(t *testing.T) {
	s := diagStore(t)
	ctx := context.Background()
	ids := diagRecordSupersessionChain(t, s)

	before := make(map[core.ToolUseID]store.Supersession, len(ids))
	for _, id := range ids {
		rec, err := s.ToolUse(ctx, id)
		require.NoError(t, err)
		before[id] = rec.Status
	}

	first := diagDetect(t, s, diagSession)
	second := diagDetect(t, s, diagSession)
	require.Equal(t, first, second, "two scans of an unchanged session must agree exactly")

	for _, id := range ids {
		rec, err := s.ToolUse(ctx, id)
		require.NoError(t, err)
		require.Equal(t, before[id], rec.Status,
			"%s changed status: DetectRedundancy reports, and store.MarkSuperseded is the caller's call",
			string(id))
	}
}

// TestDetectRedundancy_AnEmptySessionReportsNothing asserts a fresh session is the normal case
// rather than a missing one.
func TestDetectRedundancy_AnEmptySessionReportsNothing(t *testing.T) {
	s := diagStore(t)
	diagRecordSupersessionChain(t, s)

	got := diagDetect(t, s, diagEmptySes)
	require.Empty(t, got.Superseded)
	require.Empty(t, got.NearDups)
}

// TestDetectRedundancy_ANilStoreReportsNothing pins the composition-root path test/guards and
// analyzertest both construct: no store means nothing was read, which is an empty report and not a
// failure — and not core.ErrDegraded either, since nothing degraded.
func TestDetectRedundancy_ANilStoreReportsNothing(t *testing.T) {
	got, err := analyzer.DetectRedundancy(context.Background(), nil, diagSession)
	require.NoError(t, err)
	require.Empty(t, got.Superseded)
	require.Empty(t, got.NearDups)
}

// TestDetectRedundancy_UsesTheShippedThresholdWhenGivenNoConfig pins what the three-argument entry
// point actually does with the configuration it was never handed: it answers at config.Defaults(),
// identically to the explicit call, and a caller with its own threshold has to say so.
func TestDetectRedundancy_UsesTheShippedThresholdWhenGivenNoConfig(t *testing.T) {
	s := diagStore(t)
	ctx := context.Background()
	diagRecord(t, s, diagToolUse{
		ID: "toolu_tls_permissive", Session: diagSession, Turn: 1, TS: diagEpoch + 1000,
		Tool: "Read", Path: "internal/httpclient/permissive.go",
		Content: diagFixture(t, fixtureNearDup, "tlsclient_permissive.txt"),
	})
	diagRecord(t, s, diagToolUse{
		ID: "toolu_tls_hardened", Session: diagSession, Turn: 2, TS: diagEpoch + 2000,
		Tool: "Read", Path: "internal/httpclient/hardened.go",
		Content: diagFixture(t, fixtureNearDup, "tlsclient_hardened.txt"),
	})

	implicit, err := analyzer.DetectRedundancy(ctx, s, diagSession)
	require.NoError(t, err)
	explicit, err := analyzer.DetectRedundancyWithConfig(ctx, s, diagSession, config.Defaults())
	require.NoError(t, err)
	require.Equal(t, explicit, implicit)

	// A threshold above 1 cannot be reached by any signature, which is what shows the parameter is
	// actually consulted rather than decorative.
	cfg := config.Defaults()
	cfg.Store.Canonicalize.MinHash.NearDupThreshold = 1.5
	strict, err := analyzer.DetectRedundancyWithConfig(ctx, s, diagSession, cfg)
	require.NoError(t, err)
	require.Empty(t, strict.NearDups, "an unreachable threshold admits no candidates")
	require.Equal(t, implicit.Superseded, strict.Superseded,
		"the exact half of the report does not move with a sketch threshold")
}
