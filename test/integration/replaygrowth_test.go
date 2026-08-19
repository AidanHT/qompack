package integration

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
	"strings"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/chunk"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/eval"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/redact"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/symbols"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/qompack/qompack/internal/tokens"
	"github.com/stretchr/testify/require"
)

// The growth walk's shape. 256 puts of a 100 KB payload mutated 1 % per iteration, sampled at
// eight turns spanning 32x raw bytes — comfortably past CheckSublinearGrowth's >= 6 samples and
// >= 8x span rules, which exist so the fit cannot pretend to a precision it does not have.
//
// growthLineWidth * growthLines is exactly 102 400 bytes, and growthMutatedLines is 1 % of the
// lines. The mutated lines are CONTIGUOUS and the window rotates: that is one edit per turn in one
// place, the shape a real tool re-running over an evolving file produces, and it is what leaves
// most of the content-defined chunk boundaries where they were. A scattered 1 % would touch most
// of a 100 KB payload's chunks every turn and would be measuring re-chunking, not dedup.
const (
	growthTurns        = 256
	growthLineWidth    = 64
	growthLines        = 1600
	growthMutatedLines = growthLines / 100
	growthPayloadBytes = growthLineWidth * growthLines
	growthSampleCount  = 8
	growthRawSpan      = 32.0
)

// growthTruncatedSamples is deliberately below CheckSublinearGrowth's floor of 6, so the truncated
// re-run exercises the "refuse to judge" branch rather than a smaller fit.
const growthTruncatedSamples = 3

// growthPath is the paths.Key-form path every put in the walk is filed under, so the store sees
// one logical artifact re-produced 256 times rather than 256 unrelated ones.
const growthPath = "build/testrunner-output.txt"

// growthChunkCacheFile is the per-project token measurement cache tokens.NewExact persists under
// <root>/.qompack/state (G10.2). internal/store spells the same name in its own defaultDeps; this
// test builds the deps explicitly instead of letting them default, so it has to spell it too.
const growthChunkCacheFile = "chunktokens.bin"

// growthSampleTurns are the turns Stats is sampled at: 8 samples, 8 -> 256, a 32x span in raw
// bytes because every put hands the store the same number of bytes.
var growthSampleTurns = map[int]bool{8: true, 16: true, 32: true, 64: true, 128: true, 192: true, 224: true, 256: true}

// The replay gate's own vocabulary, as this test drives it from outside the driver's package.
//
// replayExitGateFailed is the driver's exit 1 — "the gate ran and found something" — and asserting
// it exactly rather than "non-zero" is what keeps a truncated growth file from passing this test
// by failing for some unrelated reason (exit 2 bad input, 3 wall clock, 5 phase checks disabled).
const (
	replayExitOK         = 0
	replayExitGateFailed = 1
	replayMaxWall        = 2 * time.Minute
)

// bloomCeilingSentence is 00-ARCHITECTURE.md §11.4 verbatim, as test/replay prints it when the
// false-positive ceiling is breached. The gate quotes the sentence so the number carries its
// reason, and this test asserts the sentence rather than only the exit code for the same reason.
const bloomCeilingSentence = "At 1% they are safe; at 10% the agent starts skipping viable approaches."

// The §11.4 bloom parameters: Appendix A's worked example, and the synthetic rate that has to be
// refused. bloomOverCeilingFPRate is just past the 0.10 ceiling; bloomFPTolerance is wide enough
// for the difference between the configured p and the filter's own observed-fill estimate at
// exactly n entries, and narrow enough that a filter sized an order of magnitude wrong fails.
const (
	bloomCapacity          = 10_000
	bloomTargetFPRate      = 0.01
	bloomFPTolerance       = 0.002
	bloomOverCeilingFPRate = 0.11
)

// initialEnv is the process environment as it was before any test ran. Child processes are built
// from this rather than from a live os.Environ() because testutil.NewProject points HOME and
// USERPROFILE at a temporary directory via t.Setenv, and a `go build` inheriting that would
// resolve GOPATH — and with it the module cache — under an empty temp home.
var initialEnv = os.Environ()

// TestIntegration_RealStoreGrowthIsSublinear is §11.3's "store growth sublinear in session length
// after dedup", measured for the first time against a real store rather than a fixture.
//
// SP-02 committed testdata/golden/eval/growth/stats-growth.json as a W-2 placeholder because
// internal/eval is foundation-only and no store existed to sample: the guardrail could be tested
// but not exercised. Wave 1 landed the store, so this test supplies the provider the seam was cut
// for — and then asserts the placeholder is still shape-compatible with what the real provider
// emits, which is the only way to know the swap does not require editing internal/eval.
func TestIntegration_RealStoreGrowthIsSublinear(t *testing.T) {
	samples := growthWalk(t)
	require.Len(t, samples, growthSampleCount)

	got := eval.CheckSublinearGrowth(samples)
	t.Logf("real store growth: exponent %.4f over %d samples spanning %.1fx raw bytes (sublinear=%t)",
		got.Exponent, got.Samples, got.RawSpan, got.Sublinear)

	require.Empty(t, got.Reason, "a conclusive verdict explains nothing")
	require.True(t, got.Sublinear)
	require.Less(t, got.Exponent, 1.0,
		"an exponent of 1 is dedup achieving nothing: stored bytes tracking raw bytes one for one")
	require.Equal(t, growthSampleCount, got.Samples)
	require.InDelta(t, growthRawSpan, got.RawSpan, 0.01,
		"every put hands the store the same byte count, so turn 256 must hold exactly 32x turn 8")

	// The provider seam, proven swappable: the committed placeholder and the real samples must
	// serialize to the same field set, or the driver's loadGrowthSamples would need a second
	// shape and internal/eval a second type.
	realJSON, err := json.Marshal(samples)
	require.NoError(t, err)
	fixtureJSON, err := os.ReadFile(growthFixturePath(t))
	require.NoError(t, err)

	require.Equal(t, growthFieldSet(t, fixtureJSON), growthFieldSet(t, realJSON),
		"the W-2 placeholder and the real store.Stats provider must agree field for field")

	var fixture []eval.StatsSample
	require.NoError(t, json.Unmarshal(fixtureJSON, &fixture))
	require.NotEmpty(t, fixture)
	for i, s := range fixture {
		require.Greater(t, s.DedupRatio, 1.0, "fixture sample %d claims dedup achieved nothing", i)
	}
	for i, s := range samples {
		require.Greater(t, s.DedupRatio, 1.0, "real sample %d: raw %d bytes stored as %d",
			i, s.RawBytes, s.Bytes)
	}
}

// TestIntegration_ReplayGateAcceptsRealGrowthFile drives the CI gate itself — the real driver
// binary, the real corpus, the real baseline — over samples a real store produced, which is what
// separates "the guardrail is tested" from "the guardrail is wired to a producer".
//
// Then it truncates the same file to three samples and asserts the gate refuses to judge. An
// inconclusive verdict must FAIL: a guardrail that reports "we could not tell" as green is how a
// store that grows linearly ships.
func TestIntegration_ReplayGateAcceptsRealGrowthFile(t *testing.T) {
	// Built before growthWalk's testutil.NewProject redirects HOME for the process; see initialEnv.
	driver := buildReplayDriver(t)

	samples := growthWalk(t)
	dir := t.TempDir()

	full := filepath.Join(dir, "growth-real.json")
	growthWriteJSON(t, full, samples)
	truncated := filepath.Join(dir, "growth-truncated.json")
	growthWriteJSON(t, truncated, samples[:growthTruncatedSamples])

	code, stdout, stderr := runReplayGate(t, driver, "--growth", full)
	require.Equal(t, replayExitOK, code, "stdout:\n%s\nstderr:\n%s", stdout, stderr)
	require.Contains(t, stdout, "sublinear=true",
		"the gate has to report the verdict it reached, not only its exit code")

	code, stdout, stderr = runReplayGate(t, driver, "--growth", truncated)
	require.Equal(t, replayExitGateFailed, code, "stdout:\n%s\nstderr:\n%s", stdout, stderr)
	require.Contains(t, stderr, "at least 6",
		"the failure has to name the sample floor, so the fix is obvious from the CI log")
}

// TestIntegration_RealBloomHealthFeedsTheFPCeiling closes the §11.4 half of the same provider
// seam: a real sketch.Bloom's health, mapped into eval.SketchHealth and fed to the gate.
//
// It asserts NOTHING about Records, Active, Stale or already_tried. Those are negative-knowledge
// numbers and negknow is SP-09, wave 2; the two fields a filter can answer for on its own are the
// two this test fills in.
func TestIntegration_RealBloomHealthFeedsTheFPCeiling(t *testing.T) {
	b := sketch.NewBloom(bloomCapacity, bloomTargetFPRate)
	for i := 0; i < bloomCapacity; i++ {
		b.Add([]byte(fmt.Sprintf("approach:%d", i)))
	}
	st := b.Stats()

	health := eval.SketchHealth{FillRatio: st.FillRatio, EstFPRate: st.EstFPRate}
	t.Logf("real bloom at capacity: fill %.4f, estimated fp %.4f (m=%d bits, k=%d)",
		health.FillRatio, health.EstFPRate, st.MBits, st.K)

	require.InDelta(t, bloomTargetFPRate, health.EstFPRate, bloomFPTolerance,
		"a filter sized for 1%% at 10 000 entries has to land near 1%% once it holds 10 000")
	require.False(t, st.Saturated, "§11.4's watch-for must not already be tripped at capacity")

	dir := t.TempDir()
	realHealthFile := filepath.Join(dir, "health-real.json")
	growthWriteJSON(t, realHealthFile, health)

	over := health
	over.EstFPRate = bloomOverCeilingFPRate
	overCeilingFile := filepath.Join(dir, "health-over-ceiling.json")
	growthWriteJSON(t, overCeilingFile, over)

	driver := buildReplayDriver(t)

	code, stdout, stderr := runReplayGate(t, driver, "--sketch", realHealthFile)
	require.Equal(t, replayExitOK, code, "stdout:\n%s\nstderr:\n%s", stdout, stderr)

	code, stdout, stderr = runReplayGate(t, driver, "--sketch", overCeilingFile)
	require.Equal(t, replayExitGateFailed, code, "stdout:\n%s\nstderr:\n%s", stdout, stderr)
	require.Contains(t, stderr, bloomCeilingSentence,
		"the ceiling is a number with a reason, and the gate prints the reason")
}

// growthWalk runs the 256-put walk against a real store with every dependency real, and returns
// the eight eval.StatsSample observations.
//
// Every dep is constructed explicitly rather than left nil for store.Open to default. The
// defaults would produce the same objects, but the point of a composition-root test is that the
// composition is visible: this is chunk + canon + symbols + tokens + redact + store in one call,
// which no internal package's §3.2 allow-set permits.
func growthWalk(t *testing.T) []eval.StatsSample {
	t.Helper()

	p := testutil.NewProject(t)
	l := paths.Of(p.Root)
	s, err := store.Open(p.Root, p.Cfg, store.Deps{
		Chunker: chunk.New(chunk.FromConfig(p.Cfg)),
		Canon:   canon.Default(p.Cfg.Store.Canonicalize),
		Symbols: symbols.New(),
		Tokens:  tokens.NewExact(p.Cfg, tokens.DefaultCalibPath(), filepath.Join(l.State, growthChunkCacheFile)),
		Redact:  redact.New(p.Cfg),
		Log:     p.Log,
		Clock:   p.Clock,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	ctx := context.Background()
	opts := store.PutOptions{
		Tool:  "Bash",
		Path:  growthPath,
		Canon: canon.OptionsFrom(p.Cfg.Store.Canonicalize, false),
	}

	lines := make([]string, growthLines)
	for i := range lines {
		lines[i] = growthLine(i, 0)
	}

	var samples []eval.StatsSample
	for turn := 1; turn <= growthTurns; turn++ {
		growthMutate(lines, turn)
		body := []byte(strings.Join(lines, ""))
		require.Len(t, body, growthPayloadBytes)

		_, err := s.PutBytes(ctx, body, opts)
		require.NoError(t, err, "turn %d", turn)

		if !growthSampleTurns[turn] {
			continue
		}
		st, statsErr := s.Stats(ctx)
		require.NoError(t, statsErr)
		samples = append(samples, eval.StatsSample{
			Turn:       core.TurnIndex(turn),
			Objects:    st.Objects,
			Bytes:      st.Bytes,
			RawBytes:   st.RawBytes,
			DedupRatio: st.DedupRatio,
		})
	}
	return samples
}

// growthLine renders one line of the payload, padded to exactly growthLineWidth bytes including
// the newline so the payload size is arithmetic rather than a measurement.
//
// The content deliberately carries no timestamp, ANSI escape, pid, address, temp path or duration:
// those are the classes the canonicalizers strip, and a payload full of them would make this test
// measure canonicalization rather than deduplication. §4.1 is where canon is the subject.
func growthLine(index, revision int) string {
	s := fmt.Sprintf("ok  pkg/mod%04d/case%04d  rev=%06d  status=pass", index, index, revision)
	if len(s) > growthLineWidth-1 {
		s = s[:growthLineWidth-1]
	}
	return s + strings.Repeat(" ", growthLineWidth-1-len(s)) + "\n"
}

// growthMutate rewrites 1 % of the lines in place: a contiguous window whose start rotates with
// the turn, so successive turns edit successive regions and the payload keeps drifting rather
// than oscillating between two states.
func growthMutate(lines []string, turn int) {
	start := (turn * growthMutatedLines) % (growthLines - growthMutatedLines)
	for i := start; i < start+growthMutatedLines; i++ {
		lines[i] = growthLine(i, turn)
	}
}

// growthFieldSet returns the sorted JSON field names of every element of a StatsSample array, and
// fails if the elements disagree with each other.
func growthFieldSet(t *testing.T, raw []byte) []string {
	t.Helper()

	var elems []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &elems))
	require.NotEmpty(t, elems)

	var want []string
	for i, e := range elems {
		keys := make([]string, 0, len(e))
		for k := range e {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if i == 0 {
			want = keys
			continue
		}
		require.Equal(t, want, keys, "element %d carries a different field set", i)
	}
	return want
}

// growthWriteJSON writes v to path as the driver's --growth / --sketch flags expect to read it.
func growthWriteJSON(t *testing.T, path string, v any) {
	t.Helper()
	body, err := json.Marshal(v)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, body, 0o600))
}

// growthFixturePath resolves SP-02's committed W-2 placeholder.
//
// It moved from testdata/golden/contracts/store/ to testdata/golden/eval/ this checkpoint
// (V2-MERGE-18): the contracts directories belong to SP-06 and SP-09, neither MANIFEST declared
// this file, and the first -update run that noticed it would have deleted it.
func growthFixturePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(growthModuleRoot(t), "testdata", "golden", "eval", "growth", "stats-growth.json")
}

// growthModuleRoot walks up from the test's working directory to the directory holding go.mod.
func growthModuleRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, parent, dir, "walked past the filesystem root without finding go.mod")
		dir = parent
	}
}

// buildReplayDriver compiles ./test/replay and returns the executable.
//
// The gate is driven as a process rather than by calling into it, because it is package main and
// its checks are unexported: test/replay's own e2e test calls run() in-process, which is the same
// code path but only reachable from inside that package. Building it here keeps the thing under
// test the artifact CI actually runs — flag parsing, exit codes and all — which is the half of the
// gate a wired-up provider has to survive.
func buildReplayDriver(t *testing.T) string {
	t.Helper()

	out := filepath.Join(t.TempDir(), "replay")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}

	cmd := exec.Command("go", "build", "-o", out, "./test/replay")
	cmd.Dir = growthModuleRoot(t)
	cmd.Env = initialEnv
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Run(), "go build -o %s ./test/replay:\n%s", out, stderr.String())
	return out
}

// runReplayGate runs the driver with the CI replay-gate job's own flags plus extra, and returns
// its exit code and both streams.
//
// The child's environment is pinned rather than inherited live: it starts from initialEnv, HOME
// and USERPROFILE point at a fresh temporary directory so the user-global configuration layer is
// empty, and QOMPACK_PROJECT_ROOT at the repository, so the gate resolves exactly the
// configuration CI resolves no matter which project the calling test happened to create. --ci is
// what makes the phase checks non-negotiable.
func runReplayGate(t *testing.T, bin string, extra ...string) (code int, stdout, stderr string) {
	t.Helper()

	root := growthModuleRoot(t)
	dir := t.TempDir()
	args := append([]string{
		"--corpus", "testdata/sessions/synthetic",
		"--baseline", "testdata/baseline/phase0.json",
		"--phase", "0",
		"--out", filepath.Join(dir, "report.json"),
		"--max-wall", replayMaxWall.String(),
		"--ci",
	}, extra...)

	cmd := exec.Command(bin, args...)
	cmd.Dir = root
	// os/exec keeps the LAST value for a duplicated key, so these three override initialEnv's.
	env := append([]string{}, initialEnv...)
	env = append(env,
		"HOME="+dir,
		"USERPROFILE="+dir,
		"QOMPACK_PROJECT_ROOT="+root,
	)
	cmd.Env = env

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		code = replayExitOK
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	default:
		t.Fatalf("integration: could not run %s %v: %v\nstderr:\n%s", bin, args, err, errBuf.String())
	}
	return code, outBuf.String(), errBuf.String()
}
