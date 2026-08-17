package canon

import (
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/stretchr/testify/require"
)

// The word-boundary invariant is a property of the REGISTRY, not of any one canonicalizer: what it
// forbids is one rule's rewrite handing a different rule a boundary the input did not have. That
// is why these tables live here rather than beside the generic rules they mostly exercise — they
// need all fourteen canonicalizers registered, including the per-tool ones, and a Bash tool name
// to reach them.

// boundaryCase is one row of the two word-boundary tables. It carries no Delta expectation: what
// these rows are about is which rules FIRE, on which pass.
type boundaryCase struct {
	name string
	in   string
	want string
}

// runBoundaryCases drives the full Default registry over each row twice.
//
// The word-boundary class is cross-canonicalizer by nature — one rule's rewrite enabling a
// DIFFERENT rule on the next pass — so it can only be pinned through the registry, never through
// one canonicalizer's own Canonicalize. Both halves of the assertion matter: the exact bytes,
// because "pass 2 == pass 1" alone would also be satisfied by a rule that stopped working
// altogether, and the pass-2 equality, because that is the property itself.
func runBoundaryCases(t *testing.T, cases []boundaryCase) {
	t.Helper()
	r := Default(config.Defaults().Store.Canonicalize)
	o := OptionsFrom(config.Defaults().Store.Canonicalize, true)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			first, err := r.Run("Bash", "", []byte(tc.in), o)
			require.NoError(t, err)
			require.Equal(t, tc.want, string(first.Canonical))

			second, err := r.Run("Bash", "", first.Canonical, o)
			require.NoError(t, err)
			require.Equal(t, string(first.Canonical), string(second.Canonical),
				"pass 2 differs from pass 1: a rewrite created a word boundary the input did not have")

			restored, err := Restore(first.Canonical, first.Deltas)
			require.NoError(t, err)
			require.Equal(t, tc.in, string(restored))
		})
	}
}

// TestWordBoundary_TokensNeverCreateOne pins the invariant documented above reRule in generic.go:
// a replacement token must never hand a later pass a word boundary the input did not have.
//
// Every row butts a rule whose span ends in a word byte straight against a rule that needs a
// leading word boundary, with NOTHING between them. Before the span-edge anchors went in, pass 1
// replaced the first span and the token's '>' — a non-word byte — then satisfied the second rule's
// \b, so pass 2 found a match pass 1 had correctly refused. Each row is paired with the same input
// plus one separating space, which is the control: it proves the anchor rejected the abutting case
// without also breaking the ordinary one.
func TestWordBoundary_TokensNeverCreateOne(t *testing.T) {
	runBoundaryCases(t, []boundaryCase{
		{
			// The fuzzer's own counter-example, from
			// testdata/fuzz/FuzzCanonicalize/4a9f1c14899e6f0d. Without the ISO rule's trailing \b
			// this was "<ts>pid 0000" on pass 1 and "<ts>pid <n>" on pass 2.
			name: "iso timestamp abutting pid",
			in:   "0000-00-00 00:00:00Zpid 0000",
			want: "0000-00-00 00:00:00Zpid 0000",
		},
		{
			name: "iso timestamp separated from pid still fires",
			in:   "0000-00-00 00:00:00Z pid 0000",
			want: "<ts> pid <n>",
		},
		{
			// The RFC-1123 rule declines because 'T' and 'p' are both word bytes; the bare-clock
			// rule inside it still fires, because a space sits on each side of "03:04:05".
			name: "rfc-1123 date abutting pid",
			in:   "Mon, 02 Jan 2024 03:04:05 GMTpid 4242",
			want: "Mon, 02 Jan 2024 <ts> GMTpid 4242",
		},
		{
			name: "rfc-1123 date separated from pid still fires",
			in:   "Mon, 02 Jan 2024 03:04:05 GMT pid 4242",
			want: "<ts> pid <n>",
		},
		{
			// Both the syslog rule and the clock rule inside it decline here.
			name: "syslog timestamp abutting pid",
			in:   "Jan  2 15:04:05pid 4242",
			want: "Jan  2 15:04:05pid 4242",
		},
		{
			name: "syslog timestamp separated from pid still fires",
			in:   "Jan  2 15:04:05 pid 4242",
			want: "<ts> pid <n>",
		},
		{
			// testrunner's cargo duration and durations' own rules all end in 's', a word byte.
			name: "duration abutting pid",
			in:   "finished in 1.23spid 99999",
			want: "finished in 1.23spid 99999",
		},
		{
			name: "duration separated from pid still fires",
			in:   "finished in 1.23s pid 99999",
			want: "finished in <d> pid <n>",
		},
		{
			// git's index rule: the second hash ends in a hex digit with nothing after it in the
			// pattern, so it is the one that needed an explicit trailing \b.
			name: "git index hash abutting pid",
			in:   "index 1234567..89abcdepid 4242",
			want: "index 1234567..89abcdepid 4242",
		},
		{
			name: "git index hash separated from pid still fires",
			in:   "index 1234567..89abcde pid 4242",
			want: "index <sha>..<sha> pid <n>",
		},
	})
}

// TestWordBoundary_EscapeDeletionNeverCreatesOne pins the same failure reached the other way: not
// by a token, but by ansiCanon DELETING an SGR sequence and joining its neighbours.
//
// An SGR sequence ends with a letter, so " \x1b[32mpid=41235" has no boundary in front of "pid" —
// 'm' and 'p' are both word bytes — and `\bpid` correctly does not fire. Deleting the escape in the
// same pass leaves " pid=41235", where the boundary DOES exist, so pass 2 fired where pass 1 had
// not. wordEdge is the fix: the leading assertion is satisfied either by a real boundary or by the
// escape run itself, so both passes agree. This mechanism is far easier to hit than the
// token-mediated one, because every colour-capable tool produces it.
func TestWordBoundary_EscapeDeletionNeverCreatesOne(t *testing.T) {
	runBoundaryCases(t, []boundaryCase{
		{name: "colourized pid", in: " \x1b[32mpid=41235 ", want: " pid=<n>"},
		{name: "colourized epoch", in: " \x1b[32m1700000000000 ", want: " <ts>"},
		{name: "colourized hex address", in: " \x1b[32m0xc000123456 ", want: " <addr>"},
		{name: "colourized iso timestamp", in: " \x1b[1m2024-01-02T03:04:05Z ", want: " <ts>"},
		{name: "colourized duration", in: " \x1b[33m1m30.5s ", want: " <d>"},
		{
			// Two SGR sequences back to back, which is what most tools actually emit: wordEdge's
			// escape branch is a '+' for exactly this.
			name: "two escapes before the value", in: " \x1b[1m\x1b[32mpid=41235 ", want: " pid=<n>",
		},
		{
			// A WORD byte before the escape. Reading the escape as invisible, the rule "should" not
			// fire here — after deletion "a1700000000000" has no leading boundary. wordEdge is
			// deliberately more eager than that: its escape branch does not care what precedes the
			// escape. That is safe, and the row exists to pin it, because what idempotence needs is
			// only that the two passes AGREE — and a colourized value is volatile either way.
			name: "word byte before the escape", in: "a\x1b[0m1700000000000 ", want: "a<ts>",
		},
	})
}
