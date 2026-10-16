package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/qompack/qompack/internal/eval"
	"github.com/qompack/qompack/internal/modwire"
	"github.com/stretchr/testify/require"
)

// liveTestTasksV7 is the test-only four-arm task set: the pilot task in the Stage A shape.
const liveTestTasksV7 = "../../testdata/eval/live/tasks-v7-harness-test.json"

// TestParseLiveFlags_StageAArms: the four Stage A arms replace stock/qompack and are the default;
// the two-arm design's qompack arm is refused; --bundle-mods and --order-seed are flags; every
// Qompack arm's daemon idles out at the product default of 1800 s unless told otherwise.
func TestParseLiveFlags_StageAArms(t *testing.T) {
	o, err := parseLiveFlags(nil)
	require.NoError(t, err)
	require.Equal(t, []string{"stock", "classic", "generic", "steer"}, o.arms)
	require.Equal(t, 1800, o.idleExit)

	o, err = parseLiveFlags([]string{"--arms", "steer,stock", "--bundle-mods", "m", "--order-seed", "7"})
	require.NoError(t, err)
	require.Equal(t, []string{"steer", "stock"}, o.arms)
	require.Equal(t, "m", o.bundleMods)
	require.Equal(t, int64(7), o.orderSeed)

	for _, bad := range [][]string{{"--arms", "qompack"}, {"--arms", "stock,stock"}} {
		_, err := parseLiveFlags(bad)
		require.Error(t, err, "%v", bad)
	}
}

// TestPlanLiveTrials_BalancedInterleaving: with unequal per-arm counts every (task, arm) pair runs
// exactly its count; an arm with n of R trials runs its k-th trial in round ceil((2k-1)R/(2n)), the
// middle of its share of the schedule, so no arm is pushed early or late and each arm's mean round is
// within one round of the schedule's; trial numbers of a pair run in order; and the order within each
// block (one round) is a seeded shuffle, the same for the same seed.
func TestPlanLiveTrials_BalancedInterleaving(t *testing.T) {
	tasks := []eval.LiveTask{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	arms := []string{"stock", "classic", "generic", "steer"}
	counts := map[string]int{"stock": 3, "classic": 14, "generic": 10, "steer": 14}
	const rounds = 14
	plan := planLiveTrials(tasks, arms, counts, 42)

	roundOf := func(arm string, k int) int {
		n := counts[arm]
		return ((2*k-1)*rounds + 2*n - 1) / (2 * n)
	}
	wantRounds := func(arm string) []int {
		var out []int
		for k := 1; k <= counts[arm]; k++ {
			out = append(out, roundOf(arm, k))
		}
		return out
	}
	require.Equal(t, []int{3, 7, 12}, wantRounds("stock"), "stock's three trials sit mid-share")
	require.Equal(t, []int{1, 3, 4, 5, 7, 8, 10, 11, 12, 14}, wantRounds("generic"))
	overall := float64(rounds+1) / 2
	for _, arm := range arms {
		sum := 0
		for _, r := range wantRounds(arm) {
			sum += r
		}
		mean := float64(sum) / float64(counts[arm])
		require.InDelta(t, overall, mean, 1, "%s's mean round %.2f is within one round of %.2f", arm, mean, overall)
	}

	require.Len(t, plan, 3*(3+14+10+14))
	got := map[string]int{}
	last := map[string]int{}
	prevRound := 0
	for _, p := range plan {
		key := p.Task + "/" + p.Arm
		got[key]++
		require.Equal(t, last[key]+1, p.Trial, "%s runs its trials in order", key)
		last[key] = p.Trial
		r := roundOf(p.Arm, p.Trial)
		require.GreaterOrEqual(t, r, prevRound, "%s/%d runs in round %d, block by block", key, p.Trial, r)
		prevRound = r
	}
	for _, task := range tasks {
		for _, arm := range arms {
			require.Equal(t, counts[arm], got[task.ID+"/"+arm], "%s/%s", task.ID, arm)
		}
	}

	require.Equal(t, plan, planLiveTrials(tasks, arms, counts, 42), "the same seed gives the same order")
	require.NotEqual(t, plan, planLiveTrials(tasks, arms, counts, 43), "another seed gives another order")

	// Equal counts: every block is one round of every pair, in some order.
	eq := planLiveTrials(tasks, []string{"stock", "steer"}, map[string]int{"stock": 2, "steer": 2}, 1)
	require.Len(t, eq, 12)
	for b := range 2 {
		var keys []string
		for _, p := range eq[b*6 : b*6+6] {
			require.Equal(t, b+1, p.Trial)
			keys = append(keys, p.Task+"/"+p.Arm)
		}
		slices.Sort(keys)
		require.Equal(t, []string{"a/steer", "a/stock", "b/steer", "b/stock", "c/steer", "c/stock"}, keys)
	}
}

// TestLiveSessionEnv_PerArm: every arm's session has the auto-updater off, the claude.ai MCP servers
// off and the daemon idle exit; every Qompack arm sets both mod switches, steer on and the others
// off; stock sets no mod switch. The check refuses an environment that does not say what the arm
// declares.
func TestLiveSessionEnv_PerArm(t *testing.T) {
	spec := func(arm string) eval.LiveArmState {
		s, ok := eval.LiveArmSpec(arm)
		require.True(t, ok)
		return s
	}
	base := []string{"ENABLE_CLAUDEAI_MCP_SERVERS=false", "DISABLE_AUTOUPDATER=1", "QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=1800"}
	require.Equal(t, base, liveSessionEnv(spec("stock"), 1800))
	require.Equal(t, append(slices.Clone(base), "QOMPACK_RUNTIME__MOD__ENABLED=false", "QOMPACK_RUNTIME__MOD__STEERING=false"),
		liveSessionEnv(spec("classic"), 1800))
	require.Equal(t, liveSessionEnv(spec("classic"), 1800), liveSessionEnv(spec("generic"), 1800))
	require.Equal(t, append(slices.Clone(base), "QOMPACK_RUNTIME__MOD__ENABLED=true", "QOMPACK_RUNTIME__MOD__STEERING=true"),
		liveSessionEnv(spec("steer"), 1800))

	for _, arm := range eval.LiveArms {
		require.NoError(t, checkLiveArmEnv(spec(arm), 1800, liveSessionEnv(spec(arm), 1800)), arm)
	}
	require.ErrorContains(t, checkLiveArmEnv(spec("steer"), 1800, liveSessionEnv(spec("classic"), 1800)),
		"QOMPACK_RUNTIME__MOD__ENABLED")
	require.ErrorContains(t, checkLiveArmEnv(spec("stock"), 1800, liveSessionEnv(spec("steer"), 1800)),
		"QOMPACK_RUNTIME__MOD__ENABLED")
	require.ErrorContains(t, checkLiveArmEnv(spec("classic"), 1800, liveSessionEnv(spec("classic"), 120)),
		"IDLEEXITSECONDS")
	require.ErrorContains(t, checkLiveArmEnv(spec("stock"), 1800, base[:2]), "IDLEEXITSECONDS")
	require.ErrorContains(t, checkLiveArmEnv(spec("stock"), 1800, base[1:]), "ENABLE_CLAUDEAI_MCP_SERVERS")
	require.ErrorContains(t, checkLiveArmEnv(spec("stock"), 1800, []string{base[0], base[2]}), "DISABLE_AUTOUPDATER")
}

// TestApplyLiveArmProject: generic's root CLAUDE.md gains the `# Compact instructions` section with
// text G — created when absent, appended otherwise — and every Qompack arm gets an empty .qompack/;
// stock's project is left as the fixture made it.
func TestApplyLiveArmProject(t *testing.T) {
	spec := func(arm string) eval.LiveArmState {
		s, _ := eval.LiveArmSpec(arm)
		return s
	}
	const g = "Keep every identifier."

	p := t.TempDir()
	require.NoError(t, applyLiveArmProject(p, spec("generic"), g))
	md, err := os.ReadFile(filepath.Join(p, "CLAUDE.md"))
	require.NoError(t, err)
	require.Equal(t, "# Compact instructions\n\n"+g+"\n", string(md))
	require.DirExists(t, filepath.Join(p, ".qompack"))
	entries, err := os.ReadDir(filepath.Join(p, ".qompack"))
	require.NoError(t, err)
	require.Empty(t, entries, "the store is pre-created empty")

	p = t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(p, "CLAUDE.md"), []byte("# Project\n\nUse tabs."), 0o600))
	require.NoError(t, applyLiveArmProject(p, spec("generic"), g))
	md, err = os.ReadFile(filepath.Join(p, "CLAUDE.md"))
	require.NoError(t, err)
	require.Equal(t, "# Project\n\nUse tabs.\n\n# Compact instructions\n\n"+g+"\n", string(md))

	for _, arm := range []string{"classic", "steer"} {
		p = t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(p, "CLAUDE.md"), []byte("x\n"), 0o600))
		require.NoError(t, applyLiveArmProject(p, spec(arm), g))
		md, err = os.ReadFile(filepath.Join(p, "CLAUDE.md"))
		require.NoError(t, err)
		require.Equal(t, "x\n", string(md), arm)
		require.DirExists(t, filepath.Join(p, ".qompack"), arm)
	}

	p = t.TempDir()
	require.NoError(t, applyLiveArmProject(p, spec("stock"), g))
	require.NoFileExists(t, filepath.Join(p, "CLAUDE.md"))
	require.NoDirExists(t, filepath.Join(p, ".qompack"))

	require.ErrorContains(t, applyLiveArmProject(t.TempDir(), spec("generic"), " "), "text G")
}

// TestLiveSetAside: a configuration-discarded or limit-cut attempt is set aside in its slot under
// trial.<kind>-<n>.json, n counting per kind from 1, with the attempt's own files renamed beside it,
// and never as trial.json, the only name a scored trial has.
func TestLiveSetAside(t *testing.T) {
	slot := t.TempDir()
	write := func() {
		for _, f := range []string{"stream.jsonl", "stderr.txt", "invocation.json"} {
			require.NoError(t, os.WriteFile(filepath.Join(slot, f), []byte(f), 0o600))
		}
	}
	write()
	p, err := liveSetAside(slot, liveSlotDiscarded, eval.LiveTrial{ConfigurationError: "first"})
	require.NoError(t, err)
	require.Equal(t, filepath.Join(slot, "trial.discarded-1.json"), p)
	write()
	p, err = liveSetAside(slot, liveSlotDiscarded, eval.LiveTrial{ConfigurationError: "second"})
	require.NoError(t, err)
	require.Equal(t, filepath.Join(slot, "trial.discarded-2.json"), p)
	write()
	p, err = liveSetAside(slot, liveSlotLimited, eval.LiveTrial{})
	require.NoError(t, err)
	require.Equal(t, filepath.Join(slot, "trial.limited-1.json"), p)

	var names []string
	entries, err := os.ReadDir(slot)
	require.NoError(t, err)
	for _, e := range entries {
		names = append(names, e.Name())
	}
	require.ElementsMatch(t, []string{
		"trial.discarded-1.json", "stream.discarded-1.jsonl", "stderr.discarded-1.txt", "invocation.discarded-1.json",
		"trial.discarded-2.json", "stream.discarded-2.jsonl", "stderr.discarded-2.txt", "invocation.discarded-2.json",
		"trial.limited-1.json", "stream.limited-1.jsonl", "stderr.limited-1.txt", "invocation.limited-1.json",
	}, names)
	var rec eval.LiveTrial
	readJSON(t, filepath.Join(slot, "trial.discarded-2.json"), &rec)
	require.Equal(t, "second", rec.ConfigurationError)
}

// TestJudgeLiveHello: steer's hello file is right only when the project holds exactly one
// state/mod-hello-*.json, it parses, its session is the stream's, its host meets the mod's minimum
// and its served hello said steering true. Every other case names the problem.
func TestJudgeLiveHello(t *testing.T) {
	const sid = "0b6b8f42-6f0a-4d39-9c2e-7a4c1d2e3f40"
	write := func(t *testing.T, project, session string, h modwire.HelloFile) {
		t.Helper()
		dir := filepath.Join(project, ".qompack", modwire.StateDir)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		name, err := modwire.HelloFileName(session)
		require.NoError(t, err)
		b, err := h.Encode()
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), b, 0o600))
	}
	good := modwire.HelloFile{V: modwire.Version, SessionID: sid, HostVersion: modwire.MinHostVersion, Enabled: true, Steering: true, TS: 1}

	p := t.TempDir()
	write(t, p, sid, good)
	h := judgeLiveHello(p, sid)
	require.True(t, h.OK, h.Problem)
	require.Equal(t, eval.LiveHelloOutcome{
		Files: 1, SessionID: sid, HostVersion: modwire.MinHostVersion, Enabled: true, Steering: true, OK: true,
	}, *h)

	for name, c := range map[string]struct {
		setup func(t *testing.T, p string)
		want  string
	}{
		"missing": {func(*testing.T, string) {}, "no state/mod-hello-*.json"},
		"two": {func(t *testing.T, p string) {
			write(t, p, sid, good)
			other := good
			other.SessionID = "1b6b8f42-6f0a-4d39-9c2e-7a4c1d2e3f40"
			write(t, p, other.SessionID, other)
		}, "2 state/mod-hello-*.json"},
		"another session": {func(t *testing.T, p string) {
			other := good
			other.SessionID = "1b6b8f42-6f0a-4d39-9c2e-7a4c1d2e3f40"
			write(t, p, other.SessionID, other)
		}, "not the stream's"},
		"old host": {func(t *testing.T, p string) {
			old := good
			old.HostVersion, old.Enabled, old.Steering = "2.1.299", false, false
			write(t, p, sid, old)
		}, "below the mod's minimum"},
		"not steering": {func(t *testing.T, p string) {
			off := good
			off.Steering = false
			write(t, p, sid, off)
		}, "steering false"},
		"malformed": {func(t *testing.T, p string) {
			dir := filepath.Join(p, ".qompack", modwire.StateDir)
			require.NoError(t, os.MkdirAll(dir, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "mod-hello-"+sid+".json"), []byte(`{"v":1}`), 0o600))
		}, "does not parse"},
	} {
		p := t.TempDir()
		c.setup(t, p)
		h := judgeLiveHello(p, sid)
		require.False(t, h.OK, name)
		require.Contains(t, h.Problem, c.want, name)
	}
}

// TestRunLiveEval_TwoBundlesFromOneCommit: classic and generic need --bundle, steer --bundle-mods;
// the mod sources must be in the mods bundle's file list and absent from the other; both bundles
// come from one commit; generic needs the task set's text G.
func TestRunLiveEval_TwoBundlesFromOneCommit(t *testing.T) {
	env := fakeLiveEnv(t, nil)
	run := func(o liveOptions) (string, error) {
		var out bytes.Buffer
		o.tasksFile, o.rates, o.out, o.dryRun, o.defectsAttested = liveTestTasksV7, liveTestRates, t.TempDir(), true, true
		o.idleExit = 1800
		err := runLiveEval(context.Background(), o, env, &out)
		return out.String(), err
	}
	classic, mods := fakeBundle(t), fakeModsBundle(t, "deadbeef")

	out, err := run(liveOptions{arms: eval.LiveArms, install: liveInstallPluginDir, bundle: classic, bundleMods: mods})
	require.NoError(t, err)
	require.Contains(t, out, "live-eval: bundle (classic, generic) 9.9.9-test at commit deadbeef")
	require.Contains(t, out, "live-eval: bundle-mods (steer) 9.9.9-test at commit deadbeef")
	require.Contains(t, out, "--plugin-dir "+mods)

	_, err = run(liveOptions{arms: []string{"steer"}, install: liveInstallPluginDir, bundle: classic})
	require.ErrorContains(t, err, "--bundle-mods")
	_, err = run(liveOptions{arms: []string{"classic"}, install: liveInstallPluginDir, bundleMods: mods})
	require.ErrorContains(t, err, "--bundle <dir>")
	_, err = run(liveOptions{arms: []string{"classic"}, install: liveInstallPluginDir, bundle: mods})
	require.ErrorContains(t, err, "carries the mod sources")
	_, err = run(liveOptions{arms: []string{"steer"}, install: liveInstallPluginDir, bundleMods: classic})
	require.ErrorContains(t, err, "lacks the mod source")
	_, err = run(liveOptions{
		arms: []string{"classic", "steer"}, install: liveInstallPluginDir, bundle: classic, bundleMods: fakeModsBundle(t, "cafef00d"),
	})
	require.ErrorContains(t, err, "one commit")

	var o2 bytes.Buffer
	err = runLiveEval(context.Background(), liveOptions{
		tasksFile: liveTestPilot, rates: liveTestRates, out: t.TempDir(), dryRun: true, defectsAttested: true,
		arms: []string{"generic"}, install: liveInstallPluginDir, bundle: classic, idleExit: 1800,
	}, env, &o2)
	require.ErrorContains(t, err, "text G")
}

// TestRunLiveEval_FourArms drives every Stage A arm through the scripted host from the four-arm task
// set: each arm runs its pre-registered trials per task; each Qompack arm's session starts with an
// empty store, its own bundle and its environment; generic's CLAUDE.md carries text G and no other
// arm's does; steer's hello file is judged; every arm sends the same messages; plan.json and each
// trial record carry the arm's state and bundle identity.
func TestRunLiveEval_FourArms(t *testing.T) {
	home := t.TempDir()
	env := fakeLiveEnv(t, nil)
	env.home = home
	scripted := scriptedPilotHost(t, home)
	type seen struct {
		store    bool
		claudeMD string
		env      []string
		args     []string
		messages []string
	}
	var mu sync.Mutex
	byArm := map[string][]seen{}
	armOf := func(spec liveProcSpec) string {
		for _, kv := range spec.Env {
			if kv == "QOMPACK_RUNTIME__MOD__STEERING=true" {
				return "steer"
			}
		}
		md, _ := os.ReadFile(filepath.Join(spec.Dir, "CLAUDE.md"))
		switch {
		case len(md) > 0:
			return "generic"
		case slices.Contains(spec.Args, "--plugin-dir"):
			return "classic"
		}
		return "stock"
	}
	env.run = func(ctx context.Context, spec liveProcSpec) liveProcResult {
		_, statErr := os.Stat(filepath.Join(spec.Dir, ".qompack"))
		md, _ := os.ReadFile(filepath.Join(spec.Dir, "CLAUDE.md"))
		arm := armOf(spec)
		mu.Lock()
		byArm[arm] = append(byArm[arm], seen{statErr == nil, string(md), spec.Env, spec.Args, spec.Messages})
		mu.Unlock()
		return scripted(ctx, spec)
	}
	t.Setenv(liveEvalGateEnv, "1")
	classic, mods := fakeBundle(t), fakeModsBundle(t, "deadbeef")
	o := liveOptions{
		tasksFile: liveTestTasksV7, rates: liveTestRates, arms: eval.LiveArms, out: t.TempDir(),
		install: liveInstallPluginDir, bundle: classic, bundleMods: mods, idleExit: 1800, defectsAttested: true,
	}
	var log bytes.Buffer
	require.NoError(t, runLiveEval(context.Background(), o, env, &log), log.String())

	ts, _, err := eval.LoadLiveTaskSet(liveTestTasksV7)
	require.NoError(t, err)
	want := map[string]int{"stock": 1, "classic": 2, "generic": 1, "steer": 2}
	for arm, n := range want {
		require.Len(t, byArm[arm], n, arm)
		for _, s := range byArm[arm] {
			require.Equal(t, arm != "stock", s.store, "%s: the store exists before the session", arm)
			require.Equal(t, liveMessages(ts.Tasks[0]), s.messages, "%s sends the task's own messages", arm)
			spec, _ := eval.LiveArmSpec(arm)
			require.Equal(t, liveSessionEnv(spec, 1800), s.env, arm)
			if arm == "generic" {
				require.Equal(t, "# Compact instructions\n\n"+ts.Analysis.GenericCompactInstructions+"\n", s.claudeMD)
			} else {
				require.Empty(t, s.claudeMD, arm)
			}
			bundle := ""
			for i, a := range s.args {
				if a == "--plugin-dir" {
					bundle = s.args[i+1]
				}
			}
			switch arm {
			case "stock":
				require.Empty(t, bundle)
			case "steer":
				require.Equal(t, mods, bundle)
			default:
				require.Equal(t, classic, bundle, arm)
			}
		}
	}

	var plan eval.LivePlan
	readJSON(t, filepath.Join(o.out, "plan.json"), &plan)
	require.Equal(t, want, plan.ArmTrials)
	require.Equal(t, "2.1.280", plan.ClaudeCLIVersion)
	require.Equal(t, 1800, plan.DaemonIdleExitSeconds)
	require.NotZero(t, plan.OrderSeed)
	require.Len(t, plan.ArmStates, 4)
	require.Len(t, plan.Trials, 6)
	require.NotNil(t, plan.Plugin)
	require.NotNil(t, plan.PluginMods)
	require.False(t, plan.Plugin.Mods)
	require.True(t, plan.PluginMods.Mods)
	require.Equal(t, plan.Plugin.BundleSHA256, plan.ArmPlugins["classic"].BundleSHA256)
	require.Equal(t, plan.Plugin.BundleSHA256, plan.ArmPlugins["generic"].BundleSHA256)
	require.Equal(t, plan.PluginMods.BundleSHA256, plan.ArmPlugins["steer"].BundleSHA256)
	require.NotContains(t, plan.ArmPlugins, "stock")

	for arm, n := range want {
		for k := 1; k <= n; k++ {
			var rec eval.LiveTrial
			readJSON(t, filepath.Join(o.out, "trials", "pilot-codeword", arm, fmt.Sprintf("%02d", k), "trial.json"), &rec)
			require.Empty(t, rec.HarnessError, arm)
			require.Empty(t, rec.ConfigurationError, arm)
			require.NotNil(t, rec.ArmState, arm)
			require.Equal(t, arm, rec.ArmState.Arm)
			switch arm {
			case "stock":
				require.Nil(t, rec.Plugin)
				require.False(t, rec.PluginExpected)
			case "steer":
				require.Equal(t, plan.PluginMods.BundleSHA256, rec.Plugin.BundleSHA256)
				require.NotNil(t, rec.Hello)
				require.True(t, rec.Hello.OK, rec.Hello.Problem)
			default:
				require.Equal(t, plan.Plugin.BundleSHA256, rec.Plugin.BundleSHA256, arm)
				require.Nil(t, rec.Hello, arm)
			}
			if arm != "stock" {
				require.True(t, rec.PluginExpected, arm)
				require.True(t, rec.PluginLoaded, arm)
			}
		}
	}
}

// TestRunLiveEval_ConfigurationErrorIsDiscardedAndRerun: a trial whose host loaded Qompack from
// another source than the arm's install is a configuration error, not a scored trial: its record is
// set aside as trial.discarded-1.json and its slot runs again, and the re-run is the scored trial.
func TestRunLiveEval_ConfigurationErrorIsDiscardedAndRerun(t *testing.T) {
	home := t.TempDir()
	env := fakeLiveEnv(t, nil)
	env.home = home
	wrong := scriptedPilotHostWith(t, home, pilotHostOpts{source: "qompack@somewhere-else"})
	right := scriptedPilotHost(t, home)
	sessions := 0
	env.run = func(ctx context.Context, spec liveProcSpec) liveProcResult {
		sessions++
		if sessions == 1 {
			return wrong(ctx, spec)
		}
		return right(ctx, spec)
	}
	t.Setenv(liveEvalGateEnv, "1")
	o := liveOptions{
		tasksFile: liveTestPilot, rates: liveTestRates, arms: []string{"classic"}, out: t.TempDir(),
		install: liveInstallPluginDir, bundle: fakeBundle(t), idleExit: 1800, trials: 1, defectsAttested: true,
	}
	var log bytes.Buffer
	require.NoError(t, runLiveEval(context.Background(), o, env, &log), log.String())
	require.Equal(t, 2, sessions)

	slot := filepath.Join(o.out, "trials", "pilot-codeword", "classic", "01")
	var discarded, scored eval.LiveTrial
	readJSON(t, filepath.Join(slot, "trial.discarded-1.json"), &discarded)
	require.Contains(t, discarded.ConfigurationError, "qompack@somewhere-else")
	require.FileExists(t, filepath.Join(slot, "stream.discarded-1.jsonl"))
	readJSON(t, filepath.Join(slot, "trial.json"), &scored)
	require.Empty(t, scored.ConfigurationError)
	require.True(t, scored.PluginLoaded)

	var sum eval.LiveSummary
	readJSON(t, filepath.Join(o.out, "summary.json"), &sum)
	require.Equal(t, 1, sum.Arms["classic"].Trials, "the discarded attempt is not scored")
	require.Zero(t, sum.Arms["classic"].PluginMismatch)
	require.Contains(t, strings.Join(sum.Notes, "\n"), "1 configuration-discarded attempt(s)")
	require.Contains(t, log.String(), "configuration error")
}

// TestRunLiveEval_ThreeConfigurationErrorsStopTheRun: the third configuration error of a run stops
// it for investigation. Here the host's init line reports another version than the pinned one on
// every session; each attempt is set aside in its slot, nothing is scored, and the run reaches no
// verdict.
func TestRunLiveEval_ThreeConfigurationErrorsStopTheRun(t *testing.T) {
	home := t.TempDir()
	env := fakeLiveEnv(t, nil)
	env.home = home
	env.run = scriptedPilotHost(t, home) // its init line says 2.1.280
	recordCLI := env.cli
	env.cli = func(ctx context.Context, dir string, args ...string) liveCLIResult {
		if len(args) > 0 && args[0] == "--version" {
			return liveCLIResult{Stdout: "2.1.281 (Claude Code)\n"}
		}
		return recordCLI(ctx, dir, args...)
	}
	t.Setenv(liveEvalGateEnv, "1")
	o := liveOptions{
		tasksFile: liveTestPilot, rates: liveTestRates, arms: []string{"stock", "classic"}, out: t.TempDir(),
		install: liveInstallPluginDir, bundle: fakeBundle(t), idleExit: 1800, trials: 2, defectsAttested: true,
	}
	var log bytes.Buffer
	err := runLiveEval(context.Background(), o, env, &log)
	require.ErrorContains(t, err, "three configuration errors")

	var plan eval.LivePlan
	readJSON(t, filepath.Join(o.out, "plan.json"), &plan)
	first := plan.Trials[0]
	slot := filepath.Join(o.out, "trials", first.Task, first.Arm, fmt.Sprintf("%02d", first.Trial))
	for n := 1; n <= 3; n++ {
		var rec eval.LiveTrial
		readJSON(t, filepath.Join(slot, fmt.Sprintf("trial.discarded-%d.json", n)), &rec)
		require.Contains(t, rec.ConfigurationError, "2.1.280", "the init line's version names the mismatch")
	}
	require.NoFileExists(t, filepath.Join(slot, "trial.json"))
	var sum eval.LiveSummary
	readJSON(t, filepath.Join(o.out, "summary.json"), &sum)
	require.Empty(t, sum.Arms)
	require.Equal(t, "not-applicable", sum.Decision.Verdict)
	require.Contains(t, strings.Join(sum.Notes, "\n"), "three configuration errors")
}

// TestRunLiveEval_HostPin: every session and every plugin command runs with the auto-updater off;
// plan.json records the host version; a host whose version changed between trials is refused
// before the next trial starts — a harness error that stops the run, with no record in the slot it
// refused — and a host whose version cannot be read is refused before anything runs.
func TestRunLiveEval_HostPin(t *testing.T) {
	home := t.TempDir()
	env := fakeLiveEnv(t, nil)
	env.home = home
	env.run = scriptedPilotHost(t, home)
	versionCalls := 0
	recordCLI := env.cli
	env.cli = func(ctx context.Context, dir string, args ...string) liveCLIResult {
		if len(args) > 0 && args[0] == "--version" {
			versionCalls++
			if versionCalls > 2 { // the plan's reading, then the first trial's check
				return liveCLIResult{Stdout: "2.1.301 (Claude Code)\n"}
			}
		}
		return recordCLI(ctx, dir, args...)
	}
	t.Setenv(liveEvalGateEnv, "1")
	o := liveOptions{
		tasksFile: liveTestPilot, rates: liveTestRates, arms: []string{"stock"}, out: t.TempDir(),
		idleExit: 1800, trials: 2,
	}
	var log bytes.Buffer
	err := runLiveEval(context.Background(), o, env, &log)
	require.ErrorContains(t, err, "2.1.301")
	require.ErrorContains(t, err, "pinned 2.1.280")
	require.FileExists(t, filepath.Join(o.out, "trials", "pilot-codeword", "stock", "01", "trial.json"))
	require.NoDirExists(t, filepath.Join(o.out, "trials", "pilot-codeword", "stock", "02"), "the refused trial never started")
	var sum eval.LiveSummary
	readJSON(t, filepath.Join(o.out, "summary.json"), &sum)
	require.Equal(t, "not-applicable", sum.Decision.Verdict)

	env.cli = func(context.Context, string, ...string) liveCLIResult { return liveCLIResult{Code: 1} }
	o.out = t.TempDir()
	err = runLiveEval(context.Background(), o, env, &log)
	require.ErrorContains(t, err, "host version")
	require.NoFileExists(t, filepath.Join(o.out, "plan.json"), "nothing is planned on an unpinned host")

	require.Contains(t, liveCLIEnv([]string{"PATH=x", "DISABLE_AUTOUPDATER=0"}), "DISABLE_AUTOUPDATER=1")
	require.NotContains(t, liveCLIEnv([]string{"PATH=x", "DISABLE_AUTOUPDATER=0"}), "DISABLE_AUTOUPDATER=0")
}

// ── fakes ────────────────────────────────────────────────────────────────────────────────────────

// fakeModsBundle writes a minimal --mods bundle: the classic fake's file plus every mod source the
// real assembler ships, all listed in its BUNDLE.json, from the given commit.
func fakeModsBundle(t *testing.T, commit string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "qompack-plugin-9.9.9-test-mods-"+runtime.GOOS+"-"+runtime.GOARCH)
	files := map[string][]byte{".claude-plugin/plugin.json": []byte(`{"name":"qompack","version":"9.9.9-test"}`)}
	mods, err := liveModFiles(runtime.GOOS)
	require.NoError(t, err)
	require.NotEmpty(t, mods)
	for _, m := range mods {
		files[m] = []byte("// " + m + "\n")
	}
	var listed []bundleFile
	for _, rel := range slices.Sorted(func(yield func(string) bool) {
		for k := range files {
			if !yield(k) {
				return
			}
		}
	}) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, files[rel], 0o600))
		sum, size, err := hashFile(p)
		require.NoError(t, err)
		listed = append(listed, bundleFile{Path: rel, SHA256: sum, Bytes: size})
	}
	id := bundleIdentity{
		Name: "qompack", Version: "9.9.9-test", Target: bundleTarget{OS: runtime.GOOS, Arch: runtime.GOARCH},
		Source: bundleSource{Commit: commit}, Files: listed,
	}
	raw, err := json.Marshal(id)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, identityFileName), raw, 0o600))
	return dir
}

// writeTestHello writes a hello file into project's store the way `qompack mod hello` does.
func writeTestHello(t *testing.T, project string, h modwire.HelloFile) {
	t.Helper()
	dir := filepath.Join(project, ".qompack", modwire.StateDir)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	name, err := modwire.HelloFileName(h.SessionID)
	require.NoError(t, err)
	b, err := h.Encode()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), b, 0o600))
}

// seedPuttingFirst finds an order seed whose plan of the pilot task, one trial per arm, starts with
// arm, so a test that needs one arm to run first can say so without depending on a shuffle.
func seedPuttingFirst(t *testing.T, arm string, arms []string) int64 {
	t.Helper()
	counts := map[string]int{}
	for _, a := range arms {
		counts[a] = 1
	}
	for seed := int64(1); seed < 1000; seed++ {
		if planLiveTrials([]eval.LiveTask{{ID: "pilot-codeword"}}, arms, counts, seed)[0].Arm == arm {
			return seed
		}
	}
	t.Fatalf("no seed below 1000 puts %s first", arm)
	return 0
}

// pinnedCLI wraps a fake `claude` subcommand runner so `--version` answers the fake host's version,
// which every run pins before its first trial and checks before each one.
func pinnedCLI(f func(ctx context.Context, dir string, args ...string) liveCLIResult) func(context.Context, string, ...string) liveCLIResult {
	return func(ctx context.Context, dir string, args ...string) liveCLIResult {
		if len(args) > 0 && args[0] == "--version" {
			return liveCLIResult{Stdout: "2.1.280 (Claude Code)\n"}
		}
		return f(ctx, dir, args...)
	}
}
