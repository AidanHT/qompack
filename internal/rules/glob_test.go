package rules_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/rules"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// The cases below pin the grammar 00-ARCHITECTURE.md §5.15 gives `paths:` frontmatter globs.
// path.Match cannot express `**` at all, so every one of these is a behaviour the in-repo matcher
// owns outright rather than a thin wrapper's passthrough.

// TestMatch_Star_WithinSegment pins the single star as a within-segment wildcard: it fills any
// run of characters inside one path segment and never reaches across a separator.
func TestMatch_Star_WithinSegment(t *testing.T) {
	t.Parallel()
	require.True(t, rules.Match("src/*.ts", "src/a.ts"))
	require.False(t, rules.Match("src/*.ts", "src/x/a.ts"))
}

// TestMatch_DoubleStar_ZeroSegments pins the zero-segment half of `**`: `src/**/a.ts` has to
// match `src/a.ts`, which is the case a naive "** means at least one directory" reading gets
// wrong and which every real rule file relies on.
func TestMatch_DoubleStar_ZeroSegments(t *testing.T) {
	t.Parallel()
	require.True(t, rules.Match("src/**/a.ts", "src/a.ts"))
}

// TestMatch_DoubleStar_ManySegments pins the other half: `**` spans any number of segments.
func TestMatch_DoubleStar_ManySegments(t *testing.T) {
	t.Parallel()
	require.True(t, rules.Match("src/**/a.ts", "src/x/y/z/a.ts"))
}

// TestMatch_DoubleStar_Trailing_MatchesDirItself pins the trailing-`/**` special case: a rule
// scoped to `src/api/**` covers the directory node itself, not only the files below it, because
// pointers can name a directory.
func TestMatch_DoubleStar_Trailing_MatchesDirItself(t *testing.T) {
	t.Parallel()
	require.True(t, rules.Match("src/api/**", "src/api"))
	require.True(t, rules.Match("src/api/**", "src/api/routes.ts"))
	require.False(t, rules.Match("src/api/**", "src/apix"))
}

// TestMatch_BareGlob_ImpliesAnyDepth pins the implicit `**/` prefix a separator-free pattern
// carries: `*.ts` in a rule file means "any .ts file anywhere", not "any .ts file at the project
// root".
func TestMatch_BareGlob_ImpliesAnyDepth(t *testing.T) {
	t.Parallel()
	require.True(t, rules.Match("*.ts", "src/api/routes.ts"))
	require.True(t, rules.Match("*.ts", "routes.ts"))
	require.False(t, rules.Match("*.ts", "src/api/routes.go"))
}

// TestMatch_CharClass pins that character classes are honoured, delegated to path.Match inside a
// single segment, and that a malformed class is a non-match rather than a panic.
func TestMatch_CharClass(t *testing.T) {
	t.Parallel()
	require.True(t, rules.Match("src/[a-c].ts", "src/b.ts"))
	require.False(t, rules.Match("src/[a-c].ts", "src/d.ts"))
	require.False(t, rules.Match("src/[a-.ts", "src/b.ts"), "a malformed class must not match")
}

// TestMatch_QuestionMark pins `?` as exactly one character inside one segment.
func TestMatch_QuestionMark(t *testing.T) {
	t.Parallel()
	require.True(t, rules.Match("src/?.ts", "src/a.ts"))
	require.False(t, rules.Match("src/?.ts", "src/ab.ts"))
	require.False(t, rules.Match("src/?.ts", "src/.ts"))
}

// TestMatch_NoMatchAcrossSegmentWithSingleStar pins the one distinction the grammar exists to
// draw: `*` is a within-segment wildcard and `**` is the cross-segment one. A bare `*` therefore
// stays single-segment rather than being lifted to any depth, which would make it a synonym for
// `**` and erase the distinction entirely.
func TestMatch_NoMatchAcrossSegmentWithSingleStar(t *testing.T) {
	t.Parallel()
	require.False(t, rules.Match("*", "a/b"))
	require.True(t, rules.Match("*", "a"))
	require.True(t, rules.Match("**", "a/b"), "the cross-segment wildcard still matches any depth")
}

// TestMatch_RejectsPathologicalPattern pins the MaxPatternSegments guard: a pattern deeper than
// the cap is rejected outright rather than driving the segment matcher's backtracking, so a rule
// file cannot turn a hook invocation into a stall.
func TestMatch_RejectsPathologicalPattern(t *testing.T) {
	t.Parallel()
	pattern := strings.Repeat("**/", rules.MaxPatternSegments+8) + "a.ts"
	key := strings.Repeat("a/", rules.MaxPatternSegments+8) + "a.ts"

	start := time.Now()
	got := rules.Match(pattern, key)
	elapsed := time.Since(start)

	require.False(t, got, "a pattern past MaxPatternSegments must be rejected, not matched")
	require.Less(t, elapsed, time.Millisecond, "the guard must fire before any backtracking")
}

// globAlphabet is the metacharacter-dense alphabet the property below draws from: every rune that
// steers the matcher, and one ordinary letter to separate them with.
const globAlphabet = "a/.*?[]"

// propMaxTokenLen and propPairsPerCheck size the property. rapid's default of 100 checks times
// 100 pairs per check is the 10 000 cases the matcher is specified to survive.
const (
	propMaxTokenLen   = 16
	propPairsPerCheck = 100
)

// TestPropMatch_NeverPanics is the PropMatch_NeverPanics property (named with the Test prefix
// because Go's test runner only invokes TestXxx): over 10 000 metacharacter-dense pairs, Match
// neither panics nor disagrees with itself. Determinism is asserted alongside panic-freedom
// because the matcher recurses over `**` and a memoised or short-circuited variant that returned
// a different answer on the second call would be indistinguishable from a correct one in every
// example-based test above.
func TestPropMatch_NeverPanics(t *testing.T) {
	t.Parallel()
	gen := rapid.StringOfN(rapid.SampledFrom([]rune(globAlphabet)), 0, propMaxTokenLen, -1)

	rapid.Check(t, func(rt *rapid.T) {
		for i := range propPairsPerCheck {
			pattern := gen.Draw(rt, fmt.Sprintf("pattern%d", i))
			key := gen.Draw(rt, fmt.Sprintf("key%d", i))
			first := rules.Match(pattern, key)
			second := rules.Match(pattern, key)
			require.Equal(rt, first, second, "Match(%q, %q) is not deterministic", pattern, key)
		}
	})
}
