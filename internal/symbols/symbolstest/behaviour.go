package symbolstest

import (
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/symbols"
	"github.com/stretchr/testify/require"
)

// This file authors the behaviour assertions of the symbolstest suite (00-ARCHITECTURE.md §5.22
// table; §15 of plans/V1-SP-01-foundation-toolchain-and-contracts.md): Enclosing resolves to the
// smallest containing span, and Extract is stable under CRLF. Both are authored now, gated behind
// the same Rule W-1 stub probe as the rest of the suite, so SP-04 inherits them rather than
// writing its own grader.

// nestedSpanSource declares a top-level function (Outer) containing a nested, local type
// declaration (Inner) — valid Go, and about as unambiguous a same-file span-nesting fixture as
// exists within §5.22b's own documented Kind vocabulary (func|type|class|const|var): a
// regex/brace-scanning heuristic that finds "func \w+" and "type \w+" starts and matches braces to
// find each one's end will find Inner's span strictly inside Outer's, regardless of nesting depth.
//
// Fixture note for SP-04: if the real Extractor's heuristic does not treat a local (in-function)
// type declaration as its own Symbol, adjust this fixture text to whatever nested construct your
// heuristic does recognize — the assertion strategy below (find two symbols where one's span
// contains the other's, probe an offset inside the smaller one, require Enclosing returns exactly
// that smaller symbol) does not need to change.
const nestedSpanSource = `package sample

func Outer() int {
	type Inner struct {
		X int
	}
	v := Inner{X: 1}
	return v.X
}
`

// innerFieldOffset is an offset inside nestedSpanSource that falls within Inner's body (the "X
// int" field line) but not outside it, so it is unambiguously nested inside both Outer's and
// Inner's spans.
var innerFieldOffset = strings.Index(nestedSpanSource, "X int")

// runEnclosingSmallestSpanCase asserts Enclosing resolves an offset inside a nested declaration to
// the smallest (innermost) containing Symbol, not an outer ancestor — the minimal-sufficient-span
// contract behind retrieval.defaultSpan = "minimal" (Qompack.md §8.7).
func runEnclosingSmallestSpanCase(t *testing.T, factory func(t *testing.T) symbols.Extractor) {
	t.Helper()
	ex := factory(t)
	src := []byte(nestedSpanSource)

	got, ok := ex.Enclosing("sample.go", src, innerFieldOffset)
	require.True(t, ok, "an offset inside a nested declaration must resolve to an enclosing symbol")
	require.Equal(t, "Inner", got.Name,
		"Enclosing must return the smallest containing span (Inner), not the outer Outer")
	require.LessOrEqual(t, got.Len, len(nestedSpanSource),
		"Inner's span must not exceed the whole fixture")

	// Sanity check on the fixture itself: Extract must actually see both declarations, and Outer's
	// span must be strictly larger than Inner's (otherwise this fixture does not exercise nesting
	// at all).
	all := ex.Extract("sample.go", src)
	var outer, inner *symbols.Symbol
	for i := range all {
		switch all[i].Name {
		case "Outer":
			outer = &all[i]
		case "Inner":
			inner = &all[i]
		}
	}
	require.NotNil(t, outer, "fixture sanity: Extract must find Outer")
	require.NotNil(t, inner, "fixture sanity: Extract must find Inner")
	require.Greater(t, outer.Len, inner.Len, "fixture sanity: Outer's span must strictly contain Inner's")
}

// crlf converts every bare LF in s to CRLF, without doubling a line ending that is already CRLF.
func crlf(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
}

// runExtractStableUnderCRLFCase asserts Extract finds the same symbols, in the same order, with
// the same Name/Kind/Line, whether the source uses LF or CRLF line endings. Offset and Len are
// deliberately not compared: a CRLF file is legitimately longer (one extra \r per line), so its
// byte offsets shift — "stable" means symbol identification does not break or miscount under
// CRLF, not that the raw byte positions are identical.
func runExtractStableUnderCRLFCase(t *testing.T, factory func(t *testing.T) symbols.Extractor) {
	t.Helper()
	ex := factory(t)

	lf := ex.Extract("sample.go", []byte(nestedSpanSource))
	cr := ex.Extract("sample.go", []byte(crlf(nestedSpanSource)))

	require.NotEmpty(t, lf, "fixture sanity: Extract must find symbols in the LF source")
	require.Len(t, cr, len(lf), "Extract must find the same number of symbols under CRLF")
	for i := range lf {
		require.Equal(t, lf[i].Name, cr[i].Name, "symbol %d name must be stable under CRLF", i)
		require.Equal(t, lf[i].Kind, cr[i].Kind, "symbol %d kind must be stable under CRLF", i)
		require.Equal(t, lf[i].Line, cr[i].Line, "symbol %d line number must be stable under CRLF", i)
	}
}
