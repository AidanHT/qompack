package main

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

// updateGolden is the package's -update flag: `go test ./tools/devtool -run Describe -update`
// rewrites the goldens under testdata/ from the code. It is the only way those files change.
var updateGolden = registerGoldenUpdateFlag()

// registerGoldenUpdateFlag registers -update, or adopts an existing registration of it.
func registerGoldenUpdateFlag() func() bool {
	if f := flag.Lookup("update"); f != nil {
		return func() bool { return f.Value.String() == "true" }
	}
	v := flag.Bool("update", false, "rewrite golden files instead of comparing against them")
	return func() bool { return *v }
}

// requireGolden asserts got equals testdata/<name>, rewriting it under -update. Newlines are
// normalized on the read side only, so a Windows checkout with autocrlf on does not read as drift.
func requireGolden(t *testing.T, name string, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if updateGolden() {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(got), 0o644)) // #nosec G306 -- test fixture
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "golden %s is missing; run go test ./tools/devtool -run %s -update", name, t.Name())
	require.Equal(t, strings.ReplaceAll(string(want), "\r\n", "\n"), got, "%s drifted from the golden", name)
}

// TestRunLiveEval_DescribeGolden: --describe writes, from the files a run reads, every task step by
// step (prompts verbatim, the compaction, each check in plain words), the arms and what differs
// between them (text G included), both bundle identities, the host pin, per-arm trials, the planned
// order, the model, the flags and the pacing — and starts nothing (W6 item 10). The bundle
// directories and hashes vary with the machine and are replaced before the comparison.
func TestRunLiveEval_DescribeGolden(t *testing.T) {
	env := fakeLiveEnv(t, nil) // its run fails the test if a session starts
	classic, mods := fakeBundle(t), fakeModsBundle(t, "deadbeef")
	night, err := parseNightWindow("22:00-07:00")
	require.NoError(t, err)
	out := filepath.Join(t.TempDir(), "run")
	o := liveOptions{
		tasksFile: liveTestTasksV7, rates: liveTestRates, arms: eval.LiveArms, out: out,
		install: liveInstallPluginDir, bundle: classic, bundleMods: mods, idleExit: 1800, defectsAttested: true,
		openDefects: []string{}, orderSeed: 11, describe: true, night: night, fiveHourGate: eval.LiveFiveHourGate,
		sevenDayCeiling: eval.LiveSevenDayCeiling, stepTimeout: liveDefaultStepTimeout, sessionTimeout: liveDefaultSessionTimeout,
	}
	var w bytes.Buffer
	require.NoError(t, runLiveEval(context.Background(), o, env, &w))
	require.NoDirExists(t, out, "--describe writes no run directory")

	var plan struct{ classic, mods eval.LivePluginIdentity }
	for _, b := range []struct {
		dir  string
		mods bool
		dst  *eval.LivePluginIdentity
	}{{classic, false, &plan.classic}, {mods, true, &plan.mods}} {
		id, err := readLiveBundle(b.dir, "bundle", []string{"x"}, b.mods)
		require.NoError(t, err)
		*b.dst = id
	}
	got := w.String()
	for old, repl := range map[string]string{
		plan.classic.BundleDir:    "<bundle dir>",
		plan.mods.BundleDir:       "<bundle-mods dir>",
		plan.classic.BundleSHA256: "<bundle sha256>",
		plan.mods.BundleSHA256:    "<bundle-mods sha256>",
	} {
		require.Contains(t, got, old)
		got = strings.ReplaceAll(got, old, repl)
	}
	requireGolden(t, "live-describe.golden.md", got)

	for _, want := range []string{
		"Run go run ./cmd/code and tell me the code word it prints, in one line.",
		"/compact Keep the code word the program printed.",
		"2.1.280", "claude-fake", "<bundle sha256>", "<bundle-mods sha256>", "22:00-07:00", "0.90", "0.60",
		"# Compact instructions", "When compacting, reproduce verbatim",
	} {
		require.Contains(t, got, want)
	}

	o.orderSeed = 0
	w.Reset()
	require.NoError(t, runLiveEval(context.Background(), o, env, &w))
	require.Contains(t, w.String(), "--order-seed", "with no fixed seed the order is drawn at run time, and the page says so")
}
