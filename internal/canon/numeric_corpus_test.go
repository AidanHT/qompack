package canon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The corpus-driven half of the numeric scanner's evidence. It lives in its own file because it is
// the half that needs testdata/corpora/toolout, which lands with the corpus commit; the table, the
// property and the pathological-escape rows in numeric_test.go stand on their own without it.

// numericCorpusRoot is testdata/corpora/toolout relative to this package's directory.
var numericCorpusRoot = filepath.Join("..", "..", "testdata", "corpora", "toolout")

// numericCorpus reads every captured tool output under testdata/corpora/toolout, keyed by its
// group-qualified name. The .meta.json sidecars are skipped: they say which canonicalizers apply
// to a file, which is a question this file does not ask — every input is fed to both classes here
// regardless of the tool that produced it, because a Matcher's answer may not depend on one.
func numericCorpus(t *testing.T) map[string][]byte {
	t.Helper()

	out := map[string][]byte{}
	err := filepath.WalkDir(numericCorpusRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(d.Name(), ".meta.json") {
			return err
		}
		b, rerr := os.ReadFile(p) //nolint:gosec // a path from this repo's own testdata tree
		if rerr != nil {
			return rerr
		}
		rel, rerr := filepath.Rel(numericCorpusRoot, p)
		if rerr != nil {
			return rerr
		}
		out[filepath.ToSlash(rel)] = b
		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, out, "the tool-output corpus is empty")
	return out
}

// TestNumericMatchesAgreeWithReference is the evidence that matters most, because it is the
// evidence the goldens were generated from: every captured tool output, both classes, byte for
// byte against the patterns.
func TestNumericMatchesAgreeWithReference(t *testing.T) {
	corpus := numericCorpus(t)
	require.GreaterOrEqual(t, len(corpus), 24, "the corpus lost files")

	for rel, body := range corpus {
		t.Run(rel, func(t *testing.T) {
			requireNumericAgreement(t, body, rel)
		})
	}
}

// FuzzNumericMatchesAgreeWithReference is the same agreement on arbitrary bytes, seeded from the
// corpus and from the rows above that pin the escape-run walk.
//
// The escape branch is what this exists for. wordEdge's `(?:escAny)+` is a greedy repetition of a
// four-way alternation, which the scanner reproduces as a depth-first walk; the corpus contains
// well-formed SGR sequences and essentially nothing else, so the readings that disagree — an OSC
// body that swallows a value a two-byte reading would have found, a run that reaches the same
// position two ways — are reachable by mutation and by nothing else.
func FuzzNumericMatchesAgreeWithReference(f *testing.F) {
	err := filepath.WalkDir(numericCorpusRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(d.Name(), ".meta.json") {
			return err
		}
		b, rerr := os.ReadFile(p) //nolint:gosec // a path from this repo's own testdata tree
		if rerr != nil {
			return rerr
		}
		f.Add(b)
		return nil
	})
	if err != nil {
		f.Fatalf("reading the corpus: %v", err)
	}

	f.Add([]byte("2024-01-15T10:32:07.123456789+02:00 12:34:56.1234567 1700000000000\n"))
	f.Add([]byte("\x1b[32m1h2m3.5s\x1b[0m done in 250ms, 5 µs, 12ms, 3ns\n"))
	f.Add([]byte("\x1b]1234567890\x071234567890 \x1b[12:34:56~01:02:03 \x1b]5s\x07"))
	f.Add([]byte("\x1b]\x1b\\\x1b]\x1b\\\x1b]\x1b\\5s"))
	f.Add([]byte(""))

	f.Fuzz(func(t *testing.T, in []byte) {
		requireNumericAgreement(t, in, "fuzz")
	})
}
