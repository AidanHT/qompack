package eval_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

// The three recorded streams are the real stdout of Claude Code 2.1.280 sessions run for C5.4's
// harness validation (plans/sdd/V6-closeout/eval/runs/): smoke1 with no plugin and a /compact turn,
// smoke2 with the plugin loaded by --plugin-dir and a /compact turn, smoke3 with the plugin and
// automatic compaction forced by CLAUDE_CODE_AUTO_COMPACT_WINDOW / CLAUDE_AUTOCOMPACT_PCT_OVERRIDE.
const (
	smoke1Stream = "smoke1-stock.stream.jsonl"
	smoke2Stream = "smoke2-plugin.stream.jsonl"
	smoke3Stream = "smoke3-autocompact.stream.jsonl"
)

func liveFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "live", name))
	require.NoError(t, err)
	return raw
}

func parseLiveFixture(t *testing.T, name string) eval.HostStream {
	t.Helper()
	s, err := eval.ParseHostStream(bytes.NewReader(liveFixture(t, name)), nil)
	require.NoError(t, err)
	return s
}

// TestParseHostStream_StockCompactSession pins the shape the accounting rests on: one result per
// user message (the /compact turn included, as a local command with zero usage), requests
// deduplicated by message id, and the compaction boundary on the /compact turn.
func TestParseHostStream_StockCompactSession(t *testing.T) {
	s := parseLiveFixture(t, smoke1Stream)
	require.Equal(t, 26, s.Lines)
	require.False(t, s.TruncatedTail)
	require.NotNil(t, s.Init)
	require.Equal(t, "2.1.280", s.Init.ClaudeCodeVersion)
	require.Equal(t, "claude-haiku-4-5-20251001", s.Init.Model)
	require.False(t, s.PluginLoaded("qompack"), "the stock session loaded no Qompack plugin")
	require.Equal(t, 3, s.InitCount, "the host re-prints system/init per turn and after a compaction")
	require.Len(t, s.Turns, 3)

	t0 := s.Turns[0]
	require.Len(t, t0.Requests, 2, "four assistant lines share two message ids: two requests")
	require.Equal(t, "Read", t0.Requests[0].ToolUses[0].Name)
	require.Equal(t, "The release codeword is HELIOTROPE-47.", t0.Answer())
	require.Equal(t, int64(328), t0.Result.Usage.OutputTokens)
	require.NotNil(t, t0.Result.Usage.ThinkingTokens)
	require.Equal(t, int64(181), *t0.Result.Usage.ThinkingTokens)

	t1 := s.Turns[1]
	require.Equal(t, "compact", t1.Result.LocalCommand)
	require.Zero(t, t1.Result.NumTurns)
	require.Len(t, t1.Compactions, 1)
	require.Equal(t, "manual", t1.Compactions[0].Trigger)
	require.Equal(t, int64(25960), t1.Compactions[0].PreTokens)
	require.Equal(t, []eval.HostCompactAttempt{{Result: "success"}}, t1.CompactAttempts)
	require.Positive(t, t1.CompactSummaryChars)
	require.Equal(t, []string{"Compacted"}, t1.LocalOutput)
	require.Empty(t, s.HookProblems())

	require.Contains(t, s.Turns[2].Answer(), "8443")
}

// TestParseHostStream_PluginSessionReportsTheRejectedPreCompactOutput pins the review finding the
// harness must surface on every qompack-arm trial until C1.12 lands: Claude Code 2.1.280 rejects
// the PreCompact hook's hookSpecificOutput, and the only trace is the /compact turn's local-command
// output. The rehydration on SessionStart:compact still happened and is visible as a hook response.
func TestParseHostStream_PluginSessionReportsTheRejectedPreCompactOutput(t *testing.T) {
	s := parseLiveFixture(t, smoke2Stream)
	require.True(t, s.PluginLoaded("qompack"))
	require.Len(t, s.Turns, 3)

	problems := s.HookProblems()
	require.Len(t, problems, 1)
	p := problems[0]
	require.Equal(t, "PreCompact", p.Event)
	require.Equal(t, eval.HookProblemOutputRejected, p.Kind)
	require.Equal(t, 1, p.Turn)
	require.Contains(t, p.Command, "qompack")
	require.Contains(t, p.Detail, "hookSpecificOutput.hookEventName")

	var sawCompactStart bool
	for _, h := range s.Turns[1].Hooks {
		if h.Name == "SessionStart:compact" {
			sawCompactStart = true
			require.True(t, h.Responded)
			require.Equal(t, "success", h.Outcome)
			require.Contains(t, h.Output, "qompack:injected")
		}
	}
	require.True(t, sawCompactStart, "the rehydration hook ran after the compaction")
	require.Empty(t, s.HookLatencies(), "no receive times were supplied, so no pipe latency is invented")
}

// TestParseHostStream_AutoCompactionAndAFailedAttempt pins what automatic compaction looks like: a
// compact_boundary with trigger "auto" inside an ordinary turn, and a refused attempt reported only
// on a status line.
func TestParseHostStream_AutoCompactionAndAFailedAttempt(t *testing.T) {
	s := parseLiveFixture(t, smoke3Stream)
	var auto, failed int
	for _, turn := range s.Turns {
		for _, c := range turn.Compactions {
			if c.Trigger == "auto" {
				auto++
			}
		}
		for _, a := range turn.CompactAttempts {
			if a.Result == "failed" {
				failed++
				require.Equal(t, "too_few_groups", a.Error)
			}
		}
		require.Empty(t, turn.Result.LocalCommand, "no /compact was sent in this session")
	}
	require.Equal(t, 3, auto)
	require.Equal(t, 1, failed)
}

// TestParseHostStream_ReceiveTimesGiveHookLatency: with receive times the paired hook lines give a
// pipe-observed latency per hook name.
func TestParseHostStream_ReceiveTimesGiveHookLatency(t *testing.T) {
	raw := liveFixture(t, smoke2Stream)
	n := bytes.Count(raw, []byte("\n"))
	recv := make([]int64, n)
	for i := range recv {
		recv[i] = int64(10 * (i + 1))
	}
	s, err := eval.ParseHostStream(bytes.NewReader(raw), recv)
	require.NoError(t, err)
	lat := s.HookLatencies()
	require.Equal(t, []int64{10}, lat["SessionStart:startup"], "lines 1 and 2 are the startup hook's start and response")
	require.Len(t, lat["Stop"], 2)
	require.Positive(t, s.Turns[0].LastRecvMS)
}

// TestParseHostStream_TruncationAndCorruption: a cut-off final line is recorded; a malformed line
// followed by more output is an error naming it.
func TestParseHostStream_TruncationAndCorruption(t *testing.T) {
	raw := liveFixture(t, smoke1Stream)
	lines := strings.SplitAfter(string(raw), "\n")

	cut := strings.Join(lines[:13], "") + `{"type":"system","subtype":"sta`
	s, err := eval.ParseHostStream(strings.NewReader(cut), nil)
	require.NoError(t, err)
	require.True(t, s.TruncatedTail)
	require.Len(t, s.Turns, 1)

	corrupt := strings.Join(lines[:5], "") + "not json\n" + strings.Join(lines[5:], "")
	_, err = eval.ParseHostStream(strings.NewReader(corrupt), nil)
	require.ErrorContains(t, err, "line 6 is not JSON")
}

// TestParseHookFailures_StderrForm: the host prints the same report shape on stderr, where a
// cancelled SessionEnd hook lands.
func TestParseHookFailures_StderrForm(t *testing.T) {
	got := eval.ParseHookFailures("SessionEnd hook [\"${CLAUDE_PLUGIN_ROOT}/bin/qompack\" flush] failed: Hook cancelled\n", -1)
	require.Len(t, got, 1)
	require.Equal(t, "SessionEnd", got[0].Event, "the event is the last word before the bracket, not \"hook\"")
	require.Equal(t, eval.HookProblemFailed, got[0].Kind)
	require.Equal(t, "Hook cancelled", got[0].Detail)
}

// TestHostStream_PluginFromAndForeignPlugins: a qompack trial's plugin counts only when the host
// loaded it from the arm's own install — the inline --plugin-dir source, or the disposable
// marketplace — and every non-builtin plugin other than the arm's own is named, so a session that
// picked up an operator plugin (or a Qompack copy from somewhere else) is visible in its record.
func TestHostStream_PluginFromAndForeignPlugins(t *testing.T) {
	s := eval.HostStream{Init: &eval.HostInit{Plugins: []eval.HostPlugin{
		{Name: "qompack", Path: `C:\b\qompack-plugin`, Source: "qompack@inline", Version: "1"},
		{Name: "agents-md", Path: "builtin", Source: "agents-md@builtin"},
		{Name: "telemetry", Path: "builtin", Source: "telemetry@builtin"},
		{Name: "superpowers", Path: `C:\cache\superpowers`, Source: "superpowers@claude-plugins-official"},
	}}}
	pl, ok := s.PluginFrom("qompack", "qompack@inline")
	require.True(t, ok)
	require.Equal(t, `C:\b\qompack-plugin`, pl.Path)
	_, ok = s.PluginFrom("qompack", "qompack@qompack-live-eval")
	require.False(t, ok, "loaded, but not from the marketplace the arm installed through")
	_, ok = s.PluginFrom("qompack", "")
	require.True(t, ok, "an empty source accepts any")

	require.Equal(t, []string{"superpowers@claude-plugins-official"}, s.ForeignPlugins("qompack@inline"))
	require.Equal(t, []string{"qompack@inline", "superpowers@claude-plugins-official"}, s.ForeignPlugins(""),
		"on the stock arm every non-builtin plugin is foreign, Qompack included")

	var none eval.HostStream
	_, ok = none.PluginFrom("qompack", "")
	require.False(t, ok)
	require.Empty(t, none.ForeignPlugins(""))
}
