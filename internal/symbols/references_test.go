package symbols_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/qompack/qompack/internal/symbols"
	"github.com/stretchr/testify/require"
)

// TestReferences_WordBoundaries pins the property the analyzer's cheap Δ-scorer (SP-15) depends on:
// References counts whole identifier tokens, never substrings. "parse" appearing inside "parseX",
// "Xparse" and "_parse" contributes nothing, because each of those is one token of its own.
func TestReferences_WordBoundaries(t *testing.T) {
	body := []byte("parse(parseX, Xparse);\nconst _parse = parseAgain;\n")

	got := symbols.New().References(body, []string{"parse"})
	require.Empty(t, cmp.Diff(map[string]int{"parse": 1}, got),
		"only the single standalone `parse` token counts")
}

// TestReferences_ZeroFill pins the zero-fill contract: every requested name is a key in the result,
// even one that never appears. A consumer ranging over names and reading counts[name] must not have
// to distinguish "absent" from "zero".
func TestReferences_ZeroFill(t *testing.T) {
	got := symbols.New().References([]byte("a a b_suffix"), []string{"a", "b"})
	require.Empty(t, cmp.Diff(map[string]int{"a": 2, "b": 0}, got))
}

// TestReferences_DedupAndCaps pins the three input-hygiene rules: duplicates collapse to one key,
// the empty name is dropped, and a name longer than the 256-byte cap is dropped. Dropped means
// absent from the map, not present-with-zero — a caller that asked for something unusable gets no
// answer rather than a misleading 0.
func TestReferences_DedupAndCaps(t *testing.T) {
	long := strings.Repeat("x", 300)
	body := []byte("dup dup " + long + " tail")

	got := symbols.New().References(body, []string{"dup", "dup", "", long, "tail"})
	require.Empty(t, cmp.Diff(map[string]int{"dup": 2, "tail": 1}, got))
}

// TestReferences_NoNames asserts the degenerate call shape every wave-0 composition root can make
// before it has any names to count: an empty, non-nil map, not nil.
func TestReferences_NoNames(t *testing.T) {
	got := symbols.New().References([]byte("anything at all"), nil)
	require.NotNil(t, got)
	require.Empty(t, got)
}

// TestReferences_CaseSensitive pins that Parse and parse are different names. §5.22b's consumers
// key on source identifiers, and every language symbols supports is case-sensitive.
func TestReferences_CaseSensitive(t *testing.T) {
	got := symbols.New().References([]byte("Parse parse PARSE"), []string{"parse", "Parse"})
	require.Empty(t, cmp.Diff(map[string]int{"parse": 1, "Parse": 1}, got))
}

// TestReferences_DollarIdentifiers covers the JavaScript half of the token alphabet: `$` is both a
// valid identifier start and a valid continuation, so `$fn` and `a$b` are single tokens.
func TestReferences_DollarIdentifiers(t *testing.T) {
	got := symbols.New().References([]byte("$fn($fn, a$b, $fnX)"), []string{"$fn", "a$b"})
	require.Empty(t, cmp.Diff(map[string]int{"$fn": 2, "a$b": 1}, got))
}

// TestReferences_EmptyBody asserts that zero input still zero-fills every requested name.
func TestReferences_EmptyBody(t *testing.T) {
	got := symbols.New().References(nil, []string{"a"})
	require.Empty(t, cmp.Diff(map[string]int{"a": 0}, got))
}
