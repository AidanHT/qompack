package rehydrate

import (
	"context"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// ruleW1SkipMsg is the exact, mandatory Rule W-1 skip reason (00-ARCHITECTURE.md §5.22). devtool
// lint's stubskips sub-check greps test output for this literal string, so it must never be
// paraphrased. The tests below that need a REAL Build — as opposed to the builders and renderers
// this file owns — skip with it while build.go is still SP-01's stub.
const ruleW1SkipMsg = "behaviour: implementation is a stub (Rule W-1)"

// ── Wrap / Unwrap ──

// TestWrap_TagsThePayload pins §8.5's regeneration rule at the injection boundary: the payload is
// opened and closed with the checkpoint package's own tags, carrying the sequence it came from
// and checkpoint.SchemaVersion — never a locally duplicated version literal, which would drift.
func TestWrap_TagsThePayload(t *testing.T) {
	got := Wrap(core.CheckpointSeq(1), "# body\nline\n")

	require.True(t, strings.HasPrefix(got, "<!-- qompack:injected seq=1 ver=1 -->\n"),
		"payload must open with the seq/ver tag and a newline, got %q", got)
	require.True(t, strings.HasSuffix(got, checkpoint.InjectionCloseTag),
		"payload must end with the close tag, got %q", got)
	require.Less(t, strings.Index(got, "qompack:injected"), strings.Index(got, "/qompack:injected"),
		"the open tag must precede the close tag")
}

// TestWrap_UsesTheCheckpointSchemaVersion asserts the ver= value IS checkpoint.SchemaVersion.
// InjectionOpenTag's own doc comment says "ver is SchemaVersion"; a local payloadVersion constant
// would satisfy this test today and silently disagree with the checkpointer after the first
// schema bump.
func TestWrap_UsesTheCheckpointSchemaVersion(t *testing.T) {
	got := Wrap(core.CheckpointSeq(7), "x\n")

	require.Contains(t, got, " ver="+itoa(checkpoint.SchemaVersion)+" -->")
}

// TestUnwrap_RoundTrips asserts Unwrap is Wrap's exact inverse, for bodies with and without
// interior newlines. The round trip is what lets a later read of the transcript recover the
// injected body rather than re-encoding it (§4.6).
func TestUnwrap_RoundTrips(t *testing.T) {
	for _, body := range []string{
		"one line",
		"# Qompack rehydration — checkpoint 0007, session 3f2a9c81\n\n## 1. Invariants\n- x",
		"",
		"trailing newline inside the body\n",
	} {
		got, seq, ok := Unwrap(Wrap(core.CheckpointSeq(42), body))

		require.True(t, ok, "round trip must succeed for %q", body)
		require.Equal(t, body, got)
		require.Equal(t, core.CheckpointSeq(42), seq)
	}
}

// TestUnwrap_ToleratesTrailingContent covers the §12.1 contract probe, which the daemon appends
// on its own line AFTER the close tag. Unwrap must return the injected body and ignore it, not
// swallow it into the body and not reject the whole payload.
func TestUnwrap_ToleratesTrailingContent(t *testing.T) {
	payload := Wrap(core.CheckpointSeq(3), "body") + "\n<!-- qompack-contract-probe seq=3 -->\n"

	body, seq, ok := Unwrap(payload)

	require.True(t, ok)
	require.Equal(t, "body", body)
	require.Equal(t, core.CheckpointSeq(3), seq)
}

// TestUnwrap_RejectsMalformed asserts ok=false — never a partial body, never a panic — for every
// way the tags can be absent or broken. A caller that trusted a partial parse would strip the
// wrong span out of a transcript.
func TestUnwrap_RejectsMalformed(t *testing.T) {
	cases := map[string]string{
		"empty":                "",
		"no tags at all":       "# Qompack rehydration\nbody\n",
		"open tag only":        "<!-- qompack:injected seq=1 ver=1 -->\nbody\n",
		"close tag only":       "body\n" + checkpoint.InjectionCloseTag,
		"close before open":    checkpoint.InjectionCloseTag + "\n<!-- qompack:injected seq=1 ver=1 -->\n",
		"non-numeric seq":      "<!-- qompack:injected seq=abc ver=1 -->\nbody\n" + checkpoint.InjectionCloseTag,
		"non-numeric ver":      "<!-- qompack:injected seq=1 ver=x -->\nbody\n" + checkpoint.InjectionCloseTag,
		"missing ver":          "<!-- qompack:injected seq=1 -->\nbody\n" + checkpoint.InjectionCloseTag,
		"unterminated open":    "<!-- qompack:injected seq=1 ver=1\nbody\n" + checkpoint.InjectionCloseTag,
		"no newline after tag": "<!-- qompack:injected seq=1 ver=1 -->body\n" + checkpoint.InjectionCloseTag,
		"no newline before end": "<!-- qompack:injected seq=1 ver=1 -->\nbody" +
			checkpoint.InjectionCloseTag,
		"leading garbage": "noise <!-- qompack:injected seq=1 ver=1 -->\nbody\n" + checkpoint.InjectionCloseTag,
	}

	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			body, seq, ok := Unwrap(in)

			require.False(t, ok, "must be rejected")
			require.Empty(t, body, "a rejected payload yields no body")
			require.Zero(t, seq, "a rejected payload yields no sequence")
		})
	}
}

// TestUnwrap_AcceptsAFutureSchemaVersion asserts ver= is parsed but not gated. A payload written
// by a newer Qompack must still be recognizable as an injected span so that StripInjections's
// never-re-encode guarantee survives a version skew.
func TestUnwrap_AcceptsAFutureSchemaVersion(t *testing.T) {
	body, seq, ok := Unwrap("<!-- qompack:injected seq=9 ver=99 -->\nbody\n" + checkpoint.InjectionCloseTag)

	require.True(t, ok)
	require.Equal(t, "body", body)
	require.Equal(t, core.CheckpointSeq(9), seq)
}

// TestWrap_IsStrippableByTheCheckpointer closes the loop §8.5 exists for: a wrapped payload that
// found its way back into transcript-derived text is removed in full by checkpoint's own
// stripper, so it can never be encoded into the next checkpoint (§4.6).
func TestWrap_IsStrippableByTheCheckpointer(t *testing.T) {
	transcript := "before\n" + Wrap(core.CheckpointSeq(2), "injected body\nmore") + "\nafter"

	require.Equal(t, "before\n\nafter", checkpoint.StripInjections(transcript))
}

// ── AffordanceNotice ──

// TestAffordanceNotice_OmitsStandingInstruction is the guard on the inherited conformance case
// runStandingInstructionCase, which asserts that with NO item 3 the payload does not contain the
// standing instruction at all. Item 8 is emitted on every rehydration, so appending the sentence
// here would fail that case on every checkpoint with an empty eliminations set.
func TestAffordanceNotice_OmitsStandingInstruction(t *testing.T) {
	require.NotContains(t, AffordanceNotice(), StandingInstruction(),
		"the standing instruction belongs to item 3, not to item 8")
}

// TestAffordanceNotice_NamesEveryRetrievalTool asserts §8.6 item 8's job — "one line telling the
// agent that recall, re_read and already_tried exist" — is actually done, for every tool SP-13
// exposes. A notice that named the tools inconsistently would be worse than none: the model would
// call something that does not exist.
func TestAffordanceNotice_NamesEveryRetrievalTool(t *testing.T) {
	got := AffordanceNotice()

	for _, tool := range []string{
		"recall(query,k)", "expand(hash|tool_use_id)", "re_read(path,at)",
		"already_tried(target,approach)", "record_eliminated(target,approach,reason)",
		"timeline(from,to)", "why(decision_id)", "dropped()",
	} {
		require.Contains(t, got, tool)
	}
}

// TestAffordanceNotice_IsOneLine keeps item 8 to the single line §8.6 budgets for; it is
// concatenated into an already-tagged payload, so a stray newline would land verbatim.
func TestAffordanceNotice_IsOneLine(t *testing.T) {
	got := AffordanceNotice()

	require.NotContains(t, got, "\n")
	require.Equal(t, strings.TrimSpace(got), got)
}

// ── document header ──

// TestDocumentHeader_FormatsSeqAndSession pins the header line byte-for-byte: %04d of Ref.Seq and
// the session truncated to its first eight characters, which is what makes a payload identifiable
// in a transcript without spending budget on a full session id.
func TestDocumentHeader_FormatsSeqAndSession(t *testing.T) {
	var r Request
	r.Session = core.SessionID("3f2a9c81ffffffffffff")
	r.Ref.Seq = core.CheckpointSeq(7)

	require.Equal(t, "# Qompack rehydration — checkpoint 0007, session 3f2a9c81", documentHeader(r))
}

// TestDocumentHeader_ShortSessionIsNotPadded asserts a session id shorter than eight characters
// is emitted whole rather than padded or index-panicking.
func TestDocumentHeader_ShortSessionIsNotPadded(t *testing.T) {
	var r Request
	r.Session = core.SessionID("s1")
	r.Ref.Seq = core.CheckpointSeq(12345)

	require.Equal(t, "# Qompack rehydration — checkpoint 12345, session s1", documentHeader(r))
}

// ── item and body assembly ──

// TestItemText_PutsTheHeadingFirst asserts a section is its heading line followed by its admitted
// unit texts in order, ending in exactly one newline — the invariant renderBody's blank-line join
// depends on.
func TestItemText_PutsTheHeadingFirst(t *testing.T) {
	got := itemText(ItemInvariants, 2, 2, []string{"- [a] one\n", "- [b] two\n"})

	require.Equal(t, "## 1. Invariants (pinned, verbatim)\n- [a] one\n- [b] two\n", got)
}

// TestItemText_EliminationsHeadingCountsTopNOfSeen pins §8.6's "top 8 of 23 by slice relevance":
// the heading names how many elimination lines the builder produced and how many candidates it
// had, which is the same pair the trailing note is rendered from, so the two always agree.
func TestItemText_EliminationsHeadingCountsTopNOfSeen(t *testing.T) {
	got := itemText(ItemEliminations, 8, 23, []string{"- x\n"})

	require.True(t, strings.HasPrefix(got,
		"## 3. Approaches already eliminated (top 8 of 23 by slice relevance)\n"), "got %q", got)
}

// TestItemText_EmptyUnitsYieldNoSection asserts an item with nothing admitted renders as the empty
// string rather than as a bare heading, so renderBody never emits a section with no content.
func TestItemText_EmptyUnitsYieldNoSection(t *testing.T) {
	require.Equal(t, "", itemText(ItemDecisions, 0, 0, nil))
	require.Equal(t, "", itemText(ItemDecisions, 0, 0, []string{}))
}

// TestRenderBody_SeparatesSectionsWithOneBlankLine pins the payload layout: the header, then each
// section, joined so exactly one blank line falls between them, with no trailing newline (Wrap
// adds the one that precedes the close tag).
func TestRenderBody_SeparatesSectionsWithOneBlankLine(t *testing.T) {
	var r Request
	r.Session = core.SessionID("3f2a9c81ffff")
	r.Ref.Seq = core.CheckpointSeq(7)

	got := renderBody(r, []Item{
		{Kind: ItemInvariants, Text: "## 1. Invariants (pinned, verbatim)\n- [a] one\n"},
		{Kind: ItemAffordance, Text: "## 8. Retrieval\n" + AffordanceNotice() + "\n"},
	})

	require.Equal(t, strings.Join([]string{
		"# Qompack rehydration — checkpoint 0007, session 3f2a9c81",
		"",
		"## 1. Invariants (pinned, verbatim)",
		"- [a] one",
		"",
		"## 8. Retrieval",
		AffordanceNotice(),
	}, "\n"), got)
	require.False(t, strings.HasSuffix(got, "\n"), "Wrap supplies the newline before the close tag")
}

// TestRenderBody_SkipsEmptyItems asserts an Item whose Text is empty contributes no section and no
// blank line, so an omitted item leaves no trace in the payload.
func TestRenderBody_SkipsEmptyItems(t *testing.T) {
	var r Request
	r.Ref.Seq = core.CheckpointSeq(1)

	got := renderBody(r, []Item{
		{Kind: ItemInvariants, Text: ""},
		{Kind: ItemAffordance, Text: "## 8. Retrieval\nx\n"},
	})

	require.Equal(t, "# Qompack rehydration — checkpoint 0001, session \n\n## 8. Retrieval\nx", got)
}

// TestRenderBody_HeaderOnly asserts a rehydration with no items at all still produces a
// well-formed, newline-free-tailed body rather than a body ending in a dangling separator.
func TestRenderBody_HeaderOnly(t *testing.T) {
	var r Request
	r.Session = core.SessionID("abcdefgh12")
	r.Ref.Seq = core.CheckpointSeq(2)

	require.Equal(t, "# Qompack rehydration — checkpoint 0002, session abcdefgh", renderBody(r, nil))
}

// ── full-Build integration (skipped until the main session lands build.go) ──

// TestBuild_InjectionTagging is the payload-level assertion of §8.5 that the inherited suite's
// runInjectionTagCase also makes: the whole rehydration is wrapped, and Unwrap recovers the
// checkpoint sequence it came from. It needs a REAL Build, so it skips with the Rule W-1 message
// while build.go is still the stub.
func TestBuild_InjectionTagging(t *testing.T) {
	r := requestFor(t, ckFull(t), generousTestBudget)
	d := fullDeps(t, ckFull(t))

	got, err := Build(context.Background(), r, d)
	if core.IsNotImplemented(err) {
		t.Skip(ruleW1SkipMsg)
	}
	require.NoError(t, err)

	require.True(t, strings.HasPrefix(got.Text, "<!-- qompack:injected seq=1 ver=1 -->\n"), got.Text)
	require.True(t, strings.HasSuffix(got.Text, checkpoint.InjectionCloseTag))

	body, seq, ok := Unwrap(got.Text)
	require.True(t, ok, "Build's own payload must round-trip through Unwrap")
	require.Equal(t, core.CheckpointSeq(1), seq)
	require.Contains(t, body, "# Qompack rehydration — checkpoint 0001, session sess_01J")
}
