package core

import "errors"

// The sentinel errors. Every package reuses these rather than defining synonyms, so that a
// caller can branch on meaning without knowing which layer produced the failure.
var (
	// ErrNotImplemented is returned by every SP-01 interface stub. Conformance suites probe for
	// it to decide whether to run their behaviour block (Rule W-1).
	ErrNotImplemented = errors.New("qompack: not implemented")
	// ErrNotFound covers a missing object, record, or unparseable reference.
	ErrNotFound = errors.New("qompack: not found")
	// ErrAppendOnly is the §7.4 invariant: checkpoints/, pins/ and sketches/tried.bloom are
	// additive-only and cannot be truncated or rewritten through any code path in this repo.
	ErrAppendOnly = errors.New("qompack: append-only violation")
	// ErrAlreadyEncoded is the DPI guard of §4.6: a segment may be encoded into a checkpoint
	// exactly once, from the original on disk.
	ErrAlreadyEncoded = errors.New("qompack: segment already encoded (DPI guard)")
	// ErrBudget signals a token, byte, or latency budget was exceeded.
	ErrBudget = errors.New("qompack: budget exceeded")
	// ErrDegraded signals the session is running in degraded-passive mode (§12).
	ErrDegraded = errors.New("qompack: running in degraded mode")
	// ErrContract signals a host hook-contract violation (G9.3, §12.1).
	ErrContract = errors.New("qompack: host contract violated")
)

// IsNotImplemented reports whether err is, or wraps, ErrNotImplemented. Conformance suites and
// the build-order guards in test/guards use it to tell a stub from a real implementation, so it
// deliberately matches only the sentinel — never a look-alike message.
func IsNotImplemented(err error) bool { return errors.Is(err, ErrNotImplemented) }
