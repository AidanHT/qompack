package canon

import (
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// TestBash_ProgressCollapse pins the carriage-return overwrite collapse: a progress bar redrawn
// three times leaves only what the user actually saw.
func TestBash_ProgressCollapse(t *testing.T) {
	runCanonCases(t, newBash(), []canonCase{
		{
			// Two deletions, not one: each runs from the start of its own segment through its own
			// '\r', so they do not overlap and acceptCandidates keeps both. Both land at canonical
			// offset 0 with length 0, because a deletion writes no bytes.
			name: "npm-style redraw",
			in:   "downloading 10%\rdownloading 90%\rdone\n",
			want: "done\n",
			wantDeltas: []Delta{
				{Offset: 0, Len: 0, Original: "downloading 10%\r", Class: ClassANSI},
				{Offset: 0, Len: 0, Original: "downloading 90%\r", Class: ClassANSI},
			},
		},
		{
			// A '\r' followed by '\n' is a line ending and belongs to crlfCanon; claiming it here
			// would delete the whole line instead of normalizing its terminator.
			name: "crlf is not progress residue", in: "a\r\nb", want: "a\r\nb",
		},
		{
			// A RUN of CRs ending in LF is a mangled line terminator, not progress residue: the
			// text before it WAS displayed, and crlfCanon owns the whole run. Claiming the first
			// CR here would overlap crlfCanon's match, win in acceptCandidates, and leave a live
			// "\r\n" behind for a second pass — which is what the captured `curl -v` output does.
			name: "cr run ending in lf belongs to crlf", in: "kept\r\r\ndone\n",
			want: "kept\r\r\ndone\n",
		},
		{name: "trailing bare cr", in: "spinner\r", want: "", deltas: 1},
		{name: "redraw across two lines", in: "a\rb\nc\rd\n", want: "b\nd\n", deltas: 2},
		{name: "no carriage returns", in: "plain\n", want: "plain\n"},
	})
}

// TestBash_TrailingWhitespace pins the trailing-whitespace rule, which is ClassCRLF — always-on
// and not gateable — for the same reason CRLF→LF is (00-ARCHITECTURE.md §4).
func TestBash_TrailingWhitespace(t *testing.T) {
	runCanonCases(t, newBash(), []canonCase{
		{name: "spaces tabs and eof", in: "a  \nb\t\nc  ", want: "a\nb\nc", deltas: 3},
		{
			// The progress deletion and the trailing-whitespace deletion can never overlap: a
			// progress span always ends at a '\r', and '\r' is not in [ \t].
			name: "progress and trailing whitespace together",
			in:   "x  \rfinal  \n", want: "final\n", deltas: 2,
		},
		{
			// The CR is matched as context and left in the input, so crlfCanon still sees an intact
			// "\r\n" to normalize (and LineEndingClass still sees its Delta). Without the `\r*`
			// this rule would not fire until crlfCanon had removed the CR — a second pass.
			name: "whitespace before a crlf", in: "a  \r\nb", want: "a\r\nb", deltas: 1,
		},
		{
			// Doubly-converted streams really do carry "\r\r\n"; the quantifier is '*', not '?'.
			name: "whitespace before a double cr", in: "a  \r\r\nb", want: "a\r\r\nb", deltas: 1,
		},
		{
			// The shared trailing-whitespace rule's own CR-separated-runs regression lives on glob
			// (TestTrailingWhitespace_CRSeparatedRuns), not here: bash's progress-collapse span
			// starts at the line start and so wins overlap resolution against it, which would mask
			// exactly the behaviour that test exists to pin.
			name: "cr-only tail is left entirely alone",
			in:   "a\r\r\nb", want: "a\r\r\nb", deltas: 0,
		},
		{
			// A colourized tool writes " \x1b[m" at the end of a line. A span that stopped at the
			// space would leave the escape for ansiCanon to delete in the same pass, after which
			// the space is trailing again for the NEXT pass — so the escape is inside the span.
			name: "whitespace followed by an escape", in: "a \x1b[m\nb", want: "a\nb", deltas: 1,
		},
		{name: "interior whitespace is untouched", in: "a  b\n", want: "a  b\n"},
		{name: "nothing to trim", in: "a\nb\n", want: "a\nb\n"},
	})
}

// TestBash_Applies pins the alias set and its case-insensitivity.
func TestBash_Applies(t *testing.T) {
	b := newBash()
	for _, tool := range []string{"bash", "Bash", "POWERSHELL", "shell"} {
		require.True(t, b.Applies(tool, "out.txt"), tool)
	}
	for _, tool := range []string{"read", "grep", "webfetch", ""} {
		require.False(t, b.Applies(tool, "out.txt"), tool)
	}
}

// TestTestRunner_Go covers `go test`'s three volatile shapes and names the guard reliance on
// short goroutine numbers.
func TestTestRunner_Go(t *testing.T) {
	runCanonCases(t, newTestRunner(), []canonCase{
		{
			name: "package summary",
			in:   "ok  \tgithub.com/qompack/qompack/internal/canon\t0.412s\n",
			want: "ok  \tgithub.com/qompack/qompack/internal/canon\t<d>\n", deltas: 1,
		},
		{
			// "(cached)" is the most informative token on the line and is not a duration; no rule
			// may touch it.
			name: "cached result is untouched",
			in:   "ok  \tgithub.com/qompack/qompack/internal/core\t(cached)\n",
			want: "ok  \tgithub.com/qompack/qompack/internal/core\t(cached)\n",
		},
		{name: "verbose pass line", in: "--- PASS: TestFoo (0.03s)\n", want: "--- PASS: TestFoo (<d>)\n", deltas: 1},
		{
			name: "indented fail line", in: "    --- FAIL: TestBar/sub (1.25s)\n",
			want: "    --- FAIL: TestBar/sub (<d>)\n", deltas: 1,
		},
		{name: "goroutine header", in: "goroutine 4242 [running]:\n", want: "goroutine <n> [running]:\n", deltas: 1},
		{
			// GUARD RELIANCE. goroutine 1 is the main goroutine and its capture is one byte against
			// a three-byte tokenNumber, so acceptCandidates drops the Match rather than grow the
			// input. The rule still emits it; non-growth is the registry's decision, made once.
			name: "single-digit goroutine survives the non-growing guard",
			in:   "goroutine 1 [running]:\n", want: "goroutine 1 [running]:\n",
		},
	})
}

// TestTestRunner_Jest covers jest's summary time and worker slot.
func TestTestRunner_Jest(t *testing.T) {
	runCanonCases(t, newTestRunner(), []canonCase{
		{name: "summary time", in: "Time:        3.456 s\n", want: "Time:        <d>\n", deltas: 1},
		{name: "worker slot", in: "Ran all test suites with worker 137.\n", want: "Ran all test suites with worker <n>.\n", deltas: 1},
		{
			// GUARD RELIANCE, as for goroutine numbers: workers 0-99 are one- or two-byte captures.
			name: "low worker number survives the non-growing guard",
			in:   "assigned to worker 2 now\n", want: "assigned to worker 2 now\n",
		},
	})
}

// TestTestRunner_Pytest covers pytest's footer, its randomly-seed line and the xdist worker slot.
func TestTestRunner_Pytest(t *testing.T) {
	runCanonCases(t, newTestRunner(), []canonCase{
		{
			name: "footer", in: "===== 3 passed in 0.12s =====\n",
			want: "===== 3 passed in <d> =====\n", deltas: 1,
		},
		{
			name: "randomly seed", in: "Using --randomly-seed=1739481922\n",
			want: "Using --randomly-seed=<seed>\n", deltas: 1,
		},
		{
			// GUARD RELIANCE. tokenSeed is six bytes, so a seed shorter than six digits is dropped
			// rather than replaced.
			name: "short seed survives the non-growing guard",
			in:   "Using --randomly-seed=42\n", want: "Using --randomly-seed=42\n",
		},
		{
			// The xdist rule matches the WHOLE marker, not its digits, and this row is why.
			// `\bgw(\d{1,2})\b` captures at most two bytes against a three-byte tokenNumber, so
			// acceptCandidates would reject every Match it could ever produce — the rule would be
			// inert in its entirety while still compiling, running and emitting matches. Matching
			// "gw3" whole is three bytes, which fits.
			name: "xdist worker slot is replaced whole",
			in:   "[gw3] PASSED test_x.py::test_y\n", want: "[<n>] PASSED test_x.py::test_y\n",
			wantDeltas: []Delta{{Offset: 1, Len: 3, Original: "gw3", Class: ClassPIDs}},
		},
		{
			// The two-digit form is four bytes against the same three-byte token, so it fits with a
			// byte to spare; both widths must collapse to the SAME token or a suite that happened to
			// use ten or more workers would dedup against itself and not against a smaller run.
			name: "two-digit xdist worker slot collapses to the same token",
			in:   "[gw12] PASSED test_x.py::test_y\n", want: "[<n>] PASSED test_x.py::test_y\n",
			wantDeltas: []Delta{{Offset: 1, Len: 3, Original: "gw12", Class: ClassPIDs}},
		},
	})
}

// TestTestRunner_Cargo covers cargo's two duration lines.
func TestTestRunner_Cargo(t *testing.T) {
	runCanonCases(t, newTestRunner(), []canonCase{
		{
			name: "nextest finished in", in: "test result: ok. 12 passed; finished in 1.23s\n",
			want: "test result: ok. 12 passed; finished in <d>\n", deltas: 1,
		},
		{
			name: "cargo build finished line",
			in:   "    Finished dev [unoptimized + debuginfo] target(s) in 0.53s\n",
			want: "    Finished dev [unoptimized + debuginfo] target(s) in <d>\n", deltas: 1,
		},
		{name: "no runner output", in: "hello world\n", want: "hello world\n"},
	})
}

// TestGrep_PathPrefixOnly is the boundary this canonicalizer exists to hold: separators are
// normalized INSIDE a hit's "path:line:" prefix and nowhere else, because everything after the
// prefix is the matched source line and may legitimately contain backslashes of its own.
func TestGrep_PathPrefixOnly(t *testing.T) {
	runCanonCases(t, newGrep(), []canonCase{
		{
			name: "prefix separators only",
			in:   "src\\a.ts:12:const x = a\\b\n",
			want: "src/a.ts:12:const x = a\\b\n", deltas: 1,
		},
		{
			// The drive letter and its separator are one three-byte span replaced by "/", rather
			// than two overlapping matches left for the registry to resolve.
			name: "drive letter and separators",
			in:   "C:\\src\\a.ts:12:hit\n",
			want: "/src/a.ts:12:hit\n", deltas: 2,
		},
		{
			name: "forward-slash drive letter",
			in:   "C:/src/a.ts:12:hit\n",
			want: "/src/a.ts:12:hit\n", deltas: 1,
		},
		{
			// No "path:line:" prefix means no prefix to normalize: the line is left completely
			// alone, which is the conservative answer for a line that is not a hit.
			name: "no prefix means no rewrite",
			in:   "just text with a \\ backslash\n", want: "just text with a \\ backslash\n",
		},
		{name: "trailing whitespace still goes", in: "a.ts:1:x  \n", want: "a.ts:1:x\n", deltas: 1},
		{name: "colon with no line number", in: "C:\\x\\y:notdigits\n", want: "C:\\x\\y:notdigits\n"},
	})
}

// TestGlob_SeparatorsAndTrailing pins glob's whole-line rewrite, which is safe precisely because
// every byte of a glob/ls/find line is a path.
func TestGlob_SeparatorsAndTrailing(t *testing.T) {
	runCanonCases(t, newGlob(), []canonCase{
		{
			name: "separators and trailing space",
			in:   "src\\a.ts\ndocs\\b.md  \n", want: "src/a.ts\ndocs/b.md\n", deltas: 3,
		},
		{name: "already posix", in: "src/a.ts\n", want: "src/a.ts\n"},
		{
			// Unlike grep there is no prefix restriction: a backslash anywhere on the line is a
			// separator, because the whole line is a path.
			name: "every backslash on the line", in: "a\\b\\c\n", want: "a/b/c\n", deltas: 2,
		},
	})
}

// TestFileRead_BOMAndTrailing pins BOM stripping and, just as importantly, the line-number gutter
// that is deliberately NOT stripped.
func TestFileRead_BOMAndTrailing(t *testing.T) {
	runCanonCases(t, newFileRead(), []canonCase{
		{name: "utf-8 bom", in: "\xef\xbb\xbfpackage main\n", want: "package main\n", deltas: 1},
		{
			// The whole RUN of BOMs, in one match: stripping one per pass would never reach a
			// fixed point.
			name: "double bom in one pass", in: "\xef\xbb\xbf\xef\xbb\xbfx", want: "x", deltas: 1,
		},
		{name: "trailing whitespace", in: "a  \nb", want: "a\nb", deltas: 1},
		{
			// NOT a gutter strip. A content line beginning with digits and a tab is
			// indistinguishable from a Read-tool line-number gutter — it could be a data file, a
			// diff or a tab-separated table — so removing it would eat real data and would not
			// survive a second pass.
			name: "line-number-shaped content is never stripped",
			in:   "12\tconst x = 1\n", want: "12\tconst x = 1\n",
		},
		{name: "bom in the middle stays", in: "a\xef\xbb\xbfb", want: "a\xef\xbb\xbfb"},
	})
}

// TestWebFetch_Table covers the four volatile-identifier rules.
func TestWebFetch_Table(t *testing.T) {
	runCanonCases(t, newWebFetch(), []canonCase{
		{
			name: "utm parameter deleted",
			in:   "https://x.dev/p?a=1&utm_source=news&b=2",
			want: "https://x.dev/p?a=1&b=2", deltas: 1,
		},
		{
			name: "two utm parameters", in: "https://x.dev/p?utm_source=a&utm_medium=b",
			want: "https://x.dev/p", deltas: 2,
		},
		{
			// The parameter NAME survives: the canonical URL still shows its shape.
			name: "session id value replaced", in: "https://x.dev/p?sid=abc123def",
			want: "https://x.dev/p?sid=<v>", deltas: 1,
		},
		{name: "cache buster", in: "https://x.dev/p?cb=98765432", want: "https://x.dev/p?cb=<v>", deltas: 1},
		{name: "weak etag", in: `ETag: W/"6a-1b2c3d"`, want: "ETag: <etag>", deltas: 1},
		{
			// A strong etag is a content digest — stable across fetches of unchanged content, and
			// therefore signal rather than churn.
			name: "strong etag is kept", in: `ETag: "6a-1b2c3d"`, want: `ETag: "6a-1b2c3d"`,
		},
		{
			// Unlike the query-parameter rule, this one replaces the WHOLE attribute rather than
			// just its value: a nonce attribute is meaningless without its value, so there is no
			// structure worth preserving around it.
			name: "csp nonce", in: `<script nonce="aGVsbG8xMjM0">`, want: "<script <nonce>>", deltas: 1,
		},
		{
			// GUARD RELIANCE. A one-byte capture against the three-byte tokenValue is dropped by
			// acceptCandidates rather than grown.
			name: "one-character session value survives the non-growing guard",
			in:   "https://x.dev/p?cb=7", want: "https://x.dev/p?cb=7",
		},
	})
}

// TestGit_Table covers the three object-hash lines and pins the two shapes git canonicalization
// must never touch.
func TestGit_Table(t *testing.T) {
	const sha40 = "0123456789abcdef0123456789abcdef01234567"
	runCanonCases(t, newGit(), []canonCase{
		{name: "commit line", in: "commit " + sha40 + "\n", want: "commit <sha>\n", deltas: 1},
		{name: "diff index line", in: "index 1234567..89abcde 100644\n", want: "index <sha>..<sha> 100644\n", deltas: 2},
		{
			name: "format-patch from line", in: "From " + sha40 + " Mon Sep 17 00:00:00 2001\n",
			want: "From <sha> Mon Sep 17 00:00:00 2001\n", deltas: 1,
		},
		{
			// Hunk header coordinates are SEMANTIC: they are where the hunk applies, and a reader
			// or a patch tool needs them exactly. Every rule here is keyword-anchored and needs a
			// hex run of at least seven characters, so none of them can reach these.
			name: "hunk header is never touched",
			in:   "@@ -1,7 +1,9 @@ func main() {\n", want: "@@ -1,7 +1,9 @@ func main() {\n",
		},
		{
			// The version identifies the environment that produced the output, which is signal.
			name: "git version is kept", in: "git version 2.43.0\n", want: "git version 2.43.0\n",
		},
		{
			// A decorated commit line fails the end anchor and is left whole rather than
			// half-rewritten.
			name: "decorated commit line is left whole",
			in:   "commit " + sha40 + " (HEAD -> main)\n", want: "commit " + sha40 + " (HEAD -> main)\n",
		},
		{
			// `git diff --color` wraps the index line in SGR sequences. Composition is one pass
			// over the ORIGINAL, so a bare ^ anchor would find this line only after ansiCanon had
			// stripped the escapes — on the SECOND pass. lineLead is what makes one pass enough.
			name: "colourized index line",
			in:   "\x1b[1mindex 1234567..89abcde 100644\x1b[m\n",
			want: "\x1b[1mindex <sha>..<sha> 100644\x1b[m\n", deltas: 2,
		},
		{
			// The same argument at the other end of the line: a trailing escape and a CR both sit
			// between the hash and the '$'.
			name: "colourized crlf-terminated commit line",
			in:   "commit " + sha40 + "\x1b[m\r\n", want: "commit <sha>\x1b[m\r\n", deltas: 1,
		},
	})
}

// ------------------------------------------------------------------------------------------
// Properties held by all fourteen canonicalizers.
// ------------------------------------------------------------------------------------------

// allCanonicalizers returns the fourteen built-ins in 00-ARCHITECTURE.md §5.6's registration
// order, which is the order Default registers them in and therefore the order that breaks ties in
// sortCandidates.
func allCanonicalizers() []Canonicalizer {
	return []Canonicalizer{
		newCRLF(), newANSI(), newTimestamps(), newDurations(), newPIDs(), newAddresses(),
		newTmpPaths(), newBash(), newTestRunner(), newGrep(), newGlob(), newFileRead(),
		newWebFetch(), newGit(),
	}
}

// volatileFixture is one blob carrying something for every one of the fourteen canonicalizers at
// once. Each of them sees it whole, so a rule that mis-fires on ANOTHER canonicalizer's content —
// the case that composition in Registry.Run will expose — is caught here rather than in a
// single-rule table that never shows it the neighbouring shapes.
const volatileFixture = "2024-01-02T03:04:05Z \x1b[31mERROR\x1b[0m pid=41235 took 1m30.5s\r\n" +
	"downloading 10%\rdone at 0xc000123456 on localhost:8080\r\n" +
	"src\\a.ts:12:const p = \"C:\\\\Users\\\\q\\\\AppData\\\\Local\\\\Temp\\\\t1\"  \n" +
	"ok  \tgithub.com/qompack/qompack\t0.412s\ngoroutine 4242 [running]:\n" +
	"commit 0123456789abcdef0123456789abcdef01234567\n" +
	"index 1234567..89abcde 100644\n@@ -1,7 +1,9 @@\n" +
	"GET https://x.dev/p?utm_source=a&sid=abc123def W/\"6a-1b2c3d\"\n" +
	"[1700000000000] Jan  2 15:04:05 /tmp/qompack-1/x $TMPDIR/y\n"

// canonFixtures is the unit corpus every property below runs over: one representative input per
// canonicalizer plus the combined blob and the two degenerate cases.
func canonFixtures() []string {
	return []string{
		"",
		"\x00\x01\x02\xff\xfe",
		volatileFixture,
		"a\r\nb\r\r\nc\rd",
		"\x1b[0\x1b[0mm\x1b]0;t\x07\x1b",
		"2024-01-02 03:04:05.123 [1700000000000] 12:34:56 Mon, 02 Jan 2024 03:04:05 GMT",
		"1h2m3s 1m30.5s 12ms 0.02s 5 s 3ns 5s finished in 250ms",
		"pid=41235 PID: 990 pid=41 pid=7 process 4242\n[12345] boot",
		"0xc000123456 Object@1a2b3c localhost:8080 127.0.0.1:54321 [::1]:9000",
		"/tmp/a/b /var/folders/qx/z/T/g $TMPDIR/s C:\\Users\\q\\AppData\\Local\\Temp\\t",
		"a  \rb  \r\nc  \n",
		"ok  \tpkg\t0.412s\n--- PASS: T (0.03s)\ngoroutine 1 [running]:\nTime:  3.456 s\n" +
			"===== 3 passed in 0.12s =====\nUsing --randomly-seed=1739481922\n[gw3] PASSED\n" +
			"    Finished dev target(s) in 0.53s\n",
		"src\\a.ts:12:a\\b\nC:\\x\\y.ts:3:c\nplain\\line\n",
		"a\\b\\c  \nd/e\n",
		"\xef\xbb\xbf\xef\xbb\xbf12\tcontent  \n",
		"?utm_a=1&utm_b=2&sid=abc123&cb=7 W/\"abcd\" nonce=\"aGVsbG8xMjM0\"",
		"commit 0123456789abcdef0123456789abcdef01234567\nindex 1234567..89abcde 100644\n@@ -1,7 +1,9 @@\n",
	}
}

// maxPropBytes bounds the property-test inputs. It is small on purpose: every property here is
// about the SHAPE of a rewrite, not about volume, and a short input shrinks to a readable
// counterexample.
const maxPropBytes = 96

// volatileAlphabet is the byte set the property tests draw from.
//
// A uniform rapid.Byte() generator essentially never produces an ESC introducer, a CR/LF pair, a
// "pid=" prefix or a run of ten digits, so it would explore none of the fourteen rule sets and the
// idempotence property would pass vacuously. Drawing from the bytes the rules actually key on —
// including the BOM bytes and both ANSI introducers — is what makes the property a real test. The
// uniform generator is kept alongside it for the bytes no rule expects.
var volatileAlphabet = []byte("\r\n\t \x1b\x07\xef\xbb\xbf[]<>\"'&?=@:;./\\-+_$%=0123456789" +
	"abcdefghimnopqrstuvwxyzABCDEFGIJLMNOPRSTUW")

// canonInput is the generator TestEveryCanonicalizer_Idempotent and its siblings draw from.
func canonInput() *rapid.Generator[[]byte] {
	return rapid.OneOf(
		rapid.SliceOfN(rapid.SampledFrom(volatileAlphabet), 0, maxPropBytes),
		rapid.SliceOfN(rapid.Byte(), 0, maxPropBytes),
	)
}

// TestEveryCanonicalizer_Idempotent is 00-ARCHITECTURE.md §5.6's central requirement:
// Canonicalize(Canonicalize(x)) == Canonicalize(x), byte for byte.
//
// It is a property rather than a table because idempotence is what makes canonicalization safe to
// re-run — the store canonicalizes on write and the replay harness canonicalizes again on read,
// and a rule that converges only on the second pass would make two identical inputs hash
// differently depending on how many times they had been through.
func TestEveryCanonicalizer_Idempotent(t *testing.T) {
	for _, c := range allCanonicalizers() {
		t.Run(c.Name(), func(t *testing.T) {
			for _, in := range canonFixtures() {
				once, err := c.Canonicalize([]byte(in), Options{KeepDeltas: true})
				require.NoError(t, err)
				twice, err := c.Canonicalize(once.Canonical, Options{KeepDeltas: true})
				require.NoError(t, err)
				require.Equal(t, string(once.Canonical), string(twice.Canonical), "fixture %q", in)
			}
			rapid.Check(t, func(rt *rapid.T) {
				in := canonInput().Draw(rt, "in")
				once, err := c.Canonicalize(in, Options{})
				if err != nil {
					rt.Fatalf("Canonicalize: %v", err)
				}
				twice, err := c.Canonicalize(once.Canonical, Options{})
				if err != nil {
					rt.Fatalf("Canonicalize: %v", err)
				}
				if string(once.Canonical) != string(twice.Canonical) {
					rt.Fatalf("not idempotent:\n  in     %q\n  once   %q\n  twice  %q",
						in, once.Canonical, twice.Canonical)
				}
			})
		})
	}
}

// TestEveryCanonicalizer_NeverGrows pins 00-ARCHITECTURE.md §5.6's no-growth property. It holds
// structurally — acceptCandidates rejects any Match whose Token is longer than its span — which is
// exactly why several rules can carry a token longer than their shortest possible capture without
// that being a bug; see the GUARD RELIANCE notes on durationRules, pidRules, testRunnerRules and
// webFetchRules.
func TestEveryCanonicalizer_NeverGrows(t *testing.T) {
	for _, c := range allCanonicalizers() {
		t.Run(c.Name(), func(t *testing.T) {
			for _, in := range canonFixtures() {
				got, err := c.Canonicalize([]byte(in), Options{})
				require.NoError(t, err)
				require.LessOrEqual(t, len(got.Canonical), len(in), "fixture %q", in)
			}
			rapid.Check(t, func(rt *rapid.T) {
				in := canonInput().Draw(rt, "in")
				got, err := c.Canonicalize(in, Options{})
				if err != nil {
					rt.Fatalf("Canonicalize: %v", err)
				}
				if len(got.Canonical) > len(in) {
					rt.Fatalf("grew %d -> %d for %q", len(in), len(got.Canonical), in)
				}
			})
		})
	}
}

// TestEveryCanonicalizer_MatchesCarryKnownClass guards the failure mode that is otherwise
// invisible: acceptCandidates DROPS a Match carrying a Class outside the gate, and the zero Class
// is never in the gate, so a rule that forgot its Class would compile, run, emit matches and
// silently do nothing at all.
func TestEveryCanonicalizer_MatchesCarryKnownClass(t *testing.T) {
	known := make(map[Class]bool)
	for _, cl := range KnownClasses() {
		known[cl] = true
	}
	for _, c := range allCanonicalizers() {
		t.Run(c.Name(), func(t *testing.T) {
			for _, in := range canonFixtures() {
				ms, err := MatchesOf(c, []byte(in), Options{})
				require.NoError(t, err, "every built-in must implement Matcher")
				for _, m := range ms {
					require.NotEqual(t, Class(""), m.Class, "zero Class in %q", in)
					require.True(t, known[m.Class], "unknown Class %q in %q", m.Class, in)
				}
			}
		})
	}
}

// TestEveryCanonicalizer_MatchesInBounds pins Matcher's "must not return spans outside in".
// applyMatches slices the input with these coordinates directly, so an out-of-range Match is a
// panic on the PostToolUse hot path rather than a wrong answer.
func TestEveryCanonicalizer_MatchesInBounds(t *testing.T) {
	for _, c := range allCanonicalizers() {
		t.Run(c.Name(), func(t *testing.T) {
			check := func(in []byte, fail func(format string, args ...any)) {
				ms, err := MatchesOf(c, in, Options{})
				if err != nil {
					fail("MatchesOf: %v", err)
					return
				}
				for _, m := range ms {
					if m.Offset < 0 || m.Len < 0 || m.End() > len(in) {
						fail("match %+v out of bounds for %d bytes: %q", m, len(in), in)
					}
				}
			}
			for _, in := range canonFixtures() {
				check([]byte(in), func(format string, args ...any) { t.Fatalf(format, args...) })
			}
			rapid.Check(t, func(rt *rapid.T) {
				check(canonInput().Draw(rt, "in"), rt.Fatalf)
			})
		})
	}
}

// TestEveryCanonicalizer_AppliesIsPure pins that Applies is a pure function of (tool, path).
// Registry.For calls it once per registered canonicalizer per tool call and callers cache nothing,
// so an Applies that depended on hidden state — a package-level counter, the filesystem, a clock —
// would make dispatch non-deterministic and the store's contents depend on call order.
func TestEveryCanonicalizer_AppliesIsPure(t *testing.T) {
	probes := []struct{ tool, path string }{
		{"Bash", "out.txt"},
		{"bash", ""},
		{"PowerShell", "a/b.ps1"},
		{"shell", "x"},
		{"Read", "internal/canon/generic.go"},
		{"FileWrite", "a.md"},
		{"Edit", "b.go"},
		{"Grep", "src"},
		{"Search", ""},
		{"Glob", "**/*.go"},
		{"LS", "."},
		{"WebFetch", "https://x.dev"},
		{"git", ""},
		{"", ""},
		{"Unknown", "z"},
	}
	for _, c := range allCanonicalizers() {
		t.Run(c.Name(), func(t *testing.T) {
			for _, p := range probes {
				first := c.Applies(p.tool, p.path)
				require.Equal(t, first, c.Applies(p.tool, p.path), "tool=%q path=%q", p.tool, p.path)
				require.Equal(t, first, c.Applies(p.tool, p.path), "tool=%q path=%q", p.tool, p.path)
			}
		})
	}
}

// TestEveryCanonicalizer_NameIsStable pins the fourteen names 00-ARCHITECTURE.md §5.6 fixes, in
// registration order. They are not free identifiers: Result.Applied reports them, Appendix C's
// store.canonicalize.strip shares their spelling for the six gateable classes, and
// Registry.Register rejects a duplicate — so a rename is a wire-format change.
func TestEveryCanonicalizer_NameIsStable(t *testing.T) {
	want := []string{
		"crlf", "ansi", "timestamps", "durations", "pids", "addresses", "tmpPaths",
		"bash", "testrunner", "grep", "glob", "fileread", "webfetch", "git",
	}
	got := make([]string, 0, len(want))
	for _, c := range allCanonicalizers() {
		got = append(got, c.Name())
	}
	require.Equal(t, want, got)
}

// TestTrailingWhitespace_CRSeparatedRuns is the regression rapid found, pinned on the canonicalizer
// where it actually bites.
//
// The shared trailing-whitespace rule was once a single regex, `(?m)([ \t](?:escAny|[ \t])*)\r*$`,
// replacing its capture group. That is not idempotent. Because the trailing carriage returns are
// matched as CONTEXT rather than as span — deliberately, so crlfCanon keeps its own Delta and
// LineEndingClass can still tell a CRLF payload from an LF one — deleting the LAST whitespace run
// promotes the CR to final byte, and the run BEFORE that CR, which was not trailing in the
// original, becomes trailing on the next pass. "\t\r\t" needed three passes to converge, violating
// 00-ARCHITECTURE.md §5.6.
//
// The rule is now a region scan that emits every whitespace-bearing run in the end-of-line tail in
// one pass. glob is the right canonicalizer to pin it on: it has no rule whose span can overlap
// this one, whereas bash's progress collapse starts at the line start and wins overlap resolution,
// which would hide the behaviour entirely.
func TestTrailingWhitespace_CRSeparatedRuns(t *testing.T) {
	runCanonCases(t, newGlob(), []canonCase{
		{
			name: "whitespace on both sides of a cr goes in one pass",
			in:   "\t\r\t", want: "\r", deltas: 2,
		},
		{
			// The same shape with content around it and more than one CR between the runs, so the
			// split-on-CR walk is exercised rather than a two-byte special case.
			name: "multiple cr-separated whitespace runs",
			in:   "a  \r\t\r  \nb", want: "a\r\r\nb", deltas: 3,
		},
		{
			// A tail of nothing but CRs must produce no match at all: emitting it would steal
			// crlfCanon's span and make LineEndingClass misreport a CRLF payload as LF.
			name: "cr-only tail is left entirely alone",
			in:   "a\r\r\nb", want: "a\r\r\nb", deltas: 0,
		},
		{
			// A tail of nothing but an escape sequence likewise belongs to ansiCanon, not here.
			name: "escape-only tail is left to ansi",
			in:   "a\x1b[0m\nb", want: "a\x1b[0m\nb", deltas: 0,
		},
		{
			// ...but an escape sitting among whitespace is inside the span, for the reason the
			// rule's own doc comment gives: leaving it out would let ansiCanon delete it in the
			// same pass and re-expose the space for the next one.
			name: "escape among whitespace stays inside the span",
			in:   "a \x1b[0m \r \nb", want: "a\r\nb", deltas: 2,
		},
	})
}
