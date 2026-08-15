package canon

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// A rule's need list and its perLine flag are pure optimizations: they must change how long
// matching takes and nothing about what it finds. Nothing else in the package can check that. A
// need that is not implied by its pattern silently disables the rule — the canonical output simply
// stops containing that token, no error is raised anywhere, and the only visible symptom is a
// dedup ratio that is quietly worse than it should be. The tests below are what make the prefilter
// safe to add to a rule, and they are the reason a new rule may carry one at all.
//
// referenceMatches is the unoptimized ruleMatches: every rule scanned over the whole buffer, with
// need and perLine ignored. Match ORDER is compared too, not just the set — ruleMatches walks
// rules in table order and, within a rule, lines and then matches in ascending offset, which is
// the same order this produces.
func referenceMatches(in []byte, rules []reRule) []Match {
	var dst []Match
	for i := range rules {
		dst = appendRuleSpans(dst, &rules[i], in, 0)
	}
	return dst
}

// allTables is every rule table ruleMatches is called with, named for subtest output.
func allTables() []struct {
	name  string
	rules []reRule
} {
	return []struct {
		name  string
		rules []reRule
	}{
		{"ansi", ansiRules},
		{"timestamps", timestampRules},
		{"durations", durationRules},
		{"pids", pidRules},
		{"addresses", addressRules},
		{"tmpPaths", tmpPathRules},
		{"testrunner", testRunnerRules},
		{"webfetch", webFetchRules},
		{"git", gitRules},
	}
}

// corpusBodies reads every captured tool output under testdata/corpora/toolout, keyed by its
// group-qualified name. It is the realistic half of the prefilter evidence: the fuzz target below
// reaches shapes no captured file contains, and the corpus reaches combinations no generator
// would stumble on.
func corpusBodies(t *testing.T) map[string][]byte {
	t.Helper()

	root := filepath.Join("..", "..", "testdata", "corpora", "toolout")
	out := map[string][]byte{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(d.Name(), ".meta.json") {
			return err
		}
		b, rerr := os.ReadFile(p) //nolint:gosec // a path from this repo's own testdata tree
		if rerr != nil {
			return rerr
		}
		rel, rerr := filepath.Rel(root, p)
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

// TestPrefilterAgreesWithFullScan runs every rule table both ways over every captured file.
func TestPrefilterAgreesWithFullScan(t *testing.T) {
	bodies := corpusBodies(t)
	for _, tb := range allTables() {
		t.Run(tb.name, func(t *testing.T) {
			for rel, body := range bodies {
				require.Equal(t, referenceMatches(body, tb.rules), ruleMatches(body, tb.rules),
					"%s: prefiltered and full scan disagree", rel)
			}
		})
	}
}

// TestPrefilterAgreesOnGeneratedInput is the property version, over bytes drawn from the alphabet
// the rules actually key on. A uniform byte generator would essentially never produce "goroutine "
// or an ESC introducer, so it would confirm only that absent literals stay absent.
func TestPrefilterAgreesOnGeneratedInput(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		in := prefilterInput().Draw(rt, "in")
		for _, tb := range allTables() {
			require.Equal(rt, referenceMatches(in, tb.rules), ruleMatches(in, tb.rules), tb.name)
		}
	})
}

// prefilterInput draws a string out of fragments that sit on the boundary of some rule's need:
// the literal itself, a case-variant of it, the literal split by an escape sequence or a newline,
// and the surrounding punctuation that decides whether the rule then matches.
func prefilterInput() *rapid.Generator[[]byte] {
	frags := []string{
		"ok", "OK", "o\x1b[0mk", "--- PASS", "--- FAIL", "---\nPASS", "goroutine ", "GoRoutine ",
		"Time:", "Time:\n", "worker ", "gw", "gw1", "finished in ", "Finished ", "commit ",
		"index ", "From ", "pid", "PID", "PiD", "pid=", "process ", "0x", "0xdeadbeef",
		"127.0.0.1", "localhost", "0.0.0.0", "[::1]", ":8080", "/tmp/", "/tmp/x", "/var/folders/",
		"$TMPDIR/", `C:\Users\a\AppData\Local\Temp\x`, "C:/Users/a/AppData/Local/Temp/x",
		"AppData", "APPDATA", "utm_source=x", "sid=abc", "nonce=\"abcdefgh\"", `W/"abcd"`,
		"GMT", "Jan", "Dec", "Mon, 02 Jan 2006 15:04:05 GMT", "2024-01-15T10:32:07Z",
		"12:34:56", "1700000000000", "1.5s", "250ms", "3ns", "1h2m3s", "in 4.5s", "[1234]",
		"\x1b[32m", "\x1b]0;title\x07", "\x1b", "\n", "\r\n", " ", "\t", "=====", "a", "_", "-",
	}
	return rapid.Custom(func(rt *rapid.T) []byte {
		parts := rapid.SliceOfN(rapid.SampledFrom(frags), 0, 12).Draw(rt, "parts")
		return []byte(strings.Join(parts, ""))
	})
}

// FuzzPrefilterAgreesWithFullScan is the same property on arbitrary bytes, seeded from the corpus.
func FuzzPrefilterAgreesWithFullScan(f *testing.F) {
	for _, body := range corpusSeeds(f) {
		f.Add(body)
	}
	f.Add([]byte("ok  \tgithub.com/x/y\t0.123s\n--- FAIL: TestX (0.00s)\n"))
	f.Add([]byte("\x1b[32mPASS\x1b[0m pid=41235 in 1.5s\r\n"))
	f.Add([]byte(""))

	f.Fuzz(func(t *testing.T, in []byte) {
		for _, tb := range allTables() {
			require.Equal(t, referenceMatches(in, tb.rules), ruleMatches(in, tb.rules), tb.name)
		}
	})
}

// corpusSeeds is corpusBodies for a *testing.F, which cannot be passed where a *testing.T is
// wanted; making the loader generic over testing.TB would cost the require helpers' t.Helper
// attribution, which is worth more here than the duplication costs.
func corpusSeeds(f *testing.F) [][]byte {
	f.Helper()

	root := filepath.Join("..", "..", "testdata", "corpora", "toolout")
	var out [][]byte
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(d.Name(), ".meta.json") {
			return err
		}
		b, rerr := os.ReadFile(p) //nolint:gosec // a path from this repo's own testdata tree
		if rerr != nil {
			return rerr
		}
		out = append(out, b)
		return nil
	})
	if err != nil {
		f.Fatalf("reading the corpus: %v", err)
	}
	return out
}

// trailingWSReference is the regexp trailingWSMatches replaced, kept as the oracle the byte loop is
// compared against. escAnyInline rather than escAny, because the loop bounds escLen to the line.
var trailingWSReference = regexp.MustCompile(`(?m)(?:` + escAnyInline + `|[ \t\r])+$`)

// referenceTrailingWS is the region-scan trailingWSMatches used before it became a byte loop.
func referenceTrailingWS(in []byte) []Match {
	var dst []Match
	for _, loc := range trailingWSReference.FindAllIndex(in, -1) {
		start, end := loc[0], loc[1]
		for i := start; i < end; {
			if in[i] == '\r' {
				i++
				continue
			}
			runStart, sawWS := i, false
			for ; i < end && in[i] != '\r'; i++ {
				if in[i] == ' ' || in[i] == '\t' {
					sawWS = true
				}
			}
			if sawWS {
				dst = append(dst, Match{Offset: runStart, Len: i - runStart, Class: ClassCRLF})
			}
		}
	}
	return dst
}

// TestEscLen_Table pins the four escape shapes escLen decodes by hand.
//
// The differential fuzz below exercises these far harder than a table can, but it exercises them
// through trailingWSMatches, where an off-by-one only shows up when it happens to move a tail
// boundary. These rows say what each shape IS, so a wrong answer is reported at the byte that is
// wrong rather than at the span that shifted.
func TestEscLen_Table(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want int
	}{
		{"not an escape", "abc", 0},
		{"bare esc at end", "\x1b", 1},
		{"csi sgr", "\x1b[32m", 5},
		{"csi no params", "\x1b[m", 3},
		{"csi with intermediates", "\x1b[0 q", 5},
		{"csi unterminated is a bare esc", "\x1b[0", 1},
		{"csi missing final byte", "\x1b[", 1},
		{"osc bel terminated", "\x1b]0;title\x07", 10},
		{"osc st terminated", "\x1b]8;;http://x\x1b\\", 15},
		{"osc unterminated falls back to escTwo", "\x1b]0;title", 2},
		{"esc two", "\x1bM", 2},
		{"esc followed by bracket is not escTwo", "\x1b[", 1},
		{"esc followed by a low byte", "\x1b\x01", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, escLen([]byte(tc.in), 0))
		})
	}

	require.Equal(t, 0, escLen([]byte("abc"), 3), "an index at the end decodes nothing")
	require.Equal(t, 5, escLen([]byte("ab\x1b[32mcd"), 2), "escLen decodes at an offset, not only at 0")
}

// TestContainsFold_Table pins the allocation-free case-insensitive search, including the case its
// two-pass shape exists for: a haystack whose lowercase first byte is common but whose only real
// occurrence is in another case.
func TestContainsFold_Table(t *testing.T) {
	for _, tc := range []struct {
		in, lit string
		want    bool
	}{
		{"", "pid", false},
		{"pid", "pid", true},
		{"PID", "pid", true},
		{"PiD=7", "pid", true},
		{"a a a a APPDATA", "appdata", true},
		{"aaaaaaaaaa", "appdata", false},
		{"pi", "pid", false},
		{"xxpid", "pid", true},
		{"pidx", "pid", true},
		{"anything", "", true},
	} {
		require.Equal(t, tc.want, containsFold([]byte(tc.in), tc.lit), "%q contains %q", tc.in, tc.lit)
	}
}

// TestTrailingWS_AgreesWithReference pins the byte loop to the pattern it replaced.
func TestTrailingWS_AgreesWithReference(t *testing.T) {
	for rel, body := range corpusBodies(t) {
		require.Equal(t, referenceTrailingWS(body), trailingWSMatches(nil, body), rel)
	}

	rapid.Check(t, func(rt *rapid.T) {
		in := prefilterInput().Draw(rt, "in")
		require.Equal(rt, referenceTrailingWS(in), trailingWSMatches(nil, in))
	})
}

// FuzzTrailingWSAgreesWithReference is the same on arbitrary bytes: escLen reimplements four escape
// shapes by hand, and an off-by-one in any of them shows up here as a tail that starts one byte
// early or late.
func FuzzTrailingWSAgreesWithReference(f *testing.F) {
	for _, body := range corpusSeeds(f) {
		f.Add(body)
	}
	f.Add([]byte("a \x1b[0m\t\r\n b  \n"))
	f.Add([]byte("\t\r\t"))
	f.Add([]byte(" \x1b"))
	f.Add([]byte("x \x1b]0;t\x07 \n"))

	f.Fuzz(func(t *testing.T, in []byte) {
		require.Equal(t, referenceTrailingWS(in), trailingWSMatches(nil, in))
	})
}
