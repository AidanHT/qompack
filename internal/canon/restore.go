package canon

import "fmt"

// restoreHeadroom is the slack Restore preallocates beyond len(canonical). Every Delta replaces a
// token with something at least as long, so the reconstruction is never shorter than the
// canonical form; this is a first guess at the overshoot, not a bound.
const restoreHeadroom = 64

// Restore applies deltas to canonical to reconstruct the original, pre-canonicalization bytes: a
// byte-exact inverse of Canonicalize when Options.KeepDeltas was set (00-ARCHITECTURE.md §5.6).
//
// It is a single forward pass, which is the whole reason Registry.Run composes matches over the
// original input rather than chaining byte transforms: every Delta.Offset is in canonical
// coordinates and the list is ascending and non-overlapping, so Restore never has to know which
// canonicalizer produced which span, or in what order they ran.
//
// Restore never panics on adversarial input — FuzzRestore asserts that over arbitrary byte slices
// and arbitrary Delta lists. A malformed list is reported: ErrDeltaRange for an offset or length
// outside canonical, ErrDeltaOrder for a non-monotonic list. Both wrap with %w so a caller can
// errors.Is them; SP-06 treats either as a corrupt side record rather than as a missing object.
func Restore(canonical []byte, deltas []Delta) ([]byte, error) {
	if len(deltas) == 0 {
		// A copy, never the caller's backing array: Restore's result outlives the canonical buffer
		// in SP-06's read path, where the canonical bytes come straight out of a pooled decompress
		// buffer that is reused for the next object.
		return append([]byte(nil), canonical...), nil
	}

	out := make([]byte, 0, len(canonical)+restoreHeadroom)
	prev := 0
	for i, d := range deltas {
		if i > 0 && d.Offset < deltas[i-1].Offset {
			return nil, fmt.Errorf("%w: delta %d offset %d precedes delta %d offset %d",
				ErrDeltaOrder, i, d.Offset, i-1, deltas[i-1].Offset)
		}
		if d.Offset < prev || d.Len < 0 || d.Offset < 0 || d.Offset+d.Len > len(canonical) {
			return nil, fmt.Errorf("%w: delta %d offset=%d len=%d canonical=%d",
				ErrDeltaRange, i, d.Offset, d.Len, len(canonical))
		}
		out = append(out, canonical[prev:d.Offset]...)
		out = append(out, d.Original...)
		prev = d.Offset + d.Len
	}
	return append(out, canonical[prev:]...), nil
}

// LineEndingClass reports the original line-ending class of a canonicalized payload: "lf", "crlf"
// or "mixed". canonicalNewlines is bytes.Count(canonical, "\n").
//
// 00-ARCHITECTURE.md §4 requires the original line-ending class to be recorded as a canon.Delta,
// which the crlf canonicalizer does. This function is how a caller reads it back without a second
// scan over the payload, and without SP-04 adding a field to the §5.6 Result struct (which it may
// not do — that would need an arch amendment).
//
// It counts only Deltas whose Class is ClassCRLF AND whose Original is a run of one or more
// carriage returns terminated by a newline. Two filters are doing work there.
//
// ClassCRLF also carries the always-on trailing-whitespace rule that bash, grep, glob and fileread
// share — trailing whitespace is line-terminator normalization by the same argument as CRLF→LF —
// and counting those as line endings would report "mixed" for a pure-LF file that merely had a few
// trailing spaces. Those Deltas never end in a newline, so the suffix test excludes them.
//
// The run, rather than the exact two bytes "\r\n", is because the crlf canonicalizer collapses
// "\r+\n" and not just "\r\n": a terminal that overwrote a line and then ended it emits "\r\r\n",
// which is still a CRLF line ending with a cursor-return artefact in front of it. Counting it as
// LF would report "mixed" for a genuinely CRLF file — real `curl -v` output on Windows does
// exactly this.
func LineEndingClass(deltas []Delta, canonicalNewlines int) string {
	crlfCount := 0
	for _, d := range deltas {
		if d.Class == ClassCRLF && isCRRunLineEnding(d.Original) {
			crlfCount++
		}
	}
	switch {
	case crlfCount == 0:
		return "lf"
	case crlfCount == canonicalNewlines:
		return "crlf"
	default:
		return "mixed"
	}
}

// isCRRunLineEnding reports whether s is one or more '\r' followed by exactly one '\n' — the shape
// the crlf canonicalizer replaces with a bare "\n".
func isCRRunLineEnding(s string) bool {
	if len(s) < 2 || s[len(s)-1] != '\n' {
		return false
	}
	for i := 0; i < len(s)-1; i++ {
		if s[i] != '\r' {
			return false
		}
	}
	return true
}
