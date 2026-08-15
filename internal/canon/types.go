package canon

import "github.com/qompack/qompack/internal/sketch"

// Class names one category of volatile content a Canonicalizer strips (00-ARCHITECTURE.md §5.6).
type Class string

// The eight canonicalizer classes, matching internal/config's own
// store.canonicalize.strip enum spelling exactly.
const (
	// ClassTimestamps covers wall-clock timestamps.
	ClassTimestamps Class = "timestamps"
	// ClassANSI covers ANSI escape/color codes.
	ClassANSI Class = "ansi"
	// ClassPIDs covers process IDs.
	ClassPIDs Class = "pids"
	// ClassAddresses covers memory addresses and similar hex pointers.
	ClassAddresses Class = "addresses"
	// ClassTmpPaths covers volatile temp-directory paths.
	ClassTmpPaths Class = "tmpPaths"
	// ClassDurations covers elapsed-time / duration values.
	ClassDurations Class = "durations"
	// ClassCRLF covers CRLF-to-LF line-ending normalization (00-ARCHITECTURE.md §4).
	ClassCRLF Class = "crlf"
	// ClassPaths covers path normalization (00-ARCHITECTURE.md §4, paths.Norm/paths.Key).
	ClassPaths Class = "paths"
)

// Delta is one volatile span a Canonicalizer stripped: where it was, what it originally said, and
// which Class stripped it. Deltas are the byte-exact record Restore replays to undo
// canonicalization.
type Delta struct {
	// Offset is the span's byte offset in the CANONICAL (post-strip) output.
	Offset int
	// Len is the span's length in bytes in the canonical output.
	Len int
	// Original is the original text the span replaced.
	Original string
	// Class names which canonicalizer produced this Delta.
	Class Class
}

// Result is one Canonicalizer's (or the Registry's) output.
type Result struct {
	// Canonical is the canonicalized content.
	Canonical []byte
	// Deltas is the volatile side record of every span stripped; empty when Options.KeepDeltas is
	// false.
	Deltas []Delta
	// Applied lists every canonicalizer that ran, in application order.
	Applied []string
	// Signature is the MinHash signature over canonical shingles, for near-duplicate detection.
	Signature sketch.Signature
	// Reduced is 1 - len(Canonical)/len(input): the fraction of the input removed.
	Reduced float64
}

// Options configures one canonicalization pass.
type Options struct {
	// Strip lists which classes to strip; nil means every registered canonicalizer's default.
	Strip []Class
	// KeepDeltas requests that Result.Deltas be populated, so Restore can later undo the pass.
	KeepDeltas bool
	// MinHash configures the near-duplicate signature computed over the canonical result.
	MinHash sketch.MinHashOptions
}
