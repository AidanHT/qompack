package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

const (
	liveTestTasks = "../../testdata/eval/live/tasks.json"
	liveTestPilot = "../../testdata/eval/live/pilot.json"
	liveTestRates = "../../testdata/eval/live/rates.json"
	pilotCodeWord = "2c1a7a62c3"
)

func TestParseLiveFlags(t *testing.T) {
	o, err := parseLiveFlags([]string{"--arms", "qompack", "--install", "marketplace", "--only", "a, b", "--trials", "3"})
	require.NoError(t, err)
	require.Equal(t, []string{"qompack"}, o.arms)
	require.Equal(t, map[string]bool{"a": true, "b": true}, o.only)
	require.Equal(t, 3, o.trials)

	for _, bad := range [][]string{
		{"--arms", "control"},
		{"--arms", ""},
		{"--install", "copy"},
		{"--trials", "-1"},
		{"stray"},
		{"--daemon-idle-exit", "0"},
	} {
		_, err := parseLiveFlags(bad)
		require.Error(t, err, "%v", bad)
	}
}

// TestPlanLiveTrials_AlternatesArmOrder: across tasks and trials each arm goes first half the time.
func TestPlanLiveTrials_AlternatesArmOrder(t *testing.T) {
	tasks := []eval.LiveTask{{ID: "a"}, {ID: "b"}}
	plan := planLiveTrials(tasks, []string{"stock", "qompack"}, 2)
	require.Len(t, plan, 8)
	first := map[string]int{}
	for i := 0; i < len(plan); i += 2 {
		first[plan[i].Arm]++
		require.Equal(t, plan[i].Task, plan[i+1].Task)
		require.NotEqual(t, plan[i].Arm, plan[i+1].Arm)
	}
	require.Equal(t, map[string]int{"stock": 2, "qompack": 2}, first)
}

func TestSelectLiveTasks_HeldOutNeedsTheConfirmatoryFlag(t *testing.T) {
	ts, _, err := eval.LoadLiveTaskSet(liveTestTasks)
	require.NoError(t, err)
	got, err := selectLiveTasks(ts, liveOptions{})
	require.NoError(t, err)
	require.Len(t, got, 7, "the three held-out tasks are excluded by default")
	got, err = selectLiveTasks(ts, liveOptions{includeHeldOut: true})
	require.NoError(t, err)
	require.Len(t, got, 10)
	_, err = selectLiveTasks(ts, liveOptions{only: map[string]bool{"seed-recall": true}})
	require.ErrorContains(t, err, "held out")
	_, err = selectLiveTasks(ts, liveOptions{only: map[string]bool{"nope": true}})
	require.ErrorContains(t, err, "unknown task")
}

// TestLiveSessionArgs_IdenticalAcrossArmsButThePlugin pins the host command line: streaming input
// and output, hook events, no user settings (so the operator's own plugins and hooks load in
// neither arm), dontAsk with the task's allow-list, and a session id the driver chose.
func TestLiveSessionArgs_IdenticalAcrossArmsButThePlugin(t *testing.T) {
	ts, _, err := eval.LoadLiveTaskSet(liveTestPilot)
	require.NoError(t, err)
	task := ts.Tasks[0]
	stock := liveSessionArgs("m", task, ts.Defaults, "sid", nil)
	plug := liveSessionArgs("m", task, ts.Defaults, "sid", []string{"--plugin-dir", "B"})
	require.Equal(t, stock, plug[:len(stock)])
	require.Equal(t, []string{"--plugin-dir", "B"}, plug[len(stock):])
	joined := strings.Join(stock, " ")
	for _, want := range []string{
		"-p", "--input-format stream-json", "--output-format stream-json", "--verbose", "--include-hook-events",
		"--model m", "--max-turns 6", "--setting-sources project,local", "--permission-mode dontAsk", "--session-id sid",
		"--allowedTools Read,Edit,Write",
	} {
		require.Contains(t, joined, want)
	}
	require.Contains(t, liveSessionEnv(120), "QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=120")
	require.Equal(t, []string{
		"Run go run ./cmd/code and tell me the code word it prints, in one line.", "/compact",
		task.Steps[2].Prompt,
	}, liveMessages(task))
	require.Regexp(t, `^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`, newUUID())
}

func TestRunLiveEval_DryRunAndTheGate(t *testing.T) {
	env := fakeLiveEnv(t, nil)
	var out bytes.Buffer
	o := liveOptions{tasksFile: liveTestPilot, rates: liveTestRates, arms: []string{"stock"}, out: t.TempDir(), dryRun: true}
	require.NoError(t, runLiveEval(context.Background(), o, env, &out))
	require.Contains(t, out.String(), "plan: pilot-codeword/stock/1")
	require.Contains(t, out.String(), "install none", "a run with no plugin arm installs nothing")
	require.Contains(t, out.String(), "no session started")

	o.arms = []string{"stock", "qompack"}
	require.ErrorContains(t, runLiveEval(context.Background(), o, env, &out), "--bundle", "the qompack arm needs a bundle")

	o.arms, o.dryRun = []string{"stock"}, false
	t.Setenv(liveEvalGateEnv, "")
	err := runLiveEval(context.Background(), o, env, &out)
	require.ErrorContains(t, err, liveEvalGateEnv, "no real session without the gate")

	o.maxSessions, o.trials = 1, 2
	t.Setenv(liveEvalGateEnv, "1")
	require.ErrorContains(t, runLiveEval(context.Background(), o, env, &out), "--max-sessions")
}

// TestRunLiveEval_OfflineTrialsBothArms drives the whole driver with a scripted host: the pilot
// task on both arms, the qompack arm through the marketplace flow, grading against the real hidden
// test with the real `go` toolchain, and the guard around every trial.
func TestRunLiveEval_OfflineTrialsBothArms(t *testing.T) {
	home := t.TempDir()
	var cliCalls []string
	env := fakeLiveEnv(t, &cliCalls)
	env.home = home
	env.run = scriptedPilotHost(t, home)
	bundle := fakeBundle(t)
	out := t.TempDir()
	t.Setenv(liveEvalGateEnv, "1")

	// Mirror what 2.1.280 did in the marketplace rehearsal: install copies the bundle into the
	// global plugin cache, and uninstall leaves that copy behind marked .orphaned_at.
	cache := filepath.Join(home, "plugins", "cache", liveMarketplaceName, livePluginName, "9.9.9-test")
	recordCLI := env.cli
	env.cli = func(ctx context.Context, dir string, args ...string) liveCLIResult {
		if len(args) > 1 && args[0] == "plugin" {
			switch args[1] {
			case "install":
				require.NoError(t, copyTree(bundle, cache))
			case "uninstall":
				require.NoError(t, os.WriteFile(filepath.Join(cache, orphanedMarker), []byte("1"), 0o600))
			}
		}
		return recordCLI(ctx, dir, args...)
	}

	o := liveOptions{
		tasksFile: liveTestPilot, rates: liveTestRates, arms: []string{"stock", "qompack"}, out: out,
		install: liveInstallMarketplace, bundle: bundle, idleExit: 1, trials: 1,
	}
	var log bytes.Buffer
	require.NoError(t, runLiveEval(context.Background(), o, env, &log), log.String())

	for _, arm := range []string{"stock", "qompack"} {
		var rec eval.LiveTrial
		readJSON(t, filepath.Join(out, "trials", "pilot-codeword", arm, "01", "trial.json"), &rec)
		require.Empty(t, rec.HarnessError, arm)
		require.True(t, rec.Completed, "%s: %+v", arm, rec.Steps)
		require.True(t, rec.TaskSuccess, "%s: %+v", arm, rec.Checks)
		require.Zero(t, rec.ConstraintViolations, "%s: %+v", arm, rec.Checks)
		require.NotNil(t, rec.Recovered)
		require.True(t, *rec.Recovered)
		require.True(t, rec.HomeGuard.Checked)
		require.True(t, rec.HomeGuard.Unchanged, "%+v", rec.HomeGuard)
		require.Len(t, rec.Compactions, 1)
		require.NotNil(t, rec.Estimate)
		require.Positive(t, rec.Estimate.Micros)
		require.Equal(t, "2026-09-22", rec.RateTableDate)
		require.NotEmpty(t, rec.TranscriptHookMS, "the host-measured hook durations come from the transcript")
		require.Equal(t, arm == "qompack", rec.PluginLoaded)
		require.False(t, rec.PreregisteredModel == false && rec.Model == "claude-haiku-4-5-20251001")
		for _, f := range []string{"stream.jsonl", "stderr.txt", "ledger.json", "estimate.json", "transcript-facts.json", "invocation.json"} {
			require.FileExists(t, filepath.Join(out, "trials", "pilot-codeword", arm, "01", f))
		}
		require.NoFileExists(t, filepath.Join(out, "trials", "pilot-codeword", arm, "01", "transcript.raw.jsonl"),
			"the raw transcript is copied only on request")
		if arm == "qompack" {
			require.NotNil(t, rec.Qompack)
			require.Equal(t, "connected", rec.Qompack.MCPServerStatus)
			require.Equal(t, liveInstallMarketplace, rec.Install)
			require.Equal(t, "9.9.9-test", rec.Plugin.Version)
			require.Len(t, rec.HookProblems, 1, "the scripted host rejected PreCompact the way 2.1.280 does")
		} else {
			require.Nil(t, rec.Qompack)
			require.Empty(t, rec.HookProblems)
		}
	}
	require.Equal(t, []string{
		"plugin marketplace add", "plugin install qompack@qompack-live-eval --scope local -y",
		"plugin uninstall qompack@qompack-live-eval --scope local -y", "plugin marketplace remove qompack-live-eval",
	}, cliCalls)

	require.NoDirExists(t, filepath.Join(home, "plugins", "cache", liveMarketplaceName),
		"the orphaned copy of this run's bundle is removed")

	var sum eval.LiveSummary
	readJSON(t, filepath.Join(out, "summary.json"), &sum)
	require.Equal(t, 1, sum.Arms["qompack"].HookProblemTrials)
	require.FileExists(t, filepath.Join(out, "summary.md"))
	require.FileExists(t, filepath.Join(out, "plan.json"))
}

// TestRunLiveEval_GuardFailsClosed: an install that leaves the operator's plugin registry changed
// stops the run after that trial, and the trial record says what changed.
func TestRunLiveEval_GuardFailsClosed(t *testing.T) {
	home := t.TempDir()
	env := fakeLiveEnv(t, nil)
	env.home = home
	env.run = scriptedPilotHost(t, home)
	env.cli = func(_ context.Context, _ string, args ...string) liveCLIResult {
		if len(args) > 1 && args[1] == "install" {
			require.NoError(t, os.MkdirAll(filepath.Join(home, "plugins"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(home, "plugins", "installed_plugins.json"), []byte(`{"leaked":true}`), 0o600))
		}
		return liveCLIResult{}
	}
	out := t.TempDir()
	t.Setenv(liveEvalGateEnv, "1")
	o := liveOptions{
		tasksFile: liveTestPilot, rates: liveTestRates, arms: []string{"qompack", "stock"}, out: out,
		install: liveInstallMarketplace, bundle: fakeBundle(t), idleExit: 1, trials: 1,
	}
	var log bytes.Buffer
	err := runLiveEval(context.Background(), o, env, &log)
	require.ErrorContains(t, err, "configuration changed")
	require.Contains(t, log.String(), "STOPPING")

	var rec eval.LiveTrial
	readJSON(t, filepath.Join(out, "trials", "pilot-codeword", "qompack", "01", "trial.json"), &rec)
	require.False(t, rec.HomeGuard.Unchanged)
	require.Contains(t, strings.Join(rec.HomeGuard.After, "\n"), "plugins/installed_plugins.json")
	require.NoDirExists(t, filepath.Join(out, "trials", "pilot-codeword", "stock"), "no trial runs after the guard fails")
	var sum eval.LiveSummary
	readJSON(t, filepath.Join(out, "summary.json"), &sum)
	require.Contains(t, strings.Join(sum.Notes, "\n"), "stopped early")
}

// TestRemoveCreatedPluginData: a plugin data directory the trial created is removed when empty and
// named when not; one that existed before is never touched.
func TestRemoveCreatedPluginData(t *testing.T) {
	home := t.TempDir()
	env := &liveEnv{home: home}
	pre := filepath.Join(home, "plugins", "data", "qompack-preexisting")
	require.NoError(t, os.MkdirAll(pre, 0o755))
	before, err := env.guardSnapshot()
	require.NoError(t, err)
	empty := filepath.Join(home, "plugins", "data", "qompack-inline")
	full := filepath.Join(home, "plugins", "data", "qompack-qompack-live-eval")
	require.NoError(t, os.MkdirAll(empty, 0o755))
	require.NoError(t, os.MkdirAll(full, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(full, "x"), []byte("x"), 0o600))

	// A directory absent from the snapshot but older than the trial was made by someone else
	// sharing this configuration (another lane's session): it is named, never removed.
	foreign := filepath.Join(home, "plugins", "data", "qompack-shell-probe-inline")
	require.NoError(t, os.MkdirAll(foreign, 0o755))
	old := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(foreign, old, old))

	left := env.removeCreatedPluginData(before, time.Now().Add(-time.Minute))
	require.NoDirExists(t, empty)
	require.DirExists(t, full)
	require.DirExists(t, pre)
	require.DirExists(t, foreign)
	require.Len(t, left, 2)
	joined := strings.Join(left, "\n")
	require.Contains(t, joined, "qompack-qompack-live-eval")
	require.Contains(t, joined, "qompack-shell-probe-inline")
}

// TestLiveTaskSet_ReferenceSolutionsPassEveryCheck proves every task is gradeable: a correct,
// constraint-respecting project (fixture + reference solution + hidden tests, with the reference
// answers and no tool re-run) passes every check, and the untouched fixture does not pass them all.
func TestLiveTaskSet_ReferenceSolutionsPassEveryCheck(t *testing.T) {
	var answers map[string]map[string]string
	readJSON(t, "../../testdata/eval/live/reference/answers.json", &answers)
	for _, file := range []string{liveTestTasks, liveTestPilot} {
		ts, _, err := eval.LoadLiveTaskSet(file)
		require.NoError(t, err)
		for _, task := range ts.Tasks {
			t.Run(task.ID, func(t *testing.T) {
				t.Parallel()
				fixture := task.FixtureDir(file)
				hashes, err := eval.HashTree(fixture)
				require.NoError(t, err)
				after := map[string][]eval.HostToolUse{}
				for _, s := range task.Steps {
					if s.Compact {
						after[s.ID] = nil
					}
				}
				grade := func(withReference, runCommands bool) []eval.LiveCheckResult {
					project := t.TempDir()
					require.NoError(t, copyTree(fixture, project))
					if withReference {
						require.NoError(t, copyTree(filepath.Join(filepath.Dir(file), "reference", task.ID), project))
					}
					if task.HiddenFixture != "" {
						require.NoError(t, copyTree(filepath.Join(filepath.Dir(file), filepath.FromSlash(task.HiddenFixture)), project))
					}
					cmds := map[string]eval.CommandOutcome{}
					for _, c := range task.Checks {
						if c.Kind == eval.CheckCommand && runCommands {
							cmds[c.ID] = runLiveCheckCommand(project, c.Argv)
						}
					}
					return eval.GradeLiveTrial(task, project, eval.LiveEvidence{
						Answers: answers[task.ID], Fixture: hashes, Commands: cmds, ToolUsesAfter: after,
					})
				}
				for _, r := range grade(true, true) {
					require.True(t, r.Passed, "reference fails %s: %s", r.ID, r.Detail)
				}
				// The untouched fixture must not already pass every check. A failing non-command
				// check settles it without compiling anything; only when every file and answer check
				// passes do the commands have to run and one of them fail.
				failed := func(res []eval.LiveCheckResult, kinds func(string) bool) bool {
					for _, r := range res {
						if kinds(r.Kind) && !r.Passed {
							return true
						}
					}
					return false
				}
				notCommand := func(k string) bool { return k != eval.CheckCommand }
				if !failed(grade(false, false), notCommand) {
					require.True(t, failed(grade(false, true), func(string) bool { return true }),
						"the untouched fixture must not already pass every check")
				}
			})
		}
	}
}

// ── fakes ────────────────────────────────────────────────────────────────────────────────────────

func fakeLiveEnv(t *testing.T, cliCalls *[]string) *liveEnv {
	t.Helper()
	return &liveEnv{
		claudeBin: "claude-fake",
		home:      t.TempDir(),
		now:       func() time.Time { return time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC) },
		run: func(context.Context, liveProcSpec) liveProcResult {
			t.Fatal("no session may start in this test")
			return liveProcResult{}
		},
		cli: func(_ context.Context, _ string, args ...string) liveCLIResult {
			if cliCalls != nil && len(args) > 0 && args[0] == "plugin" {
				call := strings.Join(args, " ")
				if args[1] == "marketplace" && args[2] == "add" {
					call = "plugin marketplace add"
				}
				*cliCalls = append(*cliCalls, call)
			}
			if len(args) > 0 && args[0] == "--version" {
				return liveCLIResult{Stdout: "2.1.280 (Claude Code)\n"}
			}
			return liveCLIResult{}
		},
		stopDaemon: func(string, string) (string, error) { return "no daemon lock after the session", nil },
		check:      runLiveCheckCommand,
		git:        runLiveGit,
	}
}

// fakeBundle writes a minimal bundle directory whose BUNDLE.json matches its files.
func fakeBundle(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "qompack-plugin-9.9.9-test-"+runtime.GOOS+"-"+runtime.GOARCH)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".claude-plugin"), 0o755))
	manifest := []byte(`{"name":"qompack","version":"9.9.9-test"}`)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".claude-plugin", "plugin.json"), manifest, 0o600))
	sum, size, err := hashFile(filepath.Join(dir, ".claude-plugin", "plugin.json"))
	require.NoError(t, err)
	id := bundleIdentity{
		Name: "qompack", Version: "9.9.9-test", Target: bundleTarget{OS: runtime.GOOS, Arch: runtime.GOARCH},
		Source: bundleSource{Commit: "deadbeef"}, Files: []bundleFile{{Path: ".claude-plugin/plugin.json", SHA256: sum, Bytes: size}},
	}
	raw, err := json.Marshal(id)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, identityFileName), raw, 0o600))
	return dir
}

// scriptedPilotHost plays the pilot task the way a real host would: it prints a stream shaped like
// Claude Code 2.1.280's, writes CODE.txt into the project, and leaves a transcript under home.
func scriptedPilotHost(t *testing.T, home string) func(context.Context, liveProcSpec) liveProcResult {
	t.Helper()
	return func(_ context.Context, spec liveProcSpec) liveProcResult {
		sid := ""
		plugin := false
		for i, a := range spec.Args {
			if a == "--session-id" {
				sid = spec.Args[i+1]
			}
			if a == "--plugin-dir" {
				plugin = true
			}
		}
		if _, err := os.Stat(filepath.Join(spec.Dir, ".git")); err != nil {
			t.Errorf("the project was not a git repository when the session started")
		}
		marketplace := false
		if raw, err := os.ReadFile(filepath.Join(spec.Dir, "..", "marketplace", ".claude-plugin", "marketplace.json")); err == nil {
			marketplace = strings.Contains(string(raw), liveMarketplaceName)
		}
		loaded := plugin || marketplace
		require.NoError(t, os.WriteFile(filepath.Join(spec.Dir, "CODE.txt"), []byte(pilotCodeWord+"\n"), 0o600))

		transcript, err := os.ReadFile("../../internal/eval/testdata/live/smoke2-plugin.transcript.redacted.jsonl")
		require.NoError(t, err)
		if !loaded {
			// The stock arm's host ran no PreCompact hook, so its transcript carries no rejection.
			var kept []string
			for _, l := range strings.SplitAfter(string(transcript), "\n") {
				if !strings.Contains(l, "failed: Hook JSON output validation failed") {
					kept = append(kept, l)
				}
			}
			transcript = []byte(strings.Join(kept, ""))
		}
		tdir := filepath.Join(home, "projects", "C--fake-project")
		require.NoError(t, os.MkdirAll(tdir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(tdir, sid+".jsonl"), transcript, 0o600))

		stream := pilotStream(sid, loaded)
		recv := make([]int64, 0)
		for i := range strings.Count(stream, "\n") {
			recv = append(recv, int64(100*(i+1)))
		}
		return liveProcResult{
			Stream: []byte(stream), RecvMS: recv, StepsCompleted: len(spec.Messages),
			StartedAt: time.Unix(0, 0), EndedAt: time.Unix(3, 0),
		}
	}
}

func pilotStream(sid string, plugin bool) string {
	plugins := `[]`
	mcp := `[]`
	if plugin {
		plugins = `[{"name":"qompack","path":"x","source":"qompack@inline","version":"9.9.9-test"}]`
		mcp = `[{"name":"plugin:qompack:qompack","status":"connected","source":"plugin"}]`
	}
	usage := func(in, out, cr, cw int) string {
		return fmt.Sprintf(`{"input_tokens":%d,"output_tokens":%d,"cache_read_input_tokens":%d,"cache_creation_input_tokens":%d,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":%d},"output_tokens_details":{"thinking_tokens":0}}`, in, out, cr, cw, cw)
	}
	model := func(in, out, cr, cw int, cost float64) string {
		return fmt.Sprintf(`{"claude-haiku-4-5-20251001":{"inputTokens":%d,"outputTokens":%d,"cacheReadInputTokens":%d,"cacheCreationInputTokens":%d,"thinkingTokens":0,"costUSD":%g}}`, in, out, cr, cw, cost)
	}
	lines := []string{
		fmt.Sprintf(`{"type":"system","subtype":"init","session_id":%q,"model":"claude-haiku-4-5-20251001","claude_code_version":"2.1.280","plugins":%s,"mcp_servers":%s,"tools":["Bash"]}`, sid, plugins, mcp),
		`{"type":"system","subtype":"hook_started","hook_id":"h1","hook_name":"SessionStart:startup","hook_event":"SessionStart"}`,
		`{"type":"system","subtype":"hook_response","hook_id":"h1","hook_name":"SessionStart:startup","hook_event":"SessionStart","output":"{}","exit_code":0,"outcome":"success"}`,
		`{"type":"assistant","message":{"id":"m1","model":"claude-haiku-4-5-20251001","content":[{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"go run ./cmd/code"}}],"usage":` + usage(10, 1, 100, 50) + `},"parent_tool_use_id":null}`,
		`{"type":"assistant","message":{"id":"m2","model":"claude-haiku-4-5-20251001","content":[{"type":"text","text":"The code word is ` + pilotCodeWord + `."}],"usage":` + usage(8, 1, 150, 20) + `},"parent_tool_use_id":null}`,
		`{"type":"result","subtype":"success","is_error":false,"num_turns":2,"result":"The code word is ` + pilotCodeWord + `.","total_cost_usd":0.001,"usage":` + usage(18, 40, 250, 70) + `,"modelUsage":` + model(18, 40, 250, 70, 0.001) + `,"permission_denials":[]}`,
		`{"type":"system","subtype":"status","status":"compacting"}`,
		`{"type":"system","subtype":"status","status":null,"compact_result":"success"}`,
		`{"type":"system","subtype":"compact_boundary","compact_metadata":{"trigger":"manual","pre_tokens":5000,"post_tokens":900,"duration_ms":4000}}`,
	}
	local := "Compacted "
	if plugin {
		local = `Compacted PreCompact [\"${CLAUDE_PLUGIN_ROOT}/bin/qompack\" checkpoint] failed: Hook JSON output validation failed — hookSpecificOutput.hookEventName: expected one of \"PreToolUse\"`
	}
	lines = append(lines,
		`{"type":"user","message":{"role":"user","content":"<local-command-stdout>`+local+`</local-command-stdout>"}}`,
		`{"type":"result","subtype":"success","is_error":false,"num_turns":0,"local_command":"compact","result":"","total_cost_usd":0.002,"usage":`+usage(0, 0, 0, 0)+`,"modelUsage":`+model(1018, 240, 250, 90, 0.002)+`,"permission_denials":[]}`,
		`{"type":"assistant","message":{"id":"m3","model":"claude-haiku-4-5-20251001","content":[{"type":"tool_use","id":"tu2","name":"Write","input":{"file_path":"CODE.txt"}}],"usage":`+usage(5, 1, 300, 10)+`},"parent_tool_use_id":null}`,
		`{"type":"assistant","message":{"id":"m4","model":"claude-haiku-4-5-20251001","content":[{"type":"text","text":"Wrote `+pilotCodeWord+` to CODE.txt."}],"usage":`+usage(5, 1, 310, 5)+`},"parent_tool_use_id":null}`,
		`{"type":"result","subtype":"success","is_error":false,"num_turns":2,"result":"Wrote `+pilotCodeWord+` to CODE.txt.","total_cost_usd":0.003,"usage":`+usage(10, 30, 610, 15)+`,"modelUsage":`+model(1028, 270, 860, 105, 0.003)+`,"permission_denials":[]}`,
	)
	return strings.Join(lines, "\n") + "\n"
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, v))
}

// TestRemoveOrphanedMarketplaceCache: after `plugin uninstall` Claude Code 2.1.280 keeps the
// uninstalled copy under plugins/cache/<marketplace>/ with an .orphaned_at marker for its own later
// sweep (observed in the marketplace rehearsal). The driver removes that copy only when the trial
// created it, the host marked every version orphaned, and every version is this run's bundle.
func TestRemoveOrphanedMarketplaceCache(t *testing.T) {
	bundleJSON := []byte(`{"name":"qompack","version":"1"}`)
	setup := func(t *testing.T, orphaned bool, identity []byte) (*liveEnv, string) {
		t.Helper()
		home := t.TempDir()
		v := filepath.Join(home, "plugins", "cache", liveMarketplaceName, livePluginName, "1")
		require.NoError(t, os.MkdirAll(v, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(v, identityFileName), identity, 0o600))
		if orphaned {
			require.NoError(t, os.WriteFile(filepath.Join(v, ".orphaned_at"), []byte("1790117032484"), 0o600))
		}
		return &liveEnv{home: home}, filepath.Join(home, "plugins", "cache", liveMarketplaceName)
	}
	sum := sha256Hex(bundleJSON)

	env, dir := setup(t, true, bundleJSON)
	require.Empty(t, env.removeOrphanedMarketplaceCache(liveGuardSnap{}, sum))
	require.NoDirExists(t, dir)

	env, dir = setup(t, false, bundleJSON)
	left := env.removeOrphanedMarketplaceCache(liveGuardSnap{}, sum)
	require.Len(t, left, 1)
	require.Contains(t, left[0], "not marked orphaned")
	require.DirExists(t, dir)

	env, dir = setup(t, true, []byte(`{"someone":"else"}`))
	left = env.removeOrphanedMarketplaceCache(liveGuardSnap{}, sum)
	require.Len(t, left, 1)
	require.Contains(t, left[0], "not this run's bundle")
	require.DirExists(t, dir)

	env, dir = setup(t, true, bundleJSON)
	require.Empty(t, env.removeOrphanedMarketplaceCache(liveGuardSnap{"plugins/cache/" + liveMarketplaceName: "present"}, sum))
	require.DirExists(t, dir, "a cache that existed before the trial is never touched")
}

// TestRunLiveEval_HarnessFailureIsScoredAsFailingEveryOutcome: a trial the harness could not even
// prepare (here, git refuses to initialise the project) never reaches grading, and its record must
// still say it failed on every pre-registered outcome — recovery included — so the summary keeps it
// in every denominator (preregistration §8, intention to treat).
func TestRunLiveEval_HarnessFailureIsScoredAsFailingEveryOutcome(t *testing.T) {
	env := fakeLiveEnv(t, nil)
	env.git = func(context.Context, string, ...string) error { return fmt.Errorf("git init: exit status 128") }
	out := t.TempDir()
	t.Setenv(liveEvalGateEnv, "1")
	o := liveOptions{tasksFile: liveTestPilot, rates: liveTestRates, arms: []string{"stock"}, out: out, idleExit: 1, trials: 1}
	var log bytes.Buffer
	require.NoError(t, runLiveEval(context.Background(), o, env, &log), log.String())

	var rec eval.LiveTrial
	readJSON(t, filepath.Join(out, "trials", "pilot-codeword", "stock", "01", "trial.json"), &rec)
	require.Contains(t, rec.HarnessError, "preparing the project")
	require.False(t, rec.Completed)
	require.False(t, rec.TaskSuccess)
	require.NotNil(t, rec.Recovered, "the pilot task declares recovery checks, so the failed trial is in that denominator")
	require.False(t, *rec.Recovered)

	var sum eval.LiveSummary
	readJSON(t, filepath.Join(out, "summary.json"), &sum)
	st := sum.Arms[eval.ArmStock]
	require.Equal(t, [2]int{0, 1}, [2]int{st.Recovery.K, st.Recovery.N})
	require.Equal(t, [2]int{0, 1}, [2]int{st.ConstraintClean.K, st.ConstraintClean.N})
	require.Len(t, sum.Failed, 1)
}
