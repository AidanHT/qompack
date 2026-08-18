package canon

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
)

// canonCase is one row of a canonicalizer table test.
type canonCase struct {
	// name is the subtest name.
	name string
	// in is the raw input.
	in string
	// want is the expected canonical output.
	want string
	// wantDeltas, when non-nil, is compared structurally with go-cmp; it pins the Class and the
	// canonical-coordinate Offset of every stripped span, not just how many there were.
	wantDeltas []Delta
	// deltas is the expected Delta count, asserted when wantDeltas is nil.
	deltas int
}

// runCanonCases drives one canonicalizer over a table, asserting four things per row: the exact
// canonical bytes, the Delta record, that Restore reconstructs the ORIGINAL byte for byte, and
// that a second pass over the canonical output is a no-op.
//
// The round-trip and the idempotence check are run on every row of every table rather than in
// tests of their own because they are the two properties 00-ARCHITECTURE.md §5.6 states about
// canonicalization itself, and a rule that satisfies its table while breaking either of them is
// still broken. Every row runs with a nil Options.Strip, which classes.go defines as "every class
// in play" — a table row is about what a rule DOES, and gating is tested where it lives.
func runCanonCases(t *testing.T, c Canonicalizer, cases []canonCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.Canonicalize([]byte(tc.in), Options{KeepDeltas: true})
			require.NoError(t, err)
			require.Equal(t, tc.want, string(got.Canonical))
			if tc.wantDeltas != nil {
				require.Empty(t, cmp.Diff(tc.wantDeltas, got.Deltas))
			} else {
				require.Len(t, got.Deltas, tc.deltas)
			}
			restored, err := Restore(got.Canonical, got.Deltas)
			require.NoError(t, err)
			require.Equal(t, tc.in, string(restored),
				"Restore(Canonical, Deltas) must reproduce the input byte for byte")

			again, err := c.Canonicalize(got.Canonical, Options{KeepDeltas: true})
			require.NoError(t, err)
			require.Equal(t, string(got.Canonical), string(again.Canonical),
				"Canonicalize(Canonicalize(x)) must equal Canonicalize(x)")
		})
	}
}

// TestCRLF_Table pins CRLF→LF normalization, the one canonicalization 00-ARCHITECTURE.md §4 makes
// a precondition of cross-platform dedup.
func TestCRLF_Table(t *testing.T) {
	runCanonCases(t, newCRLF(), []canonCase{
		{
			name: "crlf pairs become lf",
			in:   "a\r\nb\r\n",
			want: "a\nb\n",
			wantDeltas: []Delta{
				{Offset: 1, Len: 1, Original: "\r\n", Class: ClassCRLF},
				{Offset: 3, Len: 1, Original: "\r\n", Class: ClassCRLF},
			},
		},
		{
			// A lone CR is a cursor return, not a line ending: collapsing what it overwrote is
			// bashCanon's job, and it classifies the result as ClassANSI because that is what a
			// carriage-return overwrite is.
			name: "lone cr is left for bash",
			in:   "a\rb",
			want: "a\rb",
		},
		{name: "already lf", in: "a\nb", want: "a\nb"},
		{
			// The RUN, not the pair. Replacing only "\r\n" here would yield "a\r\nb" — a fresh
			// CRLF that a second pass would rewrite again.
			name:       "cr run collapses in one pass",
			in:         "a\r\r\nb",
			want:       "a\nb",
			wantDeltas: []Delta{{Offset: 1, Len: 1, Original: "\r\r\n", Class: ClassCRLF}},
		},
		{name: "empty", in: "", want: ""},
	})
}

// TestANSI_Table pins escape-sequence stripping, including the residual-ESC rule that makes it
// idempotent.
func TestANSI_Table(t *testing.T) {
	runCanonCases(t, newANSI(), []canonCase{
		{name: "csi sgr pair", in: "\x1b[31mred\x1b[0m", want: "red", deltas: 2},
		{name: "csi erase line", in: "a\x1b[2Kb", want: "ab", deltas: 1},
		{name: "osc terminated by bel", in: "\x1b]0;title\x07x", want: "x", deltas: 1},
		{name: "osc terminated by st", in: "\x1b]0;t\x1b\\y", want: "y", deltas: 1},
		{
			// ESC ']' is inside the two-byte rule's 0x5c-0x5f range, so an OSC that never
			// terminates loses its introducer instead of surviving whole.
			name: "unterminated osc keeps its body", in: "\x1b]abc", want: "abc", deltas: 1,
		},
		{name: "two-byte escape", in: "a\x1bMb", want: "ab", deltas: 1},
		{
			// The idempotence case the residual-ESC rule exists for. "\x1b[0" is not a CSI
			// sequence, so without that rule the inner "\x1b[0m" would be deleted first and the
			// remainder would splice into a fresh "\x1b[0m" for the next pass to find.
			name: "deletion cannot splice a new csi", in: "\x1b[0\x1b[0mm", want: "[0m", deltas: 2,
		},
		{name: "stray esc", in: "a\x1bb", want: "ab", deltas: 1},
		{name: "no escapes", in: "plain text", want: "plain text"},
	})
}

// TestTimestamps_Table covers all five timestamp shapes; the sixth row is the negative control.
func TestTimestamps_Table(t *testing.T) {
	runCanonCases(t, newTimestamps(), []canonCase{
		{name: "iso with numeric offset", in: "2024-01-02T03:04:05+02:00 boot", want: "<ts> boot", deltas: 1},
		{
			// The bare-clock rule also matches, nine bytes in; acceptCandidates rejects it because
			// the ISO match starts earlier and already covers it.
			name: "iso with z and nanoseconds", in: "2024-01-02T03:04:05.123456789Z", want: "<ts>", deltas: 1,
		},
		{name: "rfc 1123 http date", in: "Date: Mon, 02 Jan 2024 03:04:05 GMT", want: "Date: <ts>", deltas: 1},
		{name: "syslog", in: "Jan  2 15:04:05 host sshd", want: "<ts> host sshd", deltas: 1},
		{name: "epoch millis in brackets", in: "[1700000000000] start", want: "[<ts>] start", deltas: 1},
		{
			// Two epochs separated by one space. The framed spelling of this rule would replace one
			// per pass, because the match consumed the delimiter the next match needed.
			name: "adjacent epochs both go in one pass", in: "1700000000000 1700000000001",
			want: "<ts> <ts>", deltas: 2,
		},
		{name: "bare clock", in: "12:34:56 ready", want: "<ts> ready", deltas: 1},
		{
			// SP04-D3(c). The ISO fraction is unbounded, so a ten-digit one is part of the
			// timestamp rather than left outside it for the epoch rule to claim as a second token.
			// Capped at nine this was "<ts>.<ts>", which spelled one instant two ways depending on
			// how much precision the writer printed.
			name: "over-long fraction is one timestamp", in: "2024-01-02T03:04:05.1234567890",
			want: "<ts>", deltas: 1,
		},
		{name: "digits inside a longer run are not a clock", in: "id17000000000001x", want: "id17000000000001x"},
		{name: "no timestamp", in: "nothing here", want: "nothing here"},
	})
}

// TestDurations_Table covers the composite, simple and phrase forms, and names the one place this
// canonicalizer relies on the registry's non-growing guard.
func TestDurations_Table(t *testing.T) {
	runCanonCases(t, newDurations(), []canonCase{
		{name: "go composite minutes", in: "1m30.5s", want: "<d>", deltas: 1},
		{name: "go composite hours", in: "1h2m3s", want: "<d>", deltas: 1},
		{name: "millis", in: "took 12ms", want: "took <d>", deltas: 1},
		{name: "fractional seconds", in: "took 0.02s", want: "took <d>", deltas: 1},
		{name: "space before unit", in: "took 5 s", want: "took <d>", deltas: 1},
		{
			// SP04-D3(a). The separator is horizontal whitespace, so a magnitude ending one line
			// does not pair with a unit beginning the next — which is what a wrapped log line
			// looks like, and never what one duration looks like.
			name: "a line break does not separate a magnitude from a unit", in: "in 5\nms",
			want: "in 5\nms",
		},
		{name: "nanos", in: "3ns", want: "<d>", deltas: 1},
		{
			// The phrase rule and the simple rule report the identical span here; overlap
			// resolution keeps one of them, so this is one Delta and not two.
			name: "in phrase", in: "finished in 250ms", want: "finished in <d>", deltas: 1,
		},
		{
			// GUARD RELIANCE. "5s" and "9m" are two-byte spans and tokenDuration is three bytes, so
			// acceptCandidates drops these matches rather than growing the input
			// (00-ARCHITECTURE.md §5.6: no canonicalizer ever grows its input). The rule still
			// EMITS them — gating and non-growth are the registry's job, not the Matcher's — which
			// is why the assertion here is on the canonical bytes rather than on Matches.
			name: "two-byte duration survives the non-growing guard", in: "took 5s then 9m",
			want: "took 5s then 9m",
		},
		{name: "no duration", in: "seconds elapsed", want: "seconds elapsed"},
	})
}

// TestPIDs_Table covers the three pid spellings and is where the reliance on the registry's
// non-growing guard for short numbers is named.
func TestPIDs_Table(t *testing.T) {
	runCanonCases(t, newPIDs(), []canonCase{
		{name: "pid equals", in: "pid=41235 up", want: "pid=<n> up", deltas: 1},
		{name: "pid colon uppercase", in: "PID: 990", want: "PID: <n>", deltas: 1},
		{name: "bracketed at line start", in: "[12345] boot", want: "[<n>] boot", deltas: 1},
		{name: "process word", in: "process 4242 exited", want: "process <n> exited", deltas: 1},
		{
			// Below the rule's own \d{2,7} floor: a single digit never even matches.
			name: "single-digit pid does not match", in: "pid=7", want: "pid=7",
		},
		{
			// GUARD RELIANCE, and the canonical example of it. "41" is a two-byte capture and
			// tokenNumber is three bytes, so acceptCandidates rejects the Match rather than let
			// this canonicalizer grow its input. pidRules deliberately does not carry a shorter
			// token to dodge this: 00-ARCHITECTURE.md §5.6's no-growth property is enforced once,
			// structurally, in the registry, instead of being re-derived in fourteen Matchers.
			name: "two-digit pid survives the non-growing guard", in: "pid=41", want: "pid=41",
		},
		{name: "no pid", in: "no numbers of interest", want: "no numbers of interest"},
	})
}

// TestAddresses_Table covers hex pointers, '@' identities and loopback ports.
func TestAddresses_Table(t *testing.T) {
	runCanonCases(t, newAddresses(), []canonCase{
		{name: "hex pointer", in: "panic at 0xc000123456", want: "panic at <addr>", deltas: 1},
		{name: "at-identity", in: "Object@1a2b3c done", want: "Object<addr> done", deltas: 1},
		{name: "localhost port", in: "listening on localhost:8080", want: "listening on localhost:<p>", deltas: 1},
		{name: "loopback v4 port", in: "http://127.0.0.1:54321/x", want: "http://127.0.0.1:<p>/x", deltas: 1},
		{
			// The '[' of "[::1]" is not a word byte, so a word boundary in FRONT of the host
			// alternation could never match this line; the boundary is distributed into the three
			// alternatives that start with one instead.
			name: "loopback v6 port", in: "[::1]:9000 ready", want: "[::1]:<p> ready", deltas: 1,
		},
		{name: "short hex constant is not an address", in: "mask 0xff", want: "mask 0xff"},
		{name: "no address", in: "nothing here", want: "nothing here"},
	})
}

// TestTmpPaths_Table covers all five temp-root layouts, which exist so a Linux CI run and a
// Windows developer run of the same suite canonicalize to the same bytes (00-ARCHITECTURE.md §4).
func TestTmpPaths_Table(t *testing.T) {
	runCanonCases(t, newTmpPaths(), []canonCase{
		{name: "posix tmp", in: "wrote /tmp/qompack-123/out.txt ok", want: "wrote <tmp> ok", deltas: 1},
		{name: "macos var folders", in: "/var/folders/qx/abc123/T/go-build42", want: "<tmp>", deltas: 1},
		{
			name: "windows appdata temp backslash",
			in:   `C:\Users\quant\AppData\Local\Temp\qompack1`, want: "<tmp>", deltas: 1,
		},
		{
			name: "windows appdata temp forward slash",
			in:   "C:/Users/quant/AppData/Local/Temp/qompack1", want: "<tmp>", deltas: 1,
		},
		{
			// SP04-D1. The same path once a tool has embedded it in a JSON payload, which is how
			// every hook transcript and every docker/go-build log carries a Windows path. The
			// doubled separators are matched by a rule of their own; the first rule's `[^\\]+`
			// user-name segment cannot cross one, which is exactly why it is a second rule.
			name: "windows appdata temp json-escaped",
			in:   `{"cwd":"C:\\Users\\quant\\AppData\\Local\\Temp\\qompack1"}`,
			want: `{"cwd":"<tmp>"}`, deltas: 1,
		},
		{
			// The case the fix had to be shaped around: widening the user-name class to admit a
			// doubled backslash would let ONE match run from the first path to the end of the
			// second, replacing the comma and the JSON structure between them along with it.
			// Two separate spans is the answer, and it is what a per-path tail delivers.
			name: "two json-escaped paths on one line",
			in: `{"a":"C:\\Users\\quant\\AppData\\Local\\Temp\\one",` +
				`"b":"D:\\Users\\other\\AppData\\Local\\Temp\\two"}`,
			want: `{"a":"<tmp>","b":"<tmp>"}`, deltas: 2,
		},
		{
			// A doubled separator in the TAIL is ordinary content for both rules: the tail class is
			// a complement, so it admits backslashes and stops at the quote either way.
			name: "escaped tail keeps its own separators",
			in:   `"C:\\Users\\quant\\AppData\\Local\\Temp\\a\\b\\c" done`,
			want: `"<tmp>" done`, deltas: 1,
		},
		{name: "unexpanded tmpdir", in: "$TMPDIR/qompack.sock", want: "<tmp>", deltas: 1},
		{name: "tmpfs is not tmp", in: "/tmpfs/x", want: "/tmpfs/x"},
		{name: "no temp path", in: "/usr/local/bin/qompack", want: "/usr/local/bin/qompack"},
	})
}
