package eval_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

// The transcript fixture is smoke2's real host transcript with every record the parser does not
// read replaced by a placeholder and every message body blanked: the operator's identity and the
// system prompt it carried are not in the repository.
const smoke2Transcript = "smoke2-plugin.transcript.redacted.jsonl"

// TestParseHostTranscript_HostMeasuredHooksAndTheRehydration pins the three facts only the
// transcript has: the host's own hook durations, the Qompack block the host injected after the
// compaction, and the rejected PreCompact output in the /compact turn's local output.
func TestParseHostTranscript_HostMeasuredHooksAndTheRehydration(t *testing.T) {
	raw := liveFixture(t, smoke2Transcript)
	for _, secret := range []string{"session_context", "prompt_snapshot", "userEmail"} {
		require.NotContains(t, string(raw), secret, "the committed fixture must stay redacted")
	}
	tr, err := eval.ParseHostTranscript(bytes.NewReader(raw))
	require.NoError(t, err)
	require.Equal(t, 67, tr.Lines)

	lat := tr.HookLatencies()
	require.Equal(t, map[string][]int64{
		"SessionStart:startup": {1249},
		"PostToolUse:Read":     {430},
		"Stop":                 {331, 338},
		"SessionStart:compact": {589},
	}, lat)

	inj, total := tr.QompackInjections()
	require.Len(t, inj, 1)
	require.Equal(t, "SessionStart", inj[0].HookEvent)
	require.True(t, strings.HasPrefix(inj[0].Text, "<!-- qompack:injected seq=1"))
	require.Contains(t, inj[0].Text, "Original user intent")
	require.Equal(t, inj[0].Bytes, total)
	require.Len(t, tr.Contexts, 2, "the startup contract probe is injected context too, but not a rehydration")

	require.Equal(t, []eval.HostCompaction{{Trigger: "manual", PreTokens: 25598, DurationMS: 12934}}, tr.Compactions)
	problems := tr.HookProblems()
	require.Len(t, problems, 1)
	require.Equal(t, "PreCompact", problems[0].Event)
	require.Equal(t, eval.HookProblemOutputRejected, problems[0].Kind)
}

func TestParseHostTranscript_TruncatedTailAndCorruptLine(t *testing.T) {
	raw := string(liveFixture(t, smoke2Transcript))
	tr, err := eval.ParseHostTranscript(strings.NewReader(raw + `{"type":"attachment","attachment":{"ty`))
	require.NoError(t, err)
	require.True(t, tr.TruncatedTail)

	lines := strings.SplitAfter(raw, "\n")
	_, err = eval.ParseHostTranscript(strings.NewReader(lines[0] + "{broken\n" + lines[1]))
	require.ErrorContains(t, err, "line 2")
}

// TestParseHostTranscript_UnseenHookRunTypeIsCounted: a hook-execution record kind this parser has
// not seen (2.1.280 writes only hook_success) is counted as a run and reported as a problem,
// rather than dropped because it was not predicted.
func TestParseHostTranscript_UnseenHookRunTypeIsCounted(t *testing.T) {
	line := `{"type":"attachment","attachment":{"type":"hook_non_blocking_error","hookName":"PreCompact","hookEvent":"PreCompact","exitCode":1,"durationMs":12}}` + "\n"
	tr, err := eval.ParseHostTranscript(strings.NewReader(line))
	require.NoError(t, err)
	require.Len(t, tr.HookRuns, 1)
	require.Equal(t, map[string][]int64{"PreCompact": {12}}, tr.HookLatencies())
	p := tr.HookProblems()
	require.Len(t, p, 1)
	require.Equal(t, eval.HookProblemOutcome, p[0].Kind)
	require.Contains(t, p[0].Detail, "hook_non_blocking_error")
}

// TestMergeHookProblems_OneFailureReportedTwiceIsOne: the stream and the transcript both carry the
// rejected PreCompact, in texts that differ; merged it is one problem, taken from the stream. Two
// real failures in both sources stay two, and a failure only stderr saw is kept.
func TestMergeHookProblems_OneFailureReportedTwiceIsOne(t *testing.T) {
	pre := func(turn int, detail string) eval.HostHookProblem {
		return eval.HostHookProblem{Turn: turn, Event: "PreCompact", Command: "q checkpoint", Kind: eval.HookProblemOutputRejected, Detail: detail}
	}
	end := eval.HostHookProblem{Turn: -1, Event: "SessionEnd", Command: "q flush", Kind: eval.HookProblemFailed, Detail: "Hook cancelled"}

	got := eval.MergeHookProblems([]eval.HostHookProblem{pre(1, "short")}, []eval.HostHookProblem{pre(-1, "longer text")}, []eval.HostHookProblem{end})
	require.Equal(t, []eval.HostHookProblem{pre(1, "short"), end}, got)

	got = eval.MergeHookProblems([]eval.HostHookProblem{pre(1, "a"), pre(3, "a")}, []eval.HostHookProblem{pre(-1, "a"), pre(-1, "a")})
	require.Len(t, got, 2)
	require.Equal(t, 3, got[1].Turn)

	got = eval.MergeHookProblems(nil, []eval.HostHookProblem{pre(-1, "only the transcript saw it")})
	require.Len(t, got, 1)
}
