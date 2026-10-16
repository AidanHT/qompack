package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/eval"
	"github.com/qompack/qompack/internal/modwire"
	"github.com/stretchr/testify/require"
)

// TestRunLiveEval_SteerHelloFailureIsScoredNotDiscarded is intention to treat on a correctly
// configured steer trial (W6 item 4, R27): a missing hello file, or one whose served hello said
// steering false, is a treatment outcome. The attempt is scored — trial.json, no discarded record,
// one session, no re-run — and counted in the arm's denominators and its hello-problem count.
func TestRunLiveEval_SteerHelloFailureIsScoredNotDiscarded(t *testing.T) {
	for name, writeBad := range map[string]bool{"missing hello": false, "steering false": true} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			env := fakeLiveEnv(t, nil)
			env.home = home
			scripted := scriptedPilotHostWith(t, home, pilotHostOpts{noHello: true})
			sessions := 0
			env.run = func(ctx context.Context, spec liveProcSpec) liveProcResult {
				sessions++
				res := scripted(ctx, spec)
				if writeBad {
					writeTestHello(t, spec.Dir, modwire.HelloFile{
						V: modwire.Version, SessionID: argAfter(spec.Args, "--session-id"),
						HostVersion: modwire.MinHostVersion, Enabled: true, Steering: false, TS: 1,
					})
				}
				return res
			}
			t.Setenv(liveEvalGateEnv, "1")
			o := liveOptions{
				tasksFile: liveTestTasksV7, rates: liveTestRates, arms: []string{"steer"}, out: t.TempDir(),
				install: liveInstallPluginDir, bundleMods: fakeModsBundle(t, "deadbeef"), idleExit: 1800, trials: 1,
				defectsAttested: true,
			}
			var log bytes.Buffer
			require.NoError(t, runLiveEval(context.Background(), o, env, &log), log.String())
			require.Equal(t, 1, sessions, "the slot is not run again")

			slot := filepath.Join(o.out, "trials", "pilot-codeword", "steer", "01")
			require.NoFileExists(t, filepath.Join(slot, "trial.discarded-1.json"))
			var rec eval.LiveTrial
			readJSON(t, filepath.Join(slot, "trial.json"), &rec)
			require.Empty(t, rec.ConfigurationError)
			require.NotNil(t, rec.Hello)
			require.False(t, rec.Hello.OK)
			if writeBad {
				require.Contains(t, rec.Hello.Problem, "steering false")
			} else {
				require.Contains(t, rec.Hello.Problem, "no state/mod-hello-*.json")
			}

			var sum eval.LiveSummary
			readJSON(t, filepath.Join(o.out, "summary.json"), &sum)
			st := sum.Arms["steer"]
			require.Equal(t, 1, st.Trials)
			require.Equal(t, 1, st.HelloProblemTrials)
			require.Equal(t, 1, st.TaskSuccess.N, "in the denominator")
			require.Equal(t, 1, st.Recovery.N, "in the recovery denominator")
			require.Contains(t, strings.Join(sum.Notes, "\n"), "intention to treat")
		})
	}
}

// TestRunLiveEval_EveryConfigurationCheckFails drives each configuration check of W6 item 4 to its
// failing side end to end. A fault on the first attempt only sets that attempt aside as
// trial.discarded-1.json and the re-run is the slot's scored trial; a fault on every attempt stops
// the run at the third. A check that fails before the session starts leaves no stream and no guard
// record, because no host ran.
func TestRunLiveEval_EveryConfigurationCheckFails(t *testing.T) {
	type rig struct {
		env  *liveEnv
		home string
		o    liveOptions
	}
	type fault struct {
		arm        string
		preSession bool
		want       string
		// inject arranges for attempts 1..until to fail the check.
		inject func(t *testing.T, r *rig, until int)
	}
	faults := map[string]fault{
		"store missing on a Qompack arm": {
			arm: "classic", preSession: true, want: "has a store before the session: false, want true",
			inject: func(t *testing.T, r *rig, until int) {
				inits := 0
				git := r.env.git
				r.env.git = func(ctx context.Context, dir string, args ...string) error {
					if len(args) > 0 && args[0] == "init" {
						inits++
						if inits <= until {
							require.NoError(t, os.Remove(filepath.Join(dir, liveStoreDir)))
						}
					}
					return git(ctx, dir, args...)
				}
			},
		},
		"store on stock": {
			arm: "stock", preSession: true, want: "has a store before the session: true, want false",
			inject: func(t *testing.T, r *rig, until int) {
				inits := 0
				git := r.env.git
				r.env.git = func(ctx context.Context, dir string, args ...string) error {
					if len(args) > 0 && args[0] == "init" {
						inits++
						if inits <= until {
							require.NoError(t, os.Mkdir(filepath.Join(dir, liveStoreDir), 0o755))
						}
					}
					return git(ctx, dir, args...)
				}
			},
		},
		"bundle changed after planning": {
			arm: "classic", preSession: true, want: "now hashes to",
			inject: func(t *testing.T, r *rig, until int) {
				identity := filepath.Join(r.o.bundle, identityFileName)
				orig, err := os.ReadFile(identity)
				require.NoError(t, err)
				calls := 0
				cli := r.env.cli
				r.env.cli = func(ctx context.Context, dir string, args ...string) liveCLIResult {
					if len(args) > 0 && args[0] == "--version" {
						calls++ // 1 is the plan's reading; 2 is attempt 1's check, 3 attempt 2's
						body := orig
						if calls >= 2 && calls-1 <= until {
							body = append(append([]byte{}, orig...), ' ')
						}
						require.NoError(t, os.WriteFile(identity, body, 0o600))
					}
					return cli(ctx, dir, args...)
				}
			},
		},
		"stock loads Qompack": {
			arm: "stock", want: "the host loaded Qompack (qompack@elsewhere) on the stock arm",
			inject: func(t *testing.T, r *rig, until int) {
				r.env.run = sessionFault(t, r.home, until, pilotHostOpts{extra: []string{
					`{"name":"qompack","path":"p","source":"qompack@elsewhere","version":"9.9.9-test"}`,
				}})
			},
		},
		"two Qompack plugins on one arm": {
			arm: "classic", want: "the host loaded 2 Qompack plugin(s) (qompack@inline, qompack@elsewhere)",
			inject: func(t *testing.T, r *rig, until int) {
				r.env.run = sessionFault(t, r.home, until, pilotHostOpts{extra: []string{
					`{"name":"qompack","path":"p","source":"qompack@elsewhere","version":"9.9.9-test"}`,
				}})
			},
		},
	}
	for name, f := range faults {
		for _, always := range []bool{false, true} {
			label := name + "/first attempt"
			if always {
				label = name + "/every attempt"
			}
			t.Run(label, func(t *testing.T) {
				r := &rig{env: fakeLiveEnv(t, nil), home: t.TempDir()}
				r.env.home = r.home
				r.env.run = scriptedPilotHost(t, r.home)
				r.o = liveOptions{
					tasksFile: liveTestPilot, rates: liveTestRates, arms: []string{f.arm}, out: t.TempDir(),
					install: liveInstallPluginDir, idleExit: 1800, trials: 1, defectsAttested: true,
				}
				if f.arm != "stock" {
					r.o.bundle = fakeBundle(t)
				}
				until := 1
				if always {
					until = 99
				}
				f.inject(t, r, until)
				t.Setenv(liveEvalGateEnv, "1")
				var log bytes.Buffer
				err := runLiveEval(context.Background(), r.o, r.env, &log)

				slot := filepath.Join(r.o.out, "trials", "pilot-codeword", f.arm, "01")
				var discarded eval.LiveTrial
				readJSON(t, filepath.Join(slot, "trial.discarded-1.json"), &discarded)
				require.Contains(t, discarded.ConfigurationError, f.want)
				if f.preSession {
					require.NoFileExists(t, filepath.Join(slot, "stream.discarded-1.jsonl"), "no host started")
					require.Nil(t, discarded.HomeGuard, "no host started, so no guard verdict")
				} else {
					require.FileExists(t, filepath.Join(slot, "stream.discarded-1.jsonl"))
				}
				if always {
					require.ErrorContains(t, err, "three configuration errors")
					require.FileExists(t, filepath.Join(slot, "trial.discarded-3.json"))
					require.NoFileExists(t, filepath.Join(slot, "trial.json"))
					return
				}
				require.NoError(t, err, log.String())
				require.NoFileExists(t, filepath.Join(slot, "trial.discarded-2.json"))
				var scored eval.LiveTrial
				readJSON(t, filepath.Join(slot, "trial.json"), &scored)
				require.Empty(t, scored.ConfigurationError)
			})
		}
	}
}

// TestCheckLiveArmProject: the project just before the session is the arm's own — a store exactly on
// a Qompack arm, and the fixture's CLAUDE.md bytes, plus text G's section on generic.
func TestCheckLiveArmProject(t *testing.T) {
	spec := func(arm string) eval.LiveArmState {
		s, _ := eval.LiveArmSpec(arm)
		return s
	}
	const g = "Keep every identifier."
	type setup struct {
		fixtureMD string // "" for a fixture with no CLAUDE.md
		projectMD string // "" for none
		store     bool
	}
	generic := string(liveGenericClaudeMD([]byte("# Rules\n"), g))
	for name, c := range map[string]struct {
		arm  string
		s    setup
		want string
	}{
		"stock as the fixture":     {"stock", setup{fixtureMD: "# Rules\n", projectMD: "# Rules\n"}, ""},
		"stock with a store":       {"stock", setup{store: true}, "has a store before the session: true, want false"},
		"classic without a store":  {"classic", setup{}, "has a store before the session: false, want true"},
		"classic with its store":   {"classic", setup{store: true}, ""},
		"classic CLAUDE.md edited": {"classic", setup{fixtureMD: "# Rules\n", projectMD: "# Rules\nmore\n", store: true}, "CLAUDE.md is not what the arm declares"},
		"classic CLAUDE.md added":  {"classic", setup{projectMD: "# Compact instructions\n", store: true}, "CLAUDE.md is not what the arm declares"},
		"generic with text G":      {"generic", setup{fixtureMD: "# Rules\n", projectMD: generic, store: true}, ""},
		"generic without section":  {"generic", setup{fixtureMD: "# Rules\n", projectMD: "# Rules\n", store: true}, "CLAUDE.md is not what the arm declares"},
		"generic, no fixture file": {"generic", setup{projectMD: string(liveGenericClaudeMD(nil, g)), store: true}, ""},
		"generic, missing file":    {"generic", setup{store: true}, "CLAUDE.md is not what the arm declares"},
		"steer with its store":     {"steer", setup{store: true}, ""},
	} {
		fixture, project := t.TempDir(), t.TempDir()
		if c.s.fixtureMD != "" {
			require.NoError(t, os.WriteFile(filepath.Join(fixture, "CLAUDE.md"), []byte(c.s.fixtureMD), 0o600))
		}
		if c.s.projectMD != "" {
			require.NoError(t, os.WriteFile(filepath.Join(project, "CLAUDE.md"), []byte(c.s.projectMD), 0o600))
		}
		if c.s.store {
			require.NoError(t, os.Mkdir(filepath.Join(project, liveStoreDir), 0o755))
		}
		err := checkLiveArmProject(project, fixture, spec(c.arm), g)
		if c.want == "" {
			require.NoError(t, err, name)
			continue
		}
		require.ErrorContains(t, err, c.want, name)
	}
}

// sessionFault is the scripted pilot host with opts on its first until sessions and as configured
// after them.
func sessionFault(t *testing.T, home string, until int, opts pilotHostOpts) func(context.Context, liveProcSpec) liveProcResult {
	t.Helper()
	bad, good := scriptedPilotHostWith(t, home, opts), scriptedPilotHost(t, home)
	sessions := 0
	return func(ctx context.Context, spec liveProcSpec) liveProcResult {
		sessions++
		if sessions <= until {
			return bad(ctx, spec)
		}
		return good(ctx, spec)
	}
}

// argAfter is the argument after flag in args, or "".
func argAfter(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
