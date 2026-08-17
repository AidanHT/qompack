package eval_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/eval"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/stretchr/testify/require"
)

// writeSessions writes each session into dir the way the committed corpus is encoded.
func writeSessions(t *testing.T, dir string, sessions ...eval.Session) {
	t.Helper()
	for _, s := range sessions {
		b, err := json.MarshalIndent(s, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, s.ID+".json"), append(b, '\n'), 0o600))
	}
}

func deterministicOptions() eval.ReplayOptions {
	return eval.ReplayOptions{Deterministic: true, Budget: eval.DefaultKeepBudget, K: eval.DefaultHorizonK}
}

// TestLoad_ReadsSessionsSortedByFilename: the corpus is a set, but the load order decides the
// order scores accumulate in, so it has to be stable.
func TestLoad_ReadsSessionsSortedByFilename(t *testing.T) {
	dir := t.TempDir()
	writeSessions(t, dir,
		eval.Session{ID: "b-second", Synthetic: true},
		eval.Session{ID: "a-first", Synthetic: true},
		eval.Session{ID: "c-third", Synthetic: true},
	)

	got, err := eval.New(eval.Options{}).Load(dir)
	require.NoError(t, err)

	require.Equal(t, []string{"a-first", "b-second", "c-third"},
		[]string{got[0].ID, got[1].ID, got[2].ID})
}

// TestLoad_EmptyDirectoryIsNotFound: a corpus that silently loads zero sessions would produce a
// baseline over nothing, which is worse than an error.
func TestLoad_EmptyDirectoryIsNotFound(t *testing.T) {
	_, err := eval.New(eval.Options{}).Load(t.TempDir())
	require.ErrorIs(t, err, core.ErrNotFound)
}

// TestLoad_UnparseableFileIsFatal: a silently skipped fixture is a silently wrong baseline.
func TestLoad_UnparseableFileIsFatal(t *testing.T) {
	dir := t.TempDir()
	writeSessions(t, dir, eval.Session{ID: "good", Synthetic: true})
	require.NoError(t, os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{not json"), 0o600))

	_, err := eval.New(eval.Options{}).Load(dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "broken.json")
}

// TestLoad_UnknownFieldIsFatal: schema drift in a committed fixture must fail loudly rather than
// zeroing a field the baseline was computed from.
func TestLoad_UnknownFieldIsFatal(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "drift.json"),
		[]byte(`{"id":"x","turns":[],"unexpectedKey":1}`), 0o600))

	_, err := eval.New(eval.Options{}).Load(dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unexpectedKey")
}

// TestLoad_SkipsCorpusManifestByName: CORPUS.json lives beside the sessions and is a manifest, not
// a session. It is skipped by name, never by a heuristic that could swallow a real fixture.
func TestLoad_SkipsCorpusManifestByName(t *testing.T) {
	dir := t.TempDir()
	writeSessions(t, dir, eval.Session{ID: "only", Synthetic: true})
	require.NoError(t, os.WriteFile(filepath.Join(dir, "CORPUS.json"),
		[]byte(`{"generator":"eval.Synthesize/1","sessions":[]}`), 0o600))

	got, err := eval.New(eval.Options{}).Load(dir)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "only", got[0].ID)
}

// ── Replay ──────────────────────────────────────────────────────────────────────────────────

// repairSession has one file read before the compaction and four turns after it that each
// re-touch that file, so a policy keeping nothing must produce exactly four repairs.
func repairSession() eval.Session {
	res, _ := json.Marshal(map[string]int{"tokens": 900})
	turns := []eval.Turn{
		{Index: 0, Role: "user", Tokens: 100},
		{Index: 1, Role: "assistant", Tokens: 100, ToolCalls: []eval.ToolCall{
			{ID: "t1", Name: "FileRead", Paths: []string{"src/a.go"}, Result: res},
		}},
		{Index: 2, Role: "assistant", Tokens: 100},
	}
	for i := 3; i < 7; i++ {
		turns = append(turns, eval.Turn{
			Index: core.TurnIndex(i), Role: "assistant", Tokens: 100,
			ToolCalls: []eval.ToolCall{{
				ID: core.ToolUseID("r" + strconv.Itoa(i)), Name: "Edit", Paths: []string{"src/a.go"},
			}},
		})
	}
	return eval.Session{ID: "repair", Turns: turns, CompactionAt: []core.TurnIndex{2}}
}

// TestReplay_NullPolicyInjectsRepairForEveryDemand is the core of the deterministic estimator:
// every demand the keep-set failed to satisfy becomes work the agent has to redo.
func TestReplay_NullPolicyInjectsRepairForEveryDemand(t *testing.T) {
	s := repairSession()
	h := eval.New(eval.Options{})

	run, err := h.Replay(context.Background(), s, eval.NewNullPolicy(config.Defaults()), deterministicOptions())
	require.NoError(t, err)

	base := eval.BaselineRun(s)
	require.Len(t, run.Actions, len(base.Actions)+4,
		"four post-compaction turns re-touch src/a.go, and null kept none of it")
	require.Equal(t, "compacted", run.Branch)
	require.Equal(t, "null", run.Policy)
	require.Equal(t, "repair", run.Session)
}

// TestReplay_OracleNeedsNoRepairsWhenItFits: with room to keep the demanded block, the clairvoyant
// policy produces a branch identical to the logged one — which is the ceiling being a real ceiling.
func TestReplay_OracleNeedsNoRepairsWhenItFits(t *testing.T) {
	s := repairSession()
	h := eval.New(eval.Options{})

	oracle, err := h.Replay(context.Background(), s, eval.NewOraclePolicy(config.Defaults()), deterministicOptions())
	require.NoError(t, err)
	null, err := h.Replay(context.Background(), s, eval.NewNullPolicy(config.Defaults()), deterministicOptions())
	require.NoError(t, err)

	require.Len(t, oracle.Actions, len(eval.BaselineRun(s).Actions))
	require.Less(t, len(oracle.Actions), len(null.Actions))
}

// TestReplay_PopulatesTheFieldsScoreRunNeeds: ScoreRun and Compare are handed a Run and no
// Session, so these five fields are the whole reason they are computable at all.
func TestReplay_PopulatesTheFieldsScoreRunNeeds(t *testing.T) {
	s := repairSession()

	run, err := eval.New(eval.Options{}).
		Replay(context.Background(), s, eval.NewStockPolicy(config.Defaults()), deterministicOptions())
	require.NoError(t, err)

	require.Equal(t, []core.TurnIndex{2}, run.At)
	require.Len(t, run.Demands, 1)
	require.Len(t, run.PrefixTokens, 1)
	require.Len(t, run.Keeps, 1)
	require.Len(t, run.PauseMS, 1)
	require.Len(t, run.ResidualSpan, 1)
	require.Len(t, run.FirstTurnAfterMS, 1)
	require.Equal(t, core.TurnIndex(2), run.FirstCompactionTurn)
	require.Equal(t, eval.DefaultHorizonK, run.Horizon)
	require.NotEmpty(t, run.Demands[0], "the fixture exists to produce demands")
}

// TestBaselineRun_IsTheLoggedGroundTruth: the uncompacted branch is not a replay at all, so it
// carries no keep-sets and no modelled latency.
func TestBaselineRun_IsTheLoggedGroundTruth(t *testing.T) {
	s := repairSession()

	base := eval.BaselineRun(s)

	require.Equal(t, "uncompacted", base.Branch)
	require.Len(t, base.Actions, len(s.Turns))
	require.Empty(t, base.Keeps)
	require.Empty(t, base.PauseMS)
	require.Equal(t, core.TurnIndex(2), base.FirstCompactionTurn)
	require.Equal(t, "FileRead", base.Actions[1].Tool)
	require.Equal(t, []string{"src/a.go"}, base.Actions[1].Paths)
}

// TestBaselineRun_NoCompactionIsMinusOne: -1 is what tells Compare there are no branches to
// compare, and it must not be confused with turn 0.
func TestBaselineRun_NoCompactionIsMinusOne(t *testing.T) {
	base := eval.BaselineRun(eval.Session{ID: "flat", Turns: []eval.Turn{{Index: 0, Role: "user"}}})
	require.Equal(t, core.TurnIndex(-1), base.FirstCompactionTurn)
}

// TestReplay_LiveModeRefusedWithoutEnv: live mode must never silently degrade to deterministic,
// or a CI number would be mistaken for a model-backed one.
func TestReplay_LiveModeRefusedWithoutEnv(t *testing.T) {
	_, err := eval.New(eval.Options{}).
		Replay(context.Background(), repairSession(), eval.NewNullPolicy(config.Defaults()),
			eval.ReplayOptions{Deterministic: false})

	require.Error(t, err)
	require.Contains(t, err.Error(), "QOMPACK_EVAL_LIVE")
	require.ErrorIs(t, err, core.ErrNotImplemented,
		"evaltest's shape block accepts only the four core sentinels")
}

// TestReplay_LiveRunnerAbsent: with the environment gate open and no runner installed, the error
// has to name the thing that is missing rather than the gate that let it through.
func TestReplay_LiveRunnerAbsent(t *testing.T) {
	t.Setenv("QOMPACK_EVAL_LIVE", "1")

	_, err := eval.New(eval.Options{}).
		Replay(context.Background(), repairSession(), eval.NewNullPolicy(config.Defaults()),
			eval.ReplayOptions{Deterministic: false})

	require.Error(t, err)
	require.Contains(t, err.Error(), "LiveRunner")
	require.ErrorIs(t, err, core.ErrNotImplemented)
}

// TestReplay_DeterministicAcrossRuns: the same session, policy and options must produce byte-equal
// Runs, or every number downstream is noise.
func TestReplay_DeterministicAcrossRuns(t *testing.T) {
	s := repairSession()
	for _, name := range eval.PolicyNames() {
		t.Run(name, func(t *testing.T) {
			p, ok := eval.PolicyByName(name, config.Defaults())
			require.True(t, ok)

			first, err := eval.New(eval.Options{}).Replay(context.Background(), s, p, deterministicOptions())
			require.NoError(t, err)
			second, err := eval.New(eval.Options{}).Replay(context.Background(), s, p, deterministicOptions())
			require.NoError(t, err)

			require.Equal(t, first, second)
		})
	}
}

// TestReplay_HorizonRespected: with K = 5 and a compaction at 10 the demand window is turns 11-14,
// so a re-touch at turn 20 produces no repair.
func TestReplay_HorizonRespected(t *testing.T) {
	res, _ := json.Marshal(map[string]int{"tokens": 900})
	turns := make([]eval.Turn, 40)
	for i := range turns {
		turns[i] = eval.Turn{Index: core.TurnIndex(i), Role: "assistant", Tokens: 100}
	}
	turns[1].ToolCalls = []eval.ToolCall{
		{ID: "t1", Name: "FileRead", Paths: []string{"src/a.go"}, Result: res},
	}
	turns[20].ToolCalls = []eval.ToolCall{{ID: "far", Name: "Edit", Paths: []string{"src/a.go"}}}
	s := eval.Session{ID: "horizon", Turns: turns, CompactionAt: []core.TurnIndex{10}}

	o := deterministicOptions()
	o.K = 5
	run, err := eval.New(eval.Options{}).
		Replay(context.Background(), s, eval.NewNullPolicy(config.Defaults()), o)
	require.NoError(t, err)

	require.Equal(t, 5, run.Horizon)
	require.Empty(t, run.Demands[0], "turn 20 is outside the window turns 11-14")
	require.Len(t, run.Actions, len(eval.BaselineRun(s).Actions), "no demands, no repairs")
}

// decisionLossSession mints a decision before the compaction and refers back to it after, so a
// policy that drops the decision block makes the later turn forget what it had decided.
func decisionLossSession() eval.Session {
	turns := make([]eval.Turn, 8)
	for i := range turns {
		turns[i] = eval.Turn{Index: core.TurnIndex(i), Role: "assistant", Tokens: 100}
	}
	turns[1].Text = "settled on the pooled client [decision:dec_pool]"
	turns[5].Text = "continuing under [decision:dec_pool]"
	return eval.Session{ID: "decision-loss", Turns: turns, CompactionAt: []core.TurnIndex{3}}
}

// TestCompare_GoldenScenarios freezes the divergence numbers for the three canonical outcomes, so
// a change in the estimator shows up as a reviewable diff rather than as a baseline that quietly
// moved.
func TestCompare_GoldenScenarios(t *testing.T) {
	h := eval.New(eval.Options{})
	ctx := context.Background()

	cases := []struct {
		golden  string
		session eval.Session
		policy  eval.Policy
	}{
		{"divergence/identical.json", repairSession(), eval.NewOraclePolicy(config.Defaults())},
		{"divergence/repairs.json", repairSession(), eval.NewNullPolicy(config.Defaults())},
		{"divergence/decision-loss.json", decisionLossSession(), eval.NewNullPolicy(config.Defaults())},
	}
	for _, tc := range cases {
		t.Run(tc.golden, func(t *testing.T) {
			run, err := h.Replay(ctx, tc.session, tc.policy, deterministicOptions())
			require.NoError(t, err)
			testutil.GoldenJSON(t, tc.golden, h.Compare(eval.BaselineRun(tc.session), run))
		})
	}
}

// TestCompare_DecisionLossIsObservable is the assertion behind the decision-loss golden: without
// it the golden would freeze whatever the code happens to produce, including nothing.
func TestCompare_DecisionLossIsObservable(t *testing.T) {
	s := decisionLossSession()
	run, err := eval.New(eval.Options{}).
		Replay(context.Background(), s, eval.NewNullPolicy(config.Defaults()), deterministicOptions())
	require.NoError(t, err)

	got := eval.New(eval.Options{}).Compare(eval.BaselineRun(s), run)
	require.Equal(t, 0.0, got.DecisionPreservation, "dec_pool was minted before the compaction and dropped")
	require.False(t, got.SameDecision)
}

// TestReplay_MultipleCompactionsAreParallelSlices: At, Demands, PrefixTokens and Keeps index the
// same event, and ScoreRun relies on that alignment.
func TestReplay_MultipleCompactionsAreParallelSlices(t *testing.T) {
	turns := make([]eval.Turn, 40)
	for i := range turns {
		turns[i] = eval.Turn{Index: core.TurnIndex(i), Role: "assistant", Tokens: 200}
	}
	s := eval.Session{ID: "multi", Turns: turns, CompactionAt: []core.TurnIndex{10, 20, 30}}

	run, err := eval.New(eval.Options{}).
		Replay(context.Background(), s, eval.NewStockPolicy(config.Defaults()), deterministicOptions())
	require.NoError(t, err)

	require.Equal(t, []core.TurnIndex{10, 20, 30}, run.At)
	require.Len(t, run.Demands, 3)
	require.Len(t, run.PrefixTokens, 3)
	require.Len(t, run.Keeps, 3)
	require.Equal(t, 2000, int(run.PrefixTokens[0]), "10 turns x 200 tokens")
	require.Equal(t, 4000, int(run.PrefixTokens[1]))
}
