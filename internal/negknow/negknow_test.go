package negknow_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/negknow/negknowtest"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/stretchr/testify/require"
)

// This file is negknow's own external-test surface: the §5.22 conformance suite pointed at the
// real negknow.Open, and the three_way_answer contract fixture's recorder and its consumer.
//
// It is package negknow_test rather than package negknow for one reason (ruling R16):
// internal/testutil imports internal/cli -> internal/daemon -> internal/negknow, so an INTERNAL
// test file cannot reach testutil without an import cycle. An external test package can, because
// nothing imports it. bench_test.go and the rest of this package's tests are internal and build
// their projects with config.Load directly; this file is the one that gets the house fixture.

// conformanceSession is the session every ledger the conformance factory builds records under.
const conformanceSession core.SessionID = "sess-conformance"

// TestLedgerConformance runs the §5.22 conformance suite against the ledger negknow.Open actually
// returns, over a REAL project: testutil.NewProject's .qompack/ layout, its fully loaded Appendix
// C configuration (eliminations.requireEvidence TRUE, sketches.bloom capacity 10 000 / fp 0.01),
// its file-backed logger and its frozen clock.
//
// This is the half of the suite that negknowtest's own suite_test.go cannot cover: that one runs
// over a ZERO config.Config, exercising Open's defaulting path, and the two together are what pin
// that the behaviour block passes both fully configured and unconfigured. Rule W-1's merge blocker
// — no t.Skip anywhere in negknowtest — is satisfied by the runtime stub probe finding a real
// Query here, not by any change to the suite's skip mechanism.
func TestLedgerConformance(t *testing.T) {
	negknowtest.RunLedgerSuite(t, "negknow.Open", func(t *testing.T) negknow.Ledger {
		p := testutil.NewProject(t)
		l, err := negknow.Open(p.Root, p.Cfg, nil, negknow.Deps{
			Session: conformanceSession,
			Clock:   p.Clock,
			Log:     p.Log,
		})
		require.NoError(t, err)
		// A real ledger holds an open append handle on records/eliminations.jsonl. On Windows an
		// open handle makes t.TempDir()'s RemoveAll cleanup fail, with a message that says nothing
		// about the ledger, so every ledger a test opens is closed through t.Cleanup.
		t.Cleanup(func() { _ = l.Close() })
		return l
	})
}

// ── the three_way_answer contract fixture ─────────────────────────────────────────────────────

// fixtureSession is the session testdata/golden/contracts/negknow/input/ledger_seed.jsonl's two
// project-scoped records were recorded under, and the session the fixture's ledger is opened for.
// It is the session id the frozen elimination_record.jsonl fixture already carries, so the two
// SP-09 fixtures describe one coherent project rather than two unrelated ones.
const fixtureSession core.SessionID = "sess_01J8ZQ5R7N3K2M4P6T8V0X2Y4A"

// The three (target, approach) pairs the fixture queries, one per answer state.
//
// absent* is a pair the seed never mentions at all. active* is the seed's first record. stale* is
// the seed's second record, which the seed's own op:"stale" control line flips.
const (
	absentTarget   = "src/queue/worker.ts:drain"
	absentApproach = "shard the queue"

	activeTarget   = "src/db/pool.ts:acquire"
	activeApproach = "widen pool timeout"

	staleTarget   = "src/api/retry.ts:backoff"
	staleApproach = "increase retry count"
)

// threeWayEntry is one Query's answer as the fixture records it: Answer.MCPResult's four rendered
// strings plus the BloomOnly flag MCPResult does not carry (§13 invariant 3 — a membership answer
// is either record-backed or explicitly flagged, and the fixture has to show which).
type threeWayEntry struct {
	State     string `json:"state"`
	Reason    string `json:"reason"`
	Note      string `json:"note"`
	Evidence  string `json:"evidence"`
	BloomOnly bool   `json:"bloom_only"`
}

// threeWayDoc is want/three_way_answer.json: one entry per answer state, in the §8.3 order the
// three-way response is always described in.
type threeWayDoc struct {
	Absent threeWayEntry `json:"absent"`
	Active threeWayEntry `json:"active"`
	Stale  threeWayEntry `json:"stale"`
}

// entryFor renders one Query as a fixture entry.
func entryFor(t *testing.T, l negknow.Ledger, target, approach string) threeWayEntry {
	t.Helper()
	a, err := l.Query(context.Background(), target, approach, negknow.ScopeSession)
	require.NoError(t, err)
	state, reason, note, evidence := a.MCPResult()
	return threeWayEntry{State: state, Reason: reason, Note: note, Evidence: evidence, BloomOnly: a.BloomOnly}
}

// threeWayOverSeed materializes seed into a fresh project and renders the three-way answer
// document the live ledger produces over it.
//
// THE FIXTURE PINS THE ADOPTED-FILTER PATH, DELIBERATELY, AND A COLD OPEN OVER THE SAME SEED
// WOULD NOT REPRODUCE IT. Stated plainly because it is the one thing about this fixture a reader
// could otherwise get wrong: hand negknow.Open a nil bloom over input/ledger_seed.jsonl and the
// "stale" entry comes back ABSENT, not stale. That is not a bug in either the ledger or the
// fixture — it is §3.3's rebuild-from-active-records-only rule doing exactly what it says. A cold
// Open finds no tried.bloom on disk, rebuilds it from visibleActive(), and the seed's stale record
// is not active, so its key is not in the filter Query then tests. The stale third way is
// unreachable from a cold start by design, and that is the same mechanism
// runBloomRebuildActiveOnlyCase asserts as a feature: a stale elimination reopens the question it
// once closed.
//
// What a real session does is the other order, and it is the order this fixture models. The
// previous process rebuilt tried.bloom while the record was still active; the flip then appended
// its control line; eliminations.rebuildOnStale is "nextIdle", so the rebuild that flip owed never
// ran before the session ended; and the next process starts by ADOPTING the filter left on disk —
// which is precisely what Open's b parameter is for ("the filter to adopt — the daemon has usually
// loaded it already"). So the bloom handed to Open here holds exactly the keys that previous
// session's rebuild would have written: both project-scoped records, and not the foreign session's
// session-scoped one, which was never visible to it.
//
// The consequence worth carrying forward to SP-11 and SP-13: §8.3's stale answer is reachable only
// between a staleness flip and the next rebuild. A record that was already stale in the log when
// the filter was last rebuilt answers absent, and the re-verification note is gone.
func threeWayOverSeed(t *testing.T, seed []byte) threeWayDoc {
	t.Helper()
	p := testutil.NewProject(t)

	logPath := filepath.Join(paths.Of(p.Root).Records, "eliminations.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0o755))
	require.NoError(t, os.WriteFile(logPath, seed, 0o600))

	b := sketch.NewBloom(p.Cfg.Sketches.Bloom.Capacity, p.Cfg.Sketches.Bloom.FPRate)
	for _, r := range seedRecords(t, seed) {
		if r.Scope != negknow.ScopeProject {
			continue
		}
		b.Add(r.Desc.Key())
		b.Add(r.Desc.MatchKey())
	}

	l, err := negknow.Open(p.Root, p.Cfg, b, negknow.Deps{
		Session: fixtureSession,
		Clock:   p.Clock,
		Log:     p.Log,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })

	return threeWayDoc{
		Absent: entryFor(t, l, absentTarget, absentApproach),
		Active: entryFor(t, l, activeTarget, activeApproach),
		Stale:  entryFor(t, l, staleTarget, staleApproach),
	}
}

// seedRecords decodes the record lines of a seed log, skipping the control lines.
//
// A control line is discriminated exactly as replayLog discriminates one: by the presence of an
// "op" key. A bare Record line never carries one, which is the wire rule the frozen
// elimination_record.jsonl fixture settles.
func seedRecords(t *testing.T, seed []byte) []negknow.Record {
	t.Helper()
	var out []negknow.Record
	for _, line := range strings.Split(strings.ReplaceAll(string(seed), "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var probe struct {
			Op string `json:"op"`
		}
		require.NoError(t, json.Unmarshal([]byte(line), &probe), "seed line is not JSON: %s", line)
		if probe.Op != "" {
			continue
		}
		var r negknow.Record
		require.NoError(t, json.Unmarshal([]byte(line), &r), "seed record line does not decode: %s", line)
		out = append(out, r)
	}
	require.NotEmpty(t, out, "the seed log must carry at least one record line")
	return out
}

// renderThreeWay encodes doc exactly as want/three_way_answer.json is committed: two-space
// indentation, LF line endings, one trailing newline.
func renderThreeWay(t *testing.T, doc threeWayDoc) []byte {
	t.Helper()
	b, err := json.MarshalIndent(doc, "", "  ")
	require.NoError(t, err)
	return append(b, '\n')
}

// TestRecordContractFixtures is the recorder `devtool gen-contract-fixtures --record negknow`
// drives (implementation spec §16). It is a no-op in every ordinary test run and writes only when
// QOMPACK_RECORD_CONTRACTS names the fixture directory to write into.
//
// devtool owns the gate (it refuses to record while the package is a stub) and the promotion (it
// flips the MANIFEST row to frozen); this test owns the bytes, because devtool must not import
// internal/negknow to produce them.
func TestRecordContractFixtures(t *testing.T) {
	dir := os.Getenv("QOMPACK_RECORD_CONTRACTS")
	if dir == "" {
		t.Skip("platform: QOMPACK_RECORD_CONTRACTS not set; recorder only")
	}

	seed, err := os.ReadFile(filepath.Join(dir, "input", "ledger_seed.jsonl"))
	require.NoError(t, err, "the three_way_answer fixture's input seed must exist before it is recorded")

	out := filepath.Join(dir, "want", "three_way_answer.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(out), 0o755))
	require.NoError(t, os.WriteFile(out, renderThreeWay(t, threeWayOverSeed(t, seed)), 0o644))
}

// TestThreeWayAnswerFixture is the consumer half of Rule W-2: the live ledger, over the frozen
// input seed, must reproduce the frozen want bytes exactly. A fixture the real implementation
// cannot reproduce is a verification failure, not a fixture bug — so this test never rewrites
// want/, it only compares against it.
func TestThreeWayAnswerFixture(t *testing.T) {
	seed, want, frozen := testutil.ContractFixture(t, "negknow", "three_way_answer")
	if !frozen {
		t.Skip(testutil.NotRecordedSkip)
	}
	require.NotEmpty(t, seed, "the frozen fixture names an input seed")

	got := renderThreeWay(t, threeWayOverSeed(t, seed))

	// LF-normalized: the fixture is committed with LF endings, and a checkout under
	// core.autocrlf=true would otherwise fail this on line endings rather than on content.
	require.Equal(t, lf(want), lf(got))
}

// TestThreeWayAnswerFixture_PinsEachState reads the frozen fixture on its own terms and pins what
// each of the three entries has to say, so that a regression which changed all three answers
// consistently — and therefore still round-trips through TestThreeWayAnswerFixture — is still
// caught here.
func TestThreeWayAnswerFixture_PinsEachState(t *testing.T) {
	_, want, frozen := testutil.ContractFixture(t, "negknow", "three_way_answer")
	if !frozen {
		t.Skip(testutil.NotRecordedSkip)
	}

	var doc threeWayDoc
	require.NoError(t, json.Unmarshal(want, &doc))

	require.Equal(t, "absent", doc.Absent.State, "a pair the seed never mentions")
	require.Empty(t, doc.Absent.Reason)
	require.Empty(t, doc.Absent.Note)
	require.Empty(t, doc.Absent.Evidence)
	require.False(t, doc.Absent.BloomOnly, "a genuine absence is not a bloom false positive")

	require.Equal(t, "active", doc.Active.State)
	require.NotEmpty(t, doc.Active.Reason, "an active answer carries the reason the agent reads")
	require.Empty(t, doc.Active.Note, "only a stale answer carries the re-verification note")
	require.True(t, strings.HasPrefix(doc.Active.Evidence, "sha256:"))
	require.False(t, doc.Active.BloomOnly)

	require.Equal(t, "stale", doc.Stale.State)
	require.NotEmpty(t, doc.Stale.Reason)
	require.Equal(t, negknow.StaleNote, doc.Stale.Note,
		"§8.3 item 4's re-verification sentence, verbatim")
	require.True(t, strings.HasPrefix(doc.Stale.Evidence, "sha256:"))
	require.False(t, doc.Stale.BloomOnly)
}

// lf normalizes CRLF to LF so a Windows checkout compares on content, not on line endings.
func lf(b []byte) string { return strings.ReplaceAll(string(b), "\r\n", "\n") }
