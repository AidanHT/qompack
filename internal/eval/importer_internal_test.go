package eval

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// markerToken is the shape of every marker redactRules leaves behind: angle brackets around an
// upper-case name. The brackets are not decoration — they are the one character pair every rule's
// text-consuming character class excludes, which is what stops one rule accepting another rule's
// output as input on a second pass.
var markerToken = regexp.MustCompile(`<[A-Z]+>`)

// TestRedactRules_SpanningClassesRejectMarkerBrackets is the general form of a defect class that
// has now bitten redaction twice: a rule whose character class admits < and > can swallow a marker
// some other rule already wrote, so Redact(Redact(x)) != Redact(x).
//
// The first instance was <HOME> re-consumed as a Windows user name; the second was <EMAIL> re-
// consumed as a URL user name (seed corpus entries cf687edf6ceb59ac and 4ca9f371d73b9950). Both
// were found by fuzzing, which is the wrong place to learn it a third time — a repeated negated
// character class is exactly "here a rule swallows a run of arbitrary user text", so requiring
// every one of them to exclude the marker brackets turns the convention into something a new rule
// cannot forget.
//
// A negated class that matches a single character is exempt, because one character cannot hold a
// marker. That exemption is load-bearing rather than a loophole: the e-mail rule's one-character
// left guard has to admit "<" or "<alice@example.com>" — the ordinary angle-address spelling —
// would stop being redacted at all. It excludes ">" for the separate reason spelled out beside the
// rule table.
func TestRedactRules_SpanningClassesRejectMarkerBrackets(t *testing.T) {
	checked := 0
	for _, rule := range redactRules {
		pattern := rule.re.String()
		for _, class := range spanningNegatedClasses(pattern) {
			checked++
			require.Contains(t, class, "<",
				"rule %q swallows a run of user text with [^%s], which admits the < of a marker "+
					"another rule may already have written", pattern, class)
			require.Contains(t, class, ">",
				"rule %q swallows a run of user text with [^%s], which admits the > of a marker "+
					"another rule may already have written", pattern, class)
		}
	}
	require.GreaterOrEqual(t, checked, 4,
		"the scanner found fewer classes than the table is known to contain — the two <HOME> "+
			"rules and both halves of the URL credential — so this guard is asserting nothing")
}

// TestRedactRules_ReplacementsAreBracketedMarkers keeps the assumption the guard above rests on
// true. Excluding < and > only protects a marker while every marker is spelled with them, so a
// replacement that introduced a bare word, or a stray bracket around something else, would leave
// the convention checking nothing.
func TestRedactRules_ReplacementsAreBracketedMarkers(t *testing.T) {
	for _, rule := range redactRules {
		markers := markerToken.FindAllString(rule.with, -1)
		require.NotEmpty(t, markers,
			"replacement %q writes no <MARKER>, so nothing identifies it as already-redacted",
			rule.with)
		require.Len(t, markers, strings.Count(rule.with, "<"),
			"replacement %q carries a < that does not open a marker", rule.with)
		require.Len(t, markers, strings.Count(rule.with, ">"),
			"replacement %q carries a > that does not close a marker", rule.with)
	}
}

// spanningNegatedClasses returns the body of every [^…] character class in a regular expression's
// source that carries a quantifier able to match more than one character, which is precisely the
// set of places a rule can swallow a whole marker.
//
// It honours backslash escapes, so a class holding \\ or \] is not cut short at the wrong byte and
// the guard above cannot be fooled into inspecting a truncated class.
func spanningNegatedClasses(pattern string) []string {
	var out []string
	for i := 0; i+1 < len(pattern); i++ {
		if pattern[i] == '\\' {
			i++
			continue
		}
		if pattern[i] != '[' || pattern[i+1] != '^' {
			continue
		}
		j := i + 2
		for ; j < len(pattern); j++ {
			if pattern[j] == '\\' {
				j++
				continue
			}
			if pattern[j] == ']' {
				break
			}
		}
		if j > len(pattern) {
			j = len(pattern)
		}
		if j+1 < len(pattern) && strings.ContainsRune("+*{", rune(pattern[j+1])) {
			out = append(out, pattern[i+2:j])
		}
		i = j
	}
	return out
}
