package canon

import "github.com/qompack/qompack/internal/core"

// Restore applies deltas to canonical to reconstruct the original, pre-canonicalization bytes: a
// byte-exact inverse of Canonicalize when Options.KeepDeltas was set (00-ARCHITECTURE.md §5.6).
// Restore always reports core.ErrNotImplemented until SP-04 lands the real implementation.
func Restore(canonical []byte, deltas []Delta) ([]byte, error) {
	return nil, core.ErrNotImplemented
}
