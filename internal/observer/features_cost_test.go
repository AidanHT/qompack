package observer

// SP08-D1 (plans/V2-SP-08-carried-defects.md), V6 close-out. The lexical-cohesion feature ran the
// token regexp [A-Za-z_][A-Za-z0-9_]* through FindAll on BOTH feature windows on every PostToolUse:
// 2 × cohesionTokenCap = 8 000 regexp matches per event, each one a []byte slice header, a string
// conversion and a lowercase copy. On BenchmarkOnToolUse_TestOutput256KB that was 13 % of OnToolUse,
// more than any other stage outside store.PutBytes.
//
// These tests pin the cost model (allocations, which co-load cannot inflate — ADR 0010) and, more
// importantly, what the scanner that replaced the regexp may not change: the token sequence, and so
// every term-frequency map and every cosine, is the regexp's exactly. The regexp itself is kept
// here as the oracle.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// cohesionTokenOracle is the shape the lexical-cohesion feature has always scored, compiled from
// the pattern it was specified with.
var cohesionTokenOracle = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

// oracleTermFrequencies is termFrequencies as it was written against the regexp.
func oracleTermFrequencies(b []byte) map[string]float64 {
	if len(b) == 0 {
		return nil
	}
	matches := cohesionTokenOracle.FindAll(b, cohesionTokenCap)
	if len(matches) == 0 {
		return nil
	}
	tf := make(map[string]float64, len(matches))
	for _, m := range matches {
		tf[strings.ToLower(string(m))]++
	}
	return tf
}

// corpusTexts is every tool-output corpus file, the realistic half of the equivalence check.
func corpusTexts(t *testing.T) map[string][]byte {
	t.Helper()
	dir := filepath.Join("..", "..", "testdata", "corpora", "toolout")
	out := map[string][]byte{}
	require.NoError(t, filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(p, ".meta.json") {
			return err
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		out[p] = b
		return nil
	}))
	require.NotEmpty(t, out)
	return out
}

// TestTermFrequenciesMatchTheTokenRegexp: over every corpus file, over the capped prefix of a
// 256 KB test-runner payload, and over arbitrary bytes including invalid UTF-8, the term
// frequencies are exactly the regexp's.
func TestTermFrequenciesMatchTheTokenRegexp(t *testing.T) {
	for name, b := range corpusTexts(t) {
		require.Equal(t, oracleTermFrequencies(b), termFrequencies(b), name)
	}
	edge := []string{
		"", "9", "_", "__a1", "9abc", "a9b", "ABC def_GHI", "x\xc3a\xffb\xe2\x82\xacc", "é_a", "a-b.c/d",
		"\x00a\x80b", strings.Repeat("tok ", cohesionTokenCap+10), strings.Repeat("Z", 1<<10),
	}
	for _, s := range edge {
		require.Equal(t, oracleTermFrequencies([]byte(s)), termFrequencies([]byte(s)), "%q", s)
	}
	rapid.Check(t, func(rt *rapid.T) {
		b := rapid.SliceOf(rapid.Byte()).Draw(rt, "b")
		require.Equal(rt, oracleTermFrequencies(b), termFrequencies(b))
	})
	rapid.Check(t, func(rt *rapid.T) {
		// Word-heavy input, so the token cap and the case folding are both exercised.
		words := rapid.SliceOf(rapid.StringMatching(`[A-Za-z_0-9]{0,6}[ .\n\xc3\x80-]{0,2}`)).Draw(rt, "w")
		b := []byte(strings.Join(words, ""))
		require.Equal(rt, oracleTermFrequencies(b), termFrequencies(b))
	})
}

// TestWindowTermFrequenciesMatchTheConcatenation: scoring a window event by event is the same as
// scoring concatText of it, which is what the feature was specified over — including the token cap
// landing inside a later event.
func TestWindowTermFrequenciesMatchTheConcatenation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(0, 2*featureWindow).Draw(rt, "n")
		window := make([]recentEvent, n)
		for i := range window {
			words := rapid.SliceOf(rapid.StringMatching(`[A-Za-z_0-9]{0,5}[ .\xc3]{0,1}`)).Draw(rt, "w")
			window[i].Text = []byte(strings.Join(words, ""))
			if rapid.Bool().Draw(rt, "long") {
				window[i].Text = []byte(strings.Repeat("Word ", cohesionTokenCap/3))
			}
		}
		require.Equal(rt, oracleTermFrequencies(concatText(window)), windowTermFrequencies(window))
	})
}

// TestTermFrequenciesCostIsPerDistinctToken is the SP08-D1 cost pin for this stage: counting a
// window's tokens allocates for each DISTINCT term it keeps, not for each token it reads. Ten times
// the tokens over the same sixteen terms must cost no more allocations, and the whole count stays
// within a few allocations per term. The regexp form allocated at least once per token — 12 030
// times for the 4 000-token case below.
func TestTermFrequenciesCostIsPerDistinctToken(t *testing.T) {
	const distinct = 16
	text := func(tokens int) []byte {
		var sb strings.Builder
		for i := range tokens {
			sb.WriteString("Ident")
			sb.WriteByte(byte('a' + i%distinct))
			sb.WriteString(" = 42;\n")
		}
		return []byte(sb.String())
	}
	short, long := text(cohesionTokenCap/10), text(cohesionTokenCap)
	require.Len(t, termFrequencies(short), distinct)
	require.Len(t, termFrequencies(long), distinct)

	allocsShort := testing.AllocsPerRun(5, func() { _ = termFrequencies(short) })
	allocsLong := testing.AllocsPerRun(5, func() { _ = termFrequencies(long) })
	require.LessOrEqual(t, allocsLong, allocsShort,
		"%d tokens allocated %.0f times against %.0f for %d tokens of the same %d terms: the cost grows with tokens",
		cohesionTokenCap, allocsLong, allocsShort, cohesionTokenCap/10, distinct)
	require.LessOrEqual(t, allocsLong, float64(3*distinct+16),
		"termFrequencies over %d tokens of %d distinct terms allocated %.0f times", cohesionTokenCap, distinct, allocsLong)
}
