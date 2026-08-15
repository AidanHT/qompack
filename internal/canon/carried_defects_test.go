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

// TestCarriedDefect_SP04D1_EscapedTempPathIsNotStripped pins SP04-D1.
//
// tmpPathRules matches a Windows temp path with SINGLE separators. Its `[^\\]+` user-name segment
// cannot cross a doubled backslash, so the JSON-escaped spelling — what any tool produces once it
// has embedded a Windows path in a JSON payload — is passed through untouched. Two costs: the
// volatile path forks the dedup space exactly as an unescaped one would, and a real user name
// reaches stored content. The second is why testdata/corpora/toolout had to be sanitized.
//
// When fixed, the escaped input canonicalizes to the same <tmp> the plain input already does.
func TestCarriedDefect_SP04D1_EscapedTempPathIsNotStripped(t *testing.T) {
	plain := `C:\Users\alice\AppData\Local\Temp\build\x`
	require.Equal(t, "<tmp>", runDefect(t, plain),
		"the single-separator spelling is stripped today and must keep being stripped")

	escaped := `{"cwd":"C:\\Users\\alice\\AppData\\Local\\Temp\\build\\x"}`
	require.Equal(t, escaped, runDefect(t, escaped),
		"SP04-D1: the escaped spelling is NOT stripped today. If this now fails because the "+
			"canonical form is `{\"cwd\":\"<tmp>\"}`, the defect is fixed — mark SP04-D1 fixed in "+
			"plans/CARRIED-DEFECTS.tsv and replace this assertion with the corrected output")
}

// TestCarriedDefect_SP04D3_TimestampAndDurationEdges pins SP04-D3, three edge behaviours the
// hand-written numeric scanner reproduces faithfully from the regexes it replaced.
//
// All three are the scanner being CORRECT about the patterns and the patterns being wrong about
// the world, so none of them can be fixed in numeric.go alone: the reference regexes in
// numeric_test.go are the oracle, and changing one without the other breaks the agreement tests
// that make the scanner trustworthy. Fix the pattern and the scanner together, in that order.
func TestCarriedDefect_SP04D3_TimestampAndDurationEdges(t *testing.T) {
	t.Run("duration spans a line break", func(t *testing.T) {
		// `\s?` between the magnitude and the unit admits '\n', so a number ending one line pairs
		// with a unit beginning the next. Go's \s also excludes the vertical tab, so "5\fms" is a
		// duration and "5\vms" is not — an inconsistency inherited from the class, not chosen.
		require.Equal(t, "in <d>", runDefect(t, "in 5\nms"),
			"SP04-D3(a): should be left alone; a duration may not cross a newline")
	})

	t.Run("rfc3339 followed by a word byte strips nothing", func(t *testing.T) {
		// 'T' is a word byte, so the bare-clock rule's leading boundary cannot fire inside an
		// RFC-3339 string; when the ISO rule then fails its own trailing boundary there is no
		// fallback, and the whole timestamp survives.
		require.Equal(t, "2024-01-15T10:32:07Zx", runDefect(t, "2024-01-15T10:32:07Zx"),
			"SP04-D3(b): should be <ts>x, or at minimum the clock inside it should be stripped")

		require.Equal(t, "<ts> ok", runDefect(t, "2024-01-15T10:32:07Z ok"),
			"the same timestamp followed by a non-word byte is stripped, which is the contrast "+
				"that shows (b) is about the boundary and not about the timestamp")
	})

	t.Run("over-long fraction is re-matched as an epoch", func(t *testing.T) {
		// The ISO rule caps the fraction at nine digits, so a ten-digit one is left outside the
		// match — and the bare 10-to-13-digit epoch rule then claims those digits.
		require.Equal(t, "<ts>.<ts>", runDefect(t, "2024-01-02T03:04:05.1234567890"),
			"SP04-D3(c): should be a single <ts>")
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
