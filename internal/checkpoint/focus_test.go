package checkpoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/stretchr/testify/require"
)

// focusGoldenDir is testdata/golden/checkpoints/focus/, relative to this package's own directory.
// The two files there are the emitted instruction byte for byte, so a paraphrase of any template
// fails here rather than reaching a live PreCompact call.
const focusGoldenDir = "../../testdata/golden/checkpoints/focus"

// readFocusGolden reads one focus golden and normalizes CRLF to LF, the way testutil.Golden does:
// .gitattributes marks the tree eol=lf, but a Windows checkout can still hand a tool CRLF bytes
// and that has nothing to do with the code under test.
func readFocusGolden(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(focusGoldenDir, name))
	require.NoError(t, err, "focus golden missing: %s", name)
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

// focusRef is the Ref every focus test passes in. FocusInstructions reads nothing out of it — the
// templates are deliberately content-free — so its only job is to be a realistic value.
func focusRef() Ref {
	return Ref{Seq: 7, Path: filepath.Join(".qompack", "checkpoints", "0007.json"), Bytes: 4821}
}

// TestFocusStandingTemplateVerbatim pins the emitted instruction against
// testdata/golden/checkpoints/focus/standing.txt for the golden's stated options
// (ForbidSnippets: true, no incremental span), and separately pins paragraph 1 against the
// standingTemplate constant so a golden rewrite alone cannot legitimize a paraphrase.
func TestFocusStandingTemplateVerbatim(t *testing.T) {
	got := FocusInstructions(Checkpoint{}, focusRef(), FocusOptions{ForbidSnippets: true})

	require.Equal(t, readFocusGolden(t, "standing.txt"), got)
	require.Equal(t, standingTemplate, strings.SplitN(got, "\n\n", 2)[0],
		"paragraph 1 must be standingTemplate verbatim (Qompack.md §8.5)")
	require.Equal(t, standingTemplate+"\n\n"+snippetProhibition, got)
}

// TestFocusStandingOnlyWhenAllOptionsOff asserts the zero FocusOptions emits exactly one
// paragraph: no span, no prohibition, no sentinel. The standing paragraph is unconditional, so
// the zero value still renders a non-empty focus text rather than an empty one.
func TestFocusStandingOnlyWhenAllOptionsOff(t *testing.T) {
	got := FocusInstructions(Checkpoint{}, focusRef(), FocusOptions{})

	require.Equal(t, standingTemplate, got)
	require.NotContains(t, got, SentinelPhrase)
	require.NotContains(t, got, "\n\n", "one paragraph means no paragraph separator")
}

// TestFocusIncrementalSpanNamesPathAndTurn is the O1 span-narrowing paragraph: it must name the
// artifact's path and the frontier turn, twice, in the two places the template spells them.
func TestFocusIncrementalSpanNamesPathAndTurn(t *testing.T) {
	got := FocusInstructions(Checkpoint{}, focusRef(), FocusOptions{
		IncrementalSpan: true,
		Frontier:        core.TurnIndex(58),
		CheckpointPath:  ".qompack/checkpoints/0007.json",
	})

	require.Equal(t, readFocusGolden(t, "incremental.txt"), got)
	require.Contains(t, got,
		"A durable checkpoint (`.qompack/checkpoints/0007.json`) fully covers the session through turn 58")
	require.Contains(t, got, "Summarize only what happened after turn 58")
}

// TestFocusOmitsSpanWhenFrontierZero covers the second half of the span guard: a frontier of 0 is
// store.SegmentLog.Frontier's "nothing encoded yet", deliberately indistinguishable from "turn 0
// covered", so naming it in an instruction would point the summarizer at a span that does not
// exist.
func TestFocusOmitsSpanWhenFrontierZero(t *testing.T) {
	got := FocusInstructions(Checkpoint{}, focusRef(), FocusOptions{
		IncrementalSpan: true,
		Frontier:        0,
		CheckpointPath:  ".qompack/checkpoints/0007.json",
	})

	require.Equal(t, standingTemplate, got)
	require.NotContains(t, got, "A durable checkpoint")
}

// TestFocusOmitsSpanWhenConfigDisabled is checkpoint.incrementalSpanInstruction:false as
// FocusInstructions sees it — PreCompact reads the config flag and passes IncrementalSpan
// accordingly, so at this level the disabled case is IncrementalSpan false with a real frontier.
func TestFocusOmitsSpanWhenConfigDisabled(t *testing.T) {
	got := FocusInstructions(Checkpoint{}, focusRef(), FocusOptions{
		IncrementalSpan: false,
		Frontier:        core.TurnIndex(58),
		CheckpointPath:  ".qompack/checkpoints/0007.json",
		ForbidSnippets:  true,
	})

	require.Equal(t, standingTemplate+"\n\n"+snippetProhibition, got)
	require.NotContains(t, got, "A durable checkpoint")
}

// TestFocusContainsSentinel asserts the G3.4 prohibition carries SentinelPhrase, the marker that
// identifies Qompack-authored focus text (and the value state/precompact.json records).
func TestFocusContainsSentinel(t *testing.T) {
	got := FocusInstructions(Checkpoint{}, focusRef(), FocusOptions{ForbidSnippets: true})

	require.Contains(t, got, SentinelPhrase)
	require.Equal(t, "qompack checkpoint", SentinelPhrase)
}

// TestFocusForwardSlashesOnWindows asserts the emitted path is forward-slash on every platform.
// The production caller hands FocusInstructions a paths.Norm result, which is already slash form;
// this asserts the function does not re-introduce a separator of its own when handed the
// platform-native spelling, which on Windows is the one that would leak a backslash into the most
// expensive call in the session.
func TestFocusForwardSlashesOnWindows(t *testing.T) {
	native := filepath.Join(".qompack", "checkpoints", "0007.json")

	got := FocusInstructions(Checkpoint{}, focusRef(), FocusOptions{
		IncrementalSpan: true,
		Frontier:        core.TurnIndex(58),
		CheckpointPath:  native,
	})

	require.Contains(t, got, ".qompack/checkpoints/0007.json")
	require.NotContains(t, got, `\`)

	// The same assertion against the real producer of the field, so this test fails if paths.Norm
	// ever stops returning slash form.
	root := t.TempDir()
	l := paths.Of(root)
	normed, err := paths.Norm(root, paths.CheckpointPath(l, core.CheckpointSeq(7)))
	require.NoError(t, err)
	require.Equal(t, ".qompack/checkpoints/0007.json", normed)
}

// TestFocusCappedAtFourThousandBytes asserts the payload cap holds and that it truncates at a
// paragraph boundary rather than mid-sentence: a half-sentence instruction is worse than a
// missing one, because the summarizer acts on it either way.
func TestFocusCappedAtFourThousandBytes(t *testing.T) {
	huge := strings.Repeat("p", 5000)

	got := FocusInstructions(Checkpoint{}, focusRef(), FocusOptions{
		IncrementalSpan: true,
		Frontier:        core.TurnIndex(58),
		CheckpointPath:  huge,
		ForbidSnippets:  true,
	})

	require.LessOrEqual(t, len(got), maxFocusBytes)
	require.Equal(t, standingTemplate, got, "the cap drops whole paragraphs, never part of one")
	for _, para := range strings.Split(got, "\n\n") {
		require.NotEmpty(t, para)
		require.Equal(t, strings.TrimSpace(para), para)
	}
}

// TestFocusFirstLineIsAProbePhrase pins paragraph 1 as the whole first line of the rendered text,
// at least 24 runes long. Until C1.18 that was the precondition of contract.probePhrase, which
// built precompact.custom_instructions_accepted's transcript probe from the first line of the
// recorded instruction. C1.18 retired the instruction and the probe and removed probePhrase; the
// shape is still §8.5's (the standing instruction first, whole, never wrapped), so it stays
// pinned, and the name stays because plans/sdd/V5-VERIFY/inventory-SP-10.md cites it.
func TestFocusFirstLineIsAProbePhrase(t *testing.T) {
	const minPhraseRunes = 24

	for _, o := range []FocusOptions{
		{},
		{ForbidSnippets: true},
		{IncrementalSpan: true, Frontier: 58, CheckpointPath: ".qompack/checkpoints/0007.json"},
		{IncrementalSpan: true, Frontier: 58, CheckpointPath: ".qompack/checkpoints/0007.json", ForbidSnippets: true},
	} {
		got := FocusInstructions(Checkpoint{}, focusRef(), o)
		first := strings.SplitN(got, "\n", 2)[0]

		require.Equal(t, standingTemplate, first, "paragraph 1 is always first and never wrapped")
		require.GreaterOrEqual(t, utf8.RuneCountInString(first), minPhraseRunes)
	}
}

// TestFocusTrimsTrailingWhitespace asserts the result carries no trailing whitespace, so the
// payload the host receives is exactly the paragraphs and nothing else.
func TestFocusTrimsTrailingWhitespace(t *testing.T) {
	for _, o := range []FocusOptions{{}, {ForbidSnippets: true}} {
		got := FocusInstructions(Checkpoint{}, focusRef(), o)
		require.Equal(t, strings.TrimRight(got, " \t\r\n"), got)
	}
}

// TestFocusTemplatesAreVerbatimConstants pins the three constants themselves, so that a change to
// any of them shows up as a diff in this file rather than only as a golden rewrite. The literals
// below are transcribed from plans/V4-SP-10-checkpointer-l4.md lines 1008-1024, which quotes
// Qompack.md §8.5 and §4.5.
func TestFocusTemplatesAreVerbatimConstants(t *testing.T) {
	require.Equal(t,
		"Encode what a competent engineer with no session history would get wrong. "+
			"Do not restate file contents, directory structure, or command output — those are retrievable. "+
			"Prioritise: intent, decisions and their rationale, approaches eliminated and why, and "+
			"constraints discovered empirically.",
		standingTemplate)

	require.Equal(t,
		"A durable checkpoint (`%s`) fully covers the session through turn %d, "+
			"including all decisions, eliminations, and file state up to that point. Do not re-summarize "+
			"that material. Summarize only what happened after turn %d: new decisions, new eliminations, "+
			"new intent, current work.",
		incrementalTemplate)

	require.Equal(t,
		"Do not include code snippets: every file named above is available by "+
			"path and hash through the qompack checkpoint, and a pointer costs roughly 20 tokens where a "+
			"snippet costs roughly 500.",
		snippetProhibition)
}

// forbiddenImports are the packages internal/checkpoint and internal/pins may never reach.
// hookio's absence is the mechanical form of the advisory-handling rule: with no transcript in
// scope, nothing in these packages can branch on whether the summarizer honoured either
// paragraph (§14 "Advisory handling is normative").
var forbiddenImports = []string{
	"github.com/qompack/qompack/internal/hookio",
	"github.com/qompack/qompack/internal/scheduler",
	"github.com/qompack/qompack/internal/ipc",
	"github.com/qompack/qompack/internal/daemon",
	"os/exec",
	"net",
	"net/http",
}

// allowedInternalImports is 00-ARCHITECTURE.md §3.2's checkpoint allow-set plus the foundation.
// pins' own allow-set is narrower; asserting the wider set over both still catches every
// forbidden edge, and devtool lint's import-graph check owns the narrower one.
var allowedInternalImports = map[string]bool{
	"core": true, "paths": true, "config": true, "logging": true, "obs": true,
	"store": true, "dag": true, "negknow": true, "pins": true, "grammar": true, "tokens": true,
}
