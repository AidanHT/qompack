package canon_test

import (
	"testing"

	"github.com/qompack/qompack/internal/canon"
	"github.com/stretchr/testify/require"
)

// TestRestore_NoDeltas asserts the degenerate case returns a COPY, not the caller's backing array:
// SP-06's read path hands Restore a pooled decompression buffer that is reused for the next
// object, so aliasing it would corrupt the result the moment the next read happened.
func TestRestore_NoDeltas(t *testing.T) {
	t.Parallel()

	canonical := []byte("unchanged")
	got, err := canon.Restore(canonical, nil)
	require.NoError(t, err)
	require.Equal(t, canonical, got)

	canonical[0] = 'X'
	require.Equal(t, "unchanged", string(got), "Restore must not alias the canonical buffer")
}

// TestRestore_RoundTrip walks a hand-built delta list, so the inverse is pinned independently of
// whatever the registry happens to produce.
func TestRestore_RoundTrip(t *testing.T) {
	t.Parallel()

	// "a<ts>b<d>c" restores to "a2024-01-15T10:32:07Zb1.25sc".
	canonical := []byte("a<ts>b<d>c")
	deltas := []canon.Delta{
		{Offset: 1, Len: 4, Original: "2024-01-15T10:32:07Z", Class: canon.ClassTimestamps},
		{Offset: 6, Len: 3, Original: "1.25s", Class: canon.ClassDurations},
	}

	got, err := canon.Restore(canonical, deltas)
	require.NoError(t, err)
	require.Equal(t, "a2024-01-15T10:32:07Zb1.25sc", string(got))
}

// TestRestore_AdjacentDeltas asserts two deltas that touch (one ends exactly where the next
// begins) round-trip, since the accept loop permits abutting matches.
func TestRestore_AdjacentDeltas(t *testing.T) {
	t.Parallel()

	got, err := canon.Restore([]byte("<a><b>"), []canon.Delta{
		{Offset: 0, Len: 3, Original: "AAAA", Class: canon.ClassANSI},
		{Offset: 3, Len: 3, Original: "BBBB", Class: canon.ClassANSI},
	})
	require.NoError(t, err)
	require.Equal(t, "AAAABBBB", string(got))
}

// TestRestore_ZeroLengthDelta asserts a deletion — a Delta whose token was empty, so Len is 0 —
// reinserts its original at the right place.
func TestRestore_ZeroLengthDelta(t *testing.T) {
	t.Parallel()

	got, err := canon.Restore([]byte("ERR"), []canon.Delta{
		{Offset: 0, Len: 0, Original: "\x1b[31m", Class: canon.ClassANSI},
		{Offset: 3, Len: 0, Original: "\x1b[0m", Class: canon.ClassANSI},
	})
	require.NoError(t, err)
	require.Equal(t, "\x1b[31mERR\x1b[0m", string(got))
}

// TestRestore_Errors is the malformed-input table. Every case must report rather than panic or
// silently truncate: SP-06 treats either sentinel as a corrupt side record.
func TestRestore_Errors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		canonical string
		deltas    []canon.Delta
		wantErr   error
	}{
		{"offset_past_end", "0123456789", []canon.Delta{{Offset: 999, Len: 1}}, canon.ErrDeltaRange},
		{"len_past_end", "0123456789", []canon.Delta{{Offset: 8, Len: 5}}, canon.ErrDeltaRange},
		{"negative_len", "0123456789", []canon.Delta{{Offset: 0, Len: -1}}, canon.ErrDeltaRange},
		{"negative_offset", "0123456789", []canon.Delta{{Offset: -1, Len: 1}}, canon.ErrDeltaRange},
		{"unordered", "0123456789", []canon.Delta{{Offset: 6, Len: 1}, {Offset: 2, Len: 1}}, canon.ErrDeltaOrder},
		{"overlapping", "0123456789", []canon.Delta{{Offset: 0, Len: 4}, {Offset: 2, Len: 2}}, canon.ErrDeltaRange},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := canon.Restore([]byte(tc.canonical), tc.deltas)
			require.ErrorIs(t, err, tc.wantErr)
			require.Nil(t, got)
		})
	}
}

// TestLineEndingClass_Table pins 00-ARCHITECTURE.md §4's "the original line-ending class is
// recorded as a canon.Delta", read back without a second scan over the payload.
//
// The trailing-whitespace rows are the reason this function filters on Original == "\r\n" rather
// than on Class alone: bash, grep, glob and fileread all emit trailing-whitespace deltas under
// ClassCRLF, and counting those as line endings would report "mixed" for a pure-LF file that
// merely had a few trailing spaces.
func TestLineEndingClass_Table(t *testing.T) {
	t.Parallel()

	crlfDelta := canon.Delta{Original: "\r\n", Class: canon.ClassCRLF}
	// A terminal that overwrote a line and then ended it emits "\r\r\n"; the crlf canonicalizer
	// collapses the whole run, and it is still a CRLF line ending. Real `curl -v` output does this.
	crRunDelta := canon.Delta{Original: "\r\r\n", Class: canon.ClassCRLF}
	wsDelta := canon.Delta{Original: "   ", Class: canon.ClassCRLF}
	// A trailing-whitespace Delta that swallowed a bare CR still never ends in a newline.
	wsCRDelta := canon.Delta{Original: "  \r", Class: canon.ClassCRLF}
	tsDelta := canon.Delta{Original: "2024-01-15T10:32:07Z", Class: canon.ClassTimestamps}

	cases := []struct {
		name     string
		deltas   []canon.Delta
		newlines int
		want     string
	}{
		{"pure_lf", nil, 3, "lf"},
		{"pure_lf_with_trailing_whitespace", []canon.Delta{wsDelta, wsDelta}, 3, "lf"},
		{"pure_lf_with_trailing_whitespace_and_cr", []canon.Delta{wsCRDelta, wsCRDelta}, 3, "lf"},
		{"pure_crlf", []canon.Delta{crlfDelta, crlfDelta, crlfDelta}, 3, "crlf"},
		{"pure_crlf_with_other_classes", []canon.Delta{crlfDelta, tsDelta, crlfDelta, crlfDelta}, 3, "crlf"},
		{"cr_overwrite_runs_still_count_as_crlf", []canon.Delta{crRunDelta, crlfDelta, crRunDelta}, 3, "crlf"},
		{"mixed", []canon.Delta{crlfDelta}, 3, "mixed"},
		{"mixed_with_trailing_whitespace_noise", []canon.Delta{crlfDelta, wsDelta, crlfDelta}, 3, "mixed"},
		{"no_newlines_at_all", nil, 0, "lf"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, canon.LineEndingClass(tc.deltas, tc.newlines))
		})
	}
}
