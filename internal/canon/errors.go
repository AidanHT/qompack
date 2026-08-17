package canon

import "errors"

// The canon package sentinels. Every failure this package reports wraps one of these with %w, so
// a caller can branch on meaning with errors.Is rather than on a message.
var (
	// ErrUnknownClass is reported by Registry.Run when Options.Strip names a Class that is not one
	// of KnownClasses. It is deliberately a hard failure rather than a silent skip: a typo in
	// store.canonicalize.strip that quietly disabled a canonicalizer would show up only as a
	// mysteriously worse dedup ratio months later.
	ErrUnknownClass = errors.New("canon: unknown canonicalizer class")
	// ErrNotMatcher is reported by MatchesOf when a Canonicalizer does not also implement Matcher
	// and therefore cannot take part in Registry.Run's single-pass match composition.
	//
	// It is NOT reported by Registry.Register. The conformance suite in canontest registers a
	// plain Canonicalizer and requires Register to succeed, and its shape block then calls Run on
	// that same Registry and tolerates only core's four sentinels — so refusing at registration,
	// or failing Run because a non-Matcher is present, would both break the suite SP-01 froze
	// (Rule W-3). Registration accepts any Canonicalizer; Run composes matches from the Matchers
	// among them, and a non-Matcher contributes nothing and never appears in Result.Applied.
	// Every canonicalizer Default registers is a Matcher, which TestDefault_EveryBuiltinIsMatcher
	// pins directly.
	ErrNotMatcher = errors.New("canon: canonicalizer does not implement Matcher")
	// ErrDuplicateName is reported by Registry.Register when a Canonicalizer's Name is already
	// registered (00-ARCHITECTURE.md §5.6: "error on duplicate Name").
	ErrDuplicateName = errors.New("canon: duplicate canonicalizer name")
	// ErrDeltaRange is reported by Restore when a Delta's Offset or Len falls outside the
	// canonical buffer.
	ErrDeltaRange = errors.New("canon: delta out of range")
	// ErrDeltaOrder is reported by Restore when a Delta list is not in ascending Offset order.
	ErrDeltaOrder = errors.New("canon: deltas out of order")
)
