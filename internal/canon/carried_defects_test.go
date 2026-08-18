package canon_test

import (
	"testing"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/config"
	"github.com/stretchr/testify/require"
)

// CHARACTERIZATION TESTS FOR CARRIED DEFECTS.
//
// Every test in this file asserts what canonicalization does TODAY, including where that is wrong.
// They are not approval of the behaviour: each row names a defect in plans/CARRIED-DEFECTS.tsv,
// and plans/V2-SP-04-carried-defects.md holds the diagnosis and the acceptance criteria.
//
// Pinning a known-wrong output looks perverse and is the point. A defect recorded only in prose is
// invisible to the build, so it survives by being forgotten, and the two ways it can be lost are
// symmetrical: someone can delete the note, or someone can fix the behaviour and leave the note
// claiming it is still broken. A characterization test closes both. It cannot be deleted without
// deleting a named test, and the moment the behaviour is fixed it FAILS — which is the signal that
// the manifest row should move to `fixed`, and the failure message says exactly that.
//
// So a failure here is not a regression. It is either the fix landing, in which case update the
// row and rewrite the assertion to the corrected output, or a genuine behaviour change nobody
// intended, in which case the defect note tells you what the output used to be and why.

// defectRegistry is the production registry these defects are observed through: Appendix C's
// defaults, deltas kept, exactly as SP-06 will call it.
func defectRegistry(t *testing.T) (canon.Registry, canon.Options) {
	t.Helper()
	cfg := config.Defaults().Store.Canonicalize
	return canon.Default(cfg), canon.OptionsFrom(cfg, true)
}

// runDefect canonicalizes in as Bash output and returns the canonical text, asserting the Restore
// inverse holds. Restore is asserted on every row because it is the property that bounds how bad
// any of these defects can be: a span that is wrongly canonicalized, or wrongly left alone, still
// round-trips, so the cost is always a dedup miss and never lost content.
func runDefect(t *testing.T, in string) string {
	t.Helper()
	r, o := defectRegistry(t)

	res, err := r.Run("Bash", "", []byte(in), o)
	require.NoError(t, err)

	restored, err := canon.Restore(res.Canonical, res.Deltas)
	require.NoError(t, err)
	require.Equal(t, in, string(restored), "Restore must stay exact regardless of the defect")

	return string(res.Canonical)
}

// TestCarriedDefect_SP04D1_EscapedTempPathIsStripped pins SP04-D1, now FIXED.
//
// tmpPathRules used to recognize a Windows temp path with SINGLE separators only. Its `[^\\]+`
// user-name segment cannot cross a doubled backslash, so the JSON-escaped spelling — what any tool
// produces once it has embedded a Windows path in a JSON payload — was passed through untouched.
// That cost twice over: the volatile path forked the dedup space exactly as an unescaped one would,
// and a real user name reached stored content, which is why testdata/corpora/toolout had to be
// sanitized before it could be committed.
//
// The escaped spelling now canonicalizes to the same <tmp> the plain one always did, through a
// SECOND rule rather than a looser first one — see tmpPathRules for why widening the user-name
// class was the wrong shape of fix. Both spellings are asserted in one test, because the defect was
// precisely that the two disagreed.
func TestCarriedDefect_SP04D1_EscapedTempPathIsStripped(t *testing.T) {
	plain := `C:\Users\alice\AppData\Local\Temp\build\x`
	require.Equal(t, "<tmp>", runDefect(t, plain),
		"the single-separator spelling was always stripped and must keep being stripped")

	escaped := `{"cwd":"C:\\Users\\alice\\AppData\\Local\\Temp\\build\\x"}`
	require.Equal(t, `{"cwd":"<tmp>"}`, runDefect(t, escaped),
		"SP04-D1: the JSON-escaped spelling strips to the same token as the plain one")

	// The user name is what made this a hygiene defect and not only a dedup one, so its absence
	// from the canonical text is asserted directly rather than inferred from the token.
	require.NotContains(t, runDefect(t, escaped), "alice",
		"a user name must not survive canonicalization into stored content")
}

// TestCarriedDefect_SP04D3_TimestampAndDurationEdges pins SP04-D3: three edge behaviours the
// hand-written numeric scanner reproduced faithfully from the regexes it replaced. Two of them
// were the patterns being wrong about the world and are now corrected; the third is not a pattern
// bug at all, and the assertion that still pins it says why.
//
// The scanner is never fixed alone: the reference regexes in numeric_test.go are the oracle for
// the agreement tests that make it trustworthy, so pattern and scanner move together, pattern
// first.
func TestCarriedDefect_SP04D3_TimestampAndDurationEdges(t *testing.T) {
	t.Run("a duration may not cross a line break", func(t *testing.T) {
		// FIXED. The separator between the magnitude and the unit was `\s?`, which admits '\n', so
		// a number ending one line paired with a unit beginning the next. It is horizontal
		// whitespace now, which is what a separator inside one value can be.
		require.Equal(t, "in 5\nms", runDefect(t, "in 5\nms"),
			"SP04-D3(a): a value on one line and a unit on the next are not one duration")

		require.Equal(t, "in <d>", runDefect(t, "in 5 ms"),
			"the same phrase on one line is still a duration, which is the contrast that shows "+
				"(a) was about the line break and not about the space")
	})

	t.Run("rfc3339 followed by a word byte strips nothing", func(t *testing.T) {
		// NOT FIXED, and not fixable while composition is one pass — this row is SP04-D2 wearing
		// different clothes, not an independent pattern bug.
		//
		// Producing "<ts>x" means dropping the ISO rule's trailing \b. That boundary is not
		// decoration: it is the word-boundary invariant documented above reRule in generic.go, and
		// the counter-example that put it there is "0000-00-00 00:00:00Zpid 0000". Without the \b
		// the first pass yields "<ts>pid 0000" — and the token's '>' is a NON-word byte, so `\bpid`
		// acquires on pass 2 the boundary it was denied on pass 1, and the second pass differs
		// from the first. The demonstration below is that step, run on today's rules.
		//
		// Stripping only the clock inside the timestamp fails identically and for the same reason:
		// its span would end immediately before the word byte 'Z', so "2024-01-15T10:32:07pid 1234"
		// would canonicalize to "2024-01-15T<ts>pid 1234" and then to "…<ts>pid <n>".
		//
		// Both readings are legal the moment composition runs to a FIXED POINT, because a
		// second-pass match is then folded into the same answer — which is the decision SP04-D2
		// records, and the reason this edge is deferred with it rather than separately.
		require.Equal(t, "2024-01-15T10:32:07Zx", runDefect(t, "2024-01-15T10:32:07Zx"),
			"SP04-D3(b): unchanged, because <ts>x is not reachable in one pass")

		require.Equal(t, "<ts> ok", runDefect(t, "2024-01-15T10:32:07Z ok"),
			"the same timestamp followed by a non-word byte is stripped, which is the contrast "+
				"that shows (b) is about the boundary and not about the timestamp")

		require.Equal(t, "<ts>pid <n>", runDefect(t, "<ts>pid 0000"),
			"THE OBSTACLE: this is what a rule dropping its trailing \\b would produce on the "+
				"SECOND pass over its own first-pass output. Canonicalize(Canonicalize(x)) would "+
				"stop equalling Canonicalize(x), which §5.6 forbids")
	})

	t.Run("an over-long fraction stays part of the timestamp", func(t *testing.T) {
		// FIXED. The ISO rule capped its fraction at nine digits, so a ten-digit one was left
		// outside the match and the bare 10-to-13-digit epoch rule claimed those digits. The cap
		// is gone; the two spans now overlap and acceptCandidates keeps the longer.
		require.Equal(t, "<ts>", runDefect(t, "2024-01-02T03:04:05.1234567890"),
			"SP04-D3(c): one timestamp is one token")
	})
}

// TestCarriedDefect_SP04D2_IsPinnedElsewhere records where SP04-D2 lives rather than duplicating
// it. Deletion-mediated non-idempotence is pinned by TestKnownDeletionMediatedLimit in
// golden_test.go, next to the FuzzCanonicalize assertion that carves out the exception, because
// the two have to be read together to make sense.
//
// This test exists so a search for the defect ID finds something in the package it affects.
func TestCarriedDefect_SP04D2_IsPinnedElsewhere(t *testing.T) {
	require.Equal(t, "<d>", runDefect(t, "0000s"),
		"the joined value a deletion can produce is an ordinary duration once it exists; "+
			"TestKnownDeletionMediatedLimit is what pins the two-pass path that reaches it")
}
