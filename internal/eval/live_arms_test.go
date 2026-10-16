package eval_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

// liveTasksV7Test is the test-only four-arm task set: the pilot task in the Stage A shape.
const liveTasksV7Test = "tasks-v7-harness-test.json"

// TestLiveArmSpec_EachArmDeclaresItsExpectedState pins plan section 9.1's table: stock loads no
// plugin; classic and generic load the bundle without the mod, with both mod switches off; steer
// loads the mods bundle with both switches on; only generic carries the CLAUDE.md section; every
// Qompack arm gets a pre-created store.
func TestLiveArmSpec_EachArmDeclaresItsExpectedState(t *testing.T) {
	require.Equal(t, []string{"stock", "classic", "generic", "steer"}, eval.LiveArms)
	want := map[string]eval.LiveArmState{
		"stock": {Arm: "stock"},
		"classic": {
			Arm: "classic", Plugin: true, Bundle: eval.LiveBundleClassic, StorePrecreated: true,
		},
		"generic": {
			Arm: "generic", Plugin: true, Bundle: eval.LiveBundleClassic, StorePrecreated: true,
			ClaudeMDCompactSection: true,
		},
		"steer": {
			Arm: "steer", Plugin: true, Bundle: eval.LiveBundleMods, StorePrecreated: true,
			ModEnabled: true, ModSteering: true,
		},
	}
	for _, arm := range eval.LiveArms {
		got, ok := eval.LiveArmSpec(arm)
		require.True(t, ok, arm)
		require.Equal(t, want[arm], got, arm)
	}
	_, ok := eval.LiveArmSpec(eval.ArmQompack)
	require.False(t, ok, "the two-arm design's qompack arm is not a Stage A arm")
	_, ok = eval.LiveArmSpec("")
	require.False(t, ok)
}

// TestLiveTaskSet_V7ShapeCarriesPerArmTrialsAndTextG: the test-only four-arm set loads, holds its
// per-arm trials per task and text G in the hashed analysis block, and the legacy sets still answer
// one count for every arm.
func TestLiveTaskSet_V7ShapeCarriesPerArmTrialsAndTextG(t *testing.T) {
	ts, _, err := eval.LoadLiveTaskSet(liveTaskFile(liveTasksV7Test))
	require.NoError(t, err)
	require.Equal(t, map[string]int{"stock": 1, "classic": 2, "generic": 1, "steer": 2}, ts.Analysis.ArmTrialsPerTask)
	require.Equal(t, 2, ts.Analysis.TrialsFor(eval.ArmSteer))
	require.Equal(t, 1, ts.Analysis.TrialsFor(eval.ArmStock))
	require.Zero(t, ts.Analysis.TrialsFor("nope"))
	require.Contains(t, ts.Analysis.GenericCompactInstructions, "When compacting, reproduce verbatim")
	require.Equal(t, "harness-validation", ts.Tasks[0].Domain, "every task of a four-arm set names its domain (W6 item 6)")

	v2, _, err := eval.LoadLiveTaskSet(liveTaskFile(liveTasksV2))
	require.NoError(t, err)
	require.Equal(t, 2, v2.Analysis.TrialsFor(eval.ArmStock), "a legacy set's one count serves every arm")
	require.Equal(t, 2, v2.Analysis.TrialsFor(eval.ArmSteer))
}

// TestLiveTaskSet_V7ShapeValidation: per-arm counts name only Stage A arms with positive counts, a
// set with neither count is refused, a generic count needs text G, and every task of a four-arm set
// declares a recovery check (plan W6 item 3), so every trial counts toward the primary comparison.
func TestLiveTaskSet_V7ShapeValidation(t *testing.T) {
	base := func() map[string]any {
		raw, err := os.ReadFile(liveTaskFile(liveTasksV7Test))
		require.NoError(t, err)
		var m map[string]any
		require.NoError(t, json.Unmarshal(raw, &m))
		return m
	}
	obj := func(v any) map[string]any {
		m, ok := v.(map[string]any)
		require.True(t, ok, "%T is not an object", v)
		return m
	}
	arr := func(v any) []any {
		a, ok := v.([]any)
		require.True(t, ok, "%T is not an array", v)
		return a
	}
	analysis := func(m map[string]any) map[string]any { return obj(m["analysis"]) }
	counts := func(m map[string]any) map[string]any { return obj(analysis(m)["arm_trials_per_task"]) }
	task := func(m map[string]any) map[string]any { return obj(arr(m["tasks"])[0]) }

	for name, c := range map[string]struct {
		mutate func(m map[string]any)
		want   string
	}{
		"legacy arm":   {func(m map[string]any) { counts(m)["qompack"] = 1 }, `arm_trials_per_task names "qompack"`},
		"zero count":   {func(m map[string]any) { counts(m)["steer"] = 0 }, "must be positive"},
		"no count":     {func(m map[string]any) { delete(analysis(m), "arm_trials_per_task") }, "trials_per_arm or arm_trials_per_task"},
		"both counts":  {func(m map[string]any) { analysis(m)["trials_per_arm"] = 2 }, "not both"},
		"no text G":    {func(m map[string]any) { delete(analysis(m), "generic_compact_instructions") }, "generic_compact_instructions"},
		"blank text G": {func(m map[string]any) { analysis(m)["generic_compact_instructions"] = "  " }, "generic_compact_instructions"},
		"no domain":    {func(m map[string]any) { delete(task(m), "domain") }, "declares no domain"},
		"bad domain":   {func(m map[string]any) { task(m)["domain"] = "two words" }, "domain"},
		"no recovery check": {func(m map[string]any) {
			var kept []any
			for _, c := range arr(task(m)["checks"]) {
				if obj(c)["outcome"] != "recovery" {
					kept = append(kept, c)
				}
			}
			task(m)["checks"] = kept
		}, "declares no recovery check"},
	} {
		m := base()
		c.mutate(m)
		raw, err := json.Marshal(m)
		require.NoError(t, err)
		_, err = eval.ParseLiveTaskSet(raw)
		require.Error(t, err, name)
		require.Contains(t, err.Error(), c.want, name)
	}

	// Without a generic count, text G is not required.
	m := base()
	delete(counts(m), "generic")
	delete(analysis(m), "generic_compact_instructions")
	raw, err := json.Marshal(m)
	require.NoError(t, err)
	_, err = eval.ParseLiveTaskSet(raw)
	require.NoError(t, err)
}
