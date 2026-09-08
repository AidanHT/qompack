package commands_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/commands"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/obs"
)

// goldenDir is testdata/golden/commands/status/, relative to internal/commands. The repository
// keeps its golden files in one root-level tree rather than per-package testdata directories.
const goldenDir = "../../testdata/golden/commands/status"

// goldenPerm matches the rest of the repository's golden files: these are committed source, not
// runtime store files.
const goldenPerm = 0o644

// updateGolden reports whether -update was passed. It looks the flag up before registering it, so
// that two packages in one test binary share a single flag rather than panicking during init —
// the same arrangement internal/dag's golden_test.go documents.
var updateGolden = registerUpdateFlag()

func registerUpdateFlag() func() bool {
	const name = "update"
	if f := flag.Lookup(name); f != nil {
		return func() bool { return f.Value.String() == "true" }
	}
	p := flag.Bool(name, false, "rewrite golden files under testdata/golden/ instead of comparing")
	return func() bool { return *p }
}

// assertGolden compares got against testdata/golden/commands/status/<name>, or rewrites it under
// -update.
func assertGolden(t *testing.T, name, got string) {
	t.Helper()

	path := filepath.Join(goldenDir, name)
	if updateGolden() {
		require.NoError(t, os.MkdirAll(goldenDir, 0o755))
		require.NoError(t, os.WriteFile(path, []byte(got), goldenPerm))
		return
	}

	want, err := os.ReadFile(path)
	require.NoError(t, err, "missing golden %s; regenerate with -update", path)
	require.Equal(t, string(want), got, "%s is stale; regenerate with -update if the change is intended", path)
}

// fullReport is the state where the daemon answered and every instrument has samples.
func fullReport() commands.StatusReport {
	return commands.CollectStatus(context.Background(), commands.StatusSources{
		Daemon: func(context.Context) (commands.DaemonStatus, time.Time, error) {
			return commands.DaemonStatus{
				Mode: "active",
				Hot:  "sync",
				Contract: []contract.Result{
					{ID: "C1", OK: true},
				},
				Sessions: []json.RawMessage{json.RawMessage(`{"ID":"s-1"}`)},
				Latency: map[string]obs.HistSnapshot{
					"hook_controlled":     {N: 1200, P50: 3 * time.Millisecond, P95: 7 * time.Millisecond, P99: 9 * time.Millisecond, Max: 21 * time.Millisecond},
					"l0_ingest":           {N: 1200, P50: 800 * time.Microsecond, P95: 1500 * time.Microsecond, P99: 1900 * time.Microsecond, Max: 4 * time.Millisecond},
					"l0_process":          {N: 1190, P50: 12 * time.Millisecond, P95: 40 * time.Millisecond, P99: 61 * time.Millisecond, Max: 210 * time.Millisecond},
					"checkpoint_finalize": {N: 6, P50: 380 * time.Millisecond, P95: 900 * time.Millisecond, P99: 1200 * time.Millisecond, Max: 1400 * time.Millisecond},
					"mcp_tool_call":       {N: 44, P50: 6 * time.Millisecond, P95: 30 * time.Millisecond, P99: 44 * time.Millisecond, Max: 51 * time.Millisecond},
					"hook_degraded":       {N: 2, P50: 900 * time.Microsecond, P95: time.Millisecond, P99: time.Millisecond, Max: time.Millisecond},
				},
				Budgets: []obs.BudgetBreach{
					{Budget: "B-C", Observed: 61 * time.Millisecond, Limit: 50 * time.Millisecond, Windows: 2},
				},
				Counters:   map[string]int64{"l0_admission_daemon": 1200, "hotpath_degraded": 2},
				SpoolFiles: 0,
				LoudTail:   []string{"daemon: budget B-C over limit for 2 windows"},
			}, collectedAt, nil
		},
	}, collectedAt)
}

// degradedReport is the state where no daemon answered and the persisted metrics file did.
func degradedReport() commands.StatusReport {
	persisted := collectedAt.Add(-4 * time.Minute)
	return commands.CollectStatus(context.Background(), commands.StatusSources{
		Daemon: func(context.Context) (commands.DaemonStatus, time.Time, error) {
			return commands.DaemonStatus{}, time.Time{}, errors.New("dial: no daemon listening")
		},
		Disk: func(context.Context) (obs.Snapshot, error) {
			return obs.Snapshot{
				TS: core.UnixMilli(persisted.UnixMilli()),
				Hists: map[string]obs.HistSnapshot{
					"checkpoint_finalize": {N: 2, P50: 500 * time.Millisecond, P95: 700 * time.Millisecond, P99: 700 * time.Millisecond, Max: 700 * time.Millisecond},
					"hook_controlled":     {N: 40, P50: 4 * time.Millisecond, P95: 8 * time.Millisecond, P99: 11 * time.Millisecond, Max: 30 * time.Millisecond},
				},
			}, nil
		},
	}, collectedAt)
}

// unknownReport is the state where nothing was reachable at all.
func unknownReport() commands.StatusReport {
	return commands.CollectStatus(context.Background(), commands.StatusSources{}, collectedAt)
}

// TestRenderStatus_Golden pins the three rendering states the plan names. They are separate files
// because the difference between them is the whole point of the design: a reader must be able to
// tell a live answer from a stale one and a stale one from no answer.
func TestRenderStatus_Golden(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		rep  commands.StatusReport
	}{
		{"full.txt", fullReport()},
		{"degraded.txt", degradedReport()},
		{"unknown.txt", unknownReport()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			require.NoError(t, commands.RenderStatus(&out, tc.rep))
			assertGolden(t, tc.name, out.String())
		})
	}
}

// TestRenderStatus_JSONGolden pins the machine-readable half of the same three states, so the text
// and JSON renderings can be compared against each other rather than each drifting alone.
func TestRenderStatus_JSONGolden(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		rep  commands.StatusReport
	}{
		{"full.json", fullReport()},
		{"degraded.json", degradedReport()},
		{"unknown.json", unknownReport()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.MarshalIndent(tc.rep, "", "  ")
			require.NoError(t, err)
			assertGolden(t, tc.name, string(b)+"\n")
		})
	}
}

// TestRenderStatus_CarriesNoANSI is the "no ANSI" half of the Produces contract. A colour code in
// the middle of a golden file is a diff nobody can read, and it is worse in a piped log.
func TestRenderStatus_CarriesNoANSI(t *testing.T) {
	t.Parallel()

	for _, rep := range []commands.StatusReport{fullReport(), degradedReport(), unknownReport()} {
		var out bytes.Buffer
		require.NoError(t, commands.RenderStatus(&out, rep))
		require.NotContains(t, out.String(), "\x1b")
	}
}

// TestRenderStatus_IsDeterministic guards the counter map, whose iteration order Go deliberately
// randomizes.
func TestRenderStatus_IsDeterministic(t *testing.T) {
	t.Parallel()

	var first bytes.Buffer
	require.NoError(t, commands.RenderStatus(&first, fullReport()))
	for i := 0; i < 20; i++ {
		var again bytes.Buffer
		require.NoError(t, commands.RenderStatus(&again, fullReport()))
		require.Equal(t, first.String(), again.String())
	}
}

// TestRenderStatus_NeverPrintsAZeroForAnAbsentMeasurement is the SP14-M7-02 gate at the rendering
// layer. The collector already refuses to invent a number; this asserts the renderer does not
// invent one either by formatting a nil as "0".
func TestRenderStatus_NeverPrintsAZeroForAnAbsentMeasurement(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	require.NoError(t, commands.RenderStatus(&out, unknownReport()))
	text := out.String()

	require.NotContains(t, text, "p50 0µs", "an unmeasured row must not render as a measured zero")
	require.NotContains(t, text, "n=0")
	require.Contains(t, text, "unavailable")

	// Every hook and budget row must say so in words.
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "  B-") || strings.HasPrefix(line, "  Post") || strings.HasPrefix(line, "  Session") {
			require.NotContains(t, line, "p99 ", "%q must carry no percentile", line)
		}
	}
}

// TestRenderStatus_RetainsEstimatorSourceAndAge is the evidence rule: every displayed number says
// whether it was measured or derived, where it came from, and how old it is. Strip any one of the
// three and the figure becomes a claim the report cannot support.
func TestRenderStatus_RetainsEstimatorSourceAndAge(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	require.NoError(t, commands.RenderStatus(&out, fullReport()))
	text := out.String()

	require.Contains(t, text, "estimated from daemon", "B-A is derived and must say so")
	require.Contains(t, text, "observed from daemon", "a measured row must say so too")
	require.Contains(t, text, "old", "every figure carries the age of its observation")

	var stale bytes.Buffer
	require.NoError(t, commands.RenderStatus(&stale, degradedReport()))
	require.Contains(t, stale.String(), "from disk", "a fallback reading must name its source")
	require.Contains(t, stale.String(), "4.00m old", "a fallback reading must show how stale it is")
}
