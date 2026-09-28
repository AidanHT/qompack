package store

import (
	"bytes"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// countFoldEveryOffset and indexFoldEveryOffset are countFold and indexFold as they were before
// foldCursor: a comparison at every offset. The cursor versions must return exactly what these
// return.
func countFoldEveryOffset(hay []byte, needle string) int {
	if needle == "" || len(hay) < len(needle) {
		return 0
	}
	n := 0
	for i := 0; i+len(needle) <= len(hay); {
		if foldEqualAt(hay, i, needle) {
			n++
			i += len(needle)
			continue
		}
		i++
	}
	return n
}

func indexFoldEveryOffset(hay []byte, needle string) int {
	if needle == "" || len(hay) < len(needle) {
		return -1
	}
	for i := 0; i+len(needle) <= len(hay); i++ {
		if foldEqualAt(hay, i, needle) {
			return i
		}
	}
	return -1
}

// TestFoldSearch_CursorMatchesEveryOffsetScan compares the cursor-driven countFold and indexFold
// with the every-offset scans over generated inputs: small alphabets so matches, overlaps and
// near-misses are frequent, both cases of letters, non-letter and non-ASCII first bytes (which
// fold to themselves), needles longer than the haystack, and matches at both ends.
func TestFoldSearch_CursorMatchesEveryOffsetScan(t *testing.T) {
	alphabets := []string{"aA", "abAB", "ab[", "lL zZ", "\x80\xc3aA", "aaaaaaaaaA", "the quick brown fox LAZY dog\n"}
	rng := rand.New(rand.NewPCG(5, 6))
	pick := func(alpha string, n int) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = alpha[rng.IntN(len(alpha))]
		}
		return b
	}
	for iter := 0; iter < 50000; iter++ {
		alpha := alphabets[rng.IntN(len(alphabets))]
		hay := pick(alpha, rng.IntN(64))
		needle := string(pick(alpha, 1+rng.IntN(5)))
		if rng.IntN(4) == 0 && len(hay) >= len(needle) {
			copy(hay[len(hay)-len(needle):], strings.ToUpper(needle))
		}
		require.Equal(t, countFoldEveryOffset(hay, needle), countFold(hay, needle), "countFold(%q, %q)", hay, needle)
		require.Equal(t, indexFoldEveryOffset(hay, needle), indexFold(hay, needle), "indexFold(%q, %q)", hay, needle)
	}
}

// BenchmarkCountFold_4MiB is score's text term over the content BenchmarkSearch_1000Roots scans:
// 512 candidates of its ~8 KB body, 4 MiB in all.
func BenchmarkCountFold_4MiB(b *testing.B) {
	hay := bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog\n"), (4<<20)/44)
	b.SetBytes(int64(len(hay)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if countFold(hay, "lazy dog") == 0 {
			b.Fatal("fixture: the needle must occur")
		}
	}
}
