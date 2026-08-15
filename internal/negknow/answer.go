package negknow

// AnswerState is Query's three-way response (00-ARCHITECTURE.md §5.10, §8.3): whether a
// target/approach has never been tried (AnswerAbsent), was tried and is still believed to hold
// (AnswerActive), or was tried but a dependency has since changed (AnswerStale).
type AnswerState uint8

const (
	// AnswerAbsent means no elimination record matches, and the bloom does not claim one either.
	AnswerAbsent AnswerState = iota
	// AnswerActive means a matching elimination record exists and is still StatusActive.
	AnswerActive
	// AnswerStale means a matching elimination record exists but is StatusStale: a dependency
	// changed since it was recorded, so re-verification may be warranted.
	AnswerStale
)

// Answer is Query's result (00-ARCHITECTURE.md §5.10).
type Answer struct {
	State AnswerState
	// Record is the matching elimination, or nil when State is AnswerAbsent, or when a bloom hit
	// has no backing record (BloomOnly).
	Record *Record
	// Note is a human-readable elaboration, e.g. for AnswerStale: "previously eliminated, but the
	// evidence has changed since — re-verification may be warranted".
	Note string
	// BloomOnly is true when tried.bloom claimed a match but no backing Record was found — a
	// possible bloom false positive. Per §13 invariant 3, the bloom is a cache, never the source
	// of truth: every membership answer must be backed by a record lookup or explicitly flagged
	// BloomOnly, which is what this field is for.
	BloomOnly bool
}
