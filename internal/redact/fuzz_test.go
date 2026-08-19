package redact_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/redact"
)

// seedFuzzCorpus adds every committed corpus fixture plus the hand-picked edge cases that probe
// the placeholder guard and the rule boundaries. Both fuzz targets share it.
func seedFuzzCorpus(f *testing.F) {
	f.Helper()
	ents, err := os.ReadDir(corpusDir)
	require.NoError(f, err)
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".txt") {
			continue
		}
		b, rerr := os.ReadFile(filepath.Join(corpusDir, e.Name()))
		require.NoError(f, rerr)
		f.Add(b)
	}
	f.Add([]byte(""))
	f.Add([]byte("«redacted:jwt»"))
	f.Add([]byte("«redacted:«redacted:jwt»»"))
	f.Add([]byte("password="))
	f.Add([]byte("password=x"))
	f.Add([]byte("AKIA" + "IOSFODNN7EXAMPLE"))
	f.Add([]byte("\xff\xfe\x00 binary noise \x01\x02"))
}

// FuzzRedactIdempotent is the §5.22a fuzz target: Redact(Redact(x)) == Redact(x) for arbitrary
// input, Redact never panics, and a non-empty input never yields an empty output.
//
// Idempotence is the property that makes the store's choke point safe to re-run — a WAL replay, a
// retried hook, or a re-ingested tool result must not stack placeholders on top of placeholders,
// which would both corrupt content and defeat chunk-level dedup.
func FuzzRedactIdempotent(f *testing.F) {
	seedFuzzCorpus(f)

	r := redact.New(config.Defaults())

	f.Fuzz(func(t *testing.T, in []byte) {
		once, ms1 := r.Redact(in)
		twice, ms2 := r.Redact(once)

		require.Equal(t, once, twice, "Redact(Redact(x)) must equal Redact(x)")
		if len(ms1) > 0 {
			require.Empty(t, ms2, "a second pass over already-redacted content must find nothing")
		}
		if len(in) > 0 {
			require.NotEmpty(t, once, "a non-empty input must never redact to nothing")
		}
		if utf8.Valid(in) {
			require.True(t, utf8.Valid(once),
				"valid UTF-8 in must stay valid UTF-8 out: the placeholder writer must not truncate »")
		}
	})
}

// FuzzRedactPrefilterEquivalence asserts the mandatory-literal prefilter is behaviour-neutral on
// arbitrary input: skipping a rule's regex because none of its literals is present must produce
// exactly what running the regex would have produced.
//
// This is the fuzz half of TestPrefilter_IsBehaviourNeutral, and it is the target that matters
// most for this optimization. A literal that is only ALMOST mandatory — one that holds for every
// hand-written fixture but not for some odd byte sequence — would silently stop redacting a real
// secret, and a table test full of realistic inputs is exactly the thing that would not catch it.
func FuzzRedactPrefilterEquivalence(f *testing.F) {
	seedFuzzCorpus(f)
	for _, probe := range prefilterProbes {
		f.Add([]byte(probe))
	}

	fast := redact.New(config.Defaults())
	slow := redact.NewWithoutPrefilter(config.Defaults())

	f.Fuzz(func(t *testing.T, in []byte) {
		fastOut, fastMs := fast.Redact(in)
		slowOut, slowMs := slow.Redact(in)

		require.Equal(t, slowOut, fastOut, "prefilter changed the redacted output")
		require.Equal(t, slowMs, fastMs, "prefilter changed the reported matches")
	})
}
