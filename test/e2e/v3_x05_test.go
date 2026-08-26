// X5 — TestV3_ReplayGateConsumesRealSketchHealth (plans/V3-VERIFY §5).
//
// Seams: negknow.Health → eval.SketchHealth → test/replay --sketch watch-for (SP-09 + SP-02 +
// SP-03). At wave 1 the gate read the committed fixture testdata/golden/eval/growth/health.json;
// Rule W-2 says this checkpoint re-runs the fixture-backed check against the REAL implementation:
// a real ledger's health, marshalled into the fixture's shape and handed to the real driver
// binary.
//
// Both driver runs pass --baseline "" deliberately (the same thing cb27449 had to add to
// test/integration/replaygrowth_test.go): since b103037, bloom_fp_rate and bloom_fill_ratio are
// ratio metrics judged at 2% against testdata/baseline/phase0.json's watch-fors (0.006 / 0.18),
// and a real ledger's health at a different fill would fail that RELATIVE comparison on a healthy
// store. The §11.4 ceiling is an ABSOLUTE limit and is what this test judges; the 2% rule keeps
// its own coverage in TestGate_WatchForsAreJudgedAsRatios and in B11 (§7.3). Without the empty
// baseline the negative arm below would fail for the watch-for comparison instead, and the
// failure would no longer be attributable to the ceiling sentence.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/eval"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/stretchr/testify/require"
)

// The Appendix C bloom defaults the healthy arm runs at, asserted against the loaded
// configuration rather than assumed, so the test states its premise. The overload count is what
// the negative control ingests against a PINNED capacity-10000 filter: 30 000 records add 60 000
// keys (Key + MatchKey each), which drives the estimated false-positive rate far past §11.4's
// hard 0.10 ceiling.
const (
	x5BloomCapacity  = 10_000
	x5BloomFPRate    = 0.01
	x5ActiveRecords  = 3_000
	x5OverloadCount  = 30_000
	x5BloomFPCeiling = 0.10
)

// x5Seed fixes the record-generation sequence: every target below embeds it, so two runs of this
// test mint byte-identical descriptors and the ingested population is reproducible.
const x5Seed = 0x5109_0005

// x5CeilingSentence is 00-ARCHITECTURE.md §11.4 verbatim, as test/replay prints it when the
// false-positive ceiling is breached. The negative arm asserts the SENTENCE, not only exit 1, so
// its failure is attributable to the ceiling rather than to any other FAIL line the gate can emit.
const x5CeilingSentence = "At 1% they are safe; at 10% the agent starts skipping viable approaches."

// The driver's exit vocabulary, and its CPU budget. The budget is CPU-time and not wall-clock:
// this test runs beside the rest of the e2e suite, and a wall budget would measure the runner's
// co-load rather than the driver's cost (§7.3, race-suite load sensitivity).
const (
	x5ExitOK         = 0
	x5ExitGateFailed = 1
	x5MaxCPU         = "2m"
)

// x5InitialEnv is the process environment before any test's t.Setenv mutated it.
// testutil.NewProject points HOME and USERPROFILE at a temp dir, and a `go build` inheriting that
// would resolve the module cache under an empty temp home.
var x5InitialEnv = os.Environ()

// x5Elimination is one deterministic active elimination. Targets are DISTINCT per index so no
// record is identity-deduped, and Evidence is non-zero because Appendix C's requireEvidence
// default is true and negknow.Record refuses an evidence-free record outright.
func x5Elimination(arm string, i int) negknow.Record {
	target := fmt.Sprintf("src/gen/%s_%08x_%05d.go:Handler%05d", arm, x5Seed, i, i)
	approach := fmt.Sprintf("widen retry budget variant %05d", i)
	reason := "the upstream pool ignores the widened budget in transaction mode"
	return negknow.Record{
		Target:   target,
		Approach: approach,
		Reason:   reason,
		Evidence: core.HashBytes(core.DomainRoot, []byte(fmt.Sprintf("x5-evidence-%s-%d", arm, i))),
	}
}

// x5OpenLedger opens a real negknow ledger over its own disposable project. The bloom argument is
// what pins or frees the filter: nil lets Open build the configured filter, a non-nil sized
// filter is adopted as-is — and never calling RebuildBloom afterwards is what "resizing disabled"
// means, since Record only adds keys to the filter it has and only a rebuild may replace it
// (§3.3, §13 invariant 3).
func x5OpenLedger(t *testing.T, b *sketch.Bloom, session string) negknow.Ledger {
	t.Helper()

	p := testutil.NewProject(t)
	require.Equal(t, x5BloomCapacity, p.Cfg.Sketches.Bloom.Capacity,
		"the healthy arm's premise is the Appendix C capacity default")
	require.InEpsilon(t, x5BloomFPRate, p.Cfg.Sketches.Bloom.FPRate, 1e-9,
		"the healthy arm's premise is the Appendix C fpRate default")

	led, err := negknow.Open(p.Root, p.Cfg, b, negknow.Deps{
		Session: core.SessionID(session),
		Log:     p.Log,
		Clock:   p.Clock,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = led.Close() })
	return led
}

// x5Ingest records n distinct active eliminations and asserts the ledger holds exactly n.
func x5Ingest(t *testing.T, led negknow.Ledger, arm string, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		_, err := led.Record(ctx, x5Elimination(arm, i))
		require.NoError(t, err, "%s: record %d", arm, i)
	}
	h := led.Health()
	require.Equal(t, n, h.Records, "%s: every distinct record must land, none deduped", arm)
	require.Equal(t, n, h.Active, "%s: every record was minted active", arm)
	require.Zero(t, h.Stale, arm)
}

// x5HealthFile marshals a ledger's Health into the eval.SketchHealth shape — the seam SP-09
// supplies through the same provider door the fixture held open — and writes it where the driver's
// --sketch flag will read it.
func x5HealthFile(t *testing.T, dir, name string, h negknow.Health) (path string, sh eval.SketchHealth) {
	t.Helper()
	sh = eval.SketchHealth{
		FillRatio: h.FillRatio,
		EstFPRate: h.EstFPRate,
		Records:   h.Records,
		Active:    h.Active,
		Stale:     h.Stale,
	}
	body, err := json.Marshal(sh)
	require.NoError(t, err)
	path = filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, body, 0o600))
	return path, sh
}

// x5KeySet returns the sorted top-level JSON keys of one object.
func x5KeySet(t *testing.T, raw []byte) []string {
	t.Helper()
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &m))
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// x5BuildReplayDriver compiles ./test/replay and returns the executable. The gate is driven as a
// process rather than in-package because package main's checks are unexported, and the artifact
// CI runs — flag parsing, exit codes and all — is the thing a wired-up provider has to survive.
func x5BuildReplayDriver(t *testing.T) string {
	t.Helper()

	root, err := moduleRoot()
	require.NoError(t, err)

	out := filepath.Join(t.TempDir(), "x5replay")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", out, "./test/replay")
	cmd.Dir = root
	cmd.Env = x5InitialEnv
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Run(), "go build -o %s ./test/replay:\n%s", out, stderr.String())
	return out
}

// x5RunReplayGate runs the driver over the committed synthetic corpus with --baseline "" and the
// given --sketch file, returning the exit code, both streams, and the report it wrote.
//
// The child environment is pinned: HOME and USERPROFILE at a fresh temp dir so the user-global
// configuration layer is empty, QOMPACK_PROJECT_ROOT at the repository so the gate resolves the
// configuration CI resolves — not whichever temp project the calling test last created via
// t.Setenv. --ci makes the phase checks non-negotiable.
func x5RunReplayGate(t *testing.T, bin, sketchFile string) (code int, stdout, stderr, reportPath string) {
	t.Helper()

	root, err := moduleRoot()
	require.NoError(t, err)

	dir := t.TempDir()
	reportPath = filepath.Join(dir, "report.json")
	cmd := exec.Command(bin,
		"--corpus", "testdata/sessions/synthetic",
		"--baseline", "",
		"--phase", "0",
		"--out", reportPath,
		"--max-cpu", x5MaxCPU,
		"--ci",
		"--sketch", sketchFile,
	)
	cmd.Dir = root
	// os/exec keeps the LAST value for a duplicated key, so these three override x5InitialEnv's.
	env := append([]string{}, x5InitialEnv...)
	env = append(env,
		"HOME="+dir,
		"USERPROFILE="+dir,
		"QOMPACK_PROJECT_ROOT="+root,
	)
	cmd.Env = env

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
		code = x5ExitOK
	case errors.As(runErr, &exitErr):
		code = exitErr.ExitCode()
	default:
		t.Fatalf("x5: could not run %s: %v\nstderr:\n%s", bin, runErr, errBuf.String())
	}
	return code, outBuf.String(), errBuf.String(), reportPath
}

// x5ReportWatchFor reads the watchFor map back out of the driver's own report artifact — the
// gate's emission, not this test's input echoed back.
func x5ReportWatchFor(t *testing.T, reportPath string) map[string]float64 {
	t.Helper()
	raw, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	var rep struct {
		WatchFor map[string]float64 `json:"watchFor"`
	}
	require.NoError(t, json.Unmarshal(raw, &rep))
	return rep.WatchFor
}

// TestV3_ReplayGateConsumesRealSketchHealth is §5 X5: the replay gate's §11.4 bloom watch-for,
// fed for the first time by a REAL ledger's health instead of the committed fixture — and, as the
// negative control, a genuinely saturated ledger that the gate must refuse with the §11.4
// sentence.
func TestV3_ReplayGateConsumesRealSketchHealth(t *testing.T) {
	ctx := context.Background()
	driver := x5BuildReplayDriver(t)
	healthDir := t.TempDir()

	// ---- Healthy arm: 3 000 active eliminations at the Appendix C defaults, then a rebuild. ----
	led := x5OpenLedger(t, nil, "sess_v3_x5_healthy")
	x5Ingest(t, led, "healthy", x5ActiveRecords)
	_, _, err := led.RebuildBloom(ctx)
	require.NoError(t, err)

	h := led.Health()
	t.Logf("healthy ledger: records=%d active=%d fill=%.4f estFP=%.6f needsResize=%t",
		h.Records, h.Active, h.FillRatio, h.EstFPRate, h.NeedsResize)

	// Expected output 1: the real ledger is healthy by §11.4's own numbers.
	require.LessOrEqual(t, h.FillRatio, 0.5)
	require.Less(t, h.EstFPRate, 0.02)
	require.False(t, h.NeedsResize)

	healthPath, sh := x5HealthFile(t, healthDir, "v3-health.json", h)

	// Expected output 3: the real numbers stay within the fixture's declared shape — same keys,
	// same types. The fixture was a W-2 shape contract, and this is the wave-2 moment it is
	// reconciled against reality.
	root, err := moduleRoot()
	require.NoError(t, err)
	fixtureRaw, err := os.ReadFile(filepath.Join(root,
		"testdata", "golden", "eval", "growth", "health.json"))
	require.NoError(t, err)
	realRaw, err := json.Marshal(sh)
	require.NoError(t, err)
	require.Equal(t, x5KeySet(t, fixtureRaw), x5KeySet(t, realRaw),
		"the fixture and the real negknow health must agree field for field, or the provider "+
			"swap silently forked the seam's shape")
	var fixture eval.SketchHealth
	dec := json.NewDecoder(bytes.NewReader(fixtureRaw))
	dec.DisallowUnknownFields()
	require.NoError(t, dec.Decode(&fixture),
		"every fixture key must decode into eval.SketchHealth with the type the field declares")
	t.Logf("fixture estFP=%.6f fill=%.4f vs real estFP=%.6f fill=%.4f",
		fixture.EstFPRate, fixture.FillRatio, sh.EstFPRate, sh.FillRatio)

	// Expected output 2: the gate consumes the real health, emits both watch-for metrics, and
	// does not trip the hard ceiling.
	code, stdout, stderr, reportPath := x5RunReplayGate(t, driver, healthPath)
	require.Equal(t, x5ExitOK, code, "stdout:\n%s\nstderr:\n%s", stdout, stderr)
	require.NotContains(t, stderr, x5CeilingSentence,
		"a healthy filter must not be refused under the §11.4 ceiling")
	require.LessOrEqual(t, h.EstFPRate, x5BloomFPCeiling)

	watch := x5ReportWatchFor(t, reportPath)
	require.Contains(t, watch, "bloom_fp_rate")
	require.Contains(t, watch, "bloom_fill_ratio")
	require.InDelta(t, h.EstFPRate, watch["bloom_fp_rate"], 1e-9,
		"the gate must report the number the ledger emitted, not a fixture's")
	require.InDelta(t, h.FillRatio, watch["bloom_fill_ratio"], 1e-9)

	// ---- Negative control: 30 000 active records against a PINNED capacity-10000 filter. ----
	// The filter is handed to Open pre-sized and RebuildBloom is never called, so nothing may
	// resize it: Record's incremental Adds saturate it, which is exactly the state §11.4's
	// absolute ceiling exists to refuse.
	pinned := sketch.NewBloom(x5BloomCapacity, x5BloomFPRate)
	led2 := x5OpenLedger(t, pinned, "sess_v3_x5_pinned")
	x5Ingest(t, led2, "pinned", x5OverloadCount)

	h2 := led2.Health()
	t.Logf("pinned ledger: records=%d fill=%.4f estFP=%.6f needsResize=%t",
		h2.Records, h2.FillRatio, h2.EstFPRate, h2.NeedsResize)
	require.Greater(t, h2.EstFPRate, x5BloomFPCeiling,
		"the control is only a control if the pinned filter really is over the ceiling")

	overPath, _ := x5HealthFile(t, healthDir, "v3-health-over-ceiling.json", h2)
	code, stdout, stderr, _ = x5RunReplayGate(t, driver, overPath)
	require.Equal(t, x5ExitGateFailed, code,
		"the gate must FAIL, and with the gate's own exit code — not bad-input, not a budget "+
			"breach\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	require.Contains(t, stderr, x5CeilingSentence,
		"the ceiling is a number with a reason, and the gate prints the reason; with "+
			`--baseline "" this failure is attributable to the ceiling alone`)
}
