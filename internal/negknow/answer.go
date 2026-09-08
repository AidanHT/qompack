package negknow

import "github.com/qompack/qompack/internal/core"

// AnswerState is Query's response (00-ARCHITECTURE.md §5.10, §8.3, §11.3 Required invariants item
// 8): whether a target/approach has never been tried (AnswerAbsent), was tried and is still
// believed to hold (AnswerActive), was tried but a dependency has since changed (AnswerStale), or
// whether the ledger cannot currently back any of those with confidence (AnswerUnavailable,
// AnswerUncertain).
//
// AnswerUnavailable and AnswerUncertain are never a heuristic guess. They are what Query returns
// INSTEAD of asserting AnswerAbsent or AnswerActive when the ledger itself is degraded (blind
// mode), when a bloom hit resolves to a record this reader cannot classify, or when coverage or
// freshness could not be established (an unverified dependency, or a staleness detail
// configuration asked to suppress). AnswerActive, AnswerStale and AnswerAbsent stay reserved for a
// backed record lookup with known applicability; no amount of degradation may turn uncertainty
// into either an active prohibition or an assertion of absence.
type AnswerState uint8

const (
	// AnswerAbsent means a backed record lookup, with complete and fresh coverage, found no
	// matching elimination — the bloom does not claim one either.
	AnswerAbsent AnswerState = iota
	// AnswerActive means a matching elimination record exists and is still StatusActive, and its
	// dependency coverage is current.
	AnswerActive
	// AnswerStale means a matching elimination record exists but is StatusStale: a dependency
	// changed since it was recorded, so re-verification may be warranted.
	AnswerStale
	// AnswerUnavailable means the ledger itself could not be consulted at all — blind mode, or an
	// equivalent operational fault. The caller must treat this as neither absence nor
	// confirmation. Coverage carries the reason and the recovery direction.
	AnswerUnavailable
	// AnswerUncertain means the ledger answered, but coverage or freshness could not be
	// established with confidence: a bloom hit resolving to a record with an unrecognized status,
	// a stale record whose detail configuration suppresses, or an active record whose dependency
	// coverage the last refresh could not verify. Coverage carries the reason and the recovery
	// direction.
	AnswerUncertain
)

// Answer is Query's result (00-ARCHITECTURE.md §5.10).
type Answer struct {
	State AnswerState
	// Record is the matching elimination, or nil when State is AnswerAbsent or AnswerUnavailable,
	// when a bloom hit has no backing record (BloomOnly), or when a bloom hit resolves to a
	// record whose status this reader does not recognize. It is populated for AnswerActive and
	// AnswerStale, and for an AnswerUncertain produced by unverified dependency coverage — there
	// the record itself is still known and only its freshness is unconfirmed.
	Record *Record
	// Note is a human-readable elaboration, e.g. for AnswerStale: "previously eliminated, but the
	// evidence has changed since — re-verification may be warranted".
	Note string
	// BloomOnly is true when tried.bloom claimed a match but no backing Record was found — a
	// possible bloom false positive. Per §13 invariant 3, the bloom is a cache, never the source
	// of truth: every membership answer must be backed by a record lookup or explicitly flagged
	// BloomOnly, which is what this field is for.
	BloomOnly bool
	// Coverage explains WHY State is AnswerUnavailable or AnswerUncertain, and what the caller
	// should do about it: Reason is what could not be established, Recovery is the direction that
	// resolves it (00-ARCHITECTURE.md §11.3 Required invariants item 8). It reuses
	// internal/core's EvidenceEnvelope vocabulary (Omission's Reason/Recovery) rather than
	// inventing a second one, and is the zero core.Omission for AnswerAbsent, AnswerActive and
	// AnswerStale.
	Coverage core.Omission
}
