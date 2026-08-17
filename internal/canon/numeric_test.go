package canon

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// numeric.go replaced seven regexes with one byte scanner, and the only thing that makes that safe
// is keeping the seven regexes. They are compiled here, verbatim, and every assertion below is the
// same one: the scanner and the patterns must report the IDENTICAL multiset of Match values.
//
// Identical, not merely equivalent. testdata/golden/canon/** froze the canonical form of every
// captured corpus file under the old rules, so a scanner that is "better" — one that declines a
// match Go's leftmost-first semantics would have taken, or takes one it would have declined —
// rewrites twenty-six goldens and changes what the content-addressed store dedups against. The
// patterns are the specification; this file is what holds the rewrite to it.
//
// numericReferenceRules is the seven, in the order the two retired tables listed them.
var numericReferenceRules = []struct {
	class Class
	token string
	re    *regexp.Regexp
}{
	{ClassTimestamps, tokenTimestamp, regexp.MustCompile(wordEdge + `(\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:?\d{2})?)\b`)},
	{ClassTimestamps, tokenTimestamp, regexp.MustCompile(wordEdge + `(\d{10,13})\b`)},
	{ClassTimestamps, tokenTimestamp, regexp.MustCompile(wordEdge + `(\d{2}:\d{2}:\d{2}(?:\.\d{1,6})?)\b`)},
	{ClassDurations, tokenDuration, regexp.MustCompile(wordEdge + `(\d+h\d+m\d+(?:\.\d+)?s)\b`)},
	{ClassDurations, tokenDuration, regexp.MustCompile(wordEdge + `(\d+m\d+(?:\.\d+)?s)\b`)},
	{ClassDurations, tokenDuration, regexp.MustCompile(wordEdge + `(\d+(?:\.\d+)?\s?(?:ns|µs|us|ms|s|m|h))\b`)},
	{ClassDurations, tokenDuration, regexp.MustCompile(wordEdge + `in (\d+(?:\.\d+)?\s?(?:ms|s))\b`)},
}

// referenceNumericMatches is what numericMatches has to reproduce: each rule of the class scanned
// independently over the whole buffer with FindAllSubmatchIndex, replacing its first capture group,
// which is exactly what appendRuleSpans did for these rules before they moved.
func referenceNumericMatches(in []byte, class Class) []Match {
	var out []Match
	for _, r := range numericReferenceRules {
		if r.class != class {
			continue
		}
		for _, loc := range r.re.FindAllSubmatchIndex(in, -1) {
			lo, hi := loc[2], loc[3]
			if lo < 0 || hi <= lo {
				continue
			}
			out = append(out, Match{Offset: lo, Len: hi - lo, Token: []byte(r.token), Class: r.class})
		}
	}
	return out
}

// sortedNumericMatches orders matches so the two sides can be compared as MULTISETS.
//
// Order is deliberately not part of the contract. Matcher documents that spans may come back in
// any order, Registry.Run sorts them itself in sortCandidates, and the scanner emits in a
// different order from the reference by construction — the reference finishes rule one over the
// whole buffer before it starts rule two, while the scanner interleaves all of them in one pass.
// What must agree is which spans exist and how many times each does.
func sortedNumericMatches(ms []Match) []Match {
	out := append([]Match(nil), ms...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Offset != out[j].Offset {
			return out[i].Offset < out[j].Offset
		}
		return out[i].Len < out[j].Len
	})
	return out
}

// requireNumericAgreement asserts both classes agree with their reference on in.
func requireNumericAgreement(t require.TestingT, in []byte, where string) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	for _, class := range []Class{ClassTimestamps, ClassDurations} {
		require.Equal(t,
			sortedNumericMatches(referenceNumericMatches(in, class)),
			sortedNumericMatches(numericMatches(nil, in, class)),
			"%s: %s disagrees with its reference on %q", where, class, in)
	}
}

// numericSpans renders the spans of one class as the text they cover, in offset order, which is
// what the table below pins.
func numericSpans(in []byte, class Class) []string {
	ms := sortedNumericMatches(numericMatches(nil, in, class))
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, string(in[m.Offset:m.End()]))
	}
	return out
}

// numericCase is one row of the table below: an input, and the text of every span each class
// reports over it, in offset order.
type numericCase struct {
	name   string
	in     string
	wantTS []string
	wantD  []string
}

// TestNumericMatches_Table pins the shapes, the edges and the four places Go's regexp semantics
// are visible in the answer. Every row is ALSO checked against the reference patterns, so a row
// whose expectation is wrong fails twice rather than silently redefining the contract.
func TestNumericMatches_Table(t *testing.T) {
	for _, tc := range []numericCase{
		// ---- the seven shapes, one row each ----------------------------------------------
		{
			// The ISO rule and the clock rule overlap by construction: the date-time CONTAINS
			// "10:32:07". Both are reported; acceptCandidates keeps the earlier, longer one.
			//
			// The overlap needs the SQL-style space separator to be visible. With the RFC-3339
			// 'T' the clock rule's leading \b fails — 'T' is a word byte and so is the '1' after
			// it — which is why "2024-01-15T10:32:07Zx" below reports nothing at all rather than
			// falling back to the clock.
			name:   "iso date-time and the clock inside it",
			in:     "2024-01-15 10:32:07 ready",
			wantTS: []string{"2024-01-15 10:32:07", "10:32:07"},
		},
		{name: "epoch millis", in: "[1700000000000] start", wantTS: []string{"1700000000000"}},
		{name: "bare clock", in: "12:34:56 ready", wantTS: []string{"12:34:56"}},
		{name: "composite hours", in: "took 1h2m3s", wantD: []string{"1h2m3s"}},
		{
			// The simple rule cannot also fire at offset 0 — its \b fails against the digit
			// after "1m" — but it does fire on the "5s" the fraction ends with.
			name: "composite minutes", in: "took 1m30.5s", wantD: []string{"1m30.5s", "5s"},
		},
		{name: "magnitude and unit", in: "took 250ms", wantD: []string{"250ms"}},
		{
			// The phrase rule and the simple rule report the IDENTICAL span, so the multiset
			// holds it twice. Overlap resolution keeps one.
			name: "in phrase", in: "finished in 250ms", wantD: []string{"250ms", "250ms"},
		},

		// ---- wordEdge -------------------------------------------------------------------
		{
			// wordEdge's escape branch. The escape is consumed by the MATCH and never by the
			// SPAN, so ansiCanon still owns those five bytes. The composite-minutes rule also
			// fires, INSIDE the CSI sequence, because '[' is not a word byte and 'm' is a unit.
			name: "escape sequence before the value", in: "\x1b[32m1.5s",
			wantD: []string{"32m1.5s", "1.5s"},
		},
		{
			name: "word byte before the escape still matches", in: "a\x1b[0m1700000000000 ",
			wantTS: []string{"1700000000000"},
		},
		{name: "underscore is a word byte", in: "_5s 5s_ -5s", wantD: []string{"5s"}},
		{
			// Neither the ISO rule (its  fails against the leading '2') nor the clock rule
			// (its  fails against the 'T') nor the epoch rule (five digits) fires here.
			name: "leading digit blocks the iso rule", in: "20240-01-02T03:04:05Z ",
		},

		// ---- the trailing \b -------------------------------------------------------------
		{name: "duration followed by a word byte", in: "1.5sx"},
		{name: "iso followed by a word byte", in: "2024-01-15T10:32:07Zx"},
		{name: "epoch followed by a word byte", in: "1700000000000pid"},
		{
			// "ms" fails its \b, and so does the "m" the alternation falls back to.
			name: "unit alternation exhausts itself", in: "12msx 5mss",
		},

		// ---- adjacency -------------------------------------------------------------------
		{
			// Two epochs separated by one space. Both go in one pass, which is the property the
			// bounded spelling of that rule exists for.
			name: "adjacent epochs", in: "1700000000000 1700000000001",
			wantTS: []string{"1700000000000", "1700000000001"},
		},
		{
			// "1.5s2.5s" yields only the "5s" at offset 6: offset 0 fails its \b against the
			// '2', and offset 4 is not admissible at all because 's' is a word byte.
			name: "abutting durations", in: "1.5s2.5s 1.5s 2.5s",
			wantD: []string{"5s", "1.5s", "2.5s"},
		},

		// ---- units ------------------------------------------------------------------------
		{name: "micro is two bytes", in: "5µs 5us 5ns", wantD: []string{"5µs", "5us", "5ns"}},
		{name: "optional space before the unit", in: "took 5 ms", wantD: []string{"5 ms"}},
		{name: "only one space", in: "0.5 h 0.5  h", wantD: []string{"0.5 h"}},
		{
			// LATENT: Go's `\s` includes '\n', so a magnitude at the end of one line pairs with
			// a unit at the start of the next. It excludes the vertical tab, so the second half
			// of this row does not match. Both are reproduced deliberately.
			name: "perl space spans a newline but not a vertical tab", in: "in 5\nms 5\vms",
			wantD: []string{"5\nms", "5\nms"},
		},

		// ---- the \d{10,13} boundary --------------------------------------------------------
		{name: "nine digits are not an epoch", in: "id 123456789 x"},
		{name: "ten digits are", in: "id 1234567890 x", wantTS: []string{"1234567890"}},
		{name: "thirteen digits are", in: "id 1234567890123 x", wantTS: []string{"1234567890123"}},
		{
			// Fourteen: every capping from 13 down to 10 ends on a digit, so the rule declines
			// rather than truncating.
			name: "fourteen digits are not", in: "id 12345678901234 x",
		},

		// ---- fraction caps -----------------------------------------------------------------
		{
			name: "six fraction digits are a clock", in: "12:34:56.123456 ",
			wantTS: []string{"12:34:56.123456"},
		},
		{
			// Seven: the cap is six, all six cappings end on a digit, and the fraction is
			// dropped whole rather than truncated to six.
			name: "seven fraction digits fall back to the bare clock", in: "12:34:56.1234567 ",
			wantTS: []string{"12:34:56"},
		},
		{
			name:   "ten fraction digits fall back to the bare iso form",
			in:     "2024-01-02T03:04:05.1234567890 ",
			wantTS: []string{"2024-01-02T03:04:05", "1234567890"},
		},

		// ---- the zone ----------------------------------------------------------------------
		{
			name: "numeric zone with and without its colon",
			in:   "2024-01-02T03:04:05+0200 2024-01-02 03:04:05+02:00 ",
			wantTS: []string{
				"2024-01-02T03:04:05+0200",
				"2024-01-02 03:04:05+02:00", "03:04:05",
			},
		},
		{
			// "+02:000" satisfies neither reading of the zone, so the zone is dropped and the
			// bare 19-byte form matches — '+' is not a word byte, so its \b holds.
			name:   "over-long numeric zone drops the zone",
			in:     "2024-01-02T03:04:05+02:000 ",
			wantTS: []string{"2024-01-02T03:04:05"},
		},

		// ---- leftmost is measured on the WHOLE match ---------------------------------------
		{
			// The clock inside the CSI parameter bytes has a word boundary on each side, but
			// the escape branch lets the whole match start at offset 0 and reach the SECOND
			// clock — which is leftmost-er, so the first one is never reported.
			name: "escape run outruns an earlier clock", in: "\x1b[12:34:56~01:02:03 ",
			wantTS: []string{"01:02:03"},
		},
		{
			// The same, through an OSC body, and with the escAny alternation deciding it: the
			// OSC reading reaches the second epoch and wins over the two-byte reading that
			// would have reached the first.
			name: "osc body outruns an earlier epoch", in: "\x1b]1234567890\x071234567890 ",
			wantTS: []string{"1234567890"},
		},
		{
			// And the fallback in the other direction: the OSC reading swallows the duration,
			// the two-byte reading hands it back.
			name: "osc reading fails and the two-byte reading succeeds", in: "\x1b]5s\x07",
			wantD: []string{"5s"},
		},
		{
			// Both readings of the run reach a position that does not match, and no later
			// position is admissible — '5' at offset 6 is preceded by the word byte 'x'.
			name: "every reading of the run fails", in: "\x1b]\x1b\\9x5s",
		},
		{name: "bare escapes chain", in: "\x1b\x1b5s", wantD: []string{"5s"}},

		// ---- degenerate ----------------------------------------------------------------------
		{name: "empty", in: ""},
		{name: "no digits at all", in: "nothing of interest here"},
		{name: "a lone escape", in: "\x1b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := []byte(tc.in)
			requireNumericAgreement(t, in, tc.name)
			require.Equal(t, tc.wantTS, emptyToNil(numericSpans(in, ClassTimestamps)))
			require.Equal(t, tc.wantD, emptyToNil(numericSpans(in, ClassDurations)))
		})
	}
}

// emptyToNil lets a row with no expectation leave both want fields out entirely.
func emptyToNil(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

// TestNumericMatches_PathologicalEscapeRuns is what numEscMemoAfter exists for, and the only place
// it is exercised: no captured tool output, and nothing rapid or the fuzzer is likely to build,
// contains an escape run thousands of sequences long.
//
// Both shapes make the walk explore everything, because the tail matches no rule. The first is a
// CHAIN — every ESC has exactly one escape reading, so a walk rooted at each of them would rescan
// the whole tail and the pass would be quadratic. The second is a DAG: "\x1b]\x1b\\" is reachable
// as one OSC sequence terminated by ST and as two two-byte escapes, so the number of distinct
// paths through it doubles every four bytes and an unmemoized walk would not finish this century.
// If the memo is ever removed, this test does not fail — it hangs, which is the same message
// delivered less politely.
func TestNumericMatches_PathologicalEscapeRuns(t *testing.T) {
	for _, tc := range []struct {
		name    string
		run     string
		repeats int
	}{
		{"a chain of two-byte escapes", "\x1bA", 4000},
		{"a dag of osc and two-byte readings", "\x1b]\x1b\\", 1000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The 'x' tail is a word byte and matches no unit, so every reading of every run
			// fails and the walk runs to exhaustion.
			requireNumericAgreement(t, []byte(strings.Repeat(tc.run, tc.repeats)+"x"), tc.name)
		})
	}
}

// numericAlphabet is what the property test draws from: the digits and separators the seven shapes
// are built out of, every unit letter, both ESC introducers with their terminators, and the two
// bytes of 'µ'.
//
// A uniform byte generator would essentially never produce ten consecutive digits or an escape
// sequence followed by a magnitude, so it would confirm only that absent shapes stay absent. This
// alphabet makes near-misses — a fraction one digit too long, a unit whose \b fails, a clock inside
// a CSI parameter run — the COMMON case rather than the unreachable one.
var numericAlphabet = []byte("0123456789.:+-T Zshmnu\t\n\v\r\x1b[]\\\x07~;xi\xc2\xb5_")

// numericInput draws a buffer out of numericAlphabet, plus the uniform generator for the bytes no
// rule expects.
func numericInput() *rapid.Generator[[]byte] {
	// numericMaxPropBytes is small on purpose: every property here is about the SHAPE of a
	// match, not about volume, and a short input shrinks to a readable counterexample.
	const numericMaxPropBytes = 64
	return rapid.OneOf(
		rapid.SliceOfN(rapid.SampledFrom(numericAlphabet), 0, numericMaxPropBytes),
		rapid.SliceOfN(rapid.Byte(), 0, numericMaxPropBytes),
	)
}

// TestNumericMatchesAgreeOnGeneratedInput is the property version of the agreement, which is where
// the shapes the corpus does not contain get reached.
func TestNumericMatchesAgreeOnGeneratedInput(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		requireNumericAgreement(rt, numericInput().Draw(rt, "in"), "generated")
	})
}
