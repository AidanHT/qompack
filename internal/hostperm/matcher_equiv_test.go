package hostperm

import (
	"math/rand/v2"
	"path"
	"testing"

	"github.com/stretchr/testify/require"
)

// The one-pass matcher (prefixRow, carvedMatch) replaced a table built per directory prefix to
// bound the cost of a long rule list (C1.9 review finding 2). These tests pin it to the matcher it
// replaced, kept below verbatim as the reference, over a deterministic sample of patterns and paths
// drawn from alphabets small enough that matches, near misses and every `**` position all occur.

// referenceMatchSegments is matchSegments as committed at 7f80138, before the rewrite.
func referenceMatchSegments(pat, segs []string) bool {
	n, m := len(pat), len(segs)
	w := m + 1
	dp := make([]bool, (n+1)*w)
	dp[n*w+m] = true
	for i := n - 1; i >= 0; i-- {
		last := i == n-1
		for j := m; j >= 0; j-- {
			var v bool
			switch {
			case pat[i] == "**" && last:
				v = j < m
			case pat[i] == "**":
				v = dp[(i+1)*w+j] || (j < m && dp[i*w+j+1])
			case j < m:
				ok, _ := path.Match(pat[i], segs[j])
				v = ok && dp[(i+1)*w+j+1]
			}
			dp[i*w+j] = v
		}
	}
	return dp[0]
}

// referenceCarvedMatch is carvedMatch as committed at 7f80138: one lastCarvable scan per prefix.
func referenceCarvedMatch(pats []*pattern, rel []string) (string, bool) {
	last := func(r []string) (string, bool) {
		rule, pos := "", false
		for _, p := range pats {
			if p.carvable && referenceMatchSegments(p.segs, r) {
				rule, pos = p.raw, !p.neg
			}
		}
		return rule, pos
	}
	for i := 1; i < len(rel); i++ {
		if rule, pos := last(rel[:i]); pos {
			return rule, true
		}
	}
	return last(rel)
}

var (
	patAlphabet  = []string{"a", "b", "ab", "**", "*", "a*", "?b", "[ab]", "**", "b*a"}
	pathAlphabet = []string{"a", "b", "ab", "ba", "aa", "bb"}
)

func draw(r *rand.Rand, alphabet []string, max int) []string {
	out := make([]string, r.IntN(max+1))
	for i := range out {
		out[i] = alphabet[r.IntN(len(alphabet))]
	}
	return out
}

func TestPrefixRowAgreesWithTheReferenceMatcher(t *testing.T) {
	r := rand.New(rand.NewPCG(0xC19, 0x2))
	var sc scratch
	for n := 0; n < 20000; n++ {
		pat, segs := draw(r, patAlphabet, 6), draw(r, pathAlphabet, 7)
		row := prefixRow(pat, segs, &sc)
		require.Len(t, row, len(segs)+1)
		for k := 0; k <= len(segs); k++ {
			require.Equal(t, referenceMatchSegments(pat, segs[:k]), row[k],
				"pattern %q against %q", pat, segs[:k])
		}
	}
}

func TestCarvedMatchAgreesWithTheReferenceOrdering(t *testing.T) {
	r := rand.New(rand.NewPCG(0xC19, 0x3))
	var sc scratch
	for n := 0; n < 5000; n++ {
		l := ruleList{}
		for i := r.IntN(6); i >= 0; i-- {
			segs := draw(r, patAlphabet, 4)
			if len(segs) == 0 {
				segs = []string{"a"}
			}
			l.patterns = append(l.patterns, &pattern{
				raw: "rule" + string(rune('A'+i)), segs: segs, carvable: r.IntN(5) > 0, neg: r.IntN(3) == 0,
			})
		}
		rel := draw(r, pathAlphabet, 6)
		wantRule, wantHit := referenceCarvedMatch(l.patterns, rel)
		gotRule, gotHit := l.carvedMatch(rel, &sc)
		require.Equal(t, wantHit, gotHit, "patterns %v against %q", l.patterns, rel)
		if wantHit {
			require.Equal(t, wantRule, gotRule)
		}
	}
}
