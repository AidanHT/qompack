package grammar

import "github.com/qompack/qompack/internal/core"

// Sequitur incrementally induces a context-free grammar over an appended Symbol stream
// (00-ARCHITECTURE.md §5.11), maintaining Sequitur's two classical invariants as it goes: no
// digram appears twice, and every rule is used more than once.
type Sequitur interface {
	// Append folds one more Symbol into the grammar.
	Append(s Symbol)
	// Rules returns every rule currently in the grammar.
	Rules() []Rule
	// Thrash returns rules whose multiplicity exceeds minUses and whose expansion length is at
	// least 2: the candidates for a loop-detection Warning.
	Thrash(minUses int) []Rule
	// Compressed returns the grammar-compressed action history for the checkpoint: the top-level
	// sequence with repeated structure folded into rule references.
	Compressed() []Symbol
	// Reset clears the grammar back to empty.
	Reset()
	MarshalBinary() ([]byte, error)
	UnmarshalBinary([]byte) error
}

// New returns a Sequitur. Constructing always succeeds, so wave-0 composition roots can wire a
// grammar.Sequitur today, but every operation is a stub until SP-15 lands the real grammar
// induction (00-ARCHITECTURE.md §5.11).
//
// New has no error return, matching every other "computational" constructor in this codebase
// (chunk.New, symbols.New, redact.New): building a Sequitur performs no I/O by itself, so there
// is nothing for a stub constructor to fail at.
func New() Sequitur {
	return stubSequitur{}
}

// stubSequitur is the SP-01 placeholder Sequitur. SP-15 owns the real grammar induction.
type stubSequitur struct{}

// Append is a no-op. Append has no return value at all — unlike a method with an error return, a
// stub can't even signal core.ErrNotImplemented here — so silently folding nothing into no state
// is the only available behaviour: no consumer can observe partial or incorrect grammar state
// through Append itself, only through Rules/Thrash/Compressed, which each report their own
// documented empty answer below.
func (stubSequitur) Append(s Symbol) {}

// Rules always returns nil (Rule 1's documented zero value for a no-error-return stub method):
// the stub has induced no rules, because Append never folded anything in.
func (stubSequitur) Rules() []Rule { return nil }

// Thrash always returns nil: with no rules induced, there is nothing to report as thrashing.
func (stubSequitur) Thrash(minUses int) []Rule { return nil }

// Compressed always returns nil: with no rules induced, there is no compressed history to return.
func (stubSequitur) Compressed() []Symbol { return nil }

// Reset is a no-op: an empty grammar reset to empty is still empty.
func (stubSequitur) Reset() {}

// MarshalBinary always reports core.ErrNotImplemented.
func (stubSequitur) MarshalBinary() ([]byte, error) { return nil, core.ErrNotImplemented }

// UnmarshalBinary always reports core.ErrNotImplemented.
func (stubSequitur) UnmarshalBinary(b []byte) error { return core.ErrNotImplemented }
