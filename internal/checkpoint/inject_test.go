package checkpoint_test

import (
	"fmt"
	"testing"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/stretchr/testify/require"
)

// openTag renders a real injection open tag for seq at the current schema version, the way every
// producer of an injected span does (rehydrate wraps its digest, checkpoint tags its focus
// instruction). The tests below never spell the tag out by hand, so a change to the constant's
// text is caught by the assertions rather than silently duplicated in the fixtures.
func openTag(seq int) string {
	return fmt.Sprintf(checkpoint.InjectionOpenTag, seq, checkpoint.SchemaVersion)
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
func TestInjectionTags_AreTheFrozenSpellings(t *testing.T) {
	require.Equal(t, "<!-- qompack:injected seq=%d ver=%d -->", checkpoint.InjectionOpenTag)
	require.Equal(t, "<!-- /qompack:injected -->", checkpoint.InjectionCloseTag)
	require.Equal(t, "<!-- qompack:injected seq=7 ver=1 -->", openTag(7),
		"SchemaVersion must render as the ver= value of a real open tag")
}
