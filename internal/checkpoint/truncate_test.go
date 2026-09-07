package checkpoint_test

// Truncation tests (SP-10 §10, test table "internal/checkpoint — truncation (§6.9)").
//
// Everything here drives the real exported Truncate through the shipped default tier assignment
// (config.Defaults().Checkpoint.Tiers) and the real exact estimator over the default constants,
// in-memory only, so every measured size and every golden byte is deterministic: the estimator's
// calibration factor is the identity until Calibrate is called, and nothing here calls it.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/checkpoint/checkpointtest"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/tokens"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// truncGoldenDir is testdata/golden/checkpoints/, relative to this package's directory. The
// truncation goldens 0002–0004 live here; they are committed, and every run only verifies them
// byte for byte (requireGoldenMatches).
const truncGoldenDir = "../../testdata/golden/checkpoints"

// oversizedContractInput is the `truncate_tier3_first` behaviour fixture the checkpoint contract
// MANIFEST declares with "state": "record-by-owner" and SP-10 as owner: the INPUT checkpoint
// whose tier 3 is what a budget cut reaches first.
const oversizedContractInput = "../../testdata/golden/contracts/checkpoint/input/oversized_checkpoint.json"

// truncBudget900 is the budget the plan fixes for golden 0003: `0002-full.json` truncated to 900
// tokens must come out with narrative, pointers and open questions empty and 2 decisions left.
const truncBudget900 = core.Tokens(900)

func tiersDefault() config.TiersCfg { return config.Defaults().Checkpoint.Tiers }

func newEstimator() tokens.Estimator { return tokens.New(config.Defaults(), "") }

// mustSize is sizeOf as §10 defines it — Marshal then Estimate under ClassJSON — for test
// arithmetic. Marshal cannot fail for the well-formed fixtures built here.
func mustSize(c checkpoint.Checkpoint, est tokens.Estimator) core.Tokens {
	b, err := checkpoint.Marshal(c)
	if err != nil {
		panic(err)
	}
	return est.Estimate(b, tokens.ClassJSON)
}

// fixtureHash mints a deterministic, valid content hash for fixture fields; the label only has to
// be unique within the fixture.
func fixtureHash(label string) core.Hash {
	return core.HashBytes("sp10.fixture", []byte(label))
}

// golden0002 builds the plan's `0002-full.json` shape in Go: seq 2, parent 0001.json, 3
// invariants, intent + 4 evolution deltas, 2 eliminations (1 active + 1 stale with depends_on),
// 5 decisions (descending turn order, all with alternatives), 3 open questions, current work
// with blocked_on null, 6 file pointers, 4 tool pointers, a 3-sentence narrative, 2 dropped
// entries and cache {148230, 18770, "warm"}. Every call returns freshly allocated slices so a
// test can never corrupt another through a shared backing array.
func golden0002() checkpoint.Checkpoint {
	const sess = core.SessionID("sess_01J8ZQ5R7N3K2M4P6T8V0X2Y4A")
	return checkpoint.Checkpoint{
		Version:         checkpoint.SchemaVersion,
		Session:         sess,
		Seq:             2,
		Created:         "2026-01-01T00:58:00.000Z",
		Parent:          "0001.json",
		EncodedSegments: []core.SegmentID{15, 16, 17, 18},

		Invariants: []checkpoint.Invariant{
			{ID: "inv_7c1a9e2f4b60", Text: "The refresh-token rotation must remain single-use; reuse detection is a security requirement.", Source: "user", Pinned: 1767225120000},
			{ID: "inv_2d8f0a6c3e15", Text: "Never change the pgbouncer pool mode without re-running the exhaustion reproduction.", Source: "agent", Pinned: 1767225300000},
			{ID: "inv_5b8e2d1f7a03", Text: "The load-test gate stays required on the release branch.", Source: "user", Pinned: 1767225660000},
		},
		UserIntent: checkpoint.UserIntent{
			Original: "Fix the intermittent 500s on POST /api/session/refresh. They started after the connection-pool change last Tuesday.",
			Evolution: []string{
				"Narrowed to refreshToken(): the 500 is a pool acquisition timeout, not a token error.",
				"Scope widened to include the pgbouncer transaction-mode interaction.",
				"Added the k6 load test as the acceptance gate for the fix.",
				"Split the rotation extraction into its own branch behind a flag.",
			},
		},
		Eliminated: []negknow.Record{
			{
				ID: "elim_3f9b2c7d1a48", Session: sess, TS: 1767225480000,
				Target: "src/auth.ts:refreshToken", Approach: "widen pool timeout",
				Reason: "pgbouncer 1.18 ignores statement_timeout in transaction pooling mode, so the widened timeout never takes effect",
				Desc: negknow.Descriptor{
					NormalizedPath: "src/auth.ts", Symbol: "refreshToken",
					ApproachClass: "widen-timeout", ReasonHash: fixtureHash("reason widen-timeout"),
				},
				Evidence:  fixtureHash("evidence widen-timeout"),
				DependsOn: []core.Dep{{Path: "docker-compose.yml", Hash: fixtureHash("dep docker-compose.yml")}},
				Scope:     negknow.ScopeProject, Status: negknow.StatusActive, Source: negknow.SourceSlashCommand,
			},
			{
				ID: "elim_8c1e4a7b2d95", Session: sess, TS: 1767225840000,
				Target: "src/pool.ts:acquire", Approach: "increase pool size",
				Reason: "resizing moves the cliff without removing the lock-hold-across-IO pattern",
				Desc: negknow.Descriptor{
					NormalizedPath: "src/pool.ts", Symbol: "acquire",
					ApproachClass: "resize-pool", ReasonHash: fixtureHash("reason resize-pool"),
				},
				Evidence:  fixtureHash("evidence resize-pool"),
				DependsOn: []core.Dep{{Path: "src/pool.ts", Hash: fixtureHash("dep src/pool.ts")}},
				Scope:     negknow.ScopeSession, Status: negknow.StatusStale,
				StaleSince: 1767226020000, StaleBecause: []string{"src/pool.ts"},
				Source: negknow.SourceSlashCommand,
			},
		},

		Decisions: []checkpoint.Decision{
			{ID: "dec_a3f2c9e14b70", What: "Move the rotation out of the request transaction.", Why: "It holds a row lock across the outbound identity-provider call.", AlternativesRejected: []string{"Widen the pool timeout — eliminated.", "Increase pool size — rejected."}, Evidence: fixtureHash("dec 1"), Turn: 61},
			{ID: "dec_b7e1d4a92c58", What: "Keep pgbouncer in transaction mode.", Why: "Session mode halves effective capacity for no measured gain.", AlternativesRejected: []string{"Switch to session pooling."}, Evidence: fixtureHash("dec 2"), Turn: 54},
			{ID: "dec_c9a5f2e81d36", What: "Gate the fix on the k6 load test.", Why: "The failure only reproduces above 150 concurrent refreshes.", AlternativesRejected: []string{"Unit-test the pool wrapper only."}, Evidence: fixtureHash("dec 3"), Turn: 47},
			{ID: "dec_d2c8b5f71e94", What: "Use a dedicated short-lived connection for rotation.", Why: "It caps lock hold time at the token write itself.", AlternativesRejected: []string{"Reuse the request connection."}, Evidence: fixtureHash("dec 4"), Turn: 40},
			{ID: "dec_e5b9c8a24f17", What: "Record the widened-timeout attempt as eliminated.", Why: "pgbouncer ignores it in transaction mode.", AlternativesRejected: []string{"Leave it as a code comment."}, Evidence: fixtureHash("dec 5"), Turn: 33},
		},
		OpenQuestions: []string{
			"Does the identity provider guarantee idempotency on a retried rotation?",
			"Is the staging pgbouncer running the same 1.18 build as production?",
			"Do we need our own dedupe key for concurrent refresh attempts?",
		},
		CurrentWork: checkpoint.CurrentWork{
			Goal:     "Eliminate pool exhaustion on POST /api/session/refresh under load.",
			NextStep: "Extract the identity-provider call from the transaction and re-run the k6 gate.",
		},

		Pointers: checkpoint.Pointers{
			Files: []checkpoint.FilePointer{
				{Path: "src/auth.ts", Hash: fixtureHash("file src/auth.ts"), Why: "refreshToken lives here; the lock-across-IO pattern starts at the top"},
				{Path: "src/pool.ts", Hash: fixtureHash("file src/pool.ts"), Why: "acquisition timeout and pool sizing are configured here"},
				{Path: "src/session/refresh.ts", Hash: fixtureHash("file src/session/refresh.ts"), Why: "the route handler that drives refreshToken"},
				{Path: "docker-compose.yml", Hash: fixtureHash("file docker-compose.yml"), Why: "pins the pgbouncer version and pool mode"},
				{Path: "package-lock.json", Hash: fixtureHash("file package-lock.json"), Why: "pins the pg client the elimination depends on"},
				{Path: "test/load/refresh-k6.js", Hash: fixtureHash("file test/load/refresh-k6.js"), Why: "the acceptance load test for the fix"},
			},
			Tools: []checkpoint.ToolPointer{
				{ToolUseID: "toolu_01LOADTEST48H2M9", Hash: fixtureHash("tool loadtest"), Summary: "k6: 200 concurrent refreshes, 37 failures, all pool acquisition timeouts"},
				{ToolUseID: "toolu_01POOLSTATS7C4Q1", Hash: fixtureHash("tool poolstats"), Summary: "pool statistics during the failing window"},
				{ToolUseID: "toolu_01PGBLOG92XW5TR3", Hash: fixtureHash("tool pgblog"), Summary: "pgbouncer log for the failing window"},
				{ToolUseID: "toolu_01SCHEMA4Q8N2VD7", Hash: fixtureHash("tool schema"), Summary: "session-table schema and the rotation trigger"},
			},
		},
		Narrative: "Reproduced the 500s under concurrent load and traced them to pool acquisition timeouts rather than token validation. " +
			"Widening the timeout had no effect because pgbouncer ignores it in transaction pooling mode. " +
			"The working hypothesis is that refreshToken holds a row lock across the outbound identity-provider call.",

		SketchRefs: map[string]string{"tried": "tried.bloom", "touch": "touch.cms"},
		Dropped: []checkpoint.DropEntry{
			{Kind: "path_rule", ID: "api-conventions.md", Detail: "path-scoped rule for src/api/**; no pointer in this checkpoint matched its globs"},
			{Kind: "tool_output", ID: "toolu_01M3N4P5Q6R7S8T9U0V1W2X3", Detail: "superseded by a later read of the same file"},
		},
		Cache: checkpoint.CacheInfo{PChosen: 148230, RewriteTokens: 18770, TTLState: "warm"},
	}
}

// golden0004Input builds the `0004-tier1-over-budget.json` INPUT: a checkpoint whose tier 1
// alone is above the 200-token budget the golden is generated at, with a little of every
// cuttable field so the drop report shows the whole walk giving up.
func golden0004Input() checkpoint.Checkpoint {
	const sess = core.SessionID("sess_01J8ZQ5R7N3K2M4P6T8V0X2Y4A")
	return checkpoint.Checkpoint{
		Version:         checkpoint.SchemaVersion,
		Session:         sess,
		Seq:             3,
		Created:         "2026-01-01T01:31:00.000Z",
		Parent:          "0002.json",
		EncodedSegments: []core.SegmentID{19, 20},
		Invariants: []checkpoint.Invariant{
			{ID: "inv_7c1a9e2f4b60", Text: "The refresh-token rotation must remain single-use; reuse detection is a security requirement, not a preference.", Source: "user", Pinned: 1767225120000},
			{ID: "inv_2d8f0a6c3e15", Text: "Never change the pgbouncer pool mode without re-running the connection-exhaustion reproduction.", Source: "agent", Pinned: 1767225300000},
		},
		UserIntent: checkpoint.UserIntent{
			Original: "Fix the intermittent 500s on POST /api/session/refresh. They started after the connection-pool change last Tuesday.",
			Evolution: []string{
				"Narrowed to refreshToken(): the 500 is a pool acquisition timeout, not a token error.",
				"Scope widened to include the pgbouncer transaction-mode interaction after the timeout fix did not help.",
			},
		},
		Eliminated: []negknow.Record{{
			ID: "elim_3f9b2c7d1a48", Session: sess, TS: 1767225480000,
			Target: "src/auth.ts:refreshToken", Approach: "widen pool timeout",
			Reason: "pgbouncer 1.18 ignores statement_timeout in transaction pooling mode, so the widened timeout never takes effect",
			Desc: negknow.Descriptor{
				NormalizedPath: "src/auth.ts", Symbol: "refreshToken",
				ApproachClass: "widen-timeout", ReasonHash: fixtureHash("reason widen-timeout"),
			},
			Evidence:  fixtureHash("evidence widen-timeout"),
			DependsOn: []core.Dep{{Path: "docker-compose.yml", Hash: fixtureHash("dep docker-compose.yml")}},
			Scope:     negknow.ScopeProject, Status: negknow.StatusActive, Source: negknow.SourceSlashCommand,
		}},
		Decisions: []checkpoint.Decision{
			{ID: "dec_a3f2c9e14b70", What: "Move the rotation out of the request transaction.", Why: "It holds a row lock across the outbound identity-provider call.", AlternativesRejected: []string{"Widen the pool timeout — eliminated."}, Evidence: fixtureHash("dec 1"), Turn: 61},
		},
		OpenQuestions: []string{"Is the staging pgbouncer running the same 1.18 build as production?"},
		CurrentWork: checkpoint.CurrentWork{
			Goal:     "Eliminate pool exhaustion on POST /api/session/refresh under load.",
			NextStep: "Extract the identity-provider call from the transaction.",
		},
		Pointers: checkpoint.Pointers{
			Files: []checkpoint.FilePointer{{Path: "src/auth.ts", Hash: fixtureHash("file src/auth.ts"), Why: "refreshToken lives here"}},
			Tools: []checkpoint.ToolPointer{},
		},
		Narrative:  "Traced the 500s to pool acquisition timeouts rather than token validation.",
		SketchRefs: map[string]string{"tried": "tried.bloom", "touch": "touch.cms"},
		Cache:      checkpoint.CacheInfo{PChosen: 151080, RewriteTokens: 9410, TTLState: "expiring"},
	}
}

// golden0004Budget is the budget `0004-tier1-over-budget.json` is generated at; the fixture's
// point is that tier 1 alone exceeds it.
const golden0004Budget = core.Tokens(200)

// goldenT is the slice of *testing.T that requireGoldenMatches uses. It takes an interface rather
// than *testing.T so that TestAMissingGoldenIsReportedNotRecorded can watch the helper fail
// instead of being failed by it.
type goldenT interface {
	require.TestingT
	Helper()
}

// requireGoldenMatches verifies path byte for byte against what the implementation just produced.
//
// It never writes. A golden that is not on disk is a failure, not an invitation to record one:
// fixtures are reproduced, never regenerated (Rule W-2). The helper used to seed a missing file
// silently, with no -update gate — which turned the obvious response to a byte-diff failure
// ("delete the golden and re-run") into a mechanism that erased the evidence and reported PASS,
// including for the frozen contract fixture under testdata/golden/contracts/. Every other golden
// writer in this repository is behind an explicit flag; this one is behind none because it does
// not write at all.
func requireGoldenMatches(t goldenT, path string, got []byte) {
	t.Helper()
	want, err := os.ReadFile(path)
	require.NoError(t, err,
		"golden %s is missing: fixtures are reproduced, never regenerated (Rule W-2). "+
			"Restore it from version control rather than re-recording it", path)
	require.Equal(t, string(want), string(got),
		"golden %s drifted from what the implementation reproduces", path)
}

// errGoldenAborted is what recordingT panics with, so a test can tell "the helper failed" from
// "the helper returned".
var errGoldenAborted = errors.New("golden helper aborted")

// recordingT captures a require failure instead of ending the test with it. require.TestingT is
// only Errorf and FailNow, and *testing.T's FailNow ends the calling goroutine, so observing a
// helper's own failure needs a stand-in.
type recordingT struct{ msgs []string }

func (r *recordingT) Errorf(format string, args ...any) {
	r.msgs = append(r.msgs, fmt.Sprintf(format, args...))
}
func (r *recordingT) FailNow() { panic(errGoldenAborted) }
func (r *recordingT) Helper()  {}

// TestAMissingGoldenIsReportedNotRecorded pins Rule W-2 on this package's own golden helper: an
// absent fixture must fail the run and must not appear on disk afterwards. It would pass against
// any helper that reports the miss and fail against one that re-records it — which is what this
// helper did, on an ordinary `go test` with no flag of any kind.
func TestAMissingGoldenIsReportedNotRecorded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "contracts", "0002-full.json")
	rec := &recordingT{}

	require.PanicsWithValue(t, errGoldenAborted, func() {
		requireGoldenMatches(rec, path, []byte("{\n  \"version\": 1\n}\n"))
	}, "a missing golden must fail the test rather than being written")
	require.NoFileExists(t, path, "fixtures are reproduced, never regenerated (Rule W-2)")
	require.NotEmpty(t, rec.msgs, "the failure must say something a reader can act on")
}

// TestTruncateConformance drives checkpointtest.RunTruncateSuite against the real Truncate with
// the shipped tier assignment and estimator bound, so the suite's Rule W-1 skip stops firing and
// its behaviour block (tier order, monotonicity, purity) runs.
func TestTruncateConformance(t *testing.T) {
	checkpointtest.RunTruncateSuite(t, "real", func(t *testing.T) checkpointtest.TruncateFunc {
		est := newEstimator()
		tiers := tiersDefault()
		return func(c checkpoint.Checkpoint, budget core.Tokens) (checkpoint.Checkpoint, []checkpoint.DropEntry) {
			return checkpoint.Truncate(c, budget, tiers, est)
		}
	})
}

func TestTruncateDropsTierThreeFirst(t *testing.T) {
	est := newEstimator()
	in := golden0002()
	budget := mustSize(in, est) - 1

	got, drops := checkpoint.Truncate(in, budget, tiersDefault(), est)

	require.Empty(t, got.Narrative, "narrative is the first cut of tier 3")
	want := golden0002()
	want.Narrative = ""
	require.Equal(t, want, got, "a budget one token short cuts the narrative and nothing else")
	require.Equal(t, []checkpoint.DropEntry{
		{Kind: "narrative", ID: "narrative", Detail: "prose residue dropped at budget"},
	}, drops)
}

func TestTruncateDropsToolPointersBeforeFilePointers(t *testing.T) {
	est := newEstimator()
	in := golden0002()

	// A budget that exactly fits the document once the narrative and all 4 tool pointers are
	// gone: Truncate must land there without touching a single file pointer.
	target := golden0002()
	target.Narrative = ""
	target.Pointers.Tools = target.Pointers.Tools[:0]
	budget := mustSize(target, est)

	got, drops := checkpoint.Truncate(in, budget, tiersDefault(), est)

	require.Empty(t, got.Pointers.Tools, "all 4 tool pointers are cut")
	require.Equal(t, in.Pointers.Files, got.Pointers.Files, "file pointers are untouched")
	var toolDrops, fileDrops int
	for _, d := range drops {
		switch d.Kind {
		case "tool_pointer":
			toolDrops++
			require.Equal(t, "truncated at budget; expand(hash) still resolves", d.Detail)
		case "file_pointer":
			fileDrops++
		}
	}
	require.Equal(t, 4, toolDrops, "all 4 pointer drops are tool pointers")
	require.Zero(t, fileDrops, "no file pointer is dropped while tool pointers suffice")
}

func TestTruncateDropsPointersTailFirst(t *testing.T) {
	est := newEstimator()
	in := golden0002()
	require.Len(t, in.Pointers.Files, 6)

	// Fits exactly once narrative + all tools + the LAST TWO file pointers are gone.
	target := golden0002()
	target.Narrative = ""
	target.Pointers.Tools = target.Pointers.Tools[:0]
	target.Pointers.Files = target.Pointers.Files[:4]
	budget := mustSize(target, est)

	got, drops := checkpoint.Truncate(in, budget, tiersDefault(), est)

	require.Equal(t, in.Pointers.Files[:4], got.Pointers.Files,
		"the slice tail — the two OLDEST pointers — is what goes")

	var fileIDs []string
	for _, d := range drops {
		if d.Kind == "file_pointer" {
			fileIDs = append(fileIDs, d.ID)
			require.Equal(t, "truncated at budget; re_read(path) still resolves", d.Detail)
		}
	}
	require.Equal(t, []string{in.Pointers.Files[5].Path, in.Pointers.Files[4].Path}, fileIDs,
		"file pointers are cut tail-first, and reported in cut order")
}

func TestTruncateReachesTierTwoOnlyAfterTierThree(t *testing.T) {
	est := newEstimator()
	in := golden0002()

	got, drops := checkpoint.Truncate(in, truncBudget900, tiersDefault(), est)

	require.Empty(t, got.Pointers.Files, "tier 3 file pointers are exhausted before tier 2")
	require.Empty(t, got.Pointers.Tools, "tier 3 tool pointers are exhausted before tier 2")
	require.Empty(t, got.Narrative)
	require.NotEmpty(t, drops)
	require.Less(t, len(got.Decisions), len(in.Decisions), "budget 900 forces decision cuts")
	require.Empty(t, got.OpenQuestions, "open questions are cut before decisions")
	require.Equal(t, in.Invariants, got.Invariants)
	require.Equal(t, in.UserIntent, got.UserIntent)
	require.Equal(t, in.Eliminated, got.Eliminated)

	// The ordering half of §6.9, asserted on the drop report itself: every tier-3 drop precedes
	// every tier-2 drop.
	lastTier3, firstTier2 := -1, len(drops)
	for i, d := range drops {
		switch d.Kind {
		case "narrative", "tool_pointer", "file_pointer":
			lastTier3 = i
		case "open_question", "alternatives", "decision", "next_step":
			if i < firstTier2 {
				firstTier2 = i
			}
		}
	}
	require.Less(t, lastTier3, firstTier2, "no tier-2 drop may precede a tier-3 drop")

	// Plan drift, recorded where it bites: the plan's fixture table predicts this budget leaves 2
	// decisions standing. Against the SHIPPED exact estimator (G10.2), tier 1 of the plan-shaped
	// 0002 — two §8.5 eliminations with their three content hashes each — alone measures 1636
	// tokens, so a 900-token budget exhausts both cuttable tiers and ends in budget_exceeded. The
	// 900 was calibrated to the superseded 3.2-chars/token model.
	//
	// This test keeps the 900-token budget, because exhausting both cuttable tiers is exactly what
	// makes it able to assert the tier-3-before-tier-2 ordering over a complete walk. The golden
	// is pinned at a budget that reaches the shape the plan's fixture table names — see
	// TestGoldenTruncationFixtures — so behaviour and fixture each stay with the budget that
	// demonstrates them, and neither test owns the other's evidence.
	require.Empty(t, got.Decisions, "budget 900 is below tier 1's own size; nothing cuttable survives")
	require.Equal(t, "budget_exceeded", drops[len(drops)-1].Kind)
}

// truncBudgetMidTier2 is a budget inside [sizeOf(base+2 decisions), sizeOf(base+3 decisions)) =
// [1904, 2023) for the golden 0002 shape: wide enough that tier 1 plus two decisions fit, narrow
// enough that a third does not. This is the budget at which the outcome the plan's fixture table
// describes for 0003 — "narrative empty, pointers empty, open questions empty, 2 decisions, full
// tier 1" — actually emerges under the shipped estimator.
const truncBudgetMidTier2 = core.Tokens(1950)

// TestTruncateStopsMidDecisionsKeepingNewest pins the §6.9 walk stopping INSIDE the decisions
// stage: alternatives already emptied, the two newest decisions kept, next_step untouched.
func TestTruncateStopsMidDecisionsKeepingNewest(t *testing.T) {
	est := newEstimator()
	in := golden0002()

	got, drops := checkpoint.Truncate(in, truncBudgetMidTier2, tiersDefault(), est)

	require.LessOrEqual(t, int(mustSize(got, est)), int(truncBudgetMidTier2), "the result fits the budget")
	require.Empty(t, got.Narrative)
	require.Empty(t, got.Pointers.Files)
	require.Empty(t, got.Pointers.Tools)
	require.Empty(t, got.OpenQuestions)
	require.Len(t, got.Decisions, 2, "the two NEWEST decisions (indices 0 and 1) survive")
	require.Equal(t, in.Decisions[0].ID, got.Decisions[0].ID)
	require.Equal(t, in.Decisions[1].ID, got.Decisions[1].ID)
	for _, d := range got.Decisions {
		require.Empty(t, d.AlternativesRejected, "alternatives were emptied before any decision was cut")
	}
	require.Equal(t, in.CurrentWork, got.CurrentWork, "next_step is only cut after the last decision")
	require.Equal(t, in.Invariants, got.Invariants)
	require.Equal(t, in.UserIntent, got.UserIntent)
	require.Equal(t, in.Eliminated, got.Eliminated)
	for _, d := range drops {
		require.NotEqual(t, "budget_exceeded", d.Kind, "the budget was met; no overflow entry")
		require.NotEqual(t, "next_step", d.Kind)
	}
}

func TestTruncateEmptiesAlternativesBeforeDroppingDecisions(t *testing.T) {
	est := newEstimator()
	in := golden0002()
	for _, d := range in.Decisions {
		require.NotEmpty(t, d.AlternativesRejected, "fixture sanity: all 5 decisions carry alternatives")
	}

	// Fits exactly at the alternatives step: tier 3 and open questions gone, every
	// alternatives_rejected emptied, every decision still present.
	target := golden0002()
	target.Narrative = ""
	target.Pointers.Tools = target.Pointers.Tools[:0]
	target.Pointers.Files = target.Pointers.Files[:0]
	target.OpenQuestions = target.OpenQuestions[:0]
	for i := range target.Decisions {
		target.Decisions[i].AlternativesRejected = []string{}
	}
	budget := mustSize(target, est)

	got, drops := checkpoint.Truncate(in, budget, tiersDefault(), est)

	require.Len(t, got.Decisions, len(in.Decisions), "no decision is dropped")
	for i, d := range got.Decisions {
		require.Empty(t, d.AlternativesRejected, "decision %d alternatives are emptied", i)
		require.Equal(t, in.Decisions[i].ID, d.ID)
	}
	var altDrops []string
	for _, d := range drops {
		if d.Kind == "alternatives" {
			altDrops = append(altDrops, d.ID)
			require.Equal(t, "alternatives_rejected emptied at budget", d.Detail)
		}
		require.NotEqual(t, "decision", d.Kind, "no decision drop at this budget")
	}
	require.Len(t, altDrops, len(in.Decisions), "one alternatives entry per emptied decision")
}

func TestTruncateNeverTouchesTierOne(t *testing.T) {
	est := newEstimator()
	in := golden0002()

	got, drops := checkpoint.Truncate(in, 10, tiersDefault(), est)

	require.Equal(t, in.Invariants, got.Invariants, "tier 1 invariants are never truncated")
	require.Equal(t, in.UserIntent, got.UserIntent, "tier 1 user intent is never truncated")
	require.Equal(t, in.Eliminated, got.Eliminated, "tier 1 eliminations are never truncated")
	require.Equal(t, in.Dropped, got.Dropped,
		"Truncate reports its drops in the return value, not by editing the artifact's dropped field")
	require.Equal(t, in.CurrentWork.Goal, got.CurrentWork.Goal, "goal is kept")
	require.Nil(t, got.CurrentWork.BlockedOn, "blocked_on is kept")
	require.Empty(t, got.Pointers.Files)
	require.Empty(t, got.Pointers.Tools)
	require.Empty(t, got.Narrative)
	require.Empty(t, got.OpenQuestions)
	require.Empty(t, got.Decisions)
	require.Empty(t, got.CurrentWork.NextStep)

	var exceeded []checkpoint.DropEntry
	for _, d := range drops {
		if d.Kind == "budget_exceeded" {
			exceeded = append(exceeded, d)
		}
	}
	require.Len(t, exceeded, 1, "exactly one budget_exceeded entry")
	require.Equal(t, "tier1", exceeded[0].ID)
	require.Equal(t,
		fmt.Sprintf("tier 1 is %d tokens against a %d budget; written in full per §8.5", mustSize(got, est), 10),
		exceeded[0].Detail)
}

func TestTruncateHonoursConfiguredNeverList(t *testing.T) {
	est := newEstimator()
	tiers := tiersDefault()
	tiers.Never = append(tiers.Never, "pointers")
	in := golden0002()

	got, drops := checkpoint.Truncate(in, truncBudget900, tiers, est)

	require.Equal(t, in.Pointers, got.Pointers, "a field in Never survives every budget")
	require.Less(t, len(got.Decisions), len(in.Decisions), "decisions are cut instead")
	for _, d := range drops {
		require.NotEqual(t, "file_pointer", d.Kind)
		require.NotEqual(t, "tool_pointer", d.Kind)
	}
}

func TestTruncateZeroBudgetIsUnlimited(t *testing.T) {
	est := newEstimator()
	in := golden0002()

	for _, budget := range []core.Tokens{0, -7} {
		got, drops := checkpoint.Truncate(in, budget, tiersDefault(), est)
		require.Equal(t, in, got, "budget %d means unlimited", int(budget))
		require.Nil(t, drops, "budget %d drops nothing", int(budget))
	}
}

func TestTruncateDropEntriesAreComplete(t *testing.T) {
	est := newEstimator()
	in := golden0002()

	got, drops := checkpoint.Truncate(in, truncBudget900, tiersDefault(), est)

	byKind := map[string][]checkpoint.DropEntry{}
	for _, d := range drops {
		byKind[d.Kind] = append(byKind[d.Kind], d)
	}

	// One entry per removed element, with the §10 table's Kind, ID and Detail — computed from the
	// in/got diff so the assertions survive fixture retuning.
	require.Len(t, byKind["narrative"], 1)
	require.Equal(t, checkpoint.DropEntry{Kind: "narrative", ID: "narrative", Detail: "prose residue dropped at budget"}, byKind["narrative"][0])

	require.Len(t, byKind["tool_pointer"], len(in.Pointers.Tools)-len(got.Pointers.Tools))
	for _, d := range byKind["tool_pointer"] {
		require.Equal(t, "truncated at budget; expand(hash) still resolves", d.Detail)
	}
	require.Len(t, byKind["file_pointer"], len(in.Pointers.Files)-len(got.Pointers.Files))
	for _, d := range byKind["file_pointer"] {
		require.Equal(t, "truncated at budget; re_read(path) still resolves", d.Detail)
	}

	oqCut := len(in.OpenQuestions) - len(got.OpenQuestions)
	require.Len(t, byKind["open_question"], oqCut)
	for i, d := range byKind["open_question"] {
		idx := len(in.OpenQuestions) - 1 - i // tail-first: the last question is cut, and reported, first
		require.Equal(t, fmt.Sprintf("oq_%d", idx), d.ID)
		require.Equal(t, in.OpenQuestions[idx], d.Detail, "a short question is carried whole")
	}

	require.Len(t, byKind["alternatives"], len(in.Decisions), "every decision had alternatives to empty")
	require.Len(t, byKind["decision"], len(in.Decisions)-len(got.Decisions))
	for i, d := range byKind["decision"] {
		idx := len(in.Decisions) - 1 - i
		require.Equal(t, string(in.Decisions[idx].ID), d.ID, "decisions are cut tail-first")
		require.Equal(t, "truncated at budget; why(decision_id) still resolves", d.Detail)
	}
}

func TestTruncateCapsOpenQuestionDetailAtPointerWhyMaxRunes(t *testing.T) {
	est := newEstimator()
	in := golden0002()
	long := strings.Repeat("é", 150) // multi-byte on purpose: the cap is runes, not bytes
	in.OpenQuestions = []string{long}

	// Small enough that the walk reaches (and empties) open questions.
	got, drops := checkpoint.Truncate(in, 10, tiersDefault(), est)
	require.Empty(t, got.OpenQuestions)

	var oq []checkpoint.DropEntry
	for _, d := range drops {
		if d.Kind == "open_question" {
			oq = append(oq, d)
		}
	}
	require.Len(t, oq, 1)
	require.Equal(t, "oq_0", oq[0].ID)
	require.Equal(t, strings.Repeat("é", 120), oq[0].Detail,
		"the question text is capped at pointerWhyMaxRunes (120) runes")
}

// TestTruncateNextStepIsTheFinalCutThatFits pins the last step of the tier-2 order: when
// everything else is already gone, blanking next_step is the cut that lands the document inside
// budget — goal and blocked_on stay.
func TestTruncateNextStepIsTheFinalCutThatFits(t *testing.T) {
	est := newEstimator()
	in := checkpoint.Checkpoint{
		Version: checkpoint.SchemaVersion,
		Session: "sess_01J8ZQ5R7N3K2M4P6T8V0X2Y4A",
		Seq:     4,
		Created: "2026-01-01T02:00:00.000Z",
		CurrentWork: checkpoint.CurrentWork{
			Goal:     "Land the rotation extraction.",
			NextStep: "Re-run the k6 gate against the extracted rotation and compare acquisition timings.",
		},
	}
	target := in
	target.CurrentWork.NextStep = ""
	budget := mustSize(target, est)

	got, drops := checkpoint.Truncate(in, budget, tiersDefault(), est)

	require.Empty(t, got.CurrentWork.NextStep)
	require.Equal(t, in.CurrentWork.Goal, got.CurrentWork.Goal, "goal is kept")
	require.Equal(t, []checkpoint.DropEntry{
		{Kind: "next_step", ID: "current_work.next_step", Detail: "truncated at budget"},
	}, drops, "the walk stops at the next_step cut with no budget_exceeded entry")

	// An already-empty next_step is not a cut: the walk passes through to the overflow entry
	// without inventing a drop.
	in.CurrentWork.NextStep = ""
	_, drops = checkpoint.Truncate(in, 1, tiersDefault(), est)
	for _, d := range drops {
		require.NotEqual(t, "next_step", d.Kind, "nothing was removed, so nothing is reported")
	}
	require.Equal(t, "budget_exceeded", drops[len(drops)-1].Kind)
}

// TestTruncateIsMonotone is the plan's rapid property: for budgets b1 < b2, the b1 result is
// never larger than the b2 result, and its content is a subset of the b2 result's.
func TestTruncateIsMonotone(t *testing.T) {
	est := newEstimator()
	tiers := tiersDefault()

	rapid.Check(t, func(rt *rapid.T) {
		c := genCheckpoint(rt)
		b1 := core.Tokens(rapid.IntRange(1, 2500).Draw(rt, "b1"))
		b2 := b1 + core.Tokens(rapid.IntRange(0, 2500).Draw(rt, "delta"))

		got1, _ := checkpoint.Truncate(c, b1, tiers, est)
		got2, _ := checkpoint.Truncate(c, b2, tiers, est)

		require.LessOrEqual(rt, int(mustSize(got1, est)), int(mustSize(got2, est)),
			"sizeOf(Truncate(c,b1)) <= sizeOf(Truncate(c,b2)) for b1 <= b2")
		requireContentSubset(rt, got1, got2)
	})
}

// requireContentSubset asserts everything small carries is present, identically, in big — the
// "field set is a subset" half of the monotonicity property. Cuts are tail-only, so surviving
// slices must be prefixes.
func requireContentSubset(t require.TestingT, small, big checkpoint.Checkpoint) {
	require.Equal(t, big.Invariants, small.Invariants)
	require.Equal(t, big.UserIntent, small.UserIntent)
	require.Equal(t, big.Eliminated, small.Eliminated)

	require.LessOrEqual(t, len(small.Pointers.Files), len(big.Pointers.Files))
	require.Equal(t, big.Pointers.Files[:len(small.Pointers.Files)], small.Pointers.Files)
	require.LessOrEqual(t, len(small.Pointers.Tools), len(big.Pointers.Tools))
	require.Equal(t, big.Pointers.Tools[:len(small.Pointers.Tools)], small.Pointers.Tools)
	require.LessOrEqual(t, len(small.OpenQuestions), len(big.OpenQuestions))
	require.Equal(t, big.OpenQuestions[:len(small.OpenQuestions)], small.OpenQuestions)

	require.LessOrEqual(t, len(small.Decisions), len(big.Decisions))
	for i, d := range small.Decisions {
		b := big.Decisions[i]
		require.Equal(t, b.ID, d.ID)
		require.Equal(t, b.What, d.What)
		require.Equal(t, b.Why, d.Why)
		require.Equal(t, b.Turn, d.Turn)
		if len(d.AlternativesRejected) > 0 {
			require.Equal(t, b.AlternativesRejected, d.AlternativesRejected,
				"alternatives are emptied wholesale or kept, never partially cut")
		}
	}

	if small.Narrative != "" {
		require.Equal(t, big.Narrative, small.Narrative)
	}
	if small.CurrentWork.NextStep != "" {
		require.Equal(t, big.CurrentWork.NextStep, small.CurrentWork.NextStep)
	}
	require.Equal(t, big.CurrentWork.Goal, small.CurrentWork.Goal)
}

// genCheckpoint draws a small random checkpoint whose tier-2/3 slices honour the writer's
// descending-turn invariant, which is what Truncate's tail-first direction relies on.
func genCheckpoint(rt *rapid.T) checkpoint.Checkpoint {
	word := rapid.StringMatching(`[a-z]{1,12}( [a-z]{1,12}){0,5}`)
	c := checkpoint.Checkpoint{
		Version: checkpoint.SchemaVersion,
		Session: "sess_rapid",
		Seq:     2,
		Created: "2026-01-01T00:12:30.000Z",
		Parent:  "0001.json",
	}
	for i, n := 0, rapid.IntRange(0, 2).Draw(rt, "ninv"); i < n; i++ {
		c.Invariants = append(c.Invariants, checkpoint.Invariant{
			ID: fmt.Sprintf("inv_%012d", i), Text: word.Draw(rt, "invtext"), Source: "user", Pinned: core.UnixMilli(1767225120000 + i),
		})
	}
	c.UserIntent.Original = word.Draw(rt, "intent")
	for i, n := 0, rapid.IntRange(0, 5).Draw(rt, "ndec"); i < n; i++ {
		d := checkpoint.Decision{
			ID:   core.DecisionID(fmt.Sprintf("dec_%012d", i)),
			What: word.Draw(rt, "what"), Why: word.Draw(rt, "why"),
			Turn: core.TurnIndex(100 - i),
		}
		for j, m := 0, rapid.IntRange(0, 2).Draw(rt, "nalt"); j < m; j++ {
			d.AlternativesRejected = append(d.AlternativesRejected, word.Draw(rt, "alt"))
		}
		c.Decisions = append(c.Decisions, d)
	}
	for i, n := 0, rapid.IntRange(0, 4).Draw(rt, "noq"); i < n; i++ {
		c.OpenQuestions = append(c.OpenQuestions, word.Draw(rt, "oq"))
	}
	c.CurrentWork.Goal = word.Draw(rt, "goal")
	c.CurrentWork.NextStep = word.Draw(rt, "next")
	for i, n := 0, rapid.IntRange(0, 6).Draw(rt, "nfile"); i < n; i++ {
		c.Pointers.Files = append(c.Pointers.Files, checkpoint.FilePointer{
			Path: fmt.Sprintf("src/f%d.ts", i), Hash: fixtureHash(fmt.Sprintf("rf%d", i)), Why: word.Draw(rt, "fwhy"),
		})
	}
	for i, n := 0, rapid.IntRange(0, 5).Draw(rt, "ntool"); i < n; i++ {
		c.Pointers.Tools = append(c.Pointers.Tools, checkpoint.ToolPointer{
			ToolUseID: core.ToolUseID(fmt.Sprintf("toolu_%08d", i)), Hash: fixtureHash(fmt.Sprintf("rt%d", i)), Summary: word.Draw(rt, "tsum"),
		})
	}
	if rapid.Bool().Draw(rt, "narr") {
		c.Narrative = word.Draw(rt, "narrative")
	}
	return c
}

// TestGoldenTruncationFixtures authors and verifies the SP-10 truncation goldens:
//
//	0002-full.json            Marshal(golden0002())
//	0003-truncated.json       Marshal of Truncate(0002, 1950, default tiers, exact estimator)
//	                          with the returned drops folded into .dropped, as 0004 does
//	0004-tier1-over-budget.json Marshal of Truncate(0004 input, 200) with the returned drops
//	                          folded into .dropped the way Finalize folds them, so the file
//	                          itself carries the budget_exceeded proof
//	contracts/.../input/oversized_checkpoint.json  the record-by-owner behaviour input
//	                          (same bytes as 0002-full.json)
func TestGoldenTruncationFixtures(t *testing.T) {
	est := newEstimator()

	full, err := checkpoint.Marshal(golden0002())
	require.NoError(t, err)

	// 0003: the walk stopping INSIDE the decisions stage — the shape the plan's fixture table
	// describes for this file ("narrative empty, pointers empty, open questions empty, 2
	// decisions, full tier 1").
	//
	// The plan reaches that shape at 900 tokens; against the shipped exact estimator it is not
	// reachable there, because tier 1 of the plan-shaped 0002 — two §8.5 eliminations with three
	// content hashes each — alone measures 1636 tokens (the 900 was calibrated to the superseded
	// 3.2-chars/token model). truncBudgetMidTier2 is the budget that reaches it now, and the
	// fixture is pinned to the SHAPE rather than to the stale number: a golden that stopped at
	// tier 1 would duplicate what 0004 already pins, leaving the mid-tier-2 stop — the one
	// outcome that shows importance ordering doing something more interesting than giving up —
	// with no golden at all. The 900-token behaviour is still pinned, by
	// TestTruncateReachesTierTwoOnlyAfterTierThree.
	//
	// Drops are folded into .dropped the way Finalize folds them, matching 0004, so the file
	// carries its own evidence of what was cut and why.
	truncated, drops := checkpoint.Truncate(golden0002(), truncBudgetMidTier2, tiersDefault(), est)
	require.NotEmpty(t, drops)
	require.Empty(t, truncated.Pointers.Files)
	require.Empty(t, truncated.Pointers.Tools)
	require.Empty(t, truncated.Narrative)
	require.Empty(t, truncated.OpenQuestions)
	require.Len(t, truncated.Decisions, 2, "the two newest decisions survive")
	require.Equal(t, golden0002().CurrentWork, truncated.CurrentWork, "next_step outlives the decisions")
	for _, d := range drops {
		require.NotEqual(t, "budget_exceeded", d.Kind, "the budget was met; this is not an overflow")
	}
	truncated.Dropped = append(truncated.Dropped, drops...)
	truncBytes, err := checkpoint.Marshal(truncated)
	require.NoError(t, err)

	// 0004: tier 1 alone is above the budget; the walk gives everything up and says so.
	in4 := golden0004Input()
	out4, drops4 := checkpoint.Truncate(in4, golden0004Budget, tiersDefault(), est)
	require.Greater(t, int(mustSize(out4, est)), int(golden0004Budget),
		"fixture sanity: tier 1 alone is above the budget")
	require.Equal(t, in4.Invariants, out4.Invariants)
	require.Equal(t, in4.UserIntent, out4.UserIntent)
	require.Equal(t, in4.Eliminated, out4.Eliminated)
	require.Equal(t, "budget_exceeded", drops4[len(drops4)-1].Kind)
	out4.Dropped = append(out4.Dropped, drops4...)
	tier1Bytes, err := checkpoint.Marshal(out4)
	require.NoError(t, err)

	requireGoldenMatches(t, filepath.Join(truncGoldenDir, "0002-full.json"), full)
	requireGoldenMatches(t, filepath.Join(truncGoldenDir, "0003-truncated.json"), truncBytes)
	requireGoldenMatches(t, filepath.Join(truncGoldenDir, "0004-tier1-over-budget.json"), tier1Bytes)
	requireGoldenMatches(t, oversizedContractInput, full)

	// Every golden must round-trip Unmarshal -> Marshal byte-identically.
	for name, raw := range map[string][]byte{
		"0002-full.json": full, "0003-truncated.json": truncBytes, "0004-tier1-over-budget.json": tier1Bytes,
	} {
		back, err := checkpoint.Unmarshal(raw)
		require.NoError(t, err, name)
		again, err := checkpoint.Marshal(back)
		require.NoError(t, err, name)
		require.Equal(t, string(raw), string(again), "%s must round-trip byte-identically", name)
	}
}

// BenchmarkTruncate measures the full §6.9 walk — tier 3 exhausted, open questions and
// alternatives cut, decisions binary-searched — on the golden 0002 shape at the plan's 900-token
// budget. The SP-10 budget is < 5 ms/op.
func BenchmarkTruncate(b *testing.B) {
	est := newEstimator()
	tiers := tiersDefault()
	c := golden0002()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = checkpoint.Truncate(c, truncBudget900, tiers, est)
	}
}
