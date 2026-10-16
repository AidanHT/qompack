package main

// Tests of W6 task 2 in the live-eval driver: the Stage A confirmatory path (item 14), and what the
// run records and decides for the four arms (items 2, 5 and 6, amendment 4).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/eval"
)

// registerStageATestEntry adds a test-only four-arm pre-registration of the test task set for the
// length of the test: its hash, its fixture tree, --plugin-dir, claude-opus-5-5 with the alias opus,
// the four arms with the set's own per-arm trials and both bundles.
func registerStageATestEntry(t *testing.T) eval.LiveTaskSet {
	t.Helper()
	ts, raw, err := eval.LoadLiveTaskSet(liveTestTasksV7)
	require.NoError(t, err)
	sum := sha256.Sum256(raw)
	tree, err := eval.TreeManifestSHA256(filepath.Dir(liveTestTasksV7), ts.FixtureTreeDirs()...)
	require.NoError(t, err)
	eval.LivePreregistrations[ts.ID] = eval.LivePreregistration{
		Document: "test-only", TaskSetFile: "testdata/eval/live/tasks-v7-harness-test.json",
		TaskSetSHA256: hex.EncodeToString(sum[:]), FixtureTreeSHA256: tree, Install: liveInstallPluginDir,
		Model: "claude-opus-5-5", ModelContingency: "opus", RequiredFixed: []string{"C1.12"},
		Arms: eval.LiveArms, ArmTrialsPerTask: ts.Analysis.ArmTrialsPerTask,
		Bundles: []string{eval.LiveBundleClassic, eval.LiveBundleMods},
	}
	t.Cleanup(func() { delete(eval.LivePreregistrations, ts.ID) })
	return ts
}

// TestRunLiveEval_StageAConfirmatoryPath (task 1 review m-1): with a four-arm pre-registration entry,
// a plan of its whole design reports the plan-time preconditions met and --confirmatory accepts it;
// any one departure — an arm left out, an arm's trials per task, the mods bundle missing, the
// held-out tasks left out — makes --confirmatory refuse, naming it.
func TestRunLiveEval_StageAConfirmatoryPath(t *testing.T) {
	registerStageATestEntry(t)
	env := fakeLiveEnv(t, nil)
	classic, mods := fakeBundle(t), fakeModsBundle(t, "deadbeef")
	whole := func() liveOptions {
		return liveOptions{
			tasksFile: liveTestTasksV7, rates: liveTestRates, arms: eval.LiveArms, out: t.TempDir(),
			install: liveInstallPluginDir, bundle: classic, bundleMods: mods, includeHeldOut: true,
			defectsAttested: true, openDefects: []string{}, dryRun: true, idleExit: 1800,
			stepTimeout: liveDefaultStepTimeout,
		}
	}
	var out bytes.Buffer
	require.NoError(t, runLiveEval(context.Background(), whole(), env, &out))
	require.Contains(t, out.String(), "live-eval: confirmatory preconditions at plan time: met")
	require.NotContains(t, out.String(), "not the confirmatory design")

	o := whole()
	o.confirmatory = true
	out.Reset()
	require.NoError(t, runLiveEval(context.Background(), o, env, &out), "--confirmatory accepts the whole design")
	require.Contains(t, out.String(), "plan: pilot-codeword/steer/2")

	o = whole()
	o.confirmatory, o.model = true, "opus"
	out.Reset()
	require.NoError(t, runLiveEval(context.Background(), o, env, &out), "the entry's contingency alias is the design's model")

	for name, c := range map[string]struct {
		mutate func(*liveOptions)
		want   string
	}{
		"an arm left out": {func(o *liveOptions) { o.arms = []string{"stock", "classic", "steer"} }, "missing generic"},
		"trials per task": {func(o *liveOptions) { o.trials = 3 }, "ran 3 trials per task on the classic arm, not the pre-registered 2"},
		"held-out left":   {func(o *liveOptions) { o.includeHeldOut = false }, "it excluded the held-out tasks"},
		"no steer bundle": {func(o *liveOptions) { o.arms, o.bundleMods = []string{"stock", "classic", "generic"}, "" }, "names no --bundle-mods"},
	} {
		o := whole()
		c.mutate(&o)
		out.Reset()
		require.NoError(t, runLiveEval(context.Background(), o, env, &out), name)
		require.Contains(t, out.String(), "live-eval: not the confirmatory design: ", name)
		require.Contains(t, out.String(), c.want, name)

		o.confirmatory = true
		out.Reset()
		err := runLiveEval(context.Background(), o, env, &out)
		require.ErrorContains(t, err, "--confirmatory", name)
		require.ErrorContains(t, err, c.want, name)
		require.NotContains(t, out.String(), "plan: ", "%s: nothing is planned", name)
	}
}

// runFourArmPilot runs the test task set's six trials in all four arms against the scripted pilot
// host, with opts varying what the host does, and returns the run directory.
func runFourArmPilot(t *testing.T, opts pilotHostOpts, mutate func(*liveEnv)) string {
	t.Helper()
	home := t.TempDir()
	env := fakeLiveEnv(t, nil)
	env.home = home
	env.run = scriptedPilotHostWith(t, home, opts)
	if mutate != nil {
		mutate(env)
	}
	t.Setenv(liveEvalGateEnv, "1")
	o := liveOptions{
		tasksFile: liveTestTasksV7, rates: liveTestRates, arms: eval.LiveArms, out: t.TempDir(),
		install: liveInstallPluginDir, bundle: fakeBundle(t), bundleMods: fakeModsBundle(t, "deadbeef"),
		idleExit: 1800, defectsAttested: true, stepTimeout: liveDefaultStepTimeout,
	}
	var log bytes.Buffer
	require.NoError(t, runLiveEval(context.Background(), o, env, &log), log.String())
	return o.out
}

// TestRunLiveEval_RecordsTheTaskDomain: each trial records its task's domain and summary.md breaks
// the outcomes down by it (W6 item 6).
func TestRunLiveEval_RecordsTheTaskDomain(t *testing.T) {
	out := runFourArmPilot(t, pilotHostOpts{}, nil)
	var rec eval.LiveTrial
	readJSON(t, filepath.Join(out, "trials", "pilot-codeword", "steer", "01", "trial.json"), &rec)
	require.Equal(t, "harness-validation", rec.Domain)
	var sum eval.LiveSummary
	readJSON(t, filepath.Join(out, "summary.json"), &sum)
	require.Len(t, sum.ByDomain["harness-validation"], 4)
	md, err := os.ReadFile(filepath.Join(out, "summary.md"))
	require.NoError(t, err)
	require.Contains(t, string(md), "## By domain")
	require.Contains(t, string(md), "| harness-validation | steer | 2 |")
}
