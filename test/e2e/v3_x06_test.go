// V3-VERIFY §5 X6: the §11.3 sublinear-growth guardrail, fed by the REAL store.
//
// SP-02 shipped testdata/golden/eval/growth/stats-growth.json as a W-2 shape fixture and left a
// provider seam for a real source. Wave 2 is the first point where a real session can be driven
// end to end, so this test performs the swap and proves the guardrail is measured, not fixtured:
// eval.Synthesize supplies only the CALL SEQUENCE (seedReadHeavy, readHeavy — the Phase 1 shapes,
// verbatim), every tool-result payload is real corpus bytes through SP-08's loader, every event is
// driven through observer.OnToolUse/OnUserPrompt against a real store.Open, and the sampled
// store.Stats series is what eval.CheckSublinearGrowth and the replay driver's --growth gate run
// over. (SP-08 + SP-06 + SP-02.)
//
// No wave-3 component appears here: the sampling loop below plays the role a scheduler-driven
// sampler would later own, composed inside this file per the §5 Rule.

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/eval"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
)

// x6Milestones are the 1-based turn counts after which store.Stats is sampled, verbatim from the
// X6 row: turns 25, 50, 75, 100, 150, 200, 300, 400.
var x6Milestones = []int{25, 50, 75, 100, 150, 200, 300, 400}

// x6DriveAndSample drives every event of s — one UserPromptSubmit per user turn, one PostToolUse
// per ToolCall, each carrying corpus bytes through corpusPayloadFor exactly as the Phase 1 harness
// does — through a real observer over a real store.Open on t.TempDir(), sampling store.Stats after
// each milestone turn into an []eval.StatsSample. A FakeClock advances one second per event; no
// wall clock is consulted anywhere.
func x6DriveAndSample(t *testing.T, s eval.Session) []eval.StatsSample {
	t.Helper()
	ctx := context.Background()

	root := t.TempDir()
	cfg := config.Defaults()
	cfg.Store.Canonicalize.Enabled = true

	clk := testutil.NewFakeClock(testutil.Epoch)
	log := logging.Nop()

	st, err := store.Open(root, cfg, store.Deps{Log: log, Clock: clk})
	require.NoError(t, err)
	defer func() { require.NoError(t, st.Close()) }()

	g, err := dag.Open(root, cfg, log)
	require.NoError(t, err)

	obsv, err := observer.New(observer.Options{
		ProjectRoot: root, Cfg: cfg, Store: st, Graph: g,
		Touch:   sketch.NewCMS(phase1CMSEpsilon, phase1CMSDelta),
		Explore: sketch.NewHLL(phase1HLLRegisters),
		Hot:     sketch.NewMisraGries(phase1MGCounters),
		Log:     log, Metrics: obs.New(clk), Clock: clk,
	})
	require.NoError(t, err)

	// The refresh seed is derived from the session ID exactly as eventsFor derives it, so the
	// served bytes are a pure function of (Synthesize seed, spec).
	sh := fnv.New64a()
	_, _ = sh.Write([]byte(s.ID))
	seed := sh.Sum64()

	c := loadCorpus(t)

	milestone := map[int]bool{}
	for _, m := range x6Milestones {
		milestone[m] = true
	}

	var samples []eval.StatsSample
	seq := 0
	for i, turn := range s.Turns {
		if turn.Role == "user" {
			clk.Advance(time.Second)
			_, err := obsv.OnUserPrompt(ctx, hookio.Event{
				HookEventName: hookUserPromptSubmit,
				SessionID:     core.SessionID(s.ID), Prompt: turn.Text,
			})
			require.NoError(t, err)
		}
		for _, tc := range turn.ToolCalls {
			body := corpusPayloadFor(c, tc.Name, tc.Paths, seq, seed)
			seq++
			clk.Advance(time.Second)
			_, err := obsv.OnToolUse(ctx, hookio.Event{
				HookEventName: hookPostToolUse,
				SessionID:     core.SessionID(s.ID), ToolName: tc.Name, ToolUseID: tc.ID,
				ToolInput:    toolInputFor(tc),
				ToolResponse: mustJSON(map[string]string{"content": string(body)}),
			})
			require.NoError(t, err)
		}
		if milestone[i+1] {
			stats, err := st.Stats(ctx)
			require.NoError(t, err)
			samples = append(samples, eval.StatsSample{
				Turn:     core.TurnIndex(i + 1),
				Objects:  stats.Objects,
				Bytes:    stats.Bytes,
				RawBytes: stats.RawBytes, DedupRatio: stats.DedupRatio,
			})
		}
	}
	return samples
}

// x6WriteGrowthFile writes samples as the []eval.StatsSample JSON the driver's --growth flag
// loads (test/replay/growth.go unmarshals exactly this shape), and returns the absolute path.
func x6WriteGrowthFile(t *testing.T, dir, name string, samples []eval.StatsSample) string {
	t.Helper()
	encoded, err := json.MarshalIndent(samples, "", "  ")
	require.NoError(t, err)
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, append(encoded, '\n'), 0o600))
	return path
}

// x6BuildDriver compiles ./test/replay once for this test and returns the executable's path. Built
// rather than `go run` so the two gate runs below pay for one compile.
func x6BuildDriver(t *testing.T, root string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "replay")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.CommandContext(context.Background(), "go", "build", "-o", bin, "./test/replay")
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Run(), "go build ./test/replay:\n%s", stderr.String())
	return bin
}

// x6RunDriver executes the replay driver from the module root (its repoRoot walks up from the
// working directory) and returns its exit code and stderr. A failure to START the process fails
// the test; a non-zero exit is returned, never asserted here — the exit code is what X6 checks.
func x6RunDriver(t *testing.T, root, bin string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), bin, args...)
	cmd.Dir = root
	var out, errw bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errw
	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0, errw.String()
	case errors.As(err, &exitErr):
		return exitErr.ExitCode(), errw.String()
	default:
		t.Fatalf("x6: could not run %s %v: %v\nstderr:\n%s", bin, args, err, errw.String())
		return -1, ""
	}
}

// x6DriverArgs is the committed replay-gate invocation (test/replay/e2e_test.go's end-to-end run)
// with the --growth fixture swapped for the file X6 measured off the real store.
func x6DriverArgs(growthPath, outPath string) []string {
	return []string{
		"--corpus", "testdata/sessions/synthetic",
		"--baseline", "testdata/baseline/phase0.json",
		"--phase", "0",
		"--growth", growthPath,
		"--sketch", "testdata/golden/eval/growth/health.json",
		"--out", outPath,
	}
}

// TestV3_ReplayGrowthGuardrailUsesRealStore is X6: observer → store.Stats →
// eval.CheckSublinearGrowth → test/replay --growth, with every sample measured off a real store.
func TestV3_ReplayGrowthGuardrailUsesRealStore(t *testing.T) {
	samples := x6DriveAndSample(t, eval.Synthesize(seedReadHeavy, readHeavy))
	require.Len(t, samples, len(x6Milestones),
		"the read-heavy session must reach turn 400 so every milestone is sampled")

	// RawBytes must be non-decreasing in Turn — otherwise CheckSublinearGrowth's verdict is
	// inconclusive, which fails. Asserted directly so a violation names the turn.
	for i := 1; i < len(samples); i++ {
		require.GreaterOrEqual(t, samples[i].RawBytes, samples[i-1].RawBytes,
			"RawBytes decreased between turn %d and turn %d: the session-length proxy is invalid",
			samples[i-1].Turn, samples[i].Turn)
	}

	// The guardrail itself, over the measured series: never inconclusive, ≥ 6 usable samples,
	// ≥ 8x raw-byte span, and a sublinear exponent.
	res := eval.CheckSublinearGrowth(samples)
	require.Empty(t, res.Reason, "the real-store series must never be inconclusive")
	require.GreaterOrEqual(t, res.Samples, 6, "at least 6 usable samples")
	require.GreaterOrEqual(t, res.RawSpan, 8.0, "RawSpan must be >= 8x")
	require.True(t, res.Sublinear, "store growth must be sublinear (exponent %.4f)", res.Exponent)
	require.LessOrEqual(t, res.Exponent, 0.95,
		"§11.3: the growth exponent must be <= 0.95, got %.4f", res.Exponent)
	// Record the exponent, as the X6 row and the §7 checklist ask.
	t.Logf("X6 growth exponent α = %.4f over %d samples spanning %.2fx raw bytes",
		res.Exponent, res.Samples, res.RawSpan)

	// The driver run: the committed gate invocation with the REAL growth file must exit 0.
	root, err := moduleRoot()
	require.NoError(t, err)
	tmp := t.TempDir()
	growthPath := x6WriteGrowthFile(t, tmp, "v3-growth.json", samples)
	bin := x6BuildDriver(t, root)

	code, stderr := x6RunDriver(t, root, bin,
		x6DriverArgs(growthPath, filepath.Join(tmp, "report-real.json"))...)
	require.Equal(t, 0, code,
		"the replay driver must exit 0 with the real growth file; stderr:\n%s", stderr)

	// Negative control: a synthetic LINEAR series (Bytes == RawBytes, exponent 1.0) over the same
	// turn axis and a >= 8x span must be judged not sublinear, and the driver must fail on it.
	linear := make([]eval.StatsSample, len(samples))
	for i, s := range samples {
		raw := int64(1_000) << i // 128x span, strictly increasing: usable but linear
		linear[i] = eval.StatsSample{Turn: s.Turn, Objects: s.Objects, Bytes: raw, RawBytes: raw, DedupRatio: 1.0}
	}
	linRes := eval.CheckSublinearGrowth(linear)
	require.Empty(t, linRes.Reason, "the control series must be usable, not inconclusive")
	require.False(t, linRes.Sublinear,
		"a Bytes == RawBytes series must never be judged sublinear (exponent %.4f)", linRes.Exponent)

	linearPath := x6WriteGrowthFile(t, tmp, "v3-growth-linear.json", linear)
	code, stderr = x6RunDriver(t, root, bin,
		x6DriverArgs(linearPath, filepath.Join(tmp, "report-linear.json"))...)
	require.NotEqual(t, 0, code, "the driver must exit non-zero on a linear growth series")
	require.Contains(t, stderr, "store growth is not sublinear",
		"the non-zero exit must come from the §11.3 growth gate, not some other check")
}
