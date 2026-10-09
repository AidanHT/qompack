package checkpoint_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// openTag renders a real injection open tag for seq at the current schema version, the way every
// producer of an injected span does (rehydrate wraps its digest, checkpoint tags its focus
// instruction). The tests below never spell the tag out by hand, so a change to the constant's
// text is caught by the assertions rather than silently duplicated in the fixtures.
func openTag(seq int) string {
	return fmt.Sprintf(checkpoint.InjectionOpenTag, seq, checkpoint.SchemaVersion)
}

// openPrefix is InjectionOpenTag's fixed, verb-free head — the bytes StripInjections scans for,
// and the exact and only bytes a neutralized splice artifact costs. Derived from the exported
// constant, like openTag, so a change to the tag's spelling cannot leave a test pinned to a stale
// copy of it.
func openPrefix() string {
	return checkpoint.InjectionOpenTag[:strings.IndexByte(checkpoint.InjectionOpenTag, '%')]
}

// legacyOpenTag and legacyOpenPrefix are openTag and openPrefix for the 0.3.x spelling, which
// transcripts still hold and StripInjections still strips.
func legacyOpenTag(seq int) string {
	return fmt.Sprintf(checkpoint.LegacyInjectionOpenTag, seq, checkpoint.SchemaVersion)
}

func legacyOpenPrefix() string {
	return checkpoint.LegacyInjectionOpenTag[:strings.IndexByte(checkpoint.LegacyInjectionOpenTag, '%')]
}

// isSubsequence reports whether a can be obtained from b by deleting bytes, without reordering or
// inventing any. Stripping is a deletion, so this holds of every (input, output) pair; an
// implementation that rewrote, reordered or fabricated bytes would fail it.
func isSubsequence(a, b string) bool {
	i := 0
	for j := 0; i < len(a) && j < len(b); j++ {
		if a[i] == b[j] {
			i++
		}
	}
	return i == len(a)
}

// TestStripInjections_RemovesWholeSpans covers the ordinary cases: no tags at all, one span, and
// text on both sides of a span.
func TestStripInjections_RemovesWholeSpans(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "no tags is the identity", in: "ordinary transcript text", want: "ordinary transcript text"},
		{
			name: "one span is removed entirely",
			in:   openTag(1) + "injected digest body" + checkpoint.InjectionCloseTag,
			want: "",
		},
		{
			name: "surrounding text survives",
			in:   "before " + openTag(7) + "injected" + checkpoint.InjectionCloseTag + " after",
			want: "before  after",
		},
		{
			name: "an empty span is removed",
			in:   "a" + openTag(2) + checkpoint.InjectionCloseTag + "b",
			want: "ab",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, checkpoint.StripInjections(tc.in))
		})
	}
}

// TestStripInjections_IsNonGreedy pins the first normative property: each span ends at the FIRST
// close tag after its own open tag, so two adjacent injected spans are two spans and the ordinary
// text between them survives. A greedy implementation would swallow "kept between" here.
func TestStripInjections_IsNonGreedy(t *testing.T) {
	in := "head" +
		openTag(1) + "first injected" + checkpoint.InjectionCloseTag +
		"kept between" +
		openTag(2) + "second injected" + checkpoint.InjectionCloseTag +
		"tail"

	require.Equal(t, "headkept betweentail", checkpoint.StripInjections(in))
}

// TestStripInjections_MissingCloseTagDropsToEnd pins the second normative property. A truncated
// transcript is exactly the case where an open tag has no close tag, and keeping the remainder
// would re-encode an injected body into the next checkpoint — the compress-a-compression failure
// §4.6 forbids.
func TestStripInjections_MissingCloseTagDropsToEnd(t *testing.T) {
	t.Run("open tag with no close tag", func(t *testing.T) {
		in := "kept" + openTag(4) + "everything after the open tag is injected"
		require.Equal(t, "kept", checkpoint.StripInjections(in))
	})

	t.Run("open tag truncated mid-tag", func(t *testing.T) {
		// The open tag itself is cut off before its own " -->" terminator.
		in := "kept<!-- qompack:injected seq="
		require.Equal(t, "kept", checkpoint.StripInjections(in))
	})

	t.Run("a complete span followed by an unterminated one", func(t *testing.T) {
		in := "a" + openTag(1) + "x" + checkpoint.InjectionCloseTag + "b" + openTag(2) + "y"
		require.Equal(t, "ab", checkpoint.StripInjections(in))
	})
}

// TestStripInjections_StrayCloseTagIsOrdinaryText pins the third normative property: a close tag
// with no open tag before it delimits nothing, so it is content Qompack did not write and must
// not silently edit.
func TestStripInjections_StrayCloseTagIsOrdinaryText(t *testing.T) {
	in := "before " + checkpoint.InjectionCloseTag + " after"
	require.Equal(t, in, checkpoint.StripInjections(in))
}

// TestStripInjections_RecognizesEverySeqAndVersion asserts the scan keys off the tag's fixed
// prefix rather than off any particular seq/ver pair, so a span written by an older schema
// version is still stripped.
func TestStripInjections_RecognizesEverySeqAndVersion(t *testing.T) {
	for _, seq := range []int{0, 1, 9, 1234} {
		in := "x" + fmt.Sprintf(checkpoint.InjectionOpenTag, seq, 0) + "body" + checkpoint.InjectionCloseTag + "y"
		require.Equal(t, "xy", checkpoint.StripInjections(in), "seq=%d ver=0", seq)
	}
}

// TestStripInjections_IsIdempotent asserts stripping twice equals stripping once, for every
// fixture the cases above use. This is the property SP-08 relies on when the same prompt text is
// read back through more than one code path.
func TestStripInjections_IsIdempotent(t *testing.T) {
	inputs := []string{
		"",
		"plain text",
		openTag(1) + "body" + checkpoint.InjectionCloseTag,
		"a" + openTag(1) + "b" + checkpoint.InjectionCloseTag + "c" + openTag(2) + "d" + checkpoint.InjectionCloseTag + "e",
		"kept" + openTag(3) + "unterminated",
		checkpoint.InjectionCloseTag + " stray",
	}
	for i, in := range inputs {
		once := checkpoint.StripInjections(in)
		require.Equal(t, once, checkpoint.StripInjections(once), "input %d: %q", i, in)
	}
}

// TestInjectionTags_AreTheFrozenSpellings pins the two constants byte-for-byte. SP-08 injects
// through them, SP-11 wraps through them and SP-15 asserts them, so a whitespace change here
// would silently orphan every span already written into a transcript.
//
// 0.3.2 renamed the model-visible spelling from qompack:injected to qompack:session-record (the
// c55-c8 eval saw a model call an "injected-looking" block untrustworthy); the legacy pair is pinned
// too, because spans written by 0.3.x must keep stripping.
func TestInjectionTags_AreTheFrozenSpellings(t *testing.T) {
	require.Equal(t, "<!-- qompack:session-record seq=%d ver=%d -->", checkpoint.InjectionOpenTag)
	require.Equal(t, "<!-- /qompack:session-record -->", checkpoint.InjectionCloseTag)
	require.Equal(t, "<!-- qompack:session-record seq=7 ver=1 -->", openTag(7),
		"SchemaVersion must render as the ver= value of a real open tag")
	require.Equal(t, "<!-- qompack:injected seq=%d ver=%d -->", checkpoint.LegacyInjectionOpenTag)
	require.Equal(t, "<!-- /qompack:injected -->", checkpoint.LegacyInjectionCloseTag)
}

// TestStripInjections_LegacySpellingKeepsEveryGuarantee runs the three normative properties, the
// count and the splice neutralization over the 0.3.x spelling, and over the two spellings mixed: a
// span ends only at its own spelling's close tag, the other spelling's being ordinary text.
func TestStripInjections_LegacySpellingKeepsEveryGuarantee(t *testing.T) {
	lclose := checkpoint.LegacyInjectionCloseTag
	cases := []struct {
		name, in, want string
		spans          int
	}{
		{"whole legacy span", "before " + legacyOpenTag(7) + "x" + lclose + " after", "before  after", 1},
		{"non-greedy", "a" + legacyOpenTag(1) + "x" + lclose + "kept" + legacyOpenTag(2) + "y" + lclose + "b", "akeptb", 2},
		{"missing close drops to end", "kept" + legacyOpenTag(4) + "everything after", "kept", 1},
		{"truncated open tag", "kept" + legacyOpenPrefix(), "kept", 1},
		{"stray legacy close is text", "before " + lclose + " after", "before " + lclose + " after", 0},
		{"legacy then current", "a" + legacyOpenTag(1) + "x" + lclose + "b" + openTag(2) + "y" + checkpoint.InjectionCloseTag + "c", "abc", 2},
		{"a current close does not end a legacy span", "a" + legacyOpenTag(1) + "x" + checkpoint.InjectionCloseTag + "b", "a", 1},
		{"a legacy close does not end a current span", "a" + openTag(1) + "x" + lclose + "b", "a", 1},
		{
			"a legacy splice costs its prefix alone",
			"<!-- qompack:injected se" + legacyOpenTag(1) + "X" + lclose + "q=" + "prose<!-- c -->tail",
			"prose<!-- c -->tail", 2,
		},
		{
			"a current splice around a legacy span costs its prefix alone",
			"<!-- qompack:session-record se" + legacyOpenTag(1) + "X" + lclose + "q=" + "prose<!-- c -->tail",
			"prose<!-- c -->tail", 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, n := checkpoint.StripInjectionsCount(tc.in)
			require.Equal(t, tc.want, out)
			require.Equal(t, tc.spans, n)
			require.Equal(t, out, checkpoint.StripInjections(out), "idempotent")
			require.NotContains(t, out, legacyOpenPrefix())
			require.NotContains(t, out, openPrefix())
		})
	}
}

// ── SP-10 additions. Everything below extends the seven shipped tests above; none of them are
// modified, and the three normative properties they pin are inherited verbatim by
// StripInjectionsCount (§5 of plans/V4-SP-10-checkpointer-l4.md). ──

// stripFixture is one shared input for the SP-10 injection tests: the input string and the
// number of injected spans a correct scan consumes from it. The set reproduces the shipped
// tests' own fixtures — including the stray-close-tag and truncated-open-tag cases — so the
// counting wrapper is held to agreement on exactly the inputs whose string behaviour is already
// pinned above.
type stripFixture struct {
	name string
	in   string
	// spans is the number of open tags a correct scan consumes: a span whose close tag is
	// missing still counts, because its body was recognized and dropped.
	spans int
}

// shippedStripFixtures returns the fixture set of the seven shipped tests, each with its
// expected span count.
func shippedStripFixtures() []stripFixture {
	return []stripFixture{
		{name: "empty", in: "", spans: 0},
		{name: "no tags", in: "ordinary transcript text", spans: 0},
		{name: "one whole span", in: openTag(1) + "injected digest body" + checkpoint.InjectionCloseTag, spans: 1},
		{name: "text on both sides", in: "before " + openTag(7) + "injected" + checkpoint.InjectionCloseTag + " after", spans: 1},
		{name: "empty span", in: "a" + openTag(2) + checkpoint.InjectionCloseTag + "b", spans: 1},
		{
			name: "two adjacent spans are two spans",
			in: "head" + openTag(1) + "first injected" + checkpoint.InjectionCloseTag +
				"kept between" + openTag(2) + "second injected" + checkpoint.InjectionCloseTag + "tail",
			spans: 2,
		},
		{name: "open tag with no close tag", in: "kept" + openTag(4) + "everything after the open tag is injected", spans: 1},
		{name: "open tag truncated mid-tag", in: "kept<!-- qompack:injected seq=", spans: 1},
		{
			name:  "complete span then unterminated one",
			in:    "a" + openTag(1) + "x" + checkpoint.InjectionCloseTag + "b" + openTag(2) + "y",
			spans: 2,
		},
		{name: "stray close tag is ordinary text", in: "before " + checkpoint.InjectionCloseTag + " after", spans: 0},
		{name: "older schema version span", in: "x" + fmt.Sprintf(checkpoint.InjectionOpenTag, 9, 0) + "body" + checkpoint.InjectionCloseTag + "y", spans: 1},
	}
}

// TestStripInjectionsPairedBlock is the plan's canonical paired-span case: the open tag, its
// body and the close tag vanish, the surrounding lines survive untouched, and exactly one span
// is counted. It would fail against a scan that also ate the newline before the open tag or
// after the close tag, and against a count keyed on close tags rather than open tags.
func TestStripInjectionsPairedBlock(t *testing.T) {
	in := "a\n" + checkpoint.OpenTag(7) + "\nX\n" + checkpoint.InjectionCloseTag + "\nb"
	out, n := checkpoint.StripInjectionsCount(in)
	require.Equal(t, "a\n\nb", out)
	require.Equal(t, 1, n)
}

// TestStripInjectionsUnmatchedOpen pins the §4.6 leak-closing behaviour on the counting wrapper:
// an unterminated open tag drops everything to the END of the string, paragraph breaks included,
// and still counts as one consumed span. An implementation that resumed keeping text at the next
// "\n\n" would keep "keep me" — which is precisely re-encoding an injected body out of a
// truncated transcript, the compress-a-compression failure this slice exists to close.
func TestStripInjectionsUnmatchedOpen(t *testing.T) {
	in := "a\n" + checkpoint.OpenTag(7) + "\nX\n\nkeep me"
	out, n := checkpoint.StripInjectionsCount(in)
	require.Equal(t, "a\n", out)
	require.Equal(t, 1, n)
}

// TestStripInjectionsCountAgreesWithStripInjections runs both functions over the shipped tests'
// own fixture set and requires (a) the first return value of StripInjectionsCount to equal
// StripInjections exactly, and (b) the count to equal the number of open tags consumed.
//
// The string half documents the one-function delegation §5 mandates — it can only fail if
// someone splits the two into separate scans that then drift. The count half is independently
// falsifiable today: counting close tags would fail the truncated-open and unterminated cases
// (want 1, close tag absent), and counting only completed spans would fail them the same way.
func TestStripInjectionsCountAgreesWithStripInjections(t *testing.T) {
	for _, tc := range shippedStripFixtures() {
		t.Run(tc.name, func(t *testing.T) {
			out, n := checkpoint.StripInjectionsCount(tc.in)
			require.Equal(t, checkpoint.StripInjections(tc.in), out,
				"StripInjectionsCount's string result must be StripInjections's, verbatim")
			require.Equal(t, tc.spans, n, "count must be the number of open tags consumed")
		})
	}
}

// TestStripInjectionsLeavesCleanTextAlone is the identity property: text containing no injection
// open prefix passes through byte-for-byte with a zero count. The generator deliberately builds
// strings out of near-tag fragments — comment openers, close tags, "qompack", "seq=" — so the
// boundary is probed hard; any assembly that completes the real open prefix is filtered out,
// because that string is no longer clean. 10 strings per rapid iteration at the default 100
// checks gives the plan's 1 000 strings (-rapid.checks scales it).
//
// It would fail against a scanner keyed on anything looser than the full open prefix — for
// example one that treated a stray close tag, a bare "<!--", or the word "qompack:injected"
// without comment syntax as a span delimiter (the "mangled residue" pass §5 deliberately
// rejects).
func TestStripInjectionsLeavesCleanTextAlone(t *testing.T) {
	frag := rapid.OneOf(
		rapid.SampledFrom([]string{
			"ordinary prose about the refresh-token pool",
			" ", "\n", "\n\n", "<", ">", "<!--", "-->", "<!-- comment -->",
			"qompack", ":injected", ":session-record", "seq=", "ver=1", " seq=3",
			checkpoint.InjectionCloseTag, checkpoint.LegacyInjectionCloseTag,
			"<!- qompack:injected seq=",
			"<!-- qompack:injectedseq=",
			"<!- qompack:session-record seq=",
			"<!-- qompack:session-recordseq=",
		}),
		rapid.String(),
	)
	rapid.Check(t, func(rt *rapid.T) {
		batch := rapid.SliceOfN(rapid.SliceOfN(frag, 0, 24), 10, 10).Draw(rt, "batch")
		for _, pieces := range batch {
			s := strings.Join(pieces, "")
			out, n := checkpoint.StripInjectionsCount(s)
			if strings.Contains(s, openPrefix()) || strings.Contains(s, legacyOpenPrefix()) {
				// Not clean text: the fragments assembled a real open prefix, so something is
				// legitimately stripped and the identity assertion below cannot apply. The
				// generator reaches this arm often, so rather than dropping those draws on the
				// floor they are held to the properties that hold for EVERY input: bytes may be
				// deleted but never invented or reordered, the documented postcondition holds
				// (no open prefix survives, which is what makes the scan idempotent outright),
				// and a recognized prefix is always accounted for in the count.
				require.True(rt, isSubsequence(out, s),
					"the scan may delete bytes but must never invent or reorder them: %q -> %q", s, out)
				require.NotContains(rt, out, openPrefix(),
					"no open prefix may survive the scan: %q -> %q", s, out)
				require.NotContains(rt, out, legacyOpenPrefix(),
					"no legacy open prefix may survive the scan: %q -> %q", s, out)
				require.Equal(rt, out, checkpoint.StripInjections(out),
					"the scan must be idempotent: %q", s)
				require.NotZero(rt, n,
					"an assembled open prefix is either a consumed span or a neutralized artifact: %q", s)
				continue
			}
			require.Equal(rt, s, out, "clean text must pass through byte-for-byte")
			require.Zero(rt, n, "clean text must count zero spans")
		}
	})
}

// spliceAround plants interior immediately downstream of a spliced-together open tag. The shape
// is the one that broke idempotence in the shipped SP-01 scanner: kept text ending in a PARTIAL
// open-tag prefix, a real injected span, then kept text beginning with the prefix's completion.
// Removing the span joins the two kept halves into a well-formed open tag nobody wrote, and
// interior is the reader's own words sitting right behind that artifact — the bytes a neutralizer
// that deletes more than the artifact itself will take with it.
func spliceAround(interior string) string {
	return "<!-- qompack:injected se" +
		openTag(1) + "X" + checkpoint.InjectionCloseTag +
		"q=" + interior
}

// spliceRegressionInput is spliceAround with an interior that happens to complete the assembled
// tag ("7 ver=1 -->") before any of the reader's text. It is the one splice input whose downstream
// " -->" belongs to the artifact rather than to the reader, which is exactly why it cannot on its
// own distinguish a neutralizer that stops at the prefix from one that runs on to the next arrow.
// Used by the regression test below and seeded into FuzzStripInjections.
func spliceRegressionInput() string {
	return spliceAround("7 ver=1 -->tail")
}

// spliceCase is one splice fixture: a name and the reader-written interior planted behind the
// artifact. The expected output is always that interior, verbatim — that is the whole contract.
type spliceCase struct {
	name     string
	interior string
}

// spliceInteriorCases are the interiors that separate "the assembled prefix was removed" from
// "everything up to the next arrow was removed". Each one puts a " -->" downstream of the
// artifact that does NOT belong to the artifact, so a neutralizer bounded by that arrow destroys
// the reader's text in between. Shared with FuzzStripInjections, which seeds them.
func spliceInteriorCases() []spliceCase {
	// Long enough that a failure is unmistakably an unbounded deletion rather than an off-by-one.
	prose := strings.Repeat(
		"The 500s reproduce under concurrent load and trace to pool acquisition, not token validation. ", 5)
	return []spliceCase{
		{
			name: "an ordinary html comment downstream does not terminate the artifact",
			interior: "paragraph one. paragraph two. paragraph three." +
				"<!-- a normal html comment -->and the text after it",
		},
		{
			name:     "hundreds of bytes of prose survive an arrow that follows them",
			interior: prose + "<!-- end of section -->and the final sentence.",
		},
		{
			// Property 3 on StripInjections: a close tag with no open tag before it is ordinary
			// text. Its own trailing " -->" must not be mistaken for the artifact's terminator.
			name:     "an unmatched close tag downstream stays ordinary text, arrow and all",
			interior: "prose the reader wrote" + checkpoint.InjectionCloseTag + "more prose",
		},
	}
}

// TestStripInjectionsSpliceDeletesOnlyTheAssembledPrefix is the contract for splice
// neutralization: the assembled open prefix is removed and nothing else is. The proof that an
// open prefix surviving a strip pass is an artifact covers those bytes and no more — it says
// nothing about what follows them — so deleting on to the next " -->" deletes whatever the reader
// happened to write next. fromStore runs on every string entering a checkpoint from the store, so
// that is the checkpoint deleting the user's own words; Qompack.md §8.5 says tagged material is
// IGNORED as a source for the next pass, not that its neighbours are erased.
//
// Each case asserts the interior by exact content. Contains() is not enough: the shipped defect
// left the last few bytes of the interior in place while destroying everything before them, and a
// Contains assertion on those surviving bytes passes against it.
func TestStripInjectionsSpliceDeletesOnlyTheAssembledPrefix(t *testing.T) {
	for _, tc := range spliceInteriorCases() {
		t.Run(tc.name, func(t *testing.T) {
			in := spliceAround(tc.interior)
			out, n := checkpoint.StripInjectionsCount(in)
			require.Equal(t, tc.interior, out,
				"the reader's text behind a neutralized splice must survive byte-for-byte")
			require.Equal(t, 2, n, "one consumed span plus one neutralized splice artifact")
			require.Equal(t, out, checkpoint.StripInjections(in),
				"StripInjections must be StripInjectionsCount's string result")
			require.Equal(t, out, checkpoint.StripInjections(out),
				"no open prefix may survive, so a second pass must change nothing")
		})
	}
}

// TestStripInjectionsSpliceKeepsTrailingText is the permanent regression test for the splice
// defect, on the input where the artifact's own " -->" is the next arrow downstream. The result
// is asserted exactly: the assembled tag loses its prefix and the remainder of it stays as
// visible residue ("7 ver=1 -->"), followed by the user's "tail".
//
// That residue is the deliberate trade. The scan can prove those prefix bytes were assembled by a
// join and can prove nothing at all about the bytes after them, so it removes the prefix and
// stops; a reader who sees "7 ver=1 -->" in a checkpoint has lost nothing, whereas the
// alternative — deleting through the next arrow — silently loses paragraphs (see
// TestStripInjectionsSpliceDeletesOnlyTheAssembledPrefix). Idempotence is asserted alongside,
// because fromStore runs on every store read and the same text crosses more than one read cycle.
func TestStripInjectionsSpliceKeepsTrailingText(t *testing.T) {
	in := spliceRegressionInput()
	// The assembled tag is openTag(7); neutralizing it costs exactly its prefix.
	want := openTag(7)[len(openPrefix()):] + "tail"

	once := checkpoint.StripInjections(in)
	require.Equal(t, want, once,
		"only the assembled prefix may be deleted; the rest of the tag and the user's text stay")
	require.Equal(t, once, checkpoint.StripInjections(once),
		"StripInjections must be idempotent on the splice input")
}

// FuzzStripInjections requires, for arbitrary input: no panic, agreement between the wrapper and
// the counting scan, idempotence — Strip(Strip(s)) == Strip(s) — and that the scan only ever
// DELETES bytes, never inventing or reordering them. Idempotence is what lets the same
// transcript-derived text pass through more than one read path (SP-08 prompts, SP-11 digests,
// SP-10 store reads) without the second pass editing what the first produced.
//
// None of those can see over-deletion, and that is not hypothetical. Deleting MORE text is still
// idempotent, and a shorter string is still a subsequence, so a neutralizer that erased every byte
// between a spliced-together tag and the next " -->" anywhere downstream satisfied all of them
// while reducing one measured 10 KB transcript to four bytes.
//
// So the target also drives the property that does see it: the arbitrary input is used as the
// READER'S OWN PROSE, planted immediately behind a splice artifact and in front of a downstream
// " -->", and the whole of it is required back byte-for-byte. An implementation whose deletion is
// bounded by that arrow rather than by the artifact fails on the first input reaching this arm.
// The arm is entered only for inputs carrying no comment syntax of their own, because such an
// input is inert: it can neither open, close nor complete a tag, so it cannot change which spans
// the scan finds and the expected output is exactly what was planted.
func FuzzStripInjections(f *testing.F) {
	for _, tc := range shippedStripFixtures() {
		f.Add(tc.in)
	}
	f.Add("a\n" + openTag(7) + "\nX\n\nkeep me")
	f.Add("<!-- qompack:injected se")
	f.Add("<!-- qompack:session-record se")
	f.Add("a" + legacyOpenTag(1) + "x" + checkpoint.InjectionCloseTag + "b")
	// The splice-defect input is a permanent seed: 60 s of fuzzing never assembled the
	// three-part shape (partial prefix + full span + prefix completion) on its own, so the
	// one input known to have broken idempotence in shipped code is pinned here rather than
	// left to chance. The same goes for the interiors an arrow-bounded deletion destroys.
	f.Add(spliceRegressionInput())
	for _, tc := range spliceInteriorCases() {
		f.Add(spliceAround(tc.interior))
	}
	f.Fuzz(func(t *testing.T, s string) {
		once, _ := checkpoint.StripInjectionsCount(s)
		require.Equal(t, once, checkpoint.StripInjections(s),
			"StripInjections must be StripInjectionsCount's string result")
		require.Equal(t, once, checkpoint.StripInjections(once),
			"StripInjections must be idempotent: a second pass over stripped output must change nothing")
		require.True(t, isSubsequence(once, s),
			"the scan may delete bytes but must never invent or reorder them: %q -> %q", s, once)

		if strings.Contains(s, "<!--") || strings.Contains(s, "-->") {
			return // not inert: s could open, close or complete a tag and change which spans exist
		}
		// bait supplies the downstream " -->" that an over-eager neutralizer deletes back to.
		const bait = "<!-- an ordinary html comment -->and text the reader wrote"
		out, n := checkpoint.StripInjectionsCount(spliceAround(s + bait))
		require.Equal(t, s+bait, out,
			"neutralizing a splice must cost the assembled prefix and not one reader-written byte")
		require.Equal(t, 2, n, "one consumed span plus one neutralized splice artifact")
	})
}

// BenchmarkStripInjections measures one strip of a 256 KB transcript tail carrying interleaved
// injected spans — the §8.5 read-path shape fromStore sees. Budget: < 2 ms/op
// (plans/V4-SP-10-checkpointer-l4.md); the number is asserted by the bench gate, not here, so a
// loaded CI worker cannot flake a correctness run.
func BenchmarkStripInjections(b *testing.B) {
	const targetBytes = 256 * 1024
	para := "Reproduced the 500s under concurrent load and traced them to pool acquisition timeouts " +
		"rather than token validation; the working hypothesis is a row lock held across an outbound call.\n\n"
	var sb strings.Builder
	for i := 0; sb.Len() < targetBytes; i++ {
		if i%16 == 15 {
			sb.WriteString(openTag(i/16) + "\nrehydrated digest paragraph previously injected by qompack\n" +
				checkpoint.InjectionCloseTag + "\n")
		}
		sb.WriteString(para)
	}
	tail := sb.String()[:targetBytes]

	b.SetBytes(targetBytes)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if out := checkpoint.StripInjections(tail); out == tail {
			b.Fatal("the tail carries injected spans; stripping must change it")
		}
	}
}
