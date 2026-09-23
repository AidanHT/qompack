package canon

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// TestPlainLineRules pins the plain-line form (see THE PLAIN-LINE FORM in generic.go) two ways.
//
// Structurally: every per-line rule anchored with plainLineAnchor carries a plain form, so a rule
// added later cannot silently fall back to the slow path — and none that is not so anchored does.
//
// Semantically: on every plain line of every corpus file, and on generated plain lines drawn from
// the fragments the rules key on, the plain form finds exactly the spans the full pattern finds on
// the same line — the same offsets, groups, tokens and classes.
func TestPlainLineRules(t *testing.T) {
	var withPlain []*reRule
	for _, tb := range allTables() {
		for i := range tb.rules {
			r := &tb.rules[i]
			anchored := r.perLine && strings.HasPrefix(r.re.String(), plainLineAnchor)
			if !anchored {
				require.Nil(t, r.plain, "%s rule %d is not line-anchored and must not carry a plain form", tb.name, i)
				continue
			}
			require.NotNil(t, r.plain, "%s rule %d (%s) is line-anchored but has no plain form", tb.name, i, r.re)
			require.NotEmpty(t, r.plainLead)
			t.Logf("%s rule %d: lead %q, plain %s", tb.name, i, r.plainLead, r.plain)
			withPlain = append(withPlain, r)
		}
	}
	require.Len(t, withPlain, 10, "the ten line-anchored rules of the pids, testrunner and git tables")

	agree := func(tb require.TestingT, line []byte) {
		if !isPlainLine(line) || bytes.IndexByte(line, '\n') >= 0 {
			return
		}
		for _, r := range withPlain {
			require.Equal(tb, appendRuleSpans(nil, r, line, 7), appendPlainLineSpans(nil, r, line, 7),
				"rule %s on %q", r.re, line)
		}
	}
	for rel, body := range corpusBodies(t) {
		forEachLine(body, func(start, end int) { agree(t, body[start:end]) })
		_ = rel
	}
	rapid.Check(t, func(rt *rapid.T) {
		line := plainLineInput().Draw(rt, "line")
		agree(rt, line)
	})
}

// plainLineInput draws a single plain line out of the fragments the line-anchored rules key on,
// their near misses, and the whitespace the anchor looks past.
func plainLineInput() *rapid.Generator[[]byte] {
	frags := []string{
		"ok", "ok  ", "ok\t", "OK", "o k", "--- PASS: ", "--- FAIL: ", "--- SKIP: ", "--- PASS:", "---",
		"goroutine ", "goroutine 12 [", "Time:", "Time: ", "Finished ", "Finished `dev` in ", "commit ",
		"index ", "From ", "[", "[1234]", "]", "=", "==", "=== ", " in ", "(", ")", "0.00s", "1.5s", "12.345s",
		"s", "abc", "0123456789abcdef0123456789abcdef01234567", "1234567..89abcde", "..", " ", "\t", "  ",
		" ", " ", "x", "_", "-", ".", "TestX", "github.com/x/y", "\xff", "é",
	}
	return rapid.Custom(func(rt *rapid.T) []byte {
		parts := rapid.SliceOfN(rapid.SampledFrom(frags), 0, 10).Draw(rt, "parts")
		return []byte(strings.Join(parts, ""))
	})
}
