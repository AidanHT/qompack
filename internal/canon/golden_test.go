package canon_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/stretchr/testify/require"
)

// corpusRoot is testdata/corpora/toolout relative to this package's directory, which is the
// working directory of a Go test binary.
var corpusRoot = filepath.Join("..", "..", "testdata", "corpora", "toolout")

// metaSuffix marks the sidecar carrying the (tool, path) pair a corpus file was captured as.
const metaSuffix = ".meta.json"

// corpusFile is one captured tool output plus the sidecar that says how to canonicalize it.
type corpusFile struct {
	// rel is the group-qualified name, e.g. "testrunner/go-test-pass.txt", used as the subtest
	// name and as the golden path.
	rel  string
	tool string
	path string
	body []byte
}

// loadCorpus reads every captured tool output under testdata/corpora/toolout, sorted, so subtests
// and goldens are stable across filesystems.
//
// The (tool, path) sidecar is data rather than a convention because it selects which per-tool
// canonicalizers Applies returns true for: `go test` output measured as tool "Grep" would exercise
// none of the rules that matter for it, and every assertion below would pass vacuously.
func loadCorpus(t *testing.T) []corpusFile {
	t.Helper()

	var out []corpusFile
	err := filepath.WalkDir(corpusRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || strings.HasSuffix(d.Name(), metaSuffix) {
			return nil
		}

		body, rerr := os.ReadFile(p) //nolint:gosec // a path from this repo's own testdata tree
		require.NoError(t, rerr)

		metaBytes, rerr := os.ReadFile(p + metaSuffix) //nolint:gosec // same
		require.NoError(t, rerr, "every corpus file needs a %s sidecar", metaSuffix)

		var m struct {
			Tool string `json:"tool"`
			Path string `json:"path"`
		}
		require.NoError(t, json.Unmarshal(metaBytes, &m))
		require.NotEmpty(t, m.Tool, "sidecar for %s names no tool", p)

		rel, rerr := filepath.Rel(corpusRoot, p)
		require.NoError(t, rerr)
		out = append(out, corpusFile{rel: filepath.ToSlash(rel), tool: m.Tool, path: m.Path, body: body})
		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, out, "the tool-output corpus is empty")

	sort.Slice(out, func(i, j int) bool { return out[i].rel < out[j].rel })
	return out
}

// corpusOptions is how production canonicalizes: Appendix C's defaults, with deltas kept so the
// Restore inverse can be asserted.
func corpusOptions() canon.Options {
	return canon.OptionsFrom(config.Defaults().Store.Canonicalize, true)
}

// TestGoldenCorpus_AllFiles freezes the canonical form of every captured tool output.
//
// A golden per corpus file is what makes a regex change reviewable: tuning one rule shows up as a
// diff in the outputs it actually affects, rather than as a dedup ratio that silently moved.
func TestGoldenCorpus_AllFiles(t *testing.T) {
	r := canon.Default(config.Defaults().Store.Canonicalize)
	o := corpusOptions()

	for _, f := range loadCorpus(t) {
		t.Run(f.rel, func(t *testing.T) {
			res, err := r.Run(f.tool, f.path, f.body, o)
			require.NoError(t, err)
			testutil.Golden(t, f.rel+".canon.txt", res.Canonical)
		})
	}
}

// TestCorpus_StructuralProperties asserts 00-ARCHITECTURE.md §5.6's three normative properties
// over real tool output rather than over synthetic fixtures: idempotence, non-growth, and a
// byte-exact Restore.
//
// Real output is the interesting case. Synthetic fixtures exercise one rule at a time; a captured
// `npm install` log exercises ANSI, progress-carriage-returns, durations, temp paths and trailing
// whitespace in the same bytes, which is where overlapping rules break each other.
func TestCorpus_StructuralProperties(t *testing.T) {
	r := canon.Default(config.Defaults().Store.Canonicalize)
	o := corpusOptions()

	for _, f := range loadCorpus(t) {
		t.Run(f.rel, func(t *testing.T) {
			first, err := r.Run(f.tool, f.path, f.body, o)
			require.NoError(t, err)

			require.LessOrEqual(t, len(first.Canonical), len(f.body),
				"no canonicalizer may grow its input (§5.6)")

			second, err := r.Run(f.tool, f.path, first.Canonical, o)
			require.NoError(t, err)
			require.Equal(t, first.Canonical, second.Canonical,
				"Canonicalize(Canonicalize(x)) must equal Canonicalize(x) (§5.6)")

			restored, err := canon.Restore(first.Canonical, first.Deltas)
			require.NoError(t, err)
			require.Equal(t, f.body, restored,
				"Restore(Canonicalize(x).Canonical, deltas) must equal x (§5.6)")

			t.Logf("%s: %d -> %d bytes (reduced %.4f), applied %v",
				f.rel, len(f.body), len(first.Canonical), first.Reduced, first.Applied)
		})
	}
}

// TestCorpus_AppliedIsDeterministic asserts Result.Applied is stable across repeated runs, which
// is what makes it usable as a stored field in SP-06's records rather than as a debugging aid.
func TestCorpus_AppliedIsDeterministic(t *testing.T) {
	r := canon.Default(config.Defaults().Store.Canonicalize)
	o := corpusOptions()

	for _, f := range loadCorpus(t) {
		t.Run(f.rel, func(t *testing.T) {
			first, err := r.Run(f.tool, f.path, f.body, o)
			require.NoError(t, err)
			for i := 0; i < 10; i++ {
				again, rerr := r.Run(f.tool, f.path, f.body, o)
				require.NoError(t, rerr)
				require.Equal(t, first.Applied, again.Applied, "repeat %d", i)
				require.Equal(t, first.Canonical, again.Canonical, "repeat %d", i)
			}
		})
	}
}

// TestMatcherClassAssigned is the guard that keeps a rule from being silently dead.
//
// Registry.Run's accept loop drops any Match whose Class is not in play, and the zero Class is in
// play for nobody — so a rule that forgot to set one would compile, run, emit matches, and change
// nothing, with no error anywhere. This walks every built-in over every corpus file and asserts
// each emitted Match carries a Class from KnownClasses, and lies inside the input.
func TestMatcherClassAssigned(t *testing.T) {
	r := canon.Default(config.Defaults().Store.Canonicalize)
	o := corpusOptions()

	known := map[canon.Class]bool{}
	for _, c := range canon.KnownClasses() {
		known[c] = true
	}

	for _, f := range loadCorpus(t) {
		t.Run(f.rel, func(t *testing.T) {
			for _, c := range r.For(f.tool, f.path) {
				matches, err := canon.MatchesOf(c, f.body, o)
				require.NoError(t, err, "every built-in must implement Matcher")
				for i, m := range matches {
					require.NotEqual(t, canon.Class(""), m.Class,
						"%s match %d carries the zero Class and would be silently dropped", c.Name(), i)
					require.True(t, known[m.Class],
						"%s match %d carries unknown Class %q", c.Name(), i, m.Class)
					require.GreaterOrEqual(t, m.Offset, 0, "%s match %d", c.Name(), i)
					require.LessOrEqual(t, m.End(), len(f.body),
						"%s match %d runs past the input", c.Name(), i)
				}
			}
		})
	}
}

// FuzzCanonicalize asserts the whole registry never panics, never grows its input, stays exactly
// invertible, and settles — on arbitrary bytes, seeded from real tool output.
//
// The tool argument is fuzzed too, because Applies dispatches on it: a mutated tool name reaches
// combinations of canonicalizers that no captured corpus file does.
//
// # Idempotence is asserted first, and only a deletion may excuse it
//
// 00-ARCHITECTURE.md §5.6 wants Canonicalize(Canonicalize(x)) == Canonicalize(x) for every x, and
// that is the first thing checked. When it does not hold, this does not shrug: it requires the
// first pass to have DELETED bytes, and then requires the sequence to reach a fixed point anyway.
//
// That split is the exact boundary of what single-pass composition can promise, and it is
// derivable rather than empirical. A pass that only SUBSTITUTES cannot change what the next pass
// finds: every token is bracketed in '<' and '>', which no rule admits, so no new match can cross
// a token; and the word-boundary invariant documented above reRule in generic.go means a
// substitution only ever removes a word boundary, never creates one. A substitution-only pass is
// therefore idempotent by construction, so a failure there is a real rule bug — which is why it is
// hard-failed here, and it is what caught the unanchored ISO-8601 rule that turned
// "0000-00-00 00:00:00Zpid 0000" into "<ts>pid <n>" on the second pass but not the first.
//
// A DELETION carries no such guarantee. Removing bytes concatenates their neighbours, and the
// joined text can match a rule that neither fragment matched: stripping the ESC out of "000\x1b0s"
// leaves "0000s", a duration the first pass could not see. No rule-level anchor reaches that,
// because the hazard is concatenation rather than adjacency — see the package doc for why closing
// it means rebasing second-pass Deltas into original coordinates, which is a change to the Delta
// contract SP-06 stores against.
//
// Real tool output does not exhibit it. TestCorpus_StructuralProperties asserts unconditional
// idempotence over every captured file and passes, because colourizers wrap whole tokens instead of
// splitting values. TestKnownDeletionMediatedLimit pins the two reproducers exactly.
func FuzzCanonicalize(f *testing.F) {
	for _, seed := range fuzzSeeds(f) {
		f.Add(seed.tool, seed.path, seed.body)
	}
	f.Add("Bash", "", []byte("\x1b[31mfail\x1b[0m 2024-01-15T10:32:07Z pid=41235 0.42s\r\n"))
	f.Add("Read", "src/auth.ts", []byte("\xef\xbb\xbfexport const x = 1;   \r\n"))
	f.Add("", "", []byte(""))

	r := canon.Default(config.Defaults().Store.Canonicalize)
	o := corpusOptions()

	f.Fuzz(func(t *testing.T, tool, path string, body []byte) {
		res, err := r.Run(tool, path, body, o)
		require.NoError(t, err)
		require.LessOrEqual(t, len(res.Canonical), len(body))

		restored, err := canon.Restore(res.Canonical, res.Deltas)
		require.NoError(t, err)
		if len(body) == 0 {
			require.Empty(t, restored)
			return
		}
		require.Equal(t, body, restored)

		again, err := r.Run(tool, path, res.Canonical, o)
		require.NoError(t, err)
		if bytes.Equal(again.Canonical, res.Canonical) {
			return
		}
		require.True(t, deletedSpan(res.Deltas),
			"a substitution-only pass must be idempotent (§5.6): %q -> %q -> %q",
			body, res.Canonical, again.Canonical)
		requireFixedPoint(t, r, tool, path, again.Canonical, o)
	})
}

// deletedSpan reports whether any Delta removed its span outright rather than substituting a token
// for it. Delta.Len is measured in CANONICAL coordinates, so a deletion is exactly Len == 0.
func deletedSpan(deltas []canon.Delta) bool {
	for _, d := range deltas {
		if d.Len == 0 {
			return true
		}
	}
	return false
}

// requireFixedPoint canonicalizes until the bytes stop changing, and fails if they have not settled
// within maxSettlingPasses.
//
// The bound matters more than the count. Output length is non-increasing and every rewrite is
// finite, so the sequence terminates on its own; what a bound rules out is a rule set that settles
// only after a number of passes proportional to the input, which would make canonicalization's cost
// a function of adversarial content rather than of size. Nothing found by fuzzing has needed more
// than one settling pass.
func requireFixedPoint(t *testing.T, r canon.Registry, tool, path string, cur []byte, o canon.Options) {
	t.Helper()

	const maxSettlingPasses = 8
	for i := 0; i < maxSettlingPasses; i++ {
		next, err := r.Run(tool, path, cur, o)
		require.NoError(t, err)
		require.LessOrEqual(t, len(next.Canonical), len(cur), "settling pass %d grew its input", i)
		if bytes.Equal(next.Canonical, cur) {
			return
		}
		cur = next.Canonical
	}
	require.Failf(t, "canonicalization never settled",
		"still changing after %d settling passes", maxSettlingPasses)
}

// TestKnownDeletionMediatedLimit pins the behaviour FuzzCanonicalize's deletion branch exists for,
// so the limit is recorded evidence rather than a hole in an assertion.
//
// Both rows are ANSI stripping joining two fragments into a value neither half contained. What the
// rows assert is that the cost is confined to WHICH canonical form is reached — never to
// correctness: Restore is byte-exact at every step, so a second-pass rewrite costs a store lookup
// that misses, not content that cannot be recovered.
func TestKnownDeletionMediatedLimit(t *testing.T) {
	r := canon.Default(config.Defaults().Store.Canonicalize)
	o := corpusOptions()

	for _, tc := range []struct {
		name         string
		in           []byte
		first, final string
	}{
		{
			name:  "esc split inside a duration",
			in:    []byte("000\x1b0s"),
			first: "0000s",
			final: "<d>",
		},
		{
			name:  "esc split inside an epoch-millis timestamp",
			in:    []byte("000\x1b0000000"),
			first: "0000000000",
			final: "<ts>",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := r.Run("0", "0", tc.in, o)
			require.NoError(t, err)
			require.Equal(t, tc.first, string(res.Canonical),
				"the first pass sees the ESC and cannot match across it")

			restored, err := canon.Restore(res.Canonical, res.Deltas)
			require.NoError(t, err)
			require.Equal(t, tc.in, restored, "Restore stays byte-exact through the limit")

			second, err := r.Run("0", "0", res.Canonical, o)
			require.NoError(t, err)
			require.Equal(t, tc.final, string(second.Canonical),
				"the second pass matches the value the deletion joined")

			restored, err = canon.Restore(second.Canonical, second.Deltas)
			require.NoError(t, err)
			require.Equal(t, res.Canonical, restored)

			third, err := r.Run("0", "0", second.Canonical, o)
			require.NoError(t, err)
			require.Equal(t, tc.final, string(third.Canonical), "and then it settles")
		})
	}
}

// fuzzSeed is one corpus entry reduced to what f.Add takes.
type fuzzSeed struct {
	tool, path string
	body       []byte
}

// fuzzSeeds reads the corpus for FuzzCanonicalize. It takes *testing.F rather than *testing.T,
// so it re-implements the walk instead of reusing loadCorpus; the alternative — making loadCorpus
// generic over testing.TB — would lose the require helpers' t.Helper attribution.
func fuzzSeeds(f *testing.F) []fuzzSeed {
	f.Helper()

	var out []fuzzSeed
	err := filepath.WalkDir(corpusRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(d.Name(), metaSuffix) {
			return err
		}
		body, rerr := os.ReadFile(p) //nolint:gosec // a path from this repo's own testdata tree
		if rerr != nil {
			return rerr
		}
		metaBytes, rerr := os.ReadFile(p + metaSuffix) //nolint:gosec // same
		if rerr != nil {
			return rerr
		}
		var m struct {
			Tool string `json:"tool"`
			Path string `json:"path"`
		}
		if jerr := json.Unmarshal(metaBytes, &m); jerr != nil {
			return jerr
		}
		out = append(out, fuzzSeed{tool: m.Tool, path: m.Path, body: body})
		return nil
	})
	if err != nil {
		f.Fatalf("reading the fuzz seed corpus: %v", err)
	}
	return out
}
