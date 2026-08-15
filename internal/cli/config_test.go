package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// writeProjectConfig plants a .qompack/config.json under root and returns its path.
func writeProjectConfig(t *testing.T, root string, body string) string {
	t.Helper()
	l := paths.Of(root)
	require.NoError(t, os.MkdirAll(paths.Long(l.Dot), 0o700))
	p := filepath.Join(l.Dot, "config.json")
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	return p
}

// TestConfigPrint_Provenance is the test behind §11.4's advice to run `config print --provenance`
// first: the output must name not just the value but the layer and file that produced it.
func TestConfigPrint_Provenance(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeProjectConfig(t, dir, `{"scheduler":{"softFloorPct":0.42}}`)

	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), All(),
		[]string{"qompack", "config", "print", "--provenance"}, Env{
			Getenv:  envWith(map[string]string{"QOMPACK_PROJECT_ROOT": dir}),
			Clock:   testClock(),
			HomeDir: t.TempDir(),
		}, &out, &errw)
	require.Equal(t, ExitOK, code, "stderr=%s", errw.String())

	// The annotated line for the overridden leaf must carry the value, the layer and the file.
	var line string
	for _, l := range strings.Split(out.String(), "\n") {
		if strings.Contains(l, "softFloorPct") {
			line = l
			break
		}
	}
	require.NotEmpty(t, line, "provenance output must mention softFloorPct; got:\n%s", out.String())
	require.Contains(t, line, "0.42")
	require.Contains(t, line, "project")
	require.Contains(t, line, cfgPath)
}

// TestConfigSchema_Emits checks `config schema` is the machine-readable twin of the defaults, and
// that it does not drift from Config.JSONSchema.
func TestConfigSchema_Emits(t *testing.T) {
	t.Parallel()

	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), All(), []string{"qompack", "config", "schema"}, Env{
		Getenv:  envWith(map[string]string{"QOMPACK_PROJECT_ROOT": t.TempDir()}),
		Clock:   testClock(),
		HomeDir: t.TempDir(),
	}, &out, &errw)
	require.Equal(t, ExitOK, code, "stderr=%s", errw.String())

	var got any
	require.NoError(t, json.Unmarshal(out.Bytes(), &got), "schema must be valid JSON")

	var want any
	require.NoError(t, json.Unmarshal(config.Defaults().JSONSchema(), &want))
	require.Equal(t, want, got)
}

// TestSetFlagParsing proves --set is both parsed by the dispatcher and actually applied as the
// highest-precedence layer (§11.2), in both --set k=v and --set=k=v spellings.
func TestSetFlagParsing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), All(), []string{
		"qompack", "config", "print",
		"--set", "scheduler.cache.readMultiplier=0.08",
		"--set=eval.minSessions=5",
	}, Env{
		Getenv:  envWith(map[string]string{"QOMPACK_PROJECT_ROOT": dir}),
		Clock:   testClock(),
		HomeDir: t.TempDir(),
	}, &out, &errw)
	require.Equal(t, ExitOK, code, "stderr=%s", errw.String())

	var cfg config.Config
	require.NoError(t, json.Unmarshal(out.Bytes(), &cfg))
	require.InDelta(t, 0.08, cfg.Scheduler.Cache.ReadMultiplier, 1e-9)
	require.Equal(t, 5, cfg.Eval.MinSessions)
}

// TestSetFlagParsing_MalformedIsUsageErrorButHooksStillExitZero pins the asymmetry in Dispatch: a
// bad --set is the user's mistake for an ordinary command, but for a hook the ARGUMENTS came from
// the host, and failing a turn over them helps nobody.
func TestSetFlagParsing_MalformedIsUsageErrorButHooksStillExitZero(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		argv []string
		want int
	}{
		{"ordinary command, missing value", []string{"qompack", "config", "print", "--set"}, ExitUsage},
		{"ordinary command, no equals", []string{"qompack", "config", "print", "--set", "novalue"}, ExitUsage},
		{"hook, missing value", []string{"qompack", "observe", "tool", "--set"}, ExitOK},
		{"hook, no equals", []string{"qompack", "observe", "tool", "--set", "novalue"}, ExitOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errw bytes.Buffer
			code := Dispatch(context.Background(), All(), tc.argv, Env{
				Getenv: noEnv,
				Stdin:  strings.NewReader(`{"session_id":"s1"}`),
				Clock:  testClock(),
			}, &out, &errw)
			require.Equal(t, tc.want, code, "stderr=%s", errw.String())
		})
	}
}

// TestCLI_ConfigViolationsAreLoudAndPersisted is the cli half of §11.3. config.Load does the
// per-leaf fallback and returns the evidence; it cannot log or persist, because §3.2 gives it the
// allow-set {core}. This test asserts the reporting half actually happens.
func TestCLI_ConfigViolationsAreLoudAndPersisted(t *testing.T) {
	dir := t.TempDir()

	// Two leaves, both out of range: softFloorPct must be in (0,1) and minSessions in [1,∞).
	writeProjectConfig(t, dir, `{"scheduler":{"softFloorPct":9.5},"eval":{"minSessions":-3}}`)

	logDir := filepath.Join(t.TempDir(), "logs")
	require.NoError(t, os.MkdirAll(logDir, 0o700))
	log, closer, err := logging.New(logDir, logging.Info)
	require.NoError(t, err)
	defer func() { _ = closer.Close() }()

	cfg, _, err := LoadConfigAndReport(config.Env{
		ProjectRoot: dir,
		HomeDir:     t.TempDir(),
		Getenv:      noEnv,
	}, log, nil)
	require.NoError(t, err, "invalid config is never a crash (§11.3)")

	// Both leaves fell back to their defaults rather than keeping the invalid values.
	defs := config.Defaults()
	require.InDelta(t, defs.Scheduler.SoftFloorPct, cfg.Scheduler.SoftFloorPct, 1e-9)
	require.Equal(t, defs.Eval.MinSessions, cfg.Eval.MinSessions)

	// A Loud line per violation.
	loud := strings.Join(logging.LastLoud(), "\n")
	require.Contains(t, loud, "scheduler.softFloorPct")
	require.Contains(t, loud, "eval.minSessions")

	// And the typed list on disk, where /qompack:status and the next SessionStart can find it.
	p := filepath.Join(paths.Of(dir).State, configViolationsFile)
	raw, err := os.ReadFile(p)
	require.NoError(t, err, "violations must be persisted to %s", p)

	var violations []config.Violation
	require.NoError(t, json.Unmarshal(raw, &violations))
	require.Len(t, violations, 2)

	keys := make([]string, 0, len(violations))
	for _, v := range violations {
		keys = append(keys, v.Key)
		require.NotEmpty(t, v.Message, "a violation must say what was wrong")
		require.NotNil(t, v.Want, "a violation must say what was used instead")
	}
	require.ElementsMatch(t, []string{"scheduler.softFloorPct", "eval.minSessions"}, keys)
}

// TestLoud_CounterWiring closes the loop §3.2 forces open: logging cannot import obs, so the Loud
// counter only exists if a composition root wires it. This asserts the real registry sees it.
func TestLoud_CounterWiring(t *testing.T) {
	reg := obs.New(core.SystemClock())
	AttachLoudCounter(reg)
	// Leave no global observer behind for other tests in this package.
	defer AttachLoudCounter(nil)

	before := reg.Counter("loud.total").Value()

	cmds := []Cmd{{
		Name: "boom", Hook: true, Summary: "panics on purpose",
		Run: func(context.Context, Env, []string, io.Writer, io.Writer) error {
			panic("wiring check")
		},
	}}

	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), cmds, []string{"qompack", "boom"},
		Env{Getenv: noEnv, Clock: testClock()}, &out, &errw)
	require.Equal(t, ExitOK, code)

	require.Equal(t, before+1, reg.Counter("loud.total").Value(),
		"a Loud through the dispatcher must increment loud.total")
}
