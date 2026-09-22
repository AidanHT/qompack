package observer

// SP08-D1 (plans/V2-SP-08-carried-defects.md), V6 close-out. The carried-defect diagnosis recorded
// that OnToolUse unwraps the same tool_response several times per event: once for the body at
// step 3, once inside ExtractTestOutcome for the grammar's verdict symbol, and again inside
// ExtractSignals (ExtractTestOutcome a second time, and isGitCommit for a commit). Each unwrap of a
// 256 KB stdout payload is two JSON scans plus a string and a byte-slice copy of the whole text —
// about 10 % of OnToolUse on BenchmarkOnToolUse_TestOutput256KB.
//
// The exported §5.21 functions keep their shape; the observer's own path now hands them the text it
// already decoded. The pin is on bytes allocated, which co-load cannot inflate (ADR 0010): one
// event through the whole pipeline must allocate less than two decodes' worth of the payload.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/grammar"
)

// bytesAllocatedPerRun is testing.AllocsPerRun for bytes: the mean heap bytes f allocates per call.
func bytesAllocatedPerRun(runs int, f func()) float64 {
	f() // warm: first-call lazy initialization is not the cost being measured
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for range runs {
		f()
	}
	runtime.ReadMemStats(&after)
	return float64(after.TotalAlloc-before.TotalAlloc) / float64(runs)
}

// TestOnToolUseDecodesTheToolResponseOnce: a test-runner Bash result that is also a commit — the
// shape that reaches every responseText call site — costs OnToolUse less than two decodes of its
// payload, with the grammar wired so the verdict-symbol path runs too.
func TestOnToolUseDecodesTheToolResponseOnce(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.Grammar = grammar.New() })
	out := strings.Repeat("--- PASS: TestPoolStatistics (0.01s)\nok  \texample.com/pool\t0.412s\n", 6000)
	require.Greater(t, len(out), 256<<10, "the payload must be large enough that decoding dominates")
	require.Less(t, len(out), h.obs.maxResultBytes, "and small enough that the hot-path cap does not cut it")
	const command = "go test ./... && git commit -m 'pool statistics'"

	base := bashOf("toolu_probe", command, out)

	decode := bytesAllocatedPerRun(3, func() { _ = responseText(base) })

	ctx := context.Background()
	n := 0
	perEvent := bytesAllocatedPerRun(3, func() {
		n++
		e := base
		e.ToolUseID = core.ToolUseID(fmt.Sprintf("toolu_decode_%d", n))
		_, err := h.obs.OnToolUse(ctx, e)
		require.NoError(t, err)
	})
	t.Logf("one decode of the %d-byte payload: %.0f bytes; one OnToolUse: %.0f bytes", len(out), decode, perEvent)
	require.Less(t, perEvent, 2*decode,
		"one OnToolUse allocated %.0f bytes against %.0f for one decode of its tool_response: "+
			"the response is being unwrapped more than once per event", perEvent, decode)

	// And the verdicts the shared text feeds are the ones the exported functions give.
	sig := h.signals()
	require.NotEmpty(t, sig)
	want := ExtractSignals(base)
	require.Equal(t, want.TestPassed, sig[len(sig)-1].Signals.TestPassed)
	require.Equal(t, want.GitCommit, sig[len(sig)-1].Signals.GitCommit)
	require.True(t, want.TestPassed, "the fixture is a passing run")
	require.True(t, want.GitCommit, "and a commit")
	require.Equal(t, core.SessionID(testSession), sig[len(sig)-1].Session)
}

// oracleResponseText is responseText as it was written before SP08-D1's close-out: every shape
// tried in order, each through a full json.Unmarshal.
func oracleResponseText(e Event) []byte {
	raw := e.ToolResponse
	if len(raw) == 0 {
		return nil
	}
	if len(raw) > maxScanBytes {
		raw = raw[:maxScanBytes]
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return []byte(text)
	}
	var resp toolResponse
	if err := json.Unmarshal(raw, &resp); err == nil {
		if len(resp.Content) > 0 {
			var content string
			if err := json.Unmarshal(resp.Content, &content); err == nil {
				return []byte(content)
			}
			var blocks []contentBlock
			if err := json.Unmarshal(resp.Content, &blocks); err == nil {
				parts := make([]string, len(blocks))
				for i, block := range blocks {
					parts[i] = block.Text
				}
				return []byte(strings.Join(parts, "\n"))
			}
		}
		if resp.Stdout != "" || resp.Stderr != "" {
			if resp.Stderr == "" {
				return []byte(resp.Stdout)
			}
			return []byte(resp.Stdout + "\n" + resp.Stderr)
		}
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return raw
	}
	return compact.Bytes()
}

// TestResponseTextMatchesEveryShapeOfTheOracle: whatever responseText skips, it returns exactly
// what trying every shape in order returned — for every shape the host sends, for the degenerate
// ones, and for arbitrary bytes.
func TestResponseTextMatchesEveryShapeOfTheOracle(t *testing.T) {
	shapes := []string{
		``, `null`, ` null `, `"plain"`, "\t\n\r \"ws string\"", `"esc\u00e9\n"`, `"bad \x escape"`,
		`{"stdout":"out"}`, `{"stdout":"out","stderr":"err"}`, `{"stderr":"only"}`, `  {"stdout":"lead ws"}`,
		`{"content":"text"}`, `{"content":[{"type":"text","text":"a"},{"text":"b"}]}`, `{"content":7}`,
		`{"content":null,"stdout":"x"}`, `{"other":1}`, `{}`, `[1, 2]`, `[{"text":"x"}]`, `42`, `true`,
		`{"stdout":`, `{"stdout":"unterminated`, `"unterminated`, `{"stdout":"x"} trailing`, `"a" "b"`,
		"\ufeff{\"stdout\":\"bom\"}", `{"stdout":"x","stdout":"y"}`, `{"Stdout":"case"}`,
	}
	for _, s := range shapes {
		e := Event{ToolResponse: json.RawMessage(s)}
		require.Equal(t, oracleResponseText(e), responseText(e), "%q", s)
	}
	for name, b := range corpusTexts(t) {
		for _, wrap := range []string{`%s`, `{"stdout":%s}`, `{"content":%s}`, `{"content":[{"text":%s}]}`} {
			e := Event{ToolResponse: json.RawMessage(fmt.Sprintf(wrap, jsonString(string(b))))}
			require.Equal(t, oracleResponseText(e), responseText(e), "%s in %s", name, wrap)
		}
	}
	rapid.Check(t, func(rt *rapid.T) {
		b := rapid.SliceOf(rapid.SampledFrom([]byte(" \t\r\n{}[]\":,nul0123456789truefalsesdoutcontext\\x"))).Draw(rt, "b")
		e := Event{ToolResponse: json.RawMessage(b)}
		require.Equal(rt, oracleResponseText(e), responseText(e))
	})
}
